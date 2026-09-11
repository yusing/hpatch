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

// Called under the debug output lock after request shutdown. Discovery reads only
// filenames for known threads; the inspector validates each rollout's own identity.
func (d *debugOutput) writeAXReport() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	threads := slices.Sorted(maps.Keys(d.axThreads))
	paths, discoveryErr := discoverDebugRollouts(ctx, threads)
	report := struct {
		Schema         string          `json:"schema"`
		Scope          string          `json:"scope"`
		ReadLog        string          `json:"read_log"`
		DroppedThreads bool            `json:"dropped_threads"`
		Threads        []debugAXThread `json:"threads"`
	}{
		Schema:  "mekugi.ax.debug.v1",
		Scope:   "observed router threads; whole-rollout evidence available at router shutdown",
		ReadLog: d.paths[4], DroppedThreads: d.axDroppedThreads, Threads: []debugAXThread{},
	}
	for _, id := range threads {
		entry := debugAXThread{ThreadID: id, State: "rollout_unavailable"}
		if discoveryErr != nil {
			entry.State = "discovery_incomplete"
		} else if len(paths[id]) > 1 {
			entry.State = "rollout_ambiguous"
		} else if len(paths[id]) == 1 {
			var stdout, stderr bytes.Buffer
			code := RunSessionInspection(ctx, []string{
				"--session", paths[id][0], "--ax", "--read-log", d.paths[4], "--limit", "1",
			}, &stdout, &stderr)
			var inspection sessionInspection
			if code != 0 || json.Unmarshal(stdout.Bytes(), &inspection) != nil || inspection.AX == nil {
				entry.State = "inspection_failed"
			} else if inspection.AX.ThreadID != id {
				entry.State = "rollout_identity_mismatch"
			} else {
				entry.State, entry.Report = "observed", inspection.AX
			}
		}
		if entry.Report == nil {
			reads, err := capturer.ReadAXReads(ctx, d.paths[4], id)
			if err != nil {
				reads = capturer.AXReadMetrics{State: "invalid_or_unavailable", Coverage: reads.Coverage}
			}
			entry.Reads = &reads
		}
		report.Threads = append(report.Threads, entry)
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
