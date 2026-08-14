package hpatch

import (
	"slices"
	"strings"
	"testing"
)

func TestHPatch2FinalStateReport(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\nbeta\ngamma\n", 0o644)
	script := "in file.txt\ntype " + row(2, "beta") + ` "B"`
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	for _, fragment := range []string{
		"in file.txt\n",
		"last type file.txt 1 ranges 2:1-3:1\n",
		"files add=0 update=1 move=0 delete=0\n",
		"refs 2 type file.txt\n",
		"1:" + hashLine("alpha") + " alpha\n",
		"2:" + hashLine("B") + " B\n",
		"3:" + hashLine("gamma") + " gamma\n",
	} {
		if !strings.Contains(result.Report, fragment) {
			t.Fatalf("report %q lacks %q", result.Report, fragment)
		}
	}
}

func TestHPatch2FinalStateReportProvidesReusableReplacementTarget(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\nbeta\ngamma\n", 0o644)
	before := row(2, "beta")
	after := row(2, "B")
	firstScript := "in file.txt\ntype " + before + ` "B"`
	translated, err := TranslateForHostAt(t.Context(), root, firstScript, "")
	if err != nil {
		t.Fatal(err)
	}
	wantAlias := TargetAlias{Path: "file.txt", Before: before, After: after}
	if len(translated.TargetAliases) != 1 || translated.TargetAliases[0] != wantAlias {
		t.Fatalf("target aliases = %+v, want %+v", translated.TargetAliases, wantAlias)
	}
	result, err := applyForHostAtTest(t, root, firstScript, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}

	rewritten, err := RewriteTargetAliases(
		"in file.txt\ntype "+before+` "C"`,
		[]TargetAlias{{Path: "file.txt", Before: before, After: after}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := "in file.txt\ntype " + after + ` "C"`; rewritten != want {
		t.Fatalf("rewritten script = %q, want %q", rewritten, want)
	}
	if result, err := applyForHostAtTest(t, root, rewritten, ""); err != nil {
		t.Fatalf("rewritten ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if content := readTestFile(t, root, "file.txt"); content != "alpha\nC\ngamma\n" {
		t.Fatalf("rewritten content = %q", content)
	}
}

func TestRewriteTargetAliasesIsPathScopedAndChainsConfirmedReplacements(t *testing.T) {
	first := row(2, "beta")
	second := row(3, "B")
	third := row(4, "C")
	aliases := []TargetAlias{
		{Path: "file.txt", Before: first, After: second},
		{Path: "file.txt", Before: second, After: third},
	}
	script := "in other.txt\ntype " + first + ` "other"` +
		"\nin file.txt\ntype " + first + ` "matched"`
	rewritten, commands, err := RewriteTargetAliasesWithCommands(script, aliases)
	if err != nil {
		t.Fatal(err)
	}
	want := "in other.txt\ntype " + first + ` "other"` +
		"\nin file.txt\ntype " + third + ` "matched"`
	if rewritten != want {
		t.Fatalf("rewritten script = %q, want %q", rewritten, want)
	}
	if len(commands) != 1 || commands[0] != 4 {
		t.Fatalf("rewritten commands = %v, want [4]", commands)
	}
}

func TestRewriteTargetAliasesClassifiesSamePathRowSpanRelationsWithoutHashes(t *testing.T) {
	alias := TargetAlias{
		Path:   "file.txt",
		Before: "10:aaaa..20:bbbb",
		After:  "30:cccc..40:dddd",
	}
	script := strings.Join([]string{
		"in file.txt",
		`type 10:1111..20:2222 "coordinate exact"`,
		`type 10:aaaa..20:bbbb "rewritten exact"`,
		`type 5:1111..25:2222 "contains"`,
		`type 12:1111..18:2222 "contained"`,
		`type 18:1111..25:2222 "overlap"`,
		`type 21:1111..25:2222 "none"`,
		"in other.txt",
		`type 10:1111..20:2222 "other path"`,
		`type "literal" "not a row span"`,
	}, "\n")

	rewritten, diagnostics, err := RewriteTargetAliasesWithDiagnostics(script, []TargetAlias{alias})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten, `type 30:cccc..40:dddd "rewritten exact"`) {
		t.Fatalf("rewritten script = %q", rewritten)
	}
	want := []TargetAliasDiagnostic{
		{Command: 2, Relation: TargetAliasRelationExact},
		{Command: 3, Rewritten: true, Relation: TargetAliasRelationExact},
		{Command: 4, Relation: TargetAliasRelationContains},
		{Command: 5, Relation: TargetAliasRelationContained},
		{Command: 6, Relation: TargetAliasRelationOverlap},
		{Command: 7, Relation: TargetAliasRelationNone},
		{Command: 9, Relation: TargetAliasRelationNone},
	}
	if !slices.Equal(diagnostics, want) {
		t.Fatalf("alias diagnostics = %+v, want %+v", diagnostics, want)
	}
}

func TestHPatch2FormattedReferencesTrackEditedContent(t *testing.T) {
	root := t.TempDir()
	before := "package p\n\nvar ( a=1; b=2; c=3; d=4 )\n\nvar filler1=1\nvar filler2=2\nvar target=1\n"
	writeTestFile(t, root, "file.go", before, 0o644)
	script := "in file.go\ntype " + row(7, "var target=1") + ` "var target=2"`
	translated, err := TranslateForHostAt(t.Context(), root, script, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if !strings.Contains(result.Report, "refs 2 type file.go\n") {
		t.Fatalf("formatted references %q lack command header", result.Report)
	}
	want := "12:" + hashLine("var target = 2") + " var target = 2\n"
	if !strings.Contains(result.Report, want) {
		t.Fatalf("formatted references %q lack edited row %q", result.Report, want)
	}
	wantAlias := TargetAlias{Path: "file.go", Before: row(7, "var target=1"), After: row(12, "var target = 2")}
	if len(translated.TargetAliases) != 1 || translated.TargetAliases[0] != wantAlias {
		t.Fatalf("formatted aliases = %+v, want %+v", translated.TargetAliases, wantAlias)
	}
}

func TestHPatch2FinalStateReportIsBounded(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "x x x x\n", 0o644)
	script := "in file.txt\ntype " + row(1, "x x x x") + ` "x" 4 "y"`
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if !strings.Contains(result.Report, "last type file.txt 4 ranges ") || !strings.Contains(result.Report, " +1 more\n") {
		t.Fatalf("report = %q", result.Report)
	}
}

func TestHPatch2InsertionReportNamesTargetRange(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\nbeta\n", 0o644)
	script := "in file.txt\ntype+ " + row(1, "alpha") + ` "inserted\n"`
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if !strings.Contains(result.Report, "last type+ file.txt 1 ranges 1:1-2:1\n") {
		t.Fatalf("report = %q", result.Report)
	}
	if !strings.Contains(result.Report, "refs 2 type+ file.txt\n") {
		t.Fatalf("report = %q", result.Report)
	}
}

func TestHPatch2PreviewDoesNotInventTrailingEmptyLine(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\n", 0o644)
	result, err := applyForHostAtTest(t, root, "in file.txt", "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	want := "in file.txt\nlast none\nfiles add=0 update=0 move=0 delete=0\n1:" + hashLine("alpha") + " alpha\n"
	if result.Report != want {
		t.Fatalf("report = %q, want %q", result.Report, want)
	}
}

func TestHPatch2EmptyNewFileReport(t *testing.T) {
	root := t.TempDir()
	result, err := applyForHostAtTest(t, root, "new empty.txt", "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	want := "in empty.txt\nlast none\nfiles add=1 update=0 move=0 delete=0\n1:" + hashLine("") + " \n"
	if result.Report != want {
		t.Fatalf("report = %q, want %q", result.Report, want)
	}
}

func TestHPatch2MovedMutationReportUsesFinalPath(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "old.txt", "alpha\n", 0o644)
	script := "in old.txt\ntype " + row(1, "alpha") + ` "beta"` + "\nmv new.txt"
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	for _, want := range []string{
		"in new.txt\n",
		"last type new.txt 1 ranges 1:1-2:1\n",
		"files add=0 update=1 move=1 delete=0\n",
		"refs 2 type new.txt\n",
		"1:" + hashLine("beta") + " beta\n",
	} {
		if !strings.Contains(result.Report, want) {
			t.Fatalf("report %q lacks %q", result.Report, want)
		}
	}
}

func TestHPatch2PreviewBoundsContent(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("界", 70) + "tail\n"
	writeTestFile(t, root, "file.txt", content, 0o644)
	result, err := applyForHostAtTest(t, root, "in file.txt", "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	rowText := strings.Split(strings.TrimSuffix(result.Report, "\n"), "\n")[3]
	if strings.Count(rowText, "界") != 64 || strings.Contains(rowText, "tail") {
		t.Fatalf("bounded preview row = %q", rowText)
	}
}

func TestHPatch2PreviewEscapesControls(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "a\x01b\n", 0o644)
	result, err := applyForHostAtTest(t, root, "in file.txt", "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if strings.ContainsRune(result.Report, '\x01') || !strings.Contains(result.Report, `a\x01b`) {
		t.Fatalf("escaped report = %q", result.Report)
	}
}

func TestHPatch2FinalStateNoActiveFile(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\n", 0o644)
	result, err := applyForHostAtTest(t, root, "in file.txt\nrm", "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	want := "no active file\nlast none\nfiles add=0 update=0 move=0 delete=1\n"
	if result.Report != want {
		t.Fatalf("report = %q, want %q", result.Report, want)
	}
}

func TestHPatch2FinalReferencesCoverCommandsFilesAndContinuation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "first.txt", "a0\na1\na2\na3\na4\n", 0o644)
	writeTestFile(t, root, "second.txt", "b0\nb1\nb2\n", 0o644)
	script := "in first.txt\n" +
		"type " + row(2, "a1") + " \"A1\"\n" +
		"type " + row(4, "a3") + " \"A3\"\n" +
		"in second.txt\n" +
		"type+ " + row(2, "b1") + " \"inserted\\n\""
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}

	firstHeader := "refs 2 type first.txt\n"
	secondHeader := "refs 3 type first.txt\n"
	thirdHeader := "refs 5 type+ second.txt\n"
	firstIndex := strings.Index(result.Report, firstHeader)
	secondIndex := strings.Index(result.Report, secondHeader)
	thirdIndex := strings.Index(result.Report, thirdHeader)
	if firstIndex < 0 || secondIndex <= firstIndex || thirdIndex <= secondIndex {
		t.Fatalf("reference block order in report %q", result.Report)
	}
	for _, current := range []string{
		"2:" + hashLine("A1") + " A1\n",
		"4:" + hashLine("A3") + " A3\n",
		"3:" + hashLine("inserted") + " inserted\n",
		"4:" + hashLine("b2") + " b2\n",
	} {
		if !strings.Contains(result.Report, current) {
			t.Fatalf("report %q lacks current row %q", result.Report, current)
		}
	}

	reportedTarget := row(4, "b2")
	if !strings.Contains(result.Report, reportedTarget+" b2\n") {
		t.Fatalf("report %q lacks reusable target %q", result.Report, reportedTarget)
	}
	continuation, continuationErr := applyForHostAtTest(t, root, "in second.txt\ntype "+reportedTarget+" \"B2\"", "")
	if continuationErr != nil {
		t.Fatalf("reported-row continuation error = %v, report %q", continuationErr, continuation.Report)
	}

	stale, staleErr := applyForHostAtTest(t, root, "in first.txt\ntype "+row(2, "a1")+" \"stale\"", "")
	if staleErr == nil || !strings.Contains(stale.Diagnostic, "row 2 is stale") {
		t.Fatalf("saved pre-edit row error = %v, diagnostic %q", staleErr, stale.Diagnostic)
	}
}

func TestHPatch2FinalReferencesProjectCollapsedDeletion(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\nbeta\ngamma\n", 0o644)
	result, err := applyForHostAtTest(t, root, "in file.txt\ntype "+row(2, "beta")+` ""`, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	want := "refs 2 type file.txt\n" +
		"1:" + hashLine("alpha") + " alpha\n" +
		"2:" + hashLine("gamma") + " gamma\n"
	if !strings.Contains(result.Report, want) {
		t.Fatalf("deletion references %q lack %q", result.Report, want)
	}
}

func TestHPatch2FinalReferencesAreBoundedAndDeduplicated(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "zero\nx\none\ntwo\nx\nend\n", 0o644)
	script := "in file.txt\ntype " + row(2, "x") + ` "x" 2 "y"`
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	want := "refs 2 type file.txt\n" +
		"1:" + hashLine("zero") + " zero\n" +
		"2:" + hashLine("y") + " y\n" +
		"5:" + hashLine("y") + " y\n" +
		"6:" + hashLine("end") + " end\n"
	if !strings.Contains(result.Report, want) {
		t.Fatalf("bounded references %q lack %q", result.Report, want)
	}
}

func TestHPatch2FinalReferencesPreserveUneditedActiveEmptyFile(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "edited.txt", "old\n", 0o644)
	script := "in edited.txt\ntype " + row(1, "old") + " \"new\"\nnew empty.txt"
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	for _, want := range []string{
		"in empty.txt\n",
		"refs 2 type edited.txt\n",
		"1:" + hashLine("new") + " new\n",
		"1:" + hashLine("") + " \n",
	} {
		if !strings.Contains(result.Report, want) {
			t.Fatalf("report %q lacks %q", result.Report, want)
		}
	}
}

func TestPreviewTextMakesLeadingWhitespaceVisible(t *testing.T) {
	if got, want := previewText("  \tindented value"), `\x20\x20\tindented value`; got != want {
		t.Fatalf("previewText() = %q, want %q", got, want)
	}
}
