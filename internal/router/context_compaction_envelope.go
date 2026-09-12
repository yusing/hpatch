package router

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const (
	contextCompactionPrefix   = "mekugi.compaction.v1:"
	contextCompactionIDPrefix = "cmp_mekugi_"
)

// The key is installation-owned, not session-owned: resumed and forked Codex
// histories must remain readable after a router restart. Only encrypted retained
// history travels in the envelope; no transcript archive or retrieval is used.
type contextCompactor struct {
	keyPath string
}

func (c *contextCompactor) cipher(ctx context.Context, create bool) (cipher.AEAD, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.keyPath == "" {
		return nil, errors.New("mekugi compaction key path is not configured")
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(c.keyPath), 0o700); err != nil {
			return nil, fmt.Errorf("create compaction key directory: %w", err)
		}
	}
	lock := flock.New(c.keyPath + ".lock")
	defer lock.Close()
	locked, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !locked {
		return nil, errors.Join(ctx.Err(), err, errors.New("could not acquire compaction key lock"))
	}
	key, err := os.ReadFile(c.keyPath)
	if errors.Is(err, os.ErrNotExist) && create {
		key = make([]byte, 32)
		rand.Read(key)
		file, createErr := os.OpenFile(c.keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return nil, fmt.Errorf("create compaction key: %w", createErr)
		}
		_, writeErr := file.Write(key)
		err = errors.Join(writeErr, file.Sync(), file.Close())
	}
	if err != nil {
		return nil, fmt.Errorf("read compaction key (required to resume compacted histories): %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("invalid compaction key; restore the original key before resuming compacted histories")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (c *contextCompactor) seal(ctx context.Context, items []json.RawMessage) (json.RawMessage, error) {
	plaintext, err := marshalProtocolJSON(items)
	if err != nil {
		return nil, err
	}
	if len(plaintext) > responsesRequestBufferBytes {
		return nil, errors.New("retained compaction history exceeds the router buffer budget")
	}
	var compressed bytes.Buffer
	compressor := zlib.NewWriter(&compressed)
	if _, err := compressor.Write(plaintext); err != nil {
		return nil, err
	}
	if err := compressor.Close(); err != nil {
		return nil, err
	}
	aead, err := c.cipher(ctx, true)
	if err != nil {
		return nil, err
	}
	encoded := contextCompactionPrefix + base64.RawStdEncoding.EncodeToString(aead.Seal(nil, nil, compressed.Bytes(), []byte(contextCompactionPrefix)))
	digest := sha256.Sum256([]byte(encoded))
	return marshalProtocolJSON(map[string]any{
		"type":              "compaction",
		"id":                fmt.Sprintf("%s%x", contextCompactionIDPrefix, digest[:16]),
		"encrypted_content": encoded,
	})
}

func (c *contextCompactor) open(ctx context.Context, raw json.RawMessage) ([]json.RawMessage, bool, error) {
	var item struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Content string `json:"encrypted_content"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, false, nil
	}
	item.Type = jsonString(fields, "type")
	item.ID = jsonString(fields, "id")
	item.Content = jsonString(fields, "encrypted_content")
	local := strings.HasPrefix(item.Content, "mekugi.compaction.") || strings.HasPrefix(item.ID, contextCompactionIDPrefix)
	if !local {
		return nil, false, nil
	}
	if item.Type != "compaction" || !strings.HasPrefix(item.Content, contextCompactionPrefix) {
		return nil, true, errors.New("unsupported or damaged mekugi compaction envelope")
	}
	encrypted, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(item.Content, contextCompactionPrefix))
	if err != nil {
		return nil, true, errors.New("invalid mekugi compaction envelope encoding")
	}
	aead, err := c.cipher(ctx, false)
	if err != nil {
		return nil, true, err
	}
	compressed, err := aead.Open(nil, nil, encrypted, []byte(contextCompactionPrefix))
	if err != nil {
		return nil, true, errors.New("mekugi compaction envelope authentication failed")
	}
	decompressor, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, true, errors.New("invalid mekugi compaction envelope payload")
	}
	defer decompressor.Close()
	plaintext, err := io.ReadAll(io.LimitReader(decompressor, responsesRequestBufferBytes+1))
	if err != nil || len(plaintext) > responsesRequestBufferBytes {
		return nil, true, errors.New("mekugi compaction envelope exceeds the router buffer budget or is damaged")
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	var items []json.RawMessage
	if json.Unmarshal(plaintext, &items) != nil || len(items) == 0 {
		return nil, true, errors.New("invalid retained mekugi compaction history")
	}
	return items, true, nil
}
