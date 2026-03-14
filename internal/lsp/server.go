package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/driver"
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

type openDocument struct {
	Version int
	Text    string
}

type Server struct {
	in  *bufio.Reader
	out io.Writer

	mu             sync.Mutex
	rootURI        string
	isShuttingDown bool
	shouldExit     bool
	documents      map[string]openDocument
}

func Run(stdin io.Reader, stdout io.Writer) error {
	s := &Server{
		in:        bufio.NewReader(stdin),
		out:       stdout,
		documents: make(map[string]openDocument),
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
	s.documents[uri] = openDocument{Version: params.TextDocument.Version, Text: params.TextDocument.Text}
	s.mu.Unlock()

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
	s.documents[uri] = openDocument{Version: params.TextDocument.Version, Text: text}
	s.mu.Unlock()

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
	s.mu.Unlock()

	s.publishDiagnostics(params.TextDocument.URI, nil, nil)
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
