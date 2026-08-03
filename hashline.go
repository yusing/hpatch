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
	return hex.EncodeToString(sum[:lineHashLength/2])
}

func lineContent(text string, line logicalLine) string {
	return text[line.start:line.contentEnd]
}

func writeHashLine(output *strings.Builder, number int, content, displayed string) {
	fmt.Fprintf(output, "%d:%s %s\n", number, hashLine(content), displayed)
}
