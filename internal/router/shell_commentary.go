package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const (
	shellCommentaryStartCommand   = "__hpatch_commentary_start"
	shellCommentaryFinishCommand  = "__hpatch_commentary_finish"
	shellCommentaryNoopCommand    = "__hpatch_commentary_noop"
	shellCommentaryUnboundID      = "-"
	shellCommentaryNoopDefinition = "__hpatch_commentary_noop() { __hpatch_commentary_noop_status=; }"
)

type shellCommentaryEvent struct {
	Text    string
	Form    string
	Outcome string
	Reason  string
}

func shellCommentaryVisibleText(event shellCommentaryEvent) string {
	if event.Reason == "" {
		return event.Text
	}
	return event.Text + "\nReason: " + event.Reason
}

type shellCommentarySink interface {
	Publish(context.Context, shellCommentaryEvent) error
	Complete(context.Context) error
}

type discardShellCommentarySink struct{}

func (discardShellCommentarySink) Publish(context.Context, shellCommentaryEvent) error { return nil }
func (discardShellCommentarySink) Complete(context.Context) error                      { return nil }

type shellCommentaryRuntime struct {
	sink shellCommentarySink

	mu               sync.Mutex
	active           map[string]shellCommentaryEvent
	nextToken        uint64
	failurePublished bool
}

func newShellCommentaryRuntime(sink shellCommentarySink) *shellCommentaryRuntime {
	return &shellCommentaryRuntime{sink: sink, active: make(map[string]shellCommentaryEvent)}
}

func (r *shellCommentaryRuntime) call(ctx context.Context, stdout io.Writer, arguments []string) ([]string, error) {
	switch arguments[0] {
	case shellCommentaryStartCommand:
		if len(arguments) < 4 {
			return nil, errors.New("shell commentary start marker is malformed")
		}
		event := shellCommentaryEvent{Text: strings.Join(arguments[4:], " "), Form: arguments[2]}
		_ = r.sink.Publish(ctx, event)
		if arguments[1] == shellCommentaryUnboundID {
			return []string{shellCommentaryNoopCommand}, nil
		}
		r.mu.Lock()
		if arguments[3] != "" {
			delete(r.active, arguments[3])
		}
		r.nextToken++
		token := strconv.FormatUint(r.nextToken, 10)
		r.active[token] = event
		r.mu.Unlock()
		if _, err := io.WriteString(stdout, token); err != nil {
			return nil, fmt.Errorf("write shell commentary token: %w", err)
		}
		return []string{shellCommentaryNoopCommand}, nil

	case shellCommentaryFinishCommand:
		if len(arguments) != 3 {
			return nil, errors.New("shell commentary finish marker is malformed")
		}
		status, err := strconv.Atoi(arguments[2])
		if err != nil || status < 0 || status > 255 {
			return nil, errors.New("shell commentary status is malformed")
		}
		if arguments[1] == "" {
			if status == 0 {
				return []string{shellCommentaryNoopCommand}, nil
			}
			return nil, interp.ExitStatus(status)
		}
		r.mu.Lock()
		event, exists := r.active[arguments[1]]
		delete(r.active, arguments[1])
		r.mu.Unlock()
		if !exists {
			return nil, errors.New("shell commentary status has no active label")
		}
		if status != 0 && event.Text != "" {
			event.Text = "Failed: " + event.Text
			event.Outcome = "failure"
			event.Reason = fmt.Sprintf("exit status %d", status)
			event.Form = ""
			_ = r.sink.Publish(ctx, event)
			r.mu.Lock()
			r.failurePublished = true
			r.mu.Unlock()
		}
		if status == 0 {
			return []string{shellCommentaryNoopCommand}, nil
		}
		// A labelled standalone command is an operation boundary, so a
		// nonzero marker runs outside a subshell and ends evaluation even when
		// the script did not enable errexit. Zero and skipped markers run in a
		// subshell, containing this private status without invoking a command
		// that a shell function could replace.
		return nil, interp.ExitStatus(status)
	default:
		return arguments, nil
	}
}

