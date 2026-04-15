package lsp

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	projectpkg "compiler/internal/core/project"
	"compiler/internal/core/source"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
	"compiler/internal/prelude"
	"compiler/internal/tokens"
)

func TestConvertDiagnosticsPreservesZeroWidthRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.fer")
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

func TestConvertDiagnosticsMarksUnusedWarningsAsUnnecessary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.fer")
	at := source.Position{Line: 1, Column: 1}
	loc := source.NewLocation(path, at, at)
	d := diagnostics.NewWarning("unused local").
		WithCode(diagnostics.WarnUnusedLocal).
		WithPrimaryLabel(&loc, "unused")

	out := convertDiagnostics([]*diagnostics.Diagnostic{d}, path)
	if len(out) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(out))
	}
	if len(out[0].Tags) != 1 || out[0].Tags[0] != diagTagUnnecessary {
		t.Fatalf("expected unnecessary diagnostic tag, got %#v", out[0].Tags)
	}
}

func TestPublishSyntaxDiagnosticsRecoversFromParserPanic(t *testing.T) {
	oldLex := lexSource
	oldParse := parseSource
	defer func() {
		lexSource = oldLex
		parseSource = oldParse
	}()

	lexSource = func(path, text string, diag *diagnostics.DiagnosticBag) []tokens.Token {
		return []tokens.Token{{Kind: tokens.EOF, Start: source.NewPosition(), End: source.NewPosition()}}
	}
	parseSource = func(path string, toks []tokens.Token, diag *diagnostics.DiagnosticBag) {
		panic("boom")
	}

	var out bytes.Buffer
	s := &Server{out: &out}

	path := filepath.Join(t.TempDir(), "main.fer")
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

func TestInitializeAdvertisesHoverDefinitionAndCompletionProvider(t *testing.T) {
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

	var definitionProvider bool
	if err := json.Unmarshal(payload.Capabilities["definitionProvider"], &definitionProvider); err != nil {
		t.Fatalf("failed to decode definitionProvider: %v", err)
	}
	if !definitionProvider {
		t.Fatal("expected definitionProvider=true")
	}

	var completionProvider map[string]json.RawMessage
	if err := json.Unmarshal(payload.Capabilities["completionProvider"], &completionProvider); err != nil {
		t.Fatalf("failed to decode completionProvider: %v", err)
	}
	var triggerChars []string
	if err := json.Unmarshal(completionProvider["triggerCharacters"], &triggerChars); err != nil {
		t.Fatalf("failed to decode completion trigger characters: %v", err)
	}
	if len(triggerChars) == 0 {
		t.Fatal("expected completion trigger characters")
	}

	if _, ok := payload.Capabilities["documentSymbolProvider"]; ok {
		t.Fatal("expected documentSymbolProvider to be omitted")
	}
}

func TestInitializedRegistersWatchedFilesWhenClientSupportsDynamicRegistration(t *testing.T) {
	var out bytes.Buffer
	s := &Server{out: &out, documents: make(map[string]openDocument)}

	s.handleRequest(rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params: mustRawJSON(t, initializeParams{
			Capabilities: initializeCapabilities{
				Workspace: &workspaceClientCapabilities{
					DidChangeWatchedFiles: &dynamicRegistrationClientCapabilities{DynamicRegistration: true},
				},
			},
		}),
	})
	out.Reset()

	s.handleRequest(rpcRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
	})

	bodies := decodeFramedBodies(t, out.String())
	if len(bodies) != 1 {
		t.Fatalf("expected 1 server request after initialized, got %d", len(bodies))
	}
	var req rpcRequest
	if err := json.Unmarshal(bodies[0], &req); err != nil {
		t.Fatalf("failed to decode registration request: %v", err)
	}
	if req.Method != "client/registerCapability" {
		t.Fatalf("expected client/registerCapability request, got %q", req.Method)
	}
	var params struct {
		Registrations []struct {
			ID              string `json:"id"`
			Method          string `json:"method"`
			RegisterOptions struct {
				Watchers []struct {
					GlobPattern string `json:"globPattern"`
				} `json:"watchers"`
			} `json:"registerOptions"`
		} `json:"registrations"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("failed to decode registerCapability params: %v", err)
	}
	if len(params.Registrations) != 1 {
		t.Fatalf("expected 1 registration, got %#v", params.Registrations)
	}
	if params.Registrations[0].Method != "workspace/didChangeWatchedFiles" {
		t.Fatalf("unexpected registration method: %#v", params.Registrations[0])
	}
	if len(params.Registrations[0].RegisterOptions.Watchers) != 1 || params.Registrations[0].RegisterOptions.Watchers[0].GlobPattern != "**/*.fer" {
		t.Fatalf("unexpected watched-file registration options: %#v", params.Registrations[0].RegisterOptions)
	}
}

func TestInitializedSkipsWatchedFileRegistrationWithoutClientSupport(t *testing.T) {
	var out bytes.Buffer
	s := &Server{out: &out, documents: make(map[string]openDocument)}

	s.handleRequest(rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  mustRawJSON(t, initializeParams{}),
	})
	out.Reset()

	s.handleRequest(rpcRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
	})

	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("expected no registration request without client support, got %q", out.String())
	}
}

func TestURIToPathStripsWindowsDriveSlash(t *testing.T) {
	got, err := uriToPath("file:///E:/Dev/Ferret/app/main.fer")
	if err != nil {
		t.Fatalf("unexpected uriToPath error: %v", err)
	}
	want := filepath.Clean(filepath.FromSlash("E:/Dev/Ferret/app/main.fer"))
	if got != want {
		t.Fatalf("expected windows drive path %q, got %q", want, got)
	}
}

func TestFileURIPathPrefixesWindowsDrive(t *testing.T) {
	got := fileURIPath("E:/Dev/Ferret/app/main.fer")
	if got != "/E:/Dev/Ferret/app/main.fer" {
		t.Fatalf("expected file URI path with drive slash, got %q", got)
	}
	if uri := (&url.URL{Scheme: "file", Path: got}).String(); uri != "file:///E:/Dev/Ferret/app/main.fer" {
		t.Fatalf("expected canonical windows file URI, got %q", uri)
	}
}

func TestHoverReturnsTypeFromSavedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
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
	path := filepath.Join(dir, "main.fer")
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
	if !strings.Contains(hover.Contents.Value, "[2]u8") {
		t.Fatalf("expected [2]u8 hover from overlay, got %q", hover.Contents.Value)
	}
}

func TestHoverReturnsKeywordDocumentation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn main() {\n    if true {\n        return\n    }\n}\n"
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
			Position:     lspPosition{Line: 1, Character: 4}, // "if"
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
	if !strings.Contains(hover.Contents.Value, "```ferret\nif\n```") {
		t.Fatalf("expected keyword code block in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "conditional") {
		t.Fatalf("expected keyword documentation text in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverRangeExpressionShowsSourceSyntaxWithoutType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn main() -> void {\n    for 2..=10:2 |v| {\n        v\n    }\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "2..=10:2")
	if !ok {
		t.Fatal("failed to find range expression")
	}
	char++

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
	if !strings.Contains(hover.Contents.Value, "2..=10:2") {
		t.Fatalf("expected source-like range hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "range<") {
		t.Fatalf("did not expect internal range type in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverShowsExpandedNamedConstraint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type numeric union {\n    i32,\n    i64,\n}\n\nfn add_numbers<T: numeric>(a: T, b: T) -> T {\n    return a + b\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "fn add_numbers<T: numeric>")
	if !ok {
		t.Fatal("failed to find numeric use")
	}
	char += len("fn add_numbers<T: ")

	var out bytes.Buffer
	s := &Server{out: &out, documents: make(map[string]openDocument)}
	uri := "file://" + filepath.ToSlash(path)
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
	if !strings.Contains(hover.Contents.Value, "type numeric union { i32, i64 }") {
		t.Fatalf("expected expanded constraint hover, got %q", hover.Contents.Value)
	}
}

func TestDefinitionReturnsFunctionDeclaration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn run2() -> void {\n}\n\nfn main() {\n    run2()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	declLine, declChar, ok := findPosition(src, "fn run2()")
	if !ok {
		t.Fatal("failed to find run2 declaration")
	}
	declChar += len("fn ")

	line, char, ok := findPosition(src, "    run2()")
	if !ok {
		t.Fatal("failed to find run2 call")
	}
	char += len("    ")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/definition",
		Params: mustRawJSON(t, definitionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	loc := decodeDefinitionResult(t, out.String())
	if loc == nil {
		t.Fatal("expected definition result")
	}
	if loc.URI != uri {
		t.Fatalf("expected same-file definition URI %q, got %q", uri, loc.URI)
	}
	if loc.Range.Start.Line != declLine || loc.Range.Start.Character != declChar {
		t.Fatalf("expected definition start at %d:%d, got %d:%d", declLine, declChar, loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestDefinitionCrossModuleFunction(t *testing.T) {
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "util", "math.fer")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o755); err != nil {
		t.Fatalf("failed to create module dir: %v", err)
	}
	moduleSrc := "fn Pick(v: i32) -> i32 {\n    return v\n}\n"
	if err := os.WriteFile(modulePath, []byte(moduleSrc), 0o644); err != nil {
		t.Fatalf("failed to write module source: %v", err)
	}

	path := filepath.Join(dir, "main.fer")
	src := "import \"util/math\"\n\nfn main() -> i32 {\n    return math::Pick(1)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	declLine, declChar, ok := findPosition(moduleSrc, "fn Pick(")
	if !ok {
		t.Fatal("failed to find Pick declaration")
	}
	declChar += len("fn ")

	line, char, ok := findPosition(src, "    return math::Pick(1)")
	if !ok {
		t.Fatal("failed to find Pick call")
	}
	char += len("    return math::")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	expectedURI := "file://" + filepath.ToSlash(modulePath)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/definition",
		Params: mustRawJSON(t, definitionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	loc := decodeDefinitionResult(t, out.String())
	if loc == nil {
		t.Fatal("expected definition result")
	}
	if loc.URI != expectedURI {
		t.Fatalf("expected cross-module URI %q, got %q", expectedURI, loc.URI)
	}
	if loc.Range.Start.Line != declLine || loc.Range.Start.Character != declChar {
		t.Fatalf("expected definition start at %d:%d, got %d:%d", declLine, declChar, loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestHoverWorksInsideStdlibFile(t *testing.T) {
	restore := usePackagedLibsForTest(t)
	defer restore()

	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	path := filepath.Join(dir, context.STD_LIB_DEV, "std", "mem.fer")
	srcBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read stdlib source: %v", err)
	}
	src := string(srcBytes)

	line, char, ok := findPosition(src, "    return malloc(align_up(size, PageSize))")
	if !ok {
		t.Fatal("failed to find align_up call in stdlib source")
	}
	char += len("    return malloc(")

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
		t.Fatal("expected hover result for stdlib source")
	}
	if !strings.Contains(hover.Contents.Value, "fn align_up(value: usize, align: usize) -> usize") {
		t.Fatalf("expected stdlib align_up hover, got %q", hover.Contents.Value)
	}
}

func TestDefinitionWorksInsideStdlibFile(t *testing.T) {
	restore := usePackagedLibsForTest(t)
	defer restore()

	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	path := filepath.Join(dir, context.STD_LIB_DEV, "std", "mem.fer")
	srcBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read stdlib source: %v", err)
	}
	src := string(srcBytes)

	declLine, declChar, ok := findPosition(src, "fn align_up(value: usize, align: usize) -> usize")
	if !ok {
		t.Fatal("failed to find align_up declaration in stdlib source")
	}
	declChar += len("fn ")

	line, char, ok := findPosition(src, "    return malloc(align_up(size, PageSize))")
	if !ok {
		t.Fatal("failed to find align_up call in stdlib source")
	}
	char += len("    return malloc(")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/definition",
		Params: mustRawJSON(t, definitionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	loc := decodeDefinitionResult(t, out.String())
	if loc == nil {
		t.Fatal("expected definition result for stdlib source")
	}
	if loc.URI != uri {
		t.Fatalf("expected stdlib definition URI %q, got %q", uri, loc.URI)
	}
	if loc.Range.Start.Line != declLine || loc.Range.Start.Character != declChar {
		t.Fatalf("expected stdlib definition at %d:%d, got %d:%d", declLine, declChar, loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestDefinitionOverlayReturnsOriginalFileURI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	onDisk := "fn old() -> void {\n}\n\nfn main() {\n    old()\n}\n"
	overlay := "fn run2() -> void {\n}\n\nfn main() {\n    run2()\n}\n"
	if err := os.WriteFile(path, []byte(onDisk), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	declLine, declChar, ok := findPosition(overlay, "fn run2()")
	if !ok {
		t.Fatal("failed to find run2 declaration")
	}
	declChar += len("fn ")

	line, char, ok := findPosition(overlay, "    run2()")
	if !ok {
		t.Fatal("failed to find run2 call")
	}
	char += len("    ")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{
		out:        &out,
		hoverCache: make(map[string]hoverCacheEntry),
		documents: map[string]openDocument{
			uri: {Version: 3, Text: overlay},
		},
	}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/definition",
		Params: mustRawJSON(t, definitionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	loc := decodeDefinitionResult(t, out.String())
	if loc == nil {
		t.Fatal("expected definition result")
	}
	if loc.URI != uri {
		t.Fatalf("expected overlay definition URI %q, got %q", uri, loc.URI)
	}
	if loc.Range.Start.Line != declLine || loc.Range.Start.Character != declChar {
		t.Fatalf("expected definition start at %d:%d, got %d:%d", declLine, declChar, loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestDefinitionGenericStaticOwnerCallResolvesOwnerAndMethod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Circle<T> struct {\n    Rad: T\n}\n\nfn Circle<T>::New(v: T) -> Self {\n    return .{ .Rad = v }\n}\n\nfn main() -> void {\n    let c = Circle<i32>::New(1)\n    c\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	typeLine, typeChar, ok := findPosition(src, "type Circle<T> struct")
	if !ok {
		t.Fatal("failed to find type declaration")
	}
	typeChar += len("type ")

	methodLine, methodChar, ok := findPosition(src, "fn Circle<T>::New(v: T) -> Self")
	if !ok {
		t.Fatal("failed to find method declaration")
	}
	methodChar += len("fn Circle<T>::")

	line, char, ok := findPosition(src, "    let c = Circle<i32>::New(1)")
	if !ok {
		t.Fatal("failed to find static owner call")
	}
	circleChar := char + len("    let c = ")
	newChar := char + len("    let c = Circle<i32>::")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	reqOwner := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/definition",
		Params: mustRawJSON(t, definitionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: circleChar},
		}),
	}
	s.handleRequest(reqOwner)
	ownerLoc := decodeDefinitionResult(t, out.String())
	if ownerLoc == nil {
		t.Fatal("expected owner definition result")
	}
	if ownerLoc.URI != uri {
		t.Fatalf("expected owner definition URI %q, got %q", uri, ownerLoc.URI)
	}
	if ownerLoc.Range.Start.Line != typeLine || ownerLoc.Range.Start.Character != typeChar {
		t.Fatalf("expected owner definition at %d:%d, got %d:%d", typeLine, typeChar, ownerLoc.Range.Start.Line, ownerLoc.Range.Start.Character)
	}

	out.Reset()
	reqMethod := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "textDocument/definition",
		Params: mustRawJSON(t, definitionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: newChar},
		}),
	}
	s.handleRequest(reqMethod)
	methodLoc := decodeDefinitionResult(t, out.String())
	if methodLoc == nil {
		t.Fatal("expected method definition result")
	}
	if methodLoc.URI != uri {
		t.Fatalf("expected method definition URI %q, got %q", uri, methodLoc.URI)
	}
	if methodLoc.Range.Start.Line != methodLine || methodLoc.Range.Start.Character != methodChar {
		t.Fatalf("expected method definition at %d:%d, got %d:%d", methodLine, methodChar, methodLoc.Range.Start.Line, methodLoc.Range.Start.Character)
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
	path := filepath.Join(dir, "main.fer")
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

func TestHoverSkipsIndexBuildWhenOpenDocumentHasSyntaxErrors(t *testing.T) {
	oldParseProject := parseProject
	defer func() { parseProject = oldParseProject }()

	calls := 0
	parseProject = func(path string) compiler.Result {
		calls++
		return fakeHoverResult(path, "i32")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	if err := os.WriteFile(path, []byte("fn main() {}"), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	uri := "file://" + filepath.ToSlash(path)
	cachedLoc := source.NewLocation(path, source.Position{Line: 1, Column: 1}, source.Position{Line: 1, Column: 3})
	cachedIndex := &hoverIndex{
		candidates: []hoverCandidate{{
			markdown: "cached hover",
			location: cachedLoc,
			span:     locationSpan(cachedLoc),
			priority: 1,
		}},
	}
	var out bytes.Buffer
	s := &Server{
		out: &out,
		documents: map[string]openDocument{
			uri: {Version: 7, Text: "fn main(", HasSyntaxErrors: true},
		},
		hoverCache: map[string]hoverCacheEntry{
			uri: {
				Mode:      "file",
				FileStamp: fileStamp(path),
				Index:     cachedIndex,
			},
		},
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

	if calls != 0 {
		t.Fatalf("expected hover parse to be skipped on syntax-invalid open doc, got %d calls", calls)
	}
	hover := decodeHoverResult(t, out.String())
	if hover == nil || hover.Contents.Value != "cached hover" {
		t.Fatalf("expected cached hover fallback on syntax-invalid open doc, got %#v", hover)
	}
}

func TestBuildHoverIndexFromResultDoesNotReadDiskForEmptyOverlayText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	if err := os.WriteFile(path, []byte("mod::Value"), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	start := source.Position{Line: 1, Column: 1, Index: 0}
	end := source.Position{Line: 1, Column: 11, Index: 10}
	loc := source.NewLocation(path, start, end)
	ident := &ast.Ident{Path: []string{"mod", "Value"}, Location: loc}
	fnName := &ast.Ident{Path: []string{"Value"}, Location: loc}
	fnDecl := &ast.FuncDecl{Name: fnName, Location: loc}
	sym := symbols.New("Value", symbols.SymbolFunc, fnDecl)

	typeInfo := typeinfo.NewModuleInfo()
	bindings := binding.NewModuleInfo()
	bindings.BindNode(ident, &binding.Resolution{
		Kind:   binding.ResolutionSymbol,
		Symbol: sym,
	})
	mod := &context.Module{
		FilePath: path,
		Types:    typeInfo,
		Bindings: bindings,
	}
	result := compiler.Result{
		Entry:   mod,
		Modules: []*context.Module{mod},
	}

	index := buildHoverIndexFromResult(result, path, path, "", false)
	hover := hoverFromIndex(index, source.Position{Line: 1, Column: 7})
	if hover != nil {
		t.Fatalf("expected no hover when overlay text is intentionally empty, got %#v", hover)
	}

	index = buildHoverIndexFromResult(result, path, path, "", true)
	hover = hoverFromIndex(index, source.Position{Line: 1, Column: 7})
	if hover == nil {
		t.Fatal("expected disk-backed hover when readSourceFromDisk is true")
	}
}

func TestPublishSemanticDiagnosticsContinuesWhenOpenDocumentHasSyntaxErrors(t *testing.T) {
	oldParseSemanticProject := parseSemanticProject
	defer func() { parseSemanticProject = oldParseSemanticProject }()

	calls := 0
	parseSemanticProject = func(path string) compiler.Result {
		calls++
		return compiler.Result{Diagnostics: diagnostics.NewDiagnosticBag(path)}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	uri := "file://" + filepath.ToSlash(path)
	seq := uint64(3)
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		out:          &bytes.Buffer{},
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 9, Text: "fn main(", HasSyntaxErrors: true},
		},
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: seq},
		},
	}

	s.publishSemanticDiagnostics(uri, path, seq)

	if calls != 1 {
		t.Fatalf("expected semantic parse to continue on syntax-invalid open doc, got %d calls", calls)
	}
}

func TestPublishSemanticDiagnosticsReusesCachedSnapshotForSameVersion(t *testing.T) {
	oldParseSemanticProject := parseSemanticProject
	defer func() { parseSemanticProject = oldParseSemanticProject }()

	calls := 0
	parseSemanticProject = func(path string) compiler.Result {
		calls++
		return compiler.Result{Diagnostics: diagnostics.NewDiagnosticBag(path)}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	uri := "file://" + filepath.ToSlash(path)
	seq := uint64(4)
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		out:          &bytes.Buffer{},
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 3, Text: "fn main() {}"},
		},
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: seq},
		},
	}

	s.publishSemanticDiagnostics(uri, path, seq)
	s.publishSemanticDiagnostics(uri, path, seq)

	if calls != 1 {
		t.Fatalf("expected semantic diagnostics cache reuse, got %d parses", calls)
	}
}

func TestPublishSemanticDiagnosticsReusesCachedSnapshotForSameTextNewVersion(t *testing.T) {
	oldParseSemanticProject := parseSemanticProject
	defer func() { parseSemanticProject = oldParseSemanticProject }()

	calls := 0
	parseSemanticProject = func(path string) compiler.Result {
		calls++
		return compiler.Result{Diagnostics: diagnostics.NewDiagnosticBag(path)}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	uri := "file://" + filepath.ToSlash(path)
	seq := uint64(44)
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		out:          &bytes.Buffer{},
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 3, Text: "fn main() {}"},
		},
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: seq},
		},
	}

	s.publishSemanticDiagnostics(uri, path, seq)
	s.documents[uri] = openDocument{Version: 4, Text: "fn main() {}"}
	s.publishSemanticDiagnostics(uri, path, seq)

	if calls != 1 {
		t.Fatalf("expected semantic diagnostics cache reuse across version bump with same text, got %d parses", calls)
	}
}

func TestPublishSemanticDiagnosticsReparsesAfterVersionChange(t *testing.T) {
	oldParseSemanticProject := parseSemanticProject
	defer func() { parseSemanticProject = oldParseSemanticProject }()

	calls := 0
	parseSemanticProject = func(path string) compiler.Result {
		calls++
		return compiler.Result{Diagnostics: diagnostics.NewDiagnosticBag(path)}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	uri := "file://" + filepath.ToSlash(path)
	seq := uint64(5)
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		out:          &bytes.Buffer{},
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 3, Text: "fn main() {}"},
		},
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: seq},
		},
	}

	s.publishSemanticDiagnostics(uri, path, seq)
	s.documents[uri] = openDocument{Version: 4, Text: "fn main() -> void {}"}
	s.publishSemanticDiagnostics(uri, path, seq)

	if calls != 2 {
		t.Fatalf("expected semantic diagnostics cache invalidation after version change, got %d parses", calls)
	}
}

func TestPublishSemanticDiagnosticsReparsesAfterDependencyChange(t *testing.T) {
	oldParseSemanticProject := parseSemanticProject
	defer func() { parseSemanticProject = oldParseSemanticProject }()

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	depPath := filepath.Join(dir, "dep.fer")
	if err := os.WriteFile(depPath, []byte("v1"), 0o644); err != nil {
		t.Fatalf("failed to write dependency source: %v", err)
	}

	calls := 0
	parseSemanticProject = func(path string) compiler.Result {
		calls++
		result := compiler.Result{
			Entry:       &context.Module{FilePath: path},
			Modules:     []*context.Module{{FilePath: depPath}},
			Diagnostics: diagnostics.NewDiagnosticBag(path),
		}
		return result
	}

	uri := "file://" + filepath.ToSlash(path)
	seq := uint64(45)
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		out:          &bytes.Buffer{},
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 3, Text: "fn main() {}"},
		},
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: seq},
		},
	}

	s.publishSemanticDiagnostics(uri, path, seq)
	if err := os.WriteFile(depPath, []byte("dependency changed"), 0o644); err != nil {
		t.Fatalf("failed to update dependency source: %v", err)
	}
	s.publishSemanticDiagnostics(uri, path, seq)

	if calls != 2 {
		t.Fatalf("expected semantic diagnostics reparse after dependency change, got %d parses", calls)
	}
}

func TestHoverReusesIndexBuiltBySemanticDiagnostics(t *testing.T) {
	oldParseProject := parseProject
	oldParseSemanticProject := parseSemanticProject
	defer func() {
		parseProject = oldParseProject
		parseSemanticProject = oldParseSemanticProject
	}()

	hoverParses := 0
	parseProject = func(path string) compiler.Result {
		hoverParses++
		return fakeHoverResult(path, "never-used")
	}
	parseSemanticProject = func(path string) compiler.Result {
		result := fakeHoverResult(path, "i32")
		result.Diagnostics = diagnostics.NewDiagnosticBag(path)
		return result
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	uri := "file://" + filepath.ToSlash(path)
	seq := uint64(6)
	src := "x"
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	var diagOut bytes.Buffer
	s := &Server{
		out:          &diagOut,
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 2, Text: src},
		},
		hoverCache: make(map[string]hoverCacheEntry),
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: seq},
		},
	}

	s.publishSemanticDiagnostics(uri, path, seq)

	var hoverOut bytes.Buffer
	s.out = &hoverOut
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

	if hoverParses != 0 {
		t.Fatalf("expected hover to reuse semantic-built hover cache, got %d hover parses", hoverParses)
	}
	hover := decodeHoverResult(t, hoverOut.String())
	if hover == nil || hover.Contents.Value != "```ferret\ni32\n```" {
		t.Fatalf("expected hover from semantic-built cache, got %#v", hover)
	}
}

func TestHoverReusesIndexForSameTextNewVersion(t *testing.T) {
	oldParseProject := parseProject
	defer func() { parseProject = oldParseProject }()

	calls := 0
	parseProject = func(path string) compiler.Result {
		calls++
		return fakeHoverResult(path, "i32")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
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
	s.mu.Lock()
	s.documents[uri] = openDocument{Version: 2, Text: "fn main() {}"}
	s.mu.Unlock()
	s.handleRequest(req)

	if calls != 1 {
		t.Fatalf("expected hover cache reuse across version bump with same text, got %d parses", calls)
	}
}

func TestHoverReparsesAfterDependencyChange(t *testing.T) {
	oldParseProject := parseProject
	defer func() { parseProject = oldParseProject }()

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	depPath := filepath.Join(dir, "dep.fer")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}
	if err := os.WriteFile(depPath, []byte("v1"), 0o644); err != nil {
		t.Fatalf("failed to write dependency source: %v", err)
	}

	calls := 0
	parseProject = func(path string) compiler.Result {
		calls++
		return fakeHoverResultWithDependency(path, "i32", depPath)
	}

	uri := "file://" + filepath.ToSlash(path)
	var out bytes.Buffer
	s := &Server{
		out: &out,
		documents: map[string]openDocument{
			uri: {Version: 1, Text: "x"},
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
	if err := os.WriteFile(depPath, []byte("dependency changed"), 0o644); err != nil {
		t.Fatalf("failed to update dependency source: %v", err)
	}
	s.handleRequest(req)

	if calls != 2 {
		t.Fatalf("expected hover reparse after dependency change, got %d parses", calls)
	}
}

func TestHoverFileCacheReparsesAfterDependencyChange(t *testing.T) {
	oldParseProject := parseProject
	defer func() { parseProject = oldParseProject }()

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	depPath := filepath.Join(dir, "dep.fer")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}
	if err := os.WriteFile(depPath, []byte("v1"), 0o644); err != nil {
		t.Fatalf("failed to write dependency source: %v", err)
	}

	calls := 0
	parseProject = func(path string) compiler.Result {
		calls++
		return fakeHoverResultWithDependency(path, "i32", depPath)
	}

	uri := "file://" + filepath.ToSlash(path)
	var out bytes.Buffer
	s := &Server{
		out:        &out,
		documents:  make(map[string]openDocument),
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
	if err := os.WriteFile(depPath, []byte("dependency changed"), 0o644); err != nil {
		t.Fatalf("failed to update dependency source: %v", err)
	}
	s.handleRequest(req)

	if calls != 2 {
		t.Fatalf("expected file-backed hover cache to reparse after dependency change, got %d parses", calls)
	}
}

func TestLSPKeepsProjectResolutionAndInvalidationProjectLocal(t *testing.T) {
	restore := usePackagedLibsForTest(t)
	defer restore()

	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	parent := t.TempDir()
	projA := filepath.Join(parent, "projA")
	projB := filepath.Join(parent, "projB")
	depA := filepath.Join(parent, "deps", "pkgA")
	depB := filepath.Join(parent, "deps", "pkgB")

	write(filepath.Join(projA, "fer.ret"), `[package]
name = "projA"

[dependencies]
tool = "../deps/pkgA"
`)
	write(filepath.Join(projB, "fer.ret"), `[package]
name = "projB"

[dependencies]
tool = "../deps/pkgB"
`)
	write(filepath.Join(depA, "fer.ret"), `[package]
name = "pkgA"
`)
	write(filepath.Join(depB, "fer.ret"), `[package]
name = "pkgB"
`)
	depAPath := filepath.Join(depA, "api.fer")
	depBPath := filepath.Join(depB, "api.fer")
	write(depAPath, "fn Value() -> i32 {\n    return 1\n}\n")
	write(depBPath, "fn Value() -> bool {\n    return true\n}\n")

	mainASrc := "import \"tool/api\"\n\nfn main() -> i32 {\n    return api::Value()\n}\n"
	mainBSrc := "import \"tool/api\"\n\nfn main() -> bool {\n    return api::Value()\n}\n"
	mainAPath := filepath.Join(projA, "main.fer")
	mainBPath := filepath.Join(projB, "main.fer")
	write(mainAPath, mainASrc)
	write(mainBPath, mainBSrc)

	uriA := "file://" + filepath.ToSlash(mainAPath)
	uriB := "file://" + filepath.ToSlash(mainBPath)
	lineA, charA, ok := findPosition(mainASrc, "    return api::Value()")
	if !ok {
		t.Fatal("failed to find project A hover position")
	}
	charA += len("    return api::")
	lineB, charB, ok := findPosition(mainBSrc, "    return api::Value()")
	if !ok {
		t.Fatal("failed to find project B hover position")
	}
	charB += len("    return api::")

	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		out:          &bytes.Buffer{},
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uriA: {Version: 1, Text: mainASrc},
			uriB: {Version: 1, Text: mainBSrc},
		},
		hoverCache:   make(map[string]hoverCacheEntry),
		semanticDeps: make(map[string][]string),
		dependents:   make(map[string]map[string]struct{}),
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uriA: {seq: 1},
			uriB: {seq: 1},
		},
	}

	s.publishSemanticDiagnostics(uriA, mainAPath, 1)
	s.publishSemanticDiagnostics(uriB, mainBPath, 1)

	if got := s.semanticDiagnostics[uriA].snapshot; got == nil || !strings.Contains(strings.Join(got.DepFiles, "\n"), depAPath) {
		t.Fatalf("expected project A semantic snapshot to depend on %q, got %#v", depAPath, got)
	}
	if got := s.semanticDiagnostics[uriB].snapshot; got == nil || !strings.Contains(strings.Join(got.DepFiles, "\n"), depBPath) {
		t.Fatalf("expected project B semantic snapshot to depend on %q, got %#v", depBPath, got)
	}

	var hoverOutA bytes.Buffer
	s.out = &hoverOutA
	s.handleRequest(rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uriA},
			Position:     lspPosition{Line: lineA, Character: charA},
		}),
	})
	hoverA := decodeHoverResult(t, hoverOutA.String())
	if hoverA == nil || !strings.Contains(hoverA.Contents.Value, "fn Value() -> i32") {
		t.Fatalf("expected project A hover to resolve its own dependency, got %#v", hoverA)
	}

	var hoverOutB bytes.Buffer
	s.out = &hoverOutB
	s.handleRequest(rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uriB},
			Position:     lspPosition{Line: lineB, Character: charB},
		}),
	})
	hoverB := decodeHoverResult(t, hoverOutB.String())
	if hoverB == nil || !strings.Contains(hoverB.Contents.Value, "fn Value() -> bool") {
		t.Fatalf("expected project B hover to resolve its own dependency, got %#v", hoverB)
	}

	depAURI := "file://" + filepath.ToSlash(depAPath)
	updatedDepASrc := "fn Value() -> i32 {\n    return 2\n}\n"
	s.handleDidChange(mustRawJSON(t, didChangeParams{
		TextDocument: versionedTextDocumentIdentifier{URI: depAURI, Version: 2},
		ContentChanges: []textDocumentContentChangeEvent{
			{Text: updatedDepASrc},
		},
	}))

	if entry := s.semanticDiagnostics[uriA]; entry == nil || entry.snapshot != nil || entry.seq != 2 || entry.timer == nil {
		t.Fatalf("expected project A dependent cache to be invalidated and requeued, got %#v", entry)
	}
	if entry := s.semanticDiagnostics[uriB]; entry == nil || entry.snapshot == nil || entry.seq != 1 {
		t.Fatalf("expected project B semantic snapshot to remain intact, got %#v", entry)
	}
	if _, ok := s.hoverCache[uriA]; ok {
		t.Fatalf("expected project A hover cache to be invalidated after its dependency changed")
	}
	if _, ok := s.hoverCache[uriB]; !ok {
		t.Fatalf("expected project B hover cache to remain after project A dependency changed")
	}

	s.semanticDiagnostics[uriA].timer.Stop()
	if depEntry := s.semanticDiagnostics[depAURI]; depEntry != nil && depEntry.timer != nil {
		depEntry.timer.Stop()
	}
}

func TestHandleDidChangeWatchedFilesInvalidatesProjectLocalDependents(t *testing.T) {
	restore := usePackagedLibsForTest(t)
	defer restore()

	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	parent := t.TempDir()
	projA := filepath.Join(parent, "projA")
	projB := filepath.Join(parent, "projB")
	depA := filepath.Join(parent, "deps", "pkgA")
	depB := filepath.Join(parent, "deps", "pkgB")

	write(filepath.Join(projA, "fer.ret"), `[package]
name = "projA"

[dependencies]
tool = "../deps/pkgA"
`)
	write(filepath.Join(projB, "fer.ret"), `[package]
name = "projB"

[dependencies]
tool = "../deps/pkgB"
`)
	write(filepath.Join(depA, "fer.ret"), `[package]
name = "pkgA"
`)
	write(filepath.Join(depB, "fer.ret"), `[package]
name = "pkgB"
`)
	depAPath := filepath.Join(depA, "api.fer")
	depBPath := filepath.Join(depB, "api.fer")
	write(depAPath, "fn Value() -> i32 {\n    return 1\n}\n")
	write(depBPath, "fn Value() -> bool {\n    return true\n}\n")

	mainASrc := "import \"tool/api\"\n\nfn main() -> i32 {\n    return api::Value()\n}\n"
	mainBSrc := "import \"tool/api\"\n\nfn main() -> bool {\n    return api::Value()\n}\n"
	mainAPath := filepath.Join(projA, "main.fer")
	mainBPath := filepath.Join(projB, "main.fer")
	write(mainAPath, mainASrc)
	write(mainBPath, mainBSrc)

	uriA := "file://" + filepath.ToSlash(mainAPath)
	uriB := "file://" + filepath.ToSlash(mainBPath)
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		out:          &bytes.Buffer{},
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uriA: {Version: 1, Text: mainASrc},
			uriB: {Version: 1, Text: mainBSrc},
		},
		hoverCache:   make(map[string]hoverCacheEntry),
		semanticDeps: make(map[string][]string),
		dependents:   make(map[string]map[string]struct{}),
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uriA: {seq: 1},
			uriB: {seq: 1},
		},
	}

	s.publishSemanticDiagnostics(uriA, mainAPath, 1)
	s.publishSemanticDiagnostics(uriB, mainBPath, 1)
	s.hoverCache[uriA] = hoverCacheEntry{Mode: "doc", Version: 1}
	s.hoverCache[uriB] = hoverCacheEntry{Mode: "doc", Version: 1}

	s.handleDidChangeWatchedFiles(mustRawJSON(t, didChangeWatchedFilesParams{
		Changes: []fileEvent{
			{URI: "file://" + filepath.ToSlash(depAPath), Type: 2},
		},
	}))

	if entry := s.semanticDiagnostics[uriA]; entry == nil || entry.snapshot != nil || entry.seq != 2 || entry.timer == nil {
		t.Fatalf("expected project A dependent cache to be invalidated and requeued, got %#v", entry)
	}
	if entry := s.semanticDiagnostics[uriB]; entry == nil || entry.snapshot == nil || entry.seq != 1 {
		t.Fatalf("expected project B semantic snapshot to remain intact, got %#v", entry)
	}
	if _, ok := s.hoverCache[uriA]; ok {
		t.Fatalf("expected project A hover cache to be invalidated after watched dependency change")
	}
	if _, ok := s.hoverCache[uriB]; !ok {
		t.Fatalf("expected project B hover cache to remain after watched dependency change")
	}

	s.semanticDiagnostics[uriA].timer.Stop()
}

func TestHandleDidChangeWatchedFilesInvalidatesFileBackedHoverCache(t *testing.T) {
	s := &Server{
		hoverCache: make(map[string]hoverCacheEntry),
		hoverDeps:  make(map[string][]string),
		hoverDependents: map[string]map[string]struct{}{
			"/tmp/dep.fer": {
				"file:///tmp/closed.fer": {},
			},
		},
	}
	s.hoverCache["file:///tmp/closed.fer"] = hoverCacheEntry{Mode: "file", FileStamp: "1:1"}
	s.hoverDeps["file:///tmp/closed.fer"] = []string{"/tmp/dep.fer"}

	s.handleDidChangeWatchedFiles(mustRawJSON(t, didChangeWatchedFilesParams{
		Changes: []fileEvent{
			{URI: "file:///tmp/dep.fer", Type: 2},
		},
	}))

	if _, ok := s.hoverCache["file:///tmp/closed.fer"]; ok {
		t.Fatalf("expected file-backed hover cache to be invalidated")
	}
	if deps := s.hoverDeps["file:///tmp/closed.fer"]; len(deps) != 0 {
		t.Fatalf("expected file-backed hover dependency entry to be cleared, got %#v", deps)
	}
	if len(s.hoverDependents) != 0 {
		t.Fatalf("expected file-backed reverse dependency index to be cleared, got %#v", s.hoverDependents)
	}
}

func TestDefinitionReusesIndexBuiltBySemanticDiagnostics(t *testing.T) {
	oldParseProject := parseProject
	defer func() { parseProject = oldParseProject }()

	hoverParses := 0
	parseProject = func(path string) compiler.Result {
		hoverParses++
		return fakeHoverResult(path, "never-used")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn answer() -> i32 {\n    return 42\n}\n\nfn main() -> i32 {\n    return answer()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}
	line, char, ok := findPosition(src, "answer()")
	if !ok {
		t.Fatal("failed to find definition position")
	}

	uri := "file://" + filepath.ToSlash(path)
	seq := uint64(7)
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	var diagOut bytes.Buffer
	s := &Server{
		out:          &diagOut,
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 2, Text: src},
		},
		hoverCache: make(map[string]hoverCacheEntry),
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: seq},
		},
	}

	s.publishSemanticDiagnostics(uri, path, seq)

	var defOut bytes.Buffer
	s.out = &defOut
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/definition",
		Params: mustRawJSON(t, definitionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	if hoverParses != 0 {
		t.Fatalf("expected definition to reuse semantic-built hover cache, got %d hover parses", hoverParses)
	}
	loc := decodeDefinitionResult(t, defOut.String())
	if loc == nil || loc.Range.Start.Line != 0 {
		t.Fatalf("expected definition from semantic-built cache, got %#v", loc)
	}
}

func TestCompletionReusesIndexBuiltBySemanticDiagnostics(t *testing.T) {
	oldParseProject := parseProject
	defer func() { parseProject = oldParseProject }()

	hoverParses := 0
	parseProject = func(path string) compiler.Result {
		hoverParses++
		return fakeHoverResult(path, "never-used")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn printVal() -> void {}\n\nfn main() -> void {\n    pri\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}
	line, char, ok := findPosition(src, "    pri")
	if !ok {
		t.Fatal("failed to find completion position")
	}
	char += len("    pri")

	uri := "file://" + filepath.ToSlash(path)
	seq := uint64(8)
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	var diagOut bytes.Buffer
	s := &Server{
		out:          &diagOut,
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 2, Text: src},
		},
		hoverCache: make(map[string]hoverCacheEntry),
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: seq},
		},
	}

	s.publishSemanticDiagnostics(uri, path, seq)

	var completionOut bytes.Buffer
	s.out = &completionOut
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/completion",
		Params: mustRawJSON(t, completionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	if hoverParses != 0 {
		t.Fatalf("expected completion to reuse semantic-built hover cache, got %d hover parses", hoverParses)
	}
	items := decodeCompletionResult(t, completionOut.String())
	found := false
	for _, item := range items {
		if item.Label == "printVal" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected completion from semantic-built cache, got %#v", items)
	}
}

func TestPublishSemanticDiagnosticsReportsUseAfterMove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn Node::Read(self) -> i32 {
    return self.Value
}

fn main(n: Node) -> i32 {
    return n.Read() + n.Read()
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	seq := uint64(1)
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		out:          &out,
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 1, Text: src},
		},
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: seq},
		},
	}

	s.publishSemanticDiagnostics(uri, path, seq)

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
	found := false
	for _, diag := range params.Diagnostics {
		if diag.Code == diagnostics.ErrUseAfterMove {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, params.Diagnostics)
	}
}

func TestHandleDidSaveReportsUseAfterMove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn Node::Read(self) -> i32 {
    return self.Value
}

fn main(n: Node) -> i32 {
    return n.Read() + n.Read()
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument)}

	s.handleDidSave(mustRawJSON(t, didSaveParams{
		TextDocument: textDocumentIdentifier{URI: uri},
	}))

	parts := strings.SplitN(out.String(), "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatalf("expected framed LSP message, got: %q", out.String())
	}
	var notif rpcNotification
	if err := json.Unmarshal([]byte(parts[1]), &notif); err != nil {
		t.Fatalf("failed to decode notification body: %v", err)
	}
	paramsRaw, err := json.Marshal(notif.Params)
	if err != nil {
		t.Fatalf("failed to re-marshal params: %v", err)
	}
	var params publishDiagnosticsParams
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		t.Fatalf("failed to decode diagnostics params: %v", err)
	}
	found := false
	for _, diag := range params.Diagnostics {
		if diag.Code == diagnostics.ErrUseAfterMove {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, params.Diagnostics)
	}
}

func TestHandleDidSaveReusesCachedSemanticSnapshot(t *testing.T) {
	oldParseSemanticProject := parseSemanticProject
	defer func() { parseSemanticProject = oldParseSemanticProject }()

	calls := 0
	parseSemanticProject = func(path string) compiler.Result {
		calls++
		return compiler.Result{Diagnostics: diagnostics.NewDiagnosticBag(path)}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	if err := os.WriteFile(path, []byte("fn main() {}"), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{
		out: &out,
		documents: map[string]openDocument{
			uri: {Version: 7, Text: "fn main() {}"},
		},
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {
				seq: 1,
				snapshot: &semanticSnapshot{
					Version:   7,
					TextStamp: textStamp("fn main() {}"),
					Diagnostics: []lspDiagnostic{{
						Range:    lspRange{Start: lspPosition{Line: 0, Character: 0}, End: lspPosition{Line: 0, Character: 1}},
						Severity: 1,
						Code:     diagnostics.ErrUseAfterMove,
						Source:   "ferret",
						Message:  "cached moved-value diagnostic",
					}},
				},
			},
		},
	}

	s.handleDidSave(mustRawJSON(t, didSaveParams{
		TextDocument: textDocumentIdentifier{URI: uri},
	}))

	if calls != 0 {
		t.Fatalf("expected didSave to reuse cached semantic snapshot, got %d parses", calls)
	}

	parts := strings.SplitN(out.String(), "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatalf("expected framed LSP message, got: %q", out.String())
	}
	var notif rpcNotification
	if err := json.Unmarshal([]byte(parts[1]), &notif); err != nil {
		t.Fatalf("failed to decode notification body: %v", err)
	}
	paramsRaw, err := json.Marshal(notif.Params)
	if err != nil {
		t.Fatalf("failed to re-marshal params: %v", err)
	}
	var params publishDiagnosticsParams
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		t.Fatalf("failed to decode diagnostics params: %v", err)
	}
	if len(params.Diagnostics) != 1 || params.Diagnostics[0].Message != "cached moved-value diagnostic" {
		t.Fatalf("expected cached didSave diagnostics, got %#v", params.Diagnostics)
	}
}

func TestSemanticDependencyIndexTracksLatestSnapshotDependencies(t *testing.T) {
	s := &Server{
		semanticDeps: make(map[string][]string),
		dependents:   make(map[string]map[string]struct{}),
	}

	s.updateSemanticDependenciesLocked("file:///main.fer", []string{"/tmp/dep1.fer", "/tmp/dep2.fer"})
	s.updateSemanticDependenciesLocked("file:///main.fer", []string{"/tmp/dep2.fer", "/tmp/dep3.fer"})

	deps := s.semanticDeps["file:///main.fer"]
	if len(deps) != 2 || deps[0] != "/tmp/dep2.fer" || deps[1] != "/tmp/dep3.fer" {
		t.Fatalf("unexpected stored semantic deps: %#v", deps)
	}
	if dependents := s.dependents["/tmp/dep1.fer"]; len(dependents) != 0 {
		t.Fatalf("expected old dependency dependents to be cleared, got %#v", dependents)
	}
	if _, ok := s.dependents["/tmp/dep2.fer"]["file:///main.fer"]; !ok {
		t.Fatalf("expected dep2 to track the document, got %#v", s.dependents["/tmp/dep2.fer"])
	}
	if _, ok := s.dependents["/tmp/dep3.fer"]["file:///main.fer"]; !ok {
		t.Fatalf("expected dep3 to track the document, got %#v", s.dependents["/tmp/dep3.fer"])
	}
}

func TestSemanticDependencyIndexClearsOnClose(t *testing.T) {
	s := &Server{
		semanticDeps: make(map[string][]string),
		dependents:   make(map[string]map[string]struct{}),
	}

	s.updateSemanticDependenciesLocked("file:///main.fer", []string{"/tmp/dep1.fer", "/tmp/dep2.fer"})
	s.clearSemanticDependenciesLocked("file:///main.fer")

	if deps := s.semanticDeps["file:///main.fer"]; len(deps) != 0 {
		t.Fatalf("expected document dependency entry to be cleared, got %#v", deps)
	}
	if len(s.dependents) != 0 {
		t.Fatalf("expected reverse dependency index to be cleared, got %#v", s.dependents)
	}
}

func TestInvalidateDependentsClearsDependentCaches(t *testing.T) {
	s := &Server{
		hoverCache: make(map[string]hoverCacheEntry),
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			"file:///main.fer":  {snapshot: &semanticSnapshot{Version: 1}},
			"file:///other.fer": {snapshot: &semanticSnapshot{Version: 1}},
		},
		dependents: map[string]map[string]struct{}{
			"/tmp/dep.fer": {
				"file:///main.fer":  {},
				"file:///other.fer": {},
			},
		},
	}
	s.hoverCache["file:///main.fer"] = hoverCacheEntry{Mode: "doc", Version: 1}
	s.hoverCache["file:///other.fer"] = hoverCacheEntry{Mode: "doc", Version: 1}

	uris := s.invalidateDependentsLocked("/tmp/dep.fer", "file:///other.fer")

	if len(uris) != 1 || uris[0] != "file:///main.fer" {
		t.Fatalf("expected only main.fer to be invalidated, got %#v", uris)
	}
	if _, ok := s.hoverCache["file:///main.fer"]; ok {
		t.Fatalf("expected main.fer hover cache to be cleared")
	}
	if entry := s.semanticDiagnostics["file:///main.fer"]; entry == nil || entry.snapshot != nil {
		t.Fatalf("expected main.fer semantic snapshot to be cleared, got %#v", entry)
	}
	if _, ok := s.hoverCache["file:///other.fer"]; !ok {
		t.Fatalf("expected excluded dependent hover cache to remain")
	}
	if entry := s.semanticDiagnostics["file:///other.fer"]; entry == nil || entry.snapshot == nil {
		t.Fatalf("expected excluded dependent semantic snapshot to remain, got %#v", entry)
	}
}

func TestScheduleDependentSemanticDiagnosticsQueuesOpenDependents(t *testing.T) {
	uri := "file:///tmp/main.fer"
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		semanticGate: semanticGate,
		documents: map[string]openDocument{
			uri: {Version: 1, Text: "fn main() {}"},
		},
		semanticDiagnostics: map[string]*semanticDiagnosticsEntry{
			uri: {seq: 3},
		},
	}

	s.scheduleDependentSemanticDiagnostics([]string{uri})

	entry := s.semanticDiagnostics[uri]
	if entry == nil {
		t.Fatal("expected dependent semantic entry to remain")
	}
	if entry.seq != 4 {
		t.Fatalf("expected dependent semantic seq to increment, got %d", entry.seq)
	}
	if entry.timer == nil {
		t.Fatal("expected dependent semantic diagnostics to be queued")
	}
	entry.timer.Stop()
}

func TestHoverMethodDeclarationShowsFullSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Point struct {\n    X: i32\n}\n\nfn Point::Calc(&self) -> i32 {\n    return self.X\n}\n"
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
	if !strings.Contains(hover.Contents.Value, "fn Point::Calc(&self) -> i32") {
		t.Fatalf("expected full method signature in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverFunctionDeclarationWithInferredDefaultParamDoesNotShowVoid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn something(v: i32, i = v) -> i32 {\n    return v + i\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "something")
	if !ok {
		t.Fatal("failed to find function position")
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
	if strings.Contains(hover.Contents.Value, "i: void") {
		t.Fatalf("did not expect fallback hover signature to show void, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "fn something(v: i32, i: i32 = v) -> i32") {
		t.Fatalf("expected inferred hover signature, got %q", hover.Contents.Value)
	}
}

func TestHoverNamedTypeShowsFieldsAndMethods(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Point struct {\n    X: i32\n    Y: i32 = 2\n}\n\nfn Point::Calc(&self) -> i32 {\n    return self.X\n}\n\nfn main() {\n    let p: Point = .{}\n    p.Calc()\n}\n"
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
	if !strings.Contains(hover.Contents.Value, "fn Point::Calc(&self) -> i32") {
		t.Fatalf("expected type methods in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverNamedTypeQuotesStringDefaultField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Profile struct {\n    name: str = \"Fuad Hasan\"\n}\n\nfn main() {\n    let p: Profile = .{}\n    p\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "Profile =")
	if !ok {
		t.Fatal("failed to find Profile usage position")
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
	if !strings.Contains(hover.Contents.Value, `name: str = "Fuad Hasan"`) {
		t.Fatalf("expected quoted string default in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverGenericNamedTypeShowsConcreteFieldsAndMethods(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Point<T> struct {\n    Value: T\n}\n\nfn Point<T>::Calc(&self) -> T {\n    return self.Value\n}\n\nfn Point<T>::Incr(&mut self, dx: T) -> void {\n    self.Value += dx\n}\n\nfn main() -> void {\n    let p: Point<i32> = .{ .Value = 1 }\n    p\n}\n"
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
	if !strings.Contains(hover.Contents.Value, "fn Point::Calc(&self) -> i32") {
		t.Fatalf("expected concrete generic method result type in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "fn Point::Incr(&mut self, dx: i32) -> void") {
		t.Fatalf("expected concrete generic method param type in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverFunctionCallShowsNamedSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn run2() -> void {\n}\n\nfn main() {\n    run2()\n}\n"
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
	if !strings.Contains(hover.Contents.Value, "fn run2() -> void") {
		t.Fatalf("expected named function signature in hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "fn() void") {
		t.Fatalf("expected no anonymous function signature in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverBuiltinCallsShowFunctionSignatures(t *testing.T) {
	restore := usePackagedLibsForTest(t)
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
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
	path := filepath.Join(dir, "main.fer")
	src := "fn add<T>(a: T, b: T) -> T {\n    return a + b\n}\n\nfn main() -> i32 {\n    let x = 1\n    let y = 2\n    return add(x, y)\n}\n"
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
	if !strings.Contains(hover.Contents.Value, "fn add(a: i32, b: i32) -> i32") {
		t.Fatalf("expected instantiated generic signature in hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "<T>") {
		t.Fatalf("expected call-site hover to hide generic declaration form, got %q", hover.Contents.Value)
	}
}

func TestHoverGenericStaticOwnerCallShowsInstantiatedSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Circle<T> struct {\n    Rad: T\n}\n\nfn Circle<T>::New(v: T) -> Self {\n    return .{ .Rad = v }\n}\n\nfn main() -> void {\n    let c = Circle<i32>::New(1)\n    c\n}\n"
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
	if !strings.Contains(hover.Contents.Value, "fn Circle::New(v: i32) -> Circle<i32>") {
		t.Fatalf("expected instantiated static owner signature in hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "fn Circle::New(v: T) -> Self") {
		t.Fatalf("expected no unresolved owner type parameter in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverGenericStaticOwnerCallHasSegmentRanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Circle<T> struct {\n    Rad: T\n}\n\nfn Circle<T>::New(v: T) -> Self {\n    return .{ .Rad = v }\n}\n\nfn main() -> void {\n    let c = Circle<i32>::New(1)\n    c\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    let c = Circle<i32>::New(1)")
	if !ok {
		t.Fatal("failed to find static owner call")
	}
	circleChar := char + len("    let c = ")
	newChar := char + len("    let c = Circle<i32>::")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	reqOwner := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: circleChar},
		}),
	}
	s.handleRequest(reqOwner)
	ownerHover := decodeHoverResult(t, out.String())
	if ownerHover == nil {
		t.Fatal("expected owner hover result")
	}
	if ownerHover.Range == nil {
		t.Fatal("expected owner hover range")
	}
	if ownerHover.Range.Start.Character != circleChar || ownerHover.Range.End.Character != circleChar+len("Circle") {
		t.Fatalf("expected Circle segment range, got %#v", ownerHover.Range)
	}
	if !strings.Contains(ownerHover.Contents.Value, "type Circle") {
		t.Fatalf("expected Circle type hover, got %q", ownerHover.Contents.Value)
	}

	out.Reset()
	reqMethod := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: newChar},
		}),
	}
	s.handleRequest(reqMethod)
	methodHover := decodeHoverResult(t, out.String())
	if methodHover == nil {
		t.Fatal("expected method hover result")
	}
	if methodHover.Range == nil {
		t.Fatal("expected method hover range")
	}
	if methodHover.Range.Start.Character != newChar || methodHover.Range.End.Character != newChar+len("New") {
		t.Fatalf("expected New segment range, got %#v", methodHover.Range)
	}
	if !strings.Contains(methodHover.Contents.Value, "fn Circle::New(v: i32) -> Circle<i32>") {
		t.Fatalf("expected instantiated static method hover, got %q", methodHover.Contents.Value)
	}
}

func TestHoverCrossModuleGenericCallShowsInstantiatedSignature(t *testing.T) {
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "util", "math.fer")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o755); err != nil {
		t.Fatalf("failed to create module dir: %v", err)
	}
	moduleSrc := "fn Pick<T>(v: T) -> T {\n    return v\n}\n"
	if err := os.WriteFile(modulePath, []byte(moduleSrc), 0o644); err != nil {
		t.Fatalf("failed to write module source: %v", err)
	}

	path := filepath.Join(dir, "main.fer")
	src := "import \"util/math\"\n\nfn main() -> i32 {\n    return math::Pick(1)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    return math::Pick(1)")
	if !ok {
		t.Fatal("failed to find imported generic call")
	}
	char += len("    return math::")

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
	if !strings.Contains(hover.Contents.Value, "fn Pick(v: i32) -> i32") {
		t.Fatalf("expected cross-module instantiated generic signature in hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "<T>") {
		t.Fatalf("expected no unresolved generic declaration form in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverCrossModuleConstrainedGenericCallShowsInstantiatedSignature(t *testing.T) {
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "util", "shape.fer")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o755); err != nil {
		t.Fatalf("failed to create module dir: %v", err)
	}
	moduleSrc := "type Shape interface {\n    Draw(&self)\n}\n\ntype Circle struct {}\n\nfn Circle::Draw(&self) -> void {\n}\n\nfn DrawOne<T: Shape>(s: T) -> void {\n    s.Draw()\n}\n"
	if err := os.WriteFile(modulePath, []byte(moduleSrc), 0o644); err != nil {
		t.Fatalf("failed to write module source: %v", err)
	}

	path := filepath.Join(dir, "main.fer")
	src := "import \"util/shape\"\n\nfn main() -> void {\n    let c: shape::Circle = .{}\n    shape::DrawOne(c)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    shape::DrawOne(c)")
	if !ok {
		t.Fatal("failed to find imported constrained generic call")
	}
	char += len("    shape::")

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
	if !strings.Contains(hover.Contents.Value, "fn DrawOne(s: Circle) -> void") {
		t.Fatalf("expected constrained cross-module call hover to show instantiated type, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "fn DrawOne(s: T) -> void") {
		t.Fatalf("expected no unresolved constrained type parameter in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverFunctionCallShowsDocComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "const sentinel = 0\n\n/// Adds two numbers.\n/// Returns the sum.\nfn add(a: i32, b: i32) -> i32 {\n    return a + b\n}\n\nfn main() -> i32 {\n    return add(1, 2)\n}\n"
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
	if !strings.Contains(hover.Contents.Value, "fn add(a: i32, b: i32) -> i32") {
		t.Fatalf("expected function signature in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Adds two numbers.") || !strings.Contains(hover.Contents.Value, "Returns the sum.") {
		t.Fatalf("expected function doc comment in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverFunctionCallShowsLineCommentDocBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "const sentinel = 0\n\n// Adds two numbers.\n// Returns the sum.\nfn add(a: i32, b: i32) -> i32 {\n    return a + b\n}\n\nfn main() -> i32 {\n    return add(1, 2)\n}\n"
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
	path := filepath.Join(dir, "main.fer")
	src := "const sentinel = 0\n\n// Point docs line 1.\n// Point docs line 2.\ntype Point struct {\n    Value: i32 = 0\n}\n\nfn main() -> void {\n    let p: Point = .{ .Value = 1 }\n    p\n}\n"
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
	path := filepath.Join(dir, "main.fer")
	src := "type Shape interface {\n    Draw(&self)\n}\n\nfn drawShape(s: Shape) -> void {\n    s.Draw()\n}\n"
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
	if !strings.Contains(hover.Contents.Value, "fn Shape::Draw(&self) -> void") {
		t.Fatalf("expected method hover to include self receiver, got %q", hover.Contents.Value)
	}
}

func TestHoverSelfShowsWrapperAndExpandedNamedType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Point struct {\n    Value: i32 = 7\n}\n\nfn Point::Calc(&self) -> i32 {\n    return self.Value\n}\n"
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
	if !strings.Contains(hover.Contents.Value, "fn Point::Calc(&self) -> i32") {
		t.Fatalf("expected method listing in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverBindingDeclarations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Point struct {\n    Value: i32 = 7\n}\n\nfn Point::Calc(&self, mut dx: i32, step: i32) -> i32 {\n    let mut a = dx\n    const b: i32 = step\n    return a + b + self.Value\n}\n"
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
	path := filepath.Join(dir, "main.fer")
	src := "fn run() -> void {\n    // local binding docs\n    // second line\n    let mut x = 1\n    x\n}\n"
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
	path := filepath.Join(dir, "main.fer")
	src := "type Conn struct {}\n\nfn run(mut c: *Conn) -> void {\n    c\n}\n"
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
	path := filepath.Join(dir, "main.fer")
	src := "type Point<T> struct {\n    Value: T\n}\n\nfn Point<T>::Calc(&self) -> T {\n    return self.Value\n}\n"
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

func TestHoverGenericFunctionDeclarationShowsTypeParams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn add<T>(a: T, b: T) -> T {\n    return a + b\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "add<T>")
	if !ok {
		t.Fatal("failed to find generic function name")
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
	if !strings.Contains(hover.Contents.Value, "fn add<T>(a: T, b: T) -> T") {
		t.Fatalf("expected generic declaration signature in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverLabels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn run() -> void {\nouter: while true {\n    break outer\n}\n}\n"
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
	path := filepath.Join(dir, "main.fer")
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

func TestHoverRecursiveGenericInterfaceDoesNotLoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Node<T> interface {\n    Next(&self) -> Node<T>\n}\n\nfn use(n: Node<i32>) -> void {\n    n\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "n: Node<i32>")
	if !ok {
		t.Fatal("failed to find recursive generic interface parameter")
	}
	char += len("n: ")

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
	if !strings.Contains(hover.Contents.Value, "Node<i32>") {
		t.Fatalf("expected instantiated recursive interface type in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Next(&self) -> Node<i32>") {
		t.Fatalf("expected instantiated recursive interface method in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverLargeMethodSetShowsTruncationNote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	var srcBuilder strings.Builder
	srcBuilder.WriteString("type Big struct {}\n\n")
	totalMethods := maxHoverMethodsPerKind + 5
	for i := 0; i < totalMethods; i++ {
		srcBuilder.WriteString("fn Big::M")
		srcBuilder.WriteString(strconv.Itoa(i))
		srcBuilder.WriteString("(&self) void {\n}\n\n")
	}
	srcBuilder.WriteString("fn main() -> void {\n    let x: Big = .{}\n    x\n}\n")
	src := srcBuilder.String()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "Big = .")
	if !ok {
		t.Fatal("failed to find Big type usage")
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
	if !strings.Contains(hover.Contents.Value, "Hover truncated: omitted") {
		t.Fatalf("expected truncation note in hover, got %q", hover.Contents.Value)
	}
	if got := strings.Count(hover.Contents.Value, "fn Big::M"); got >= totalMethods {
		t.Fatalf("expected method list truncation in hover, got %d rendered methods", got)
	}
}

func TestHoverConstrainedGenericParameterShowsConstraint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Shape interface {\n    Draw(&self)\n}\n\nfn drawShape<T: Shape>(s: T) {\n    s.Draw()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "(s: T)")
	if !ok {
		t.Fatal("failed to find constrained parameter")
	}
	char++

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
	if !strings.Contains(hover.Contents.Value, "(parameter) s: T") {
		t.Fatalf("expected constrained parameter hover header, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "T: Shape") {
		t.Fatalf("expected constrained parameter hover, got %q", hover.Contents.Value)
	}
}

func TestHoverConstrainedTypeParamNameShowsConstraint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Shape interface {\n    Draw(&self)\n}\n\nfn drawShape<T: Shape>(s: T) {\n    s.Draw()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "(s: T)")
	if !ok {
		t.Fatal("failed to find constrained parameter type")
	}
	char += len("(s: ")

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
	if !strings.Contains(hover.Contents.Value, "T: Shape") {
		t.Fatalf("expected constrained type parameter hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "(parameter)") {
		t.Fatalf("did not expect parameter header while hovering on type parameter, got %q", hover.Contents.Value)
	}
}

func TestCompletionReturnsVisibleSymbols(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn helper() -> void {}\n\nfn main() {\n    let value = 1\n    val\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    val")
	if !ok {
		t.Fatal("failed to find completion position")
	}
	char += len("    val")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/completion",
		Params: mustRawJSON(t, completionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	items := decodeCompletionResult(t, out.String())
	if len(items) == 0 {
		t.Fatal("expected completion results")
	}
	foundValue := false
	for _, item := range items {
		if item.Label == "value" {
			foundValue = true
			break
		}
	}
	if !foundValue {
		t.Fatalf("expected local variable completion, got %#v", items)
	}
}

func TestCompletionIncludesPreludeGlobalsWithoutImport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "fn main() {\n    pri\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    pri")
	if !ok {
		t.Fatal("failed to find completion position")
	}
	char += len("    pri")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/completion",
		Params: mustRawJSON(t, completionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	items := decodeCompletionResult(t, out.String())
	foundPrint := false
	foundPrintln := false
	for _, item := range items {
		if item.Label == "print" {
			foundPrint = true
		}
		if item.Label == "println" {
			foundPrintln = true
		}
	}
	if !foundPrint || !foundPrintln {
		t.Fatalf("expected prelude completions for print/println, got %#v", items)
	}
}

func TestCompletionMemberAndStaticMember(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := "type Point struct {\n    Value: i32\n}\n\nfn Point::Draw(&self) -> void {\n}\n\n" +
		"type Circle<T> struct {\n    Rad: T\n}\n\nfn Circle<T>::New(v: T) -> Self {\n    return .{ .Rad = v }\n}\n\n" +
		"fn main() -> void {\n    let p: Point = .{ .Value = 1 }\n    p.Draw()\n    Circle<i32>::New(1)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	memberLine, memberChar, ok := findPosition(src, "    p.Draw()")
	if !ok {
		t.Fatal("failed to find member completion position")
	}
	memberChar += len("    p.")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}
	memberReq := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/completion",
		Params: mustRawJSON(t, completionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: memberLine, Character: memberChar},
		}),
	}
	s.handleRequest(memberReq)

	memberItems := decodeCompletionResult(t, out.String())
	foundDraw := false
	foundKeywordIf := false
	for _, item := range memberItems {
		if item.Label == "Draw" {
			foundDraw = true
		}
		if item.Label == "if" {
			foundKeywordIf = true
		}
	}
	if !foundDraw {
		t.Fatalf("expected instance method completion, got %#v", memberItems)
	}
	if foundKeywordIf {
		t.Fatalf("did not expect keyword completion in member context, got %#v", memberItems)
	}

	out.Reset()
	staticLine, staticChar, ok := findPosition(src, "    Circle<i32>::New(1)")
	if !ok {
		t.Fatal("failed to find static completion position")
	}
	staticChar += len("    Circle<i32>::")
	staticReq := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "textDocument/completion",
		Params: mustRawJSON(t, completionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: staticLine, Character: staticChar},
		}),
	}
	s.handleRequest(staticReq)

	staticItems := decodeCompletionResult(t, out.String())
	foundNew := false
	foundStaticKeywordIf := false
	for _, item := range staticItems {
		if item.Label == "New" {
			foundNew = true
		}
		if item.Label == "if" {
			foundStaticKeywordIf = true
		}
	}
	if !foundNew {
		t.Fatalf("expected static method completion, got %#v", staticItems)
	}
	if foundStaticKeywordIf {
		t.Fatalf("did not expect keyword completion in static member context, got %#v", staticItems)
	}
}

func TestHoverImportAliasAndPathShowsModuleDoc(t *testing.T) {
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "util", "os.fer")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o755); err != nil {
		t.Fatalf("failed to create module dir: %v", err)
	}
	moduleSrc := "// OS helpers.\n// Module level docs.\nfn CpuCount() -> i32 {\n    return 1\n}\n"
	if err := os.WriteFile(modulePath, []byte(moduleSrc), 0o644); err != nil {
		t.Fatalf("failed to write module source: %v", err)
	}

	path := filepath.Join(dir, "main.fer")
	src := "import \"util/os\" as os\n\nfn main() -> void {\n    os::CpuCount()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	pathLine, pathChar, ok := findPosition(src, "\"util/os\"")
	if !ok {
		t.Fatal("failed to find import path position")
	}
	pathChar++ // inside quotes
	aliasLine, aliasChar, ok := findPosition(src, "as os")
	if !ok {
		t.Fatal("failed to find import alias position")
	}
	aliasChar += len("as ")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}

	pathReq := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: pathLine, Character: pathChar},
		}),
	}
	s.handleRequest(pathReq)
	pathHover := decodeHoverResult(t, out.String())
	if pathHover == nil {
		t.Fatal("expected hover result for import path")
	}
	if !strings.Contains(pathHover.Contents.Value, "import \"util/os\"") {
		t.Fatalf("expected import header in hover, got %q", pathHover.Contents.Value)
	}
	if !strings.Contains(pathHover.Contents.Value, "OS helpers.") || !strings.Contains(pathHover.Contents.Value, "Module level docs.") {
		t.Fatalf("expected module doc in import path hover, got %q", pathHover.Contents.Value)
	}

	out.Reset()
	aliasReq := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: aliasLine, Character: aliasChar},
		}),
	}
	s.handleRequest(aliasReq)
	aliasHover := decodeHoverResult(t, out.String())
	if aliasHover == nil {
		t.Fatal("expected hover result for import alias")
	}
	if !strings.Contains(aliasHover.Contents.Value, "OS helpers.") || !strings.Contains(aliasHover.Contents.Value, "Module level docs.") {
		t.Fatalf("expected module doc in import alias hover, got %q", aliasHover.Contents.Value)
	}
}

func TestHoverImportPathUsesModuleDocOverFirstDeclDoc(t *testing.T) {
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "util", "testing.fer")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o755); err != nil {
		t.Fatalf("failed to create module dir: %v", err)
	}
	moduleSrc := "// Module docs line 1.\n// Module docs line 2.\n\n// Function docs should not replace module docs.\nfn ExpectEq() -> void {\n}\n"
	if err := os.WriteFile(modulePath, []byte(moduleSrc), 0o644); err != nil {
		t.Fatalf("failed to write module source: %v", err)
	}

	path := filepath.Join(dir, "main.fer")
	src := "import \"util/testing\"\n\nfn main() -> void {\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	pathLine, pathChar, ok := findPosition(src, "\"util/testing\"")
	if !ok {
		t.Fatal("failed to find import path position")
	}
	pathChar++

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/hover",
		Params: mustRawJSON(t, hoverParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: pathLine, Character: pathChar},
		}),
	}
	s.handleRequest(req)

	hover := decodeHoverResult(t, out.String())
	if hover == nil {
		t.Fatal("expected hover result")
	}
	if !strings.Contains(hover.Contents.Value, "Module docs line 1.") || !strings.Contains(hover.Contents.Value, "Module docs line 2.") {
		t.Fatalf("expected module docs in hover, got %q", hover.Contents.Value)
	}
	if strings.Contains(hover.Contents.Value, "Function docs should not replace module docs.") {
		t.Fatalf("expected function docs to be excluded from module hover, got %q", hover.Contents.Value)
	}
}

func TestHoverCastRawOwnerBoundaryShowsAdoptExposeGuidance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := `
fn main() -> void {
    unsafe {
        let raw = 0 as ^i32
        let own = raw as *i32
        own
    }
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}
	line, char, ok := findPosition(src, "as *i32")
	if !ok {
		t.Fatal("failed to find cast position")
	}
	char += len("as ")

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
	if !strings.Contains(hover.Contents.Value, "std/mem::Adopt") || !strings.Contains(hover.Contents.Value, "std/mem::Expose") {
		t.Fatalf("expected ownership-boundary cast guidance in hover, got %q", hover.Contents.Value)
	}
}

