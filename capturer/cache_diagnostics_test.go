package capturer

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func diagnosticRecorder(t *testing.T) *Recorder {
	t.Helper()
	r, err := New(Config{Mode: "hpatch", ModelProtocol: "native"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestCacheFingerprintsArePrivateAndFramingIndependent(t *testing.T) {
	r := diagnosticRecorder(t)
	a := r.requestFingerprint([]byte(`{"model":"model","instructions":"private <instructions>","prompt_cache_key":"private key","input":[{"content":"private <text>","n":1000}]}`))
	b := r.requestFingerprint([]byte(` { "input" : [ { "n":1e3, "content":"private \u003ctext\u003e" } ], "prompt_cache_key":"private key", "instructions":"private \u003cinstructions\u003e", "model":"model" } `))
	if got := comparePrefix(a, b); got.Status != "identical" {
		t.Fatalf("framing changed fingerprint: %+v", got)
	}
	if a.RequestKey != b.RequestKey {
		t.Fatal("stable request cache key changed")
	}
	encoded, _ := json.Marshal(a)
	for _, secret := range []string{"private", "<text>", "<instructions>"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("raw content retained in fingerprint")
		}
	}
	other := diagnosticRecorder(t).requestFingerprint([]byte(`{"model":"model","input":[]}`))
	if got := comparePrefix(a, other); got.Status != "unavailable" {
		t.Fatal("fingerprints compared across recorder lifetimes")
	}
	clone := cloneFingerprint(a)
	clone.Items[0] = "mutated"
	clone.Fields["model"] = "mutated"
	if a.Items[0] == clone.Items[0] || a.Fields["model"] == clone.Fields["model"] {
		t.Fatal("snapshot aliases recorder fingerprint state")
	}
}

func TestCacheFingerprintLocatesChangesAndBoundsHistory(t *testing.T) {
	r := diagnosticRecorder(t)
	base := r.requestFingerprint([]byte(`{"model":"model","input":["one","two"]}`))
	appended := r.requestFingerprint([]byte(`{"input":["one","two","three"],"model":"model"}`))
	if got := comparePrefix(base, appended); got.Status != "appended" || got.CommonItems != 2 {
		t.Fatalf("append: %+v", got)
	}
	changed := r.requestFingerprint([]byte(`{"model":"different","input":["one","changed"]}`))
	if got := comparePrefix(base, changed); got.Status != "changed" || got.CommonItems != 1 || len(got.ChangedFields) != 1 || got.ChangedFields[0] != "model" {
		t.Fatalf("change: %+v", got)
	}
	shortened := r.requestFingerprint([]byte(`{"model":"model","input":["one"]}`))
	if got := comparePrefix(base, shortened); got.Status != "changed" {
		t.Fatalf("removed history: %+v", got)
	}
	items := make([]string, maxFingerprintItems+1)
	encoded, _ := json.Marshal(map[string]any{"input": items})
	truncated := r.requestFingerprint(encoded)
	if truncated.Complete || len(truncated.Items) != maxFingerprintItems || truncated.ItemCount != len(items) {
		t.Fatal("unbounded fingerprint history")
	}
	if comparePrefix(truncated, truncated).Status != "unavailable" {
		t.Fatal("partial fingerprint claimed a stable prefix")
	}
}

func TestCacheDiagnosisUsesArrivalOrderThreadAndFinalAttempt(t *testing.T) {
	r := diagnosticRecorder(t)
	first := r.requestFingerprint([]byte(`{"model":"model","input":["one"]}`))
	first.RoutingKey = "route-a"
	second := r.requestFingerprint([]byte(`{"model":"model","input":["one","two"]}`))
	second.RoutingKey = "route-b"
	exchanges := []exchangeMetrics{
		{Sequence: 2, PredecessorSequence: 1, ThreadID: "thread", Status: "completed", ClientFingerprint: second, ProviderAttempts: []providerAttemptMetrics{{Fingerprint: first}, {Fingerprint: second, NativeFingerprint: second}}},
		{Sequence: 1, ThreadID: "thread", Status: "completed", ClientFingerprint: first, ProviderAttempts: []providerAttemptMetrics{{Fingerprint: first, NativeFingerprint: first}}},
		{Sequence: 3, ThreadID: "other", Status: "completed", ClientFingerprint: second, ProviderAttempts: []providerAttemptMetrics{{Fingerprint: second, NativeFingerprint: second}}},
	}
	diagnoseCacheExchanges(exchanges)
	got := exchanges[0].CacheDiagnosis
	if got.PreviousSequence != 1 || got.Provider.Status != "appended" || got.Native.Status != "appended" || got.Routing != "changed" {
		t.Fatalf("diagnosis: %+v", got)
	}
	if exchanges[2].CacheDiagnosis.PreviousSequence != 0 || exchanges[2].CacheDiagnosis.Provider.Status != "unavailable" {
		t.Fatal("unrelated thread became cache predecessor")
	}
}

func TestCacheDiagnosisDoesNotSkipPendingPredecessor(t *testing.T) {
	r := diagnosticRecorder(t)
	fp := r.requestFingerprint([]byte(`{"input":["stable"]}`))
	exchanges := []exchangeMetrics{
		{Sequence: 1, ThreadID: "thread", Status: "completed", ProviderAttempts: []providerAttemptMetrics{{Fingerprint: fp}}},
		{Sequence: 3, PredecessorSequence: 2, ThreadID: "thread", Status: "completed", ProviderAttempts: []providerAttemptMetrics{{Fingerprint: fp}}},
	}
	diagnoseCacheExchanges(exchanges)
	if got := exchanges[1].CacheDiagnosis; got.PreviousSequence != 0 || got.Provider.Status != "unavailable" {
		t.Fatalf("skipped pending predecessor: %+v", got)
	}
}

func TestCacheDiagnosisDoesNotBridgeEvictedFailedPredecessor(t *testing.T) {
	r := diagnosticRecorder(t)
	header := http.Header{"Thread-Id": []string{"thread"}}
	first, _ := r.beginRequest(header)
	failed, _ := r.beginRequest(header)
	third, _ := r.beginRequest(header)
	if failed.predecessorSequence != first.sequence || third.predecessorSequence != failed.sequence {
		t.Fatal("arrival predecessor was not retained")
	}
	fp := r.requestFingerprint([]byte(`{"input":["stable"]}`))
	// The failed middle completion has left the bounded detail window before
	// the first and third requests complete. Its identity must still block reuse.
	exchanges := []exchangeMetrics{
		{Sequence: third.sequence, PredecessorSequence: third.predecessorSequence, ThreadID: "thread", Status: "completed", ProviderAttempts: []providerAttemptMetrics{{Fingerprint: fp}}},
		{Sequence: first.sequence, ThreadID: "thread", Status: "completed", ProviderAttempts: []providerAttemptMetrics{{Fingerprint: fp}}},
	}
	diagnoseCacheExchanges(exchanges)
	if got := exchanges[0].CacheDiagnosis; got.PreviousSequence != 0 || got.Provider.Status != "unavailable" {
		t.Fatalf("bridged evicted failed predecessor: %+v", got)
	}
}
