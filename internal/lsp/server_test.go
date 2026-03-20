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

func TestHoverGenericNamedTypeShowsConcreteFieldsAndMethods(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Point<T> struct {\n    Value: T\n}\n\nfn Point<T>::Calc(&self) T {\n    return self.Value\n}\n\nfn Point<T>::Incr(&mut self, dx: T) void {\n    self.Value += dx\n}\n\nfn main() void {\n    let p: Point<i32> = .{ .Value = 1 }\n    p\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    p")
	if !ok {
		t.Fatal("failed to find p position")
	}
	char += len("    ")

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
	if !strings.Contains(hover.Contents.Value, "type Point<i32> struct") {
		t.Fatalf("expected concrete generic type header in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Value: i32") {
		t.Fatalf("expected concrete generic field type in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "fn Point::Calc(&self) i32") {
		t.Fatalf("expected concrete generic method result type in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "fn Point::Incr(&mut self, dx: i32) void") {
		t.Fatalf("expected concrete generic method param type in hover, got %q", hover.Contents.Value)
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

func TestHoverGenericCallShowsInstantiatedSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "fn add<T>(a: T, b: T) T {\n    return a + b\n}\n\nfn main() i32 {\n    let x = 1\n    let y = 2\n    return add(x, y)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    return add(x, y)")
	if !ok {
		t.Fatal("failed to find add call")
	}
	char += len("    return a")

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
	if !strings.Contains(hover.Contents.Value, "fn add(a: i32, b: i32) i32") {
		t.Fatalf("expected instantiated generic signature in hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "<T>") {
		t.Fatalf("expected call-site hover to hide generic declaration form, got %q", hover.Contents.Value)
	}
}

func TestHoverGenericStaticOwnerCallShowsInstantiatedSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Circle<T> struct {\n    Rad: T\n}\n\nfn Circle<T>::New(v: T) Self {\n    return .{ .Rad = v }\n}\n\nfn main() void {\n    let c = Circle<i32>::New(1)\n    c\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    let c = Circle<i32>::New(1)")
	if !ok {
		t.Fatal("failed to find static owner call")
	}
	char += len("    let c = Circle<i32>::")

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
	if !strings.Contains(hover.Contents.Value, "fn Circle::New(v: i32) Circle<i32>") {
		t.Fatalf("expected instantiated static owner signature in hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "fn Circle::New(v: T) Self") {
		t.Fatalf("expected no unresolved owner type parameter in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverFunctionCallShowsDocComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "/// Adds two numbers.\n/// Returns the sum.\nfn add(a: i32, b: i32) i32 {\n    return a + b\n}\n\nfn main() i32 {\n    return add(1, 2)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    return add(1, 2)")
	if !ok {
		t.Fatal("failed to find add call")
	}
	char += len("    return ")

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
	if !strings.Contains(hover.Contents.Value, "fn add(a: i32, b: i32) i32") {
		t.Fatalf("expected function signature in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Adds two numbers.") || !strings.Contains(hover.Contents.Value, "Returns the sum.") {
		t.Fatalf("expected function doc comment in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverFunctionCallShowsLineCommentDocBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "// Adds two numbers.\n// Returns the sum.\nfn add(a: i32, b: i32) i32 {\n    return a + b\n}\n\nfn main() i32 {\n    return add(1, 2)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    return add(1, 2)")
	if !ok {
		t.Fatal("failed to find add call")
	}
	char += len("    return ")

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
	if !strings.Contains(hover.Contents.Value, "Adds two numbers.") || !strings.Contains(hover.Contents.Value, "Returns the sum.") {
		t.Fatalf("expected function doc comment from // block in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Adds two numbers.  \nReturns the sum.") {
		t.Fatalf("expected preserved line breaks in hover doc markdown, got %q", hover.Contents.Value)
	}
}

func TestHoverTypeShowsDocCommentBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "// Point docs line 1.\n// Point docs line 2.\ntype Point struct {\n    Value: i32 = 0\n}\n\nfn main() void {\n    let p: Point = .{ .Value = 1 }\n    p\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "Point = .")
	if !ok {
		t.Fatal("failed to find Point usage")
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
	if !strings.Contains(hover.Contents.Value, "Point docs line 1.") || !strings.Contains(hover.Contents.Value, "Point docs line 2.") {
		t.Fatalf("expected type doc comment in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Point docs line 1.  \nPoint docs line 2.") {
		t.Fatalf("expected preserved doc lines in type hover, got %q", hover.Contents.Value)
	}
}

func TestHoverInterfaceMethodCallShowsSelfReceiver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Shape interface {\n    Draw(&self)\n}\n\nfn drawShape(s: Shape) void {\n    s.Draw()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    s.Draw()")
	if !ok {
		t.Fatal("failed to find method call")
	}
	char += len("    s.")

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
	if !strings.Contains(hover.Contents.Value, "fn Shape::Draw(&self) void") {
		t.Fatalf("expected method hover to include self receiver, got %q", hover.Contents.Value)
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
	if !strings.Contains(hover.Contents.Value, "receiver self: &Self") {
		t.Fatalf("expected receiver binding declaration in hover, got %q", hover.Contents.Value)
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

func TestHoverBindingDeclarations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Point struct {\n    Value: i32 = 7\n}\n\nfn Point::Calc(&self, mut dx: i32, comptime step: i32) i32 {\n    let mut a = dx\n    const b: i32 = step\n    return a + b + self.Value\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &bytes.Buffer{}, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	checkHover := func(anchor, expected string) {
		t.Helper()
		line, char, ok := findPosition(src, anchor)
		if !ok {
			t.Fatalf("failed to find anchor %q", anchor)
		}
		var out bytes.Buffer
		s.out = &out
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
			t.Fatalf("expected hover result for anchor %q", anchor)
		}
		if !strings.Contains(hover.Contents.Value, expected) {
			t.Fatalf("expected hover to contain %q for anchor %q, got %q", expected, anchor, hover.Contents.Value)
		}
	}

	checkHover("a = dx", "let mut a: i32")
	checkHover("b: i32 = step", "const b: i32")
	checkHover("dx: i32", "parameter mut dx: i32")
	checkHover("self.Value", "receiver self: &Self")
}

func TestHoverLocalBindingShowsDocComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "fn run() void {\n    // local binding docs\n    // second line\n    let mut x = 1\n    x\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "let mut x = 1")
	if !ok {
		t.Fatal("failed to find local binding")
	}
	char += len("let mut ")

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
	if !strings.Contains(hover.Contents.Value, "let mut x: i32") {
		t.Fatalf("expected binding declaration in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "local binding docs") || !strings.Contains(hover.Contents.Value, "second line") {
		t.Fatalf("expected local binding docs in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverPointerParameterIsNotReceiver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Conn struct {}\n\nfn run(mut c: *Conn) void {\n    c\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "mut c: *Conn")
	if !ok {
		t.Fatal("failed to find parameter declaration")
	}
	char += len("mut ")

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
	if strings.Contains(hover.Contents.Value, "receiver c:") {
		t.Fatalf("did not expect receiver hover for normal parameter, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "parameter mut c: *Conn") {
		t.Fatalf("expected parameter declaration hover, got %q", hover.Contents.Value)
	}
}

func TestHoverMethodOwnerTypeInDeclaration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Point<T> struct {\n    Value: T\n}\n\nfn Point<T>::Calc(&self) T {\n    return self.Value\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "Point<T>::Calc")
	if !ok {
		t.Fatal("failed to find method owner type")
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
	if !strings.Contains(hover.Contents.Value, "type Point<T> struct") {
		t.Fatalf("expected owner type hover markdown, got %q", hover.Contents.Value)
	}
}

func TestHoverLabels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "fn run() void {\nouter: while true {\n    break outer\n}\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &bytes.Buffer{}, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	checkHover := func(line, char int, expected string) {
		t.Helper()
		var out bytes.Buffer
		s.out = &out
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
		if !strings.Contains(hover.Contents.Value, expected) {
			t.Fatalf("expected hover to contain %q, got %q", expected, hover.Contents.Value)
		}
	}

	line, char, ok := findPosition(src, "outer: while")
	if !ok {
		t.Fatal("failed to find label declaration")
	}
	checkHover(line, char, "loop label outer")

	line, char, ok = findPosition(src, "break outer")
	if !ok {
		t.Fatal("failed to find label reference")
	}
	char += len("break ")
	checkHover(line, char, "loop label outer")
}

func TestHoverRecursiveGenericTypeDoesNotLoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.ferr")
	src := "type Node<T> struct {\n    Next: *Node<T>\n    Value: T\n}\n\nfn main() {\n    let n = .Node<i32>{}\n    n\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "let n =")
	if !ok {
		t.Fatal("failed to find local binding declaration")
	}
	char += len("let ")

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
	if !strings.Contains(hover.Contents.Value, "type Node<i32> struct") {
		t.Fatalf("expected recursive generic type hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Next: *Node<i32>") {
		t.Fatalf("expected instantiated recursive field type, got %q", hover.Contents.Value)
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
