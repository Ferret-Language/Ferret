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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	textDocumentSyncFull   = 1
	maxHoverMethodsPerKind = 32
	maxCompletionItems     = 200
	diagTagUnnecessary     = 1

	semanticDiagnosticsDebounce = 250 * time.Millisecond

	symbolKindClass      = 5
	symbolKindMethod     = 6
	symbolKindField      = 8
	symbolKindEnum       = 10
	symbolKindInterface  = 11
	symbolKindFunction   = 12
	symbolKindVariable   = 13
	symbolKindConstant   = 14
	symbolKindEnumMember = 22
	symbolKindStruct     = 23

	completionKindText          = 1
	completionKindMethod        = 2
	completionKindFunction      = 3
	completionKindField         = 5
	completionKindVariable      = 6
	completionKindClass         = 7
	completionKindInterface     = 8
	completionKindModule        = 9
	completionKindProperty      = 10
	completionKindKeyword       = 14
	completionKindConstant      = 21
	completionKindStruct        = 22
	completionKindTypeParameter = 25
)

var (
	identPattern        = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*$`)
	memberPattern       = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)?$`)
	staticMemberPattern = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*(?:<[^>\n]*>)?)::([A-Za-z_][A-Za-z0-9_]*)?$`)

	lexSource = func(path, text string, diag *diagnostics.DiagnosticBag) []tokens.Token {
		return lexer.New(path, text, diag).Tokenize()
	}
	parseSource = func(path string, toks []tokens.Token, diag *diagnostics.DiagnosticBag) {
		_ = parser.Parse(path, toks, diag)
	}
	parseProject = func(path string) compiler.Result {
		// LSP needs resolver + typechecker data. Backend passes are too expensive
		// during interactive editing (especially generics-heavy code).
		return compiler.ParsePathForIDE(path)
	}
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
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

type definitionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
}

type completionParams struct {
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
	Tags     []int    `json:"tags,omitempty"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          lspRange         `json:"range"`
	SelectionRange lspRange         `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

type completionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
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
	Version         int
	Text            string
	HasSyntaxErrors bool
}

type hoverCacheEntry struct {
	Mode      string
	Version   int
	FileStamp string
	Index     *hoverIndex
}

type semanticDiagnosticsEntry struct {
	seq   uint64
	timer *time.Timer
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

	semanticGate        chan struct{}
	semanticDiagnostics map[string]*semanticDiagnosticsEntry
}

func Run(stdin io.Reader, stdout io.Writer) error {
	semanticGate := make(chan struct{}, 1)
	semanticGate <- struct{}{}
	s := &Server{
		in:                  bufio.NewReader(stdin),
		out:                 stdout,
		documents:           make(map[string]openDocument),
		hoverCache:          make(map[string]hoverCacheEntry),
		semanticGate:        semanticGate,
		semanticDiagnostics: make(map[string]*semanticDiagnosticsEntry),
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
	case "textDocument/definition":
		s.handleDefinition(req)
	case "textDocument/completion":
		s.handleCompletion(req)
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
			"hoverProvider":      true,
			"definitionProvider": true,
			"completionProvider": map[string]any{
				"triggerCharacters": []string{".", ":"},
			},
		},
		"serverInfo": map[string]any{
			"name":    "ferretls",
			"version": compiler.CompilerVersion,
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
	path, err := uriToPath(uri)
	syntaxValid := true
	if err == nil && isFerretSourcePath(path) {
		syntaxValid = s.publishSyntaxDiagnostics(uri, params.TextDocument.Version, params.TextDocument.Text)
	}
	s.mu.Lock()
	if s.documents == nil {
		s.documents = make(map[string]openDocument)
	}
	s.documents[uri] = openDocument{
		Version:         params.TextDocument.Version,
		Text:            params.TextDocument.Text,
		HasSyntaxErrors: !syntaxValid,
	}
	s.mu.Unlock()

	if err != nil || !isFerretSourcePath(path) {
		s.cancelSemanticDiagnostics(uri)
		s.publishDiagnostics(uri, nil, nil)
		return
	}
	s.scheduleSemanticDiagnostics(uri, path)
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
	path, err := uriToPath(uri)
	syntaxValid := true
	if err == nil && isFerretSourcePath(path) {
		syntaxValid = s.publishSyntaxDiagnostics(uri, params.TextDocument.Version, text)
	}

	s.mu.Lock()
	if s.documents == nil {
		s.documents = make(map[string]openDocument)
	}
	s.documents[uri] = openDocument{
		Version:         params.TextDocument.Version,
		Text:            text,
		HasSyntaxErrors: !syntaxValid,
	}
	s.mu.Unlock()

	if err != nil || !isFerretSourcePath(path) {
		s.cancelSemanticDiagnostics(uri)
		s.publishDiagnostics(uri, nil, nil)
		return
	}
	s.scheduleSemanticDiagnostics(uri, path)
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

	s.cancelSemanticDiagnostics(uri)
	result := compiler.ParsePathForIDE(path)
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

	s.cancelSemanticDiagnostics(params.TextDocument.URI)
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

func (s *Server) handleDefinition(req rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	params := definitionParams{}
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
	defLoc, ok := definitionFromIndex(index, pos)
	if !ok || defLoc.Start == nil {
		s.writeResponse(req.ID, nil)
		return
	}

	defPath := path
	if defLoc.Filename != nil && *defLoc.Filename != "" {
		defPath = *defLoc.Filename
	}
	uri, err := pathToURI(defPath)
	if err != nil {
		s.writeResponse(req.ID, nil)
		return
	}
	s.writeResponse(req.ID, lspLocation{
		URI:   uri,
		Range: locationToLSPRange(defLoc),
	})
}

func (s *Server) handleCompletion(req rpcRequest) {
	if len(req.ID) == 0 {
		return
	}
	params := completionParams{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeResponse(req.ID, []completionItem{})
		return
	}
	path, err := uriToPath(params.TextDocument.URI)
	if err != nil || !isFerretSourcePath(path) {
		s.writeResponse(req.ID, []completionItem{})
		return
	}
	pos := source.Position{
		Line:   params.Position.Line + 1,
		Column: params.Position.Character + 1,
	}
	doc, hasDoc := s.documentState(params.TextDocument.URI)
	index := s.getOrBuildHoverIndex(params.TextDocument.URI, path, doc, hasDoc)
	sourceText := doc.Text
	if !hasDoc {
		if bytes, err := os.ReadFile(path); err == nil {
			sourceText = string(bytes)
		}
	}
	items := completionFromIndex(index, sourceText, pos)
	s.writeResponse(req.ID, items)
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

func (s *Server) scheduleSemanticDiagnostics(uri, path string) {
	if uri == "" || path == "" {
		return
	}
	s.mu.Lock()
	if s.semanticDiagnostics == nil {
		s.semanticDiagnostics = make(map[string]*semanticDiagnosticsEntry)
	}
	entry := s.semanticDiagnostics[uri]
	if entry == nil {
		entry = &semanticDiagnosticsEntry{}
		s.semanticDiagnostics[uri] = entry
	}
	entry.seq++
	seq := entry.seq
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.timer = time.AfterFunc(semanticDiagnosticsDebounce, func() {
		s.publishSemanticDiagnostics(uri, path, seq)
	})
	s.mu.Unlock()
}

func (s *Server) cancelSemanticDiagnostics(uri string) {
	if uri == "" {
		return
	}
	s.mu.Lock()
	if s.semanticDiagnostics != nil {
		if entry := s.semanticDiagnostics[uri]; entry != nil && entry.timer != nil {
			entry.timer.Stop()
		}
		delete(s.semanticDiagnostics, uri)
	}
	s.mu.Unlock()
}

func (s *Server) publishSemanticDiagnostics(uri, path string, seq uint64) {
	if s == nil || uri == "" || path == "" {
		return
	}

	// Ensure we never run more than one semantic compile at a time.
	if s.semanticGate != nil {
		<-s.semanticGate
		defer func() { s.semanticGate <- struct{}{} }()
	}

	s.mu.Lock()
	entry := (*semanticDiagnosticsEntry)(nil)
	if s.semanticDiagnostics != nil {
		entry = s.semanticDiagnostics[uri]
	}
	doc, ok := s.documents[uri]
	if !ok || entry == nil || entry.seq != seq {
		s.mu.Unlock()
		return
	}
	version := doc.Version
	text := doc.Text
	s.mu.Unlock()

	result, parsedPath, cleanup := parseForHover(path, text, true)
	defer cleanup()
	diagnostics := convertDiagnostics(result.Diagnostics.Diagnostics(), path, parsedPath)

	s.mu.Lock()
	entry = nil
	if s.semanticDiagnostics != nil {
		entry = s.semanticDiagnostics[uri]
	}
	current, ok := s.documents[uri]
	if !ok || entry == nil || entry.seq != seq || current.Version != version {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	v := version
	s.publishDiagnostics(uri, &v, diagnostics)
}

func (s *Server) publishSyntaxDiagnostics(uri string, version int, text string) bool {
	path, err := uriToPath(uri)
	if err != nil {
		return false
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
	return !diagBag.HasErrors()
}

func (s *Server) publishDiagnostics(uri string, version *int, diagnostics []lspDiagnostic) {
	s.writeNotification("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         uri,
		Version:     version,
		Diagnostics: diagnostics,
	})
}

func convertDiagnostics(diags []*diagnostics.Diagnostic, targetPath string, extraTargets ...string) []lspDiagnostic {
	out := make([]lspDiagnostic, 0, len(diags))
	targets := make(map[string]struct{}, 1+len(extraTargets))
	if targetPath != "" {
		targets[filepath.Clean(targetPath)] = struct{}{}
	}
	for _, extra := range extraTargets {
		if extra == "" {
			continue
		}
		targets[filepath.Clean(extra)] = struct{}{}
	}
	for _, diag := range diags {
		if diag == nil {
			continue
		}
		if file := diagnosticFilePath(diag); file != "" {
			if _, ok := targets[filepath.Clean(file)]; !ok {
				continue
			}
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
			Tags:     diagnosticTags(diag),
		})
	}
	return out
}

func diagnosticTags(diag *diagnostics.Diagnostic) []int {
	if diag == nil {
		return nil
	}
	switch diag.Code {
	case diagnostics.WarnUnusedImport,
		diagnostics.WarnUnusedPrivateFunction,
		diagnostics.WarnUnusedPrivateType,
		diagnostics.WarnUnusedPrivateBinding,
		diagnostics.WarnUnusedParameter,
		diagnostics.WarnUnusedLocal,
		diagnostics.WarnUnmodifiedMutable,
		diagnostics.InfoUnnecessarySemicolon:
		return []int{diagTagUnnecessary}
	default:
		return nil
	}
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

type definitionCandidate struct {
	location source.Location
	target   source.Location
	span     int
}

type completionCandidate struct {
	item     completionItem
	location source.Location
	scope    source.Location
	name     string
	typ      typeinfo.Type
}

type completionIndex struct {
	keywords []completionItem
	items    []completionCandidate
	mod      *context.Module
	modules  map[string]*context.Module
}

type hoverIndex struct {
	candidates           []hoverCandidate
	definitionCandidates []definitionCandidate
	documentSymbols      []documentSymbol
	completions          *completionIndex
}

func (s *Server) getOrBuildHoverIndex(uri, path string, doc openDocument, hasDoc bool) *hoverIndex {
	if hasDoc {
		if !doc.HasSyntaxErrors {
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
	if mod == nil {
		return &hoverIndex{}
	}
	modules := indexModulesByKey(result)
	sourceText := text
	if !hasText {
		if bytes, err := os.ReadFile(path); err == nil {
			sourceText = string(bytes)
		}
	}
	hoverCandidates, defCandidates := collectHoverCandidates(mod, mod.Types, modules, sourceText, parsedPath, path)
	docSymbols := collectDocumentSymbols(mod, mod.Types)
	completions := collectCompletionIndex(mod, mod.Types, modules)
	return &hoverIndex{
		candidates:           hoverCandidates,
		definitionCandidates: defCandidates,
		documentSymbols:      docSymbols,
		completions:          completions,
	}
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
	file, err := os.CreateTemp(dir, ".ferretls-hover-*.fer")
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

func collectHoverCandidates(mod *context.Module, info *typeinfo.ModuleInfo, modulesByKey map[string]*context.Module, sourceText, parsedPath, originalPath string) ([]hoverCandidate, []definitionCandidate) {
	if mod == nil {
		return nil, nil
	}
	hoverCap := 0
	if info != nil {
		hoverCap = len(info.Nodes) + len(info.Symbols)
	}
	out := make([]hoverCandidate, 0, hoverCap)
	defs := make([]definitionCandidate, 0, hoverCap)
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
	collectDef := func(loc, target source.Location) {
		if loc.Start == nil || target.Start == nil {
			return
		}
		defs = append(defs, definitionCandidate{
			location: loc,
			target:   target,
			span:     locationSpan(loc),
		})
	}
	if info != nil {
		for node, typ := range info.Nodes {
			if node == nil {
				continue
			}
			collect(renderNodeHoverMarkdown(node, typ, mod, info, modulesByKey), node.Loc(), 1)
		}
		for id, typ := range info.Symbols {
			sym := info.SymbolIndex[id]
			if sym == nil {
				continue
			}
			collect(renderSymbolHoverMarkdown(sym, typ, mod, modulesByKey), sym.Location, 0)
		}
	}
	if mod.Bindings != nil {
		for node, res := range mod.Bindings.Nodes {
			if node == nil || res == nil {
				continue
			}
			switch res.Kind {
			case binding.ResolutionSymbol:
				if res.Symbol == nil {
					continue
				}
				collectDef(node.Loc(), normalizeLocationFile(res.Symbol.Location, parsedPath, originalPath))
			case binding.ResolutionModule:
				if markdown := renderModuleResolutionHoverMarkdown(res, modulesByKey); markdown != "" {
					collect(markdown, node.Loc(), 1)
				}
				if moduleLoc := moduleDefinitionLocationFromResolution(res, modulesByKey); moduleLoc.Start != nil {
					collectDef(node.Loc(), normalizeLocationFile(moduleLoc, parsedPath, originalPath))
				}
			}
		}
		for node, label := range mod.Bindings.Labels {
			ident, ok := node.(*ast.Ident)
			if !ok || ident == nil {
				continue
			}
			collect(renderLabelHoverMarkdown(label), ident.Loc(), 2)
			collectDef(ident.Loc(), normalizeLocationFile(label.Location, parsedPath, originalPath))
		}
		if info != nil {
			pathHover, pathDefs := collectQualifiedPathCandidates(mod, info, modulesByKey, sourceText, parsedPath, originalPath)
			out = append(out, pathHover...)
			defs = append(defs, pathDefs...)
		}
	}
	out = append(out, collectOwnershipCastSyntaxHoverCandidates(mod, info)...)
	return out, defs
}

func collectOwnershipCastSyntaxHoverCandidates(mod *context.Module, info *typeinfo.ModuleInfo) []hoverCandidate {
	if mod == nil || mod.AST == nil {
		return nil
	}
	out := make([]hoverCandidate, 0)
	for _, decl := range mod.AST.Decls {
		collectOwnershipCastFromDecl(decl, info, &out)
	}
	return out
}

func collectOwnershipCastFromDecl(decl ast.Decl, info *typeinfo.ModuleInfo, out *[]hoverCandidate) {
	if decl == nil {
		return
	}
	switch d := decl.(type) {
	case *ast.ConstDecl:
		collectOwnershipCastFromExpr(d.Value, info, out)
	case *ast.LetDecl:
		collectOwnershipCastFromExpr(d.Value, info, out)
	case *ast.FuncDecl:
		collectOwnershipCastFromStmt(d.Body, info, out)
	case *ast.TypeDecl:
		structType, ok := d.Type.(*ast.StructType)
		if !ok || structType == nil {
			return
		}
		for _, field := range structType.Fields {
			if field == nil {
				continue
			}
			collectOwnershipCastFromExpr(field.Default, info, out)
		}
	}
}

func collectOwnershipCastFromStmt(stmt ast.Stmt, info *typeinfo.ModuleInfo, out *[]hoverCandidate) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		if s == nil {
			return
		}
		for _, nested := range s.Stmts {
			collectOwnershipCastFromStmt(nested, info, out)
		}
	case *ast.LetStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Value, info, out)
	case *ast.ConstStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Value, info, out)
	case *ast.ReturnStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Value, info, out)
	case *ast.ExprStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Value, info, out)
	case *ast.AssignStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Left, info, out)
		collectOwnershipCastFromExpr(s.Right, info, out)
	case *ast.IfStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Cond, info, out)
		collectOwnershipCastFromStmt(s.Then, info, out)
		collectOwnershipCastFromStmt(s.Else, info, out)
	case *ast.MatchStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Value, info, out)
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			collectOwnershipCastFromExpr(arm.Pattern, info, out)
			collectOwnershipCastFromStmt(arm.Body, info, out)
		}
	case *ast.WhileStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Cond, info, out)
		collectOwnershipCastFromStmt(s.Body, info, out)
	case *ast.ForStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Iterable, info, out)
		collectOwnershipCastFromStmt(s.Body, info, out)
	case *ast.LabelStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromStmt(s.Stmt, info, out)
	case *ast.DeferStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromStmt(s.Body, info, out)
	case *ast.ReleaseStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Value, info, out)
	case *ast.PanicStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Value, info, out)
	case *ast.LockStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromExpr(s.Value, info, out)
		collectOwnershipCastFromStmt(s.Body, info, out)
	case *ast.UnsafeStmt:
		if s == nil {
			return
		}
		collectOwnershipCastFromStmt(s.Body, info, out)
	}
}

func collectOwnershipCastFromExpr(expr ast.Expr, info *typeinfo.ModuleInfo, out *[]hoverCandidate) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.PrefixExpr:
		if e == nil {
			return
		}
		collectOwnershipCastFromExpr(e.Right, info, out)
	case *ast.BinaryExpr:
		if e == nil {
			return
		}
		collectOwnershipCastFromExpr(e.Left, info, out)
		collectOwnershipCastFromExpr(e.Right, info, out)
	case *ast.PostfixExpr:
		if e == nil {
			return
		}
		collectOwnershipCastFromExpr(e.Left, info, out)
	case *ast.CallExpr:
		if e == nil {
			return
		}
		collectOwnershipCastFromExpr(e.Callee, info, out)
		for _, arg := range e.Args {
			collectOwnershipCastFromExpr(arg, info, out)
		}
	case *ast.SelectorExpr:
		if e == nil {
			return
		}
		collectOwnershipCastFromExpr(e.Left, info, out)
	case *ast.CastExpr:
		if e == nil {
			return
		}
		if note := ownershipBoundaryCastNoteForExpr(e, info); note != "" {
			loc := e.Loc()
			*out = append(*out, hoverCandidate{
				markdown: note,
				location: loc,
				span:     locationSpan(loc),
				priority: 3,
			})
		}
		collectOwnershipCastFromExpr(e.Left, info, out)
	case *ast.IsExpr:
		if e == nil {
			return
		}
		collectOwnershipCastFromExpr(e.Left, info, out)
	case *ast.MatchExpr:
		if e == nil {
			return
		}
		collectOwnershipCastFromExpr(e.Value, info, out)
		for _, arm := range e.Arms {
			if arm == nil {
				continue
			}
			collectOwnershipCastFromExpr(arm.Pattern, info, out)
			collectOwnershipCastFromStmt(arm.Body, info, out)
		}
	case *ast.CatchExpr:
		if e == nil {
			return
		}
		collectOwnershipCastFromExpr(e.Left, info, out)
		collectOwnershipCastFromExpr(e.Fallback, info, out)
		collectOwnershipCastFromStmt(e.Handler, info, out)
	case *ast.CompositeLit:
		if e == nil {
			return
		}
		for _, item := range e.Items {
			collectOwnershipCastFromExpr(item.Value, info, out)
		}
	case *ast.IndexExpr:
		if e == nil {
			return
		}
		collectOwnershipCastFromExpr(e.Left, info, out)
		collectOwnershipCastFromExpr(e.Index, info, out)
	}
}

func ownershipBoundaryCastNoteForExpr(cast *ast.CastExpr, info *typeinfo.ModuleInfo) string {
	if cast == nil {
		return ""
	}
	if note := renderOwnershipBoundaryCastHoverNote(cast, info); note != "" {
		return note
	}
	if !isOwnershipPointerTypeExpr(cast.Type) {
		return ""
	}
	return ownershipBoundaryCastHoverNoteText()
}

func isOwnershipPointerTypeExpr(typ ast.TypeExpr) bool {
	switch typ.(type) {
	case *ast.PointerType, *ast.RawPtrType:
		return true
	default:
		return false
	}
}

func collectQualifiedPathCandidates(mod *context.Module, info *typeinfo.ModuleInfo, modulesByKey map[string]*context.Module, sourceText, parsedPath, originalPath string) ([]hoverCandidate, []definitionCandidate) {
	if mod == nil || mod.Bindings == nil || info == nil || sourceText == "" {
		return nil, nil
	}
	hoverOut := make([]hoverCandidate, 0)
	defOut := make([]definitionCandidate, 0)
	for node, res := range mod.Bindings.Nodes {
		ident, ok := node.(*ast.Ident)
		if !ok || ident == nil || len(ident.Path) < 2 || res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
			continue
		}
		segments := identifierPathSegmentLocations(ident, sourceText, originalPath)
		if len(segments) != len(ident.Path) {
			continue
		}
		memberLoc := segments[len(segments)-1]
		memberType := info.Nodes[node]
		memberMarkdown := renderNodeHoverMarkdown(node, memberType, mod, info, modulesByKey)
		if memberMarkdown == "" {
			memberOwner := moduleFromResolution(modulesByKey, mod, res.ModuleKey)
			memberMarkdown = renderSymbolHoverMarkdown(res.Symbol, memberType, memberOwner, modulesByKey)
		}
		if memberMarkdown != "" {
			hoverOut = append(hoverOut, hoverCandidate{
				markdown: memberMarkdown,
				location: memberLoc,
				span:     locationSpan(memberLoc),
				priority: 0,
			})
		}
		memberTarget := normalizeLocationFile(res.Symbol.Location, parsedPath, originalPath)
		if memberTarget.Start != nil {
			defOut = append(defOut, definitionCandidate{
				location: memberLoc,
				target:   memberTarget,
				span:     locationSpan(memberLoc),
			})
		}

		ownerSym, ownerMod := ownerTypeSymbolForResolution(modulesByKey, mod, res)
		if ownerSym == nil || len(segments) < 2 {
			continue
		}
		ownerLoc := segments[len(segments)-2]
		ownerType := concreteOwnerTypeForPath(ident, ownerSym, ownerMod, info)
		ownerMarkdown := renderSymbolHoverMarkdown(ownerSym, ownerType, ownerMod, modulesByKey)
		if ownerMarkdown != "" {
			hoverOut = append(hoverOut, hoverCandidate{
				markdown: ownerMarkdown,
				location: ownerLoc,
				span:     locationSpan(ownerLoc),
				priority: 0,
			})
		}
		ownerTarget := normalizeLocationFile(ownerSym.Location, parsedPath, originalPath)
		if ownerTarget.Start != nil {
			defOut = append(defOut, definitionCandidate{
				location: ownerLoc,
				target:   ownerTarget,
				span:     locationSpan(ownerLoc),
			})
		}
	}
	return hoverOut, defOut
}

func completionFromIndex(index *hoverIndex, sourceText string, pos source.Position) []completionItem {
	if index == nil || index.completions == nil {
		return nil
	}
	comp := index.completions
	ctx := completionContextAt(sourceText, pos)
	candidates := visibleCompletionCandidatesAt(comp.items, pos)
	items := make([]completionItem, 0)
	if ctx.IsMember {
		items = append(items, memberCompletionItemsForContext(comp, candidates, sourceText, pos)...)
	} else {
		items = make([]completionItem, 0, len(comp.keywords)+len(candidates))
		items = append(items, comp.keywords...)
		for _, candidate := range candidates {
			items = append(items, candidate.item)
		}
	}
	items = filterCompletionItemsByPrefix(items, ctx.Prefix)
	items = dedupeCompletionItems(items)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Label == items[j].Label {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Label < items[j].Label
	})
	if len(items) > maxCompletionItems {
		items = items[:maxCompletionItems]
	}
	return items
}

func collectCompletionIndex(mod *context.Module, info *typeinfo.ModuleInfo, modulesByKey map[string]*context.Module) *completionIndex {
	if mod == nil {
		return &completionIndex{keywords: keywordCompletionItems()}
	}
	index := &completionIndex{
		keywords: keywordCompletionItems(),
		items:    make([]completionCandidate, 0),
		mod:      mod,
		modules:  modulesByKey,
	}
	addSymbol := func(sym *symbols.Symbol, scope source.Location) {
		if sym == nil || sym.Name == "" {
			return
		}
		item := completionItemForSymbol(sym, info, mod)
		if item.Label == "" {
			return
		}
		index.items = append(index.items, completionCandidate{
			item:     item,
			location: sym.Location,
			scope:    scope,
			name:     sym.Name,
			typ:      symbolTypeForCompletion(sym, info),
		})
	}
	if mod.ModuleScope != nil {
		for _, sym := range mod.ModuleScope.Symbols() {
			addSymbol(sym, source.Location{})
		}
	}
	if mod.Bindings != nil {
		for fn, locals := range mod.Bindings.FunctionLocals {
			if fn == nil {
				continue
			}
			scope := fn.Loc()
			for _, sym := range locals {
				addSymbol(sym, scope)
			}
			for _, typeParam := range fn.TypeParams {
				if typeParam.Name == nil {
					continue
				}
				typeName := typeParam.Name.Text()
				if typeName == "" {
					continue
				}
				item := completionItem{
					Label: typeName,
					Kind:  completionKindTypeParameter,
				}
				if typeParam.Constraint != nil {
					item.Detail = "type parameter: " + typeName + ": " + ast.TypeString(typeParam.Constraint)
				} else {
					item.Detail = "type parameter: " + typeName
				}
				index.items = append(index.items, completionCandidate{
					item:     item,
					location: typeParam.Name.Loc(),
					scope:    scope,
					name:     typeName,
					typ:      info.Nodes[typeParam.Name],
				})
			}
		}
	}
	return index
}

func symbolTypeForCompletion(sym *symbols.Symbol, info *typeinfo.ModuleInfo) typeinfo.Type {
	if sym == nil || info == nil {
		return nil
	}
	return info.Symbols[sym.ID]
}

func completionItemForSymbol(sym *symbols.Symbol, info *typeinfo.ModuleInfo, mod *context.Module) completionItem {
	item := completionItem{
		Label: sym.Name,
		Kind:  completionKindForSymbol(sym),
	}
	typ := symbolTypeForCompletion(sym, info)
	if sig := symbolFunctionSignature(sym, nil); sig != "" {
		item.Detail = sig
	} else if fnType, ok := typ.(*typeinfo.FuncType); ok && fnType != nil {
		item.Detail = typeinfo.DefaultPrinter.FuncSignature(sym.Name, fnType)
	} else if typ != nil {
		item.Detail = typeinfo.DefaultPrinter.Type(typ)
	}
	if decl := renderBindingDeclForSymbol(sym, typ, mod); decl != "" {
		item.Detail = decl
	}
	if doc := declarationDocForSymbol(sym); doc != "" {
		item.Documentation = doc
	}
	return item
}

func completionKindForSymbol(sym *symbols.Symbol) int {
	if sym == nil {
		return completionKindText
	}
	switch sym.Kind {
	case symbols.SymbolFunc:
		return completionKindFunction
	case symbols.SymbolMethod:
		return completionKindMethod
	case symbols.SymbolType:
		if decl, ok := sym.Node.(*ast.TypeDecl); ok && decl != nil {
			switch decl.Type.(type) {
			case *ast.StructType:
				return completionKindStruct
			case *ast.InterfaceType:
				return completionKindInterface
			default:
				return completionKindClass
			}
		}
		return completionKindClass
	case symbols.SymbolConst:
		return completionKindConstant
	case symbols.SymbolVar, symbols.SymbolParam:
		return completionKindVariable
	case symbols.SymbolField:
		return completionKindField
	case symbols.SymbolImport:
		return completionKindModule
	default:
		return completionKindText
	}
}

func keywordCompletionItems() []completionItem {
	keywords := []string{
		"import", "const", "let", "mut", "fn", "type", "struct", "interface", "enum", "union",
		"error", "if", "else", "match", "for", "while", "break", "continue",
		"return", "comptime", "catch", "is", "as", "defer", "release", "panic", "lock", "unsafe",
		"self", "none", "true", "false",
	}
	items := make([]completionItem, 0, len(keywords))
	for _, keyword := range keywords {
		items = append(items, completionItem{Label: keyword, Kind: completionKindKeyword})
	}
	return items
}

func visibleCompletionCandidatesAt(items []completionCandidate, pos source.Position) []completionCandidate {
	out := make([]completionCandidate, 0, len(items))
	for _, item := range items {
		if !locationIsVisibleAt(item.scope, pos) {
			continue
		}
		if item.location.Start != nil && compareSourcePosition(*item.location.Start, pos) > 0 {
			continue
		}
		out = append(out, item)
	}
	return out
}

func locationIsVisibleAt(scope source.Location, pos source.Position) bool {
	if scope.Start == nil {
		return true
	}
	return locationContainsPosition(scope, pos)
}

type completionContext struct {
	Base     string
	Prefix   string
	IsMember bool
	IsStatic bool
}

func completionContextAt(sourceText string, pos source.Position) completionContext {
	offset := sourceOffsetAtPosition(sourceText, pos)
	if offset < 0 || offset > len(sourceText) {
		return completionContext{}
	}
	lineStart := strings.LastIndex(sourceText[:offset], "\n")
	if lineStart < 0 {
		lineStart = 0
	} else {
		lineStart++
	}
	prefixText := sourceText[lineStart:offset]
	if match := staticMemberPattern.FindStringSubmatch(prefixText); len(match) == 3 {
		return completionContext{
			Base:     strings.TrimSpace(match[1]),
			Prefix:   match[2],
			IsMember: true,
			IsStatic: true,
		}
	}
	if match := memberPattern.FindStringSubmatch(prefixText); len(match) == 3 {
		return completionContext{
			Base:     match[1],
			Prefix:   match[2],
			IsMember: true,
		}
	}
	if ident := identPattern.FindString(prefixText); ident != "" {
		return completionContext{Prefix: ident}
	}
	return completionContext{}
}

func sourceOffsetAtPosition(sourceText string, pos source.Position) int {
	if sourceText == "" {
		return 0
	}
	if pos.Line <= 1 {
		column := max(pos.Column-1, 0)
		if column > len(sourceText) {
			return len(sourceText)
		}
		return column
	}
	line := 1
	offset := 0
	for offset < len(sourceText) && line < pos.Line {
		next := strings.IndexByte(sourceText[offset:], '\n')
		if next < 0 {
			return len(sourceText)
		}
		offset += next + 1
		line++
	}
	column := max(pos.Column-1, 0)
	lineEnd := offset
	for lineEnd < len(sourceText) && sourceText[lineEnd] != '\n' {
		lineEnd++
	}
	if offset+column > lineEnd {
		return lineEnd
	}
	return offset + column
}

func memberCompletionItemsForContext(index *completionIndex, visible []completionCandidate, sourceText string, pos source.Position) []completionItem {
	if index == nil || index.mod == nil {
		return nil
	}
	ctx := completionContextAt(sourceText, pos)
	if !ctx.IsMember || ctx.Base == "" {
		return nil
	}
	if ctx.IsStatic {
		items := staticMemberCompletionItems(index.mod, ctx.Base)
		items = append(items, moduleMemberCompletionItems(index.mod, index.modules, ctx.Base)...)
		return items
	}
	baseType := visibleSymbolType(ctx.Base, visible)
	if baseType == nil {
		return nil
	}
	return instanceMemberCompletionItems(index.mod, index.modules, baseType)
}

func visibleSymbolType(name string, visible []completionCandidate) typeinfo.Type {
	for i := len(visible) - 1; i >= 0; i-- {
		if visible[i].name == name && visible[i].typ != nil {
			return visible[i].typ
		}
	}
	return nil
}

func staticMemberCompletionItems(mod *context.Module, ownerText string) []completionItem {
	if mod == nil || mod.TypeMembers == nil {
		return nil
	}
	ownerName := ownerText
	if idx := strings.Index(ownerName, "<"); idx >= 0 {
		ownerName = ownerName[:idx]
	}
	members := mod.TypeMembers[ownerName]
	if len(members) == 0 {
		return nil
	}
	items := make([]completionItem, 0, len(members))
	for _, sym := range members {
		if sym == nil || sym.Kind != symbols.SymbolFunc {
			continue
		}
		fn, ok := sym.Node.(*ast.FuncDecl)
		if !ok || fn == nil || !fn.IsStatic {
			continue
		}
		items = append(items, completionItem{
			Label:         sym.Name,
			Kind:          completionKindMethod,
			Detail:        symbolFunctionSignature(sym, nil),
			Documentation: declarationDocForSymbol(sym),
		})
	}
	return items
}

func moduleMemberCompletionItems(mod *context.Module, modulesByKey map[string]*context.Module, ownerText string) []completionItem {
	if mod == nil || mod.Bindings == nil || len(mod.Bindings.Imports) == 0 {
		return nil
	}
	ownerName := strings.TrimSpace(ownerText)
	if ownerName == "" {
		return nil
	}
	if idx := strings.Index(ownerName, "<"); idx >= 0 {
		ownerName = ownerName[:idx]
	}
	var imp *binding.ImportBinding
	for _, b := range mod.Bindings.Imports {
		if b == nil {
			continue
		}
		if b.Key() == ownerName {
			imp = b
			break
		}
	}
	if imp == nil {
		return nil
	}
	target := moduleFromResolution(modulesByKey, nil, imp.ModuleKey)
	if target == nil || target.ModuleScope == nil {
		return nil
	}
	items := make([]completionItem, 0, len(target.ModuleScope.Symbols()))
	for _, sym := range target.ModuleScope.Symbols() {
		if sym == nil || !sym.IsPub {
			continue
		}
		item := completionItemForSymbol(sym, target.Types, target)
		if item.Label == "" {
			continue
		}
		items = append(items, item)
	}
	return items
}

func instanceMemberCompletionItems(mod *context.Module, modulesByKey map[string]*context.Module, baseType typeinfo.Type) []completionItem {
	named := underlyingNamedType(baseType)
	if named == nil {
		return nil
	}
	owner := moduleForNamedType(named, mod, modulesByKey)
	if owner == nil {
		return nil
	}
	items := make([]completionItem, 0)
	decl := named.Decl
	if decl == nil {
		decl = findTypeDecl(owner, named.Name)
	}
	resolved := declTypeForNamed(owner, named, decl)
	if structType, ok := resolved.(*typeinfo.StructType); ok && structType != nil {
		if decl != nil {
			if declStruct, ok := decl.Type.(*ast.StructType); ok && declStruct != nil {
				for _, field := range declStruct.Fields {
					if field == nil || field.Name == nil {
						continue
					}
					fieldType := structFieldTypeByName(structType, field.Name.Text())
					items = append(items, completionItem{
						Label:  field.Name.Text(),
						Kind:   completionKindField,
						Detail: typeinfo.DefaultPrinter.Type(fieldType),
					})
				}
			}
		} else {
			for _, field := range structType.OrderedFields {
				if field == nil || field.Name == "" {
					continue
				}
				items = append(items, completionItem{
					Label:  field.Name,
					Kind:   completionKindField,
					Detail: typeinfo.DefaultPrinter.Type(field.Type),
				})
			}
		}
	}
	if owner.MethodSets != nil {
		for receiver, methods := range owner.MethodSets {
			if receiver.TypeName != named.Name {
				continue
			}
			for _, sym := range methods {
				if sym == nil {
					continue
				}
				items = append(items, completionItem{
					Label:         sym.Name,
					Kind:          completionKindMethod,
					Detail:        symbolFunctionSignature(sym, nil),
					Documentation: declarationDocForSymbol(sym),
				})
			}
		}
	}
	return items
}

func filterCompletionItemsByPrefix(items []completionItem, prefix string) []completionItem {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return items
	}
	prefixLower := strings.ToLower(prefix)
	out := make([]completionItem, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item.Label), prefixLower) {
			out = append(out, item)
		}
	}
	return out
}

func dedupeCompletionItems(items []completionItem) []completionItem {
	if len(items) <= 1 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]completionItem, 0, len(items))
	for _, item := range items {
		key := item.Label + "#" + strconv.Itoa(item.Kind)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func collectDocumentSymbols(mod *context.Module, info *typeinfo.ModuleInfo) []documentSymbol {
	if mod == nil || mod.AST == nil {
		return nil
	}
	out := make([]documentSymbol, 0, len(mod.AST.Decls))
	typeSymbolIndex := make(map[string]int)
	pendingTypeMethods := make(map[string][]documentSymbol)

	for _, decl := range mod.AST.Decls {
		switch d := decl.(type) {
		case *ast.TypeDecl:
			if d == nil || d.Name == nil {
				continue
			}
			sym := documentSymbolForTypeDecl(d)
			typeName := d.Name.Text()
			typeSymbolIndex[typeName] = len(out)
			if pending := pendingTypeMethods[typeName]; len(pending) > 0 {
				sym.Children = append(sym.Children, pending...)
				delete(pendingTypeMethods, typeName)
			}
			out = append(out, sym)
		case *ast.FuncDecl:
			if d == nil || d.Name == nil {
				continue
			}
			sym := documentSymbolForFuncDecl(d, mod, info)
			if d.OwnerType != nil && len(d.OwnerType.Path) > 0 {
				typeName := d.OwnerType.Path[len(d.OwnerType.Path)-1]
				if idx, ok := typeSymbolIndex[typeName]; ok {
					out[idx].Children = append(out[idx].Children, sym)
				} else {
					pendingTypeMethods[typeName] = append(pendingTypeMethods[typeName], sym)
				}
				continue
			}
			out = append(out, sym)
		case *ast.ConstDecl:
			if d == nil || d.Name == nil {
				continue
			}
			detail := ""
			if info != nil {
				if typ := info.Nodes[d.Name]; typ != nil {
					detail = typeinfo.DefaultPrinter.Type(typ)
				}
			}
			out = append(out, makeDocumentSymbol(d.Name.Text(), symbolKindConstant, d.Loc(), d.Name.Loc(), detail, nil))
		case *ast.LetDecl:
			if d == nil || d.Name == nil {
				continue
			}
			detail := ""
			if info != nil {
				if typ := info.Nodes[d.Name]; typ != nil {
					detail = typeinfo.DefaultPrinter.Type(typ)
				}
			}
			out = append(out, makeDocumentSymbol(d.Name.Text(), symbolKindVariable, d.Loc(), d.Name.Loc(), detail, nil))
		}
	}

	// Gracefully expose attached methods even if the owner type symbol wasn't emitted.
	if len(pendingTypeMethods) > 0 {
		typeNames := make([]string, 0, len(pendingTypeMethods))
		for name := range pendingTypeMethods {
			typeNames = append(typeNames, name)
		}
		sort.Strings(typeNames)
		for _, name := range typeNames {
			out = append(out, pendingTypeMethods[name]...)
		}
	}
	return out
}

func documentSymbolForTypeDecl(d *ast.TypeDecl) documentSymbol {
	if d == nil || d.Name == nil {
		return documentSymbol{}
	}
	kind := symbolKindClass
	children := make([]documentSymbol, 0)
	switch t := d.Type.(type) {
	case *ast.StructType:
		kind = symbolKindStruct
		for _, field := range t.Fields {
			if field == nil || field.Name == nil {
				continue
			}
			detail := ast.TypeString(field.Type)
			children = append(children, makeDocumentSymbol(field.Name.Text(), symbolKindField, field.Loc(), field.Name.Loc(), detail, nil))
		}
	case *ast.InterfaceType:
		kind = symbolKindInterface
		for _, method := range t.Methods {
			if method == nil || method.Name == nil {
				continue
			}
			detail := interfaceMethodDetail(method)
			children = append(children, makeDocumentSymbol(method.Name.Text(), symbolKindMethod, method.Location, method.Name.Loc(), detail, nil))
		}
	case *ast.EnumType:
		kind = symbolKindEnum
		for _, variant := range t.Variants {
			if variant == nil || variant.Name == nil {
				continue
			}
			children = append(children, makeDocumentSymbol(variant.Name.Text(), symbolKindEnumMember, variant.Loc(), variant.Name.Loc(), "", nil))
		}
	case *ast.ErrorType:
		kind = symbolKindEnum
		for _, member := range t.Members {
			if member == nil || member.Name == nil {
				continue
			}
			children = append(children, makeDocumentSymbol(member.Name.Text(), symbolKindEnumMember, member.Loc(), member.Name.Loc(), "", nil))
		}
	}
	return makeDocumentSymbol(d.Name.Text(), kind, d.Loc(), d.Name.Loc(), "", children)
}

func documentSymbolForFuncDecl(d *ast.FuncDecl, mod *context.Module, info *typeinfo.ModuleInfo) documentSymbol {
	if d == nil || d.Name == nil {
		return documentSymbol{}
	}
	kind := symbolKindFunction
	if d.OwnerType != nil {
		kind = symbolKindMethod
	}
	name := d.Name.Text()
	if d.IsTest {
		name = `test "` + d.TestName + `"`
	}
	detail := functionDetailForOutline(d, mod, info)
	return makeDocumentSymbol(name, kind, d.Loc(), d.Name.Loc(), detail, nil)
}

func functionDetailForOutline(fn *ast.FuncDecl, mod *context.Module, info *typeinfo.ModuleInfo) string {
	if fn == nil {
		return ""
	}
	if mod != nil && mod.Bindings != nil && info != nil {
		if sym := mod.Bindings.FunctionSymbols[fn]; sym != nil {
			if fnType, ok := info.Symbols[sym.ID].(*typeinfo.FuncType); ok && fnType != nil {
				return typeinfo.DefaultPrinter.FuncDeclSignature(fn, fnType)
			}
		}
	}
	return fn.Signature()
}

func interfaceMethodDetail(method *ast.InterfaceMethod) string {
	if method == nil {
		return ""
	}
	parts := make([]string, 0, len(method.Params))
	for _, param := range method.Params {
		name := "_"
		if param.Name != nil && param.Name.Text() != "" {
			name = param.Name.Text()
		}
		text := name + ": " + ast.TypeString(param.Type)
		if param.IsMut {
			text = "mut " + text
		}
		parts = append(parts, text)
	}
	result := ast.TypeString(method.Result)
	if result == "" {
		result = "void"
	}
	return "fn " + method.Name.Text() + "(" + strings.Join(parts, ", ") + ") " + result
}

func makeDocumentSymbol(name string, kind int, fullLoc, selectLoc source.Location, detail string, children []documentSymbol) documentSymbol {
	if selectLoc.Start == nil {
		selectLoc = fullLoc
	}
	sym := documentSymbol{
		Name:           name,
		Detail:         detail,
		Kind:           kind,
		Range:          locationToLSPRange(fullLoc),
		SelectionRange: locationToLSPRange(selectLoc),
	}
	if len(children) > 0 {
		sym.Children = children
	}
	return sym
}

func moduleFromResolution(modulesByKey map[string]*context.Module, fallback *context.Module, moduleKey string) *context.Module {
	if moduleKey != "" && modulesByKey != nil {
		if mod := modulesByKey[moduleKey]; mod != nil {
			return mod
		}
	}
	return fallback
}

func renderModuleResolutionHoverMarkdown(res *binding.Resolution, modulesByKey map[string]*context.Module) string {
	if res == nil {
		return ""
	}
	target := moduleFromResolution(modulesByKey, nil, res.ModuleKey)
	importPath := strings.TrimSpace(res.ImportPath)
	if importPath == "" && target != nil {
		importPath = target.ImportPath
	}
	if importPath == "" {
		return ""
	}
	return appendHoverDoc(asFerretCodeBlock(`import "`+importPath+`"`), declarationDocForModule(target))
}

func moduleDefinitionLocationFromResolution(res *binding.Resolution, modulesByKey map[string]*context.Module) source.Location {
	if res == nil {
		return source.Location{}
	}
	target := moduleFromResolution(modulesByKey, nil, res.ModuleKey)
	return moduleDefinitionLocation(target)
}

func moduleDefinitionLocation(mod *context.Module) source.Location {
	if mod == nil || mod.AST == nil {
		return source.Location{}
	}
	if mod.AST.Doc != nil {
		return mod.AST.Doc.Location
	}
	if len(mod.AST.Imports) > 0 && mod.AST.Imports[0] != nil {
		return mod.AST.Imports[0].Loc()
	}
	if len(mod.AST.Decls) > 0 && mod.AST.Decls[0] != nil {
		return mod.AST.Decls[0].Loc()
	}
	return source.Location{}
}

func ownerTypeSymbolForResolution(modulesByKey map[string]*context.Module, fallback *context.Module, res *binding.Resolution) (*symbols.Symbol, *context.Module) {
	if res == nil || res.Symbol == nil || res.Symbol.OwnerType == "" {
		return nil, nil
	}
	ownerMod := moduleFromResolution(modulesByKey, fallback, res.ModuleKey)
	if ownerMod == nil || ownerMod.ModuleScope == nil {
		return nil, ownerMod
	}
	sym, ok := ownerMod.ModuleScope.LookupLocal(res.Symbol.OwnerType)
	if !ok || sym == nil || sym.Kind != symbols.SymbolType {
		return nil, ownerMod
	}
	return sym, ownerMod
}

func concreteOwnerTypeForPath(ident *ast.Ident, ownerSym *symbols.Symbol, ownerMod *context.Module, info *typeinfo.ModuleInfo) typeinfo.Type {
	if ownerSym == nil || ownerMod == nil || ownerMod.Types == nil {
		return nil
	}
	base := ownerMod.Types.Symbols[ownerSym.ID]
	if ident == nil || len(ident.TypeArgs) == 0 {
		return base
	}
	decl, ok := ownerSym.Node.(*ast.TypeDecl)
	if !ok || decl == nil {
		return base
	}
	args := make([]typeinfo.Type, 0, len(ident.TypeArgs))
	for _, arg := range ident.TypeArgs {
		if info == nil {
			return base
		}
		argType := info.Nodes[arg]
		if argType == nil {
			return base
		}
		args = append(args, argType)
	}
	return &typeinfo.NamedType{
		ModuleKey: ownerMod.Key,
		Name:      ownerSym.Name,
		Decl:      decl,
		TypeArgs:  args,
	}
}

func identifierPathSegmentLocations(ident *ast.Ident, text, file string) []source.Location {
	if ident == nil || len(ident.Path) == 0 || text == "" {
		return nil
	}
	loc := ident.Loc()
	if loc.Start == nil || loc.End == nil {
		return nil
	}
	start := loc.Start.Index
	end := loc.End.Index
	if start < 0 || end < start || end > len(text) {
		return nil
	}
	snippet := text[start:end]
	pos := *loc.Start
	cursor := 0
	out := make([]source.Location, 0, len(ident.Path))
	for _, segment := range ident.Path {
		if segment == "" {
			return nil
		}
		rel := strings.Index(snippet[cursor:], segment)
		if rel < 0 {
			return nil
		}
		segStartOffset := cursor + rel
		if segStartOffset > cursor {
			pos.Advance(snippet[cursor:segStartOffset])
		}
		segStart := pos
		pos.Advance(segment)
		segEnd := pos
		out = append(out, source.NewLocation(file, segStart, segEnd))
		cursor = segStartOffset + len(segment)
	}
	return out
}

func normalizeLocationFile(loc source.Location, parsedPath, originalPath string) source.Location {
	if originalPath == "" || parsedPath == "" {
		return loc
	}
	candidate := ""
	if loc.Filename != nil {
		candidate = *loc.Filename
	} else if loc.File != "" {
		candidate = loc.File
	}
	if candidate == "" || !sameFilePath(candidate, parsedPath) {
		return loc
	}
	normalized := loc
	normalized.File = originalPath
	normalized.Filename = &normalized.File
	return normalized
}

func renderLabelHoverMarkdown(label *binding.LabelBinding) string {
	if label == nil || label.Name == "" {
		return ""
	}
	prefix := "label"
	switch label.Stmt.(type) {
	case *ast.WhileStmt, *ast.ForStmt:
		prefix = "loop label"
	}
	return asFerretCodeBlock(prefix + " " + label.Name)
}

func renderNodeHoverMarkdown(node ast.Node, typ typeinfo.Type, mod *context.Module, info *typeinfo.ModuleInfo, modulesByKey map[string]*context.Module) string {
	ownershipNote := renderOwnershipBoundaryCastHoverNote(node, info)
	if expr, ok := node.(ast.Expr); ok {
		if _, isRange := expr.(*ast.RangeExpr); isRange {
			return appendHoverNote(asFerretCodeBlock(ast.ExprString(expr)), ownershipNote)
		}
	}
	if typ == nil {
		return appendHoverNote("", ownershipNote)
	}
	sym := resolvedSymbolForNode(mod, node)
	if constrained := renderConstrainedTypeParamBindingHover(sym, typ); constrained != "" {
		return appendHoverNote(appendHoverDoc(constrained, declarationDocForNode(node, mod)), ownershipNote)
	}
	bindingDecl := renderBindingDeclForNode(node, typ, mod)
	if sig := renderNodeFunctionSignature(node, typ, mod, info); sig != "" {
		return appendHoverNote(appendHoverDoc(asFerretCodeBlock(sig), declarationDocForNode(node, mod)), ownershipNote)
	}
	if selector, ok := node.(*ast.SelectorExpr); ok {
		if fnType, ok := typ.(*typeinfo.FuncType); ok {
			receiver := ""
			if info != nil && selector.Left != nil {
				if leftType, ok := info.Nodes[selector.Left]; ok {
					receiver = typeinfo.DefaultPrinter.Type(leftType)
				}
			}
			name := selector.Name.Text()
			if receiver != "" {
				name = receiver + "::" + name
			}
			return appendHoverNote(appendHoverDoc(asFerretCodeBlock(typeinfo.DefaultPrinter.FuncSignature(name, fnType)), declarationDocForNode(node, mod)), ownershipNote)
		}
	}
	return appendHoverNote(appendHoverDoc(joinBindingAndTypeHover(bindingDecl, renderTypeHoverMarkdown(typ, mod, modulesByKey)), declarationDocForNode(node, mod)), ownershipNote)
}

func renderOwnershipBoundaryCastHoverNote(node ast.Node, info *typeinfo.ModuleInfo) string {
	cast, ok := node.(*ast.CastExpr)
	if !ok || cast == nil || info == nil {
		return ""
	}
	sourceType := info.Nodes[cast.Left]
	targetType := info.Nodes[cast.Type]
	if sourceType == nil || targetType == nil {
		return ""
	}
	if !isRawOwnerBoundaryCast(sourceType, targetType) {
		return ""
	}
	return ownershipBoundaryCastHoverNoteText()
}

func ownershipBoundaryCastHoverNoteText() string {
	return "_`^T` and `*T` are distinct ownership domains. Use `unsafe std/mem::Adopt(^T)`, `unsafe std/mem::Expose(*T)`, or `unsafe std/mem::ExposeRef(&*T)`._"
}

func isRawOwnerBoundaryCast(sourceType, targetType typeinfo.Type) bool {
	if sourceType == nil || targetType == nil {
		return false
	}
	_, srcRaw := sourceType.(*typeinfo.RawPtrType)
	_, srcOwner := sourceType.(*typeinfo.PointerType)
	_, dstRaw := targetType.(*typeinfo.RawPtrType)
	_, dstOwner := targetType.(*typeinfo.PointerType)
	return (srcRaw && dstOwner) || (srcOwner && dstRaw)
}

func renderNodeFunctionSignature(node ast.Node, typ typeinfo.Type, mod *context.Module, info *typeinfo.ModuleInfo) string {
	fnType, ok := typ.(*typeinfo.FuncType)
	if !ok || node == nil {
		return ""
	}
	if selector, ok := node.(*ast.SelectorExpr); ok && info != nil {
		if methodReceiver, ok := info.LookupMethodReceiver(selector); ok && methodReceiver != nil {
			if sym := resolvedSymbolForNode(mod, node); sym != nil {
				if sig := symbolFunctionSignature(sym, fnType); sig != "" {
					return sig
				}
			}
			name := selector.Name.Text()
			if baseNamed, ok := typeinfo.ReceiverBaseNamedType(methodReceiver); ok && baseNamed != nil {
				name = baseNamed.Name + "::" + name
			}
			return typeinfo.DefaultPrinter.MethodSignature(name, methodReceiver, fnType)
		}
	}
	if sym := resolvedSymbolForNode(mod, node); sym != nil {
		if sig := symbolFunctionSignature(sym, fnType); sig != "" {
			return sig
		}
		if sym.Name != "" {
			return typeinfo.DefaultPrinter.FuncSignature(sym.Name, fnType)
		}
	}
	switch n := node.(type) {
	case *ast.Ident:
		name := n.Text()
		if name != "" {
			return typeinfo.DefaultPrinter.FuncSignature(name, fnType)
		}
	case *ast.SelectorExpr:
		receiver := ""
		if info != nil && n.Left != nil {
			if leftType, ok := info.Nodes[n.Left]; ok {
				receiver = typeinfo.DefaultPrinter.Type(leftType)
			}
		}
		name := n.Name.Text()
		if receiver != "" {
			name = receiver + "::" + name
		}
		if name != "" {
			return typeinfo.DefaultPrinter.FuncSignature(name, fnType)
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
		if fnType != nil {
			return typeinfo.DefaultPrinter.FuncDeclSignature(fn, fnType)
		}
		return fn.Signature()
	}
	if fnType != nil && sym.Name != "" {
		return typeinfo.DefaultPrinter.FuncSignature(sym.Name, fnType)
	}
	return ""
}

func renderSymbolHoverMarkdown(sym *symbols.Symbol, typ typeinfo.Type, mod *context.Module, modulesByKey map[string]*context.Module) string {
	if sym == nil {
		return renderTypeHoverMarkdown(typ, mod, modulesByKey)
	}
	doc := declarationDocForSymbol(sym)
	if constrained := renderConstrainedTypeParamBindingHover(sym, typ); constrained != "" {
		return appendHoverDoc(constrained, doc)
	}
	if decl := renderBindingDeclForSymbol(sym, typ, mod); decl != "" {
		return appendHoverDoc(joinBindingAndTypeHover(decl, renderTypeHoverMarkdown(typ, mod, modulesByKey)), doc)
	}
	switch sym.Kind {
	case symbols.SymbolFunc, symbols.SymbolMethod:
		fnType, _ := typ.(*typeinfo.FuncType)
		if sig := symbolFunctionSignature(sym, fnType); sig != "" {
			return appendHoverDoc(asFerretCodeBlock(sig), doc)
		}
	case symbols.SymbolType:
		if named, ok := typ.(*typeinfo.NamedType); ok {
			return appendHoverDoc(renderNamedTypeMarkdown(named, mod, modulesByKey), doc)
		}
		if decl, ok := sym.Node.(*ast.TypeDecl); ok && decl != nil {
			named := &typeinfo.NamedType{Name: sym.Name, Decl: decl}
			if mod != nil {
				named.ModuleKey = mod.Key
			}
			return appendHoverDoc(renderNamedTypeMarkdown(named, mod, modulesByKey), doc)
		}
	}
	return appendHoverDoc(renderTypeHoverMarkdown(typ, mod, modulesByKey), doc)
}

func joinBindingAndTypeHover(bindingDecl, typeMarkdown string) string {
	bindingDecl = strings.TrimSpace(bindingDecl)
	if bindingDecl == "" {
		return typeMarkdown
	}
	bindingBlock := asFerretCodeBlock(bindingDecl)
	if strings.TrimSpace(typeMarkdown) == "" {
		return bindingBlock
	}
	return bindingBlock + "\n\n" + typeMarkdown
}

func renderConstrainedTypeParamBindingHover(sym *symbols.Symbol, typ typeinfo.Type) string {
	if sym == nil || sym.Kind != symbols.SymbolParam {
		return ""
	}
	param, ok := typ.(*typeinfo.TypeParam)
	if !ok || param == nil || param.Constraint == nil {
		return ""
	}
	name := sym.Name
	if name == "" {
		name = "_"
	}
	flags := ""
	if sym.Flags.Mutable() {
		flags += "mut "
	}
	typeName := typeinfo.DefaultPrinter.Type(param)
	constraint := typeinfo.DefaultPrinter.Type(param.Constraint)
	bindingLine := "(parameter) " + flags + name + ": " + typeName
	constraintLine := typeName + ": " + constraint
	return asFerretCodeBlock(bindingLine) + "\n\n" + asFerretCodeBlock(constraintLine)
}

func renderBindingDeclForNode(node ast.Node, typ typeinfo.Type, mod *context.Module) string {
	sym := resolvedSymbolForNode(mod, node)
	return renderBindingDeclForSymbol(sym, typ, mod)
}

func renderBindingDeclForSymbol(sym *symbols.Symbol, typ typeinfo.Type, mod *context.Module) string {
	if sym == nil || typ == nil {
		return ""
	}
	switch sym.Kind {
	case symbols.SymbolVar:
		flags := typeinfo.ValueFlags(0)
		if symbolIsMutable(sym) {
			flags |= typeinfo.FlagMutable
		}
		return typeinfo.DefaultPrinter.BindingDecl("let", sym.Name, typ, flags)
	case symbols.SymbolConst:
		return typeinfo.DefaultPrinter.BindingDecl("const", sym.Name, typ, 0)
	case symbols.SymbolParam:
		if kind, ok := receiverKindForSymbol(sym, typ, mod); ok {
			return typeinfo.DefaultPrinter.ReceiverBindingDecl(sym.Name, kind)
		}
		flags := typeinfo.ValueFlags(0)
		if sym.Flags.Mutable() {
			flags |= typeinfo.FlagMutable
		}
		return typeinfo.DefaultPrinter.BindingDecl("parameter", sym.Name, typ, flags)
	default:
		return ""
	}
}

func symbolIsMutable(sym *symbols.Symbol) bool {
	if sym == nil {
		return false
	}
	switch n := sym.Node.(type) {
	case *ast.LetStmt:
		return n.IsMut
	case *ast.LetDecl:
		return n.IsMut
	}
	return sym.Flags.Mutable()
}

func receiverKindForSymbol(sym *symbols.Symbol, typ typeinfo.Type, mod *context.Module) (typeinfo.ReceiverKind, bool) {
	if sym == nil {
		return typeinfo.ReceiverValue, false
	}
	if recv := findReceiverForSymbol(mod, sym); recv != nil {
		return receiverKindFromTypeExpr(recv.Type), true
	}
	return typeinfo.ReceiverValue, false
}

func findReceiverForSymbol(mod *context.Module, sym *symbols.Symbol) *ast.Receiver {
	if mod == nil || mod.AST == nil || sym == nil || sym.Location.Start == nil {
		return nil
	}
	for _, decl := range mod.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil || fn.Receiver == nil || fn.Receiver.Name == nil {
			continue
		}
		if fn.Receiver.Name.Text() != sym.Name {
			continue
		}
		if sameLocation(fn.Receiver.Name.Loc(), sym.Location) {
			return fn.Receiver
		}
	}
	return nil
}

func sameLocation(a, b source.Location) bool {
	if a.Start == nil || b.Start == nil {
		return false
	}
	if a.File != "" && b.File != "" && a.File != b.File {
		return false
	}
	return a.Start.Index == b.Start.Index
}

func receiverKindFromTypeExpr(typ ast.TypeExpr) typeinfo.ReceiverKind {
	switch t := typ.(type) {
	case *ast.RefType:
		if t.Mutable {
			return typeinfo.ReceiverRefMut
		}
		return typeinfo.ReceiverRef
	case *ast.PointerType:
		return typeinfo.ReceiverPtr
	case *ast.RawPtrType:
		return typeinfo.ReceiverRawPtr
	default:
		return typeinfo.ReceiverValue
	}
}

func renderTypeHoverMarkdown(typ typeinfo.Type, mod *context.Module, modulesByKey map[string]*context.Module) string {
	if typ == nil {
		return ""
	}
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return renderNamedTypeMarkdown(t, mod, modulesByKey)
	case *typeinfo.TypeParam:
		if t.Constraint != nil {
			return asFerretCodeBlock(typeinfo.DefaultPrinter.Type(t) + ": " + typeinfo.DefaultPrinter.Type(t.Constraint))
		}
		return asFerretCodeBlock(typeinfo.DefaultPrinter.Type(t))
	default:
		rendered := typeinfo.DefaultPrinter.Type(typ)
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
	block := typeinfo.NamedTypeHoverBlock{
		DeclText: "type " + named.String(),
	}
	if decl != nil {
		block.DeclText = renderNamedTypeDeclText(named, owner, decl)
	}

	instanceMethods, staticMethods, omittedMethods := collectTypeMethodSignatures(owner, named)
	block.InstanceMethods = instanceMethods
	block.StaticMethods = staticMethods
	if omittedMethods > 0 {
		block.TruncationNote = fmt.Sprintf("_Hover truncated: omitted %d additional method signature(s)._", omittedMethods)
	}
	return typeinfo.FormatNamedTypeHoverMarkdown(block)
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

func collectTypeMethodSignatures(mod *context.Module, named *typeinfo.NamedType) ([]string, []string, int) {
	if mod == nil || named == nil || named.Name == "" {
		return nil, nil, 0
	}
	typeName := named.Name
	instanceSet := make(map[string]struct{})
	staticSet := make(map[string]struct{})
	instance := make([]string, 0)
	static := make([]string, 0)

	if mod.MethodSets != nil {
		for receiver, methods := range mod.MethodSets {
			if receiver.TypeName != typeName {
				continue
			}
			for _, sym := range methods {
				sig := renderMethodSignatureForType(sym, mod, named)
				if sig == "" {
					continue
				}
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
			fn, ok := sym.Node.(*ast.FuncDecl)
			if !ok || !fn.IsStatic {
				continue
			}
			sig := renderMethodSignatureForType(sym, mod, named)
			if sig == "" {
				continue
			}
			if _, exists := staticSet[sig]; exists {
				continue
			}
			staticSet[sig] = struct{}{}
			static = append(static, sig)
		}
	}

	sort.Strings(instance)
	sort.Strings(static)
	instance, omittedInstance := trimHoverMethodSignatures(instance, maxHoverMethodsPerKind)
	static, omittedStatic := trimHoverMethodSignatures(static, maxHoverMethodsPerKind)
	return instance, static, omittedInstance + omittedStatic
}

func trimHoverMethodSignatures(methods []string, limit int) ([]string, int) {
	if limit <= 0 || len(methods) <= limit {
		return methods, 0
	}
	return methods[:limit], len(methods) - limit
}

func renderNamedTypeDeclText(named *typeinfo.NamedType, owner *context.Module, decl *ast.TypeDecl) string {
	if named == nil || decl == nil {
		return ""
	}
	typeName := named.Name
	if args := namedTypeArgText(named); len(args) > 0 {
		typeName += "<" + strings.Join(args, ", ") + ">"
	}
	resolved := declTypeForNamed(owner, named, decl)
	switch typeDecl := decl.Type.(type) {
	case *ast.StructType:
		if typeDecl == nil {
			return decl.Text()
		}
		typ, _ := resolved.(*typeinfo.StructType)
		if typ == nil {
			return decl.Text()
		}

		var b strings.Builder
		b.WriteString("type " + typeName + " struct {\n")
		for _, field := range typeDecl.Fields {
			if field == nil || field.Name == nil {
				continue
			}
			fieldType := ast.TypeString(field.Type)
			if concrete := structFieldTypeByName(typ, field.Name.Text()); concrete != nil {
				fieldType = typeinfo.DefaultPrinter.Type(concrete)
			}
			line := "    " + field.Name.Text() + ": " + fieldType
			if field.Default != nil {
				defaultValue := ast.ExprString(field.Default)
				if defaultValue == "" || defaultValue == "_" {
					defaultValue = "..."
				}
				line += " = " + defaultValue
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("}")
		return b.String()
	case *ast.InterfaceType:
		if typeDecl == nil {
			return decl.Text()
		}
		typ, _ := resolved.(*typeinfo.InterfaceType)
		if typ == nil {
			return decl.Text()
		}
		return typeinfo.DefaultPrinter.NamedDecl(typeName, typ)
	default:
		return decl.Text()
	}
}

func namedTypeArgText(named *typeinfo.NamedType) []string {
	if named == nil || len(named.TypeArgs) == 0 {
		return nil
	}
	args := make([]string, 0, len(named.TypeArgs))
	for _, arg := range named.TypeArgs {
		args = append(args, typeinfo.DefaultPrinter.Type(arg))
	}
	return args
}

func declTypeForNamed(owner *context.Module, named *typeinfo.NamedType, decl *ast.TypeDecl) typeinfo.Type {
	if owner == nil || owner.Types == nil || named == nil || decl == nil {
		return nil
	}
	base := owner.Types.Nodes[decl.Type]
	if base == nil {
		return nil
	}
	if bindings := typeinfo.OwnerTypeBindings(named); len(bindings) > 0 {
		base = typeinfo.InstantiateType(base, bindings)
	}
	return base
}

func structFieldTypeByName(typ *typeinfo.StructType, name string) typeinfo.Type {
	if typ == nil || typ.Fields == nil || name == "" {
		return nil
	}
	field := typ.Fields[name]
	if field == nil {
		return nil
	}
	return field.Type
}

func renderMethodSignatureForType(sym *symbols.Symbol, mod *context.Module, named *typeinfo.NamedType) string {
	fn, ok := sym.Node.(*ast.FuncDecl)
	if !ok || fn == nil {
		return ""
	}
	if mod != nil && mod.Types != nil {
		if fnType, ok := mod.Types.Symbols[sym.ID].(*typeinfo.FuncType); ok && fnType != nil {
			if bindings := typeinfo.OwnerTypeBindings(named); len(bindings) > 0 {
				fnType = typeinfo.InstantiateFuncType(fnType, bindings)
			}
			return typeinfo.DefaultPrinter.FuncDeclSignature(fn, fnType)
		}
	}
	return fn.Signature()
}

func appendHoverDoc(markdown, doc string) string {
	doc = formatHoverDoc(doc)
	if doc == "" {
		return markdown
	}
	if markdown == "" {
		return doc
	}
	return markdown + "\n\n" + doc
}

func appendHoverNote(markdown, note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return markdown
	}
	if strings.TrimSpace(markdown) == "" {
		return note
	}
	return markdown + "\n\n" + note
}

func declarationDocForNode(node ast.Node, mod *context.Module) string {
	if node == nil {
		return ""
	}
	if doc := declarationDocFromAstNode(node); doc != "" {
		return doc
	}
	if sym := resolvedSymbolForNode(mod, node); sym != nil {
		return declarationDocForSymbol(sym)
	}
	return ""
}

func declarationDocForSymbol(sym *symbols.Symbol) string {
	if sym == nil {
		return ""
	}
	return declarationDocFromAstNode(sym.Node)
}

func declarationDocForModule(mod *context.Module) string {
	if mod == nil || mod.AST == nil {
		return ""
	}
	return commentGroupText(mod.AST.Doc)
}

func declarationDocFromAstNode(node ast.Node) string {
	switch n := node.(type) {
	case *ast.FuncDecl:
		return commentGroupText(n.Doc)
	case *ast.TypeDecl:
		return commentGroupText(n.Doc)
	case *ast.ConstDecl:
		return commentGroupText(n.Doc)
	case *ast.LetDecl:
		return commentGroupText(n.Doc)
	case *ast.ConstStmt:
		return commentGroupText(n.Doc)
	case *ast.LetStmt:
		return commentGroupText(n.Doc)
	default:
		return ""
	}
}

func commentGroupText(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	return group.Text
}

func formatHoverDoc(doc string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	lines := strings.Split(doc, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "  \n")
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

func definitionFromIndex(index *hoverIndex, pos source.Position) (source.Location, bool) {
	if index == nil {
		return source.Location{}, false
	}
	best := source.Location{}
	bestSpan := int(^uint(0) >> 1)
	found := false

	consider := func(candidate, target source.Location) {
		if target.Start == nil || !locationContainsPosition(candidate, pos) {
			return
		}
		span := locationSpan(candidate)
		if !found || span < bestSpan {
			best = target
			bestSpan = span
			found = true
		}
	}

	for _, candidate := range index.definitionCandidates {
		consider(candidate.location, candidate.target)
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

func (s *Server) writeResponse(id json.RawMessage, result any) {
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

func (s *Server) writeNotification(method string, params any) {
	n := rpcNotification{JSONRPC: "2.0", Method: method, Params: params}
	s.write(n)
}

func (s *Server) write(v any) {
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

func decodeID(raw json.RawMessage) any {
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
	return filepath.Clean(filepath.FromSlash(fileURLPath(u))), nil
}

func pathToURI(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	u := &url.URL{Scheme: "file", Path: fileURIPath(abs)}
	return u.String(), nil
}

func fileURLPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.Host != "" && u.Host != "localhost" {
		return "//" + u.Host + u.Path
	}
	if hasWindowsDriveURIPath(u.Path) {
		return u.Path[1:]
	}
	return u.Path
}

func fileURIPath(path string) string {
	slash := filepath.ToSlash(path)
	if hasWindowsDrivePath(slash) {
		return "/" + slash
	}
	return slash
}

func hasWindowsDriveURIPath(path string) bool {
	return len(path) >= 4 && path[0] == '/' && isASCIILetter(path[1]) && path[2] == ':' && path[3] == '/'
}

func hasWindowsDrivePath(path string) bool {
	return len(path) >= 3 && isASCIILetter(path[0]) && path[1] == ':' && path[2] == '/'
}

func isASCIILetter(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
