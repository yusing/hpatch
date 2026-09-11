package shellsyntax

import (
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	for _, test := range []struct {
		name, input string
		want        []string
	}{
		{
			name:  "single unchanged",
			input: "#!python3\r\n\r\nprint('hello')\r\n",
			want:  []string{"#!python3\r\n\r\nprint('hello')\r\n"},
		},
		{
			name:  "mixed interpreters inherit",
			input: "#!params={\"yield_time_ms\":1000}\necho hello\n#!python3\nprint('hello')\n",
			want: []string{
				"#!params={\"yield_time_ms\":1000}\necho hello\n",
				"#!python3\n#!params={\"yield_time_ms\":1000}\nprint('hello')\n",
			},
		},
		{
			name:  "implicit bash replaces params",
			input: "#!params={\"workdir\":\"/tmp\",\"tty\":false}\necho one\n#!params={\"yield_time_ms\":2000}\necho two\n#!python3\nprint(3)",
			want: []string{
				"#!params={\"workdir\":\"/tmp\",\"tty\":false}\necho one\n",
				"#!params={\"yield_time_ms\":2000}\necho two\n",
				"#!python3\n#!params={\"yield_time_ms\":2000}\nprint(3)",
			},
		},
		{
			name:  "new params after selector",
			input: "echo one\n#!python3\n#!params={}\nprint(2)\n#!bash\necho three",
			want: []string{
				"echo one\n",
				"#!python3\n#!params={}\nprint(2)\n",
				"#!bash\n#!params={}\necho three",
			},
		},
		{
			name:  "empty params clears inheritance",
			input: "#!params={\"workdir\":\"/tmp\"}\necho one\n#!params={}\necho two\n#!python3\nprint(3)",
			want: []string{
				"#!params={\"workdir\":\"/tmp\"}\necho one\n",
				"#!params={}\necho two\n",
				"#!python3\n#!params={}\nprint(3)",
			},
		},
		{
			name:  "template not inherited",
			input: "#!cmd=producer | {.}\n#!params={}\ncat\n#!python3\r\nprint(2)\r\n",
			want: []string{
				"#!cmd=producer | {.}\n#!params={}\ncat\n",
				"#!python3\r\n#!params={}\nprint(2)\r\n",
			},
		},
		{
			name:  "indented body data",
			input: "echo one\n  #!python3\n  #!params={}\necho two",
			want:  []string{"echo one\n  #!python3\n  #!params={}\necho two"},
		},
		{
			name:  "later command template is body data",
			input: "echo one\n#!cmd=producer | {.}\necho two",
			want:  []string{"echo one\n#!cmd=producer | {.}\necho two"},
		},
		{
			name:  "bare CR",
			input: "#!params={}\recho one\r#!python3\rprint(2)",
			want:  []string{"#!params={}\recho one\r", "#!python3\r#!params={}\nprint(2)"},
		},
		{
			name:  "unrelated directive comment",
			input: "echo one\n#!params-file=example\necho two",
			want:  []string{"echo one\n#!params-file=example\necho two"},
		},
		{
			name:  "retained reference",
			input: "#!script=@shell/example",
			want:  []string{"#!script=@shell/example"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Split(test.input)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Split = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}

func TestSplitRejectsInvalidPrograms(t *testing.T) {
	for _, source := range []string{
		"#!bash\n#!python3\nprint(2)",
		"#!params={}\n#!python3\nprint(2)",
		"echo one\n#!params={bad}\necho two",
		"echo one\n#!python3\n",
		"echo one\n#!script=@shell/example",
		"#!params={}\n#!params={}\necho one",
		"echo one\n#!\necho two",
		"echo one\n#!python3\nprint('\x00')",
	} {
		if programs, err := Split(source); err == nil {
			t.Errorf("Split(%q) = %#v, want rejection", source, programs)
		}
	}
}
