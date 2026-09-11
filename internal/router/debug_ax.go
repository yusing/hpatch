package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yusing/mekugi/capturer"
)

type debugAXThread struct {
	Reads    *capturer.AXReadMetrics `json:"reads,omitempty"`
	ThreadID string                  `json:"thread_id"`
	State    string                  `json:"state"`
	Report   *sessionAXReport        `json:"report,omitempty"`
}

func (d *debugOutput) observeAXThread(id string) {
	if d == nil || id == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.axThreads) == 256 && !d.axThreads[id] {
		d.axDroppedThreads = true
		return
	}
	d.axThreads[id] = true
}

// Called under the debug output lock after request shutdown. Journal-only
// identities remain separate from known router threads; they are not presumed
// to be children or tests. Every supplied event is validated, including exclusions.
func (d *debugOutput) writeAXReport() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	journal, journalErr := capturer.ReadAXReadJournal(ctx, d.paths[4])
	threads := slices.Sorted(maps.Keys(d.axThreads))
	var journalOnly []string
	for id := range journal.Threads {
		if id != "" && !d.axThreads[id] {
			journalOnly = append(journalOnly, id)
		}
	}
	slices.Sort(journalOnly)
	dropped := len(journalOnly) > 256
	journalOnly = journalOnly[:min(len(journalOnly), 256)]
	paths, discoveryErr := discoverDebugRollouts(ctx, append(slices.Clone(threads), journalOnly...))
	report := struct {
		Schema                string                  `json:"schema"`
		Scope                 string                  `json:"scope"`
		ReadLog               string                  `json:"read_log"`
		JournalState          string                  `json:"journal_state"`
		DroppedThreads        bool                    `json:"dropped_threads"`
		Threads               []debugAXThread         `json:"threads"`
		JournalOnlyThreads    []debugAXThread         `json:"journal_only_threads"`
		DroppedJournalThreads bool                    `json:"dropped_journal_threads"`
		UnattributedReads     *capturer.AXReadMetrics `json:"unattributed_reads,omitempty"`
	}{
		Schema: "mekugi.ax.debug.v1", Scope: "known router threads and separately labeled journal-only evidence; whole-rollout evidence at shutdown",
		ReadLog: d.paths[4], JournalState: "observed", DroppedThreads: d.axDroppedThreads,
		Threads: []debugAXThread{}, JournalOnlyThreads: []debugAXThread{}, DroppedJournalThreads: dropped,
	}
	if journalErr != nil {
		report.JournalState = "invalid_or_unavailable"
	}
	if reads, ok := journal.Threads[""]; ok {
		report.UnattributedReads = &reads
	}
	inspect := func(id string) debugAXThread {
		entry := debugAXThread{ThreadID: id, State: "rollout_unavailable"}
		reads := journal.ForThread(id)
		if journalErr != nil {
			reads.State = "invalid_or_unavailable"
		}
		if discoveryErr != nil {
			entry.State = "discovery_incomplete"
		} else if len(paths[id]) > 1 {
			entry.State = "rollout_ambiguous"
		} else if len(paths[id]) == 1 {
			var stdout, stderr bytes.Buffer
			// Reuse the one validated journal snapshot rather than rescanning it
			// for every thread (or accepting partial evidence on a later timeout).
			code := RunSessionInspection(ctx, []string{"--session", paths[id][0], "--ax", "--limit", "1"}, &stdout, &stderr)
			var inspection sessionInspection
			if code != 0 || json.Unmarshal(stdout.Bytes(), &inspection) != nil || inspection.AX == nil {
				entry.State = "inspection_failed"
			} else if inspection.AX.ThreadID != id {
				entry.State = "rollout_identity_mismatch"
			} else {
				entry.State, entry.Report = "observed", inspection.AX
				entry.Report.Reads = reads
			}
		}
		if entry.Report == nil {
			entry.Reads = &reads
		}
		return entry
	}
	for _, id := range threads {
		report.Threads = append(report.Threads, inspect(id))
	}
	for _, id := range journalOnly {
		report.JournalOnlyThreads = append(report.JournalOnlyThreads, inspect(id))
	}
	file, err := os.OpenFile(d.paths[5], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return errors.Join(json.NewEncoder(file).Encode(report), file.Close())
}

func discoverDebugRollouts(ctx context.Context, threads []string) (map[string][]string, error) {
	found := make(map[string][]string)
	if len(threads) == 0 {
		return found, nil
	}
	codexDirectory := os.Getenv("CODEX_HOME")
	if codexDirectory == "" {
		userDirectory, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		codexDirectory = filepath.Join(userDirectory, ".codex")
	}
	visited := 0
	for _, name := range []string{"sessions", "archived_sessions"} {
		directory := filepath.Join(codexDirectory, name)
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				if path == directory && errors.Is(walkErr, os.ErrNotExist) {
					return nil
				}
				return walkErr
			}
			visited++
			if visited > 100000 {
				return errors.New("rollout discovery exceeds entry bound")
			}
			if entry.Type().IsRegular() {
				for _, id := range threads {
					if strings.HasSuffix(entry.Name(), "-"+id+".jsonl") {
						found[id] = append(found[id], path)
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}
