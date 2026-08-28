package router

import (
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const codeModeCommentaryHistoryTool = "__hpatch_code_mode_commentary"

type codeModeCommentaryDelivery uint8

const (
	codeModeCommentaryNone codeModeCommentaryDelivery = iota
	codeModeCommentaryRuntime
	codeModeCommentarySuppressed
)

type codeModeCommentaryCall struct {
	start         int
	end           int
	argumentStart int
	argumentEnd   int
	form          string
}

func (t *hpatchResponseTransform) lowerCodeModeCommentary(callID, input string) (string, bool, codeModeCommentaryDelivery, error) {
	if t.proxy.commentaryEndpoint == "" {
		return input, false, codeModeCommentaryNone, nil
	}
	calls, err := findCodeModeCommentaryCalls(input)
	if err != nil || len(calls) == 0 {
		return input, false, codeModeCommentaryNone, err
	}
	if callID == "" {
		return "", false, codeModeCommentaryNone, errors.New("Code Mode commentary call has no call ID")
	}
	slices.SortFunc(calls, func(first, second codeModeCommentaryCall) int {
		return first.start - second.start
	})
	for index := 1; index < len(calls); index++ {
		if calls[index-1].end > calls[index].start {
			return "", false, codeModeCommentaryNone, errors.New("Code Mode commentary calls overlap")
		}
	}
	delivery := codeModeCommentaryRuntime
	publisherToken := ""
	subscription, err := t.proxy.commentary.subscribe(t.historySessionID, t.sessionID, callID, false, true)
	if err != nil {
		if !errors.Is(err, errCommentaryCapacity) {
			return "", false, codeModeCommentaryNone, err
		}
		subscription = nil
		delivery = codeModeCommentarySuppressed
		publisherToken, _ = t.proxy.commentary.suppressionCapability(t.sessionID)
	} else {
		publisherToken = subscription.token
	}
	result := input
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		argument := input[call.argumentStart:call.argumentEnd]
		replacement := "await (void (" + argument + "))"
		if publisherToken != "" {
			command := workerCommand("shell", []string{
				commentaryOnceArgument,
				t.proxy.commentaryEndpoint,
				publisherToken,
				url.PathEscape(call.form),
			})
			commandExpression := strconv.Quote(command+" '") +
				" + encodeURIComponent(String(" + argument + ")).replaceAll(\"'\", \"%27\") + \"'\""
			replacement = "await tools.exec_command({cmd: " + commandExpression + ", login: false})"
		}
		result = result[:call.start] + replacement + result[call.end:]
	}
	return result, true, delivery, nil
}

func codeModeCommentaryForm(source string, start, end int) string {
	for end < len(source) && (source[end] == ' ' || source[end] == '\t') {
		end++
	}
	if end < len(source) && source[end] == ';' {
		end++
	}
	return strings.TrimRight(source[start:end], " \t")
}
