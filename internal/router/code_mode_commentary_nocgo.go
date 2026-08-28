//go:build !cgo

package router

import (
	"errors"
	"strings"
)

func findCodeModeCommentaryCalls(source string) ([]codeModeCommentaryCall, error) {
	if containsCodeModeCommentaryCall(source) {
		return nil, errors.New("Code Mode commentary requires a build with cgo-enabled JavaScript parsing")
	}
	return nil, nil
}

func containsCodeModeCommentaryCall(source string) bool {
	for index := 0; index < len(source); {
		switch source[index] {
		case '\'', '"', '`':
			index = skipCodeModeQuoted(source, index)
			continue
		case '/':
			if index+1 < len(source) && (source[index+1] == '/' || source[index+1] == '*') {
				index = skipCodeModeComment(source, index)
				continue
			}
		}
		if hasCodeModeIdentifier(source, index, "await") {
			next := skipCodeModeTrivia(source, index+len("await"))
			if hasCodeModeIdentifier(source, next, commentaryArgumentName) {
				next = skipCodeModeTrivia(source, next+len(commentaryArgumentName))
				if next < len(source) && source[next] == '(' {
					return true
				}
			}
		}
		index++
	}
	return false
}

func hasCodeModeIdentifier(source string, start int, identifier string) bool {
	end := start + len(identifier)
	return start >= 0 && end <= len(source) && source[start:end] == identifier &&
		(start == 0 || !isCodeModeIdentifierPart(source[start-1])) &&
		(end == len(source) || !isCodeModeIdentifierPart(source[end]))
}

func isCodeModeIdentifierPart(value byte) bool {
	return value >= 0x80 || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '_' || value == '$'
}

func skipCodeModeTrivia(source string, index int) int {
	for index < len(source) {
		if strings.ContainsRune(" \t\r\n\f\v", rune(source[index])) {
			index++
			continue
		}
		if source[index] == '/' && index+1 < len(source) && (source[index+1] == '/' || source[index+1] == '*') {
			index = skipCodeModeComment(source, index)
			continue
		}
		break
	}
	return index
}

func skipCodeModeQuoted(source string, start int) int {
	quote := source[start]
	for index := start + 1; index < len(source); index++ {
		if source[index] == '\\' {
			index++
			continue
		}
		if source[index] == quote {
			return index + 1
		}
	}
	return len(source)
}

func skipCodeModeComment(source string, start int) int {
	if source[start+1] == '/' {
		if end := strings.IndexByte(source[start+2:], '\n'); end >= 0 {
			return start + 2 + end + 1
		}
		return len(source)
	}
	if end := strings.Index(source[start+2:], "*/"); end >= 0 {
		return start + 2 + end + 2
	}
	return len(source)
}
