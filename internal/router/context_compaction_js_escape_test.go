package router

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestCompactionJSEscapeReferenceForms(t *testing.T) {
	// StringLiteral NonEscapeCharacter and non-strict legacy decimal/octal
	// escapes. A leading 4..7 consumes at most two octal digits, not three.
	for _, test := range []struct {
		name, source, want string
	}{
		{"identity colon", `3\:0003`, `3:0003`},
		{"identity letters", `source\_\old`, `source_old`},
		{"identity range", `3\:0003\.\.5\:0005`, `3:0003..5:0005`},
		{"identity unicode", `\界\😀`, `界😀`},
		{"octal colon two digits", `3\720003`, `3:0003`},
		{"octal colon three digits", `3\0720003`, `3:0003`},
		{"octal script", `\100shell\57result`, `@shell/result`},
		{"octal one digit", `\7x`, "\ax"},
		{"octal three digit boundary", `\1234`, `S4`},
		{"octal byte maximum", `\3777`, `ÿ7`},
		{"octal four prefix", `\400`, ` 0`},
		{"octal seven prefix", `\777`, `?7`},
		{"octal stops before eight", `\078`, "\a8"},
		{"null before eight", `\08`, "\x008"},
		{"null before nine", `\09`, "\x009"},
		{"non-octal decimal", `\8\9`, `89`},
		{"line separator continuation", "3\\\u2028:0003", `3:0003`},
		{"paragraph separator continuation", "3\\\u2029:0003", `3:0003`},
		{"escaped backslash stays one layer", `3\\720003`, `3\720003`},
		{"octal backslash stays one layer", `3\134720003`, `3\720003`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var visited []string
			unsafeEncoding := false
			compactionSourceVisitDecodedReferences(test.source, func(text string) {
				visited = append(visited, text)
			}, &unsafeEncoding)
			if unsafeEncoding || !slices.Equal(visited, []string{test.source, test.want}) {
				t.Fatalf("unsafe=%t visits=%q; want raw then %q", unsafeEncoding, visited, test.want)
			}
		})
	}
}

func TestCompactionJSEscapeInvalidUTF8IsUnsafe(t *testing.T) {
	_, _, unsafe := compactionSourceDecodeReferenceEscapes("\\\xff")
	if !unsafe {
		t.Fatal("invalid UTF-8 after a backslash was silently treated as a safe identity escape")
	}
}

func TestCompactionJSEscapeKeepsReferencedRows(t *testing.T) {
	rows := compactionSourceTestRows("", 18)
	for _, reference := range []string{`3\:0003`, `3\720003`, `3\0720003`} {
		t.Run(reference, func(t *testing.T) {
			referenced := make(map[string]bool)
			unsafeEncoding := false
			compactionSourceVisitDecodedReferences(reference, func(text string) {
				for _, row := range compactionRowReference.FindAllString(text, -1) {
					referenced[row] = true
				}
			}, &unsafeEncoding)
			if unsafeEncoding {
				t.Fatal("supported reference globally pinned source output")
			}
			got := compactionPruneSourceText(strings.Join(rows, ""), referenced, nil)
			if !strings.Contains(got, rows[2]) {
				t.Fatalf("referenced row lost: %s", got)
			}
			if strings.Contains(got, rows[10]) || !strings.Contains(got, "[mekugi compaction: omitted") {
				t.Fatal("unrelated historical source rows were not pruned")
			}
		})
	}
}

func TestCompactionJSEscapeSourceFrontier(t *testing.T) {
	for _, recent := range []int{1, compactionRecentOperations} {
		for _, reference := range []string{`3\:0003`, `3\720003`, `3\0720003`, `3\:0003\.\.5\:0005`, `3\720003\56\0565\720005`} {
			for _, consumer := range []string{"message", "function_call", "custom_tool_call"} {
				t.Run(fmt.Sprintf("recent=%d/%s/%s", recent, consumer, reference), func(t *testing.T) {
					rows := compactionSourceTestRows("source.go", 18)
					code := `const target = "` + reference + `"; text(target);`
					var retained json.RawMessage
					switch consumer {
					case "message":
						retained = mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "content": code})
					case "function_call":
						retained = compactTestCall("pending-exec", code)
					case "custom_tool_call":
						retained = mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "pending-script", "input": code})
					}
					items := []json.RawMessage{
						compactTestCall("source-old", "hread source.go"),
						compactTestOutput("source-old", strings.Join(rows, ""), 0),
						retained,
					}
					items = append(items, compactionSourceTestRecent()...)
					before := string(mustMarshalJSON(items))
					got := reduceContextCompactionSourceWithFrontier(items, items, recent)
					text := compactionSourceTestOutputText(t, got[1])
					if !strings.Contains(text, rows[2]) || strings.Contains(text, rows[10]) {
						t.Fatal("escaped reference lost or unrelated rows pinned")
					}
					if strings.Contains(reference, "0005") && (!strings.Contains(text, rows[3]) || !strings.Contains(text, rows[4])) {
						t.Fatal("escaped range did not retain every row")
					}
					for index := range items {
						if index != 1 && string(got[index]) != string(items[index]) {
							t.Fatalf("native item %d changed", index)
						}
					}
					if string(mustMarshalJSON(items)) != before {
						t.Fatal("source reduction mutated input")
					}
					again := reduceContextCompactionSourceWithFrontier(got, got, recent)
					if string(mustMarshalJSON(again)) != string(mustMarshalJSON(got)) {
						t.Fatal("repeated compaction changed retained evidence")
					}
				})
			}
		}
	}
}
