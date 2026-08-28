//go:build !cgo

package router

import (
	"errors"
	"strings"
)

func findCodeModeCommentaryCalls(source string) ([]codeModeCommentaryCall, error) {
	if strings.Contains(source, commentaryArgumentName) {
		return nil, errors.New("Code Mode commentary requires a build with cgo-enabled JavaScript parsing")
	}
	return nil, nil
}
