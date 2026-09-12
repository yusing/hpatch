package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofrs/flock"
)

const (
	maxJournalThreads   = 256
	maxJournalItems     = 256
	maxJournalItemBytes = 16 << 10
	// Terminal delivery is independent of the live progress budget. The extra
	// space covers item labels and the bounded canonical agent name.
	maxJournalFlushBytes = maxJournalItems*(maxJournalItemBytes+64) + maxJournalItemBytes
	maxJournalReceipts   = 16384
)

var errJournalThreadCapacity = errors.New("journal thread capacity reached")

type journalMutation struct {
	Op        string  `json:"op"`
	ID        string  `json:"id,omitempty"`
	Text      *string `json:"text,omitempty"`
	ReportNow bool    `json:"report_now,omitzero"`
}

type journalItem struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Author    string `json:"author"`
	Created   uint64 `json:"created"`
	Updated   uint64 `json:"updated"`
	ReportNow bool   `json:"report_now"`
	Reported  bool   `json:"reported"`
	Flushed   bool   `json:"flushed"`
	// A later silent edit does not erase the fact that the user saw this ID.
	EverReported bool `json:"ever_reported,omitzero"`
}

type journalReceipt struct {
	Digest string   `json:"digest"`
	IDs    []string `json:"ids"`
}

type journalRetraction struct {
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
}

type threadJournal struct {
	Version     int                       `json:"version"`
	Workspace   string                    `json:"workspace"`
	Thread      string                    `json:"thread"`
	Author      string                    `json:"author"`
	Sequence    uint64                    `json:"sequence"`
	NextID      uint64                    `json:"next_id"`
	Items       []journalItem             `json:"items"`
	Retractions []journalRetraction       `json:"retractions,omitempty"`
	Receipts    map[string]journalReceipt `json:"receipts"`
}

func (j threadJournal) clone() threadJournal {
	j.Retractions = slices.Clone(j.Retractions)
	j.Items = slices.Clone(j.Items)
	j.Receipts = maps.Clone(j.Receipts)
	return j
}

// journalStore owns mutable journal state, separately from immutable tool replay.
// The replay store's filesystem lock serializes journal transactions across router
// processes too; journal files never count against or evict executable history.
type journalStore struct {
	deliveryGate chan struct{}
	mu           sync.Mutex
	memory       map[string]threadJournal
}

func newJournalStore() *journalStore {
	return &journalStore{memory: make(map[string]threadJournal), deliveryGate: make(chan struct{}, 1)}
}

// Serialize mutations with in-flight delivery. The transport holds this lease
// from its snapshot through write confirmation, so deletion cannot overtake a
// notice and lose the required retraction. Waiting remains request-cancellable.
func (s *journalStore) lockDelivery(ctx context.Context, store *mekugiReplayStore) (func(), error) {
	select {
	case s.deliveryGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-s.deliveryGate }
	if store == nil {
		return release, nil
	}
	// Keep a separate lock from replay transactions: delivery must be able to
	// retain provenance and acknowledge revisions while excluding mutations
	// from other router processes.
	path := filepath.Join(store.directory, "journal-delivery.lock")
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		release()
		return nil, errors.New("journal delivery lock is not a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		release()
		return nil, err
	}
	lock := flock.New(path, flock.SetPermissions(0600))
	ok, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !ok {
		release()
		if err == nil {
			err = ctx.Err()
		}
		return nil, err
	}
	return func() {
		_ = lock.Unlock()
		release()
	}, nil
}

func journalKey(workspace, thread string) string { return workspace + "\x00" + thread }

func journalFilename(workspace, thread string) string {
	return fmt.Sprintf("journal-%x.json", sha256.Sum256([]byte(journalKey(workspace, thread))))
}

func readThreadJournal(store *mekugiReplayStore, workspace, thread string) (threadJournal, bool, error) {
	path := filepath.Join(store.directory, journalFilename(workspace, thread))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return threadJournal{}, false, nil
	}
	if err != nil {
		return threadJournal{}, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxReplayRecordBytes {
		return threadJournal{}, false, errors.New("invalid journal record")
	}
	file, err := os.Open(path)
	if err != nil {
		return threadJournal{}, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxReplayRecordBytes+1))
	if err != nil {
		return threadJournal{}, false, err
	}
	var journal threadJournal
	if len(data) > maxReplayRecordBytes || json.Unmarshal(data, &journal) != nil ||
		journal.Version != 1 || journal.Workspace != workspace || journal.Thread != thread ||
		len(journal.Items) > maxJournalItems || journal.Receipts == nil || len(journal.Receipts) > maxJournalReceipts {
		return threadJournal{}, false, errors.New("corrupt journal record")
	}
	return journal, true, nil
}

