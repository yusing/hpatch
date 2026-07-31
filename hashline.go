package hpatch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const lineHashLength = 4

func hashLine(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:2])
}

func lineContent(text string, line logicalLine) string {
	return text[line.start:line.contentEnd]
}

func writeHashLine(output *strings.Builder, content, displayed string) {
	fmt.Fprintf(output, "%s: %s\n", hashLine(content), displayed)
}

func resolveLineHash(baseline, hash string) (logicalLine, int, error) {
	lines := logicalLines(baseline)
	resolved := 0
	matches := 0
	for index, line := range lines {
		if hashLine(lineContent(baseline, line)) != hash {
			continue
		}
		resolved = index
		matches++
	}
	switch matches {
	case 0:
		return logicalLine{}, 0, fmt.Errorf(
			"hashline %s does not identify any line in the immutable baseline",
			hash,
		)
	case 1:
		return lines[resolved], resolved + 1, nil
	default:
		return logicalLine{}, 0, fmt.Errorf(
			"hashline %s is ambiguous: %d baseline lines have that hash",
			hash,
			matches,
		)
	}
}
