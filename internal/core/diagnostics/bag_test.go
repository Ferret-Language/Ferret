package diagnostics

import (
	"strings"
	"testing"
)

func TestEmitErrorsOmitsWarningsAndWarningSummary(t *testing.T) {
	bag := NewDiagnosticBag("")
	bag.Add(NewWarning("unused local"))
	bag.Add(NewError("broken"))

	out := captureEmitErrors(bag)
	if strings.Contains(out, "unused local") {
		t.Fatalf("expected warning to be omitted, got %q", out)
	}
	if !strings.Contains(out, "broken") {
		t.Fatalf("expected error to be emitted, got %q", out)
	}
	if strings.Contains(out, "warning(s)") {
		t.Fatalf("expected warning summary to be omitted, got %q", out)
	}
	if !strings.Contains(out, "Compilation failed with 1 error(s)") {
		t.Fatalf("expected error summary, got %q", out)
	}
}

func TestEmitErrorsSkipsWarningsWhenNoErrors(t *testing.T) {
	bag := NewDiagnosticBag("")
	bag.Add(NewWarning("unused local"))

	out := captureEmitErrors(bag)
	if out != "" {
		t.Fatalf("expected no output for warnings-only bag, got %q", out)
	}
}

func TestEmitAllToHTMLRendersDirectHTML(t *testing.T) {
	bag := NewDiagnosticBag("")
	bag.Add(NewError("<broken>"))

	out := bag.EmitAllToHTML()
	if !strings.Contains(out, "<span style=") {
		t.Fatalf("expected html spans, got %q", out)
	}
	if !strings.Contains(out, "&lt;broken&gt;") {
		t.Fatalf("expected escaped html message, got %q", out)
	}
	if strings.Contains(out, "\033[") {
		t.Fatalf("expected no ansi sequences, got %q", out)
	}
}

func captureEmitErrors(bag *DiagnosticBag) string {
	var sb strings.Builder
	emitter := &Emitter{
		cache:       bag.sourceCache,
		writer:      &sb,
		highlighter: NewSyntaxHighlighter(true),
	}
	bag.emitFiltered(emitter, &sb, func(diag *Diagnostic) bool {
		return diag != nil && diag.Severity == Error
	})
	return sb.String()
}
