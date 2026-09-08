package hpatch

import (
	"cmp"
	"fmt"
	"github.com/pmezard/go-difflib/difflib"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type formatToken struct {
	kind       token.Token
	literal    string
	start, end int
	importSpec bool
	offsets    *formattedOffsetMap
}

type formattedOffsetMap struct {
	beforeLength int
	afterLength  int
	before       []formatToken
	after        []formatToken
	deletions    []whitespaceDeletion
	subsequent   *formattedOffsetMap
}

// newFormattedOffsetMap creates a mapping between pre- and post-formatted Go source offsets.
func newFormattedOffsetMap(before, after string) (*formattedOffsetMap, error) {
	beforeTokens, err := scanFormatTokens(before)
	if err != nil {
		return nil, err
	}
	afterTokens, err := scanFormatTokens(after)
	if err != nil {
		return nil, err
	}

	// Import specs are the only code gofmt reorders or deduplicates. Their
	// scanner identities follow ast.SortImports' canonical spec order.
	imports := make(map[string]formatToken)
	var beforeCode, afterCode []formatToken
	for _, current := range afterTokens {
		if current.importSpec {
			imports[current.literal] = current
		} else {
			afterCode = append(afterCode, current)
		}
	}
	type anchor struct{ before, after formatToken }
	var anchors []anchor
	for _, current := range beforeTokens {
		if !current.importSpec {
			beforeCode = append(beforeCode, current)
			continue
		}
		matched, ok := imports[current.literal]
		if !ok {
			return nil, fmt.Errorf("import %q has no formatted match", current.literal)
		}
		// A deduplicated spec maps to the surviving spec, including endpoints.
		current.offsets, err = newFormattedOffsetMap(before[current.start:current.end], after[matched.start:matched.end])
		if err != nil {
			return nil, err
		}
		anchors = append(anchors, anchor{current, matched})
	}

	keys := func(tokens []formatToken) []string {
		result := make([]string, len(tokens))
		for i, current := range tokens {
			result[i] = strconv.Itoa(int(current.kind)) + ":" + current.literal
		}
		return result
	}
	// Outside imports, formatter-normalized literals and keywords retain
	// their order. Match these in linear time, then align only intervening
	// comment/punctuation gaps. Whole-file sequence matching is quadratic on
	// common source with repeated declarations or expressions.
	semantic := func(current formatToken) bool { return current.kind.IsLiteral() || current.kind.IsKeyword() }
	b, a := 0, 0
	for b < len(beforeCode) || a < len(afterCode) {
		bEnd, aEnd := b, a
		for bEnd < len(beforeCode) && !semantic(beforeCode[bEnd]) {
			bEnd++
		}
		for aEnd < len(afterCode) && !semantic(afterCode[aEnd]) {
			aEnd++
		}
		// Doc comments can gain or lose words and punctuation. Surviving
		// lexical anchors retain coordinates; rewritten gaps project between
		// adjacent anchors rather than rejecting valid formatter output.
		matcher := difflib.NewMatcher(keys(beforeCode[b:bEnd]), keys(afterCode[a:aEnd]))
		for _, block := range matcher.GetMatchingBlocks() {
			for i := range block.Size {
				anchors = append(anchors, anchor{beforeCode[b+block.A+i], afterCode[a+block.B+i]})
			}
		}
		if bEnd == len(beforeCode) && aEnd == len(afterCode) {
			break
		}
		if bEnd == len(beforeCode) || aEnd == len(afterCode) ||
			beforeCode[bEnd].kind != afterCode[aEnd].kind || beforeCode[bEnd].literal != afterCode[aEnd].literal {
			return nil, fmt.Errorf("formatted code has no ordered source correspondence")
		}
		anchors = append(anchors, anchor{beforeCode[bEnd], afterCode[aEnd]})
		b, a = bEnd+1, aEnd+1
	}
	slices.SortFunc(anchors, func(a, b anchor) int { return cmp.Compare(a.before.start, b.before.start) })
	mapping := &formattedOffsetMap{
		beforeLength: len(before),
		afterLength:  len(after),
	}
	for _, current := range anchors {
		mapping.before = append(mapping.before, current.before)
		mapping.after = append(mapping.after, current.after)
	}
	return mapping, nil
}

// scanFormatTokens lexically scans Go source into format tokens for offset mapping.
func scanFormatTokens(source string) ([]formatToken, error) {
	// Parse only complete files: format.Source also accepts declaration and
	// statement fragments, whose imports it does not sort.
	var imports []formatToken
	positions := token.NewFileSet()
	if parsed, err := parser.ParseFile(positions, "", source, parser.ParseComments|parser.SkipObjectResolution); err == nil {
		var originalSpecs []*ast.ImportSpec
		groups := make(map[*ast.ImportSpec]int)
		regionStarts := make(map[*ast.ImportSpec]token.Pos)
		group := 0
		for _, declaration := range parsed.Decls {
			decl, ok := declaration.(*ast.GenDecl)
			if !ok || decl.Tok != token.IMPORT || !decl.Lparen.IsValid() {
				continue
			}
			group++
			regionStart := decl.Lparen + 1
			for i, spec := range decl.Specs {
				line := positions.PositionFor(spec.Pos(), false).Line
				if i == 0 || line > positions.PositionFor(decl.Specs[i-1].End(), false).Line+1 {
					if i > 0 {
						group++
					}
					// SortImports moves comments only from the first spec's
					// physical line onward in each consecutive run. A doc comment
					// before that run remains at the declaration, not at its spec.
					regionStart = max(regionStart, positions.File(spec.Pos()).LineStart(line))
				}
				importSpec := spec.(*ast.ImportSpec)
				originalSpecs = append(originalSpecs, importSpec)
				groups[importSpec] = group
				regionStarts[importSpec] = regionStart
				regionStart = spec.End()
				if importSpec.Comment != nil {
					regionStart = importSpec.Comment.End()
				}
			}
		}
		commentIndex := 0
		for _, spec := range originalSpecs {
			start, end := spec.Pos(), spec.End()
			if spec.Doc != nil && spec.Doc.Pos() >= regionStarts[spec] {
				start = spec.Doc.Pos()
			}
			if spec.Comment != nil {
				end = spec.Comment.End()
			}
			// SortImports owns only specs inside parenthesized declarations.
			// Their leading region starts after '(' or the previous spec, never
			// across the declaration keyword or a previous spec's trailing comment.
			// Same-line leading block comments in that region move with the spec
			// even though the parser does not attach them as ImportSpec.Doc.
			for commentIndex < len(parsed.Comments) && parsed.Comments[commentIndex].End() <= spec.Pos() {
				comments := parsed.Comments[commentIndex]
				if comments.Pos() >= regionStarts[spec] &&
					positions.PositionFor(comments.End(), false).Line == positions.PositionFor(spec.Pos(), false).Line {
					start = min(start, comments.Pos())
				}
				commentIndex++
			}
			name := ""
			if spec.Name != nil {
				name = spec.Name.Name
			}
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, err
			}
			startOffset := positions.File(start).Offset(start)
			lineStart := strings.LastIndexByte(source[:startOffset], '\n') + 1
			if strings.TrimSpace(source[lineStart:startOffset]) == "" {
				startOffset = lineStart
			}
			endOffset := positions.File(end).Offset(end)
			// Keep a complete import row's terminator with its moved contents.
			// A half-open row extent must not acquire the next, unrelated import
			// merely because sorting moves that import across its boundary.
			if newline := strings.IndexByte(source[endOffset:], '\n'); newline >= 0 &&
				strings.TrimSpace(source[endOffset:endOffset+newline]) == "" {
				endOffset += newline + 1
			}
			imports = append(imports, formatToken{kind: token.IMPORT, literal: name + ":" + path,
				start: startOffset, end: endOffset, importSpec: true})
		}
		// Capture physical spans first: SortImports mutates positions, but
		// preserves surviving spec pointers. Use the formatter's actual order
		// and deduplication instead of guessing which equal import survives.
		ast.SortImports(positions, parsed)
		canonical := make(map[*ast.ImportSpec]string)
		for i, spec := range parsed.Imports {
			canonical[spec] = strconv.Itoa(i)
		}
		type importKey struct {
			group       int
			nameAndPath string
		}
		survivors := make(map[importKey]string)
		for i, spec := range originalSpecs {
			if identity, ok := canonical[spec]; ok {
				survivors[importKey{groups[spec], imports[i].literal}] = identity
			}
		}
		identities := make([]string, len(imports))
		for i, spec := range originalSpecs {
			identity, ok := canonical[spec]
			if !ok {
				identity, ok = survivors[importKey{groups[spec], imports[i].literal}]
			}
			if !ok {
				return nil, fmt.Errorf("import %q has no surviving spec", imports[i].literal)
			}
			identities[i] = identity
		}
		for i := range imports {
			imports[i].literal = identities[i]
		}
	}
	files := token.NewFileSet()
	file := files.AddFile("", -1, len(source))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(source), nil, scanner.ScanComments)

	var tokens []formatToken
	for {
		position, kind, literal := lexer.Scan()
		if kind == token.EOF {
			return tokens, nil
		}
		if kind == token.SEMICOLON {
			continue
		}
		if literal == "" {
			literal = kind.String()
		}
		start := file.Offset(position)
		if len(imports) != 0 && start >= imports[0].end {
			imports = imports[1:]
		}
		if len(imports) != 0 && start >= imports[0].start && start < imports[0].end {
			if len(tokens) == 0 || !tokens[len(tokens)-1].importSpec || tokens[len(tokens)-1].start != imports[0].start {
				tokens = append(tokens, imports[0])
			}
			continue
		}
		end := start + len(literal)
		if kind == token.COMMENT {
			// Scanner strips CR bytes; use physical source to retain offsets.
			if strings.HasPrefix(source[start:], "//") {
				end = len(source)
				if newline := strings.IndexByte(source[start:], '\n'); newline >= 0 {
					end = start + newline
				}
			} else {
				end = start + strings.Index(source[start:], "*/") + 2
			}
			cursor := start
			for word := range strings.FieldsFuncSeq(source[start:end], unicode.IsSpace) {
				wordStart := cursor + strings.Index(source[cursor:end], word)
				tokens = append(tokens, formatToken{kind: kind, literal: word, start: wordStart, end: wordStart + len(word)})
				cursor = wordStart + len(word)
			}
			// A standalone comment owns its terminator. Otherwise an exclusive
			// comment-row end could attach to the next, reordered import instead
			// of the comment's surviving boundary row.
			if newline := strings.IndexByte(source[end:], '\n'); newline >= 0 &&
				strings.TrimSpace(source[end:end+newline]) == "" {
				tokens[len(tokens)-1].end = end + newline + 1
			}
			continue
		}
		if kind == token.STRING && source[start] == '`' {
			end = start + 2 + strings.IndexByte(source[start+1:], '`')
		}
		if kind == token.STRING {
			// Import deduplication can retain an equivalent path written with
			// different quoting. Ordered string identity uses decoded contents.
			decoded, err := strconv.Unquote(literal)
			if err != nil {
				return nil, err
			}
			literal = decoded
		}
		if kind == token.INT || kind == token.FLOAT || kind == token.IMAG {
			// Canonicalize identity with the formatter itself, not numeric value:
			// equal-valued but differently spelled literals remain distinct.
			var canonical strings.Builder
			if err := format.Node(&canonical, token.NewFileSet(), &ast.BasicLit{Kind: kind, Value: literal}); err != nil {
				return nil, err
			}
			literal = canonical.String()
		}
		tokens = append(tokens, formatToken{
			kind:    kind,
			literal: literal,
			start:   start,
			end:     end,
		})
	}
}

