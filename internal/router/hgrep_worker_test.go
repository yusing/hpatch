package router

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yusing/hpatch"
)

func TestSplitHGrepArgumentsPreservesLiteralArgv(t *testing.T) {
	arguments, err := splitHGrepArguments(`-F 'two words' "path with spaces.txt" semi;colon dollar$(value) back\\slash`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-F", "two words", "path with spaces.txt", "semi;colon", "dollar$(value)", "back\\slash"}
	if strings.Join(arguments, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
	for _, input := range []string{"", "   ", "'unterminated", `trailing\`} {
		if _, err := splitHGrepArguments(input); err == nil {
			t.Fatalf("splitHGrepArguments(%q) unexpectedly succeeded", input)
		}
	}
}

func TestHGrepExecInputUsesNormalQuotedArgv(t *testing.T) {
	carrier := workerExecInput(hgrepExecutableName, []string{"-F", "two words", "path with spaces.txt", "semi;colon"})
	encoded := strings.TrimPrefix(carrier, "const result = await tools.exec_command(")
	encoded = strings.TrimSuffix(encoded, ");\ntext(result.output);")
	var arguments struct {
		Command string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(encoded), &arguments); err != nil {
		t.Fatal(err)
	}
	if want := `hgrep -F 'two words' 'path with spaces.txt' 'semi;colon'`; arguments.Command != want {
		t.Fatalf("hgrep command = %q, want %q", arguments.Command, want)
	}
}

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("integration coverage requires rg:", err)
	}
}

func TestNormalizeHGrepArguments(t *testing.T) {
	for _, test := range []struct {
		name         string
		arguments    []string
		want         []string
		wantWarnings []string
		wantDefault  bool
	}{
		{
			name:        "ordinary search",
			arguments:   []string{"-Fi", "pattern", "--glob", "*.go", "."},
			want:        []string{"-Fi", "pattern", "--glob", "*.go", "."},
			wantDefault: false,
		},
		{
			name:        "silent fixed presentation",
			arguments:   []string{"-inH", "--line-number", "--no-heading", "pattern"},
			want:        []string{"-i", "pattern"},
			wantDefault: true,
		},
		{
			name:         "warned conflicting presentation",
			arguments:    []string{"-iN", "--json", "--count", "pattern"},
			want:         []string{"-i", "pattern"},
			wantWarnings: []string{"-N", "--json", "--count"},
			wantDefault:  true,
		},
		{
			name:         "ignored options consume values",
			arguments:    []string{"--color", "always", "-M80", "-r", "replacement", "pattern", "."},
			want:         []string{"pattern", "."},
			wantWarnings: []string{"--color", "-M", "-r"},
		},
		{
			name:        "empty supported option value",
			arguments:   []string{"-e", "", "."},
			want:        []string{"-e", "", "."},
			wantDefault: false,
		},
		{
			name:        "option-like values stay literal",
			arguments:   []string{"-e", "-n", "."},
			want:        []string{"-e", "-n", "."},
			wantDefault: false,
		},
		{
			name:        "double dash ends options",
			arguments:   []string{"--", "--json"},
			want:        []string{"--", "--json"},
			wantDefault: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, warnings, gotDefault, err := normalizeHGrepArguments(test.arguments)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
				t.Errorf("normalized arguments = %#v, want %#v", got, test.want)
			}
			if strings.Join(warnings, "\x00") != strings.Join(test.wantWarnings, "\x00") {
				t.Errorf("warnings = %#v, want %#v", warnings, test.wantWarnings)
			}
			if gotDefault != test.wantDefault {
				t.Errorf("needs default path = %t, want %t", gotDefault, test.wantDefault)
			}
		})
	}

	for _, arguments := range [][]string{
		{"-U", "pattern"},
		{"--pre=cat", "pattern"},
		{"--encoding", "utf-16", "pattern"},
		{"-a", "pattern"},
		{"--help", "pattern"},
	} {
		if _, _, _, err := normalizeHGrepArguments(arguments); err == nil {
			t.Fatalf("normalizeHGrepArguments(%q) unexpectedly succeeded", arguments)
		}
	}
	for _, arguments := range [][]string{{}, {"--glob"}, {"-e"}} {
		if _, _, _, err := normalizeHGrepArguments(arguments); err == nil {
			t.Fatalf("incomplete arguments %q unexpectedly succeeded", arguments)
		}
	}
}

func TestNormalizeHGrepArgumentsFindsDefaultPath(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      bool
	}{
		{arguments: []string{"pattern"}, want: true},
		{arguments: []string{"pattern", "path"}},
		{arguments: []string{"-e", "pattern"}, want: true},
		{arguments: []string{"-e", "pattern", "path"}},
		{arguments: []string{"--regexp=pattern"}, want: true},
		{arguments: []string{"--regexp=pattern", "path"}},
		{arguments: []string{"-g", "*.go", "pattern"}, want: true},
		{arguments: []string{"-ig", "*.go", "pattern"}, want: true},
		{arguments: []string{"-A10", "-B5", "pattern"}, want: true},
	} {
		_, _, got, err := normalizeHGrepArguments(test.arguments)
		if err != nil {
			t.Errorf("normalizeHGrepArguments(%q): %v", test.arguments, err)
		} else if got != test.want {
			t.Errorf("normalizeHGrepArguments(%q) default path = %t, want %t", test.arguments, got, test.want)
		}
	}
}

func TestRunHGrepWorkerReturnsVerifiedRows(t *testing.T) {
	requireRipgrep(t)
	workspace := t.TempDir()
	path := filepath.Join(workspace, "path with spaces.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunHGrepWorker(t.Context(), hgrepExecutableName, []string{"-F", "alpha", "path with spaces.txt"}, &stdout, &stderr)
	if !handled || exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("worker = handled %t, exit %d, stderr %q", handled, exitCode, stderr.String())
	}
	want := `"path with spaces.txt":1:8ed3 alpha` + "\n" +
		`"path with spaces.txt":2:09af beta alpha` + "\n"
	if stdout.String() != want {
		t.Fatalf("worker output = %q, want %q", stdout.String(), want)
	}
}

func TestRunHGrepWorkerIgnoresPresentationOptions(t *testing.T) {
	requireRipgrep(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	for _, test := range []struct {
		name       string
		arguments  []string
		wantStderr string
	}{
		{
			name:      "matching format is silent",
			arguments: []string{"-FnH", "--line-number", "alpha", "source.txt"},
		},
		{
			name:       "conflicting format warns",
			arguments:  []string{"-FN", "--json", "alpha", "source.txt"},
			wantStderr: "hgrep: warning: ignoring ripgrep options -N, --json; output remains verified rows\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			handled, exitCode := RunHGrepWorker(t.Context(), hgrepExecutableName, test.arguments, &stdout, &stderr)
			if !handled || exitCode != 0 {
				t.Fatalf("worker = handled %t, exit %d, stderr %q", handled, exitCode, stderr.String())
			}
			if got, want := stdout.String(), `"source.txt":1:8ed3 alpha`+"\n"; got != want {
				t.Errorf("worker output = %q, want %q", got, want)
			}
			if stderr.String() != test.wantStderr {
				t.Errorf("worker stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestRunHGrepWorkerAcceptsNormalArgvAndReturnsContextRows(t *testing.T) {
	requireRipgrep(t)
	workspace := t.TempDir()
	lines := []string{"outside before", "before", "hpatch first", "between", "hpatch second", "after one", "after two", "outside after"}
	if err := os.WriteFile(filepath.Join(workspace, "source.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunHGrepWorker(t.Context(), hgrepExecutableName, []string{"-A2", "-B1", "hpatch"}, &stdout, &stderr)
	if !handled || exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("worker = handled %t, exit %d, stderr %q", handled, exitCode, stderr.String())
	}
	var want strings.Builder
	for index := 1; index <= 6; index++ {
		want.WriteString(`"./source.txt":`)
		want.WriteString(hpatch.FormatHashLineForHost(index+1, lines[index]))
	}
	if stdout.String() != want.String() {
		t.Fatalf("worker output = %q, want %q", stdout.String(), want.String())
	}
}

func TestRunHGrepWorkerDefaultsToCurrentDirectoryAndNoMatchSucceeds(t *testing.T) {
	requireRipgrep(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	for _, input := range []string{"needle", "absent"} {
		var stdout, stderr bytes.Buffer
		handled, exitCode := RunHGrepWorker(t.Context(), hgrepExecutableName, []string{input}, &stdout, &stderr)
		if !handled || exitCode != 0 || stderr.Len() != 0 {
			t.Fatalf("RunHGrepWorker(%q) = handled %t, exit %d, stderr %q", input, handled, exitCode, stderr.String())
		}
		if input == "needle" && !strings.Contains(stdout.String(), `:1:0988 needle`+"\n") {
			t.Fatalf("default-directory output = %q", stdout.String())
		}
		if input == "absent" && stdout.Len() != 0 {
			t.Fatalf("no-match output = %q", stdout.String())
		}
	}
}

func TestRunHGrepWorkerJSONQuotesControlCharactersInPath(t *testing.T) {
	requireRipgrep(t)
	workspace := t.TempDir()
	path := "control\t\"\x01\\name.txt"
	if err := os.WriteFile(filepath.Join(workspace, path), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunHGrepWorker(t.Context(), hgrepExecutableName, []string{"-F", "alpha", path}, &stdout, &stderr)
	if !handled || exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("worker = handled %t, exit %d, stderr %q", handled, exitCode, stderr.String())
	}
	quotedPath, err := json.Marshal(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), string(quotedPath)+":1:8ed3 alpha\n"; got != want {
		t.Fatalf("worker output = %q, want %q", got, want)
	}
}

func TestRunHGrepWorkerBoundsOversizedMatchBeforeDecode(t *testing.T) {
	requireRipgrep(t)
	workspace := t.TempDir()
	content := strings.Repeat("x", maxHGrepOutputBytes) + "needle\n"
	if err := os.WriteFile(filepath.Join(workspace, "large.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunHGrepWorker(t.Context(), hgrepExecutableName, []string{"needle", "large.txt"}, &stdout, &stderr)
	if !handled || exitCode != 0 || stderr.Len() != 0 || stdout.String() != hgrepLimitMessage {
		t.Fatalf("worker = handled %t, exit %d, stdout %q, stderr %q", handled, exitCode, stdout.String(), stderr.String())
	}
}

func TestRunHGrepWorkerReturnsConciseFailures(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	for _, test := range []struct {
		name       string
		arguments  []string
		path       string
		want       string
		requiresRG bool
	}{
		{name: "reserved option", arguments: []string{"-U", "pattern"}, want: "incompatible"},
		{name: "invalid pattern", arguments: []string{"[", "."}, want: "hgrep:", requiresRG: true},
		{name: "missing executable", arguments: []string{"pattern", "."}, path: t.TempDir(), want: "executable file not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.requiresRG {
				requireRipgrep(t)
			}
			if test.path != "" {
				t.Setenv("PATH", test.path)
			}
			var stdout, stderr bytes.Buffer
			handled, exitCode := RunHGrepWorker(t.Context(), hgrepExecutableName, test.arguments, &stdout, &stderr)
			if !handled || exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("worker = handled %t, exit %d, stdout %q, stderr %q", handled, exitCode, stdout.String(), stderr.String())
			}
			if strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("worker diagnostic is not concise: %q", stderr.String())
			}
		})
	}
}

func TestEnsureHGrepSymlinkForExecutable(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "hpatch-router")
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	link, err := ensureWorkerSymlinkInDirectory(executable, filepath.Dir(executable), hgrepExecutableName)
	if err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != executable {
		t.Fatalf("hgrep symlink target = %q, %v; want %q", target, err, executable)
	}
}

func TestEnsureHGrepSymlinkRejectsExistingCommand(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "hpatch-router")
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, hgrepExecutableName), []byte("other"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWorkerSymlinkInDirectory(executable, filepath.Dir(executable), hgrepExecutableName); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("existing command error = %v", err)
	}
}

func TestHGrepStartupSymlinkExecutesPrivateWorkerEndToEnd(t *testing.T) {
	requireRipgrep(t)
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "hpatch-router")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/hpatch-router")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hpatch-router: %v\n%s", err, output)
	}
	hgrepExecutable, err := ensureWorkerSymlinkInDirectory(binary, filepath.Dir(binary), hgrepExecutableName)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "source.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), hgrepExecutable, "-F", "package", "source.go")
	command.Dir = workspace
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute hgrep: %v\n%s", err, output)
	}
	if got, want := string(output), `"source.go":1:9705 package sample`+"\n"; got != want {
		t.Fatalf("hgrep output = %q, want %q", got, want)
	}
}
