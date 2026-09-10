package mekugi

import (
	"strings"
	"testing"
)

func TestRenderFileWritePatch(t *testing.T) {
	for _, content := range []string{"", "\n", "one\n", "one\n\n", "*** End Patch\n$HOME\\n\n", "α\n"} {
		t.Run(content, func(t *testing.T) {
			patch, err := RenderFileWritePatch("/work/file.txt", content)
			if err != nil {
				t.Fatal(err)
			}
			body := strings.TrimSuffix(strings.TrimPrefix(patch, "*** Begin Patch\n*** Add File: /work/file.txt\n"), "*** End Patch\n")
			var decoded strings.Builder
			for line := range strings.SplitSeq(strings.TrimSuffix(body, "\n"), "\n") {
				if body == "" {
					break
				}
				if !strings.HasPrefix(line, "+") {
					t.Fatalf("invalid addition row %q", line)
				}
				decoded.WriteString(line[1:])
				decoded.WriteByte('\n')
			}
			if decoded.String() != content {
				t.Fatalf("rendered content = %q, want %q", decoded.String(), content)
			}
		})
	}
	for _, content := range []string{"unterminated", "crlf\r\n", "\x00\n", "\xff\n"} {
		if _, err := RenderFileWritePatch("file", content); err == nil {
			t.Errorf("accepted unrepresentable content %q", content)
		}
	}
	for _, path := range []string{"", "file\n*** Delete File: victim", "file\r", " file", "file ", "\x00", "\xff"} {
		if _, err := RenderFileWritePatch(path, "ok\n"); err == nil {
			t.Errorf("accepted unrepresentable path %q", path)
		}
	}
}
