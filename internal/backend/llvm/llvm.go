package llvm

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"compiler/internal/backend"
	"compiler/internal/layout"
	midmir "compiler/internal/middleend/mir"
	"compiler/internal/semantics/typeinfo"
)

type lowerer struct{}

type moduleState struct {
	mod          *midmir.Module
	layout       *layout.Module
	layouts      map[string]*layout.Module
	fn           *midmir.Function
	functions    map[string]struct{}
	globals      map[string]struct{}
	modulePrefix string
	aggLocals    map[int]*aggregateLocal
	aggParams    map[int]struct{}
	scalarLocals map[int]*scalarAllocaLocal // mutable scalar locals mapped to alloca ptrs
	nextTemp     int
	nextStrConst int              // counter for unnamed string constant globals
	deferredB    *strings.Builder // deferred global definitions (e.g. string literals used in functions)
	pendingLines []string         // extra load instructions to flush before each emitted line
	debug        *debugState      // nil if debug info is disabled
	fnScopeID    int              // DISubprogram metadata ID for the current function
}

// debugState accumulates LLVM debug-info metadata nodes (DWARF) while
// lowering a program.  A single debugState is shared across all modules
// lowered in one LowerProgram call so metadata IDs are globally unique.
type debugState struct {
	nextID         int
	nodes          []string       // collected "!N = ..." lines in emission order
	cuIDs          []int          // all DICompileUnit IDs (one per module)
	fileIDs        map[string]int // abs file path → DIFile metadata ID
	subroutineType int            // cached !DISubroutineType(types: !{null}) ID; -1 = not yet emitted
}

func newDebugState() *debugState {
	return &debugState{
		nextID:         0,
		fileIDs:        make(map[string]int),
		subroutineType: -1,
	}
}

// emit appends a metadata node definition "!N = <node>" and returns its ID.
func (d *debugState) emit(node string) int {
	id := d.nextID
	d.nextID++
	d.nodes = append(d.nodes, fmt.Sprintf("!%d = %s", id, node))
	return id
}

// getFile returns (creating if needed) the DIFile metadata ID for a source file.
func (d *debugState) getFile(absPath string) int {
	if id, ok := d.fileIDs[absPath]; ok {
		return id
	}
	name := filepath.Base(absPath)
	dir := filepath.Dir(absPath)
	id := d.emit(fmt.Sprintf("!DIFile(filename: %q, directory: %q)", name, dir))
	d.fileIDs[absPath] = id
	return id
}

// addCU creates a DICompileUnit for the given source file and returns its ID.
func (d *debugState) addCU(fileID int) int {
	cuID := d.emit(fmt.Sprintf(
		`distinct !DICompileUnit(language: DW_LANG_C, file: !%d, producer: "ferret", isOptimized: false, runtimeVersion: 0, emissionKind: FullDebug)`,
		fileID))
	d.cuIDs = append(d.cuIDs, cuID)
	return cuID
}

// getSubroutineType returns (creating once) a generic !DISubroutineType for
// all functions (using a null type list, i.e. no detailed argument types).
func (d *debugState) getSubroutineType() int {
	if d.subroutineType >= 0 {
		return d.subroutineType
	}
	nullList := d.emit("!{null}")
	d.subroutineType = d.emit(fmt.Sprintf("!DISubroutineType(types: !%d)", nullList))
	return d.subroutineType
}

// addSubprogram creates a DISubprogram node for a function.
func (d *debugState) addSubprogram(humanName, linkName string, fileID, cuID, line int) int {
	stID := d.getSubroutineType()
	return d.emit(fmt.Sprintf(
		`distinct !DISubprogram(name: %q, linkageName: %q, scope: !%d, file: !%d, line: %d, type: !%d, isLocal: false, isDefinition: true, scopeLine: %d, flags: DIFlagPrototyped, spFlags: DISPFlagDefinition, unit: !%d)`,
		humanName, linkName, fileID, fileID, line, stID, line, cuID))
}

// addLocation creates a DILocation node referencing the given scope.
func (d *debugState) addLocation(line, col, scopeID int) int {
	return d.emit(fmt.Sprintf("!DILocation(line: %d, column: %d, scope: !%d)", line, col, scopeID))
}

// appendDebugSuffix appends ", !dbg !N" to every non-empty, non-comment line
// in a (possibly multi-line) LLVM IR instruction string.
func appendDebugSuffix(ir string, suffix string) string {
	parts := strings.Split(ir, "\n")
	for i, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts[i] = p + suffix
	}
	return strings.Join(parts, "\n")
}

// scalarAllocaLocal tracks an alloca-backed scalar local variable.
// Unlike aggregate locals (which are always pointers), scalar locals
// use alloca only to support reassignment (LLVM requires SSA).
type scalarAllocaLocal struct {
	ID         int
	Name       string
	AllocaName string // e.g., "%x_alloca"
	IRType     string // e.g., "i32"
}

type aggregateLocal struct {
	ID      int
	Name    string
	Type    typeinfo.Type
	Size    int64
	Align   int64
	PtrName string
}

func New() backend.Lowerer { return &lowerer{} }

func (*lowerer) Target() backend.Target { return backend.TargetLLVM }

// LowerProgram lowers all units into a single self-contained LLVM IR string.
// All named type declarations are collected and deduplicated first so they
// precede every function body — a hard requirement of the LLVM IR format.
// Use this instead of calling LowerModule per-module and concatenating.
func LowerProgram(units []*backend.Unit) (string, error) {
	// Build a merged layouts map so every unit can resolve cross-module types.
	allLayouts := make(map[string]*layout.Module)
	for _, u := range units {
		if u != nil && u.Layout != nil {
			allLayouts[u.Module.Key] = u.Layout
		}
	}

	dbg := newDebugState()

	// Pass 1: collect all type declarations in dependency order (units are
	// already ordered imports-before-entry by the caller).
	seenTypes := make(map[string]struct{})
	var typeLines []string

	for _, unit := range units {
		if unit == nil || unit.Module == nil {
			continue
		}
		state := newModuleStateWithDebug(unit, allLayouts, dbg)
		for _, decl := range unit.Module.Types {
			if decl == nil || decl.Named == nil {
				continue
			}
			key := decl.Named.ModuleKey + "::" + decl.Name
			if _, seen := seenTypes[key]; seen {
				continue
			}
			line, err := lowerTypeDecl(state, decl)
			if err != nil {
				return "", fmt.Errorf("lower program types [%s]: %w", unit.Module.ImportPath, err)
			}
			if line == "" {
				continue
			}
			seenTypes[key] = struct{}{}
			typeLines = append(typeLines, line)
		}
	}

	// Collect unique extern function declarations across all modules.
	// MIR extern functions have no Params (body is nil), so we emit
	// variadic declares: `declare rettype @sym(...)`. LLVM accepts calls
	// to vararg functions with any argument types, while still resolving
	// the symbol to the correct C library function at link time.
	seenExterns := make(map[string]struct{})
	var declLines []string
	for _, decl := range implicitExternDecls() {
		sym := implicitExternSymbol(decl)
		if sym == "" {
			continue
		}
		seenExterns[sym] = struct{}{}
		declLines = append(declLines, decl)
	}
	for _, unit := range units {
		if unit == nil || unit.Module == nil {
			continue
		}
		// We need a temporary state only to resolve return types.
		tmpState := newModuleStateWithDebug(unit, allLayouts, dbg)
		for _, fn := range unit.Module.Functions {
			if fn == nil || !fn.IsExtern || fn.ExternName == "" {
				continue
			}
			sym := sanitizeIdent(fn.ExternName)
			if _, seen := seenExterns[sym]; seen {
				continue
			}
			seenExterns[sym] = struct{}{}
			// Determine return type string.
			var retStr string
			if isAggregateType(tmpState, fn.Result) {
				if tn, err := llvmABITypeName(tmpState, fn.Result); err == nil {
					retStr = tn
				} else {
					retStr = "ptr"
				}
			} else {
				if rt, err := llvmBaseType(fn.Result); err == nil {
					retStr = rt
				} else {
					retStr = "ptr"
				}
			}
			// Use variadic signature so we don't need param type info.
			declLines = append(declLines, fmt.Sprintf("declare %s @%s(...)", retStr, sym))
		}
	}

	// Always declare Ferret runtime functions used by compiler-emitted code.
	declLines = append([]string{
		"declare void @ferret__panic(ptr)",
		"declare void @ferret__interface_panic(ptr, ptr)",
		"declare void @global__panic(ptr)",
		"declare { ptr, i64 } @global__recover()",
		"declare void @global__print_str(ptr)",
		"declare void @global__print_bool(i8)",
		"declare void @global__print_i64(i64)",
		"declare void @global__print_u64(i64)",
		"declare void @global__print_f64(double)",
		"declare void @global__print_char(i32)",
		"declare void @global__print_ptr(ptr)",
		"declare void @global__print_type(ptr)",
		"declare ptr @global__str_data(ptr)",
		"declare i64 @global__str_len(ptr)",
		"declare { ptr, i64 } @global__str_bytes(ptr)",
		"declare { ptr, i64 } @global__bytes_str(ptr)",
		"declare { ptr, i64 } @global__str_chars(ptr)",
		"declare { ptr, i64 } @global__chars_str(ptr)",
		"declare ptr @global__str_cstr(ptr)",
		"declare { ptr, i64 } @global__i64_str(i64)",
		"declare { ptr, i64 } @global__u64_str(i64)",
		"declare { ptr, i64 } @global__f64_str(double)",
	}, declLines...)

	// Pass 2: lower globals and functions for every unit, now with debug info.
	var b strings.Builder
	fmt.Fprintf(&b, "; generated by ferret llvm backend\n")

	for _, line := range typeLines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(declLines) > 0 {
		b.WriteByte('\n')
		for _, d := range declLines {
			b.WriteString(d)
			b.WriteByte('\n')
		}
	}
	for _, unit := range units {
		if unit == nil || unit.Module == nil || unit.Layout == nil {
			continue
		}
		state := newModuleStateWithDebug(unit, allLayouts, dbg)

		// Create one DICompileUnit per module source file.
		if unit.Module.FilePath != "" {
			fileID := dbg.getFile(unit.Module.FilePath)
			state.debug.addCU(fileID)
		}

		if err := emitGlobals(&b, state, unit.Module.Globals); err != nil {
			return "", fmt.Errorf("lower program globals [%s]: %w", unit.Module.ImportPath, err)
		}
		for _, fn := range unit.Module.Functions {
			if fn == nil || fn.IsBuiltin || fn.IsExtern {
				continue
			}
			// Capture function IR into a temp buffer so that any string constants
			// accumulated in deferredB during lowering can be emitted BEFORE the
			// function body (LLVM IR requires globals to be defined before use).
			var fnB strings.Builder
			if err := emitFunction(&fnB, state, fn); err != nil {
				return "", fmt.Errorf("lower program function %s [%s]: %w", fn.Name, unit.Module.ImportPath, err)
			}
			b.WriteByte('\n')
			if state.deferredB.Len() > 0 {
				b.WriteString(state.deferredB.String())
				state.deferredB.Reset()
				b.WriteByte('\n')
			}
			b.WriteString(fnB.String())
		}
	}

	// Emit DWARF debug metadata section.
	if len(dbg.cuIDs) > 0 {
		// Module flags required by LLVM for DWARF.
		dwarfVerID := dbg.emit("!{i32 7, !\"Dwarf Version\", i32 4}")
		dbgVerID := dbg.emit("!{i32 2, !\"Debug Info Version\", i32 3}")

		b.WriteString("\n; debug metadata\n")
		// !llvm.dbg.cu references all compile units.
		cuRefs := make([]string, 0, len(dbg.cuIDs))
		for _, id := range dbg.cuIDs {
			cuRefs = append(cuRefs, fmt.Sprintf("!%d", id))
		}
		fmt.Fprintf(&b, "!llvm.dbg.cu = !{%s}\n", strings.Join(cuRefs, ", "))
		fmt.Fprintf(&b, "!llvm.module.flags = !{!%d, !%d}\n", dwarfVerID, dbgVerID)
		for _, node := range dbg.nodes {
			b.WriteString(node)
			b.WriteByte('\n')
		}
	}

	return b.String(), nil
}

func implicitExternDecls() []string {
	return []string{
		"declare ptr @malloc(i64)",
		"declare void @free(ptr)",
		"declare ptr @realloc(ptr, i64)",
		"declare ptr @calloc(i64, i64)",
		"declare ptr @memcpy(ptr, ptr, i64)",
		"declare ptr @memmove(ptr, ptr, i64)",
		"declare ptr @memset(ptr, i32, i64)",
		"declare i32 @memcmp(ptr, ptr, i64)",
		"declare void @exit(i32)",
	}
}

func implicitExternSymbol(decl string) string {
	start := strings.IndexByte(decl, '@')
	if start < 0 {
		return ""
	}
	start++
	end := strings.IndexByte(decl[start:], '(')
	if end < 0 {
		return ""
	}
	return decl[start : start+end]
}

