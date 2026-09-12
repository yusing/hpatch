package router

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
	"github.com/yusing/mekugi"
	"github.com/yusing/mekugi/internal/shellruntime"
)

func TestShellChangesReadAcrossAgentsAndPages(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(shellruntime.RuntimeDirectoryEnvironment, t.TempDir())
	t.Setenv(shellruntime.ThreadIDEnvironment, "reviewer-thread")
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), testMekugiToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	directory, err := defaultMekugiReplayDirectory()
	if err != nil {
		t.Fatal(err)
	}
	store, err := openMekugiReplayStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	t.Chdir(workspace)
	id, err := store.reserveChange(t.Context(), workspace, "implementer-thread", "edited")
	if err != nil {
		t.Fatal(err)
	}
	history := mekugiHistory{
		changeID: id, correlationID: "edited", applied: true,
		reviewFiles: []mekugi.ReviewFile{{AfterPath: "file.txt", Diff: strings.Repeat("+line π changed\n", 40)}},
	}
	if err := store.put(t.Context(), workspace, map[string]mekugiHistory{"edited": history}); err != nil {
		t.Fatal(err)
	}
	// Child environment changes cannot redirect the authenticated store.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, direct := registry.directBashExecCommand([]string{"bash", "hchanges read " + id}); direct {
		t.Fatal("hchanges escaped the private runner")
	}
	want, err := store.readChanges(t.Context(), changeReadOptions{workspace: workspace, ids: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		t.Fatal(err)
	}
	for _, interpreter := range []string{"bash", "sh"} {
		t.Run(interpreter, func(t *testing.T) {
			var all strings.Builder
			cursor := ""
			for page := 0; page < 100; page++ {
				command := "hchanges read --max-tokens 32 "
				if cursor != "" {
					command += "--cursor " + cursor + " "
				}
				stdout, stderr, status := runShellWorkerTest(t, registry, interpreter, nil, command+id, nil)
				count, err := codec.Count(stdout)
				if err != nil || count > 32 {
					t.Fatalf("page tokens = %d, %v", count, err)
				}
				all.WriteString(stdout)
				if status == 0 {
					if stderr != "" || all.String() != want {
						t.Fatalf("pages differ: got %q stderr %q; want %q", all.String(), stderr, want)
					}
					break
				}
				const notice = "hchanges: incomplete; repeat this read with --cursor "
				if !strings.HasPrefix(stderr, notice) || stdout == "" || page == 99 {
					t.Fatalf("read failed: %q, %q, %d", stdout, stderr, status)
				}
				cursor = strings.TrimSpace(strings.TrimPrefix(stderr, notice))
			}
		})
	}
	if err := os.Mkdir(filepath.Join(workspace, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, status := runShellWorkerTest(t, registry, "bash", nil,
		"cd child\nhchanges read --workspace .. --summary "+id, nil)
	if status != 0 || stderr != "" || !strings.Contains(stdout, `file "" -> "file.txt"`) || strings.Contains(stdout, "+line") {
		t.Fatalf("summary from subdirectory: %q, %q, %d", stdout, stderr, status)
	}
	for _, arguments := range []string{"read hp_a99", "read hp_a1..hp_b2", "read --max-tokens 0 hp_a1", "read --history --summary hp_a1"} {
		stdout, stderr, status := runShellWorkerTest(t, registry, "bash", nil, "hchanges "+arguments, nil)
		if status == 0 || stdout != "" || stderr == "" {
			t.Fatalf("%q did not reject: %q, %q, %d", arguments, stdout, stderr, status)
		}
	}
}

func TestParseChangeRead(t *testing.T) {
	workspace := t.TempDir()
	for _, arguments := range [][]string{
		{}, {"write", "hp_a1"}, {"read"}, {"read", "--path", "", "hp_a1"},
		{"read", "--summary", "--summary", "hp_a1"}, {"read", "--max-tokens", "01", "hp_a1"},
		{"read", "--max-tokens", strconv.Itoa(hrunMaxTokens + 1), "hp_a1"},
		{"read", "--cursor"}, {"read", "--unknown", "hp_a1"},
	} {
		if _, err := parseChangeRead(arguments, workspace); err == nil {
			t.Fatalf("accepted %q", arguments)
		}
	}
}
