//go:build js && wasm
// +build js,wasm

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"syscall/js"

	"compiler/internal/core/diagnostics"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
)

type bridgeCapabilities struct {
	SyntaxAnalysis bool `json:"syntaxAnalysis"`
	RunnableWasm   bool `json:"runnableWasm"`
	MultiFile      bool `json:"multiFile"`
}

type bridgeDiagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	EndLine  int    `json:"endLine,omitempty"`
	EndCol   int    `json:"endColumn,omitempty"`
}

type compileResult struct {
	Success      bool                `json:"success"`
	Output       string              `json:"output,omitempty"`
	Error        string              `json:"error,omitempty"`
	Wasm         string              `json:"wasm,omitempty"`
	Diagnostics  []bridgeDiagnostic  `json:"diagnostics,omitempty"`
	Capabilities *bridgeCapabilities `json:"capabilities,omitempty"`
}

func main() {
	js.Global().Set("ferretWasmVersion", js.ValueOf(compiler.CompilerVersion+"-bridge"))
	js.Global().Set("ferretCompile", js.FuncOf(func(this js.Value, args []js.Value) any {
		caps := &bridgeCapabilities{
			SyntaxAnalysis: true,
			RunnableWasm:   true,
			MultiFile:      true,
		}

		if len(args) == 0 {
			return toJSResult(compileResult{
				Success:      false,
				Error:        "no source files provided",
				Capabilities: caps,
			})
		}

		files := map[string]string{}
		first := args[0]
		switch first.Type() {
		case js.TypeString:
			files["main.fer"] = first.String()
		case js.TypeObject:
			keys := js.Global().Get("Object").Call("keys", first)
			for i := 0; i < keys.Length(); i++ {
				k := keys.Index(i).String()
				files[k] = first.Get(k).String()
			}
		default:
			return toJSResult(compileResult{
				Success:      false,
				Error:        "invalid compile input; expected source string or file map",
				Capabilities: caps,
			})
		}

		if len(files) == 0 {
			return toJSResult(compileResult{
				Success:      false,
				Error:        "no source files provided",
				Capabilities: caps,
			})
		}

		synthesizedMainAlias := false
		if _, ok := files["main.fer"]; !ok {
			if mainFerr, ok2 := files["main.ferr"]; ok2 {
				files["main.fer"] = mainFerr
				synthesizedMainAlias = true
			}
		}

		if mainFerr, hasFerr := files["main.ferr"]; hasFerr {
			if mainFer, hasFer := files["main.fer"]; hasFer && mainFer == mainFerr {
				delete(files, "main.fer")
				synthesizedMainAlias = false
			}
		}

		if _, ok := files["main.fer"]; !ok {
			if _, okFerr := files["main.ferr"]; okFerr {
				// main.ferr is sufficient when aliases are deduplicated
			} else {
				return toJSResult(compileResult{
					Success:      false,
					Error:        "entry file main.fer (or main.ferr) not found in provided files",
					Capabilities: caps,
				})
			}
		}

		diag := diagnostics.NewBag()
		ferretFileCount := 0
		parsedModules := make([]*ast.Module, 0, len(files))
		seenContent := make(map[string]struct{}, len(files))
		for fileName, content := range files {
			if !strings.HasSuffix(fileName, ".fer") && !strings.HasSuffix(fileName, ".ferr") {
				continue
			}
			if synthesizedMainAlias && fileName == "main.fer" {
				continue
			}
			if _, exists := seenContent[fileName+"\x00"+content]; exists {
				continue
			}
			seenContent[fileName+"\x00"+content] = struct{}{}
			ferretFileCount++
			diag.AddSourceContent(fileName, content)
			toks := lexer.New(fileName, content, diag).Tokenize()
			parsedModules = append(parsedModules, parser.Parse(fileName, toks, diag))
		}

		diagnosticsOut := mapDiagnostics(diag.Diagnostics())
		if diag.HasErrors() {
			return toJSResult(compileResult{
				Success:      false,
				Error:        fmt.Sprintf("Compilation failed with %d error(s).", diag.ErrorCount()),
				Output:       "Bridge syntax analysis completed.",
				Diagnostics:  diagnosticsOut,
				Capabilities: caps,
			})
		}

		if ferretFileCount == 0 {
			return toJSResult(compileResult{
				Success:      false,
				Error:        "no .fer or .ferr source files provided",
				Capabilities: caps,
			})
		}

		wasmBytes, bridgeErr := emitMinimalMainWasm(parsedModules, diag)
		diagnosticsOut = mapDiagnostics(diag.Diagnostics())
		if bridgeErr != nil || diag.HasErrors() {
			msg := "Compilation failed with unsupported constructs for browser wasm bridge."
			if bridgeErr != nil {
				msg = bridgeErr.Error()
			}
			return toJSResult(compileResult{
				Success:      false,
				Output:       fmt.Sprintf("Syntax analysis passed for %d source file(s).", ferretFileCount),
				Error:        msg,
				Diagnostics:  diagnosticsOut,
				Capabilities: caps,
			})
		}

		return toJSResult(compileResult{
			Success:      true,
			Output:       fmt.Sprintf("Compiled minimal browser wasm from %d source file(s).", ferretFileCount),
			Wasm:         js.Global().Call("btoa", string(wasmBytes)).String(),
			Diagnostics:  diagnosticsOut,
			Capabilities: caps,
		})
	}))

	js.Global().Get("console").Call("log", "Ferret WASM bridge ready")
	select {}
}

