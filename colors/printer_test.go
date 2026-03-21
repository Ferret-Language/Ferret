package colors

import (
	"bytes"
	"strings"
	"testing"
)

func TestSprintAndFprintWithColor(t *testing.T) {
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

func TestStripANSIAndConvert(t *testing.T) {
	raw := RED.Sprint("alert")
	if got := StripANSI(raw); got != "alert" {
		t.Fatalf("StripANSI = %q", got)
	}
	html := ConvertANSIToHTML(raw)
	if !strings.Contains(html, "<span") || !strings.Contains(html, "alert") {
		t.Fatalf("ConvertANSIToHTML output unexpected: %q", html)
	}
}