// llvmExternDecl builds a "declare" line for an extern function.
func llvmExternDecl(state *moduleState, fn *midmir.Function) (string, error) {
	sym := sanitizeIdent(fn.ExternName)

	var retStr string
	if fn.Result == nil || isVoidType(fn.Result) {
		retStr = "void"
	} else if isAggregateType(state, fn.Result) {
		t, err := llvmABITypeName(state, fn.Result)
		if err != nil {
			return "", err
		}
		retStr = t
	} else {
		t, err := llvmBaseType(fn.Result)
		if err != nil {
			return "", err
		}
		retStr = t
	}

	paramParts := make([]string, 0, len(fn.Params))
	for _, param := range fn.Params {
		if param == nil {
			continue
		}
		if isAggregateType(state, param.Type) {
			t, err := llvmABITypeName(state, param.Type)
			if err != nil {
				return "", err
			}
			_, align, err := aggregateSizeAlign(state, param.Type)
			if err != nil {
				return "", err
			}
			paramParts = append(paramParts, fmt.Sprintf("ptr byval(%s) align %d", t, align))
		} else {
			t, err := llvmBaseType(param.Type)
			if err != nil {
				return "", err
			}
			paramParts = append(paramParts, t)
		}
	}
	return fmt.Sprintf("declare %s @%s(%s)", retStr, sym, strings.Join(paramParts, ", ")), nil
}

// newModuleStateWithDebug constructs a moduleState that shares the given
// debug state, enabling program-wide metadata accumulation.
func newModuleStateWithDebug(unit *backend.Unit, allLayouts map[string]*layout.Module, dbg *debugState) *moduleState {
	state := newModuleState(unit, allLayouts)
	state.debug = dbg
	return state
}

// newModuleState constructs a moduleState for the given unit, using allLayouts
// as the cross-module layout map.
func newModuleState(unit *backend.Unit, allLayouts map[string]*layout.Module) *moduleState {
	state := &moduleState{
		mod:          unit.Module,
		layout:       unit.Layout,
		layouts:      allLayouts,
		functions:    make(map[string]struct{}),
		globals:      make(map[string]struct{}),
		modulePrefix: sanitizePath(unit.Module.ImportPath),
		deferredB:    &strings.Builder{},
	}
	for _, fn := range unit.Module.Functions {
		if fn != nil {
			state.functions[fn.Name] = struct{}{}
		}
	}
	for _, g := range unit.Module.Globals {
		if g != nil {
			state.globals[g.Name] = struct{}{}
		}
	}
	return state
}

func (*lowerer) LowerModule(unit *backend.Unit) (*backend.Artifact, error) {
	if err := backend.ValidateUnit(unit); err != nil {
		return nil, err
	}
	state := newModuleState(unit, unit.Layouts)

	var b strings.Builder
	fmt.Fprintf(&b, "; generated by ferret llvm backend\n\n")
	// Always declare Ferret runtime functions used by compiler-emitted code.
	b.WriteString("declare void @ferret__panic(ptr)\n")
	b.WriteString("declare void @ferret__interface_panic(ptr, ptr)\n")
	b.WriteString("declare void @global__panic(ptr)\n")
	b.WriteString("declare { ptr, i64 } @global__recover()\n")
	b.WriteString("declare ptr @global__str_data(ptr)\n")
	b.WriteString("declare i64 @global__str_len(ptr)\n")
	b.WriteString("declare { ptr, i64 } @global__str_bytes(ptr)\n")
	b.WriteString("declare { ptr, i64 } @global__bytes_str(ptr)\n")
	b.WriteString("declare { ptr, i64 } @global__str_chars(ptr)\n")
	b.WriteString("declare { ptr, i64 } @global__chars_str(ptr)\n")
	b.WriteString("declare ptr @global__str_cstr(ptr)\n")
	b.WriteString("declare { ptr, i64 } @global__i64_str(i64)\n")
	b.WriteString("declare { ptr, i64 } @global__u64_str(i64)\n")
	b.WriteString("declare { ptr, i64 } @global__f64_str(double)\n")
	for _, decl := range implicitExternDecls() {
		b.WriteString(decl)
		b.WriteByte('\n')
	}
	seenExterns := make(map[string]struct{})
	for _, fn := range unit.Module.Functions {
		if fn == nil || !fn.IsExtern || fn.ExternName == "" {
			continue
		}
		sym := sanitizeIdent(fn.ExternName)
		if _, ok := seenExterns[sym]; ok {
			continue
		}
		decl, err := llvmExternDecl(state, fn)
		if err != nil {
			return nil, err
		}
		seenExterns[sym] = struct{}{}
		b.WriteString(decl)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	if err := emitTypes(&b, state, unit.Module.Types); err != nil {
		return nil, err
	}
	if len(unit.Module.Types) > 0 && (len(unit.Module.Globals) > 0 || len(unit.Module.Functions) > 0) {
		b.WriteByte('\n')
	}
	if err := emitGlobals(&b, state, unit.Module.Globals); err != nil {
		return nil, err
	}
	if len(unit.Module.Globals) > 0 && len(unit.Module.Functions) > 0 {
		b.WriteByte('\n')
	}
	written := 0
	for _, fn := range unit.Module.Functions {
		if fn == nil || fn.IsBuiltin || fn.IsExtern {
			continue
		}
		if written > 0 {
			b.WriteByte('\n')
		}
		// Capture function IR into a temp buffer so that any string constants
		// accumulated in deferredB during lowering can be emitted BEFORE the
		// function body (LLVM IR requires globals to be defined before use).
		var fnB strings.Builder
		if err := emitFunction(&fnB, state, fn); err != nil {
			return nil, err
		}
		if state.deferredB.Len() > 0 {
			b.WriteString(state.deferredB.String())
			state.deferredB.Reset()
			b.WriteByte('\n')
		}
		b.WriteString(fnB.String())
		written++
	}
	return &backend.Artifact{
		Target:    backend.TargetLLVM,
		ModuleKey: unit.Module.Key,
		FileExt:   ".ll",
		Text:      b.String(),
	}, nil
}

// ---------------------------------------------------------------------------
// Type declarations
// ---------------------------------------------------------------------------

func emitTypes(b *strings.Builder, state *moduleState, types []*midmir.TypeDecl) error {
	seen := make(map[string]struct{})
	for _, decl := range types {
		if decl == nil || decl.Named == nil || decl.Struct == nil {
			continue
		}
		key := decl.Named.ModuleKey + "::" + decl.Name
		if _, ok := seen[key]; ok {
			continue
		}
		line, err := lowerTypeDecl(state, decl)
		if err != nil {
			return err
		}
		if line == "" {
			continue
		}
		seen[key] = struct{}{}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return nil
}

func lowerTypeDecl(state *moduleState, decl *midmir.TypeDecl) (string, error) {
	if decl == nil || decl.Named == nil || decl.Struct == nil {
		return "", nil
	}
	layoutInfo, err := lookupNamedLayout(state, decl.Named)
	if err != nil {
		return "", err
	}
	if layoutInfo == nil || layoutInfo.Struct == nil || !layoutInfo.Known {
		return "", fmt.Errorf("type %s: unknown struct layout", decl.Name)
	}
	body, err := llvmStructBody(state, layoutInfo.Struct)
	if err != nil {
		return "", fmt.Errorf("type %s: %w", decl.Name, err)
	}
	return fmt.Sprintf("%%%s = type { %s }", llvmTypeName(state, decl.Named), body), nil
}

// ---------------------------------------------------------------------------
// Globals
// ---------------------------------------------------------------------------

func emitGlobals(b *strings.Builder, state *moduleState, globals []*midmir.Global) error {
	wrote := 0
	for _, g := range globals {
		if g == nil {
			continue
		}
		line, err := lowerGlobal(state, g)
		if err != nil {
			return err
		}
		if line == "" {
			continue
		}
		if wrote > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		b.WriteByte('\n')
		wrote++
	}
	return nil
}

func lowerGlobal(state *moduleState, g *midmir.Global) (string, error) {
	if g.Init == nil {
		return "", nil
	}
	name := llvmSymbol(state, []string{g.Name})
	switch v := g.Init.(type) {
	case *midmir.CompositeValue:
		body, err := lowerGlobalComposite(state, g.Type, v)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		typeName, err := llvmABITypeName(state, g.Type)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		return fmt.Sprintf("@%s = global %s { %s }", name, typeName, body), nil
	case *midmir.NumberValue:
		irType, err := llvmBaseType(g.Type)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		lit, err := llvmNumberLiteral(g.Type, v.Value)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		return fmt.Sprintf("@%s = global %s %s", name, irType, lit), nil
	case *midmir.BoolValue:
		lit := "0"
		if v.Value {
			lit = "1"
		}
		return fmt.Sprintf("@%s = global i8 %s", name, lit), nil
	case *midmir.StringValue:
		escaped := llvmStringLiteral(v.Value)
		length := len(v.Value) + 1
		return fmt.Sprintf("@%s = private unnamed_addr constant [%d x i8] %s", name, length, escaped), nil
	default:
		return "", fmt.Errorf("global %s: unsupported initializer %T", g.Name, g.Init)
	}
}

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

func emitFunction(b *strings.Builder, state *moduleState, fn *midmir.Function) error {
	if err := prepareFunctionState(state, fn); err != nil {
		return err
	}

	name := llvmSymbol(state, []string{fn.Name})

	// Build return type string.
	var retStr string
	if isAggregateType(state, fn.Result) {
		typeName, err := llvmABITypeName(state, fn.Result)
		if err != nil {
			return fmt.Errorf("function %s result: %w", fn.Name, err)
		}
		retStr = typeName
	} else {
		rt, err := llvmBaseType(fn.Result)
		if err != nil {
			return fmt.Errorf("function %s result: %w", fn.Name, err)
		}
		retStr = rt
	}

	// Build parameter list.
	paramParts := make([]string, 0, len(fn.Params))
	for _, param := range fn.Params {
		if param == nil {
			continue
		}
		if isAggregateType(state, param.Type) {
			typeName, err := llvmABITypeName(state, param.Type)
			if err != nil {
				return fmt.Errorf("function %s param %s: %w", fn.Name, param.Name, err)
			}
			_, align, err := aggregateSizeAlign(state, param.Type)
			if err != nil {
				return fmt.Errorf("function %s param %s: %w", fn.Name, param.Name, err)
			}
			paramParts = append(paramParts, fmt.Sprintf("ptr byval(%s) align %d %s", typeName, align, llvmLocalName(param.Name)))
		} else {
			pty, err := llvmBaseType(param.Type)
			if err != nil {
				return fmt.Errorf("function %s param %s: %w", fn.Name, param.Name, err)
			}
			paramParts = append(paramParts, fmt.Sprintf("%s %s", pty, llvmLocalName(param.Name)))
		}
	}

	// Attach DWARF subprogram metadata if debug state is active.
	dbgSuffix := ""
	if state.debug != nil && fn.Location.File != "" {
		fileID := state.debug.getFile(fn.Location.File)
		// Re-use the last added CU or add one for this file.
		cuID := -1
		if len(state.debug.cuIDs) > 0 {
			cuID = state.debug.cuIDs[len(state.debug.cuIDs)-1]
		} else {
			cuID = state.debug.addCU(fileID)
		}
		line := 1
		if fn.Location.Start != nil {
			line = fn.Location.Start.Line
		}
		state.fnScopeID = state.debug.addSubprogram(fn.Name, name, fileID, cuID, line)
		dbgSuffix = fmt.Sprintf(" !dbg !%d", state.fnScopeID)
	}

	if retStr == "void" {
		fmt.Fprintf(b, "define void @%s(%s)%s {\n", name, strings.Join(paramParts, ", "), dbgSuffix)
	} else {
		fmt.Fprintf(b, "define %s @%s(%s)%s {\n", retStr, name, strings.Join(paramParts, ", "), dbgSuffix)
	}

	// Sort blocks by ID, entry block first.
	blocks := append([]*midmir.Block(nil), fn.Blocks...)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].ID < blocks[j].ID })

	for _, block := range blocks {
		if block == nil {
			continue
		}
		label := llvmBlockLabel(fn, block.ID)
		fmt.Fprintf(b, "%s:\n", label)

		// Entry prelude: alloca for non-param aggregate locals.
		if block.ID == fn.EntryID {
			for _, line := range entryPrelude(state) {
				fmt.Fprintf(b, "  %s\n", line)
			}
		}

		for _, instr := range block.Instructions {
			state.pendingLines = nil
			line, err := lowerInstr(state, instr)
			if err != nil {
				return fmt.Errorf("function %s block %d: %w", fn.Name, block.ID, err)
			}
			// Determine !dbg suffix for this instruction.
			instrDbg := ""
			if state.debug != nil && state.fnScopeID >= 0 {
				loc := instr.Loc()
				srcLine, srcCol := 1, 1
				if loc.Start != nil {
					srcLine = loc.Start.Line
					srcCol = loc.Start.Column
				}
				locID := state.debug.addLocation(srcLine, srcCol, state.fnScopeID)
				instrDbg = fmt.Sprintf(", !dbg !%d", locID)
			}
			// Flush loads emitted by lowerValue (pendingLines) before the instruction.
			for _, pl := range state.pendingLines {
				fmt.Fprintf(b, "  %s%s\n", pl, instrDbg)
			}
			if line == "" {
				continue
			}
			for _, part := range strings.Split(line, "\n") {
				if part != "" {
					fmt.Fprintf(b, "  %s%s\n", part, instrDbg)
				}
			}
		}

		state.pendingLines = nil
		term, err := lowerTerm(state, block.Terminator)
		if err != nil {
			return fmt.Errorf("function %s block %d: %w", fn.Name, block.ID, err)
		}
		termDbg := ""
		if state.debug != nil && state.fnScopeID >= 0 && block.Terminator != nil {
			loc := block.Terminator.Loc()
			srcLine, srcCol := 1, 1
			if loc.Start != nil {
				srcLine = loc.Start.Line
				srcCol = loc.Start.Column
			}
			locID := state.debug.addLocation(srcLine, srcCol, state.fnScopeID)
			termDbg = fmt.Sprintf(", !dbg !%d", locID)
		}
		for _, pl := range state.pendingLines {
			fmt.Fprintf(b, "  %s%s\n", pl, termDbg)
		}
		for _, part := range strings.Split(term, "\n") {
			if part != "" {
				fmt.Fprintf(b, "  %s%s\n", part, termDbg)
			}
		}
	}

	b.WriteString("}\n")
	return nil
}

