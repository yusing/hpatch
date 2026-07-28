package hpatch

import (
	"reflect"
	"strings"
	"testing"
)

func TestClipboardCopiesAndCutsAcrossFiles(t *testing.T) {
	tests := []struct {
		operation  string
		wantSource string
	}{
		{operation: "copy", wantSource: "head\nmove one\nmove two\nkeep\n"},
		{operation: "cut", wantSource: "head\nkeep\n"},
	}
	for _, test := range tests {
		t.Run(test.operation, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "source.txt", "head\nmove one\nmove two\nkeep\n", 0o644)
			writeTestFile(t, root, "destination.txt", "target\nafter\n", 0o644)
			script := strings.Join([]string{
				"in source.txt",
				"rsel 2:3",
				test.operation,
				"in destination.txt",
				"rsel 1:1",
				"paste",
			}, "\n")
			stdout, stderr, exitCode := runForTest(root, nil, script)
			if exitCode != 0 || stdout != "" || stderr != "" {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if got := readTestFile(t, root, "source.txt"); got != test.wantSource {
				t.Fatalf("source = %q, want %q", got, test.wantSource)
			}
			if got, want := readTestFile(t, root, "destination.txt"), "target\nmove one\nmove two\nafter\n"; got != want {
				t.Fatalf("destination = %q, want %q", got, want)
			}
		})
	}
}

func TestClipboardCanBePastedAcrossSeveralFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.txt", "copied\n", 0o644)
	writeTestFile(t, root, "first.txt", "first\n", 0o644)
	writeTestFile(t, root, "second.txt", "second\n", 0o644)
	script := strings.Join([]string{
		"in source.txt",
		"rsel 1:1",
		"copy",
		"in first.txt",
		"rsel 1:1",
		"paste",
		"in second.txt",
		"rsel 1:1",
		"paste",
	}, "\n")
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "first.txt"), "first\ncopied\n"; got != want {
		t.Fatalf("first = %q, want %q", got, want)
	}
	if got, want := readTestFile(t, root, "second.txt"), "second\ncopied\n"; got != want {
		t.Fatalf("second = %q, want %q", got, want)
	}
}

func TestLinewisePasteAddsOnlyMissingDestinationBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		destination string
		script      []string
		want        string
	}{
		{
			name:   "empty new file has no leading terminator",
			source: "tail",
			script: []string{"in source.txt", "rsel 1:1", "copy", "new destination.txt", "paste"},
			want:   "tail",
		},
		{
			name:        "LF boundaries on both sides",
			source:      "tail",
			destination: "beforeafter\n",
			script:      []string{"in source.txt", "rsel 1:1", "copy", "in destination.txt", `tsel 1 1 "before"`, "paste"},
			want:        "before\ntail\nafter\n",
		},
		{
			name:        "CRLF boundaries on both sides",
			source:      "tail",
			destination: "beforeafter\r\n",
			script:      []string{"in source.txt", "rsel 1:1", "copy", "in destination.txt", `tsel 1 1 "before"`, "paste"},
			want:        "before\r\ntail\r\nafter\r\n",
		},
		{
			name:        "paste does not split CRLF",
			source:      "tail",
			destination: "anchor\r\n",
			script:      []string{"in source.txt", "rsel 1:1", "copy", "in destination.txt", `bsel "anchor" "\r"`, "paste"},
			want:        "anchor\r\ntail",
		},
		{
			name:        "first destination terminator wins",
			source:      "tail",
			destination: "before\rafter\nlast",
			script:      []string{"in source.txt", "rsel 1:1", "copy", "in destination.txt", `tsel 1 1 "before"`, "paste"},
			want:        "before\rtail\rafter\nlast",
		},
		{
			name:   "internal source terminators stay exact",
			source: "one\rtwo",
			script: []string{"in source.txt", "rsel 1:2", "copy", "new destination.txt", "paste"},
			want:   "one\rtwo",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "source.txt", test.source, 0o644)
			if test.destination != "" {
				writeTestFile(t, root, "destination.txt", test.destination, 0o644)
			}
			stdout, stderr, exitCode := runForTest(root, nil, strings.Join(test.script, "\n"))
			if exitCode != 0 || stdout != "" || stderr != "" {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if got := readTestFile(t, root, "destination.txt"); got != test.want {
				t.Fatalf("destination = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClipboardFailuresAreAtomicAndActionable(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{name: "copy requires selection", script: "in source.txt\ncopy", want: "copy requires a selection"},
		{name: "cut requires selection", script: "in source.txt\ncut", want: "cut requires a selection"},
		{name: "paste requires clipboard", script: "in source.txt\npaste", want: "paste requires a preceding copy or cut in the same script"},
		{name: "removed dup is rejected", script: "in source.txt\nrsel 1:1\ndup", want: "unknown or malformed command"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "source.txt", "source\n", 0o644)
			before := readTree(t, root)
			stdout, stderr, exitCode := runForTest(root, []string{"translate"}, test.script)
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout, stderr, test.want)
			}
			if after := readTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
			}
		})
	}
}

func TestFailedCrossFilePasteDoesNotCommitCut(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.txt", "source\nkeep\n", 0o644)
	writeTestFile(t, root, "destination.txt", "destination\n", 0o644)
	before := readTree(t, root)
	script := strings.Join([]string{
		"in source.txt",
		"rsel 1:1",
		"cut",
		"in destination.txt",
		`type "first insertion"`,
		"paste",
	}, "\n")
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "baseline line 1 receives multiple insertions") || !strings.Contains(stderr, `operation "paste"`) {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := readTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
	}
}

func TestClipboardDoesNotPersistAcrossScripts(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.txt", "source\n", 0o644)
	if stdout, stderr, exitCode := runForTest(root, nil, "in source.txt\nrsel 1:1\ncopy"); exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("copy Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in source.txt\npaste")
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "paste requires a preceding copy or cut in the same script") {
		t.Fatalf("paste Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}
