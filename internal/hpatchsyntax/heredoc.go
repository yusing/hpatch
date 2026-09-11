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
	Marker string
	Body   string
	Next   int
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
// command at headerIndex. Malformed frames retain their attributable lines so
// parsers and diagnostics do not reinterpret payload-shaped data as commands.
func FrameCommand(lines []PhysicalLine, headerIndex int, command string) (CommandFrame, error) {
	frame := CommandFrame{Next: headerIndex + 1}
	marker, err := heredocMarker(command)
	if err != nil {
		// An invalid heredoc header owns the remaining physical lines.
		frame.Next = len(lines)
		return frame, err
	}
	frame.Marker = marker
	if marker != "" {
		delimiter := strings.TrimSuffix(strings.TrimPrefix(marker, "<<"), "-")
		frame.Body, frame.Next, err = decodeHeredoc(lines, headerIndex, delimiter)
		if err == nil && strings.HasSuffix(marker, "-") && frame.Next > headerIndex+2 {
			// Remove only the final physical body terminator, not trailing
			// spaces or any preceding blank lines.
			frame.Body = strings.TrimSuffix(frame.Body, lines[frame.Next-2].Terminator)
		}
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
		strings.HasPrefix(command, "add ")
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

func heredocMarker(command string) (string, error) {
	operation, _, _ := strings.Cut(command, " ")
	if operation != "type" && operation != "add" {
		return "", nil
	}
	marker := unquotedDoubleLess(command)
	if marker < 0 {
		return "", nil
	}
	if marker > 0 && command[marker-1] == ' ' {
		switch command[marker:] {
		case "<<PATCH", "<<PATCH-", "<<TEXT", "<<TEXT-":
			return command[marker:], nil
		}
	}
	return "", errors.New("invalid heredoc; HPATCH/2 requires an unquoted <<PATCH, <<PATCH-, <<TEXT, or <<TEXT- final operand")
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
		// SplitPhysicalLines retains an empty EOF sentinel after a final
		// newline; it is not an unprefixed payload line.
		if index == len(lines)-1 && line.Text == "" && line.Terminator == "" {
			break
		}
		if delimiter == "TEXT" {
			payload, ok := strings.CutPrefix(line.Text, "|")
			if !ok {
				// A malformed value owns the remaining input. Never treat
				// an unframed payload line as an edit command.
				return "", len(lines), fmt.Errorf("text body line %d requires a leading | or closing TEXT", index+1)
			}
			line.Text = payload
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