// ---------------------------------------------------------------------------
// Instructions
// ---------------------------------------------------------------------------

func lowerInstr(state *moduleState, instr midmir.Instr) (string, error) {
	switch i := instr.(type) {
	case nil:
		return "", nil
	case *midmir.BindInstr:
		return lowerAssignLike(state, i.Name, i.Type, i.Value)
	case *midmir.AssignInstr:
		name := localNameByID(state.fn, i.TargetID)
		return lowerAssignLike(state, name, localTypeByID(state.fn, i.TargetID), i.Value)
	case *midmir.ComputeInstr:
		name := localNameByID(state.fn, i.TargetID)
		return lowerAssignLike(state, name, i.Type, i.Value)
	case *midmir.StoreFieldInstr:
		return lowerStoreField(state, i)
	case *midmir.StoreInstr:
		return lowerStorePlace(state, i)
	case *midmir.EvalInstr:
		if call, ok := i.Value.(*midmir.CallValue); ok {
			return lowerCall(state, "", nil, call)
		}
		return "", nil
	case *midmir.DeferInstr:
		// Defers are compile-time cleanup markers. CFG cleanup edges already
		// materialize their bodies in dedicated cleanup blocks, so there is no
		// direct runtime instruction to emit at the registration site.
		return "", nil
	case *midmir.UnsafeInstr:
		// Unsafe marker: safety is enforced at type-check time; no code to emit.
		return "", nil
	default:
		return "", fmt.Errorf("unsupported MIR instruction %T", instr)
	}
}

func lowerAssignLike(state *moduleState, name string, typ typeinfo.Type, value midmir.Value) (string, error) {
	// Route scalar alloca targets: compute to a fresh temp then store.
	if local := findLocalByName(state.fn, name); local != nil {
		if sc, ok := state.scalarLocals[local.ID]; ok {
			return lowerScalarAllocaAssign(state, sc, typ, value)
		}
	}
	return lowerSSAAssign(state, name, typ, value)
}

// lowerScalarAllocaAssign computes value to a fresh SSA temp then stores to alloca.
func lowerScalarAllocaAssign(state *moduleState, sc *scalarAllocaLocal, typ typeinfo.Type, value midmir.Value) (string, error) {
	tmp := freshTemp(state, "asgn")
	line, err := lowerSSAAssign(state, tmp[1:], typ, value) // strip leading %
	if err != nil {
		return "", err
	}
	storeLine := fmt.Sprintf("store %s %s, ptr %s", sc.IRType, tmp, sc.AllocaName)
	// Extract the actual result name from the last assignment in line.
	// lowerSSAAssign may produce multi-line output (e.g. cmp+zext).
	// The last "result" temp is what we need to store.
	resultName := tmp
	if line != "" {
		// The last line that starts with "%something = " defines the result.
		parts := strings.Split(strings.TrimRight(line, "\n"), "\n")
		for i := len(parts) - 1; i >= 0; i-- {
			p := strings.TrimSpace(parts[i])
			if strings.HasPrefix(p, "%") {
				if idx := strings.Index(p, " ="); idx > 0 {
					resultName = strings.TrimSpace(p[:idx])
					break
				}
			}
		}
		storeLine = fmt.Sprintf("store %s %s, ptr %s", sc.IRType, resultName, sc.AllocaName)
		return line + "\n" + storeLine, nil
	}
	return storeLine, nil
}

// lowerSSAAssign assigns a MIR value to a fresh SSA name (no alloca routing).
func lowerSSAAssign(state *moduleState, name string, typ typeinfo.Type, value midmir.Value) (string, error) {
	if local := findLocalByName(state.fn, name); local != nil {
		if agg, ok := state.aggLocals[local.ID]; ok {
			return lowerAggregateAssign(state, agg, value)
		}
	}
	if call, ok := value.(*midmir.CallValue); ok {
		return lowerCall(state, name, typ, call)
	}
	if field, ok := value.(*midmir.FieldLoadValue); ok {
		return lowerFieldLoad(state, name, typ, field)
	}
	if idx, ok := value.(*midmir.IndexValue); ok {
		return lowerIndexLoad(state, name, typ, idx)
	}
	if load, ok := value.(*midmir.LoadValue); ok {
		return lowerLoadValue(state, name, typ, load)
	}
	if bin, ok := value.(*midmir.BinaryValue); ok {
		if line, handled, err := lowerAggregateCompare(state, name, typ, bin); handled || err != nil {
			return line, err
		}
		if isCompareOp(bin.Op) {
			irType, err := llvmBaseType(typ)
			if err != nil {
				return "", err
			}
			return lowerCompareAssign(state, name, irType, bin)
		}
	}
	if un, ok := value.(*midmir.UnaryValue); ok && un.Op == "!" {
		irType, err := llvmBaseType(typ)
		if err != nil {
			return "", err
		}
		return lowerNotAssign(state, name, irType, un)
	}

	irType, err := llvmBaseType(typ)
	if err != nil {
		return "", err
	}
	expr, err := lowerValue(state, value)
	if err != nil {
		return "", err
	}
	if llvmValueNeedsCopy(value) {
		copyExpr, err := llvmCopyExpr(irType, expr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s = %s", llvmLocalName(name), copyExpr), nil
	}
	return fmt.Sprintf("%s = %s", llvmLocalName(name), expr), nil
}

// lowerCompareAssign emits an icmp/fcmp + optional zext.
func lowerCompareAssign(state *moduleState, name, targetIRType string, bin *midmir.BinaryValue) (string, error) {
	left, err := lowerValue(state, bin.Left)
	if err != nil {
		return "", err
	}
	right, err := lowerValue(state, bin.Right)
	if err != nil {
		return "", err
	}
	cmpOp, cmpTypeStr, err := llvmCompareOp(bin.Op, bin.Left.Type())
	if err != nil {
		return "", err
	}
	tmp := freshTemp(state, "cmp")
	cmpLine := fmt.Sprintf("%s = %s %s %s, %s", tmp, cmpOp, cmpTypeStr, left, right)
	if targetIRType == "i1" || targetIRType == "" {
		return fmt.Sprintf("%s\n%s = or i1 0, %s", cmpLine, llvmLocalName(name), tmp), nil
	}
	return fmt.Sprintf("%s\n%s = zext i1 %s to %s", cmpLine, llvmLocalName(name), tmp, targetIRType), nil
}

// lowerNotAssign emits an icmp eq zero + optional zext.
func lowerNotAssign(state *moduleState, name, targetIRType string, un *midmir.UnaryValue) (string, error) {
	rightExpr, err := lowerValue(state, un.Right)
	if err != nil {
		return "", err
	}
	rightIRType, err := llvmBaseType(un.Right.Type())
	if err != nil {
		return "", err
	}
	tmp := freshTemp(state, "not")
	cmpLine := fmt.Sprintf("%s = icmp eq %s %s, 0", tmp, rightIRType, rightExpr)
	if targetIRType == "i1" || targetIRType == "" {
		return fmt.Sprintf("%s\n%s = or i1 0, %s", cmpLine, llvmLocalName(name), tmp), nil
	}
	return fmt.Sprintf("%s\n%s = zext i1 %s to %s", cmpLine, llvmLocalName(name), tmp, targetIRType), nil
}

// ---------------------------------------------------------------------------
// Aggregate compare
// ---------------------------------------------------------------------------

func lowerAggregateCompare(state *moduleState, targetName string, targetType typeinfo.Type, bin *midmir.BinaryValue) (string, bool, error) {
	if bin == nil || (bin.Op != "==" && bin.Op != "!=") {
		return "", false, nil
	}
	if !isAggregateType(state, bin.Left.Type()) || !isAggregateType(state, bin.Right.Type()) {
		return "", false, nil
	}
	targetIRType, err := llvmBaseType(targetType)
	if err != nil {
		return "", true, err
	}
	leftStruct, err := lookupStructLayout(state, bin.Left.Type())
	if err != nil {
		return "", true, err
	}
	rightStruct, err := lookupStructLayout(state, bin.Right.Type())
	if err != nil {
		return "", true, err
	}
	if len(leftStruct.Fields) != len(rightStruct.Fields) {
		return "", true, fmt.Errorf("aggregate comparison requires matching layouts")
	}
	lines := make([]string, 0, len(leftStruct.Fields)*6+2)
	prevCmp := ""
	for i, field := range leftStruct.Fields {
		if field == nil {
			continue
		}
		if isAggregateType(state, field.Type) {
			return "", true, fmt.Errorf("nested aggregate comparison not supported yet")
		}
		leftLines, leftAddr, _, err := lowerFieldAddress(state, bin.Left, i)
		if err != nil {
			return "", true, err
		}
		rightLines, rightAddr, _, err := lowerFieldAddress(state, bin.Right, i)
		if err != nil {
			return "", true, err
		}
		lines = append(lines, leftLines...)
		lines = append(lines, rightLines...)

		fieldIRType, err := llvmBaseType(field.Type)
		if err != nil {
			return "", true, err
		}
		lt := freshTemp(state, "cmp")
		rt := freshTemp(state, "cmp")
		lines = append(lines, fmt.Sprintf("%s = load %s, ptr %s", lt, fieldIRType, leftAddr))
		lines = append(lines, fmt.Sprintf("%s = load %s, ptr %s", rt, fieldIRType, rightAddr))

		cmpOp, cmpTypeStr, err := llvmCompareOp("==", field.Type)
		if err != nil {
			return "", true, err
		}
		cmpTmp := freshTemp(state, "cmp")
		lines = append(lines, fmt.Sprintf("%s = %s %s %s, %s", cmpTmp, cmpOp, cmpTypeStr, lt, rt))
		widenTmp := freshTemp(state, "cmp")
		lines = append(lines, fmt.Sprintf("%s = zext i1 %s to i8", widenTmp, cmpTmp))

		if prevCmp == "" {
			prevCmp = widenTmp
			continue
		}
		merged := freshTemp(state, "cmp")
		lines = append(lines, fmt.Sprintf("%s = and i8 %s, %s", merged, prevCmp, widenTmp))
		prevCmp = merged
	}
	if prevCmp == "" {
		prevCmp = "1"
	}
	if bin.Op == "!=" {
		tmp := freshTemp(state, "cmp")
		lines = append(lines, fmt.Sprintf("%s = xor i8 %s, 1", tmp, prevCmp))
		prevCmp = tmp
	}
	if targetIRType == "i8" || targetIRType == "" {
		lines = append(lines, fmt.Sprintf("%s = or i8 0, %s", llvmLocalName(targetName), prevCmp))
	} else {
		tmp := freshTemp(state, "cmp")
		lines = append(lines, fmt.Sprintf("%s = icmp ne i8 %s, 0", tmp, prevCmp))
		lines = append(lines, fmt.Sprintf("%s = zext i1 %s to %s", llvmLocalName(targetName), tmp, targetIRType))
	}
	return strings.Join(lines, "\n"), true, nil
}

// ---------------------------------------------------------------------------
// Load / Store / Field
// ---------------------------------------------------------------------------

func lowerLoadValue(state *moduleState, targetName string, targetType typeinfo.Type, load *midmir.LoadValue) (string, error) {
	ptr, err := lowerValue(state, load.Pointer)
	if err != nil {
		return "", err
	}
	irType, err := llvmBaseType(targetType)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s = load %s, ptr %s", llvmLocalName(targetName), irType, ptr), nil
}

func lowerFieldLoad(state *moduleState, targetName string, targetType typeinfo.Type, field *midmir.FieldLoadValue) (string, error) {
	lines, addr, _, err := lowerFieldAddress(state, field.Base, field.FieldIndex)
	if err != nil {
		return "", err
	}
	irType, err := llvmBaseType(targetType)
	if err != nil {
		return "", err
	}
	lines = append(lines, fmt.Sprintf("%s = load %s, ptr %s", llvmLocalName(targetName), irType, addr))
	return strings.Join(lines, "\n"), nil
}

// lowerIndexLoad lowers arr[index] as an rvalue load.
func lowerIndexLoad(state *moduleState, targetName string, targetType typeinfo.Type, idx *midmir.IndexValue) (string, error) {
	lines, addr, err := lowerIndexAddress(state, idx.Base, idx.Index, idx.Base.Type())
	if err != nil {
		return "", err
	}
	irType, err := llvmBaseType(targetType)
	if err != nil {
		return "", err
	}
	lines = append(lines, fmt.Sprintf("%s = load %s, ptr %s", llvmLocalName(targetName), irType, addr))
	return strings.Join(lines, "\n"), nil
}

// lowerIndexAddress computes the pointer to arr[index].
// baseType must be *typeinfo.ArrayType or *typeinfo.PointerType.
func lowerIndexAddress(state *moduleState, base midmir.Value, index midmir.Value, baseType typeinfo.Type) ([]string, string, error) {
	var elemType typeinfo.Type
	switch bt := baseType.(type) {
	case *typeinfo.ArrayType:
		elemType = bt.Inner
	case *typeinfo.PointerType:
		elemType = bt.Inner
	default:
		return nil, "", fmt.Errorf("cannot index into %T", baseType)
	}
	elemIRType, err := llvmBaseType(elemType)
	if err != nil {
		return nil, "", err
	}
	baseExpr, err := lowerValue(state, base)
	if err != nil {
		return nil, "", err
	}
	indexExpr, err := lowerValue(state, index)
	if err != nil {
		return nil, "", err
	}
	addr := freshTemp(state, "elem")
	line := fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i64 %s", addr, elemIRType, baseExpr, indexExpr)
	return []string{line}, addr, nil
}

func lowerStorePlace(state *moduleState, instr *midmir.StoreInstr) (string, error) {
	if instr == nil {
		return "", nil
	}
	lines, addr, err := lowerPlaceAddr(state, instr.Target)
	if err != nil {
		return "", err
	}
	val, err := lowerValue(state, instr.Value)
	if err != nil {
		return "", err
	}
	irType, err := llvmBaseType(instr.Value.Type())
	if err != nil {
		return "", err
	}
	if !llvmValueNeedsCopy(instr.Value) {
		tmp := freshTemp(state, "store")
		lines = append(lines, fmt.Sprintf("%s = %s", tmp, val))
		val = tmp
	}
	lines = append(lines, fmt.Sprintf("store %s %s, ptr %s", irType, val, addr))
	return strings.Join(lines, "\n"), nil
}

// lowerPlaceAddr returns the LLVM ptr value for a MIR place.
func lowerPlaceAddr(state *moduleState, place midmir.Place) ([]string, string, error) {
	switch p := place.(type) {
	case nil:
		return nil, "", fmt.Errorf("nil place")
	case *midmir.LocalPlace:
		if agg, ok := state.aggLocals[p.LocalID]; ok {
			return nil, llvmLocalName(agg.PtrName), nil
		}
		if sc, ok := state.scalarLocals[p.LocalID]; ok {
			return nil, sc.AllocaName, nil
		}
		return nil, llvmLocalName(localNameByID(state.fn, p.LocalID)), nil
	case *midmir.FieldPlace:
		baseLines, basePtr, err := lowerPlaceAddr(state, p.Base)
		if err != nil {
			return nil, "", err
		}
		// get base type from the local
		baseType := localTypeByPlaceID(state, p.Base)
		sl, err2 := lookupStructLayout(state, baseType)
		if err2 != nil {
			return nil, "", err2
		}
		if p.FieldIndex < 0 || p.FieldIndex >= len(sl.Fields) {
			return nil, "", fmt.Errorf("invalid field index %d", p.FieldIndex)
		}
		field := sl.Fields[p.FieldIndex]
		if field.Offset == 0 {
			return baseLines, basePtr, nil
		}
		addrTmp := freshTemp(state, "addr")
		gepLine := fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 %d", addrTmp, basePtr, field.Offset)
		return append(baseLines, gepLine), addrTmp, nil
	case *midmir.IndexPlace:
		baseLines, basePtr, err := lowerPlaceAddr(state, p.Base)
		if err != nil {
			return nil, "", err
		}
		baseType := localTypeByPlaceID(state, p.Base)
		var elemIRType string
		if arr, ok := baseType.(*typeinfo.ArrayType); ok {
			elemIRType, err = llvmBaseType(arr.Inner)
			if err != nil {
				return nil, "", err
			}
		} else {
			return nil, "", fmt.Errorf("IndexPlace base is not an array: %T", baseType)
		}
		idxVal, err := lowerValue(state, p.Index)
		if err != nil {
			return nil, "", err
		}
		addrTmp := freshTemp(state, "elem")
		gepLine := fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i64 %s", addrTmp, elemIRType, basePtr, idxVal)
		return append(baseLines, gepLine), addrTmp, nil
	case *midmir.DerefPlace:
		// *ptr: the address to store to is the pointer value itself.
		ptrVal, err := lowerValue(state, p.Pointer)
		if err != nil {
			return nil, "", err
		}
		return nil, ptrVal, nil
	default:
		return nil, "", fmt.Errorf("unsupported place %T for lowerPlaceAddr", place)
	}
}

// localTypeByPlaceID returns the type of the variable referenced by a place.
func localTypeByPlaceID(state *moduleState, place midmir.Place) typeinfo.Type {
	if lp, ok := place.(*midmir.LocalPlace); ok {
		if state.fn != nil && lp.LocalID >= 0 && lp.LocalID < len(state.fn.Locals) {
			return state.fn.Locals[lp.LocalID].Type
		}
	}
	return nil
}

func lowerStoreField(state *moduleState, instr *midmir.StoreFieldInstr) (string, error) {
	lines, addr, fieldType, err := lowerFieldAddress(state, instr.Base, instr.FieldIndex)
	if err != nil {
		return "", err
	}
	value, err := lowerValue(state, instr.Value)
	if err != nil {
		return "", err
	}
	irType, err := llvmBaseType(fieldType)
	if err != nil {
		return "", err
	}
	lines = append(lines, fmt.Sprintf("store %s %s, ptr %s", irType, value, addr))
	return strings.Join(lines, "\n"), nil
}

func lowerFieldAddress(state *moduleState, base midmir.Value, fieldIndex int) ([]string, string, typeinfo.Type, error) {
	structLayout, err := lookupStructLayout(state, base.Type())
	if err != nil {
		return nil, "", nil, err
	}
	if fieldIndex < 0 || fieldIndex >= len(structLayout.Fields) {
		return nil, "", nil, fmt.Errorf("invalid field index %d", fieldIndex)
	}
	field := structLayout.Fields[fieldIndex]
	baseVal, err := lowerValue(state, base)
	if err != nil {
		return nil, "", nil, err
	}
	if field.Offset == 0 {
		return nil, baseVal, field.Type, nil
	}
	addr := freshTemp(state, "addr")
	line := fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 %d", addr, baseVal, field.Offset)
	return []string{line}, addr, field.Type, nil
}

// ---------------------------------------------------------------------------
// Call
// ---------------------------------------------------------------------------

func lowerCall(state *moduleState, targetName string, targetType typeinfo.Type, call *midmir.CallValue) (string, error) {
	callee, err := lowerCallee(state, call.Callee)
	if err != nil {
		return "", err
	}
	if call.IsConstructor {
		if targetName != "" {
			if local := findLocalByName(state.fn, targetName); local != nil {
				if agg, ok := state.aggLocals[local.ID]; ok {
					return lowerConstructorCall(state, llvmLocalName(agg.PtrName), call, callee)
				}
			}
		}
		return lowerConstructorCallDiscard(state, targetType, call, callee)
	}
	if isBuiltinPrintCall(call) {
		return lowerBuiltinPrintCall(state, call)
	}

	// Inline builtins: string_ptr and string_len have been removed.
	// String literals are now *i8 — no special callee interception needed.

	args := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		if isAggregateType(state, arg.Type()) {
			typeName, terr := llvmABITypeName(state, arg.Type())
			if terr != nil {
				return "", terr
			}
			_, align, terr := aggregateSizeAlign(state, arg.Type())
			if terr != nil {
				return "", terr
			}
			aval, terr := lowerValue(state, arg)
			if terr != nil {
				return "", terr
			}
			args = append(args, fmt.Sprintf("ptr byval(%s) align %d %s", typeName, align, aval))
		} else {
			atype, terr := llvmBaseType(arg.Type())
			if terr != nil {
				return "", terr
			}
			aval, terr := lowerValue(state, arg)
			if terr != nil {
				return "", terr
			}
			args = append(args, fmt.Sprintf("%s %s", atype, aval))
		}
	}
	argsStr := strings.Join(args, ", ")

	// Target is an aggregate local → call returns struct by value, store to ptr.
	if targetName != "" {
		if local := findLocalByName(state.fn, targetName); local != nil {
			if agg, ok := state.aggLocals[local.ID]; ok {
				return lowerAggregateCall(state, agg, callee, argsStr)
			}
		}
	}

	// Return type
	var retStr string
	if targetType != nil && isAggregateType(state, targetType) {
		tn, err := llvmABITypeName(state, targetType)
		if err != nil {
			return "", err
		}
		retStr = tn
	} else if targetType != nil {
		rt, err := llvmBaseType(targetType)
		if err != nil {
			return "", err
		}
		retStr = rt
	} else {
		retStr = "void"
	}

	callText := fmt.Sprintf("call %s @%s(%s)", retStr, callee, argsStr)
	if targetName == "" || retStr == "void" {
		return callText, nil
	}
	return fmt.Sprintf("%s = %s", llvmLocalName(targetName), callText), nil
}