func toJSResult(res compileResult) any {
	b, err := json.Marshal(res)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}
	}
	obj := js.Global().Get("JSON").Call("parse", string(b))
	return obj
}

func mapDiagnostics(all []*diagnostics.Diagnostic) []bridgeDiagnostic {
	out := make([]bridgeDiagnostic, 0, len(all))
	for _, d := range all {
		entry := bridgeDiagnostic{
			Severity: d.Severity.String(),
			Code:     d.Code,
			Message:  d.Message,
		}
		label := primaryOrFirstLabel(d)
		if label != nil && label.Location != nil {
			if label.Location.Filename != nil {
				entry.File = *label.Location.Filename
			}
			if label.Location.Start != nil {
				entry.Line = label.Location.Start.Line
				entry.Column = label.Location.Start.Column
			}
			if label.Location.End != nil {
				entry.EndLine = label.Location.End.Line
				entry.EndCol = label.Location.End.Column
			}
		}
		out = append(out, entry)
	}
	return out
}

func primaryOrFirstLabel(d *diagnostics.Diagnostic) *diagnostics.Label {
	if d == nil || len(d.Labels) == 0 {
		return nil
	}
	for i := range d.Labels {
		if d.Labels[i].Style == diagnostics.Primary {
			return &d.Labels[i]
		}
	}
	return &d.Labels[0]
}

func emitMinimalMainWasm(mods []*ast.Module, diag *diagnostics.Bag) ([]byte, error) {
	mainFns := make([]*ast.FuncDecl, 0, 1)
	for _, mod := range mods {
		if mod == nil {
			continue
		}
		for _, decl := range mod.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil || fn.Name.Last() != "main" {
				continue
			}
			mainFns = append(mainFns, fn)
		}
	}

	if len(mainFns) == 0 {
		diag.Add(diagnostics.NewError("missing entry function 'main' for wasm bridge").WithCode("WASM0001"))
		return nil, fmt.Errorf("missing entry function 'main'")
	}
	if len(mainFns) > 1 {
		for _, fn := range mainFns {
			addUnsupported(diag, fn, "multiple 'main' functions are not supported by the minimal wasm bridge", "WASM0002")
		}
		return nil, fmt.Errorf("multiple main functions found")
	}

	fn := mainFns[0]
	if fn.Receiver != nil {
		addUnsupported(diag, fn, "method receivers are not supported for bridge entrypoint", "WASM0003")
	}
	if fn.IsExtern || fn.Body == nil {
		addUnsupported(diag, fn, "entry function 'main' must have a body", "WASM0004")
	}
	if len(fn.Params) != 0 {
		addUnsupported(diag, fn, "entry function 'main' must not have parameters", "WASM0005")
	}

	resultKind, err := supportedResultKind(fn.Result)
	if err != nil {
		addUnsupported(diag, fn, err.Error(), "WASM0006")
	}

	if diag.HasErrors() {
		return nil, fmt.Errorf("unsupported bridge entrypoint")
	}

	body, err := compileMainBody(fn.Body, resultKind)
	if err != nil {
		addUnsupported(diag, fn.Body, err.Error(), "WASM0007")
		return nil, err
	}

	return encodeModule(resultKind, body), nil
}

