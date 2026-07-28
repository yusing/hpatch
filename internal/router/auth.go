package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	authModeChatGPT = "chatgpt"
	codexBaseURL    = "https://chatgpt.com/backend-api/codex"
)

type authConfig struct {
	Token     string
	AccountID string
	BaseURL   string
}

type codexAuthFile struct {
	AuthMode string `json:"auth_mode"`
	Tokens   *struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func loadCodexAuth() (authConfig, error) {
	path, err := codexAuthPath()
	if err != nil {
		return authConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return authConfig{}, fmt.Errorf("codex auth not found at %s; run `codex login` with file credential storage", path)
		}
		return authConfig{}, fmt.Errorf("read Codex auth %s: %w", path, err)
	}
	return parseCodexAuth(path, data)
}

func codexAuthPath() (string, error) {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate Codex auth: %w", err)
	}
	return filepath.Join(home, ".codex", "auth.json"), nil
}

func parseCodexAuth(path string, data []byte) (authConfig, error) {
	var file codexAuthFile
	if err := json.Unmarshal(data, &file); err != nil {
		return authConfig{}, fmt.Errorf("parse Codex auth %s: %w", path, err)
	}
	if file.AuthMode != authModeChatGPT {
		return authConfig{}, fmt.Errorf("codex auth %s uses unsupported mode %q; run `codex login` with ChatGPT authentication", path, file.AuthMode)
	}
	if file.Tokens == nil || strings.TrimSpace(file.Tokens.AccessToken) == "" {
		return authConfig{}, fmt.Errorf("codex auth %s is missing tokens.access_token", path)
	}
	if strings.TrimSpace(file.Tokens.AccountID) == "" {
		return authConfig{}, fmt.Errorf("codex auth %s is missing tokens.account_id", path)
	}
	return authConfig{
		Token:     file.Tokens.AccessToken,
		AccountID: file.Tokens.AccountID,
		BaseURL:   codexBaseURL,
	}, nil
}