func isBuiltinPrintCall(call *midmir.CallValue) bool {
	if call == nil {
		return false
	}
	callee, ok := call.Callee.(*midmir.NameValue)
	if !ok || callee == nil {
		return false
	}
	if callee.LinkName == "global__print" {
		return true
	}
	return len(callee.Path) == 2 && callee.Path[0] == "global" && callee.Path[1] == "print"
}

func lowerBuiltinPrintCall(state *moduleState, call *midmir.CallValue) (string, error) {
	if call == nil || len(call.Args) != 1 {
		return "", fmt.Errorf("builtin print expects exactly one argument")
	}
	arg := call.Args[0]
	if arg == nil {
		return "", fmt.Errorf("builtin print argument is nil")
	}
	if _, ok := arg.Type().(*typeinfo.StringType); ok {
		prefix, ptr, err := lowerAggregateValuePointer(state, arg)
		if err != nil {
			return "", err
		}
		return joinLLVMLines(prefix, []string{fmt.Sprintf("call void @global__print_str(ptr %s)", ptr)}), nil
	}
	if builtin, ok := arg.Type().(*typeinfo.BuiltinType); ok {
		return lowerBuiltinPrintPrimitive(state, arg, builtin.Name)
	}
	if _, ok := arg.Type().(*typeinfo.PointerType); ok {
		val, err := lowerValue(state, arg)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("call void @global__print_ptr(ptr %s)", val), nil
	}
	sym := emitStringConstant(state, arg.Type().String())
	return fmt.Sprintf("call void @global__print_type(ptr @%s)", sym), nil
}

func lowerBuiltinPrintPrimitive(state *moduleState, arg midmir.Value, name string) (string, error) {
	val, err := lowerValue(state, arg)
	if err != nil {
		return "", err
	}
	switch name {
	case "bool":
		return fmt.Sprintf("call void @global__print_bool(i8 %s)", val), nil
	case "i8", "i16", "i32", "i64", "isize":
		return lowerBuiltinPrintIntCast(state, val, name, "i64", "@global__print_i64")
	case "u8", "u16", "u32", "u64", "usize":
		return lowerBuiltinPrintIntCast(state, val, name, "u64", "@global__print_u64")
	case "f32", "f64":
		return lowerBuiltinPrintFloatCast(state, val, name)
	case "char":
		return fmt.Sprintf("call void @global__print_char(i32 %s)", val), nil
	default:
		sym := emitStringConstant(state, arg.Type().String())
		return fmt.Sprintf("call void @global__print_type(ptr @%s)", sym), nil
	}
}

func lowerBuiltinPrintIntCast(state *moduleState, value, srcName, dstName, callee string) (string, error) {
	if srcName == dstName {
		return fmt.Sprintf("call void %s(i64 %s)", callee, value), nil
	}
	castExpr, ok := llvmIntCastOp(state, srcName, dstName, value)
	if !ok {
		return "", fmt.Errorf("unsupported print integer cast from %s to %s", srcName, dstName)
	}
	tmp := freshTemp(state, "print")
	return joinLLVMLines([]string{
		fmt.Sprintf("%s = %s", tmp, castExpr),
		fmt.Sprintf("call void %s(i64 %s)", callee, tmp),
	}), nil
}

func lowerBuiltinPrintFloatCast(state *moduleState, value, srcName string) (string, error) {
	if srcName == "f64" {
		return fmt.Sprintf("call void @global__print_f64(double %s)", value), nil
	}
	castExpr, ok := llvmFloatCastOp(srcName, "f64", value)
	if !ok {
		return "", fmt.Errorf("unsupported print float cast from %s to f64", srcName)
	}
	tmp := freshTemp(state, "print")
	return joinLLVMLines([]string{
		fmt.Sprintf("%s = %s", tmp, castExpr),
		fmt.Sprintf("call void @global__print_f64(double %s)", tmp),
	}), nil
}

