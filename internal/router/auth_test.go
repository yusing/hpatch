package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCodexAuthFromCodexHome(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	path := filepath.Join(codexHome, "auth.json")
	data := `{"auth_mode":"chatgpt","tokens":{"access_token":"access-token","account_id":"account-123","future":"ignored"},"future":true}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadCodexAuth()
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "access-token" || got.AccountID != "account-123" || got.BaseURL != codexBaseURL {
		t.Fatalf("auth = %#v", got)
	}
}

func TestLoadCodexAuthRequiresSelectedFile(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	_, err := loadCodexAuth()
	if err == nil || !strings.Contains(err.Error(), filepath.Join(codexHome, "auth.json")) || !strings.Contains(err.Error(), "codex login") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadCodexAuthRejectsUnreadablePath(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.Mkdir(filepath.Join(codexHome, "auth.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := loadCodexAuth()
	if err == nil || !strings.Contains(err.Error(), "read Codex auth") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseCodexAuthRejectsMalformedIncompleteAndCollidingData(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"malformed", `{`, "parse Codex auth"},
		{"wrong mode", `{"auth_mode":"api_key"}`, "unsupported mode"},
		{"missing token", `{"auth_mode":"chatgpt","tokens":{"account_id":"account-123"}}`, "tokens.access_token"},
		{"missing account", `{"auth_mode":"chatgpt","tokens":{"access_token":"access-token"}}`, "tokens.account_id"},
		{"unrelated collision", `{"auth_mode":"chatgpt","access_token":"wrong","account_id":"wrong"}`, "tokens.access_token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCodexAuth("/tmp/auth.json", []byte(tt.data))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
