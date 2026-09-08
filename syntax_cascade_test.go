package hpatch

import (
	"context"
	"reflect"
	"testing"
)

func TestSyntaxCascadeAdaptersPreserveDiagnosticIdentity(t *testing.T) {
	goFailures := []goSyntaxFailure{
		{line: 2, column: 5, message: "root", counted: false},
		{line: 2, column: 9, message: "same row", counted: true},
		{line: 3, column: 7, message: "cascade", counted: true},
	}
	goWant := []goSyntaxFailure{goFailures[0], goFailures[1], {line: 2, column: 5, message: "root", counted: true}}
	if got := collapseGoSyntaxCascades(t.Context(), "package p\nvar =\nvar y = 1\n", goFailures); !reflect.DeepEqual(got, goWant) {
		t.Fatalf("Go cascade = %+v, want %+v", got, goWant)
	}
	if got := collapseGoSyntaxCascades(t.Context(), "package p\nvar =\nvar =\n", goFailures); !reflect.DeepEqual(got, goFailures) {
		t.Fatalf("Go independent diagnostics = %+v, want %+v", got, goFailures)
	}
	for _, test := range []struct {
		name                   string
		language               indentationWrapperLanguage
		cascading, independent string
	}{
		{"JavaScript", indentationLanguageJavaScript, "// package p\nconst x = ;\nconst y = 1;\n", "// package p\nconst x = ;\nconst y = ;\n"},
		{"TypeScript", indentationLanguageTypeScript, "// package p\nconst x = ;\nconst y = 1;\n", "// package p\nconst x = ;\nconst y = ;\n"},
		{"Python", indentationLanguagePython, "# package p\nx =\ny = 1\n", "# package p\nx =\ny =\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			failures := []languageSyntaxFailure{
				{line: 2, column: 5, kind: "root"},
				{line: 2, column: 9, kind: "same row", missing: true},
				{line: 3, column: 7, kind: "cascade", missing: true},
			}
			want := []languageSyntaxFailure{failures[0], failures[1], failures[0]}
			if got := collapseLanguageSyntaxCascades(t.Context(), test.cascading, test.language, failures); !reflect.DeepEqual(got, want) {
				t.Fatalf("cascade = %+v, want %+v", got, want)
			}
			if got := collapseLanguageSyntaxCascades(t.Context(), test.independent, test.language, failures); !reflect.DeepEqual(got, failures) {
				t.Fatalf("independent diagnostics = %+v, want %+v", got, failures)
			}
		})
	}
}

func TestSyntaxCascadeAdaptersRespectCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := collapseGoSyntaxCascades(ctx, "package p\nvar =\n", []goSyntaxFailure{{line: 2}}); len(got) != 0 {
		t.Fatalf("cancelled Go cascade = %+v", got)
	}
	if got := collapseLanguageSyntaxCascades(ctx, "const x = ;\n", indentationLanguageJavaScript, []languageSyntaxFailure{{line: 1}}); len(got) != 0 {
		t.Fatalf("cancelled language cascade = %+v", got)
	}
}

func TestSyntaxCascadeOriginsShareRepairLineCache(t *testing.T) {
	content := "first\r\nsecond\nthird\rfourth\nfifth\n"
	var candidates []string
	origins := syntaxCascadeOrigins(t.Context(), content, []int{1, 1, 2, 3, 4, 5}, func(candidate string) []int {
		candidates = append(candidates, candidate)
		switch candidate {
		case "     \r\nsecond\nthird\rfourth\nfifth\n":
			return []int{2, 3, 5}
		case "first\r\n      \nthird\rfourth\nfifth\n":
			return []int{5}
		default:
			t.Fatalf("unexpected reparse source %q", candidate)
			return nil
		}
	})
	if want := []int{0, 1, 2, 2, 0, 5}; !reflect.DeepEqual(origins, want) {
		t.Fatalf("origins = %v, want %v", origins, want)
	}
	if len(candidates) != 2 {
		t.Fatalf("reparsed %d times, want once per repair line", len(candidates))
	}
}

func TestSyntaxCascadeOriginsKeepUnlocatedDiagnostics(t *testing.T) {
	calls := 0
	origins := syntaxCascadeOrigins(t.Context(), "first\nsecond\n", []int{0, 0, 1, 2}, func(candidate string) []int {
		calls++
		if candidate != "     \nsecond\n" {
			t.Fatalf("reparsed unlocated diagnostic: %q", candidate)
		}
		return []int{2}
	})
	if want := []int{0, 1, 2, 3}; !reflect.DeepEqual(origins, want) || calls != 1 {
		t.Fatalf("origins %v, calls %d; want %v and 1", origins, calls, want)
	}
}

func TestSyntaxCascadeOriginsStopAfterCancelledReparse(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	origins := syntaxCascadeOrigins(ctx, "first\nsecond\nthird\n", []int{1, 2, 3}, func(string) []int {
		calls++
		cancel()
		return nil
	})
	if want := []int{0, 0}; !reflect.DeepEqual(origins, want) || calls != 1 {
		t.Fatalf("cancelled origins %v, calls %d; want processed prefix %v and 1", origins, calls, want)
	}
}
