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

