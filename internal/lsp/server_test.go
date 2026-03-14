package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/tokens"
)

func TestConvertDiagnosticsPreservesZeroWidthRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.ferr")
	at := source.Position{Line: 3, Column: 5}
	loc := source.NewLocation(path, at, at)
	d := diagnostics.NewError("expected ';'").WithPrimaryLabel(&loc, "expected ';'")

	out := convertDiagnostics([]*diagnostics.Diagnostic{d}, path)
	if len(out) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(out))
	}
	got := out[0].Range
	if got.Start.Line != 2 || got.Start.Character != 4 {
		t.Fatalf("unexpected start range: %#v", got.Start)
	}
	if got.End.Line != 2 || got.End.Character != 4 {
		t.Fatalf("expected zero-width end at 2:4, got %#v", got.End)
	}
}

func TestPublishSyntaxDiagnosticsRecoversFromParserPanic(t *testing.T) {
	oldLex := lexSource
	oldParse := parseSource
	defer func() {
		lexSource = oldLex
		parseSource = oldParse
	}()

	lexSource = func(path, text string, diag *diagnostics.Bag) []tokens.Token {
		return []tokens.Token{{Kind: tokens.EOF, Start: source.NewPosition(), End: source.NewPosition()}}
	}
	parseSource = func(path string, toks []tokens.Token, diag *diagnostics.Bag) {
		panic("boom")
	}

	var out bytes.Buffer
	s := &Server{out: &out}

	path := filepath.Join(t.TempDir(), "main.ferr")
	uri := "file://" + filepath.ToSlash(path)
	s.publishSyntaxDiagnostics(uri, 7, "fn main() {}")

	parts := strings.SplitN(out.String(), "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatalf("expected framed LSP message, got: %q", out.String())
	}

	var notif rpcNotification
	if err := json.Unmarshal([]byte(parts[1]), &notif); err != nil {
		t.Fatalf("failed to decode notification body: %v", err)
	}
	if notif.Method != "textDocument/publishDiagnostics" {
		t.Fatalf("unexpected notification method: %q", notif.Method)
	}

	paramsRaw, err := json.Marshal(notif.Params)
	if err != nil {
		t.Fatalf("failed to re-marshal params: %v", err)
	}
	var params publishDiagnosticsParams
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		t.Fatalf("failed to decode diagnostics params: %v", err)
	}
	if params.Version == nil || *params.Version != 7 {
		t.Fatalf("expected diagnostics version 7, got %#v", params.Version)
	}
	if len(params.Diagnostics) == 0 {
		t.Fatalf("expected fallback diagnostic after panic")
	}
	if !strings.Contains(params.Diagnostics[0].Message, "internal error") {
		t.Fatalf("expected internal error diagnostic, got %q", params.Diagnostics[0].Message)
	}
}
