package router

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionTitleCacheScansEachSessionOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session_index.jsonl")
	content := "" +
		`{"id":"other","thread_name":"Other"}` + "\n" +
		`{"id":"session-1","thread_name":"First title"}` + "\n" +
		`{"id":"session-1","thread_name":"Latest \"title\""}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := newSessionTitleCacheAt(path)
	if got := cache.title("session-1"); got != `Latest "title"` {
		t.Fatalf("title = %q", got)
	}
	if err := os.WriteFile(path, []byte(`{"id":"session-1","thread_name":"Changed"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cache.title("session-1"); got != `Latest "title"` {
		t.Fatalf("cached title = %q", got)
	}
}

func TestSessionTitleCacheCachesMissingSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session_index.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cache := newSessionTitleCacheAt(path)
	if got := cache.title("missing"); got != "" {
		t.Fatalf("title = %q", got)
	}
	if err := os.WriteFile(path, []byte(`{"id":"missing","thread_name":"Too late"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cache.title("missing"); got != "" {
		t.Fatalf("cached missing title = %q", got)
	}
}