func (r *shellCommentaryRuntime) terminal(ctx context.Context, outcome, reason string) error {
	r.mu.Lock()
	active := r.active
	r.active = make(map[string]shellCommentaryEvent)
	failurePublished := r.failurePublished
	r.mu.Unlock()
	terminalPublished := failurePublished
	identifiers := slices.Sorted(maps.Keys(active))
	for _, identifier := range identifiers {
		event := active[identifier]
		if event.Text == "" {
			continue
		}
		switch outcome {
		case "cancelled":
			event.Text = "Cancelled: " + event.Text
		case "timeout":
			event.Text = "Timed out: " + event.Text
		default:
			event.Text = "Failed: " + event.Text
			outcome = "failure"
		}
		event.Outcome = outcome
		event.Reason = reason
		event.Form = ""
		_ = r.sink.Publish(ctx, event)
		terminalPublished = true
	}
	if !terminalPublished {
		text := "Failed."
		terminalOutcome := "failure"
		switch outcome {
		case "cancelled":
			text = "Cancelled."
			terminalOutcome = outcome
		case "timeout":
			text = "Timed out."
			terminalOutcome = outcome
		}
		_ = r.sink.Publish(ctx, shellCommentaryEvent{Text: text, Outcome: terminalOutcome, Reason: reason})
	}
	return nil
}

func (r *shellCommentaryRuntime) complete() {
	r.mu.Lock()
	clear(r.active)
	r.mu.Unlock()
}

func instrumentShellCommentary(program *syntax.File, source string) (bool, error) {
	sequence := 0
	changed := false
	var instrumentErr error
	syntax.Walk(program, func(node syntax.Node) bool {
		if instrumentErr != nil {
			return false
		}
		process := func(statements *[]*syntax.Stmt) {
			if instrumentErr != nil {
				return
			}
			var listChanged bool
			*statements, listChanged, instrumentErr = instrumentShellStatementList(*statements, source, &sequence)
			changed = changed || listChanged
		}
		switch node := node.(type) {
		case *syntax.File:
			process(&node.Stmts)
		case *syntax.Block:
			process(&node.Stmts)
		case *syntax.Subshell:
			process(&node.Stmts)
		case *syntax.IfClause:
			process(&node.Cond)
			process(&node.Then)
		case *syntax.WhileClause:
			process(&node.Cond)
			process(&node.Do)
		case *syntax.ForClause:
			process(&node.Do)
		case *syntax.CaseItem:
			process(&node.Stmts)
		case *syntax.CmdSubst:
			process(&node.Stmts)
		}
		return true
	})
	return changed, instrumentErr
}

func shellArgumentsHaveCommentary(arguments []string) bool {
	if len(arguments) < 2 {
		return false
	}
	interpreter := shellInterpreterName(arguments[0])
	variant := syntax.LangBash
	if interpreter == "sh" {
		variant = syntax.LangPOSIX
	} else if interpreter != "bash" {
		return false
	}
	source := arguments[len(arguments)-1]
	program, err := syntax.NewParser(syntax.Variant(variant)).Parse(strings.NewReader(source), "")
	if err != nil {
		return false
	}
	present, err := instrumentShellCommentary(program, source)
	return err == nil && present
}

