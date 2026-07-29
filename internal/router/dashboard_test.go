package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	if !strings.Contains(body, "details.route.textContent=session.model") {
		t.Fatal("collapsed session model is missing")
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
	if !strings.Contains(body, "hpatch gain") {
		t.Fatal("dashboard does not identify the gain aggregate source")
	}
	if !strings.Contains(body, `data-tab="gain"`) || !strings.Contains(body, `id="panel-gain"`) {
		t.Fatal("dashboard does not place gain in a separate tab")
	}
	if !strings.Contains(body, "--paper:#0f0f0f") {
		t.Fatal("dashboard is not dark mode")
	}
}

func TestDashboardRejectsUnrelatedPaths(t *testing.T) {
	recorder := httptest.NewRecorder()
	serveDashboard(recorder, httptest.NewRequest(http.MethodGet, "/future", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}
