package hpatch

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/yusing/hpatch/internal/patchtest"
)

func TestCommitMaterializesGenerationBaseline(t *testing.T) {
	tests := []struct {
		name    string
		initial map[string]string
		script  string
		want    map[string]string
	}{
		{
			name:    "introduced text becomes selectable",
			initial: map[string]string{"file.txt": "old\n"},
			script:  "in file.txt\ntsel " + hashLine("old") + " \"old\"\ntype \"middle\"\ncommit\ntsel " + hashLine("middle") + " \"middle\"\ntype \"final\"\n",
			want:    map[string]string{"file.txt": "final\n"},
		},
		{
			name:   "new file accepts another generation edit",
			script: "new note.txt\ntype \"first\"\ncommit\ntsel " + hashLine("first") + " \"first\"\ntype \"second\"\n",
			want:   map[string]string{"note.txt": "second"},
		},
		{
			name:    "materialized existing edit can be removed",
			initial: map[string]string{"file.txt": "old\n"},
			script:  "in file.txt\ntsel " + hashLine("old") + " \"old\"\ntype \"new\"\ncommit\nrm\n",
			want:    map[string]string{},
		},
		{
			name:    "moves collapse across generations",
			initial: map[string]string{"file.txt": "old\n"},
			script:  "in file.txt\nmv middle.txt\ncommit\nmv final.txt\n",
			want:    map[string]string{"final.txt": "old\n"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range test.initial {
				writeTestFile(t, root, path, content, 0o644)
			}
			stdout, stderr, exitCode := runForTest(root, nil, test.script)
			if exitCode != 0 || stdout != "" || stderr != "" {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if got := readTree(t, root); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("tree = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestCommitPreservesClipboardAcrossGenerations(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.txt", "copied\n", 0o644)
	script := "in source.txt\nrsel " + hashLine("copied") + " " + hashLine("copied") + "\ncopy\nnew destination.txt\ncommit\npaste\n"
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := readTestFile(t, root, "destination.txt"); got != "copied\n" {
		t.Fatalf("destination.txt = %q", got)
	}
}

func TestCommitReleasesLogicalPathsForLaterGenerations(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   map[string]string
	}{
		{
			name:   "moved original path can be reused",
			script: "in a.txt\nmv b.txt\ncommit\nnew a.txt\ntype \"new\\n\"\n",
			want:   map[string]string{"a.txt": "new\n", "b.txt": "old\n"},
		},
		{
			name:   "removed path can be recreated",
			script: "in a.txt\nrm\ncommit\nnew a.txt\ntype \"new\\n\"\n",
			want:   map[string]string{"a.txt": "new\n"},
		},
	}
	for _, test := range tests {
		for _, args := range [][]string{nil, {"translate"}} {
			mode := "normal"
			if len(args) != 0 {
				mode = "translate"
			}
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				root := t.TempDir()
				initial := map[string]string{"a.txt": "old\n"}
				writeTestFile(t, root, "a.txt", initial["a.txt"], 0o644)
				stdout, stderr, exitCode := runForTest(root, args, test.script)
				if exitCode != 0 || stderr != "" {
					t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
				}
				got := readTree(t, root)
				if len(args) != 0 {
					if !reflect.DeepEqual(got, initial) {
						t.Fatalf("translate mutated tree: %#v", got)
					}
					var err error
					got, err = patchtest.Apply(initial, stdout)
					if err != nil {
						t.Fatalf("applying translated patch: %v\n%s", err, stdout)
					}
				} else if stdout != "" {
					t.Fatalf("normal stdout = %q", stdout)
				}
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("tree = %#v, want %#v", got, test.want)
				}
			})
		}
	}
}

func TestCommitAllowsMoveBackToOriginalPathAsNetNoop(t *testing.T) {
	const script = "in a.txt\nmv b.txt\ncommit\nmv a.txt\n"
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "old\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("normal = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := readTree(t, root); !reflect.DeepEqual(got, map[string]string{"a.txt": "old\n"}) {
		t.Fatalf("normal tree = %#v", got)
	}

	stdout, stderr, exitCode = runForTest(root, []string{"translate"}, script)
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "does not change") {
		t.Fatalf("translate no-op = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestGenerationReservationPreventsSameGenerationPathReuse(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "old\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in a.txt\nmv b.txt\nnew a.txt\n")
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "destination a.txt already exists") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestFailureAfterCommitRemainsAtomic(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)
	script := "in file.txt\ntsel " + hashLine("old") + " \"old\"\ntype \"middle\"\ncommit\ntsel " + hashLine("middle") + " \"missing\"\n"
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "found 0 of 1 requested matches of \"missing\" at or after line 1") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := readTestFile(t, root, "file.txt"); got != "old\n" {
		t.Fatalf("failure exposed materialized content: %q", got)
	}
}

func TestCommitResetsStateButScriptEndDoesNot(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantHeader string
		wantLine   string
	}{
		{
			name:       "explicit commit resets cursor",
			script:     "in file.txt\ntsel f44e \"beta\"\ntype \"B\"\ncommit\n",
			wantHeader: "in file.txt 1:1",
			wantLine:   "#|B",
		},
		{
			name:       "no-op commit clears selection",
			script:     "in file.txt\ntsel f44e \"beta\"\ncommit\n",
			wantHeader: "in file.txt 1:1",
			wantLine:   "#|beta",
		},
		{
			name:       "script end preserves pending cursor",
			script:     "in file.txt\ntsel f44e \"beta\"\ntype \"B\"\ncommit\ntsel " + hashLine("B") + " \"B\"\ntype \"CC\"\n",
			wantHeader: "in file.txt 2:3",
			wantLine:   "#|CC",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "alpha\nbeta\ngamma\n", 0o644)
			var stdout, stderr bytes.Buffer
			if exitCode := Run(nil, strings.NewReader(test.script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
			}
			if !strings.HasPrefix(normalizeHashlineRows(stderr.String()), test.wantHeader+"\n") || !strings.Contains(normalizeHashlineRows(stderr.String()), test.wantLine+"\n") {
				t.Fatalf("report %q does not contain %q and %q", normalizeHashlineRows(stderr.String()), test.wantHeader, test.wantLine)
			}
		})
	}
}

func TestCommitWithoutActiveFileSucceeds(t *testing.T) {
	stdout, stderr, exitCode := runForTest(t.TempDir(), nil, "commit\n")
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}