func supportedResultKind(result ast.TypeExpr) (string, error) {
	if result == nil {
		return "void", nil
	}
	named, ok := result.(*ast.NamedType)
	if !ok || len(named.Path) != 1 {
		return "", fmt.Errorf("main return type must be void or i32 in minimal wasm bridge")
	}
	if named.Path[0] == "void" {
		return "void", nil
	}
	if named.Path[0] == "i32" {
		return "i32", nil
	}
	return "", fmt.Errorf("main return type '%s' is not supported yet; use void or i32", named.Path[0])
}

func compileMainBody(body *ast.BlockStmt, resultKind string) ([]byte, error) {
	if body == nil {
		return nil, fmt.Errorf("main body is required")
	}

	if resultKind == "void" {
		if len(body.Stmts) == 0 {
			return []byte{0x00, 0x0b}, nil
		}
		if len(body.Stmts) != 1 {
			return nil, fmt.Errorf("void main currently supports only an empty body or a single return")
		}
		ret, ok := body.Stmts[0].(*ast.ReturnStmt)
		if !ok {
			return nil, fmt.Errorf("void main currently supports only a return statement")
		}
		if ret.Value != nil {
			return nil, fmt.Errorf("void main must use 'return' without a value")
		}
		return []byte{0x00, 0x0b}, nil
	}

	if len(body.Stmts) == 0 {
		return nil, fmt.Errorf("i32 main must return a value")
	}

	locals := make(map[string]uint32)
	localCount := uint32(0)
	instr := make([]byte, 0, 64)

	for i, stmt := range body.Stmts {
		isLast := i == len(body.Stmts)-1
		switch s := stmt.(type) {
		case *ast.LetStmt:
			if isLast {
				return nil, fmt.Errorf("i32 main must end with a return statement")
			}
			if s.Name == nil || s.Name.Last() == "" {
				return nil, fmt.Errorf("let statements must have a valid binding name")
			}
			if s.Value == nil {
				return nil, fmt.Errorf("let statement '%s' requires an initializer", s.Name.Last())
			}
			exprCode, err := compileI32Expr(s.Value, locals)
			if err != nil {
				return nil, err
			}
			idx := localCount
			localCount++
			locals[s.Name.Last()] = idx
			instr = append(instr, exprCode...)
			instr = append(instr, 0x21)
			instr = append(instr, encodeU32(idx)...)

		case *ast.ReturnStmt:
			if !isLast {
				return nil, fmt.Errorf("return must be the last statement in minimal wasm bridge")
			}
			if s.Value == nil {
				return nil, fmt.Errorf("i32 main must return a value")
			}
			exprCode, err := compileI32Expr(s.Value, locals)
			if err != nil {
				return nil, err
			}
			instr = append(instr, exprCode...)

		default:
			return nil, fmt.Errorf("unsupported statement in minimal wasm bridge; only let and return are supported")
		}
	}

	payload := make([]byte, 0, 8+len(instr))
	if localCount == 0 {
		payload = append(payload, 0x00)
	} else {
		payload = append(payload, 0x01)
		payload = append(payload, encodeU32(localCount)...)
		payload = append(payload, 0x7f)
	}
	payload = append(payload, instr...)
	payload = append(payload, 0x0b)
	return payload, nil
}

