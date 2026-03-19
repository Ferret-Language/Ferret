package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
	"compiler/internal/tokens"
)

const (
	textDocumentSyncFull = 1
)

var (
	lexSource = func(path, text string, diag *diagnostics.Bag) []tokens.Token {
		return lexer.New(path, text, diag).Tokenize()
	}
	parseSource = func(path string, toks []tokens.Token, diag *diagnostics.Bag) {
		_ = parser.Parse(path, toks, diag)
	}
	parseProject = func(path string) compiler.Result {
		return compiler.ParsePath(path)
	}
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	RootURI string `json:"rootUri"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   versionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []textDocumentContentChangeEvent `json:"contentChanges"`
}

type didSaveParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type hoverParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type textDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

type publishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
	Version     *int            `json:"version,omitempty"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity,omitempty"`
	Code     string   `json:"code,omitempty"`
	Source   string   `json:"source,omitempty"`
	Message  string   `json:"message"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type hoverResult struct {
	Contents markupContent `json:"contents"`
	Range    *lspRange     `json:"range,omitempty"`
}

type openDocument struct {
	Version int
	Text    string
}

type hoverCacheEntry struct {
	Mode      string
	Version   int
	FileStamp string
	Index     *hoverIndex
}

type Server struct {
	in  *bufio.Reader
	out io.Writer

	mu             sync.Mutex
	rootURI        string
	isShuttingDown bool
	shouldExit     bool
	documents      map[string]openDocument
	hoverCache     map[string]hoverCacheEntry
}

func Run(stdin io.Reader, stdout io.Writer) error {
	s := &Server{
		in:         bufio.NewReader(stdin),
		out:        stdout,
		documents:  make(map[string]openDocument),
		hoverCache: make(map[string]hoverCacheEntry),
	}
	return s.serve()
}

func (s *Server) serve() error {
	for {
		payload, err := s.readMessage()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req rpcRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			continue
		}

		if req.Method == "" {
			continue
		}

		s.handleRequest(req)

		s.mu.Lock()
		exit := s.shouldExit
		s.mu.Unlock()
		if exit {
			return nil
		}
	}
}

func (s *Server) handleRequest(req rpcRequest) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if len(req.ID) > 0 {
				s.writeError(req.ID, -32603, "internal server error")
			}
		}
	}()

	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "initialized":
		// no-op
	case "shutdown":
		s.mu.Lock()
		s.isShuttingDown = true
		s.mu.Unlock()
		s.writeResponse(req.ID, map[string]any{})
	case "exit":
		s.mu.Lock()
		s.shouldExit = true
		s.mu.Unlock()
		return
	case "textDocument/didOpen":
		s.handleDidOpen(req.Params)
	case "textDocument/didChange":
		s.handleDidChange(req.Params)
	case "textDocument/didSave":
		s.handleDidSave(req.Params)
	case "textDocument/didClose":
		s.handleDidClose(req.Params)
	case "textDocument/hover":
		s.handleHover(req)
	default:
		if len(req.ID) > 0 {
			s.writeError(req.ID, -32601, "method not found")
		}
	}
}

func (s *Server) handleInitialize(req rpcRequest) {
	params := initializeParams{}
	_ = json.Unmarshal(req.Params, &params)

	s.mu.Lock()
	s.rootURI = params.RootURI
	s.mu.Unlock()

	result := map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    textDocumentSyncFull,
				"save": map[string]any{
					"includeText": true,
				},
			},
			"hoverProvider": true,
		},
		"serverInfo": map[string]any{
			"name":    "ferret-lsp",
			"version": "0.1.0",
		},
	}
	s.writeResponse(req.ID, result)
}

func (s *Server) handleDidOpen(raw json.RawMessage) {
	params := didOpenParams{}
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	uri := params.TextDocument.URI
	s.mu.Lock()
	if s.documents == nil {
		s.documents = make(map[string]openDocument)
	}
	s.documents[uri] = openDocument{Version: params.TextDocument.Version, Text: params.TextDocument.Text}
	if s.hoverCache != nil {
		delete(s.hoverCache, uri)
	}
	s.mu.Unlock()

	path, err := uriToPath(uri)
	if err != nil || !isFerretSourcePath(path) {
		s.publishDiagnostics(uri, nil, nil)
		return
	}

	s.publishSyntaxDiagnostics(uri, params.TextDocument.Version, params.TextDocument.Text)
}

func (s *Server) handleDidChange(raw json.RawMessage) {
	params := didChangeParams{}
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	if len(params.ContentChanges) == 0 {
		return
	}
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	uri := params.TextDocument.URI

	s.mu.Lock()
	if s.documents == nil {
		s.documents = make(map[string]openDocument)
	}
	s.documents[uri] = openDocument{Version: params.TextDocument.Version, Text: text}
	if s.hoverCache != nil {
		delete(s.hoverCache, uri)
	}
	s.mu.Unlock()

	path, err := uriToPath(uri)
	if err != nil || !isFerretSourcePath(path) {
		s.publishDiagnostics(uri, nil, nil)
		return
	}

	s.publishSyntaxDiagnostics(uri, params.TextDocument.Version, text)
}

func (s *Server) handleDidSave(raw json.RawMessage) {
	params := didSaveParams{}
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}

	uri := params.TextDocument.URI
	path, err := uriToPath(uri)
	if err != nil {
		return
	}
	if !isFerretSourcePath(path) {
		s.publishDiagnostics(uri, nil, nil)
		return
	}

	result := compiler.ParsePath(path)
	diagnostics := convertDiagnostics(result.Diagnostics.Diagnostics(), path)
	s.publishDiagnostics(uri, nil, diagnostics)
}

func (s *Server) handleDidClose(raw json.RawMessage) {
	params := didCloseParams{}
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}

	s.mu.Lock()
	delete(s.documents, params.TextDocument.URI)
	if s.hoverCache != nil {
		delete(s.hoverCache, params.TextDocument.URI)
	}
	s.mu.Unlock()

	s.publishDiagnostics(params.TextDocument.URI, nil, nil)
}

func (s *Server) handleHover(req rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	params := hoverParams{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeResponse(req.ID, nil)
		return
	}
	path, err := uriToPath(params.TextDocument.URI)
	if err != nil || !isFerretSourcePath(path) {
		s.writeResponse(req.ID, nil)
		return
	}
	pos := source.Position{
		Line:   params.Position.Line + 1,
		Column: params.Position.Character + 1,
	}
	doc, hasDoc := s.documentState(params.TextDocument.URI)
	index := s.getOrBuildHoverIndex(params.TextDocument.URI, path, doc, hasDoc)
	hover := hoverFromIndex(index, pos)
	s.writeResponse(req.ID, hover)
}

func (s *Server) documentState(uri string) (openDocument, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.documents[uri]
	if !ok {
		return openDocument{}, false
	}
	return doc, true
}

func (s *Server) publishSyntaxDiagnostics(uri string, version int, text string) {
	path, err := uriToPath(uri)
	if err != nil {
		return
	}
	diagBag := diagnostics.NewDiagnosticBag(path)
	diagBag.AddSourceContent(path, text)
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				msg := fmt.Sprintf("language server internal error while parsing: %v", recovered)
				origin := source.NewPosition()
				loc := source.NewLocation(path, origin, origin)
				diagBag.Add(
					diagnostics.NewError(msg).
						WithCode(diagnostics.ErrUnexpectedToken).
						WithPrimaryLabel(&loc, "parser recovered; keep editing"),
				)
			}
		}()

		toks := lexSource(path, text, diagBag)
		parseSource(path, toks, diagBag)
	}()

	diagnostics := convertDiagnostics(diagBag.Diagnostics(), path)
	v := version
	s.publishDiagnostics(uri, &v, diagnostics)
}

func (s *Server) publishDiagnostics(uri string, version *int, diagnostics []lspDiagnostic) {
	s.writeNotification("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         uri,
		Version:     version,
		Diagnostics: diagnostics,
	})
}

func convertDiagnostics(diags []*diagnostics.Diagnostic, targetPath string) []lspDiagnostic {
	out := make([]lspDiagnostic, 0, len(diags))
	target := filepath.Clean(targetPath)
	for _, diag := range diags {
		if diag == nil {
			continue
		}
		if file := diagnosticFilePath(diag); file != "" && filepath.Clean(file) != target {
			continue
		}
		rng := lspRange{Start: lspPosition{Line: 0, Character: 0}, End: lspPosition{Line: 0, Character: 1}}
		if label := primaryLabel(diag); label != nil && label.Location != nil && label.Location.Start != nil {
			startLine := max(label.Location.Start.Line-1, 0)
			startCol := max(label.Location.Start.Column-1, 0)
			endLine := startLine
			endCol := startCol
			if label.Location.End != nil {
				endLine = max(label.Location.End.Line-1, 0)
				endCol = max(label.Location.End.Column-1, 0)
				if endLine < startLine || (endLine == startLine && endCol < startCol) {
					endLine = startLine
					endCol = startCol
				}
			}
			rng = lspRange{
				Start: lspPosition{Line: startLine, Character: startCol},
				End:   lspPosition{Line: endLine, Character: endCol},
			}
		}

		msg := diag.Message
		if msg == "" {
			msg = "ferret diagnostic"
		}
		out = append(out, lspDiagnostic{
			Range:    rng,
			Severity: toLSPSeverity(diag.Severity),
			Code:     diag.Code,
			Source:   "ferret",
			Message:  msg,
		})
	}
	return out
}

func primaryLabel(diag *diagnostics.Diagnostic) *diagnostics.Label {
	for i := range diag.Labels {
		if diag.Labels[i].Style == diagnostics.Primary {
			return &diag.Labels[i]
		}
	}
	if len(diag.Labels) > 0 {
		return &diag.Labels[0]
	}
	return nil
}

func diagnosticFilePath(diag *diagnostics.Diagnostic) string {
	if diag == nil {
		return ""
	}
	if diag.FilePath != "" {
		return diag.FilePath
	}
	if label := primaryLabel(diag); label != nil && label.Location != nil && label.Location.Filename != nil {
		return *label.Location.Filename
	}
	return ""
}

func toLSPSeverity(sev diagnostics.Severity) int {
	switch sev {
	case diagnostics.Error:
		return 1
	case diagnostics.Warning:
		return 2
	case diagnostics.Info:
		return 3
	case diagnostics.Hint:
		return 4
	default:
		return 1
	}
}

func isFerretSourcePath(path string) bool {
	if path == "" {
		return false
	}
	return strings.EqualFold(filepath.Ext(path), compiler.FerretSourceExt)
}

type hoverCandidate struct {
	markdown string
	location source.Location
	span     int
	priority int
}

type hoverIndex struct {
	candidates []hoverCandidate
}

func (s *Server) getOrBuildHoverIndex(uri, path string, doc openDocument, hasDoc bool) *hoverIndex {
	if hasDoc {
		s.mu.Lock()
		if s.hoverCache == nil {
			s.hoverCache = make(map[string]hoverCacheEntry)
		}
		if entry, ok := s.hoverCache[uri]; ok && entry.Mode == "doc" && entry.Version == doc.Version && entry.Index != nil {
			index := entry.Index
			s.mu.Unlock()
			return index
		}
		s.mu.Unlock()

		index := buildHoverIndex(path, doc.Text, true)

		s.mu.Lock()
		if s.hoverCache == nil {
			s.hoverCache = make(map[string]hoverCacheEntry)
		}
		s.hoverCache[uri] = hoverCacheEntry{
			Mode:    "doc",
			Version: doc.Version,
			Index:   index,
		}
		s.mu.Unlock()
		return index
	}

	stamp := fileStamp(path)

	s.mu.Lock()
	if s.hoverCache == nil {
		s.hoverCache = make(map[string]hoverCacheEntry)
	}
	if entry, ok := s.hoverCache[uri]; ok && entry.Mode == "file" && entry.FileStamp == stamp && entry.Index != nil {
		index := entry.Index
		s.mu.Unlock()
		return index
	}
	s.mu.Unlock()

	index := buildHoverIndex(path, "", false)

	s.mu.Lock()
	if s.hoverCache == nil {
		s.hoverCache = make(map[string]hoverCacheEntry)
	}
	s.hoverCache[uri] = hoverCacheEntry{
		Mode:      "file",
		FileStamp: stamp,
		Index:     index,
	}
	s.mu.Unlock()
	return index
}

func fileStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func hoverFromIndex(index *hoverIndex, pos source.Position) *hoverResult {
	if index == nil {
		return nil
	}
	candidate, ok := findBestHoverCandidate(index, pos)
	if !ok {
		return nil
	}
	rng := locationToLSPRange(candidate.location)
	return &hoverResult{
		Contents: markupContent{
			Kind:  "markdown",
			Value: candidate.markdown,
		},
		Range: &rng,
	}
}

func buildHoverIndex(path, text string, hasText bool) *hoverIndex {
	result, parsedPath, cleanup := parseForHover(path, text, hasText)
	defer cleanup()

	mod := findModuleByPath(result, parsedPath)
	if mod == nil || mod.Types == nil {
		return &hoverIndex{}
	}
	modules := indexModulesByKey(result)
	return &hoverIndex{candidates: collectHoverCandidates(mod, mod.Types, modules)}
}

func indexModulesByKey(result compiler.Result) map[string]*context.Module {
	out := make(map[string]*context.Module)
	if result.Entry != nil && result.Entry.Key != "" {
		out[result.Entry.Key] = result.Entry
	}
	for _, mod := range result.Modules {
		if mod == nil || mod.Key == "" {
			continue
		}
		out[mod.Key] = mod
	}
	return out
}

func parseForHover(path, text string, hasText bool) (compiler.Result, string, func()) {
	if !hasText {
		return parseProject(path), path, func() {}
	}
	tempPath, err := writeHoverOverlay(path, text)
	if err != nil {
		return parseProject(path), path, func() {}
	}
	cleanup := func() { _ = os.Remove(tempPath) }
	return parseProject(tempPath), tempPath, cleanup
}

func writeHoverOverlay(originalPath, text string) (string, error) {
	dir := filepath.Dir(originalPath)
	file, err := os.CreateTemp(dir, ".ferret-lsp-hover-*.ferr")
	if err != nil {
		return "", err
	}
	tempPath := file.Name()
	if _, err := file.WriteString(text); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	return tempPath, nil
}

func findModuleByPath(result compiler.Result, parsedPath string) *context.Module {
	absTarget, err := filepath.Abs(parsedPath)
	if err != nil {
		absTarget = parsedPath
	}
	absTarget = filepath.Clean(absTarget)

	if result.Entry != nil && sameFilePath(result.Entry.FilePath, absTarget) {
		return result.Entry
	}
	for _, mod := range result.Modules {
		if mod != nil && sameFilePath(mod.FilePath, absTarget) {
			return mod
		}
	}
	return result.Entry
}

func sameFilePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, err := filepath.Abs(a)
	if err != nil {
		absA = a
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		absB = b
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

func collectHoverCandidates(mod *context.Module, info *typeinfo.ModuleInfo, modulesByKey map[string]*context.Module) []hoverCandidate {
	if info == nil {
		return nil
	}
	out := make([]hoverCandidate, 0, len(info.Nodes)+len(info.Symbols))
	collect := func(markdown string, loc source.Location, priority int) {
		if markdown == "" || loc.Start == nil {
			return
		}
		out = append(out, hoverCandidate{
			markdown: markdown,
			location: loc,
			span:     locationSpan(loc),
			priority: priority,
		})
	}
	for node, typ := range info.Nodes {
		if node == nil {
			continue
		}
		collect(renderNodeHoverMarkdown(node, typ, mod, info, modulesByKey), node.Loc(), 1)
	}
	for sym, typ := range info.Symbols {
		if sym == nil {
			continue
		}
		collect(renderSymbolHoverMarkdown(sym, typ, mod, modulesByKey), sym.Location, 0)
	}
	return out
}

func renderNodeHoverMarkdown(node ast.Node, typ typeinfo.Type, mod *context.Module, info *typeinfo.ModuleInfo, modulesByKey map[string]*context.Module) string {
	if typ == nil {
		return ""
	}
	if sig := renderNodeFunctionSignature(node, typ, mod, info); sig != "" {
		return asFerretCodeBlock(sig)
	}
	if selector, ok := node.(*ast.SelectorExpr); ok {
		if fnType, ok := typ.(*typeinfo.FuncType); ok {
			receiver := ""
			if info != nil && selector.Left != nil {
				if leftType, ok := info.Nodes[selector.Left]; ok {
					receiver = typeinfo.FormatType(leftType)
				}
			}
			name := selector.Name.Text()
			if receiver != "" {
				name = receiver + "::" + name
			}
			return asFerretCodeBlock(typeinfo.FormatFuncSignature(name, fnType))
		}
	}
	return renderTypeHoverMarkdown(typ, mod, modulesByKey)
}

func renderNodeFunctionSignature(node ast.Node, typ typeinfo.Type, mod *context.Module, info *typeinfo.ModuleInfo) string {
	fnType, ok := typ.(*typeinfo.FuncType)
	if !ok || node == nil {
		return ""
	}
	if sym := resolvedSymbolForNode(mod, node); sym != nil {
		if sig := symbolFunctionSignature(sym, fnType); sig != "" {
			return sig
		}
		if sym.Name != "" {
			return typeinfo.FormatFuncSignature(sym.Name, fnType)
		}
	}
	switch n := node.(type) {
	case *ast.Ident:
		name := n.Text()
		if name != "" {
			return typeinfo.FormatFuncSignature(name, fnType)
		}
	case *ast.SelectorExpr:
		receiver := ""
		if info != nil && n.Left != nil {
			if leftType, ok := info.Nodes[n.Left]; ok {
				receiver = typeinfo.FormatType(leftType)
			}
		}
		name := n.Name.Text()
		if receiver != "" {
			name = receiver + "::" + name
		}
		if name != "" {
			return typeinfo.FormatFuncSignature(name, fnType)
		}
	}
	return ""
}

func resolvedSymbolForNode(mod *context.Module, node ast.Node) *symbols.Symbol {
	if mod == nil || mod.Bindings == nil || node == nil {
		return nil
	}
	res := mod.Bindings.Nodes[node]
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return nil
	}
	return res.Symbol
}

func symbolFunctionSignature(sym *symbols.Symbol, fnType *typeinfo.FuncType) string {
	if sym == nil {
		return ""
	}
	if fn, ok := sym.Node.(*ast.FuncDecl); ok && fn != nil {
		return fn.Signature()
	}
	if fnType != nil && sym.Name != "" {
		return typeinfo.FormatFuncSignature(sym.Name, fnType)
	}
	return ""
}

func renderSymbolHoverMarkdown(sym *symbols.Symbol, typ typeinfo.Type, mod *context.Module, modulesByKey map[string]*context.Module) string {
	if sym == nil {
		return renderTypeHoverMarkdown(typ, mod, modulesByKey)
	}
	switch sym.Kind {
	case symbols.SymbolFunc, symbols.SymbolMethod:
		if sig := symbolFunctionSignature(sym, nil); sig != "" {
			return asFerretCodeBlock(sig)
		}
		if fnType, ok := typ.(*typeinfo.FuncType); ok {
			return asFerretCodeBlock(typeinfo.FormatFuncSignature(sym.Name, fnType))
		}
	case symbols.SymbolType:
		if named, ok := typ.(*typeinfo.NamedType); ok {
			return renderNamedTypeMarkdown(named, mod, modulesByKey)
		}
		if decl, ok := sym.Node.(*ast.TypeDecl); ok && decl != nil {
			named := &typeinfo.NamedType{Name: sym.Name, Decl: decl}
			if mod != nil {
				named.ModuleKey = mod.Key
			}
			return renderNamedTypeMarkdown(named, mod, modulesByKey)
		}
	}
	return renderTypeHoverMarkdown(typ, mod, modulesByKey)
}

func renderTypeHoverMarkdown(typ typeinfo.Type, mod *context.Module, modulesByKey map[string]*context.Module) string {
	if typ == nil {
		return ""
	}
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return renderNamedTypeMarkdown(t, mod, modulesByKey)
	default:
		rendered := typeinfo.FormatType(typ)
		switch rendered {
		case "", "<unknown>", "<invalid>":
			rendered = ""
		}
		if named := underlyingNamedType(typ); named != nil {
			details := renderNamedTypeMarkdown(named, mod, modulesByKey)
			if rendered == "" {
				return details
			}
			if details == "" {
				return asFerretCodeBlock(rendered)
			}
			return asFerretCodeBlock(rendered) + "\n\n" + details
		}
		if rendered == "" {
			return ""
		}
		return asFerretCodeBlock(rendered)
	}
}

func underlyingNamedType(typ typeinfo.Type) *typeinfo.NamedType {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return t
	case *typeinfo.RefType:
		return underlyingNamedType(t.Inner)
	case *typeinfo.PointerType:
		return underlyingNamedType(t.Inner)
	case *typeinfo.RawPtrType:
		return underlyingNamedType(t.Inner)
	case *typeinfo.OptionalType:
		return underlyingNamedType(t.Inner)
	case *typeinfo.ArrayType:
		return underlyingNamedType(t.Inner)
	case *typeinfo.SliceType:
		return underlyingNamedType(t.Inner)
	case *typeinfo.ErrorUnionType:
		if named := underlyingNamedType(t.Value); named != nil {
			return named
		}
		return underlyingNamedType(t.Error)
	default:
		return nil
	}
}

func renderNamedTypeMarkdown(named *typeinfo.NamedType, mod *context.Module, modulesByKey map[string]*context.Module) string {
	if named == nil {
		return ""
	}
	owner := moduleForNamedType(named, mod, modulesByKey)
	decl := named.Decl
	if decl == nil {
		decl = findTypeDecl(owner, named.Name)
	}
	if decl == nil {
		return asFerretCodeBlock("type " + named.Name)
	}

	var b strings.Builder
	b.WriteString(asFerretCodeBlock(decl.Text()))

	instanceMethods, staticMethods := collectTypeMethodSignatures(owner, named.Name)
	if len(instanceMethods) > 0 {
		b.WriteString("\n\nInstance methods:\n")
		b.WriteString(asFerretCodeBlock(strings.Join(instanceMethods, "\n")))
	}
	if len(staticMethods) > 0 {
		b.WriteString("\n\nStatic methods:\n")
		b.WriteString(asFerretCodeBlock(strings.Join(staticMethods, "\n")))
	}
	return b.String()
}

func moduleForNamedType(named *typeinfo.NamedType, fallback *context.Module, modulesByKey map[string]*context.Module) *context.Module {
	if named != nil && named.ModuleKey != "" && modulesByKey != nil {
		if owner, ok := modulesByKey[named.ModuleKey]; ok && owner != nil {
			return owner
		}
	}
	return fallback
}

func findTypeDecl(mod *context.Module, typeName string) *ast.TypeDecl {
	if mod == nil || mod.AST == nil {
		return nil
	}
	for _, decl := range mod.AST.Decls {
		typeDecl, ok := decl.(*ast.TypeDecl)
		if !ok || typeDecl == nil || typeDecl.Name == nil {
			continue
		}
		if typeDecl.Name.Text() == typeName {
			return typeDecl
		}
	}
	return nil
}

func collectTypeMethodSignatures(mod *context.Module, typeName string) ([]string, []string) {
	if mod == nil || typeName == "" {
		return nil, nil
	}
	instanceSet := make(map[string]struct{})
	staticSet := make(map[string]struct{})
	instance := make([]string, 0)
	static := make([]string, 0)

	if mod.MethodSets != nil {
		for receiver, methods := range mod.MethodSets {
			if baseReceiverType(receiver) != typeName {
				continue
			}
			for _, sym := range methods {
				fn, ok := symbolFuncDecl(sym)
				if !ok {
					continue
				}
				sig := fn.Signature()
				if _, exists := instanceSet[sig]; exists {
					continue
				}
				instanceSet[sig] = struct{}{}
				instance = append(instance, sig)
			}
		}
	}
	if mod.TypeMembers != nil {
		for _, sym := range mod.TypeMembers[typeName] {
			if sym == nil || sym.Kind != symbols.SymbolFunc {
				continue
			}
			fn, ok := symbolFuncDecl(sym)
			if !ok || !fn.IsStatic {
				continue
			}
			sig := fn.Signature()
			if _, exists := staticSet[sig]; exists {
				continue
			}
			staticSet[sig] = struct{}{}
			static = append(static, sig)
		}
	}

	sort.Strings(instance)
	sort.Strings(static)
	return instance, static
}

func baseReceiverType(receiver string) string {
	out := strings.TrimSpace(receiver)
	for {
		switch {
		case strings.HasPrefix(out, "&mut "):
			out = strings.TrimSpace(strings.TrimPrefix(out, "&mut "))
		case strings.HasPrefix(out, "&"):
			out = strings.TrimSpace(strings.TrimPrefix(out, "&"))
		case strings.HasPrefix(out, "*"):
			out = strings.TrimSpace(strings.TrimPrefix(out, "*"))
		case strings.HasPrefix(out, "^"):
			out = strings.TrimSpace(strings.TrimPrefix(out, "^"))
		default:
			return out
		}
	}
}

func symbolFuncDecl(sym *symbols.Symbol) (*ast.FuncDecl, bool) {
	if sym == nil {
		return nil, false
	}
	fn, ok := sym.Node.(*ast.FuncDecl)
	return fn, ok
}

func asFerretCodeBlock(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "```ferret\n" + text + "\n```"
}

func findBestHoverCandidate(index *hoverIndex, pos source.Position) (hoverCandidate, bool) {
	if index == nil {
		return hoverCandidate{}, false
	}
	best := hoverCandidate{}
	found := false

	consider := func(candidate hoverCandidate) {
		if !locationContainsPosition(candidate.location, pos) {
			return
		}
		if !found || candidate.span < best.span || (candidate.span == best.span && candidate.priority < best.priority) {
			best = candidate
			found = true
		}
	}

	for _, candidate := range index.candidates {
		consider(candidate)
	}

	return best, found
}

func locationContainsPosition(loc source.Location, pos source.Position) bool {
	if loc.Start == nil {
		return false
	}
	start := *loc.Start
	end := start
	if loc.End != nil {
		end = *loc.End
	}
	if compareSourcePosition(pos, start) < 0 {
		return false
	}
	if compareSourcePosition(start, end) == 0 {
		return compareSourcePosition(pos, start) == 0
	}
	return compareSourcePosition(pos, end) < 0
}

func compareSourcePosition(a, b source.Position) int {
	if a.Line < b.Line {
		return -1
	}
	if a.Line > b.Line {
		return 1
	}
	if a.Column < b.Column {
		return -1
	}
	if a.Column > b.Column {
		return 1
	}
	return 0
}

func locationSpan(loc source.Location) int {
	if loc.Start == nil {
		return int(^uint(0) >> 1)
	}
	start := *loc.Start
	end := start
	if loc.End != nil {
		end = *loc.End
	}
	if start.Index > 0 && end.Index >= start.Index {
		return end.Index - start.Index
	}
	if compareSourcePosition(end, start) < 0 {
		return 0
	}
	lineSpan := end.Line - start.Line
	colSpan := end.Column - start.Column
	return lineSpan*10000 + colSpan
}

func locationToLSPRange(loc source.Location) lspRange {
	startLine := 0
	startCol := 0
	endLine := 0
	endCol := 0

	if loc.Start != nil {
		startLine = max(loc.Start.Line-1, 0)
		startCol = max(loc.Start.Column-1, 0)
		endLine = startLine
		endCol = startCol
	}
	if loc.End != nil {
		endLine = max(loc.End.Line-1, 0)
		endCol = max(loc.End.Column-1, 0)
		if endLine < startLine || (endLine == startLine && endCol < startCol) {
			endLine = startLine
			endCol = startCol
		}
	}

	return lspRange{
		Start: lspPosition{Line: startLine, Character: startCol},
		End:   lspPosition{Line: endLine, Character: endCol},
	}
}

func (s *Server) readMessage() ([]byte, error) {
	contentLength := 0
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %w", err)
			}
			contentLength = n
		}
	}

	if contentLength <= 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}

	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(s.in, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *Server) writeResponse(id json.RawMessage, result interface{}) {
	if len(id) == 0 {
		return
	}
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      decodeID(id),
		Result:  result,
	}
	s.write(resp)
}

func (s *Server) writeError(id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		return
	}
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      decodeID(id),
		Error:   &rpcError{Code: code, Message: message},
	}
	s.write(resp)
}

func (s *Server) writeNotification(method string, params interface{}) {
	n := rpcNotification{JSONRPC: "2.0", Method: method, Params: params}
	s.write(n)
}

func (s *Server) write(v interface{}) {
	body, err := json.Marshal(v)
	if err != nil {
		return
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)

	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(buf.Bytes())
}

func decodeID(raw json.RawMessage) interface{} {
	var numeric float64
	if err := json.Unmarshal(raw, &numeric); err == nil {
		if float64(int(numeric)) == numeric {
			return int(numeric)
		}
		return numeric
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return nil
}

func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported uri scheme %q", u.Scheme)
	}
	return filepath.Clean(filepath.FromSlash(u.Path)), nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