// mapOffset maps a pre-transformation offset to its post-transformation position.
func (m *formattedOffsetMap) mapOffset(offset int) int {
	if m == nil {
		return offset
	}
	var mapped int
	if len(m.deletions) != 0 {
		mapped = m.mapDeletedOffset(offset)
	} else {
		mapped = m.mapTokenOffset(offset)
	}
	return m.subsequent.mapOffset(mapped)
}

// mapExtent projects the whole half-open source extent. Formatting can move
// an interior import beyond either endpoint, so two mapped points do not bound
// its result. Consumers retain their own inclusive-row or boundary-row policy.
func (m *formattedOffsetMap) mapExtent(extent renderedSpan) renderedSpan {
	if m == nil {
		return extent
	}
	if extent.start == extent.end {
		point := m.mapOffset(extent.start)
		return renderedSpan{start: point, end: point}
	}
	if len(m.deletions) != 0 {
		return m.subsequent.mapExtent(renderedSpan{start: m.mapDeletedOffset(extent.start), end: m.mapDeletedOffset(extent.end)})
	}
	start, end := m.mapTokenOffset(extent.start), m.mapTokenOffset(extent.end)
	// At a shared anchor boundary, the exclusive end belongs to the anchor
	// on its left, not a possibly reordered following import on its right.
	next := sort.Search(len(m.before), func(i int) bool { return m.before[i].start >= extent.end })
	if previous := next - 1; previous >= 0 && m.before[previous].end == extent.end {
		end = m.after[previous].end
	}
	result := renderedSpan{start: min(start, end), end: max(start, end)}
	first := sort.Search(len(m.before), func(i int) bool { return m.before[i].end > extent.start })
	for i := first; i < len(m.before) && m.before[i].start < extent.end; i++ {
		before, after := m.before[i], m.after[i]
		part := renderedSpan{start: max(extent.start, before.start) - before.start, end: min(extent.end, before.end) - before.start}
		if before.offsets != nil {
			part = before.offsets.mapExtent(part)
		} else {
			part.start = min(part.start, after.end-after.start)
			if part.end == before.end-before.start {
				part.end = after.end - after.start
			} else {
				part.end = min(part.end, after.end-after.start)
			}
		}
		result.start = min(result.start, after.start+part.start)
		result.end = max(result.end, after.start+part.end)
	}
	return m.subsequent.mapExtent(result)
}

