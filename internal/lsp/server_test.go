package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
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

func TestInitializeAdvertisesHoverProvider(t *testing.T) {
	var out bytes.Buffer
	s := &Server{out: &out, documents: make(map[string]openDocument)}

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  mustRawJSON(t, initializeParams{}),
	}
	s.handleRequest(req)

	resp := decodeSingleResponse(t, out.String())
	if resp.Error != nil {
		t.Fatalf("unexpected initialize error: %#v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected initialize result")
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal initialize result: %v", err)
	}
	var payload struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("failed to unmarshal initialize result: %v", err)
	}
	if len(payload.Capabilities) == 0 {
		t.Fatal("expected capabilities object")
	}

	var hoverProvider bool
	if err := json.Unmarshal(payload.Capabilities["hoverProvider"], &hoverProvider); err != nil {
		t.Fatalf("failed to decode hoverProvider: %v", err)
	}
	if !hoverProvider {
		t.Fatal("expected hoverProvider=true")
	}
}

func TestHoverReturnsTypeFromSavedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "fn main() {\n    let x = 1\n    x\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	var out bytes.Buffer
	s := &Server{out: &out, documents: make(map[string]openDocument)}
	uri := "file://" + filepath.ToSlash(path)

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: 2, Character: 4},
		}),
	}
	s.handleRequest(req)

	resp := decodeSingleResponse(t, out.String())
	if resp.Error != nil {
		t.Fatalf("unexpected hover error: %#v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected hover result")
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal hover result: %v", err)
	}
	var hover hoverResult
	if err := json.Unmarshal(raw, &hover); err != nil {
		t.Fatalf("failed to unmarshal hover result: %v", err)
	}
	if !strings.Contains(hover.Contents.Value, "i32") {
		t.Fatalf("expected i32 hover, got %q", hover.Contents.Value)
	}
	if hover.Range == nil {
		t.Fatal("expected hover range")
	}
}

func TestHoverUsesOpenDocumentOverlayText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	onDisk := "fn main() {\n    let x = 1\n    x\n}\n"
	overlay := "fn main() {\n    let x = \"hi\"\n    x\n}\n"
	if err := os.WriteFile(path, []byte(onDisk), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{
		out: &out,
		documents: map[string]openDocument{
			uri: {Version: 2, Text: overlay},
		},
	}

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: 2, Character: 4},
		}),
	}
	s.handleRequest(req)

	resp := decodeSingleResponse(t, out.String())
	if resp.Error != nil {
		t.Fatalf("unexpected hover error: %#v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected hover result")
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal hover result: %v", err)
	}
	var hover hoverResult
	if err := json.Unmarshal(raw, &hover); err != nil {
		t.Fatalf("failed to unmarshal hover result: %v", err)
	}
	if !strings.Contains(hover.Contents.Value, "str") {
		t.Fatalf("expected str hover from overlay, got %q", hover.Contents.Value)
	}
}

func TestHoverCachesByOpenDocumentVersion(t *testing.T) {
	oldParseProject := parseProject
	defer func() { parseProject = oldParseProject }()

	calls := 0
	parseProject = func(path string) compiler.Result {
		calls++
		return fakeHoverResult(path, "i32")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	if err := os.WriteFile(path, []byte("fn main() {}"), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	uri := "file://" + filepath.ToSlash(path)
	var out bytes.Buffer
	s := &Server{
		out: &out,
		documents: map[string]openDocument{
			uri: {Version: 1, Text: "fn main() {}"},
		},
		hoverCache: make(map[string]hoverCacheEntry),
	}

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: 0, Character: 0},
		}),
	}
	s.handleRequest(req)
	s.handleRequest(req)

	if calls != 1 {
		t.Fatalf("expected one parse for repeated hover on same version, got %d", calls)
	}

	s.mu.Lock()
	s.documents[uri] = openDocument{Version: 2, Text: "fn main() { let x = 1 }"}
	s.mu.Unlock()
	s.handleRequest(req)

	if calls != 2 {
		t.Fatalf("expected cache invalidation after version change, got %d parses", calls)
	}
}

func TestHoverMethodDeclarationShowsFullSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Point struct {\n    X: i32\n}\n\nfn Point::Calc(&self) i32 {\n    return self.X\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "Calc")
	if !ok {
		t.Fatal("failed to find Calc position")
	}

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	hover := decodeHoverResult(t, out.String())
	if hover == nil {
		t.Fatalf("expected hover result, got nil response payload: %q", out.String())
	}
	if !strings.Contains(hover.Contents.Value, "fn Point::Calc(&self) i32") {
		t.Fatalf("expected full method signature in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverNamedTypeShowsFieldsAndMethods(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Point struct {\n    X: i32\n    Y: i32 = 2\n}\n\nfn Point::Calc(&self) i32 {\n    return self.X\n}\n\nfn main() {\n    let p: Point = .{}\n    p.Calc()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "Point =")
	if !ok {
		t.Fatal("failed to find Point usage position")
	}

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	hover := decodeHoverResult(t, out.String())
	if hover == nil {
		t.Fatal("expected hover result")
	}
	if !strings.Contains(hover.Contents.Value, "type Point struct") {
		t.Fatalf("expected type declaration header in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "X: i32") || !strings.Contains(hover.Contents.Value, "Y: i32 = 2") {
		t.Fatalf("expected struct fields in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "fn Point::Calc(&self) i32") {
		t.Fatalf("expected type methods in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverFunctionCallShowsNamedSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "fn run2() void {\n}\n\nfn main() {\n    run2()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    run2()")
	if !ok {
		t.Fatal("failed to find run2() call position")
	}
	char += 4 // position the cursor on the function name

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	hover := decodeHoverResult(t, out.String())
	if hover == nil {
		t.Fatal("expected hover result")
	}
	if !strings.Contains(hover.Contents.Value, "fn run2() void") {
		t.Fatalf("expected named function signature in hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "fn() void") {
		t.Fatalf("expected no anonymous function signature in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverBuiltinCallsShowFunctionSignatures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "fn main() {\n    let xs: [3]i32 = [1, 2, 3]\n    print(\"ok\")\n    len(xs)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	printLine, printChar, ok := findPosition(src, "    print(\"ok\")")
	if !ok {
		t.Fatal("failed to find print call")
	}
	printChar += 4

	printReq := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: printLine, Character: printChar},
		}),
	}
	s.handleRequest(printReq)
	printHover := decodeHoverResult(t, out.String())
	if printHover == nil {
		t.Fatal("expected hover result for print")
	}
	if !strings.Contains(printHover.Contents.Value, "fn print(") {
		t.Fatalf("expected print signature in hover, got %q", printHover.Contents.Value)
	}

	out.Reset()
	lenLine, lenChar, ok := findPosition(src, "    len(xs)")
	if !ok {
		t.Fatal("failed to find len call")
	}
	lenChar += 4

	lenReq := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: lenLine, Character: lenChar},
		}),
	}
	s.handleRequest(lenReq)
	lenHover := decodeHoverResult(t, out.String())
	if lenHover == nil {
		t.Fatal("expected hover result for len")
	}
	if !strings.Contains(lenHover.Contents.Value, "fn len(") || !strings.Contains(lenHover.Contents.Value, "usize") {
		t.Fatalf("expected len signature in hover, got %q", lenHover.Contents.Value)
	}
}

func TestHoverSelfShowsWrapperAndExpandedNamedType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Point struct {\n    Value: i32 = 7\n}\n\nfn Point::Calc(&self) i32 {\n    return self.Value\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "self.Value")
	if !ok {
		t.Fatal("failed to find self position")
	}

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	hover := decodeHoverResult(t, out.String())
	if hover == nil {
		t.Fatal("expected hover result")
	}
	if !strings.Contains(hover.Contents.Value, "&Point") {
		t.Fatalf("expected wrapper type in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "type Point struct") || !strings.Contains(hover.Contents.Value, "Value: i32 = 7") {
		t.Fatalf("expected expanded named type in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "fn Point::Calc(&self) i32") {
		t.Fatalf("expected method listing in hover, got %q", hover.Contents.Value)
	}
}

func fakeHoverResult(path string, typeName string) compiler.Result {
	start := source.Position{Line: 1, Column: 1, Index: 0}
	end := source.Position{Line: 1, Column: 2, Index: 1}
	loc := source.NewLocation(path, start, end)
	ident := &ast.Ident{Path: []string{"x"}, Location: loc}

	info := typeinfo.NewModuleInfo()
	info.BindNode(ident, &typeinfo.BuiltinType{Name: typeName})

	mod := &context.Module{
		FilePath: path,
		Types:    info,
	}
	return compiler.Result{
		Entry:   mod,
		Modules: []*context.Module{mod},
	}
}

func mustRawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}
	return raw
}

func decodeSingleResponse(t *testing.T, framed string) rpcResponse {
	t.Helper()
	parts := strings.SplitN(framed, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatalf("expected framed LSP message, got: %q", framed)
	}
	var resp rpcResponse
	if err := json.Unmarshal([]byte(parts[1]), &resp); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return resp
}

func decodeHoverResult(t *testing.T, framed string) *hoverResult {
	t.Helper()
	resp := decodeSingleResponse(t, framed)
	if resp.Error != nil {
		t.Fatalf("unexpected hover error: %#v", resp.Error)
	}
	if resp.Result == nil {
		return nil
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal hover result: %v", err)
	}
	var hover hoverResult
	if err := json.Unmarshal(raw, &hover); err != nil {
		t.Fatalf("failed to unmarshal hover result: %v", err)
	}
	return &hover
}

func findPosition(text, needle string) (int, int, bool) {
	lines := strings.Split(text, "\n")
	for lineIdx, line := range lines {
		idx := strings.Index(line, needle)
		if idx >= 0 {
			return lineIdx, idx, true
		}
	}
	return 0, 0, false
}
