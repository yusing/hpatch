package router

import (
	"cmp"
	"errors"
	"slices"
	"strconv"
)

const codeModeCommentaryHistoryTool = "__hpatch_code_mode_commentary"

type codeModeCommentaryCall struct {
	start         int
	end           int
	argumentStart int
	argumentEnd   int
}

func (t *hpatchResponseTransform) lowerCodeModeCommentary(callID, input string) (string, bool, error) {
	if t.proxy.commentaryEndpoint == "" {
		return input, false, nil
	}
	calls, err := findCodeModeCommentaryCalls(input)
	if err != nil || len(calls) == 0 {
		return input, false, err
	}
	if callID == "" {
		return "", false, errors.New("Code Mode commentary call has no call ID")
	}
	slices.SortFunc(calls, func(first, second codeModeCommentaryCall) int {
		if order := cmp.Compare(first.start, second.start); order != 0 {
			return order
		}
		return cmp.Compare(second.end, first.end)
	})
	parents := make([]int, len(calls))
	for index := range parents {
		parents[index] = -1
	}
	stack := make([]int, 0, len(calls))
	for index, call := range calls {
		for len(stack) != 0 && calls[stack[len(stack)-1]].end <= call.start {
			stack = stack[:len(stack)-1]
		}
		if len(stack) != 0 {
			parent := stack[len(stack)-1]
			if call.start < calls[parent].argumentStart || call.end > calls[parent].argumentEnd {
				return "", false, errors.New("Code Mode commentary calls overlap without nesting")
			}
			parents[index] = parent
		}
		stack = append(stack, index)
	}

	token := t.proxy.commentary.subscribe(t.historySessionID, callID)
	if token != "" {
		t.commentaryTokens = append(t.commentaryTokens, token)
	}
	replacements := make([]string, len(calls))
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		argument := input[call.argumentStart:call.argumentEnd]
		for child := len(calls) - 1; child > index; child-- {
			if parents[child] != index {
				continue
			}
			childCall := calls[child]
			start := childCall.start - call.argumentStart
			end := childCall.end - call.argumentStart
			argument = argument[:start] + replacements[child] + argument[end:]
		}
		replacement := "await (void (" + argument + "))"
		if token != "" {
			command := workerCommand("shell", []string{
				commentaryOnceArgument,
				t.proxy.commentaryEndpoint,
				token,
			})
			commandExpression := strconv.Quote(command+" '") +
				" + encodeURIComponent(String(" + argument + ")).replaceAll(\"'\", \"%27\") + \"'\""
			replacement = "await (void (await tools.exec_command({cmd: " + commandExpression + ", login: false})))"
		}
		replacements[index] = replacement
	}

	result := input
	for index := len(calls) - 1; index >= 0; index-- {
		if parents[index] != -1 {
			continue
		}
		call := calls[index]
		result = result[:call.start] + replacements[index] + result[call.end:]
	}
	return result, true, nil
}
