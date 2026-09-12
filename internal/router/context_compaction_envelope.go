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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const (
	contextCompactionPrefix   = "mekugi.compaction.v1:"
	contextCompactionV2Prefix = "mekugi.compaction.v2:"
	contextCompactionIDPrefix = "cmp_mekugi_"
)

type compactionSnapshot struct {
	Items   []json.RawMessage         `json:"items"`
	Report  *compactionPressureReport `json:"pressure_report,omitempty"`
	Carried []compactionCarriedItem   `json:"carried,omitempty"`
}

// Originals are authenticated reconciliation evidence, never model input. Codex
// can carry a no-ID message or an arbitrary client-truncated version alongside
// the capsule. Retaining its original here lets us match it without resurrecting
// its omitted content. Index is a replay item or insertion point when Removed.
type compactionCarriedItem struct {
	Originals []json.RawMessage `json:"originals"`
	Index     int               `json:"index"`
	Removed   bool              `json:"removed,omitzero"`
	source    int
}

// The key is installation-owned, not session-owned: resumed and forked Codex
// histories must remain readable after a router restart. Retained history and
// client-reconciliation receipts travel encrypted; no external archive or model
// retrieval interface is used.
type contextCompactor struct {
	keyPath string
	aeadMu  sync.Mutex
	aead    cipher.AEAD
}

func (c *contextCompactor) cipher(ctx context.Context, create bool) (cipher.AEAD, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.aeadMu.Lock()
	defer c.aeadMu.Unlock()
	if c.aead != nil {
		return c.aead, nil
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
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	c.aead = aead
	return aead, nil
}

func (c *contextCompactor) seal(ctx context.Context, items []json.RawMessage) (json.RawMessage, error) {
	return c.sealSnapshot(ctx, compactionSnapshot{Items: items})
}

func (c *contextCompactor) sealSnapshot(ctx context.Context, snapshot compactionSnapshot) (json.RawMessage, error) {
	prefix := contextCompactionPrefix
	var payload any = snapshot.Items
	if len(snapshot.Carried) > 0 || snapshot.Report != nil {
		prefix, payload = contextCompactionV2Prefix, snapshot
	}
	plaintext, err := marshalProtocolJSON(payload)
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
	encoded := prefix + base64.RawStdEncoding.EncodeToString(aead.Seal(nil, nil, compressed.Bytes(), []byte(prefix)))
	digest := sha256.Sum256([]byte(encoded))
	return marshalProtocolJSON(map[string]any{
		"type":              "compaction",
		"id":                fmt.Sprintf("%s%x", contextCompactionIDPrefix, digest[:16]),
		"encrypted_content": encoded,
	})
}

func (c *contextCompactor) open(ctx context.Context, raw json.RawMessage) ([]json.RawMessage, bool, error) {
	snapshot, local, err := c.openSnapshot(ctx, raw)
	return snapshot.Items, local, err
}

func (c *contextCompactor) openSnapshot(ctx context.Context, raw json.RawMessage) (compactionSnapshot, bool, error) {
	var snapshot compactionSnapshot
	var item struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Content string `json:"encrypted_content"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return snapshot, false, nil
	}
	item.Type = jsonString(fields, "type")
	item.ID = jsonString(fields, "id")
	item.Content = jsonString(fields, "encrypted_content")
	local := strings.HasPrefix(item.Content, "mekugi.compaction.") || strings.HasPrefix(item.ID, contextCompactionIDPrefix)
	if !local {
		return snapshot, false, nil
	}
	prefix := contextCompactionPrefix
	if strings.HasPrefix(item.Content, contextCompactionV2Prefix) {
		prefix = contextCompactionV2Prefix
	}
	if item.Type != "compaction" || !strings.HasPrefix(item.Content, prefix) {
		return snapshot, true, errors.New("unsupported or damaged mekugi compaction envelope")
	}
	encrypted, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(item.Content, prefix))
	if err != nil {
		return snapshot, true, errors.New("invalid mekugi compaction envelope encoding")
	}
	aead, err := c.cipher(ctx, false)
	if err != nil {
		return snapshot, true, err
	}
	compressed, err := aead.Open(nil, nil, encrypted, []byte(prefix))
	if err != nil {
		return snapshot, true, errors.New("mekugi compaction envelope authentication failed")
	}
	decompressor, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return snapshot, true, errors.New("invalid mekugi compaction envelope payload")
	}
	defer decompressor.Close()
	plaintext, err := io.ReadAll(io.LimitReader(decompressor, responsesRequestBufferBytes+1))
	if err != nil || len(plaintext) > responsesRequestBufferBytes {
		return snapshot, true, errors.New("mekugi compaction envelope exceeds the router buffer budget or is damaged")
	}
	if err := ctx.Err(); err != nil {
		return snapshot, true, err
	}
	if prefix == contextCompactionPrefix {
		err = json.Unmarshal(plaintext, &snapshot.Items)
	} else {
		err = json.Unmarshal(plaintext, &snapshot)
	}
	if err != nil || len(snapshot.Items) == 0 {
		return compactionSnapshot{}, true, errors.New("invalid retained mekugi compaction history")
	}
	for _, carried := range snapshot.Carried {
		if len(carried.Originals) == 0 || slices.ContainsFunc(carried.Originals, func(raw json.RawMessage) bool { return !compactionCarriedMessage(raw) }) || carried.Index < 0 || carried.Index > len(snapshot.Items) ||
			(!carried.Removed && carried.Index == len(snapshot.Items)) {
			return compactionSnapshot{}, true, errors.New("invalid compaction carried-message receipt")
		}
	}
	return snapshot, true, nil
}
