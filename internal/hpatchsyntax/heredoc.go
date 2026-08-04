package hpatchsyntax

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxHeredocBodyBytes bounds the decoded payload retained by one heredoc.
const MaxHeredocBodyBytes = 1 << 20

// PhysicalLine retains a script line's exact terminator separately from its text.
type PhysicalLine struct {
	Text       string
	Terminator string
}

// CommandFrame describes one inline command or complete heredoc command.
type CommandFrame struct {
	Delimiter string
	Body      string
	Next      int
}

// SplitPhysicalLines splits LF and CRLF scripts without losing body terminators.
func SplitPhysicalLines(source string) []PhysicalLine {
	raw := strings.Split(source, "\n")
	lines := make([]PhysicalLine, len(raw))
	for index, text := range raw {
		terminator := ""
		if index < len(raw)-1 {
			terminator = "\n"
			if trimmed, ok := strings.CutSuffix(text, "\r"); ok {
				text = trimmed
				terminator = "\r\n"
			}
		} else if trimmed, ok := strings.CutSuffix(text, "\r"); ok {
			text = trimmed
			terminator = "\r"
		}
		lines[index] = PhysicalLine{Text: text, Terminator: terminator}
	}
	return lines
}

// FrameCommand returns the complete physical-line span and decoded body for the
// command at headerIndex. Malformed heredocs retain their attributable bodies;
// quoted operands containing physical newlines retain lines through the closing
// quote so one indexed correction can replace the malformed command.
func FrameCommand(lines []PhysicalLine, headerIndex int, command string) (CommandFrame, error) {
	frame := CommandFrame{Next: headerIndex + 1}
	delimiter, err := heredocDelimiter(command)
	if err != nil {
		// An invalid heredoc header owns the remaining physical lines. They are
		// payload-shaped data, not independent commands or correction entries.
		frame.Next = len(lines)
		return frame, err
	}
	frame.Delimiter = delimiter
	if delimiter != "" {
		frame.Body, frame.Next, err = decodeHeredoc(lines, headerIndex, delimiter)
		return frame, err
	}
	if !isInlineQuotedCommand(command) {
		return frame, nil
	}

	quoteOpen := scanQuotedOperand(command, false)
	if !quoteOpen {
		return frame, nil
	}
	for index := headerIndex + 1; index < len(lines); index++ {
		frame.Next = index + 1
		quoteOpen = scanQuotedOperand(lines[index].Text, quoteOpen)
		if !quoteOpen {
			break
		}
	}
	return frame, errors.New(`physical newline inside quoted operand; encode line terminators as \n or \r`)
}

func isInlineQuotedCommand(command string) bool {
	return strings.HasPrefix(command, "type ") ||
		strings.HasPrefix(command, "type- ") ||
		strings.HasPrefix(command, "type+ ")
}

func scanQuotedOperand(text string, quoteOpen bool) bool {
	escaped := false
	for _, character := range text {
		if !quoteOpen {
			if character == '"' {
				quoteOpen = true
			}
			continue
		}
		switch {
		case escaped:
			escaped = false
		case character == '\\':
			escaped = true
		case character == '"':
			quoteOpen = false
		}
	}
	return quoteOpen
}

func heredocDelimiter(command string) (string, error) {
	operation, _, _ := strings.Cut(command, " ")
	if operation != "type" && operation != "type-" && operation != "type+" {
		return "", nil
	}
	marker := unquotedDoubleLess(command)
	if marker < 0 {
		return "", nil
	}
	if marker > 0 && command[marker-1] == ' ' && command[marker:] == "<<PATCH" {
		return "PATCH", nil
	}
	return "", errors.New("invalid heredoc; HPATCH/2 requires an unquoted <<PATCH final operand")
}

func unquotedDoubleLess(text string) int {
	quoted := false
	escaped := false
	for index := 0; index < len(text); index++ {
		character := text[index]
		if !quoted {
			if character == '"' {
				quoted = true
				continue
			}
			if character == '<' && index+1 < len(text) && text[index+1] == '<' {
				return index
			}
			continue
		}
		switch {
		case escaped:
			escaped = false
		case character == '\\':
			escaped = true
		case character == '"':
			quoted = false
		}
	}
	return -1
}

func decodeHeredoc(lines []PhysicalLine, headerIndex int, delimiter string) (string, int, error) {
	var body strings.Builder
	oversized := false
	for index := headerIndex + 1; index < len(lines); index++ {
		line := lines[index]
		if line.Text == delimiter {
			if oversized {
				return "", index + 1, fmt.Errorf("heredoc body exceeds %d bytes", MaxHeredocBodyBytes)
			}
			value := body.String()
			if !utf8.ValidString(value) {
				return "", index + 1, errors.New("heredoc body is not UTF-8")
			}
			return value, index + 1, nil
		}
		if oversized {
			continue
		}
		partBytes := len(line.Text) + len(line.Terminator)
		if partBytes > MaxHeredocBodyBytes-body.Len() {
			oversized = true
			continue
		}
		body.WriteString(line.Text)
		body.WriteString(line.Terminator)
	}
	return "", len(lines), fmt.Errorf("unterminated heredoc; expected closing delimiter %s", delimiter)
}