func lowerAggregateValuePointer(state *moduleState, value midmir.Value) ([]string, string, error) {
	if value == nil {
		return nil, "", fmt.Errorf("nil aggregate value")
	}
	switch v := value.(type) {
	case *midmir.LocalValue:
		if agg, ok := state.aggLocals[v.LocalID]; ok {
			return nil, llvmLocalName(agg.PtrName), nil
		}
	case *midmir.NameValue:
		if v.LinkName != "" {
			return nil, "@" + sanitizeIdent(v.LinkName), nil
		}
		return nil, "@" + llvmSymbol(state, v.Path), nil
	}
	if !isAggregateType(state, value.Type()) {
		return nil, "", fmt.Errorf("value %T is not aggregate", value)
	}
	typeName, err := llvmABITypeName(state, value.Type())
	if err != nil {
		return nil, "", err
	}
	_, align, err := aggregateSizeAlign(state, value.Type())
	if err != nil {
		return nil, "", err
	}
	tmp := freshTemp(state, "print_agg")
	agg := &aggregateLocal{PtrName: strings.TrimPrefix(tmp, "%"), Type: value.Type(), Align: align}
	assign, err := lowerAggregateAssign(state, agg, value)
	if err != nil {
		return nil, "", err
	}
	lines := []string{fmt.Sprintf("%s = alloca %s, align %d", tmp, typeName, align)}
	if assign != "" {
		lines = append(lines, strings.Split(assign, "\n")...)
	}
	return lines, tmp, nil
}

func joinLLVMLines(lines ...[]string) string {
	flat := make([]string, 0)
	for _, group := range lines {
		for _, line := range group {
			if strings.TrimSpace(line) == "" {
				continue
			}
			flat = append(flat, line)
		}
	}
	return strings.Join(flat, "\n")
}

func lowerAggregateCall(state *moduleState, agg *aggregateLocal, callee, argsStr string) (string, error) {
	typeName, err := llvmABITypeName(state, agg.Type)
	if err != nil {
		return "", err
	}
	tmp := freshTemp(state, "aggret")
	lines := []string{
		fmt.Sprintf("%s = call %s @%s(%s)", tmp, typeName, callee, argsStr),
		fmt.Sprintf("store %s %s, ptr %s", typeName, tmp, llvmLocalName(agg.PtrName)),
	}
	return strings.Join(lines, "\n"), nil
}

func lowerConstructorCall(state *moduleState, dstPtr string, call *midmir.CallValue, callee string) (string, error) {
	args := make([]string, 0, len(call.Args)+1)
	args = append(args, fmt.Sprintf("ptr %s", dstPtr))
	for _, arg := range call.Args {
		if isAggregateType(state, arg.Type()) {
			typeName, err := llvmABITypeName(state, arg.Type())
			if err != nil {
				return "", err
			}
			_, align, err := aggregateSizeAlign(state, arg.Type())
			if err != nil {
				return "", err
			}
			aval, err := lowerValue(state, arg)
			if err != nil {
				return "", err
			}
			args = append(args, fmt.Sprintf("ptr byval(%s) align %d %s", typeName, align, aval))
		} else {
			atype, err := llvmBaseType(arg.Type())
			if err != nil {
				return "", err
			}
			aval, err := lowerValue(state, arg)
			if err != nil {
				return "", err
			}
			args = append(args, fmt.Sprintf("%s %s", atype, aval))
		}
	}
	return fmt.Sprintf("call void @%s(%s)", callee, strings.Join(args, ", ")), nil
}

func lowerImplicitConstructorCall(state *moduleState, dstPtr string, path []string) (string, error) {
	if len(path) == 0 {
		return "", nil
	}
	return fmt.Sprintf("call void @%s(ptr %s)", llvmSymbol(state, path), dstPtr), nil
}

func lowerConstructorCallDiscard(state *moduleState, targetType typeinfo.Type, call *midmir.CallValue, callee string) (string, error) {
	if targetType == nil || !isAggregateType(state, targetType) {
		return "", fmt.Errorf("constructor call requires aggregate target")
	}
	typeName, err := llvmABITypeName(state, targetType)
	if err != nil {
		return "", err
	}
	_, align, err := aggregateSizeAlign(state, targetType)
	if err != nil {
		return "", err
	}
	tmp := freshTemp(state, "ctor_tmp")
	callText, err := lowerConstructorCall(state, tmp, call, callee)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s = alloca %s, align %d\n%s", tmp, typeName, align, callText), nil
}

// ---------------------------------------------------------------------------
// Aggregate assign
// ---------------------------------------------------------------------------

func lowerAggregateAssign(state *moduleState, agg *aggregateLocal, value midmir.Value) (string, error) {
	if agg == nil {
		return "", fmt.Errorf("nil aggregate local")
	}
	switch v := value.(type) {
	case *midmir.UnaryValue:
		if v.Op == "copy" || v.Op == "take" || v.Op == "comptime" {
			return lowerAggregateAssign(state, agg, v.Right)
		}
	case *midmir.CastValue:
		expr, err := lowerCast(state, v)
		if err != nil {
			return "", err
		}
		typeName, err := llvmABITypeName(state, agg.Type)
		if err != nil {
			return "", err
		}
		temp := freshTemp(state, "aggcast")
		lines := []string{
			fmt.Sprintf("%s = %s", temp, expr),
			fmt.Sprintf("store %s %s, ptr %s", typeName, temp, llvmLocalName(agg.PtrName)),
		}
		return strings.Join(lines, "\n"), nil
	case *midmir.CallValue:
		return lowerAggregateCallValue(state, agg, v)
	case *midmir.LocalValue, *midmir.NameValue:
		src, err := lowerAggregateSource(state, value)
		if err != nil {
			return "", err
		}
		return llvmMemcpy(llvmLocalName(agg.PtrName), src, agg.Size, agg.Align), nil
	case *midmir.CompositeValue:
		return lowerAggregateCompositeAssign(state, agg, v)
	}
	return "", fmt.Errorf("unsupported aggregate assignment %T", value)
}

// emitStringConstant writes a private unnamed_addr constant to state.deferredB
// and returns its symbol name (without @).  Each distinct string value gets a
// unique symbol; identical content currently still gets a new symbol (good
// enough for functional correctness).
func emitStringConstant(state *moduleState, s string) string {
	state.nextStrConst++
	sym := fmt.Sprintf("__str%d_%s", state.nextStrConst, sanitizeIdent(state.modulePrefix))
	escaped := llvmStringLiteral(s)
	// +1 for the null terminator we always add.
	length := len(s) + 1
	fmt.Fprintf(state.deferredB, "@%s = private unnamed_addr constant [%d x i8] %s\n",
		sym, length, escaped)
	return sym
}

func lowerAggregateCallValue(state *moduleState, agg *aggregateLocal, call *midmir.CallValue) (string, error) {
	callee, err := lowerCallee(state, call.Callee)
	if err != nil {
		return "", err
	}
	if call.IsConstructor {
		return lowerConstructorCall(state, llvmLocalName(agg.PtrName), call, callee)
	}
	args := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		if isAggregateType(state, arg.Type()) {
			typeName, terr := llvmABITypeName(state, arg.Type())
			if terr != nil {
				return "", terr
			}
			_, align, terr := aggregateSizeAlign(state, arg.Type())
			if terr != nil {
				return "", terr
			}
			aval, terr := lowerValue(state, arg)
			if terr != nil {
				return "", terr
			}
			args = append(args, fmt.Sprintf("ptr byval(%s) align %d %s", typeName, align, aval))
		} else {
			atype, terr := llvmBaseType(arg.Type())
			if terr != nil {
				return "", terr
			}
			aval, terr := lowerValue(state, arg)
			if terr != nil {
				return "", terr
			}
			args = append(args, fmt.Sprintf("%s %s", atype, aval))
		}
	}
	typeName, err := llvmABITypeName(state, agg.Type)
	if err != nil {
		return "", err
	}
	tmp := freshTemp(state, "aggret")
	lines := []string{
		fmt.Sprintf("%s = call %s @%s(%s)", tmp, typeName, callee, strings.Join(args, ", ")),
		fmt.Sprintf("store %s %s, ptr %s", typeName, tmp, llvmLocalName(agg.PtrName)),
	}
	return strings.Join(lines, "\n"), nil
}