func compileI32Expr(expr ast.Expr, locals map[string]uint32) ([]byte, error) {
	switch e := expr.(type) {
	case *ast.NumberLit:
		raw := strings.ReplaceAll(e.Value, "_", "")
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("only base-10 i32 literals are supported")
		}
		code := []byte{0x41}
		code = append(code, encodeS32(int32(n))...)
		return code, nil

	case *ast.Ident:
		name := e.Last()
		idx, ok := locals[name]
		if !ok {
			return nil, fmt.Errorf("unknown local '%s' in expression", name)
		}
		code := []byte{0x20}
		code = append(code, encodeU32(idx)...)
		return code, nil

	case *ast.PrefixExpr:
		if e.Op != "-" {
			return nil, fmt.Errorf("unsupported unary operator '%s'", e.Op)
		}
		right, err := compileI32Expr(e.Right, locals)
		if err != nil {
			return nil, err
		}
		code := make([]byte, 0, len(right)+8)
		code = append(code, right...)
		code = append(code, 0x41)
		code = append(code, encodeS32(-1)...)
		code = append(code, 0x6c)
		return code, nil

	case *ast.BinaryExpr:
		left, err := compileI32Expr(e.Left, locals)
		if err != nil {
			return nil, err
		}
		right, err := compileI32Expr(e.Right, locals)
		if err != nil {
			return nil, err
		}
		opcode, ok := i32BinaryOpcode(e.Op)
		if !ok {
			return nil, fmt.Errorf("unsupported binary operator '%s'; supported: +, -, *, /, %%", e.Op)
		}
		code := make([]byte, 0, len(left)+len(right)+1)
		code = append(code, left...)
		code = append(code, right...)
		code = append(code, opcode)
		return code, nil

	default:
		return nil, fmt.Errorf("unsupported expression in minimal wasm bridge")
	}
}

func encodeModule(resultKind string, fnBody []byte) []byte {
	var out bytes.Buffer
	out.Write([]byte{0x00, 0x61, 0x73, 0x6d})
	out.Write([]byte{0x01, 0x00, 0x00, 0x00})

	resultTypes := []byte{}
	if resultKind == "i32" {
		resultTypes = []byte{0x01, 0x7f}
	} else {
		resultTypes = []byte{0x00}
	}
	typePayload := append([]byte{0x01, 0x60, 0x00}, resultTypes...)
	writeSection(&out, 0x01, typePayload)

	funcPayload := []byte{0x01, 0x00}
	writeSection(&out, 0x03, funcPayload)

	exportName := []byte("main")
	exportPayload := []byte{0x01, byte(len(exportName))}
	exportPayload = append(exportPayload, exportName...)
	exportPayload = append(exportPayload, 0x00, 0x00)
	writeSection(&out, 0x07, exportPayload)

	bodyPayload := append([]byte(nil), fnBody...)
	codePayload := []byte{0x01}
	codePayload = append(codePayload, encodeU32(uint32(len(bodyPayload)))...)
	codePayload = append(codePayload, bodyPayload...)
	writeSection(&out, 0x0a, codePayload)

	return out.Bytes()
}

func writeSection(out *bytes.Buffer, id byte, payload []byte) {
	out.WriteByte(id)
	out.Write(encodeU32(uint32(len(payload))))
	out.Write(payload)
}

func encodeU32(v uint32) []byte {
	buf := make([]byte, 0, 5)
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if v == 0 {
			break
		}
	}
	return buf
}

func encodeS32(v int32) []byte {
	buf := make([]byte, 0, 5)
	more := true
	for more {
		b := byte(v & 0x7f)
		v >>= 7
		signBit := b & 0x40
		if (v == 0 && signBit == 0) || (v == -1 && signBit != 0) {
			more = false
		} else {
			b |= 0x80
		}
		buf = append(buf, b)
	}
	return buf
}

func addUnsupported(diag *diagnostics.Bag, loc ast.Node, msg, code string) {
	if loc == nil {
		diag.Add(diagnostics.NewError(msg).WithCode(code))
		return
	}
	l := loc.Loc()
	if l.Start == nil || l.End == nil {
		diag.Add(diagnostics.NewError(msg).WithCode(code))
		return
	}
	copyLoc := l
	diag.Add(diagnostics.NewError(msg).WithCode(code).WithPrimaryLabel(&copyLoc, "not supported in minimal wasm bridge"))
}

func i32BinaryOpcode(op string) (byte, bool) {
	switch op {
	case "+":
		return 0x6a, true
	case "-":
		return 0x6b, true
	case "*":
		return 0x6c, true
	case "/":
		return 0x6d, true
	case "%":
		return 0x6f, true
	default:
		return 0, false
	}
}
