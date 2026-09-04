package hpatch

import (
	"fmt"
	"strings"

	"github.com/yusing/hpatch/internal/verifiedrow"
)

func hashLine(content string) string {
	return verifiedrow.Hash(content)
}

func lineContent(text string, line logicalLine) string {
	return text[line.start:line.contentEnd]
}

func writeHashLine(output *strings.Builder, number int, content, displayed string) {
	fmt.Fprintf(output, "%d:%s %s\n", number, hashLine(content), displayed)
}