func lowerAggregateCompositeAssign(state *moduleState, agg *aggregateLocal, comp *midmir.CompositeValue) (string, error) {
	// Array literal: positional items stored at successive element offsets.
	if arrType, ok := agg.Type.(*typeinfo.ArrayType); ok {
		elemSize, elemAlign, err := aggregateSizeAlignOfPrimitive(arrType.Inner)
		if err != nil {
			// try inner as aggregate
			innerSz, innerAl, err2 := aggregateSizeAlign(state, arrType.Inner)
			if err2 != nil {
				return "", fmt.Errorf("unsupported array element type in composite literal: %s", arrType.Inner)
			}
			elemSize = innerSz
			elemAlign = innerAl
		}
		stride := alignUpInt64(elemSize, elemAlign)
		irType, err := llvmBaseType(arrType.Inner)
		if err != nil {
			return "", err
		}
		lines := make([]string, 0, len(comp.Items)*2)
		for i, item := range comp.Items {
			lowered, err := lowerValue(state, item.Value)
			if err != nil {
				return "", err
			}
			addr := llvmLocalName(agg.PtrName)
			offset := int64(i) * stride
			if offset != 0 {
				tmp := freshTemp(state, "addr")
				lines = append(lines, fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 %d", tmp, llvmLocalName(agg.PtrName), offset))
				addr = tmp
			}
			lines = append(lines, fmt.Sprintf("store %s %s, ptr %s", irType, lowered, addr))
		}
		return strings.Join(lines, "\n"), nil
	}

	// String / slice literal: items "ptr" and "len" stored at byte offsets 0 and 8.
	if _, ok := agg.Type.(*typeinfo.SliceType); ok {
		items := make(map[string]midmir.Value, len(comp.Items))
		for _, item := range comp.Items {
			items[item.Name] = item.Value
		}
		ptrLowered, err := lowerValue(state, items["ptr"])
		if err != nil {
			return "", err
		}
		lenLowered, err := lowerValue(state, items["len"])
		if err != nil {
			return "", err
		}
		base := llvmLocalName(agg.PtrName)
		lenAddr := freshTemp(state, "len_addr")
		lines := []string{
			fmt.Sprintf("store ptr %s, ptr %s", ptrLowered, base),
			fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", lenAddr, base),
			fmt.Sprintf("store i64 %s, ptr %s", lenLowered, lenAddr),
		}
		return strings.Join(lines, "\n"), nil
	}
	if _, ok := agg.Type.(*typeinfo.StringType); ok {
		items := make(map[string]midmir.Value, len(comp.Items))
		for _, item := range comp.Items {
			items[item.Name] = item.Value
		}
		ptrLowered, err := lowerValue(state, items["ptr"])
		if err != nil {
			return "", err
		}
		lenLowered, err := lowerValue(state, items["len"])
		if err != nil {
			return "", err
		}
		base := llvmLocalName(agg.PtrName)
		lenAddr := freshTemp(state, "len_addr")
		lines := []string{
			fmt.Sprintf("store ptr %s, ptr %s", ptrLowered, base),
			fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", lenAddr, base),
			fmt.Sprintf("store i64 %s, ptr %s", lenLowered, lenAddr),
		}
		return strings.Join(lines, "\n"), nil
	}

	structLayout, err := lookupStructLayout(state, agg.Type)
	if err != nil {
		return "", err
	}
	items := make(map[string]midmir.Value, len(comp.Items))
	for _, item := range comp.Items {
		items[item.Name] = item.Value
	}
	lines := make([]string, 0, len(structLayout.Fields)*3)
	for _, field := range structLayout.Fields {
		if field == nil {
			continue
		}
		val, ok := items[field.Name]
		if !ok {
			continue
		}
		irType, err := llvmBaseType(field.Type)
		if err != nil {
			return "", err
		}
		addr := llvmLocalName(agg.PtrName)
		if field.Offset != 0 {
			tmp := freshTemp(state, "addr")
			lines = append(lines, fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 %d", tmp, llvmLocalName(agg.PtrName), field.Offset))
			addr = tmp
		}
		lowered, err := lowerValue(state, val)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("store %s %s, ptr %s", irType, lowered, addr))
	}
	if len(comp.ConstructorPath) > 0 {
		ctor, err := lowerImplicitConstructorCall(state, llvmLocalName(agg.PtrName), comp.ConstructorPath)
		if err != nil {
			return "", err
		}
		if ctor != "" {
			lines = append(lines, ctor)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func lowerAggregateSource(state *moduleState, value midmir.Value) (string, error) {
	switch v := value.(type) {
	case *midmir.LocalValue:
		return lowerValue(state, v)
	case *midmir.NameValue:
		return lowerValue(state, v)
	default:
		return "", fmt.Errorf("unsupported aggregate source %T", value)
	}
}

// ---------------------------------------------------------------------------
// Terminator
// ---------------------------------------------------------------------------

// lowerPanicTerm emits a call to ferret__panic(ptr msg) followed by unreachable.
// String literals are null-terminated *i8 constants; we pass the pointer directly.
func lowerPanicTerm(state *moduleState, t *midmir.PanicTerm) (string, error) {
	switch v := t.Value.(type) {
	case *midmir.StringValue:
		sym := emitStringConstant(state, v.Value)
		return fmt.Sprintf("call void @ferret__panic(ptr @%s)\nunreachable", sym), nil
	default:
		return "", fmt.Errorf("panic: non-literal message not yet supported (%T)", t.Value)
	}
}

func lowerTerm(state *moduleState, term midmir.Terminator) (string, error) {
	switch t := term.(type) {
	case nil:
		return "unreachable", nil
	case *midmir.ExitTerm:
		// ExitTerm is emitted for void functions that have no explicit return.
		// Emit ret void so the function terminates correctly.
		return "ret void", nil
	case *midmir.JumpTerm:
		return fmt.Sprintf("br label %%%s", llvmBlockLabel(state.fn, t.TargetID)), nil
	case *midmir.BranchTerm:
		cond, err := lowerCondValue(state, t.Cond)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s\nbr i1 %s, label %%%s, label %%%s",
			cond,
			freshTempRef(cond),
			llvmBlockLabel(state.fn, t.TrueID),
			llvmBlockLabel(state.fn, t.FalseID)), nil
	case *midmir.ReturnTerm:
		if t.CleanupID >= 0 {
			return fmt.Sprintf("br label %%%s", llvmBlockLabel(state.fn, t.CleanupID)), nil
		}
		if t.Value == nil {
			return "ret void", nil
		}
		if call, ok := t.Value.(*midmir.CallValue); ok && call.IsConstructor {
			typeName, err := llvmABITypeName(state, call.Type())
			if err != nil {
				return "", err
			}
			_, align, err := aggregateSizeAlign(state, call.Type())
			if err != nil {
				return "", err
			}
			callee, err := lowerCallee(state, call.Callee)
			if err != nil {
				return "", err
			}
			tmpPtr := freshTemp(state, "ctor_ret")
			callText, err := lowerConstructorCall(state, tmpPtr, call, callee)
			if err != nil {
				return "", err
			}
			tmpVal := freshTemp(state, "retval")
			return fmt.Sprintf("%s = alloca %s, align %d\n%s\n%s = load %s, ptr %s\nret %s %s", tmpPtr, typeName, align, callText, tmpVal, typeName, tmpPtr, typeName, tmpVal), nil
		}
		// Aggregate return: load struct value then return by value.
		if isAggregateType(state, t.Value.Type()) {
			typeName, err := llvmABITypeName(state, t.Value.Type())
			if err != nil {
				return "", err
			}
			ptr, err := lowerValue(state, t.Value)
			if err != nil {
				return "", err
			}
			tmp := freshTemp(state, "retval")
			return fmt.Sprintf("%s = load %s, ptr %s\nret %s %s", tmp, typeName, ptr, typeName, tmp), nil
		}
		retIRType, err := llvmBaseType(t.Value.Type())
		if err != nil {
			return "", err
		}
		val, err := lowerValue(state, t.Value)
		if err != nil {
			return "", err
		}
		if retIRType == "void" {
			return "ret void", nil
		}
		return fmt.Sprintf("ret %s %s", retIRType, val), nil
	case *midmir.PanicTerm:
		if t.CleanupID >= 0 {
			return fmt.Sprintf("br label %%%s", llvmBlockLabel(state.fn, t.CleanupID)), nil
		}
		return lowerPanicTerm(state, t)
	case *midmir.SwitchTerm:
		return "", fmt.Errorf("match lowering is not implemented yet")
	default:
		return "", fmt.Errorf("unsupported MIR terminator %T", term)
	}
}

// lowerCondValue lowers a MIR value to an i1 condition.
// Returns a multi-line string where the last line is the comparison instruction
// that defines the i1 temp. Use freshTempRef to extract the temp name.
func lowerCondValue(state *moduleState, cond midmir.Value) (string, error) {
	condIRType, err := llvmBaseType(cond.Type())
	if err != nil {
		return "", err
	}
	condExpr, err := lowerValue(state, cond)
	if err != nil {
		return "", err
	}
	if condIRType == "i1" {
		// wrap in a named temp
		tmp := freshTemp(state, "br")
		return fmt.Sprintf("%s = or i1 0, %s", tmp, condExpr), nil
	}
	tmp := freshTemp(state, "br")
	return fmt.Sprintf("%s = icmp ne %s %s, 0", tmp, condIRType, condExpr), nil
}

// freshTempRef extracts the LHS temp name from a single-line "%name = ..." string.
func freshTempRef(line string) string {
	// line looks like: "%_br1 = icmp ne i8 %x, 0"
	// we want "%_br1"
	line = strings.TrimSpace(line)
	// handle multi-line: take the last line
	if idx := strings.LastIndex(line, "\n"); idx >= 0 {
		line = line[idx+1:]
	}
	if idx := strings.Index(line, " ="); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}
	return line
}

// ---------------------------------------------------------------------------
// Value lowering
// ---------------------------------------------------------------------------

func lowerValue(state *moduleState, value midmir.Value) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", fmt.Errorf("nil value")
	case *midmir.LocalValue:
		if agg, ok := state.aggLocals[v.LocalID]; ok {
			return llvmLocalName(agg.PtrName), nil
		}
		if sc, ok := state.scalarLocals[v.LocalID]; ok {
			tmp := freshTemp(state, "ld")
			state.pendingLines = append(state.pendingLines,
				fmt.Sprintf("%s = load %s, ptr %s", tmp, sc.IRType, sc.AllocaName))
			return tmp, nil
		}
		return llvmLocalName(localNameByID(state.fn, v.LocalID)), nil
	case *midmir.NameValue:
		if len(v.Path) == 1 {
			if local := findLocalByName(state.fn, v.Path[0]); local != nil {
				if agg, ok := state.aggLocals[local.ID]; ok {
					return llvmLocalName(agg.PtrName), nil
				}
				if sc, ok := state.scalarLocals[local.ID]; ok {
					tmp := freshTemp(state, "ld")
					state.pendingLines = append(state.pendingLines,
						fmt.Sprintf("%s = load %s, ptr %s", tmp, sc.IRType, sc.AllocaName))
					return tmp, nil
				}
				return llvmLocalName(local.Name), nil
			}
		}
		if _, ok := v.Type().(*typeinfo.FuncType); ok {
			if v.LinkName != "" {
				return "@" + sanitizeIdent(v.LinkName), nil
			}
			return "@" + llvmSymbol(state, v.Path), nil
		}
		if !isAggregateType(state, v.Type()) {
			irType, err := llvmBaseType(v.Type())
			if err == nil && irType != "void" {
				tmp := freshTemp(state, "ld")
				sym := "@" + llvmSymbol(state, v.Path)
				if v.LinkName != "" {
					sym = "@" + sanitizeIdent(v.LinkName)
				}
				state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = load %s, ptr %s", tmp, irType, sym))
				return tmp, nil
			}
		}
		if v.LinkName != "" {
			return "@" + sanitizeIdent(v.LinkName), nil
		}
		return "@" + llvmSymbol(state, v.Path), nil
	case *midmir.NumberValue:
		return llvmNumberLiteral(v.Type(), v.Value)
	case *midmir.BoolValue:
		if v.Value {
			return "1", nil
		}
		return "0", nil
	case *midmir.StringValue:
		// String literals are *i8 — emit a private global constant and return its address.
		sym := emitStringConstant(state, v.Value)
		return "@" + sym, nil
	case *midmir.NoneValue:
		return "null", nil
	case *midmir.UnaryValue:
		return lowerUnary(state, v)
	case *midmir.AddrOfValue:
		return lowerAddrOf(state, v)
	case *midmir.LoadValue:
		return "", fmt.Errorf("load value must be lowered in assignment/eval context")
	case *midmir.BinaryValue:
		return lowerBinary(state, v)
	case *midmir.CastValue:
		return lowerCast(state, v)
	case *midmir.CallValue:
		return "", fmt.Errorf("call value must be lowered in assignment/eval context")
	case *midmir.FieldLoadValue:
		return "", fmt.Errorf("field load must be lowered in assignment context")
	case *midmir.IndexValue:
		return "", fmt.Errorf("index value must be lowered in assignment context")
	default:
		return "", fmt.Errorf("unsupported MIR value %T", value)
	}
}

func lowerAddrOf(state *moduleState, v *midmir.AddrOfValue) (string, error) {
	switch src := v.Source.(type) {
	case *midmir.LocalValue:
		if agg, ok := state.aggLocals[src.LocalID]; ok {
			return llvmLocalName(agg.PtrName), nil
		}
		if sc, ok := state.scalarLocals[src.LocalID]; ok {
			return sc.AllocaName, nil
		}
		return "", fmt.Errorf("addr_of on scalar SSA local not supported by llvm lowerer yet")
	case *midmir.NameValue:
		if len(src.Path) == 1 {
			if local := findLocalByName(state.fn, src.Path[0]); local != nil {
				if agg, ok := state.aggLocals[local.ID]; ok {
					return llvmLocalName(agg.PtrName), nil
				}
				if sc, ok := state.scalarLocals[local.ID]; ok {
					return sc.AllocaName, nil
				}
				return "", fmt.Errorf("addr_of on scalar SSA local not supported by llvm lowerer yet")
			}
		}
		if src.LinkName != "" {
			return "@" + sanitizeIdent(src.LinkName), nil
		}
		return "@" + llvmSymbol(state, src.Path), nil
	default:
		return "", fmt.Errorf("unsupported addr_of source %T", v.Source)
	}
}

func lowerUnary(state *moduleState, v *midmir.UnaryValue) (string, error) {
	right, err := lowerValue(state, v.Right)
	if err != nil {
		return "", err
	}
	irType, err := llvmBaseType(v.Right.Type())
	if err != nil {
		return "", err
	}
	switch v.Op {
	case "copy", "take", "comptime":
		return llvmCopyExpr(irType, right)
	case "-":
		switch irType {
		case "float", "double":
			return fmt.Sprintf("fneg %s %s", irType, right), nil
		default:
			return fmt.Sprintf("sub %s 0, %s", irType, right), nil
		}
	case "!":
		// Returns an i1 expression string (handled by lowerNotAssign in assign context).
		return fmt.Sprintf("icmp eq %s %s, 0", irType, right), nil
	default:
		return "", fmt.Errorf("unsupported unary op %q", v.Op)
	}
}

func lowerBinary(state *moduleState, v *midmir.BinaryValue) (string, error) {
	left, err := lowerValue(state, v.Left)
	if err != nil {
		return "", err
	}
	right, err := lowerValue(state, v.Right)
	if err != nil {
		return "", err
	}
	irType, err := llvmBaseType(v.Left.Type())
	if err != nil {
		return "", err
	}
	switch v.Op {
	case "+", "-", "*", "/", "%", "&&", "||":
		op, err := llvmBinaryOp(v.Op, v.Type())
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s %s, %s", op, irType, left, right), nil
	case "==", "!=", "<", "<=", ">", ">=":
		op, typeStr, err := llvmCompareOp(v.Op, v.Left.Type())
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s %s, %s", op, typeStr, left, right), nil
	default:
		return "", fmt.Errorf("unsupported binary op %q", v.Op)
	}
}

func lowerCast(state *moduleState, v *midmir.CastValue) (string, error) {
	if v == nil || v.Left == nil {
		return "", fmt.Errorf("invalid cast")
	}
	if _, ok := unwrapNamed(v.Type()).(*typeinfo.StringType); ok {
		return lowerStringCast(state, v.Left)
	}
	if isAggregateType(state, v.Left.Type()) && isAggregateType(state, v.Type()) {
		return lowerValue(state, v.Left)
	}
	srcVal, err := lowerValue(state, v.Left)
	if err != nil {
		return "", err
	}
	src := unwrapNamed(v.Left.Type())
	dst := unwrapNamed(v.Type())

	srcBuiltin, srcIsBuiltin := src.(*typeinfo.BuiltinType)
	dstBuiltin, dstIsBuiltin := dst.(*typeinfo.BuiltinType)

	if srcIsBuiltin && dstIsBuiltin {
		if srcBuiltin.Name == dstBuiltin.Name {
			srcIR, _ := llvmBaseType(src)
			return llvmCopyExpr(srcIR, srcVal)
		}
		if op, ok := llvmIntCastOp(nil, srcBuiltin.Name, dstBuiltin.Name, srcVal); ok {
			return op, nil
		}
		if op, ok := llvmFloatCastOp(srcBuiltin.Name, dstBuiltin.Name, srcVal); ok {
			return op, nil
		}
	}
	if _, ok := dst.(*typeinfo.PointerType); ok {
		return llvmCopyExpr("ptr", srcVal)
	}
	return "", fmt.Errorf("unsupported cast from %s to %s", src, dst)
}

func lowerStringCast(state *moduleState, value midmir.Value) (string, error) {
	srcVal, err := lowerValue(state, value)
	if err != nil {
		return "", err
	}
	src := unwrapNamed(value.Type())
	srcBuiltin, ok := src.(*typeinfo.BuiltinType)
	if !ok {
		return "", fmt.Errorf("unsupported string cast source %s", src)
	}
	switch srcBuiltin.Name {
	case "i8", "i16", "i32":
		castExpr, _ := llvmIntCastOp(nil, srcBuiltin.Name, "i64", srcVal)
		return fmt.Sprintf("call { ptr, i64 } @global__i64_str(%s)", operandWithTemp(state, "i64", castExpr)), nil
	case "i64", "isize":
		if srcBuiltin.Name == "isize" {
			castExpr, _ := llvmIntCastOp(nil, srcBuiltin.Name, "i64", srcVal)
			return fmt.Sprintf("call { ptr, i64 } @global__i64_str(%s)", operandWithTemp(state, "i64", castExpr)), nil
		}
		return fmt.Sprintf("call { ptr, i64 } @global__i64_str(i64 %s)", srcVal), nil
	case "u8", "u16", "u32", "bool", "char":
		castExpr, _ := llvmIntCastOp(nil, srcBuiltin.Name, "u64", srcVal)
		return fmt.Sprintf("call { ptr, i64 } @global__u64_str(%s)", operandWithTemp(state, "i64", castExpr)), nil
	case "u64", "usize":
		if srcBuiltin.Name == "usize" {
			castExpr, _ := llvmIntCastOp(nil, srcBuiltin.Name, "u64", srcVal)
			return fmt.Sprintf("call { ptr, i64 } @global__u64_str(%s)", operandWithTemp(state, "i64", castExpr)), nil
		}
		return fmt.Sprintf("call { ptr, i64 } @global__u64_str(i64 %s)", srcVal), nil
	case "f32":
		castExpr, _ := llvmFloatCastOp("f32", "f64", srcVal)
		return fmt.Sprintf("call { ptr, i64 } @global__f64_str(%s)", operandWithTemp(state, "double", castExpr)), nil
	case "f64":
		return fmt.Sprintf("call { ptr, i64 } @global__f64_str(double %s)", srcVal), nil
	default:
		return "", fmt.Errorf("unsupported string cast source %s", srcBuiltin.Name)
	}
}

