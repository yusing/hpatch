package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardGainStateHasNoBlockTableReference(t *testing.T) {
	if strings.Contains(string(dashboardHTML), "blocks:blocks.tbody") {
		t.Fatal("dashboard gain state references an unsupported block table")
	}
}

func TestDashboardIsSelfContainedAndProtectedByCSP(t *testing.T) {
	recorder := httptest.NewRecorder()
	serveDashboard(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if csp := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "font-src 'none'") {
		t.Fatalf("CSP = %q", csp)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "fonts.googleapis.com") || strings.Contains(body, "fonts.gstatic.com") {
		t.Fatal("dashboard contains a remote resource")
	}
	if !strings.Contains(body, "details.name.textContent=session.title||session.session_id") {
		t.Fatal("collapsed session title is missing")
	}
	if !strings.Contains(body, "By model") || strings.Contains(body, "reasoning_effort") {
		t.Fatal("dashboard retains reasoning-route telemetry")
	}
	if !strings.Contains(body, "new EventSource('/api/metrics')") {
		t.Fatal("dashboard does not subscribe to live metrics")
	}
	if strings.Contains(body, "setInterval(") || strings.Contains(body, "fetch('/api/metrics") {
		t.Fatal("dashboard still polls metrics")
	}
	if strings.Contains(body, ".innerHTML") || !strings.Contains(body, "const sessionElements=new Map()") {
		t.Fatal("dashboard replaces live content instead of reconciling stable elements")
	}
	if !strings.Contains(body, "if(!validSnapshot(data))throw") {
		t.Fatal("dashboard does not reject malformed snapshots before updating")
	}
	if !strings.Contains(body, "updateGain(") || !strings.Contains(body, "validGain(data.gain)") {
		t.Fatal("dashboard does not render gain metrics")
	}
	if !strings.Contains(body, "Durable aggregate from router metrics") {
		t.Fatal("dashboard does not identify router metrics as the gain aggregate source")
	}
	if strings.Contains(body, "hpatch gain") {
		t.Fatal("dashboard references the removed standalone gain command")
	}
	if !strings.Contains(body, `data-tab="gain"`) || !strings.Contains(body, `id="panel-gain"`) {
		t.Fatal("dashboard does not place gain in a separate tab")
	}
	if !strings.Contains(body, "--paper:#0f0f0f") {
		t.Fatal("dashboard is not dark mode")
	}
	if !strings.Contains(body, "validToolGain") || !strings.Contains(body, "g.tools") || !strings.Contains(body, "all-tools") {
		t.Fatal("dashboard does not validate and reconcile per-tool gain rows")
	}
	if !strings.Contains(body, "tableWrap('Recoveries',['Recoveries','Count'])") ||
		!strings.Contains(body, "root.gain.recoveries") || !strings.Contains(body, "g.recoveries") {
		t.Fatal("dashboard does not render per-action recovery counts")
	}
	if !strings.Contains(body, "Input token overhead estimates") || !strings.Contains(body, "Hpatch misuse warnings") ||
		!strings.Contains(body, "misuse_warning_input_tokens") || !strings.Contains(body, "validToolInput") ||
		!strings.Contains(body, "g.tool_inputs") || !strings.Contains(body, "all_tool_inputs") ||
		!strings.Contains(body, "root.gain.overhead") {
		t.Fatal("dashboard does not expose current-versus-stock input estimates and overhead")
	}
	if strings.Contains(body, "descriptive child of the installed-definition total") ||
		strings.Contains(body, "shared framing") {
		t.Fatal("dashboard input overhead retains plugin definition child rows")
	}
}

func TestDashboardRendersRequestLifecycleAndMode(t *testing.T) {
	body := string(dashboardHTML)
	for _, fragment := range []string{
		`id="requests"`, "updateRequests(", "validRequests(", "lifecycleKeys=[",
		"data.requests", "data.hpatch_calls", "usage_missing", "stream_idle_timed_out",
		"durationText(", "upstream share", "data.mode",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("dashboard does not surface the request lifecycle: missing %q", fragment)
		}
	}
	if !strings.Contains(body, "validCalls(data.hpatch_calls)") {
		t.Fatal("dashboard renders hpatch call counters without validating them")
	}
}

func TestDashboardRendersPerSessionRequestFacts(t *testing.T) {
	body := string(dashboardHTML)
	for _, fragment := range []string{"sessionFacts(", "hpatch_rejections", "setFacts(details.sessionFacts"} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("dashboard does not surface per-session request state: missing %q", fragment)
		}
	}
}

func TestDashboardFormatsSignedTokenTotals(t *testing.T) {
	body := string(dashboardHTML)
	if !strings.Contains(body, "signedText(g.net_added_input)") {
		t.Fatal("net added input is not thousands-formatted")
	}
	if strings.Contains(body, "g.net_added_input||'0'") {
		t.Fatal("net added input still renders the raw decimal string")
	}
}

func TestDashboardRendersCTPTelemetry(t *testing.T) {
	body := string(dashboardHTML)
	for _, fragment := range []string{
		`id="ctp"`, "updateCTP(", "data.ctp", "validCTP(data.ctp)",
		"CTP/1 compression", "Admission decisions", "Representation savings",
		"Dictionary and codec", "input_observations", "output_observations",
		"input_observations_dropped", "output_observations_dropped",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("dashboard does not surface CTP telemetry: missing %q", fragment)
		}
	}
	if !strings.Contains(body, `data-tab="ctp"`) || !strings.Contains(body, `id="panel-ctp"`) {
		t.Fatal("dashboard does not place CTP telemetry in a dedicated view")
	}
}

// fillRows must re-append every row in payload order. Reconciling by key alone
// let a newly reported tool or command land after the total row.
func TestDashboardReordersReconciledRows(t *testing.T) {
	body := string(dashboardHTML)
	if !strings.Contains(body, "existing.delete(entry.key);tbody.append(row)") {
		t.Fatal("dashboard does not re-append reconciled rows in payload order")
	}
	if !strings.Contains(body, "className:'total'") {
		t.Fatal("dashboard does not mark aggregate rows as totals")
	}
}

func TestDashboardRendersInstalledDefinitionTokens(t *testing.T) {
	body := string(dashboardHTML)
	for _, fragment := range []string{"Installed tool definitions", "root.gain.definitions", "g.tool_definitions", "g.shared_definition_tokens"} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("dashboard does not report installed definition cost: missing %q", fragment)
		}
	}
}

// The data marks use their own steps because the text tokens sit above the dark
// lightness band. These two are the validated warm/cool diverging pair.
func TestDashboardUsesValidatedMarkTones(t *testing.T) {
	body := string(dashboardHTML)
	for _, fragment := range []string{"--mark-cool:#499dcf", "--mark-warm:#d27a44", `data-mode="signed"`} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("dashboard does not use the validated mark tones: missing %q", fragment)
		}
	}
	if !strings.Contains(body, "apply_patch definition removed") ||
		!strings.Contains(body, "exec_command definition removed") ||
		!strings.Contains(body, "removed_exec_command_definition_input_tokens") {
		t.Fatal("dashboard does not expose separate removed-definition metrics")
	}
}

func TestDashboardRejectsUnrelatedPaths(t *testing.T) {
	recorder := httptest.NewRecorder()
	serveDashboard(recorder, httptest.NewRequest(http.MethodGet, "/future", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}