func TestHoverAsKeywordInCastShowsKeywordDocumentation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	src := `
fn main() -> void {
    unsafe {
        let raw = 0 as ^i32
        let own = raw as *i32
        own
    }
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}
	line, char, ok := findPosition(src, "as *i32")
	if !ok {
		t.Fatal("failed to find cast position")
	}
	char += 1

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
	if !strings.Contains(hover.Contents.Value, "```ferret\nas\n```") {
		t.Fatalf("expected keyword code block in hover, got %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Cast an expression to a target type.") {
		t.Fatalf("expected cast keyword documentation in hover, got %q", hover.Contents.Value)
	}
}

func TestCompletionModuleStaticMembersViaImportAlias(t *testing.T) {
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "util", "os.fer")
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o755); err != nil {
		t.Fatalf("failed to create module dir: %v", err)
	}
	moduleSrc := "fn CpuCount() -> i32 {\n    return 1\n}\n\nconst Version: i32 = 1\n"
	if err := os.WriteFile(modulePath, []byte(moduleSrc), 0o644); err != nil {
		t.Fatalf("failed to write module source: %v", err)
	}

	path := filepath.Join(dir, "main.fer")
	src := "import \"util/os\"\n\nfn main() -> void {\n    os::CpuCount()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	line, char, ok := findPosition(src, "    os::CpuCount()")
	if !ok {
		t.Fatal("failed to find completion position")
	}
	char += len("    os::")

	var out bytes.Buffer
	uri := "file://" + filepath.ToSlash(path)
	s := &Server{out: &out, documents: make(map[string]openDocument), hoverCache: make(map[string]hoverCacheEntry)}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/completion",
		Params: mustRawJSON(t, completionParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Position:     lspPosition{Line: line, Character: char},
		}),
	}
	s.handleRequest(req)

	items := decodeCompletionResult(t, out.String())
	foundCPU := false
	foundKeywordIf := false
	for _, item := range items {
		if item.Label == "CpuCount" {
			foundCPU = true
		}
		if item.Label == "if" {
			foundKeywordIf = true
		}
	}
	if !foundCPU {
		t.Fatalf("expected module member completion for os::, got %#v", items)
	}
	if foundKeywordIf {
		t.Fatalf("did not expect keyword completion in module member context, got %#v", items)
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

func fakeHoverResultWithDependency(path string, typeName string, depPath string) compiler.Result {
	result := fakeHoverResult(path, typeName)
	result.Modules = append(result.Modules, &context.Module{FilePath: depPath})
	return result
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

func decodeDefinitionResult(t *testing.T, framed string) *lspLocation {
	t.Helper()
	resp := decodeSingleResponse(t, framed)
	if resp.Error != nil {
		t.Fatalf("unexpected definition error: %#v", resp.Error)
	}
	if resp.Result == nil {
		return nil
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal definition result: %v", err)
	}
	var loc lspLocation
	if err := json.Unmarshal(raw, &loc); err != nil {
		t.Fatalf("failed to unmarshal definition result: %v", err)
	}
	return &loc
}

func decodeCompletionResult(t *testing.T, framed string) []completionItem {
	t.Helper()
	resp := decodeSingleResponse(t, framed)
	if resp.Error != nil {
		t.Fatalf("unexpected completion error: %#v", resp.Error)
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal completion result: %v", err)
	}
	var items []completionItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("failed to unmarshal completion result: %v", err)
	}
	return items
}

func decodeFramedBodies(t *testing.T, framed string) [][]byte {
	t.Helper()
	data := []byte(framed)
	out := make([][]byte, 0)
	for len(data) > 0 {
		parts := bytes.SplitN(data, []byte("\r\n\r\n"), 2)
		if len(parts) != 2 {
			t.Fatalf("expected framed LSP message, got %q", string(data))
		}
		header := string(parts[0])
		bodyAndRest := parts[1]
		const prefix = "Content-Length: "
		if !strings.HasPrefix(header, prefix) {
			t.Fatalf("missing Content-Length header in %q", header)
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, prefix)))
		if err != nil {
			t.Fatalf("invalid Content-Length %q: %v", header, err)
		}
		if len(bodyAndRest) < n {
			t.Fatalf("framed body shorter than Content-Length: %d < %d", len(bodyAndRest), n)
		}
		out = append(out, append([]byte(nil), bodyAndRest[:n]...))
		data = bodyAndRest[n:]
	}
	return out
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

func usePackagedLibsForTest(t *testing.T) func() {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	srcRoot := filepath.Join(repoRoot, context.STD_LIB_DEV)

	bundleRoot := filepath.Join(t.TempDir(), "bundle")
	libsRoot := filepath.Join(bundleRoot, "libs")
	if err := copyDir(srcRoot, libsRoot); err != nil {
		t.Fatalf("copy packaged libs: %v", err)
	}

	execPath := filepath.Join(bundleRoot, "bin", "ferret")
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("mkdir bundle bin: %v", err)
	}

	oldProjectExecutablePath := projectpkg.ExecutablePath
	oldPreludeExecutablePath := prelude.ExecutablePath
	projectpkg.ExecutablePath = func() (string, error) { return execPath, nil }
	prelude.ExecutablePath = func() (string, error) { return execPath, nil }
	return func() {
		projectpkg.ExecutablePath = oldProjectExecutablePath
		prelude.ExecutablePath = oldPreludeExecutablePath
	}
}

func copyDir(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)
		if entry.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, 0o644)
	})
}
