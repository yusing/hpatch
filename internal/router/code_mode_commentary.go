package router

import (
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
	slices.SortFunc(calls, func(first, second codeModeCommentaryCall) int { return first.start - second.start })
	for index := 1; index < len(calls); index++ {
		if calls[index-1].end > calls[index].start {
			return "", false, errors.New("Code Mode commentary calls overlap")
		}
	}

	subscription := t.proxy.commentary.subscribe(t.historySessionID, callID)
	if subscription != nil {
		t.commentarySubscriptions = append(t.commentarySubscriptions, subscription)
	}
	result := input
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		argument := input[call.argumentStart:call.argumentEnd]
		replacement := "await (void (" + argument + "))"
		if subscription != nil {
			command := workerCommand("shell", []string{
				commentaryOnceArgument,
				t.proxy.commentaryEndpoint,
				subscription.token,
			})
			commandExpression := strconv.Quote(command+" '") +
				" + encodeURIComponent(String(" + argument + ")).replaceAll(\"'\", \"%27\") + \"'\""
			replacement = "await tools.exec_command({cmd: " + commandExpression + ", login: false})"
		}
		result = result[:call.start] + replacement + result[call.end:]
	}
	return result, true, nil
}
