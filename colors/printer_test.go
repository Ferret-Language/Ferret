package colors

import (
	"bytes"
	"strings"
	"testing"
)

func setTestLogFormat(t *testing.T, format LogFormat) {
	t.Helper()
	prev := CurrentLogFormat()
	SetLogFormat(format)
	t.Cleanup(func() {
		SetLogFormat(prev)
	})
}

func TestSprintAndFprintWithColor(t *testing.T) {
	setTestLogFormat(t, LogFormatANSI)

	s := RED.Sprint("hello")
	if !strings.Contains(s, string(RED)) || !strings.Contains(s, "hello") || !strings.Contains(s, string(RESET)) {
		t.Fatalf("unexpected colored sprint output: %q", s)
	}

	var buf bytes.Buffer
	FprintWithColor(&buf, GREEN, "x")
	out := buf.String()
	if !strings.Contains(out, string(GREEN)) || !strings.Contains(out, "x") || !strings.Contains(out, string(RESET)) {
		t.Fatalf("unexpected colored fprint output: %q", out)
	}
}

func TestNormalLogFormatStripsColorSequences(t *testing.T) {
	setTestLogFormat(t, LogFormatNormal)

	got := GREEN.Sprintf("value=%d", 3)
	if got != "value=3" {
		t.Fatalf("Sprintf normal = %q", got)
	}

	var buf bytes.Buffer
	GREEN.Fprintln(&buf, "ok")
	if buf.String() != "ok\n" {
		t.Fatalf("Fprintln normal = %q", buf.String())
	}
}

func TestHTMLLogFormatWrapsAndEscapesColoredText(t *testing.T) {
	setTestLogFormat(t, LogFormatHTML)

	got := RED.Sprint("<warn>")
	if !strings.Contains(got, "<span style=") {
		t.Fatalf("expected html span, got %q", got)
	}
	if !strings.Contains(got, "&lt;warn&gt;") {
		t.Fatalf("expected escaped html content, got %q", got)
	}
	if strings.Contains(got, string(RED)) || strings.Contains(got, string(RESET)) {
		t.Fatalf("expected no ansi escape codes, got %q", got)
	}
}

func TestParseLogFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    LogFormat
		wantErr bool
	}{
		{in: "", want: LogFormatANSI},
		{in: "ansi", want: LogFormatANSI},
		{in: "normal", want: LogFormatNormal},
		{in: "html", want: LogFormatHTML},
		{in: " ANSI ", want: LogFormatANSI},
		{in: "weird", wantErr: true},
	}

	for _, tc := range tests {
		got, err := ParseLogFormat(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q: expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}
