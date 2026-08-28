//go:build !cgo

package router

import (
	"errors"
	"strings"
)

const codeModeCommentaryParserAvailable = false

func findCodeModeCommentaryCalls(source string) ([]codeModeCommentaryCall, error) {
	if strings.Contains(source, "commentary") {
		return nil, errors.New("Code Mode commentary requires a build with cgo-enabled JavaScript parsing")
	}
	return nil, nil
}