func writeThreadJournal(store *mekugiReplayStore, journal threadJournal) error {
	data, err := marshalProtocolJSON(journal)
	if err != nil {
		return err
	}
	if len(data) > maxReplayRecordBytes {
		return errors.New("journal record capacity reached")
	}
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		return err
	}
	name := journalFilename(journal.Workspace, journal.Thread)
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "journal-") && strings.HasSuffix(entry.Name(), ".json") && entry.Name() != name {
			count++
		}
	}
	if count >= maxJournalThreads {
		return errJournalThreadCapacity
	}
	file, err := os.CreateTemp(store.directory, "journal-pending-")
	if err != nil {
		return err
	}
	defer func() { file.Close(); os.Remove(file.Name()) }()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), filepath.Join(store.directory, name)); err != nil {
		return err
	}
	return syncReplayDirectory(store.directory)
}

func (s *journalStore) transaction(ctx context.Context, store *mekugiReplayStore, workspace, thread string, mutate func(*threadJournal, bool) error) error {
	if strings.TrimSpace(thread) == "" {
		return errors.New("journal requires a stable thread ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run := func() error {
		key := journalKey(workspace, thread)
		current, exists := s.memory[key]
		if store != nil {
			var err error
			current, exists, err = readThreadJournal(store, workspace, thread)
			if err != nil {
				return err
			}
		}
		next := current.clone()
		if err := mutate(&next, exists); err != nil {
			return err
		}
		if !exists && len(s.memory) >= maxJournalThreads {
			return errJournalThreadCapacity
		}
		if store != nil {
			if err := writeThreadJournal(store, next); err != nil {
				return err
			}
		}
		s.memory[key] = next
		return nil
	}
	if store != nil {
		return store.locked(ctx, run)
	}
	return run()
}

// Initialize ordinary forks exactly once from the source's latest committed
// journal. Subagent starts do not import their parent's journal.
func (s *journalStore) initialize(ctx context.Context, store *mekugiReplayStore, workspace, thread, author, fork string) error {
	return s.transaction(ctx, store, workspace, thread, func(j *threadJournal, exists bool) error {
		if exists {
			return nil
		}
		*j = threadJournal{Version: 1, Workspace: workspace, Thread: thread, Author: author, Items: []journalItem{}, Receipts: make(map[string]journalReceipt)}
		if fork == "" {
			return nil
		}
		if fork == thread {
			return errors.New("journal fork cannot refer to itself")
		}
		source, ok := s.memory[journalKey(workspace, fork)]
		if store != nil {
			var err error
			source, ok, err = readThreadJournal(store, workspace, fork)
			if err != nil {
				return err
			}
		}
		if !ok {
			return nil
		}
		j.Items = slices.Clone(source.Items)
		j.Sequence, j.NextID = source.Sequence, source.NextID
		return nil
	})
}

func decodeJournalMutations(raw []byte) ([]journalMutation, error) {
	var mutations []journalMutation
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&mutations); err != nil || mutations == nil || len(mutations) > maxJournalItems {
		return nil, errors.New("journal must be an array of at most 256 mutations")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid journal payload")
	}
	return mutations, nil
}