func operandWithTemp(state *moduleState, irType, expr string) string {
	if !strings.Contains(expr, " ") || strings.HasPrefix(expr, "%") || strings.HasPrefix(expr, "@") || expr == "null" {
		return irType + " " + expr
	}
	tmp := freshTemp(state, "cast")
	state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = %s", tmp, expr))
	return irType + " " + tmp
}

func lowerCallee(state *moduleState, value midmir.Value) (string, error) {
	switch v := value.(type) {
	case *midmir.NameValue:
		if v.LinkName != "" {
			return sanitizeIdent(v.LinkName), nil
		}
		return llvmSymbol(state, v.Path), nil
	default:
		return "", fmt.Errorf("unsupported call callee %T", value)
	}
}

// ---------------------------------------------------------------------------
// Function state
// ---------------------------------------------------------------------------

func prepareFunctionState(state *moduleState, fn *midmir.Function) error {
	state.fn = fn
	state.aggLocals = make(map[int]*aggregateLocal)
	state.aggParams = make(map[int]struct{})
	state.scalarLocals = make(map[int]*scalarAllocaLocal)
	state.pendingLines = nil
	state.nextTemp = 0

	for _, param := range fn.Params {
		if param == nil {
			continue
		}
		if isAggregateType(state, param.Type) {
			state.aggParams[param.LocalID] = struct{}{}
		}
	}
	for _, local := range fn.Locals {
		if local == nil {
			continue
		}
		if isAggregateType(state, local.Type) {
			agg := &aggregateLocal{
				ID:      local.ID,
				Name:    local.Name,
				Type:    local.Type,
				PtrName: local.Name,
			}
			size, align, err := aggregateSizeAlign(state, local.Type)
			if err != nil {
				return err
			}
			agg.Size = size
			agg.Align = align
			state.aggLocals[local.ID] = agg
			continue
		}
		// Scalar local: use alloca so we support reassignment without
		// violating LLVM's SSA uniqueness requirement.
		irType, err := llvmBaseType(local.Type)
		if err != nil || irType == "void" {
			continue
		}
		state.scalarLocals[local.ID] = &scalarAllocaLocal{
			ID:         local.ID,
			Name:       local.Name,
			AllocaName: "%" + sanitizeIdent(local.Name) + "_alloca",
			IRType:     irType,
		}
	}
	return nil
}

func entryPrelude(state *moduleState) []string {
	if state == nil || state.fn == nil {
		return nil
	}

	// 1. Alloca scalar locals (for SSA-safe reassignment).
	scalarIDs := make([]int, 0, len(state.scalarLocals))
	for id := range state.scalarLocals {
		scalarIDs = append(scalarIDs, id)
	}
	sort.Ints(scalarIDs)

	// 2. Alloca non-param aggregate locals.
	aggIDs := make([]int, 0, len(state.aggLocals))
	for id, agg := range state.aggLocals {
		if agg == nil || agg.Size == 0 {
			continue
		}
		if _, ok := state.aggParams[id]; ok {
			continue
		}
		aggIDs = append(aggIDs, id)
	}
	sort.Ints(aggIDs)

	lines := make([]string, 0, len(scalarIDs)+len(aggIDs))

	for _, id := range scalarIDs {
		sc := state.scalarLocals[id]
		lines = append(lines, fmt.Sprintf("%s = alloca %s, align %d",
			sc.AllocaName, sc.IRType, irTypeAlign(sc.IRType)))
	}

	// Store incoming scalar param register values into their allocas.
	// Params are added to scalarLocals (via collectLocals) but the incoming
	// SSA register (%paramname) must be stored into the alloca (%paramname_alloca)
	// so that loads from the alloca read the correct value.
	for _, param := range state.fn.Params {
		if param == nil {
			continue
		}
		// Aggregate params use byval and are accessed directly via pointer; skip.
		if _, ok := state.aggParams[param.LocalID]; ok {
			continue
		}
		sc, ok := state.scalarLocals[param.LocalID]
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("store %s %s, ptr %s",
			sc.IRType, llvmLocalName(param.Name), sc.AllocaName))
	}

	for _, id := range aggIDs {
		agg := state.aggLocals[id]
		typeName, err := llvmABITypeName(state, agg.Type)
		if err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s = alloca %s, align %d",
			llvmLocalName(agg.PtrName), typeName, normalizeAlign(agg.Align)))
	}

	return lines
}

// ---------------------------------------------------------------------------
// Global composite
// ---------------------------------------------------------------------------

func lowerGlobalComposite(state *moduleState, typ typeinfo.Type, comp *midmir.CompositeValue) (string, error) {
	if _, ok := typ.(*typeinfo.StringType); ok {
		return lowerGlobalStringLike(state, comp)
	}
	if _, ok := typ.(*typeinfo.SliceType); ok {
		return lowerGlobalStringLike(state, comp)
	}
	structLayout, err := lookupStructLayout(state, typ)
	if err != nil {
		return "", err
	}
	items := make(map[string]midmir.Value, len(comp.Items))
	for _, item := range comp.Items {
		items[item.Name] = item.Value
	}
	parts := make([]string, 0, len(structLayout.Fields)*2)
	offset := int64(0)
	for _, field := range structLayout.Fields {
		if field == nil {
			continue
		}
		if field.Offset > offset {
			pad := field.Offset - offset
			parts = append(parts, fmt.Sprintf("[%d x i8] zeroinitializer", pad))
			offset = field.Offset
		}
		irType, err := llvmBaseType(field.Type)
		if err != nil {
			return "", err
		}
		val, ok := items[field.Name]
		if !ok {
			parts = append(parts, fmt.Sprintf("%s zeroinitializer", irType))
			offset += field.Size
			continue
		}
		lit, err := lowerGlobalValue(state, field.Type, val)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s %s", irType, lit))
		offset += field.Size
	}
	if structLayout.Size > offset {
		pad := structLayout.Size - offset
		parts = append(parts, fmt.Sprintf("[%d x i8] zeroinitializer", pad))
	}
	return strings.Join(parts, ", "), nil
}

func lowerGlobalStringLike(state *moduleState, comp *midmir.CompositeValue) (string, error) {
	items := make(map[string]midmir.Value, len(comp.Items))
	for _, item := range comp.Items {
		items[item.Name] = item.Value
	}
	ptrLit, err := lowerGlobalValue(state, &typeinfo.PointerType{Inner: &typeinfo.BuiltinType{Name: "u8"}}, items["ptr"])
	if err != nil {
		return "", err
	}
	lenLit, err := lowerGlobalValue(state, &typeinfo.BuiltinType{Name: "usize"}, items["len"])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("{ ptr, i64 } { ptr %s, i64 %s }", ptrLit, lenLit), nil
}

func lowerGlobalValue(state *moduleState, typ typeinfo.Type, value midmir.Value) (string, error) {
	switch v := value.(type) {
	case *midmir.NumberValue:
		return llvmNumberLiteral(typ, v.Value)
	case *midmir.BoolValue:
		if v.Value {
			return "1", nil
		}
		return "0", nil
	case *midmir.NameValue:
		if v.LinkName != "" {
			return "@" + sanitizeIdent(v.LinkName), nil
		}
		return "@" + llvmSymbol(state, v.Path), nil
	default:
		return "", fmt.Errorf("unsupported global value %T", value)
	}
}

// ---------------------------------------------------------------------------
// Layout helpers
// ---------------------------------------------------------------------------

// isVoidType reports whether typ represents the absence of a value.
func isVoidType(typ typeinfo.Type) bool {
	if typ == nil {
		return true
	}
	if b, ok := typ.(*typeinfo.BuiltinType); ok {
		return b.Name == "void"
	}
	return false
}

func isAggregateType(state *moduleState, typ typeinfo.Type) bool {
	if typ == nil {
		return false
	}
	if _, err := llvmBaseType(typ); err == nil {
		return false
	}
	_, _, err := aggregateSizeAlign(state, typ)
	return err == nil
}

func alignUpInt64(size, align int64) int64 {
	if align <= 1 {
		return size
	}
	return (size + align - 1) &^ (align - 1)
}

func aggregateSizeAlignOfPrimitive(typ typeinfo.Type) (int64, int64, error) {
	switch t := unwrapNamed(typ).(type) {
	case *typeinfo.BuiltinType:
		switch t.Name {
		case "bool", "u8", "i8":
			return 1, 1, nil
		case "u16", "i16":
			return 2, 2, nil
		case "u32", "i32", "char", "f32":
			return 4, 4, nil
		case "u64", "i64", "usize", "isize", "f64":
			return 8, 8, nil
		}
	case *typeinfo.PointerType:
		return 8, 8, nil
	}
	return 0, 0, fmt.Errorf("not a primitive type")
}

func aggregateSizeAlign(state *moduleState, typ typeinfo.Type) (int64, int64, error) {
	switch t := typ.(type) {
	case *typeinfo.BuiltinType:
		return 0, 0, fmt.Errorf("unsupported aggregate builtin %s", t.Name)
	case *typeinfo.ArrayType:
		if t.Len < 0 {
			return 0, 0, fmt.Errorf("array with unknown length")
		}
		elemSize, elemAlign, elemErr := aggregateSizeAlignOfPrimitive(t.Inner)
		if elemErr != nil {
			// Also try inner as aggregate
			innerSz, innerAl, err2 := aggregateSizeAlign(state, t.Inner)
			if err2 != nil {
				return 0, 0, fmt.Errorf("unsupported array element type %s", t.Inner)
			}
			stride := alignUpInt64(innerSz, innerAl)
			return stride * t.Len, innerAl, nil
		}
		stride := alignUpInt64(elemSize, elemAlign)
		return stride * t.Len, elemAlign, nil
	case *typeinfo.StringType:
		return 16, 8, nil
	case *typeinfo.SliceType:
		// str / []T: { ptr *T, len usize } — 16 bytes, 8-byte aligned.
		return 16, 8, nil
	case *typeinfo.NamedType:
		info, err := lookupNamedLayout(state, t)
		if err != nil {
			return 0, 0, err
		}
		if info == nil || !info.Known {
			return 0, 0, fmt.Errorf("unknown aggregate layout for %s", t)
		}
		return info.Size, info.Align, nil
	default:
		return 0, 0, fmt.Errorf("unsupported aggregate type %s", typ)
	}
}

func lookupNamedLayout(state *moduleState, named *typeinfo.NamedType) (*layout.TypeLayout, error) {
	if named == nil {
		return nil, fmt.Errorf("nil named type")
	}
	if state != nil {
		if state.layouts != nil {
			if lm, ok := state.layouts[named.ModuleKey]; ok && lm != nil {
				if info, ok := lm.Lookup(named.Name); ok {
					return info, nil
				}
			}
		}
		if state.layout != nil && state.mod != nil && named.ModuleKey == state.mod.Key {
			if info, ok := state.layout.Lookup(named.Name); ok {
				return info, nil
			}
		}
	}
	return nil, fmt.Errorf("layout for named type %s not available in llvm backend", named)
}

func lookupStructLayout(state *moduleState, typ typeinfo.Type) (*layout.StructLayout, error) {
	switch t := typ.(type) {
	case *typeinfo.BuiltinType:
		return nil, fmt.Errorf("builtin %s is not a struct layout", t.Name)
	case *typeinfo.NamedType:
		info, err := lookupNamedLayout(state, t)
		if err != nil {
			return nil, err
		}
		if info == nil || info.Struct == nil {
			return nil, fmt.Errorf("type %s is not a struct layout", t)
		}
		return info.Struct, nil
	case *typeinfo.PointerType:
		return lookupStructLayout(state, t.Inner)
	default:
		return nil, fmt.Errorf("unsupported struct base type %s", typ)
	}
}

// ---------------------------------------------------------------------------
// LLVM struct type body
// ---------------------------------------------------------------------------

func llvmStructBody(state *moduleState, st *layout.StructLayout) (string, error) {
	parts := make([]string, 0, len(st.Fields)*2)
	offset := int64(0)
	for _, field := range st.Fields {
		if field == nil {
			continue
		}
		if field.Offset > offset {
			parts = append(parts, fmt.Sprintf("[%d x i8]", field.Offset-offset))
			offset = field.Offset
		}
		sub, err := llvmFieldType(state, field.Type)
		if err != nil {
			return "", err
		}
		parts = append(parts, sub)
		offset += field.Size
	}
	if st.Size > offset {
		parts = append(parts, fmt.Sprintf("[%d x i8]", st.Size-offset))
	}
	return strings.Join(parts, ", "), nil
}

