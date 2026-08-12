package router

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type cachedSessionTitle struct {
	once  sync.Once
	title string
}

type sessionTitleCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]*cachedSessionTitle
}

func newSessionTitleCache() *sessionTitleCache {
	return newSessionTitleCacheAt(codexSessionIndexPath())
}

func newSessionTitleCacheAt(path string) *sessionTitleCache {
	return &sessionTitleCache{path: path, entries: make(map[string]*cachedSessionTitle)}
}

func (c *sessionTitleCache) title(sessionID string) string {
	if c == nil || sessionID == "" {
		return ""
	}
	c.mu.Lock()
	entry := c.entries[sessionID]
	if entry == nil {
		entry = &cachedSessionTitle{}
		c.entries[sessionID] = entry
	}
	c.mu.Unlock()
	entry.once.Do(func() {
		entry.title = scanSessionTitle(c.path, sessionID)
	})
	return entry.title
}

func codexSessionIndexPath() string {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		codexHome = filepath.Join(home, ".codex")
	}
	return filepath.Join(codexHome, "session_index.jsonl")
}

func scanSessionTitle(path, sessionID string) string {
	if path == "" {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	idNeedle := `"id":` + strconv.Quote(sessionID)
	var title string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, idNeedle) {
			continue
		}
		if candidate, ok := jsonStringField(line, "thread_name"); ok && candidate != "" {
			title = candidate
		}
	}
	return title
}

func jsonStringField(line, name string) (string, bool) {
	key := strconv.Quote(name)
	start := strings.Index(line, key)
	if start < 0 {
		return "", false
	}
	remaining := strings.TrimLeft(line[start+len(key):], " \t")
	if !strings.HasPrefix(remaining, ":") {
		return "", false
	}
	remaining = strings.TrimLeft(remaining[1:], " \t")
	if !strings.HasPrefix(remaining, `"`) {
		return "", false
	}
	escaped := false
	for end := 1; end < len(remaining); end++ {
		switch {
		case escaped:
			escaped = false
		case remaining[end] == '\\':
			escaped = true
		case remaining[end] == '"':
			quoted := strings.ReplaceAll(remaining[:end+1], `\/`, `/`)
			value, err := strconv.Unquote(quoted)
			return value, err == nil
		}
	}
	return "", false
}
