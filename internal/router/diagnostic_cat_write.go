package router

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

type diagnosticPayload struct {
	name        string
	interpreter string
	source      string
}

// These are authored shell inputs, not pretranslated patches. Playback exercises
// the same parser, carrier, native edit UI, and replay path as a model shell call.
var catWriteDiagnosticPayloads = []diagnosticPayload{
	{"single-quoted delimiter", "", "cat > single.txt <<'EOF'\nsingle quoted\nEOF\n"},
	{"input redirection first", "", "cat << 'EOF' > input-first.txt\ninput first\nEOF\n"},
	{"double-quoted delimiter", "", "cat > double.txt <<\"EOF\"\ndouble quoted\nEOF\n"},
	{"explicit file descriptors", "", "cat 0<<'EOF' 1> descriptors.txt\nexplicit descriptors\nEOF\n"},
	{"tab-stripped heredoc", "", "cat 1> tabs.txt 0<<-'EOF'\n\tfirst\n\t\tsecond\n\tEOF\n"},
	{"empty file", "", "cat > empty.txt <<'EOF'\nEOF\n"},
	{"blank lines and final LF", "", "cat > blank-lines.txt <<'EOF'\n\nbody\n\nEOF\n"},
	{"literal shell syntax and UTF-8", "", "cat > literal.txt <<'EOF'\n$HOME `whoami` $(date) \\n ; && |\n你好 α\nEOF\n"},
	{"single-quoted pathname", "", "cat > 'single path [1]~*?.txt' <<'EOF'\nquoted path\nEOF\n"},
	{"double-quoted pathname", "", "cat > \"double path [1]~*?.txt\" <<'EOF'\nquoted path\nEOF\n"},
	{"absolute pathname", "", "cat > @ABSOLUTE_PATH@ <<'EOF'\nabsolute path\nEOF\n"},
	{"newline sequence", "", "printf 'before newline\\n'\ncat > newline.txt <<'EOF'\nnewline sequence\nEOF\nprintf 'after newline\\n'\n"},
	{"semicolon sequence", "", "printf 'before semicolon\\n'; cat > semicolon.txt <<'EOF'; printf 'after semicolon\\n'\nsemicolon sequence\nEOF\n"},
	{"parent created by preceding command", "", "mkdir created-by-prefix; cat > created-by-prefix/out.txt <<'EOF'\nparent exists at execution time\nEOF\n"},
	{"repeated writes and overwrite", "", "cat > overwrite.txt <<'EOF'\nfirst version\nEOF\ncat > overwrite.txt <<'EOF'\nsecond version\nEOF\n"},
	{"explicit Bash", "#!bash", "cat > bash.txt <<'EOF'\nexplicit Bash\nEOF\n"},
	{"explicit POSIX sh", "#!sh", "cat > sh.txt <<'EOF'\nexplicit sh\nEOF\n"},
	{"env Bash selector", "#!/usr/bin/env bash", "cat > env-bash.txt <<'EOF'\nenv Bash\nEOF\n"},
	{"direct sh selector", "#!/bin/sh", "cat > direct-sh.txt <<'EOF'\ndirect sh\nEOF\n"},
}

func (p diagnosticPayload) input(directory string) string {
	params := map[string]json.RawMessage{"workdir": mustMarshalJSON(directory)}
	header := ""
	if p.interpreter != "" {
		header = p.interpreter + "\n"
	}
	if strings.Contains(p.source, "@ABSOLUTE_PATH@") {
		// The absolute-path case also exercises input without a params directive.
		return header + strings.ReplaceAll(p.source, "@ABSOLUTE_PATH@", shellQuoteArgument(filepath.Join(directory, "absolute.txt")))
	}
	header += "#!params=" + string(mustMarshalJSON(params)) + "\n"
	return header + p.source
}
