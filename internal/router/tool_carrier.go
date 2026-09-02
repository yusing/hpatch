package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const (
	nativeExecCommandToolName    = "exec_command"
	hpatchApplyExecMarker        = "// hpatch-proxy: apply translated patch\n"
	hpatchNativeApplyMarker      = "# hpatch-proxy: apply translated patch\n"
	hpatchNativeReportMarker     = "# hpatch-proxy: return hpatch report\n"
	hpatchNativeDiagnosticMarker = "# hpatch-proxy: return hpatch diagnostic "
)

type codeModeCarrierKind string

const (
	codeModeCarrierCustom   codeModeCarrierKind = "custom"
	codeModeCarrierFunction codeModeCarrierKind = "function"
)

type codeModeCarrierCatalog map[string]codeModeCarrierKind

func buildCodeModeCarrierCatalog(fields map[string]json.RawMessage, registry *toolRegistry) (codeModeCarrierCatalog, error) {
	catalog := make(codeModeCarrierCatalog)
	add := func(tool map[string]json.RawMessage) error {
		name := jsonString(tool, "name")
		if name == "" {
			return nil
		}
		if _, registered := registry.contribution(name); registered {
			return fmt.Errorf("responses request already defines registered tool %s", name)
		}
		if name == applyPatchToolName {
			return nil
		}
		var kind codeModeCarrierKind
		switch jsonString(tool, "type") {
		case string(codeModeCarrierCustom):
			kind = codeModeCarrierCustom
		case string(codeModeCarrierFunction):
			kind = codeModeCarrierFunction
		default:
			return nil
		}
		if _, exists := catalog[name]; exists {
			return fmt.Errorf("code mode carrier %q is defined more than once", name)
		}
		catalog[name] = kind
		return nil
	}

	if rawTools, exists := fields["tools"]; exists {
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(rawTools, &tools); err != nil {
			return nil, fmt.Errorf("decode Responses tools for carrier catalog: %w", err)
		}
		for _, tool := range tools {
			if err := add(tool); err != nil {
				return nil, err
			}
		}
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(fields["input"], &items) == nil {
		for _, item := range items {
			if jsonString(item, "type") != "additional_tools" {
				continue
			}
			var additionalTools []map[string]json.RawMessage
			if err := json.Unmarshal(item["tools"], &additionalTools); err != nil {
				return nil, fmt.Errorf("decode additional tools for carrier catalog: %w", err)
			}
			for _, additionalTool := range additionalTools {
				if jsonString(additionalTool, "type") != "namespace" {
					if err := add(additionalTool); err != nil {
						return nil, err
					}
					continue
				}
				var tools []map[string]json.RawMessage
				if err := json.Unmarshal(additionalTool["tools"], &tools); err != nil {
					return nil, fmt.Errorf("decode namespaced tools for carrier catalog: %w", err)
				}
				for _, tool := range tools {
					if err := add(tool); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return catalog, nil
}

func (catalog codeModeCarrierCatalog) require(name string, kind codeModeCarrierKind) error {
	if name == "" {
		return errors.New("translator returned an empty carrier name")
	}
	available, ok := catalog[name]
	if !ok {
		return fmt.Errorf("Code Mode carrier %q is unavailable", name)
	}
	if available != kind {
		return fmt.Errorf("Code Mode carrier %q has kind %q, not %q", name, available, kind)
	}
	return nil
}

func carrierItemType(kind codeModeCarrierKind) string {
	if kind == codeModeCarrierFunction {
		return "function_call"
	}
	return "custom_tool_call"
}

func carrierOutputItemType(kind codeModeCarrierKind) string {
	if kind == codeModeCarrierFunction {
		return "function_call_output"
	}
	return "custom_tool_call_output"
}

func carrierPayloadField(kind codeModeCarrierKind) string {
	if kind == codeModeCarrierFunction {
		return "arguments"
	}
	return "input"
}

func renderCarrierItem(item map[string]json.RawMessage, kind codeModeCarrierKind, name, payload string) {
	item["type"] = mustMarshalJSON(carrierItemType(kind))
	item["name"] = mustMarshalJSON(name)
	delete(item, "input")
	delete(item, "arguments")
	item[carrierPayloadField(kind)] = mustMarshalJSON(payload)
}

func renderCarrierDoneEvent(payload []byte, kind codeModeCarrierKind, carrierPayload string) ([]byte, error) {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, errors.New("decode tool-call input completion event")
	}
	eventType := "response.custom_tool_call_input.done"
	if kind == codeModeCarrierFunction {
		eventType = "response.function_call_arguments.done"
	}
	event["type"] = mustMarshalJSON(eventType)
	delete(event, "input")
	delete(event, "arguments")
	event[carrierPayloadField(kind)] = mustMarshalJSON(carrierPayload)
	return json.Marshal(event)
}

func shellQuoteArgument(value string) string {
	quoted, err := syntax.Quote(value, syntax.LangBash)
	if err == nil {
		return quoted
	}
	// NUL cannot be represented in an argv value. Preserve the prior carrier
	// construction so the native executor remains the owner of that rejection.
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func hpatchApplyExecInput(patch, report string) string {
	return hpatchApplyExecMarker +
		"await tools.apply_patch(" + strconv.Quote(patch) + ");\n" +
		"text(" + strconv.Quote(report) + ");"
}

func hpatchDiagnosticExecInput(diagnostic string) string {
	return "text(" + strconv.Quote(diagnostic) + ");"
}

func nativeExecArguments(command string) string {
	return string(mustMarshalJSON(map[string]any{"cmd": command, "login": false}))
}

func hpatchNativeApplyArguments(patch, report string) string {
	command := hpatchNativeApplyMarker +
		"hpatch_apply_output=$(printf %s " + shellQuoteArgument(patch) + " | apply_patch; " +
		"hpatch_status=$?; printf x; exit \"$hpatch_status\")\n" +
		"hpatch_status=$?\n" +
		"hpatch_apply_output=${hpatch_apply_output%x}\n" +
		"if [ \"$hpatch_status\" -ne 0 ]; then printf %s \"$hpatch_apply_output\"; exit \"$hpatch_status\"; fi\n" +
		"printf %s " + shellQuoteArgument(report)
	return nativeExecArguments(command)
}

func nativeTextExecArguments(text string) string {
	return nativeExecArguments("printf %s " + shellQuoteArgument(text))
}

func hpatchNativeReportArguments(report string) string {
	return nativeExecArguments(hpatchNativeReportMarker + "printf %s " + shellQuoteArgument(report))
}

func hpatchNativeDiagnosticArguments(diagnostic string) string {
	command := hpatchNativeDiagnosticMarker + strconv.Quote(diagnostic) + "\nprintf %s " + shellQuoteArgument(diagnostic)
	return nativeExecArguments(command)
}

func workerCommand(executable string, arguments []string) string {
	command := shellQuoteArgument(executable)
	for _, argument := range arguments {
		command += " " + shellQuoteArgument(argument)
	}
	return command
}

func (registry *toolRegistry) directBashExecCommand(arguments []string) (string, bool) {
	if len(arguments) != 2 || arguments[0] != "bash" || arguments[1] == "" {
		return "", false
	}
	command := strings.TrimSuffix(arguments[1], "\n")
	command = strings.TrimSuffix(command, "\r")
	if command == "" || strings.ContainsAny(command, "\r\n") {
		return "", false
	}
	program, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil || len(program.Stmts) != 1 {
		return "", false
	}
	statement := program.Stmts[0]
	call, ok := statement.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 || statement.Semicolon.IsValid() || statement.Negated ||
		statement.Background || statement.Coprocess || statement.Disown {
		return "", false
	}
	staticCommand := true
	syntax.Walk(call.Args[0], func(node syntax.Node) bool {
		// Walk reports nil after visiting each node's children.
		if node == nil {
			return true
		}
		switch node.(type) {
		case *syntax.Word, *syntax.Lit, *syntax.SglQuoted, *syntax.DblQuoted:
			return true
		default:
			staticCommand = false
			return false
		}
	})
	commandName, err := expand.Literal(nil, call.Args[0])
	if err != nil || !staticCommand || commandName == "" || interp.IsBuiltin(commandName) {
		return "", false
	}
	if contribution, exists := registry.contribution(commandName); exists &&
		contribution.PluginID == builtinToolsPluginID && !contribution.ModelVisible {
		return "", false
	}
	nestedCommand := false
	syntax.Walk(statement, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			nestedCommand = true
			return false
		default:
			return true
		}
	})
	if nestedCommand {
		return "", false
	}
	return command, true
}

func (registry *toolRegistry) execCarrierInput(
	contribution toolContribution,
	sourceInput string,
	arguments []string,
	template string,
	params map[string]json.RawMessage,
	resultMetadata ...map[string]json.RawMessage,
) (string, error) {
	command, err := registry.execCarrierCommand(contribution, sourceInput, arguments, template)
	if err != nil {
		return "", err
	}
	return workerCommandExecInputWithResult(command, params, contribution.PluginID == builtinToolsPluginID && contribution.Name == "shell", resultMetadata...)
}

func (registry *toolRegistry) execCarrierCommand(
	contribution toolContribution,
	sourceInput string,
	arguments []string,
	template string,
) (string, error) {
	if registry == nil {
		return "", errors.New("tool registry is unavailable")
	}
	builtinShell := contribution.PluginID == builtinToolsPluginID && contribution.Name == "shell"
	if !builtinShell {
		if _, ok := registry.wrapper(contribution.Name); !ok {
			return "", fmt.Errorf("%s worker is unavailable", contribution.Name)
		}
	}
	command := workerCommand(contribution.Name, arguments)
	if builtinShell && template == "" && len(arguments) > 0 && arguments[len(arguments)-1] == sourceInput {
		if direct, ok := registry.directBashExecCommand(arguments); ok {
			command = direct
		}
	}
	if template != "" {
		if strings.Count(template, "{.}") != 1 {
			return "", errors.New("exec command template must contain exactly one {.} placeholder")
		}
		command = strings.Replace(template, "{.}", command, 1)
	}
	return command, nil
}

func (registry *toolRegistry) nativeExecCarrierArguments(
	contribution toolContribution,
	sourceInput string,
	arguments []string,
	template string,
	params map[string]json.RawMessage,
	resultMetadata map[string]json.RawMessage,
) (string, error) {
	command, err := registry.execCarrierCommand(contribution, sourceInput, arguments, template)
	if err != nil {
		return "", err
	}
	if len(resultMetadata) != 0 {
		metadata := string(mustMarshalJSON(resultMetadata))
		command += "\nhpatch_status=$?\nprintf '\\n%s\\n' " + shellQuoteArgument(metadata) + "\nexit \"$hpatch_status\""
	}
	argumentsObject, err := execCommandArguments(command, params)
	if err != nil {
		return "", err
	}
	return string(mustMarshalJSON(argumentsObject)), nil
}

func execCommandArguments(command string, params map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if _, exists := params["cmd"]; exists {
		return nil, errors.New("exec params must not contain cmd")
	}
	if login, exists := params["login"]; exists && !bytes.Equal(bytes.TrimSpace(login), []byte("false")) {
		return nil, errors.New("exec params login must be false")
	}
	argumentsObject := maps.Clone(params)
	if argumentsObject == nil {
		argumentsObject = make(map[string]json.RawMessage)
	}
	argumentsObject["cmd"] = mustMarshalJSON(command)
	if _, exists := argumentsObject["login"]; !exists {
		argumentsObject["login"] = mustMarshalJSON(false)
	}
	return argumentsObject, nil
}

func workerCommandExecInputWithResult(command string, params map[string]json.RawMessage, forwardNativeResult bool, resultMetadata ...map[string]json.RawMessage) (string, error) {
	arguments, err := execCommandArguments(command, params)
	if err != nil {
		return "", err
	}
	resultOutput := "text(result.output);"
	if forwardNativeResult || len(resultMetadata) > 0 && len(resultMetadata[0]) > 0 {
		resultOutput = "text(JSON.stringify(result));"
		if len(resultMetadata) > 0 && len(resultMetadata[0]) > 0 {
			resultOutput = "text(JSON.stringify(Object.assign({}, result, " + string(mustMarshalJSON(resultMetadata[0])) + ")));"
		}
	}
	return "const result = await tools.exec_command(" + string(mustMarshalJSON(arguments)) + ");\n" +
		resultOutput, nil
}

func misuseWarningProjection(warning string) string {
	return "text(" + string(mustMarshalJSON(warning+"\n")) + ");\n"
}

func insertExecCommandWarning(input, warning string) (string, string, bool, error) {
	call := strings.Index(input, codeModeExecCallPrefix)
	if call < 0 {
		return input, "", false, errors.New("Code Mode input has no exec_command call")
	}
	projectionStart := -1
	for _, projection := range []string{
		codeModeOutputProjection,
		codeModeJSONProjection,
		codeModeMetadataProjection,
	} {
		if index := strings.Index(input[call:], "\n"+projection); index >= 0 {
			index += call + 1
			if projectionStart < 0 || index < projectionStart {
				projectionStart = index
			}
		}
	}
	if projectionStart < 0 {
		return input, "", false, errors.New("Code Mode input has no result projection")
	}
	warningInput := misuseWarningProjection(warning)
	if strings.Contains(input[call:projectionStart], warningInput) {
		return input, warningInput, false, nil
	}
	return input[:projectionStart] + warningInput + input[projectionStart:], warningInput, true, nil
}

func (h hpatchHistory) carrierInput() string {
	if h.pluginID != "" || h.carrierKind != "" {
		return h.carrierPayload
	}
	if h.translationError != "" {
		return hpatchDiagnosticExecInput(h.translationError)
	}
	if h.applied || h.alreadySatisfied {
		return hpatchDiagnosticExecInput(h.report)
	}
	return hpatchApplyExecInput(h.patch, h.report)
}

func (h hpatchHistory) effectiveCarrierKind() codeModeCarrierKind {
	if h.carrierKind != "" {
		return h.carrierKind
	}
	return codeModeCarrierCustom
}

const (
	nativeExecCommandWarning = "Warning: Use `functions.shell`"
	lunaShellRecoveryWarning = "Recovered Code Mode JavaScript submitted through `functions.shell`; use `functions.exec` directly next time"

	codeModeExecCallPrefix        = "const result = await tools.exec_command("
	codeModeOutputProjection      = "text(result.output);"
	codeModeJSONProjection        = "text(JSON.stringify(result));"
	codeModeMetadataProjection    = "text(JSON.stringify(Object.assign({}, result, "
	codeModeMetadataProjectionEnd = ")));"

	shellInterpreterNamePattern = `python(?:[0-9]+(?:\.[0-9]+)*)?|pypy[0-9]*|node(?:js)?|bun|` +
		`bash|dash|fish|ksh|mksh|sh|yash|zsh|perl|ruby|php|lua(?:jit)?|` +
		`r(?:script)?|psql|mysql|sqlite3|pwsh|powershell`
)

var (
	codeModeToolProgramPattern = regexp.MustCompile(
		`^(?:(?:const|let|var)[ \t]+[A-Za-z_$][A-Za-z0-9_$]*[ \t]*=[ \t]*)?await[ \t]+tools\.[A-Za-z_$][A-Za-z0-9_$]*[ \t]*\(`,
	)
	codeModeProjectionPattern             = regexp.MustCompile(`(?m)(?:^|;)[ \t]*(?:text|image|audio|generatedImage)[ \t]*\(`)
	shellHeredocPattern                   = regexp.MustCompile(`<<-?`)
	shellInterpreterCommandWrapperPattern = regexp.MustCompile(
		`(?i)(?:^|\\[nrt]|[^[:alnum:]_./+-])/?(?:[[:alnum:]_.+-]+/)*(` + shellInterpreterNamePattern + `)` +
			`((?:[ \t]+-[^ \t\r\n]+)*)[ \t]+(-[[:alpha:]]*[cer]|--(?:command|execute))(?:[^[:alpha:]]|$)`,
	)
	shellInterpreterHeredocPattern = regexp.MustCompile(
		`(?i)(?:^|\\[nrt]|[^[:alnum:]_./+-])/?(?:[[:alnum:]_.+-]+/)*(` + shellInterpreterNamePattern + `)` +
			`((?:[ \t]+-[^ \t\r\n<]+)*)[ \t]+(-?[ \t]*<<-?)`,
	)
)

type shellWrapperMisuse struct {
	Kind            string
	Interpreter     string
	InterpreterArgs []string
	wrapper         string
}

// Match the raw input intentionally: every heredoc and quoted or commented example warns too.
func shellInterpreterWrapperMisuses(contribution toolContribution, input string) []shellWrapperMisuse {
	if contribution.PluginID != builtinToolsPluginID || contribution.Name != "shell" {
		return nil
	}

	var misuses []shellWrapperMisuse
	seenKinds := make(map[string]bool)
	for _, match := range shellInterpreterCommandWrapperPattern.FindAllStringSubmatch(input, -1) {
		wrapper := match[3]
		kind := strings.ToLower(wrapper)
		interpreterArgs := strings.Fields(match[2])
		if strings.HasPrefix(wrapper, "-") && !strings.HasPrefix(wrapper, "--") && len(wrapper) > 2 {
			kind = "-" + strings.ToLower(wrapper[len(wrapper)-1:])
			interpreterArgs = append(interpreterArgs, "-"+wrapper[1:len(wrapper)-1])
		}
		if seenKinds[kind] {
			continue
		}
		seenKinds[kind] = true
		misuses = append(misuses, shellWrapperMisuse{
			Kind:            kind,
			Interpreter:     match[1],
			InterpreterArgs: interpreterArgs,
			wrapper:         wrapper,
		})
	}

	if match := shellInterpreterHeredocPattern.FindStringSubmatch(input); match != nil {
		misuse := shellWrapperMisuse{Kind: "heredoc"}
		if !shellInterpreterCommandWrapperPattern.MatchString(match[0]) {
			wrapper := "<<"
			if strings.HasPrefix(strings.TrimSpace(match[3]), "-") {
				wrapper = "- <<"
			}
			misuse.Interpreter = match[1]
			misuse.InterpreterArgs = strings.Fields(match[2])
			misuse.wrapper = wrapper
		}
		misuses = append(misuses, misuse)
	} else if shellHeredocPattern.MatchString(input) {
		misuses = append(misuses, shellWrapperMisuse{Kind: "heredoc"})
	}
	return misuses
}

func shellInterpreterWrapperWarning(misuse shellWrapperMisuse) string {
	if misuse.Interpreter == "" {
		return "functions.shell: warning: heredoc detected; submit the script body directly instead of wrapping it in a heredoc"
	}

	program := "the program"
	interpreter := strings.ToLower(misuse.Interpreter)
	switch {
	case strings.HasPrefix(interpreter, "python"), strings.HasPrefix(interpreter, "pypy"):
		program = "the Python program"
	case interpreter == "node", interpreter == "nodejs", interpreter == "bun":
		program = "the JavaScript program"
	}

	invocationArgs := misuse.InterpreterArgs
	if strings.HasPrefix(misuse.wrapper, "-") && !strings.HasPrefix(misuse.wrapper, "--") &&
		len(misuse.wrapper) > 2 && misuse.Kind != "heredoc" {
		invocationArgs = invocationArgs[:len(invocationArgs)-1]
	}
	invocation := strings.Join(append([]string{misuse.Interpreter}, invocationArgs...), " ")
	if invocation != "" {
		invocation += " "
	}
	invocation += misuse.wrapper

	if strings.EqualFold(misuse.Interpreter, "bash") {
		if misuse.Kind == "heredoc" {
			return fmt.Sprintf(
				"functions.shell: warning: remove the `%s...` heredoc wrapper and submit the Bash script body directly without a shebang",
				invocation,
			)
		}
		return fmt.Sprintf(
			"functions.shell: warning: remove the `%s` wrapper and submit the Bash script body directly without a shebang",
			invocation,
		)
	}

	shebang := strings.Join(append([]string{"#!" + misuse.Interpreter}, misuse.InterpreterArgs...), " ")
	if misuse.Kind == "heredoc" {
		return fmt.Sprintf(
			"functions.shell: warning: remove the `%s...` heredoc wrapper; start the script with `%s` and put %s directly in the body",
			invocation,
			shebang,
			program,
		)
	}
	return fmt.Sprintf(
		"functions.shell: warning: replace `%s ...` with `%s` on the first line and put %s directly in the body",
		invocation,
		shebang,
		program,
	)
}

// Luna occasionally submits Code Mode JavaScript through functions.shell.
// Recover only programs whose first statement invokes a nested Code Mode tool
// and which project a result through a Code Mode output helper. A shebang,
// directive, comment, or ordinary shell statement at the start keeps the
// documented shell semantics.
func lunaShellCodeModeProgram(contribution toolContribution, input string) bool {
	if contribution.PluginID != builtinToolsPluginID || contribution.Name != "shell" {
		return false
	}
	program := strings.TrimLeft(input, " \t\r\n")
	return codeModeToolProgramPattern.MatchString(program) &&
		codeModeProjectionPattern.MatchString(program)
}

func nativeExecCommandInput(input string) (string, string, bool, bool) {
	rewritten, warningInput, changed, err := insertExecCommandWarning(input, nativeExecCommandWarning)
	if err != nil {
		return input, "", false, false
	}
	return rewritten, warningInput, changed, true
}