// llvmFieldType returns the LLVM IR type for a struct field.
func llvmFieldType(state *moduleState, typ typeinfo.Type) (string, error) {
	if named, ok := typ.(*typeinfo.NamedType); ok {
		info, err := lookupNamedLayout(state, named)
		if err == nil && info != nil && info.Struct != nil {
			return "%" + llvmTypeName(state, named), nil
		}
	}
	if _, ok := typ.(*typeinfo.StringType); ok {
		return "{ ptr, i64 }", nil
	}
	if _, ok := typ.(*typeinfo.SliceType); ok {
		return "{ ptr, i64 }", nil
	}
	return llvmBaseType(typ)
}

// ---------------------------------------------------------------------------
// Type helpers
// ---------------------------------------------------------------------------

// llvmABITypeName returns the LLVM type name for function signatures, calls, globals.
func llvmABITypeName(state *moduleState, typ typeinfo.Type) (string, error) {
	if named, ok := typ.(*typeinfo.NamedType); ok {
		if info, err := lookupNamedLayout(state, named); err == nil && info != nil && info.Struct != nil && info.Known {
			return "%" + llvmTypeName(state, named), nil
		}
	}
	if _, ok := typ.(*typeinfo.StringType); ok {
		return "{ ptr, i64 }", nil
	}
	if _, ok := typ.(*typeinfo.SliceType); ok {
		return "{ ptr, i64 }", nil
	}
	return llvmBaseType(typ)
}

// ---------------------------------------------------------------------------
// Binary / Unary / Cast ops
// ---------------------------------------------------------------------------

func llvmBinaryOp(op string, typ typeinfo.Type) (string, error) {
	base, _ := llvmBaseType(typ)
	isFloat := base == "float" || base == "double"
	isSigned := llvmIsSigned(typ)
	switch op {
	case "+":
		if isFloat {
			return "fadd", nil
		}
		return "add", nil
	case "-":
		if isFloat {
			return "fsub", nil
		}
		return "sub", nil
	case "*":
		if isFloat {
			return "fmul", nil
		}
		return "mul", nil
	case "/":
		if isFloat {
			return "fdiv", nil
		}
		if isSigned {
			return "sdiv", nil
		}
		return "udiv", nil
	case "%":
		if isSigned {
			return "srem", nil
		}
		return "urem", nil
	case "&&":
		return "and", nil
	case "||":
		return "or", nil
	default:
		return "", fmt.Errorf("unsupported binary op %q", op)
	}
}

func llvmCompareOp(op string, typ typeinfo.Type) (string, string, error) {
	base, err := llvmBaseType(typ)
	if err != nil {
		return "", "", fmt.Errorf("compare op %q: %w", op, err)
	}
	isFloat := base == "float" || base == "double"
	isSigned := llvmIsSigned(typ)

	if isFloat {
		switch op {
		case "==":
			return "fcmp oeq", base, nil
		case "!=":
			return "fcmp one", base, nil
		case "<":
			return "fcmp olt", base, nil
		case "<=":
			return "fcmp ole", base, nil
		case ">":
			return "fcmp ogt", base, nil
		case ">=":
			return "fcmp oge", base, nil
		}
	}
	switch op {
	case "==":
		return "icmp eq", base, nil
	case "!=":
		return "icmp ne", base, nil
	case "<":
		if isSigned {
			return "icmp slt", base, nil
		}
		return "icmp ult", base, nil
	case "<=":
		if isSigned {
			return "icmp sle", base, nil
		}
		return "icmp ule", base, nil
	case ">":
		if isSigned {
			return "icmp sgt", base, nil
		}
		return "icmp ugt", base, nil
	case ">=":
		if isSigned {
			return "icmp sge", base, nil
		}
		return "icmp uge", base, nil
	}
	return "", "", fmt.Errorf("unsupported compare op %q", op)
}

func llvmIsSigned(typ typeinfo.Type) bool {
	b, ok := unwrapNamed(typ).(*typeinfo.BuiltinType)
	if !ok {
		return false
	}
	switch b.Name {
	case "i8", "i16", "i32", "i64", "isize":
		return true
	}
	return false
}

func llvmIntCastOp(_ *moduleState, src, dst string, srcVal string) (string, bool) {
	srcType := &typeinfo.BuiltinType{Name: src}
	dstType := &typeinfo.BuiltinType{Name: dst}
	srcIR, errS := llvmBaseType(srcType)
	dstIR, errD := llvmBaseType(dstType)
	if errS != nil || errD != nil {
		return "", false
	}
	srcSize := llvmTypeBits(src)
	dstSize := llvmTypeBits(dst)
	if srcSize == 0 || dstSize == 0 {
		return "", false
	}
	if srcSize == dstSize {
		return llvmCopyExprUnchecked(srcIR, srcVal), true
	}
	if dstSize > srcSize {
		if llvmIsSigned(srcType) {
			return fmt.Sprintf("sext %s %s to %s", srcIR, srcVal, dstIR), true
		}
		return fmt.Sprintf("zext %s %s to %s", srcIR, srcVal, dstIR), true
	}
	return fmt.Sprintf("trunc %s %s to %s", srcIR, srcVal, dstIR), true
}

func llvmFloatCastOp(src, dst string, srcVal string) (string, bool) {
	srcType := &typeinfo.BuiltinType{Name: src}
	dstType := &typeinfo.BuiltinType{Name: dst}
	srcIR, errS := llvmBaseType(srcType)
	dstIR, errD := llvmBaseType(dstType)
	if errS != nil || errD != nil {
		return "", false
	}
	switch {
	case src == dst:
		return llvmCopyExprUnchecked(srcIR, srcVal), true
	case src == "f32" && dst == "f64":
		return fmt.Sprintf("fpext float %s to double", srcVal), true
	case src == "f64" && dst == "f32":
		return fmt.Sprintf("fptrunc double %s to float", srcVal), true
	case isFloatType(src) && isIntType(dst):
		if llvmIsSigned(dstType) {
			return fmt.Sprintf("fptosi %s %s to %s", srcIR, srcVal, dstIR), true
		}
		return fmt.Sprintf("fptoui %s %s to %s", srcIR, srcVal, dstIR), true
	case isIntType(src) && isFloatType(dst):
		if llvmIsSigned(srcType) {
			return fmt.Sprintf("sitofp %s %s to %s", srcIR, srcVal, dstIR), true
		}
		return fmt.Sprintf("uitofp %s %s to %s", srcIR, srcVal, dstIR), true
	}
	return "", false
}

func isFloatType(name string) bool { return name == "f32" || name == "f64" }

func isIntType(name string) bool {
	switch name {
	case "bool", "u8", "i8", "u16", "i16", "u32", "i32", "char", "u64", "i64", "usize", "isize":
		return true
	}
	return false
}

func llvmTypeBits(name string) int {
	switch name {
	case "bool", "u8", "i8":
		return 8
	case "u16", "i16":
		return 16
	case "u32", "i32", "char":
		return 32
	case "u64", "i64", "usize", "isize":
		return 64
	case "f32":
		return 32
	case "f64":
		return 64
	}
	return 0
}

// ---------------------------------------------------------------------------
// Number / copy helpers
// ---------------------------------------------------------------------------

func llvmNumberLiteral(typ typeinfo.Type, lit string) (string, error) {
	typ = unwrapNamed(typ)
	if _, ok := typ.(*typeinfo.PointerType); ok {
		return lit, nil
	}
	b, ok := typ.(*typeinfo.BuiltinType)
	if !ok {
		return "", fmt.Errorf("unsupported numeric literal type %s", typ)
	}
	switch b.Name {
	case "f32", "f64":
		// LLVM accepts decimal float literals; just pass through.
		return lit, nil
	default:
		return lit, nil
	}
}

func llvmCopyExpr(irType, valExpr string) (string, error) {
	return llvmCopyExprUnchecked(irType, valExpr), nil
}

func llvmCopyExprUnchecked(irType, valExpr string) string {
	switch irType {
	case "float":
		return fmt.Sprintf("fadd float 0.0, %s", valExpr)
	case "double":
		return fmt.Sprintf("fadd double 0.0, %s", valExpr)
	case "ptr":
		return fmt.Sprintf("getelementptr i8, ptr %s, i64 0", valExpr)
	case "void":
		return valExpr
	default:
		return fmt.Sprintf("or %s 0, %s", irType, valExpr)
	}
}

func llvmValueNeedsCopy(v midmir.Value) bool {
	switch v.(type) {
	case *midmir.LocalValue, *midmir.NameValue,
		*midmir.NumberValue, *midmir.BoolValue,
		*midmir.NoneValue, *midmir.AddrOfValue:
		return true
	}
	return false
}

func isCompareOp(op string) bool {
	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Memcpy
// ---------------------------------------------------------------------------

func llvmMemcpy(dst, src string, size, align int64) string {
	if align <= 0 {
		align = 1
	}
	return fmt.Sprintf("call void @llvm.memcpy.p0.p0.i64(ptr align %d %s, ptr align %d %s, i64 %d, i1 false)",
		align, dst, align, src, size)
}

// ---------------------------------------------------------------------------
// Naming helpers
// ---------------------------------------------------------------------------

func llvmSymbol(state *moduleState, path []string) string {
	if len(path) == 0 {
		return "_"
	}
	if len(path) == 1 {
		name := sanitizeIdent(path[0])
		if state != nil {
			// The entry point: main() in the main module is always @main
			// so the C runtime can call it directly without a wrapper.
			if name == "main" && state.modulePrefix == "main" {
				if _, ok := state.functions["main"]; ok {
					return "main"
				}
			}
			if _, ok := state.functions[path[0]]; ok {
				return state.modulePrefix + "__" + name
			}
			if _, ok := state.globals[path[0]]; ok {
				return state.modulePrefix + "__" + name
			}
		}
		return name
	}
	clean := make([]string, 0, len(path))
	for _, part := range path {
		clean = append(clean, sanitizeIdent(part))
	}
	return strings.Join(clean, "__")
}

func llvmTypeName(state *moduleState, named *typeinfo.NamedType) string {
	if named == nil {
		return "type"
	}
	moduleKey := named.ModuleKey
	if moduleKey == "" && state != nil && state.mod != nil {
		moduleKey = state.mod.Key
	}
	prefix := sanitizePath(moduleKey)
	if prefix == "" {
		prefix = state.modulePrefix
	}
	return prefix + "__" + sanitizeIdent(named.Name)
}

func llvmBlockLabel(fn *midmir.Function, id int) string {
	if fn != nil && id == fn.EntryID {
		return "entry"
	}
	return fmt.Sprintf("bb%d", id)
}

func llvmLocalName(name string) string {
	if name == "" {
		return "%_tmp"
	}
	return "%" + sanitizeIdent(name)
}

func freshTemp(state *moduleState, prefix string) string {
	state.nextTemp++
	return fmt.Sprintf("%%_%s%d", prefix, state.nextTemp)
}

func normalizeAlign(align int64) int64 {
	switch {
	case align <= 1:
		return 1
	case align <= 2:
		return 2
	case align <= 4:
		return 4
	case align <= 8:
		return 8
	default:
		return 16
	}
}

func irTypeAlign(irType string) int64 {
	switch irType {
	case "i8":
		return 1
	case "i16":
		return 2
	case "i32", "float":
		return 4
	case "i64", "double", "ptr":
		return 8
	default:
		return 4
	}
}

// ---------------------------------------------------------------------------
// Local / function helpers
// ---------------------------------------------------------------------------

func findLocalByName(fn *midmir.Function, name string) *midmir.Local {
	if fn == nil {
		return nil
	}
	for _, local := range fn.Locals {
		if local != nil && local.Name == name {
			return local
		}
	}
	return nil
}

func localNameByID(fn *midmir.Function, id int) string {
	if fn == nil {
		return fmt.Sprintf("t%d", id)
	}
	for _, local := range fn.Locals {
		if local != nil && local.ID == id {
			return local.Name
		}
	}
	return fmt.Sprintf("t%d", id)
}

func localTypeByID(fn *midmir.Function, id int) typeinfo.Type {
	if fn == nil {
		return typeinfo.UnknownType{}
	}
	for _, local := range fn.Locals {
		if local != nil && local.ID == id {
			return local.Type
		}
	}
	return typeinfo.UnknownType{}
}

// ---------------------------------------------------------------------------
// String helper
// ---------------------------------------------------------------------------

func llvmStringLiteral(s string) string {
	var b strings.Builder
	b.WriteString("c\"")
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c < 0x7f && c != '"' && c != '\\' {
			b.WriteByte(c)
		} else {
			b.WriteString(fmt.Sprintf("\\%02X", c))
		}
	}
	b.WriteString("\\00\"")
	return b.String()
}

// ---------------------------------------------------------------------------
// Sanitizers
// ---------------------------------------------------------------------------

func sanitizePath(path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		switch r {
		case '/', ':', '.', '-', ' ':
			return true
		}
		return false
	})
	if len(parts) == 0 {
		return "mod"
	}
	for i := range parts {
		parts[i] = sanitizeIdent(parts[i])
	}
	return strings.Join(parts, "__")
}

func sanitizeIdent(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for i, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "_"
	}
	return out
}