func (s *journalStore) apply(ctx context.Context, store *mekugiReplayStore, workspace, thread, receiptID string, mutations []journalMutation) ([]string, error) {
	release, err := s.lockDelivery(ctx, store)
	if err != nil {
		return nil, err
	}
	defer release()
	encoded, err := marshalProtocolJSON(mutations)
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(encoded))
	var ids []string
	err = s.transaction(ctx, store, workspace, thread, func(j *threadJournal, exists bool) error {
		if !exists {
			return errors.New("journal is not initialized")
		}
		if receipt, ok := j.Receipts[receiptID]; receiptID != "" && ok {
			if receipt.Digest != digest {
				return errors.New("journal call changed its mutations")
			}
			ids = slices.Clone(receipt.IDs)
			return nil
		}
		if receiptID != "" && len(j.Receipts) >= maxJournalReceipts {
			return errors.New("journal receipt capacity reached")
		}
		for _, mutation := range mutations {
			index := slices.IndexFunc(j.Items, func(item journalItem) bool { return item.ID == mutation.ID })
			switch mutation.Op {
			case "add", "edit":
				if mutation.Text == nil || strings.TrimSpace(*mutation.Text) == "" || !utf8.ValidString(*mutation.Text) ||
					len(*mutation.Text) > maxJournalItemBytes {
					return errors.New("journal text must be nonblank UTF-8 and at most 16 KiB")
				}
				if mutation.Op == "add" && (mutation.ID != "" || len(j.Items) >= maxJournalItems) {
					return errors.New("journal add requires no ID and available item capacity")
				}
				if mutation.Op == "edit" && index < 0 {
					return errors.New("journal item not found")
				}
			case "delete":
				if index < 0 {
					return errors.New("journal item not found")
				}
				if mutation.Text != nil {
					return errors.New("journal delete does not accept text")
				}
			default:
				return errors.New("journal op must be add, edit, or delete")
			}
			if j.Sequence == ^uint64(0) || mutation.Op == "add" && j.NextID == ^uint64(0) {
				return errors.New("journal sequence exhausted")
			}
			j.Sequence++
			switch mutation.Op {
			case "add":
				j.NextID++
				item := journalItem{ID: fmt.Sprintf("j%d", j.NextID), Text: *mutation.Text, Author: j.Author, Created: j.Sequence, Updated: j.Sequence, ReportNow: mutation.ReportNow}
				j.Items = append(j.Items, item)
				ids = append(ids, item.ID)
			case "edit":
				j.Items[index].Text = *mutation.Text
				j.Items[index].Updated = j.Sequence
				j.Items[index].ReportNow = mutation.ReportNow
				j.Items[index].Reported = false
				j.Items[index].Flushed = false
				ids = append(ids, mutation.ID)
			case "delete":
				if mutation.ReportNow && j.Items[index].EverReported {
					if len(j.Retractions) >= maxJournalItems {
						return errors.New("journal retraction capacity reached")
					}
					j.Retractions = append(j.Retractions, journalRetraction{ID: mutation.ID, Sequence: j.Sequence})
				}
				j.Items = slices.Delete(j.Items, index, index+1)
				ids = append(ids, mutation.ID)
			}
		}
		if receiptID != "" {
			j.Receipts[receiptID] = journalReceipt{Digest: digest, IDs: slices.Clone(ids)}
		}
		return nil
	})
	return ids, err
}

func (s *journalStore) list(ctx context.Context, store *mekugiReplayStore, workspace, thread string) ([]journalItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []journalItem
	read := func() error {
		j, exists := s.memory[journalKey(workspace, thread)]
		if store != nil {
			var err error
			j, exists, err = readThreadJournal(store, workspace, thread)
			if err != nil {
				return err
			}
		}
		if !exists {
			return errors.New("journal is not initialized")
		}
		items = slices.Clone(j.Items)
		return nil
	}
	if store != nil {
		err := store.locked(ctx, read)
		return items, err
	}
	err := read()
	return items, err
}

// Delivery acknowledgements refer to the exact revision rendered. An edit
// arriving while a message is being written must remain eligible for delivery.
func (s *journalStore) acknowledge(ctx context.Context, store *mekugiReplayStore, workspace, thread string, revisions map[string]uint64, terminal bool) error {
	return s.transaction(ctx, store, workspace, thread, func(j *threadJournal, exists bool) error {
		if !exists {
			return errors.New("journal is not initialized")
		}
		for index := range j.Items {
			if revision, ok := revisions[j.Items[index].ID]; ok {
				j.Items[index].EverReported = true
				if revision == j.Items[index].Updated {
					j.Items[index].Reported = true
					if terminal {
						j.Items[index].Flushed = true
					}
				}
			}
		}
		j.Retractions = slices.DeleteFunc(j.Retractions, func(retraction journalRetraction) bool {
			return revisions[retraction.ID] == retraction.Sequence
		})
		return nil
	})
}
