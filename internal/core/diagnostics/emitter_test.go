package diagnostics

import (
	"bytes"
	"strings"
	"testing"
)

func TestEmitterHandlesMultiplePrimaryLabelsWithoutPanic(t *testing.T) {
	var out bytes.Buffer
	emitter := NewEmitter(&out)
	emitter.cache.AddSource("main.fer", "let a = 1\nlet b = 2\n")

	loc1 := testLoc("main.fer", 1, 5)
	loc2 := testLoc("main.fer", 2, 5)

	diag := NewError("broken diagnostic shape")
	diag.FilePath = "main.fer"
	diag.Labels = []Label{
		{Location: &loc1, Message: "first", Style: Primary},
		{Location: &loc2, Message: "second", Style: Primary},
	}

	emitter.Emit(diag)

	text := out.String()
	if !strings.Contains(text, "diagnostic has 2 primary labels") {
		t.Fatalf("expected internal multiple-primary note, got:\n%s", text)
	}
	if !strings.Contains(text, "second") {
		t.Fatalf("expected second label to remain visible in output, got:\n%s", text)
	}
}