// mapDeletedOffset maps an offset through whitespace deletions.
func (m *formattedOffsetMap) mapDeletedOffset(offset int) int {
	offset = min(max(offset, 0), m.beforeLength)
	removed := 0
	for _, deletion := range m.deletions {
		if offset <= deletion.start {
			return offset - removed
		}
		if offset < deletion.end {
			return deletion.start - removed
		}
		removed += deletion.end - deletion.start
	}
	return offset - removed
}

// mapTokenOffset maps an offset through token-based formatting changes.
func (m *formattedOffsetMap) mapTokenOffset(offset int) int {
	offset = min(max(offset, 0), m.beforeLength)
	next := sort.Search(len(m.before), func(index int) bool {
		return m.before[index].start >= offset
	})
	if next < len(m.before) && m.before[next].start == offset {
		return m.after[next].start
	}
	if previous := next - 1; previous >= 0 && offset <= m.before[previous].end {
		if offset == m.before[previous].end {
			return m.after[previous].end
		}
		if nested := m.before[previous].offsets; nested != nil {
			return m.after[previous].start + nested.mapOffset(offset-m.before[previous].start)
		}
		return m.after[previous].start + min(offset-m.before[previous].start, m.after[previous].end-m.after[previous].start)
	}

	beforeStart, afterStart := 0, 0
	if next > 0 {
		beforeStart = m.before[next-1].end
		afterStart = m.after[next-1].end
	}
	beforeEnd, afterEnd := m.beforeLength, m.afterLength
	if next < len(m.before) {
		beforeEnd = m.before[next].start
		afterEnd = m.after[next].start
	}
	if beforeEnd == beforeStart {
		return min(max(afterStart, 0), m.afterLength)
	}
	if afterStart > afterEnd {
		if offset-beforeStart <= beforeEnd-offset {
			return min(afterStart, m.afterLength)
		}
		return max(afterEnd, 0)
	}
	return afterStart + (offset-beforeStart)*(afterEnd-afterStart)/(beforeEnd-beforeStart)
}

// newWhitespaceOffsetMap creates an offset map for whitespace deletions.
func newWhitespaceOffsetMap(contentLength int, deletions []whitespaceDeletion) *formattedOffsetMap {
	removed := 0
	for _, deletion := range deletions {
		removed += deletion.end - deletion.start
	}
	return &formattedOffsetMap{
		beforeLength: contentLength,
		afterLength:  contentLength - removed,
		deletions:    deletions,
	}
}