func instrumentShellStatementList(statements []*syntax.Stmt, source string, sequence *int) ([]*syntax.Stmt, bool, error) {
	changed := false
	result := make([]*syntax.Stmt, 0, len(statements))
	var pending []*syntax.CallExpr
	forms := make(map[*syntax.CallExpr]string)
	for _, statement := range statements {
		calls, trailing, hasAction := shellCommentaryCalls(statement.Cmd)
		for _, call := range calls {
			end, ok := shellCommentaryStatementEnd(statement, call)
			if !ok {
				return nil, false, errors.New("shell commentary statement range is unavailable")
			}
			form, err := shellCommentarySourceThrough(call, end, source)
			if err != nil {
				return nil, false, err
			}
			forms[call] = form
			changed = true
		}
		if len(calls) != 0 {
			stripShellCommentaryRedirections(statement)
		}
		if !hasAction {
			pending = append(pending, calls...)
			result = append(result, statement)
			continue
		}
		trailingSet := make(map[*syntax.CallExpr]struct{}, len(trailing))
		for _, call := range trailing {
			trailingSet[call] = struct{}{}
			if err := setShellCommentaryCall(call, shellCommentaryUnboundID, forms[call]); err != nil {
				return nil, false, err
			}
		}
		bound := make([]*syntax.CallExpr, 0, len(calls)-len(trailing))
		for _, call := range calls {
			if _, unbound := trailingSet[call]; !unbound {
				bound = append(bound, call)
			}
		}
		if len(pending) != 0 {
			for _, call := range pending[:len(pending)-1] {
				if err := setShellCommentaryCall(call, shellCommentaryUnboundID, forms[call]); err != nil {
					return nil, false, err
				}
			}
			bound = append(bound, pending[len(pending)-1])
			pending = nil
		}
		result = append(result, statement)
		if len(bound) == 0 {
			continue
		}
		*sequence++
		id := strconv.Itoa(*sequence)
		for _, call := range bound {
			if err := setShellCommentaryCall(call, id, forms[call]); err != nil {
				return nil, false, err
			}
		}
		finish, err := shellCommentaryFinishStatement(id)
		if err != nil {
			return nil, false, err
		}
		result = append(result, finish)
	}
	for _, call := range pending {
		if err := setShellCommentaryCall(call, shellCommentaryUnboundID, forms[call]); err != nil {
			return nil, false, err
		}
	}
	return result, changed, nil
}

func stripShellCommentaryRedirections(statement *syntax.Stmt) {
	if call, ok := statement.Cmd.(*syntax.CallExpr); ok && shellCallName(call) == "commentary" {
		statement.Redirs = nil
		return
	}
	switch command := statement.Cmd.(type) {
	case *syntax.BinaryCmd:
		stripShellCommentaryRedirections(command.X)
		stripShellCommentaryRedirections(command.Y)
	case *syntax.TimeClause:
		stripShellCommentaryRedirections(command.Stmt)
	case *syntax.CoprocClause:
		stripShellCommentaryRedirections(command.Stmt)
	}
}

func shellCommentaryStatementEnd(statement *syntax.Stmt, target *syntax.CallExpr) (syntax.Pos, bool) {
	if call, ok := statement.Cmd.(*syntax.CallExpr); ok {
		if call == target {
			return statement.End(), true
		}
		return syntax.Pos{}, false
	}
	switch command := statement.Cmd.(type) {
	case *syntax.BinaryCmd:
		if end, ok := shellCommentaryStatementEnd(command.X, target); ok {
			return end, true
		}
		return shellCommentaryStatementEnd(command.Y, target)
	case *syntax.TimeClause:
		return shellCommentaryStatementEnd(command.Stmt, target)
	case *syntax.CoprocClause:
		return shellCommentaryStatementEnd(command.Stmt, target)
	default:
		return syntax.Pos{}, false
	}
}

func shellCommentaryCalls(command syntax.Command) (calls, trailing []*syntax.CallExpr, hasAction bool) {
	switch command := command.(type) {
	case *syntax.CallExpr:
		if shellCallName(command) == "commentary" {
			return []*syntax.CallExpr{command}, []*syntax.CallExpr{command}, false
		}
		return nil, nil, true
	case *syntax.BinaryCmd:
		leftCalls, leftTrailing, leftAction := shellCommentaryCalls(command.X.Cmd)
		rightCalls, rightTrailing, rightAction := shellCommentaryCalls(command.Y.Cmd)
		calls = append(leftCalls, rightCalls...)
		if !rightAction {
			trailing = append(leftTrailing, rightTrailing...)
		} else {
			trailing = rightTrailing
		}
		return calls, trailing, leftAction || rightAction
	case *syntax.TimeClause:
		return shellCommentaryCalls(command.Stmt.Cmd)
	case *syntax.CoprocClause:
		return shellCommentaryCalls(command.Stmt.Cmd)
	}
	return nil, nil, true
}

