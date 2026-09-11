package hpatchsyntax

import (
	"strings"
	"testing"
)

func TestHeredocFinalTerminatorModes(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "empty"},
		{name: "LF", body: "value\n", want: "value"},
		{name: "CRLF", body: "value\r\n", want: "value"},
		{name: "blank body line", body: "\n"},
		{name: "two blank body lines", body: "\n\n", want: "\n"},
		{name: "spaces", body: "value \t\n", want: "value \t"},
		{name: "interior blank", body: "one\n\nlast\n", want: "one\n\nlast"},
		{name: "mixed terminators", body: "one\r\ntwo\n\r\n", want: "one\r\ntwo\n"},
		{name: "standalone CR payload", body: "one\rtwo\n", want: "one\rtwo"},
	} {
		for _, marker := range []string{"<<PATCH", "<<PATCH-"} {
			t.Run(test.name+"/"+marker, func(t *testing.T) {
				header := "type \"old\" " + marker
				lines := SplitPhysicalLines(header + "\n" + test.body + "PATCH\nrm\n")
				frame, err := FrameCommand(lines, 0, header)
				want := test.body
				if marker == "<<PATCH-" {
					want = test.want
				}
				if err != nil || frame.Marker != marker || frame.Body != want || lines[frame.Next].Text != "rm" {
					t.Fatalf("frame = %+v, error %v; want marker %q body %q followed by rm", frame, err, marker, want)
				}
			})
		}
	}
}

func TestTextFramesContainTheirOwnExamples(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		for _, mode := range []string{"<<TEXT", "<<TEXT-"} {
			payload := []string{"type <<PATCH", "PATCH", "type <<TEXT-", "|body", "TEXT", "", "quotes \"\\\t  ", "世界"}
			var encoded, decoded strings.Builder
			for _, line := range payload {
				encoded.WriteString("|" + line + ending)
				decoded.WriteString(line + ending)
			}
			header := `type "old" ` + mode
			lines := SplitPhysicalLines(header + ending + encoded.String() + "TEXT" + ending + "rm" + ending)
			frame, err := FrameCommand(lines, 0, header)
			want := decoded.String()
			if strings.HasSuffix(mode, "-") {
				want = strings.TrimSuffix(want, ending)
			}
			if err != nil || frame.Marker != mode || frame.Body != want || lines[frame.Next].Text != "rm" {
				t.Fatalf("frame = %+v, error %v; want %q followed by rm", frame, err, want)
			}
		}
	}
}

func TestTextFrameBoundariesAndFailures(t *testing.T) {
	for _, test := range []struct {
		name, body, want, failure string
	}{
		{name: "empty", body: "TEXT\n"},
		{name: "empty line", body: "|\nTEXT\n"},
		{name: "two empty lines", body: "|\n|\nTEXT\n", want: "\n"},
		{name: "mixed endings", body: "|one\r\n|two\nTEXT\n", want: "one\r\ntwo"},
		{name: "missing prefix", body: "|first\nrm\nTEXT\n", failure: "line 3 requires a leading |"},
		{name: "blank missing prefix", body: "\nTEXT\n", failure: "requires a leading |"},
		{name: "missing close", body: "|TEXT\n", failure: "unterminated heredoc"},
		{name: "close suffix", body: "|body\nTEXT extra\n", failure: "requires a leading |"},
		{name: "UTF-8", body: "|" + string([]byte{0xff}) + "\nTEXT\n", failure: "not UTF-8"},
		{name: "limit", body: "|" + strings.Repeat("x", MaxHeredocBodyBytes-1) + "\nTEXT\n", want: strings.Repeat("x", MaxHeredocBodyBytes-1)},
		{name: "over limit before chomp", body: "|" + strings.Repeat("x", MaxHeredocBodyBytes) + "\nTEXT\n", failure: "body exceeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			header := "type <<TEXT-"
			lines := SplitPhysicalLines(header + "\n" + test.body)
			frame, err := FrameCommand(lines, 0, header)
			if test.failure != "" {
				if err == nil || !strings.Contains(err.Error(), test.failure) || frame.Body != "" {
					t.Fatalf("frame bytes = %d, error %v; want %q", len(frame.Body), err, test.failure)
				}
				if strings.Contains(test.failure, "leading |") && frame.Next != len(lines) {
					t.Fatalf("malformed frame ends at %d, want %d", frame.Next, len(lines))
				}
			} else if err != nil || frame.Body != test.want {
				t.Fatalf("frame bytes = %d, error %v; want %d bytes", len(frame.Body), err, len(test.want))
			}
		})
	}
}
func TestChompedHeredocValidatesOriginalBody(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("x", MaxHeredocBodyBytes) + "\n",
		string([]byte{0xff}) + "\n",
	} {
		header := "type <<PATCH-"
		frame, err := FrameCommand(SplitPhysicalLines(header+"\n"+body+"PATCH\n"), 0, header)
		if err == nil || frame.Body != "" {
			t.Fatalf("invalid original body accepted: frame bytes %d, error %v", len(frame.Body), err)
		}
	}
}