func shellCallName(call *syntax.CallExpr) string {
	if len(call.Args) == 0 || len(call.Args[0].Parts) != 1 {
		return ""
	}
	literal, ok := call.Args[0].Parts[0].(*syntax.Lit)
	if !ok {
		return ""
	}
	return literal.Value
}

func shellCommentarySourceThrough(call *syntax.CallExpr, endPosition syntax.Pos, source string) (string, error) {
	start, end := int(call.Pos().Offset()), int(endPosition.Offset())
	if start < 0 || end < start || end > len(source) {
		return "", errors.New("shell commentary source range is invalid")
	}
	return source[start:end], nil
}

func setShellCommentaryCall(call *syntax.CallExpr, id, form string) error {
	originalArgs := call.Args[1:]
	previous := syntax.WordPart(&syntax.DblQuoted{})
	if id != shellCommentaryUnboundID {
		previous = &syntax.DblQuoted{Parts: []syntax.WordPart{&syntax.ParamExp{
			Short: true, Param: &syntax.Lit{Value: shellCommentaryTokenVariable(id)},
		}}}
	}
	words := []*syntax.Word{
		{Parts: []syntax.WordPart{&syntax.Lit{Value: shellCommentaryStartCommand}}},
		{Parts: []syntax.WordPart{&syntax.Lit{Value: id}}},
		{Parts: []syntax.WordPart{&syntax.SglQuoted{Value: form}}},
		{Parts: []syntax.WordPart{previous}},
	}
	helper, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(
		strings.NewReader(shellCommentaryNoopDefinition),
		"",
	)
	if err != nil || len(helper.Stmts) != 1 {
		return errors.New("build shell commentary private helper")
	}
	startCall := &syntax.CallExpr{
		Assigns: call.Assigns,
		Args:    append(words, originalArgs...),
	}
	variable := "__hpatch_commentary_unbound"
	if id != shellCommentaryUnboundID {
		variable = shellCommentaryTokenVariable(id)
	}
	call.Assigns = []*syntax.Assign{{
		Name: &syntax.Lit{Value: variable},
		Value: &syntax.Word{Parts: []syntax.WordPart{&syntax.DblQuoted{
			Parts: []syntax.WordPart{&syntax.CmdSubst{Stmts: []*syntax.Stmt{helper.Stmts[0], {Cmd: startCall}}}},
		}}},
	}}
	call.Args = nil
	return nil
}

func shellCommentaryFinishStatement(id string) (*syntax.Stmt, error) {
	tokenVariable := shellCommentaryTokenVariable(id)
	statusVariable := "__hpatch_commentary_status_" + id
	program, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(
		strings.NewReader(`{ `+statusVariable+`=$?; `+shellCommentaryNoopDefinition+`; case "$`+tokenVariable+`" in "") (`+
			shellCommentaryFinishCommand+` "" "$`+statusVariable+`");; *) case "$`+statusVariable+
			`" in 0) (`+shellCommentaryFinishCommand+` "$`+tokenVariable+`" "0");; *) `+
			shellCommentaryFinishCommand+` "$`+tokenVariable+`" "$`+statusVariable+`";; esac;; esac; }`),
		"",
	)
	if err != nil || len(program.Stmts) != 1 {
		return nil, errors.New("build shell commentary status marker")
	}
	return program.Stmts[0], nil
}

func shellCommentaryTokenVariable(id string) string {
	return "__hpatch_commentary_token_" + id
}

func shellCommentaryCallHandler(runtime *shellCommentaryRuntime) interp.CallHandlerFunc {
	return func(ctx context.Context, arguments []string) ([]string, error) {
		return runtime.call(ctx, interp.HandlerCtx(ctx).Stdout, arguments)
	}
}
