package llvm

import (
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/analysis/semantics/semmeta"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/backend"
	becommon "compiler/internal/backend/common"
	"compiler/internal/ir/mir"
	"compiler/internal/utils/numeric"
)

type lowerer struct{}

type interfaceVTableKey struct {
	iface    string
	concrete string
}

type interfaceWrapperKey struct {
	iface    string
	concrete string
	method   string
}

type sharedLoweringState struct {
	interfaceVTables  map[interfaceVTableKey]string
	interfaceWrappers map[interfaceWrapperKey]struct{}
}

type moduleState struct {
	mod               *mir.Module
	layout            *layout.Module
	layouts           map[string]*layout.Module
	modules           map[string]*mir.Module
	fn                *mir.Function
	functions         map[string]struct{}
	globals           map[string]struct{}
	modulePrefix      string
	aggLocals         map[int]*aggregateLocal
	aggParams         map[int]struct{}
	scalarLocals      map[int]*scalarAllocaLocal // mutable scalar locals mapped to alloca ptrs
	nextTemp          int
	nextStrConst      int              // counter for unnamed string constant globals
	deferredB         *strings.Builder // deferred global definitions (e.g. string literals used in functions)
	pendingLines      []string         // extra load instructions to flush before each emitted line
	interfaceVTables  map[interfaceVTableKey]string
	interfaceWrappers map[interfaceWrapperKey]struct{}
	tempValues        map[int]mir.Value
	debug             *debugState // nil if debug info is disabled
	fnScopeID         int         // DISubprogram metadata ID for the current function
	debugLocalVarIDs  map[int]int // local ID -> DILocalVariable metadata ID (for dbg.value updates)
	shared            *sharedLoweringState
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
	emptyExpr      int            // cached !DIExpression() ID; -1 = not yet emitted
	emptyTuple     int            // cached !{} ID; -1 = not yet emitted
	unknownType    int            // cached generic !DIBasicType for unknown locals; -1 = not yet emitted
	stringType     int            // cached debug type for str; -1 = not yet emitted
	sliceType      int            // cached debug type for []T-like ABI shape; -1 = not yet emitted
	basicTypeIDs   map[string]int // builtin ferret type name -> DIBasicType metadata ID
	pointerTypeIDs map[int]int    // base type metadata ID -> pointer debug type metadata ID
	compositeIDs   map[string]int // logical composite key -> metadata ID
	buildingTypes  map[string]bool
}

func newDebugState() *debugState {
	return &debugState{
		nextID:         0,
		fileIDs:        make(map[string]int),
		subroutineType: -1,
		emptyExpr:      -1,
		emptyTuple:     -1,
		unknownType:    -1,
		stringType:     -1,
		sliceType:      -1,
		basicTypeIDs:   make(map[string]int),
		pointerTypeIDs: make(map[int]int),
		compositeIDs:   make(map[string]int),
		buildingTypes:  make(map[string]bool),
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

// getEmptyExpression returns (creating once) the empty DIExpression metadata.
func (d *debugState) getEmptyExpression() int {
	if d.emptyExpr >= 0 {
		return d.emptyExpr
	}
	d.emptyExpr = d.emit("!DIExpression()")
	return d.emptyExpr
}

func (d *debugState) getEmptyTuple() int {
	if d.emptyTuple >= 0 {
		return d.emptyTuple
	}
	d.emptyTuple = d.emit("!{}")
	return d.emptyTuple
}

// getUnknownType returns a generic fallback debug type for locals/params.
func (d *debugState) getUnknownType() int {
	if d.unknownType >= 0 {
		return d.unknownType
	}
	d.unknownType = d.emit(`!DIBasicType(name: "unknown", size: 0, encoding: DW_ATE_unsigned)`)
	return d.unknownType
}

func (d *debugState) getPointerType(baseTypeID int) int {
	if baseTypeID < 0 {
		baseTypeID = d.getUnknownType()
	}
	if id, ok := d.pointerTypeIDs[baseTypeID]; ok {
		return id
	}
	id := d.emit(fmt.Sprintf("!DIDerivedType(tag: DW_TAG_pointer_type, baseType: !%d, size: 64)", baseTypeID))
	d.pointerTypeIDs[baseTypeID] = id
	return id
}

func (d *debugState) getBasicType(name string, sizeBits int, encoding string) int {
	if id, ok := d.basicTypeIDs[name]; ok {
		return id
	}
	id := d.emit(fmt.Sprintf("!DIBasicType(name: %q, size: %d, encoding: %s)", name, sizeBits, encoding))
	d.basicTypeIDs[name] = id
	return id
}

func (d *debugState) getSliceLikeType(name string) int {
	if name == "str" && d.stringType >= 0 {
		return d.stringType
	}
	if name == "slice" && d.sliceType >= 0 {
		return d.sliceType
	}
	ptrID := d.getPointerType(d.getUnknownType())
	lenID := d.getBasicType("usize", 64, "DW_ATE_unsigned")
	dataMember := d.emit(fmt.Sprintf("!DIDerivedType(tag: DW_TAG_member, name: %q, baseType: !%d, size: 64, align: 64, offset: 0)", "data", ptrID))
	lenMember := d.emit(fmt.Sprintf("!DIDerivedType(tag: DW_TAG_member, name: %q, baseType: !%d, size: 64, align: 64, offset: 64)", "len", lenID))
	elemsID := d.emit(fmt.Sprintf("!{!%d, !%d}", dataMember, lenMember))
	id := d.emit(fmt.Sprintf("!DICompositeType(tag: DW_TAG_structure_type, name: %q, size: 128, align: 64, elements: !%d)", name, elemsID))
	if name == "str" {
		d.stringType = id
	} else if name == "slice" {
		d.sliceType = id
	}
	return id
}

// getOptionalType emits a DICompositeType for ?T shaped as { tag: i32, value: T }.
// If the optional uses a niche (pointer niche), the layout is just the inner type.
func (d *debugState) getOptionalType(state *moduleState, t *typeinfo.OptionalType) int {
	innerID := d.getType(state, t.Inner)
	key := fmt.Sprintf("?::%d", innerID)
	if id, ok := d.compositeIDs[key]; ok {
		return id
	}
	// Niche optionals (e.g. ?*T) have the same layout as the inner type.
	if backend.OptionalUsesNiche(t.Inner) {
		d.compositeIDs[key] = innerID
		return innerID
	}
	// Tag (i32) + inner value.
	tagTypeID := d.getBasicType("i32", 32, "DW_ATE_signed")
	tagMember := d.emit(fmt.Sprintf("!DIDerivedType(tag: DW_TAG_member, name: \"tag\", baseType: !%d, size: 32, align: 32, offset: 0)", tagTypeID))
	// Compute payload offset: must be aligned to inner type's alignment.
	innerSize := int64(32) // default
	innerAlign := int64(32)
	if state != nil {
		if sz, al, err := aggregateSizeAlignOfPrimitive(t.Inner); err == nil {
			innerSize = sz * 8
			innerAlign = al * 8
		} else if sz, al, err2 := backend.AggregateSizeAlign(aggregateLayoutContext(state), t.Inner); err2 == nil {
			innerSize = sz * 8
			innerAlign = al * 8
		}
	}
	payloadOffset := int64(32)
	if innerAlign > 32 {
		payloadOffset = innerAlign
	}
	totalSize := payloadOffset + innerSize
	valMember := d.emit(fmt.Sprintf("!DIDerivedType(tag: DW_TAG_member, name: \"value\", baseType: !%d, size: %d, align: %d, offset: %d)", innerID, innerSize, innerAlign, payloadOffset))
	elemsID := d.emit(fmt.Sprintf("!{!%d, !%d}", tagMember, valMember))
	id := d.emit(fmt.Sprintf("!DICompositeType(tag: DW_TAG_structure_type, name: \"option\", size: %d, align: %d, elements: !%d)", totalSize, innerAlign, elemsID))
	d.compositeIDs[key] = id
	return id
}

// getInterfaceType emits a DICompositeType for interface values: { data: ptr, vtable: ptr }.
func (d *debugState) getInterfaceType() int {
	key := "::iface"
	if id, ok := d.compositeIDs[key]; ok {
		return id
	}
	ptrID := d.getPointerType(d.getUnknownType())
	dataMember := d.emit(fmt.Sprintf("!DIDerivedType(tag: DW_TAG_member, name: \"data\", baseType: !%d, size: 64, align: 64, offset: 0)", ptrID))
	vtableMember := d.emit(fmt.Sprintf("!DIDerivedType(tag: DW_TAG_member, name: \"vtable\", baseType: !%d, size: 64, align: 64, offset: 64)", ptrID))
	elemsID := d.emit(fmt.Sprintf("!{!%d, !%d}", dataMember, vtableMember))
	id := d.emit("!DICompositeType(tag: DW_TAG_structure_type, name: \"iface\", size: 128, align: 64, elements: !" + fmt.Sprintf("%d)", elemsID))
	d.compositeIDs[key] = id
	return id
}

func (d *debugState) getNamedType(state *moduleState, named *typeinfo.NamedType) int {
	if named == nil {
		return d.getUnknownType()
	}
	// Interface named types always have a fixed {data ptr, vtable ptr} layout.
	if backend.IsNamedInterface(named) {
		id := d.getInterfaceType()
		key := named.ModuleKey + "::" + named.Name
		d.compositeIDs[key] = id
		return id
	}
	key := named.ModuleKey + "::" + named.Name
	if id, ok := d.compositeIDs[key]; ok {
		return id
	}
	if d.buildingTypes[key] {
		return d.getUnknownType()
	}
	if state == nil {
		return d.getUnknownType()
	}
	info, err := becommon.LookupNamedLayoutFromState(state.layouts, state.layout, state.mod, named, "llvm")
	if err != nil || info == nil || !info.Known {
		return d.getUnknownType()
	}
	d.buildingTypes[key] = true
	defer delete(d.buildingTypes, key)

	name := named.Name
	tag := "DW_TAG_structure_type"
	if info.Union != nil || backend.IsNamedUnion(named) {
		tag = "DW_TAG_union_type"
	}
	elemsID := d.getEmptyTuple()
	if info.Struct != nil {
		memberRefs := make([]string, 0, len(info.Struct.Fields))
		for _, field := range info.Struct.Fields {
			if field == nil {
				continue
			}
			memberTypeID := d.getType(state, field.Type)
			memberID := d.emit(fmt.Sprintf(
				"!DIDerivedType(tag: DW_TAG_member, name: %q, baseType: !%d, size: %d, align: %d, offset: %d)",
				field.Name, memberTypeID, field.Size*8, field.Align*8, field.Offset*8))
			memberRefs = append(memberRefs, fmt.Sprintf("!%d", memberID))
		}
		if len(memberRefs) > 0 {
			elemsID = d.emit(fmt.Sprintf("!{%s}", strings.Join(memberRefs, ", ")))
		}
	}
	align := info.Align * 8
	if align <= 0 {
		align = 8
	}
	id := d.emit(fmt.Sprintf("!DICompositeType(tag: %s, name: %q, size: %d, align: %d, elements: !%d)", tag, name, info.Size*8, align, elemsID))
	d.compositeIDs[key] = id
	return id
}

func (d *debugState) getType(state *moduleState, typ typeinfo.Type) int {
	if typ == nil {
		return d.getUnknownType()
	}
	if named, ok := typ.(*typeinfo.NamedType); ok {
		return d.getNamedType(state, named)
	}
	switch t := backend.UnwrapNamed(typ).(type) {
	case *typeinfo.BuiltinType:
		switch t.Name {
		case "bool", "u8":
			return d.getBasicType(t.Name, 8, "DW_ATE_unsigned")
		case "i8":
			return d.getBasicType(t.Name, 8, "DW_ATE_signed")
		case "u16":
			return d.getBasicType(t.Name, 16, "DW_ATE_unsigned")
		case "i16":
			return d.getBasicType(t.Name, 16, "DW_ATE_signed")
		case "u32":
			return d.getBasicType(t.Name, 32, "DW_ATE_unsigned")
		case "i32":
			return d.getBasicType(t.Name, 32, "DW_ATE_signed")
		case "char":
			return d.getBasicType(t.Name, 32, "DW_ATE_UTF")
		case "u64", "usize":
			return d.getBasicType(t.Name, 64, "DW_ATE_unsigned")
		case "i64", "isize":
			return d.getBasicType(t.Name, 64, "DW_ATE_signed")
		case "f32":
			return d.getBasicType(t.Name, 32, "DW_ATE_float")
		case "f64":
			return d.getBasicType(t.Name, 64, "DW_ATE_float")
		case "void":
			return d.getUnknownType()
		}
	case *typeinfo.PointerType:
		innerID := d.getType(state, t.Inner)
		return d.getPointerType(innerID)
	case *typeinfo.RefType:
		innerID := d.getType(state, t.Inner)
		return d.getPointerType(innerID)
	case *typeinfo.RawPtrType:
		innerID := d.getType(state, t.Inner)
		return d.getPointerType(innerID)
	case *typeinfo.StringType:
		return d.getSliceLikeType("str")
	case *typeinfo.SliceType:
		return d.getSliceLikeType("slice")
	case *typeinfo.OptionalType:
		return d.getOptionalType(state, t)
	case *typeinfo.InterfaceType:
		return d.getInterfaceType()
	}
	return d.getUnknownType()
}

// addLocalVariable creates DILocalVariable metadata for a parameter/local.
// Pass argIndex=0 for non-parameters.
func (d *debugState) addLocalVariable(state *moduleState, name string, varType typeinfo.Type, fileID, scopeID, line, argIndex int) int {
	typeID := d.getType(state, varType)
	if argIndex > 0 {
		return d.emit(fmt.Sprintf(
			`!DILocalVariable(name: %q, arg: %d, scope: !%d, file: !%d, line: %d, type: !%d)`,
			name, argIndex, scopeID, fileID, line, typeID))
	}
	return d.emit(fmt.Sprintf(
		`!DILocalVariable(name: %q, scope: !%d, file: !%d, line: %d, type: !%d)`,
		name, scopeID, fileID, line, typeID))
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
func LowerProgram(units []*backend.Unit, includeDebug bool) (string, error) {
	// Build a merged layouts map so every unit can resolve cross-module types.
	allLayouts := make(map[string]*layout.Module)
	for _, u := range units {
		if u != nil && u.Layout != nil {
			allLayouts[u.Module.Key] = u.Layout
		}
	}

	var dbg *debugState
	if includeDebug {
		dbg = newDebugState()
	}
	shared := &sharedLoweringState{
		interfaceVTables:  make(map[interfaceVTableKey]string),
		interfaceWrappers: make(map[interfaceWrapperKey]struct{}),
	}

	// Pass 1: collect all type declarations in dependency order (units are
	// already ordered imports-before-entry by the caller).
	seenTypes := make(map[string]struct{})
	var typeLines []string

	for _, unit := range units {
		if unit == nil || unit.Module == nil {
			continue
		}
		state := newProgramModuleState(unit, allLayouts, dbg, shared)
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
	if includeDebug {
		declLines = append(declLines, "declare void @llvm.dbg.declare(metadata, metadata, metadata)")
		declLines = append(declLines, "declare void @llvm.dbg.value(metadata, metadata, metadata)")
	}
	for _, unit := range units {
		if unit == nil || unit.Module == nil {
			continue
		}
		// We need a temporary state only to resolve return types.
		tmpState := newProgramModuleState(unit, allLayouts, dbg, shared)
		callDecls, err := collectExternCallDecls(tmpState, unit.Module, seenExterns)
		if err != nil {
			return "", fmt.Errorf("collect extern call declarations [%s]: %w", unit.Module.ImportPath, err)
		}
		declLines = append(declLines, callDecls...)
		for _, fn := range externFunctionsForUnit(unit) {
			if fn == nil || !fn.IsExtern || fn.LinkName == "" {
				continue
			}
			sym := becommon.SanitizeIdent(fn.LinkName)
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
	declLines = append(runtimeDecls(), declLines...)

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
		state := newProgramModuleState(unit, allLayouts, dbg, shared)

		// Create one DICompileUnit per module source file.
		if state.debug != nil && unit.Module.FilePath != "" {
			fileID := dbg.getFile(unit.Module.FilePath)
			state.debug.addCU(fileID)
		}

		if err := emitGlobals(&b, state, unit.Module.Globals); err != nil {
			return "", fmt.Errorf("lower program globals [%s]: %w", unit.Module.ImportPath, err)
		}
		for _, fn := range unit.Module.Functions {
			if fn == nil || fn.IsExtern {
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
	if dbg != nil && len(dbg.cuIDs) > 0 {
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

func runtimeDecls() []string {
	return []string{
		"declare void @ferret__panic(ptr)",
		"declare void @ferret__bounds_check(i64, i64)",
		"declare void @ferret__interface_panic(ptr, ptr)",
		"declare void @global__panic(ptr)",
		"declare { ptr, i64 } @ferret_global_str_bytes(ptr)",
		"declare { ptr, i64 } @ferret_global_bytes_str(ptr)",
		"declare { ptr, i64 } @ferret_global_str_chars(ptr)",
		"declare { ptr, i64 } @ferret_global_chars_str(ptr)",
		"declare { ptr, i64 } @ferret_global_i64_str(i64)",
		"declare { ptr, i64 } @ferret_global_u64_str(i64)",
		"declare { ptr, i64 } @ferret_global_f64_str(double)",
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
func llvmExternDecl(state *moduleState, fn *mir.Function) (string, error) {
	sym := becommon.SanitizeIdent(fn.LinkName)

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

	// Extern signatures may be unavailable or incomplete in some standalone
	// lowering paths. Emit a variadic declaration so typed call-sites stay valid
	// while still binding to the intended symbol.
	return fmt.Sprintf("declare %s @%s(...)", retStr, sym), nil
}

func externFunctionsForUnit(unit *backend.Unit) []*mir.Function {
	if unit == nil {
		return nil
	}
	out := make([]*mir.Function, 0)
	seenMods := make(map[string]struct{})
	appendFrom := func(mod *mir.Module) {
		if mod == nil {
			return
		}
		if _, ok := seenMods[mod.Key]; ok {
			return
		}
		seenMods[mod.Key] = struct{}{}
		out = append(out, mod.Functions...)
	}
	appendFrom(unit.Module)
	for _, mod := range unit.Modules {
		appendFrom(mod)
	}
	return out
}

func collectExternCallDecls(state *moduleState, mod *mir.Module, seenExterns map[string]struct{}) ([]string, error) {
	if state == nil || mod == nil {
		return nil, nil
	}
	decls := make([]string, 0)
	addCall := func(sym string, call *mir.CallValue) error {
		if sym == "" {
			return nil
		}
		if _, ok := seenExterns[sym]; ok {
			return nil
		}
		ret, err := llvmExternReturnType(state, call.Type())
		if err != nil {
			return err
		}
		params := make([]string, 0, len(call.Args))
		for _, arg := range call.Args {
			if arg == nil {
				params = append(params, "ptr")
				continue
			}
			if isAggregateType(state, arg.Type()) {
				params = append(params, "ptr")
				continue
			}
			irType, terr := llvmBaseType(arg.Type())
			if terr != nil {
				decls = append(decls, fmt.Sprintf("declare %s @%s(...)", ret, sym))
				seenExterns[sym] = struct{}{}
				return nil
			}
			params = append(params, irType)
		}
		seenExterns[sym] = struct{}{}
		decls = append(decls, fmt.Sprintf("declare %s @%s(%s)", ret, sym, strings.Join(params, ", ")))
		return nil
	}
	var walkValue func(value mir.Value) error
	var walkPlace func(place mir.Place) error
	var walkInstr func(instr mir.Instr) error
	var walkTerm func(term mir.Terminator) error
	walkValue = func(value mir.Value) error {
		switch v := value.(type) {
		case nil, *mir.NameValue, *mir.LocalValue, *mir.NumberValue, *mir.BoolValue, *mir.StringValue, *mir.NoneValue:
			return nil
		case *mir.UnaryValue:
			return walkValue(v.Right)
		case *mir.BinaryValue:
			if err := walkValue(v.Left); err != nil {
				return err
			}
			return walkValue(v.Right)
		case *mir.PostfixValue:
			return walkValue(v.Left)
		case *mir.AddrOfValue:
			return walkValue(v.Source)
		case *mir.LoadValue:
			return walkValue(v.Pointer)
		case *mir.CallValue:
			if callee, ok := v.Callee.(*mir.NameValue); ok && callee.LinkName != "" {
				if err := addCall(becommon.SanitizeIdent(callee.LinkName), v); err != nil {
					return err
				}
			}
			if err := walkValue(v.Callee); err != nil {
				return err
			}
			for _, arg := range v.Args {
				if err := walkValue(arg); err != nil {
					return err
				}
			}
			return nil
		case *mir.FieldLoadValue:
			return walkValue(v.Base)
		case *mir.FieldValue:
			return walkValue(v.Base)
		case *mir.CastValue:
			return walkValue(v.Left)
		case *mir.TypeTestValue:
			return walkValue(v.Left)
		case *mir.CompositeValue:
			for _, item := range v.Items {
				if err := walkValue(item.Value); err != nil {
					return err
				}
			}
			return nil
		case *mir.InterfaceValue:
			return walkValue(v.Value)
		case *mir.IndexValue:
			if err := walkValue(v.Base); err != nil {
				return err
			}
			return walkValue(v.Index)
		default:
			return nil
		}
	}
	walkPlace = func(place mir.Place) error {
		switch p := place.(type) {
		case nil, *mir.LocalPlace:
			return nil
		case *mir.FieldPlace:
			return walkPlace(p.Base)
		case *mir.IndexPlace:
			if err := walkPlace(p.Base); err != nil {
				return err
			}
			return walkValue(p.Index)
		case *mir.DerefPlace:
			return walkValue(p.Pointer)
		default:
			return nil
		}
	}
	walkInstr = func(instr mir.Instr) error {
		switch i := instr.(type) {
		case nil:
			return nil
		case *mir.AssignInstr:
			return walkValue(i.Value)
		case *mir.ComputeInstr:
			return walkValue(i.Value)
		case *mir.StoreInstr:
			if err := walkPlace(i.Target); err != nil {
				return err
			}
			return walkValue(i.Value)
		case *mir.StoreFieldInstr:
			if err := walkValue(i.Base); err != nil {
				return err
			}
			return walkValue(i.Value)
		case *mir.EvalInstr:
			return walkValue(i.Value)
		case *mir.BindInstr:
			return walkValue(i.Value)
		case *mir.LockInstr:
			return walkValue(i.Value)
		case *mir.DeferInstr:
			for _, child := range i.Body {
				if err := walkInstr(child); err != nil {
					return err
				}
			}
			return nil
		default:
			return nil
		}
	}
	walkTerm = func(term mir.Terminator) error {
		switch t := term.(type) {
		case nil:
			return nil
		case *mir.BranchTerm:
			return walkValue(t.Cond)
		case *mir.SwitchTerm:
			if err := walkValue(t.Value); err != nil {
				return err
			}
			for _, kase := range t.Cases {
				if err := walkValue(kase.Expr); err != nil {
					return err
				}
			}
			return nil
		case *mir.ReturnTerm:
			return walkValue(t.Value)
		case *mir.PanicTerm:
			return walkValue(t.Value)
		default:
			return nil
		}
	}
	for _, global := range mod.Globals {
		if global == nil {
			continue
		}
		if err := walkValue(global.Init); err != nil {
			return nil, err
		}
	}
	for _, fn := range mod.Functions {
		if fn == nil {
			continue
		}
		for _, block := range fn.Blocks {
			if block == nil {
				continue
			}
			for _, instr := range block.Instructions {
				if err := walkInstr(instr); err != nil {
					return nil, err
				}
			}
			if err := walkTerm(block.Terminator); err != nil {
				return nil, err
			}
		}
	}
	return decls, nil
}

func llvmExternReturnType(state *moduleState, result typeinfo.Type) (string, error) {
	if result == nil || isVoidType(result) {
		return "void", nil
	}
	if isAggregateType(state, result) {
		if ret, err := llvmABITypeName(state, result); err == nil {
			return ret, nil
		}
		return "ptr", nil
	}
	if ret, err := llvmBaseType(result); err == nil {
		return ret, nil
	}
	return "ptr", nil
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
	if unit == nil || unit.Module == nil {
		shared := &sharedLoweringState{
			interfaceVTables:  make(map[interfaceVTableKey]string),
			interfaceWrappers: make(map[interfaceWrapperKey]struct{}),
		}
		return &moduleState{
			layouts:           allLayouts,
			functions:         make(map[string]struct{}),
			globals:           make(map[string]struct{}),
			modulePrefix:      becommon.SanitizePath(""),
			deferredB:         &strings.Builder{},
			interfaceVTables:  shared.interfaceVTables,
			interfaceWrappers: shared.interfaceWrappers,
			tempValues:        make(map[int]mir.Value),
			shared:            shared,
		}
	}
	modulePrefix, functions, globals := becommon.BuildModuleSymbolTables(unit.Module)
	shared := &sharedLoweringState{
		interfaceVTables:  make(map[interfaceVTableKey]string),
		interfaceWrappers: make(map[interfaceWrapperKey]struct{}),
	}
	state := &moduleState{
		mod:               unit.Module,
		layout:            unit.Layout,
		layouts:           allLayouts,
		modules:           unit.Modules,
		functions:         functions,
		globals:           globals,
		modulePrefix:      modulePrefix,
		deferredB:         &strings.Builder{},
		interfaceVTables:  shared.interfaceVTables,
		interfaceWrappers: shared.interfaceWrappers,
		tempValues:        make(map[int]mir.Value),
		shared:            shared,
	}
	return state
}

func newProgramModuleState(unit *backend.Unit, allLayouts map[string]*layout.Module, dbg *debugState, shared *sharedLoweringState) *moduleState {
	state := newModuleStateWithDebug(unit, allLayouts, dbg)
	if shared == nil {
		return state
	}
	state.shared = shared
	state.interfaceVTables = shared.interfaceVTables
	state.interfaceWrappers = shared.interfaceWrappers
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
	for _, decl := range runtimeDecls() {
		b.WriteString(decl)
		b.WriteByte('\n')
	}
	for _, decl := range implicitExternDecls() {
		b.WriteString(decl)
		b.WriteByte('\n')
	}
	seenExterns := make(map[string]struct{})
	callDecls, err := collectExternCallDecls(state, unit.Module, seenExterns)
	if err != nil {
		return nil, err
	}
	for _, decl := range callDecls {
		b.WriteString(decl)
		b.WriteByte('\n')
	}
	for _, fn := range externFunctionsForUnit(unit) {
		if fn == nil || !fn.IsExtern || fn.LinkName == "" {
			continue
		}
		sym := becommon.SanitizeIdent(fn.LinkName)
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
		if fn == nil || fn.IsExtern {
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

func emitTypes(b *strings.Builder, state *moduleState, types []*mir.TypeDecl) error {
	seen := make(map[string]struct{})
	emitDecls := func(decls []*mir.TypeDecl) error {
		for _, decl := range decls {
			if decl == nil || decl.Named == nil || (decl.Struct == nil && decl.Union == nil && decl.Interface == nil) {
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
	if err := emitDecls(types); err != nil {
		return err
	}
	if state.modules != nil {
		keys := make([]string, 0, len(state.modules))
		for key := range state.modules {
			if state.mod != nil && key == state.mod.Key {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := emitDecls(state.modules[key].Types); err != nil {
				return err
			}
		}
	}
	return nil
}

func lowerTypeDecl(state *moduleState, decl *mir.TypeDecl) (string, error) {
	if decl == nil || decl.Named == nil || (decl.Struct == nil && decl.Union == nil && decl.Interface == nil) {
		return "", nil
	}
	layoutInfo, err := becommon.LookupNamedLayoutFromState(state.layouts, state.layout, state.mod, decl.Named, "llvm")
	if err != nil {
		return "", err
	}
	if layoutInfo == nil || !layoutInfo.Known {
		return "", fmt.Errorf("type %s: unknown layout", decl.Name)
	}
	if decl.Struct != nil {
		if layoutInfo.Struct == nil {
			return "", fmt.Errorf("type %s: unknown struct layout", decl.Name)
		}
		body, err := llvmStructBody(state, layoutInfo.Struct)
		if err != nil {
			return "", fmt.Errorf("type %s: %w", decl.Name, err)
		}
		return fmt.Sprintf("%%%s = type { %s }", llvmTypeName(state, decl.Named), body), nil
	}
	if decl.Union != nil {
		return fmt.Sprintf("%%%s = type [%"+"d x i8]", llvmTypeName(state, decl.Named), layoutInfo.Size), nil
	}
	if decl.Interface != nil {
		return fmt.Sprintf("%%%s = type { ptr, ptr }", llvmTypeName(state, decl.Named)), nil
	}
	return "", nil
}

// ---------------------------------------------------------------------------
// Globals
// ---------------------------------------------------------------------------

func emitGlobals(b *strings.Builder, state *moduleState, globals []*mir.Global) error {
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

func lowerGlobal(state *moduleState, g *mir.Global) (string, error) {
	if g.Init == nil {
		return "", nil
	}
	name := llvmSymbol(state, []string{g.Name})
	if isInterfaceAggregate(g.Type) {
		if init, ok := g.Init.(*mir.InterfaceValue); ok {
			return lowerGlobalInterface(state, name, g.Type, init)
		}
		return "", fmt.Errorf("global %s: unsupported interface initializer %T", g.Name, g.Init)
	}
	if isUnionAggregate(g.Type) {
		body, err := lowerGlobalUnion(state, g.Type, g.Init)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		size, _, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), g.Type)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		return fmt.Sprintf("@%s = global [%d x i8] %s", name, size, body), nil
	}
	switch v := g.Init.(type) {
	case *mir.CompositeValue:
		body, err := lowerGlobalComposite(state, g.Type, v)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		typeName, err := llvmABITypeName(state, g.Type)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		return fmt.Sprintf("@%s = global %s { %s }", name, typeName, body), nil
	case *mir.NumberValue:
		irType, err := llvmBaseType(g.Type)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		lit, err := llvmNumberLiteral(g.Type, v.Value)
		if err != nil {
			return "", fmt.Errorf("global %s: %w", g.Name, err)
		}
		return fmt.Sprintf("@%s = global %s %s", name, irType, lit), nil
	case *mir.BoolValue:
		lit := "0"
		if v.Value {
			lit = "1"
		}
		return fmt.Sprintf("@%s = global i8 %s", name, lit), nil
	case *mir.StringValue:
		escaped := llvmStringLiteral(v.Value)
		length := len(v.Value) + 1
		return fmt.Sprintf("@%s = private unnamed_addr constant [%d x i8] %s", name, length, escaped), nil
	default:
		return "", fmt.Errorf("global %s: unsupported initializer %T", g.Name, g.Init)
	}
}

func lowerGlobalInterface(state *moduleState, name string, target typeinfo.Type, init *mir.InterfaceValue) (string, error) {
	dataPtr, err := lowerGlobalInterfaceData(state, name, init)
	if err != nil {
		return "", err
	}
	vtSym, methodCount, err := ensureLLVMInterfaceVTable(state, target, init)
	if err != nil {
		return "", err
	}
	vtPtr := fmt.Sprintf("getelementptr inbounds ([%d x ptr], ptr @%s, i32 0, i32 0)", methodCount, vtSym)
	typeName, err := llvmABITypeName(state, target)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("@%s = global %s { ptr %s, ptr %s }", name, typeName, dataPtr, vtPtr), nil
}

func lowerGlobalInterfaceData(state *moduleState, ownerName string, init *mir.InterfaceValue) (string, error) {
	switch v := init.Value.(type) {
	case *mir.NameValue:
		if v.LinkName != "" {
			return "@" + becommon.SanitizeIdent(v.LinkName), nil
		}
		return "@" + llvmSymbol(state, v.Path), nil
	case *mir.NumberValue:
		irType, err := llvmBaseType(v.Type())
		if err != nil {
			return "", err
		}
		lit, err := llvmNumberLiteral(v.Type(), v.Value)
		if err != nil {
			return "", err
		}
		sym := becommon.SanitizeIdent(ownerName + "__iface_data")
		fmt.Fprintf(state.deferredB, "@%s = private global %s %s\n", sym, irType, lit)
		return "@" + sym, nil
	case *mir.BoolValue:
		sym := becommon.SanitizeIdent(ownerName + "__iface_data")
		lit := "0"
		if v.Value {
			lit = "1"
		}
		fmt.Fprintf(state.deferredB, "@%s = private global i8 %s\n", sym, lit)
		return "@" + sym, nil
	default:
		return "", fmt.Errorf("unsupported interface global concrete value %T", init.Value)
	}
}

func lowerGlobalUnion(state *moduleState, typ typeinfo.Type, init mir.Value) (string, error) {
	info, err := llvmUnionLayoutInfo(state, typ)
	if err != nil {
		return "", err
	}
	bytes, err := llvmUnionInitBytes(info, init)
	if err != nil {
		return "", err
	}
	if int64(len(bytes)) > info.Size {
		return "", fmt.Errorf("union initializer is larger than destination storage")
	}
	for int64(len(bytes)) < info.Size {
		bytes = append(bytes, 0)
	}
	parts := make([]string, 0, len(bytes))
	for _, b := range bytes {
		parts = append(parts, fmt.Sprintf("i8 %d", b))
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, ", ")), nil
}

func llvmUnionInitBytes(info *backendUnionLayout, init mir.Value) ([]byte, error) {
	if info == nil {
		return nil, fmt.Errorf("nil union layout")
	}
	memberIndex, err := llvmUnionMemberIndex(info.Members, init.Type())
	if err != nil {
		return nil, err
	}
	tagBytes, err := llvmScalarBytesFromNumber("u32", strconv.Itoa(memberIndex))
	if err != nil {
		return nil, err
	}
	bytes := make([]byte, 0, info.Size)
	bytes = append(bytes, tagBytes...)
	for int64(len(bytes)) < info.PayloadOffset {
		bytes = append(bytes, 0)
	}
	switch v := init.(type) {
	case *mir.BoolValue:
		if v.Value {
			bytes = append(bytes, 1)
			return bytes, nil
		}
		bytes = append(bytes, 0)
		return bytes, nil
	case *mir.NumberValue:
		builtin, ok := backend.UnwrapNamed(v.Type()).(*typeinfo.BuiltinType)
		if !ok {
			return nil, fmt.Errorf("unsupported union numeric initializer type %s", v.Type())
		}
		payload, err := llvmScalarBytesFromNumber(builtin.Name, v.Value)
		if err != nil {
			return nil, err
		}
		bytes = append(bytes, payload...)
		return bytes, nil
	default:
		return nil, fmt.Errorf("unsupported union global initializer %T", init)
	}
}

func llvmScalarBytesFromNumber(typeName, raw string) ([]byte, error) {
	value, err := numeric.StringToBigInt(raw)
	if err != nil {
		return nil, err
	}
	width := 0
	signed := false
	switch typeName {
	case "i8":
		width, signed = 1, true
	case "u8", "bool":
		width = 1
	case "i16":
		width, signed = 2, true
	case "u16":
		width = 2
	case "i32", "char":
		width, signed = 4, true
	case "u32":
		width = 4
	case "i64", "isize":
		width, signed = 8, true
	case "u64", "usize":
		width = 8
	default:
		return nil, fmt.Errorf("unsupported union numeric initializer type %s", typeName)
	}
	mod := new(big.Int).Lsh(big.NewInt(1), uint(width*8))
	if signed && value.Sign() < 0 {
		value = new(big.Int).Add(value, mod)
	}
	bytes := make([]byte, width)
	buf := value.Bytes()
	for i := 0; i < len(buf) && i < width; i++ {
		bytes[i] = buf[len(buf)-1-i]
	}
	return bytes, nil
}

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

func emitFunction(b *strings.Builder, state *moduleState, fn *mir.Function) error {
	if err := prepareFunctionState(state, fn); err != nil {
		return err
	}

	name := fn.LinkName
	if name == "" {
		name = llvmSymbol(state, []string{fn.Name})
	} else {
		name = becommon.SanitizeIdent(name)
	}

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
			_, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), param.Type)
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
	blocks := append([]*mir.Block(nil), fn.Blocks...)
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
			for _, line := range entryDebugDecls(state) {
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

func entryDebugDecls(state *moduleState) []string {
	if state == nil || state.fn == nil || state.debug == nil || state.fnScopeID < 0 {
		return nil
	}

	paramIDs := make(map[int]struct{}, len(state.fn.Params))
	lines := make([]string, 0, len(state.fn.Params)+len(state.fn.Locals))
	argIndex := 1

	emitDecl := func(localID int, name string, varType typeinfo.Type, ptrName, filePath string, line, arg int) {
		if name == "" || ptrName == "" || filePath == "" {
			return
		}
		if line <= 0 {
			line = 1
		}
		fileID := state.debug.getFile(filePath)
		varID := state.debug.addLocalVariable(state, name, varType, fileID, state.fnScopeID, line, arg)
		state.debugLocalVarIDs[localID] = varID
		exprID := state.debug.getEmptyExpression()
		locID := state.debug.addLocation(line, 1, state.fnScopeID)
		lines = append(lines, fmt.Sprintf("call void @llvm.dbg.declare(metadata ptr %s, metadata !%d, metadata !%d), !dbg !%d", ptrName, varID, exprID, locID))
	}

	for _, param := range state.fn.Params {
		if param == nil {
			continue
		}
		paramIDs[param.LocalID] = struct{}{}
		filePath := param.Location.File
		if filePath == "" {
			filePath = state.fn.Location.File
		}
		line := 1
		if param.Location.Start != nil {
			line = param.Location.Start.Line
		} else if state.fn.Location.Start != nil {
			line = state.fn.Location.Start.Line
		}
		if _, ok := state.aggParams[param.LocalID]; ok {
			emitDecl(param.LocalID, param.Name, param.Type, llvmLocalName(param.Name), filePath, line, argIndex)
			argIndex++
			continue
		}
		if sc, ok := state.scalarLocals[param.LocalID]; ok {
			emitDecl(param.LocalID, param.Name, param.Type, sc.AllocaName, filePath, line, argIndex)
			argIndex++
		}
	}

	for _, local := range state.fn.Locals {
		if local == nil || local.IsTemp {
			continue
		}
		if _, isParam := paramIDs[local.ID]; isParam {
			continue
		}
		filePath := local.Location.File
		if filePath == "" {
			filePath = state.fn.Location.File
		}
		line := 1
		if local.Location.Start != nil {
			line = local.Location.Start.Line
		} else if state.fn.Location.Start != nil {
			line = state.fn.Location.Start.Line
		}
		if sc, ok := state.scalarLocals[local.ID]; ok {
			emitDecl(local.ID, local.Name, local.Type, sc.AllocaName, filePath, line, 0)
			continue
		}
		if agg, ok := state.aggLocals[local.ID]; ok {
			if _, isParam := state.aggParams[local.ID]; isParam {
				continue
			}
			emitDecl(local.ID, local.Name, local.Type, llvmLocalName(agg.PtrName), filePath, line, 0)
		}
	}

	return lines
}

// ---------------------------------------------------------------------------
// Instructions
// ---------------------------------------------------------------------------

func lowerInstr(state *moduleState, instr mir.Instr) (string, error) {
	switch i := instr.(type) {
	case nil:
		return "", nil
	case *mir.BindInstr:
		return lowerAssignLike(state, i.Name, i.Type, i.Value)
	case *mir.AssignInstr:
		if local := becommon.FindLocalByID(state.fn, i.TargetID); local != nil && local.IsTemp {
			if field, ok := i.Value.(*mir.FieldValue); ok && isInterfaceAggregate(field.Base.Type()) {
				state.tempValues[i.TargetID] = field
				return "", nil
			}
		}
		name := becommon.LocalNameByID(state.fn, i.TargetID)
		return lowerAssignLike(state, name, becommon.LocalTypeByID(state.fn, i.TargetID), i.Value)
	case *mir.ComputeInstr:
		if local := becommon.FindLocalByID(state.fn, i.TargetID); local != nil && local.IsTemp {
			if field, ok := i.Value.(*mir.FieldValue); ok && isInterfaceAggregate(field.Base.Type()) {
				state.tempValues[i.TargetID] = field
				return "", nil
			}
		}
		name := becommon.LocalNameByID(state.fn, i.TargetID)
		return lowerAssignLike(state, name, i.Type, i.Value)
	case *mir.StoreFieldInstr:
		return lowerStoreField(state, i)
	case *mir.StoreInstr:
		return lowerStorePlace(state, i)
	case *mir.EvalInstr:
		if call, ok := i.Value.(*mir.CallValue); ok {
			return lowerCall(state, "", nil, call)
		}
		return "", nil
	case *mir.DeferInstr:
		// Defers are compile-time cleanup markers. CFG cleanup edges already
		// materialize their bodies in dedicated cleanup blocks, so there is no
		// direct runtime instruction to emit at the registration site.
		return "", nil
	case *mir.UnsafeInstr:
		// Unsafe marker: safety is enforced at type-check time; no code to emit.
		return "", nil
	default:
		return "", fmt.Errorf("unsupported MIR instruction %T", instr)
	}
}

func lowerAssignLike(state *moduleState, name string, typ typeinfo.Type, value mir.Value) (string, error) {
	// Route scalar alloca targets: compute to a fresh temp then store.
	local := becommon.FindLocalByName(state.fn, name)
	if local != nil {
		if sc, ok := state.scalarLocals[local.ID]; ok {
			return lowerScalarAllocaAssign(state, sc, typ, value)
		}
	}
	line, err := lowerSSAAssign(state, name, typ, value)
	if err != nil {
		return "", err
	}
	if local != nil {
		line = appendDbgValueForLocal(state, line, local, typ)
	}
	return line, nil
}

func appendDbgValueForLocal(state *moduleState, line string, local *mir.Local, typ typeinfo.Type) string {
	if state == nil || state.debug == nil || state.fnScopeID < 0 || local == nil || local.IsTemp || line == "" {
		return line
	}
	if _, allocaBacked := state.scalarLocals[local.ID]; allocaBacked {
		return line
	}
	if _, aggregateBacked := state.aggLocals[local.ID]; aggregateBacked {
		return line
	}
	varID, ok := state.debugLocalVarIDs[local.ID]
	if !ok {
		return line
	}
	irType, err := llvmBaseType(typ)
	if err != nil || irType == "void" {
		return line
	}
	valueExpr := fmt.Sprintf("%s %s", irType, llvmLocalName(local.Name))
	exprID := state.debug.getEmptyExpression()
	return line + "\n" + fmt.Sprintf("call void @llvm.dbg.value(metadata %s, metadata !%d, metadata !%d)", valueExpr, varID, exprID)
}

// lowerScalarAllocaAssign computes value to a fresh SSA temp then stores to alloca.
func lowerScalarAllocaAssign(state *moduleState, sc *scalarAllocaLocal, typ typeinfo.Type, value mir.Value) (string, error) {
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
func lowerSSAAssign(state *moduleState, name string, typ typeinfo.Type, value mir.Value) (string, error) {
	if local := becommon.FindLocalByName(state.fn, name); local != nil {
		if agg, ok := state.aggLocals[local.ID]; ok {
			return lowerAggregateAssign(state, agg, value)
		}
	}
	if call, ok := value.(*mir.CallValue); ok {
		return lowerCall(state, name, typ, call)
	}
	if field, ok := value.(*mir.FieldLoadValue); ok {
		return lowerFieldLoad(state, name, typ, field)
	}
	if field, ok := value.(*mir.FieldValue); ok {
		return lowerResolvedFieldValueLoad(state, name, typ, field)
	}
	if idx, ok := value.(*mir.IndexValue); ok {
		return lowerIndexLoad(state, name, typ, idx)
	}
	if load, ok := value.(*mir.LoadValue); ok {
		return lowerLoadValue(state, name, typ, load)
	}
	if bin, ok := value.(*mir.BinaryValue); ok {
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
	if un, ok := value.(*mir.UnaryValue); ok && un.Op == "!" {
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
func lowerCompareAssign(state *moduleState, name, targetIRType string, bin *mir.BinaryValue) (string, error) {
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
func lowerNotAssign(state *moduleState, name, targetIRType string, un *mir.UnaryValue) (string, error) {
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

func lowerAggregateCompare(state *moduleState, targetName string, targetType typeinfo.Type, bin *mir.BinaryValue) (string, bool, error) {
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
	if _, ok := backend.UnwrapNamed(bin.Left.Type()).(*typeinfo.StringType); ok {
		if _, ok := backend.UnwrapNamed(bin.Right.Type()).(*typeinfo.StringType); ok {
			leftBase, err := lowerValue(state, bin.Left)
			if err != nil {
				return "", true, err
			}
			rightBase, err := lowerValue(state, bin.Right)
			if err != nil {
				return "", true, err
			}
			leftPtr := freshTemp(state, "str_ptr")
			rightPtr := freshTemp(state, "str_ptr")
			leftLenAddr := freshTemp(state, "str_len_addr")
			rightLenAddr := freshTemp(state, "str_len_addr")
			leftLen := freshTemp(state, "str_len")
			rightLen := freshTemp(state, "str_len")
			lenEq := freshTemp(state, "str_len_eq")
			memcmpTmp := freshTemp(state, "str_cmp")
			bytesEq := freshTemp(state, "str_bytes_eq")
			result := freshTemp(state, "str_eq")
			lines := []string{
				fmt.Sprintf("%s = load ptr, ptr %s", leftPtr, leftBase),
				fmt.Sprintf("%s = load ptr, ptr %s", rightPtr, rightBase),
				fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", leftLenAddr, leftBase),
				fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", rightLenAddr, rightBase),
				fmt.Sprintf("%s = load i64, ptr %s", leftLen, leftLenAddr),
				fmt.Sprintf("%s = load i64, ptr %s", rightLen, rightLenAddr),
				fmt.Sprintf("%s = icmp eq i64 %s, %s", lenEq, leftLen, rightLen),
				fmt.Sprintf("%s = call i32 @memcmp(ptr %s, ptr %s, i64 %s)", memcmpTmp, leftPtr, rightPtr, leftLen),
				fmt.Sprintf("%s = icmp eq i32 %s, 0", bytesEq, memcmpTmp),
				fmt.Sprintf("%s = and i1 %s, %s", result, lenEq, bytesEq),
			}
			final := result
			if bin.Op == "!=" {
				neq := freshTemp(state, "str_ne")
				lines = append(lines, fmt.Sprintf("%s = xor i1 %s, true", neq, result))
				final = neq
			}
			if targetIRType == "i1" || targetIRType == "" {
				lines = append(lines, fmt.Sprintf("%s = or i1 0, %s", llvmLocalName(targetName), final))
			} else {
				lines = append(lines, fmt.Sprintf("%s = zext i1 %s to %s", llvmLocalName(targetName), final, targetIRType))
			}
			return strings.Join(lines, "\n"), true, nil
		}
	}
	leftStruct, err := becommon.LookupStructLayoutFromState(state.layouts, state.layout, state.mod, bin.Left.Type(), "llvm")
	if err != nil {
		return "", true, err
	}
	rightStruct, err := becommon.LookupStructLayoutFromState(state.layouts, state.layout, state.mod, bin.Right.Type(), "llvm")
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

func lowerLoadValue(state *moduleState, targetName string, targetType typeinfo.Type, load *mir.LoadValue) (string, error) {
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

func lowerFieldLoad(state *moduleState, targetName string, targetType typeinfo.Type, field *mir.FieldLoadValue) (string, error) {
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

func lowerFieldValueLoad(state *moduleState, targetName string, targetType typeinfo.Type, base mir.Value, fieldIndex int) (string, error) {
	lines, addr, _, err := lowerFieldAddress(state, base, fieldIndex)
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

func lowerResolvedFieldValueLoad(state *moduleState, targetName string, targetType typeinfo.Type, field *mir.FieldValue) (string, error) {
	if field == nil {
		return "", fmt.Errorf("nil field value")
	}
	fieldIndex, err := llvmResolveFieldIndex(state, llvmFieldBaseType(state, field.Base), field.FieldIndex, field.MemberName)
	if err != nil {
		return "", err
	}
	return lowerFieldValueLoad(state, targetName, targetType, field.Base, fieldIndex)
}

func llvmResolveFieldIndex(state *moduleState, baseType typeinfo.Type, fieldIndex int, memberName string) (int, error) {
	if fieldIndex >= 0 {
		return fieldIndex, nil
	}
	structLayout, err := becommon.LookupStructLayoutFromState(state.layouts, state.layout, state.mod, baseType, "llvm")
	if err != nil {
		return -1, err
	}
	for _, field := range structLayout.Fields {
		if field != nil && field.Name == memberName {
			return field.SemanticIndex, nil
		}
	}
	return -1, fmt.Errorf("unknown field %q", memberName)
}

// lowerIndexLoad lowers arr[index] as an rvalue load.
func lowerIndexLoad(state *moduleState, targetName string, targetType typeinfo.Type, idx *mir.IndexValue) (string, error) {
	if _, ok := backend.UnwrapNamed(idx.Base.Type()).(*typeinfo.SliceType); ok {
		return lowerSliceIndexLoad(state, targetName, targetType, idx)
	}
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

func lowerSliceIndexLoad(state *moduleState, targetName string, targetType typeinfo.Type, idx *mir.IndexValue) (string, error) {
	baseExpr, err := lowerValue(state, idx.Base)
	if err != nil {
		return "", err
	}
	indexExpr, err := lowerValue(state, idx.Index)
	if err != nil {
		return "", err
	}
	elemIRType, err := llvmBaseType(targetType)
	if err != nil {
		return "", err
	}
	dataPtr := freshTemp(state, "slice_data")
	lenAddr := freshTemp(state, "slice_len_addr")
	lenVal := freshTemp(state, "slice_len")
	elemPtr := freshTemp(state, "elem")
	loadElem := freshTemp(state, "idx")
	lines := []string{
		fmt.Sprintf("%s = load ptr, ptr %s", dataPtr, baseExpr),
		fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", lenAddr, baseExpr),
		fmt.Sprintf("%s = load i64, ptr %s", lenVal, lenAddr),
		fmt.Sprintf("call void @ferret__bounds_check(i64 %s, i64 %s)", indexExpr, lenVal),
		fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i64 %s", elemPtr, elemIRType, dataPtr, indexExpr),
		fmt.Sprintf("%s = load %s, ptr %s", loadElem, elemIRType, elemPtr),
		fmt.Sprintf("%s = or %s 0, %s", llvmLocalName(targetName), elemIRType, loadElem),
	}
	return strings.Join(lines, "\n"), nil
}

func llvmPointerInner(typ typeinfo.Type) (typeinfo.Type, bool) {
	switch t := backend.UnwrapNamed(typ).(type) {
	case *typeinfo.PointerType:
		return t.Inner, true
	case *typeinfo.RawPtrType:
		return t.Inner, true
	case *typeinfo.RefType:
		return t.Inner, true
	default:
		return nil, false
	}
}

func llvmIsPointerLike(typ typeinfo.Type) bool {
	_, ok := llvmPointerInner(typ)
	return ok
}

func tupleElementLayoutForIndex(state *moduleState, tupleType *typeinfo.TupleType, index mir.Value) (*backend.TupleElementLayout, error) {
	idx, ok := becommon.TupleIndexFromValue(index)
	if !ok {
		return nil, fmt.Errorf("tuple index must be a constant integer literal")
	}
	entries, _, _, err := backend.TupleLayout(aggregateLayoutContext(state), tupleType)
	if err != nil {
		return nil, err
	}
	if idx < 0 || idx >= len(entries) {
		return nil, fmt.Errorf("tuple index %d out of range", idx)
	}
	return &entries[idx], nil
}

// lowerIndexAddress computes the pointer to arr[index].
// baseType must be *typeinfo.ArrayType or a pointer-like type.
func lowerIndexAddress(state *moduleState, base mir.Value, index mir.Value, baseType typeinfo.Type) ([]string, string, error) {
	var elemType typeinfo.Type
	var tupleElem *backend.TupleElementLayout
	arrayLen := int64(-1)
	switch bt := baseType.(type) {
	case *typeinfo.ArrayType:
		elemType = bt.Inner
		arrayLen = bt.Len
	case *typeinfo.TupleType:
		entry, err := tupleElementLayoutForIndex(state, bt, index)
		if err != nil {
			return nil, "", err
		}
		tupleElem = entry
		elemType = entry.Type
	default:
		var ok bool
		elemType, ok = llvmPointerInner(baseType)
		if !ok {
			return nil, "", fmt.Errorf("cannot index into %T", baseType)
		}
	}
	baseExpr, err := lowerValue(state, base)
	if err != nil {
		return nil, "", err
	}
	if tupleElem != nil {
		if tupleElem.Offset == 0 {
			return nil, baseExpr, nil
		}
		addr := freshTemp(state, "elem")
		line := fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 %d", addr, baseExpr, tupleElem.Offset)
		return []string{line}, addr, nil
	}
	elemIRType, err := llvmBaseType(elemType)
	if err != nil {
		return nil, "", err
	}
	indexExpr, err := lowerValue(state, index)
	if err != nil {
		return nil, "", err
	}
	var lines []string
	if arrayLen >= 0 {
		lines = append(lines, fmt.Sprintf("call void @ferret__bounds_check(i64 %s, i64 %d)", indexExpr, arrayLen))
	}
	addr := freshTemp(state, "elem")
	line := fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i64 %s", addr, elemIRType, baseExpr, indexExpr)
	lines = append(lines, line)
	return lines, addr, nil
}

func lowerStorePlace(state *moduleState, instr *mir.StoreInstr) (string, error) {
	if instr == nil {
		return "", nil
	}
	if agg := unionAggregateLocalForPlace(state, instr.Target); agg != nil {
		return lowerUnionAssign(state, agg, instr.Value)
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

func unionAggregateLocalForPlace(state *moduleState, place mir.Place) *aggregateLocal {
	switch p := place.(type) {
	case *mir.LocalPlace:
		if agg, ok := state.aggLocals[p.LocalID]; ok && isUnionAggregate(agg.Type) {
			return agg
		}
	case *mir.DerefPlace:
		if addr, ok := p.Pointer.(*mir.AddrOfValue); ok {
			switch src := addr.Source.(type) {
			case *mir.LocalValue:
				if agg, ok := state.aggLocals[src.LocalID]; ok && isUnionAggregate(agg.Type) {
					return agg
				}
			case *mir.NameValue:
				if len(src.Path) == 1 {
					if local := becommon.FindLocalByName(state.fn, src.Path[0]); local != nil {
						if agg, ok := state.aggLocals[local.ID]; ok && isUnionAggregate(agg.Type) {
							return agg
						}
					}
				}
			}
		}
	}
	return nil
}

// lowerPlaceAddr returns the LLVM ptr value for a MIR place.
func lowerPlaceAddr(state *moduleState, place mir.Place) ([]string, string, error) {
	switch p := place.(type) {
	case nil:
		return nil, "", fmt.Errorf("nil place")
	case *mir.LocalPlace:
		if agg, ok := state.aggLocals[p.LocalID]; ok {
			return nil, llvmLocalName(agg.PtrName), nil
		}
		if sc, ok := state.scalarLocals[p.LocalID]; ok {
			return nil, sc.AllocaName, nil
		}
		return nil, llvmLocalName(becommon.LocalNameByID(state.fn, p.LocalID)), nil
	case *mir.FieldPlace:
		baseLines, basePtr, err := lowerPlaceAddr(state, p.Base)
		if err != nil {
			return nil, "", err
		}
		// get base type from the local
		baseType := localTypeByPlaceID(state, p.Base)
		sl, err2 := becommon.LookupStructLayoutFromState(state.layouts, state.layout, state.mod, baseType, "llvm")
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
	case *mir.IndexPlace:
		baseLines, basePtr, err := lowerPlaceAddr(state, p.Base)
		if err != nil {
			return nil, "", err
		}
		baseType := localTypeByPlaceID(state, p.Base)
		var elemIRType string
		arrayLen := int64(-1)
		if arr, ok := baseType.(*typeinfo.ArrayType); ok {
			elemIRType, err = llvmBaseType(arr.Inner)
			if err != nil {
				return nil, "", err
			}
			arrayLen = arr.Len
		} else if tup, ok := baseType.(*typeinfo.TupleType); ok {
			entry, err := tupleElementLayoutForIndex(state, tup, p.Index)
			if err != nil {
				return nil, "", err
			}
			if entry.Offset == 0 {
				return baseLines, basePtr, nil
			}
			addrTmp := freshTemp(state, "elem")
			gepLine := fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 %d", addrTmp, basePtr, entry.Offset)
			return append(baseLines, gepLine), addrTmp, nil
		} else if sl, ok := baseType.(*typeinfo.SliceType); ok {
			elemIRType, err = llvmBaseType(sl.Inner)
			if err != nil {
				return nil, "", err
			}
			dataPtr := freshTemp(state, "slice_data")
			lenAddr := freshTemp(state, "slice_len_addr")
			lenVal := freshTemp(state, "slice_len")
			baseLines = append(baseLines, fmt.Sprintf("%s = load ptr, ptr %s", dataPtr, basePtr))
			baseLines = append(baseLines, fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", lenAddr, basePtr))
			baseLines = append(baseLines, fmt.Sprintf("%s = load i64, ptr %s", lenVal, lenAddr))
			basePtr = dataPtr
			idxVal, err := lowerValue(state, p.Index)
			if err != nil {
				return nil, "", err
			}
			baseLines = append(baseLines, fmt.Sprintf("call void @ferret__bounds_check(i64 %s, i64 %s)", idxVal, lenVal))
			addrTmp := freshTemp(state, "elem")
			gepLine := fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i64 %s", addrTmp, elemIRType, basePtr, idxVal)
			return append(baseLines, gepLine), addrTmp, nil
		} else {
			return nil, "", fmt.Errorf("IndexPlace base is not an array, tuple, or slice: %T", baseType)
		}
		idxVal, err := lowerValue(state, p.Index)
		if err != nil {
			return nil, "", err
		}
		if arrayLen >= 0 {
			baseLines = append(baseLines, fmt.Sprintf("call void @ferret__bounds_check(i64 %s, i64 %d)", idxVal, arrayLen))
		}
		addrTmp := freshTemp(state, "elem")
		gepLine := fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i64 %s", addrTmp, elemIRType, basePtr, idxVal)
		return append(baseLines, gepLine), addrTmp, nil
	case *mir.DerefPlace:
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
func localTypeByPlaceID(state *moduleState, place mir.Place) typeinfo.Type {
	switch p := place.(type) {
	case *mir.LocalPlace:
		if state.fn != nil && p.LocalID >= 0 && p.LocalID < len(state.fn.Locals) {
			return state.fn.Locals[p.LocalID].Type
		}
	case *mir.FieldPlace:
		return localTypeByPlaceID(state, p.Base)
	case *mir.IndexPlace:
		return localTypeByPlaceID(state, p.Base)
	case *mir.DerefPlace:
		if addr, ok := p.Pointer.(*mir.AddrOfValue); ok && addr.Source != nil {
			if typ := addr.Source.Type(); typ != nil {
				return typ
			}
			switch src := addr.Source.(type) {
			case *mir.LocalValue:
				return becommon.LocalTypeByID(state.fn, src.LocalID)
			case *mir.NameValue:
				if len(src.Path) == 1 {
					if local := becommon.FindLocalByName(state.fn, src.Path[0]); local != nil {
						return local.Type
					}
				}
			}
		}
	}
	return nil
}

func lowerStoreField(state *moduleState, instr *mir.StoreFieldInstr) (string, error) {
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

func lowerFieldAddress(state *moduleState, base mir.Value, fieldIndex int) ([]string, string, typeinfo.Type, error) {
	structLayout, err := becommon.LookupStructLayoutFromState(state.layouts, state.layout, state.mod, base.Type(), "llvm")
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

func lowerCall(state *moduleState, targetName string, targetType typeinfo.Type, call *mir.CallValue) (string, error) {
	if local, ok := call.Callee.(*mir.LocalValue); ok && len(call.Args) > 0 && isInterfaceAggregate(call.Args[0].Type()) {
		if field, ok := state.tempValues[local.LocalID].(*mir.FieldValue); ok {
			return lowerInterfaceCall(state, targetName, targetType, call, field)
		}
	}
	if field, ok := call.Callee.(*mir.FieldValue); ok && len(call.Args) > 0 && isInterfaceAggregate(call.Args[0].Type()) {
		return lowerInterfaceCall(state, targetName, targetType, call, field)
	}
	callee, err := lowerCallee(state, call.Callee)
	if err != nil {
		return "", err
	}
	if call.IsConstructor {
		if targetName != "" {
			if local := becommon.FindLocalByName(state.fn, targetName); local != nil {
				if agg, ok := state.aggLocals[local.ID]; ok {
					return lowerConstructorCall(state, llvmLocalName(agg.PtrName), call, callee)
				}
			}
		}
		return lowerConstructorCallDiscard(state, targetType, call, callee)
	}

	// Inline builtins: string_ptr and string_len have been removed.
	// String literals are now *i8 — no special callee interception needed.

	args := make([]string, 0, len(call.Args))
	externLinked := false
	if callee, ok := call.Callee.(*mir.NameValue); ok && callee != nil && callee.LinkName != "" {
		externLinked = true
	}
	for _, arg := range call.Args {
		if isAggregateType(state, arg.Type()) {
			if externLinked {
				prefix, ptr, terr := lowerInterfaceConcretePointer(state, arg, arg.Type())
				if terr != nil {
					return "", terr
				}
				if len(prefix) != 0 {
					state.pendingLines = append(state.pendingLines, prefix...)
				}
				args = append(args, fmt.Sprintf("ptr %s", ptr))
				continue
			}
			typeName, terr := llvmABITypeName(state, arg.Type())
			if terr != nil {
				return "", terr
			}
			_, align, terr := backend.AggregateSizeAlign(aggregateLayoutContext(state), arg.Type())
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
		if local := becommon.FindLocalByName(state.fn, targetName); local != nil {
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
	} else if targetType != nil && !isVoidType(targetType) {
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

func lowerConstructorCall(state *moduleState, dstPtr string, call *mir.CallValue, callee string) (string, error) {
	args := make([]string, 0, len(call.Args)+1)
	args = append(args, fmt.Sprintf("ptr %s", dstPtr))
	for _, arg := range call.Args {
		if isAggregateType(state, arg.Type()) {
			typeName, err := llvmABITypeName(state, arg.Type())
			if err != nil {
				return "", err
			}
			_, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), arg.Type())
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

func lowerConstructorCallDiscard(state *moduleState, targetType typeinfo.Type, call *mir.CallValue, callee string) (string, error) {
	if targetType == nil || !isAggregateType(state, targetType) {
		return "", fmt.Errorf("constructor call requires aggregate target")
	}
	typeName, err := llvmABITypeName(state, targetType)
	if err != nil {
		return "", err
	}
	_, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), targetType)
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

func lowerAggregateAssign(state *moduleState, agg *aggregateLocal, value mir.Value) (string, error) {
	if agg == nil {
		return "", fmt.Errorf("nil aggregate local")
	}
	if isInterfaceAggregate(agg.Type) {
		if iface, ok := value.(*mir.InterfaceValue); ok {
			return lowerInterfaceAssign(state, llvmLocalName(agg.PtrName), agg.Type, iface)
		}
		return "", fmt.Errorf("unsupported interface assignment %T", value)
	}
	if isUnionAggregate(agg.Type) {
		return lowerUnionAssign(state, agg, value)
	}
	switch v := value.(type) {
	case *mir.UnaryValue:
		if v.Op == "copy" || v.Op == "take" || v.Op == "comptime" {
			return lowerAggregateAssign(state, agg, v.Right)
		}
	case *mir.CastValue:
		srcCast := backend.UnwrapNamed(v.Left.Type())
		dstCast := backend.UnwrapNamed(v.Type())
		if isAggregateType(state, v.Type()) && isAggregateType(state, v.Left.Type()) && !isStringSliceCastPair(srcCast, dstCast) {
			return lowerAggregateAssign(state, agg, v.Left)
		}
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
	case *mir.CallValue:
		return lowerAggregateCallValue(state, agg, v)
	case *mir.LocalValue, *mir.NameValue, *mir.LoadValue, *mir.FieldLoadValue:
		src, err := lowerAggregateSource(state, value)
		if err != nil {
			return "", err
		}
		return llvmMemcpy(llvmLocalName(agg.PtrName), src, agg.Size, agg.Align), nil
	case *mir.IndexValue:
		lines, src, err := lowerIndexAddress(state, v.Base, v.Index, v.Base.Type())
		if err != nil {
			return "", err
		}
		lines = append(lines, llvmMemcpy(llvmLocalName(agg.PtrName), src, agg.Size, agg.Align))
		return strings.Join(lines, "\n"), nil
	case *mir.CompositeValue:
		return lowerAggregateCompositeAssign(state, agg, v)
	}
	return "", fmt.Errorf("unsupported aggregate assignment %T", value)
}

func isUnionAggregate(typ typeinfo.Type) bool {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return backend.IsNamedUnion(t)
	case *typeinfo.UnionType:
		return true
	case *typeinfo.OptionalType:
		return !backend.OptionalUsesNiche(t.Inner)
	default:
		return false
	}
}

func isInterfaceAggregate(typ typeinfo.Type) bool {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return backend.IsNamedInterface(t)
	case *typeinfo.InterfaceType:
		return true
	default:
		return false
	}
}

func lowerUnionAssign(state *moduleState, agg *aggregateLocal, value mir.Value) (string, error) {
	switch v := value.(type) {
	case *mir.UnaryValue:
		if v.Op == "copy" || v.Op == "take" || v.Op == "comptime" {
			return lowerUnionAssign(state, agg, v.Right)
		}
	case *mir.CastValue:
		if isUnionAggregate(v.Type()) {
			return lowerUnionAssign(state, agg, v.Left)
		}
	}
	if _, ok := backend.UnwrapNamed(agg.Type).(*typeinfo.OptionalType); ok {
		if _, isNone := value.(*mir.NoneValue); isNone {
			return fmt.Sprintf("store i32 0, ptr %s", llvmLocalName(agg.PtrName)), nil
		}
	}
	if isUnionAggregate(value.Type()) {
		src, err := lowerAggregateSource(state, value)
		if err != nil {
			return "", err
		}
		size, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), agg.Type)
		if err != nil {
			return "", err
		}
		return llvmMemcpy(llvmLocalName(agg.PtrName), src, size, align), nil
	}
	if isAggregateType(state, value.Type()) {
		src, err := lowerAggregateSource(state, value)
		if err != nil {
			return "", err
		}
		size, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), value.Type())
		if err != nil {
			return "", err
		}
		info, err := llvmUnionLayoutInfo(state, agg.Type)
		if err != nil {
			return "", err
		}
		memberIndex := 1
		if _, ok := backend.UnwrapNamed(agg.Type).(*typeinfo.OptionalType); !ok {
			memberIndex, err = llvmUnionMemberIndex(info.Members, value.Type())
			if err != nil {
				return "", err
			}
		}
		lines := []string{fmt.Sprintf("store i32 %d, ptr %s", memberIndex, llvmLocalName(agg.PtrName))}
		dst := llvmLocalName(agg.PtrName)
		if info.PayloadOffset != 0 {
			tmp := freshTemp(state, "unionpayload")
			lines = append(lines, fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", tmp, llvmLocalName(agg.PtrName), info.PayloadOffset))
			dst = tmp
		}
		lines = append(lines, llvmMemcpy(dst, src, size, align))
		return strings.Join(lines, "\n"), nil
	}
	info, err := llvmUnionLayoutInfo(state, agg.Type)
	if err != nil {
		return "", err
	}
	memberIndex := 1
	if _, ok := backend.UnwrapNamed(agg.Type).(*typeinfo.OptionalType); !ok {
		memberIndex, err = llvmUnionMemberIndex(info.Members, value.Type())
		if err != nil {
			return "", err
		}
	}
	irType, err := llvmBaseType(value.Type())
	if err != nil {
		return "", err
	}
	val, err := lowerValue(state, value)
	if err != nil {
		return "", err
	}
	lines := []string{fmt.Sprintf("store i32 %d, ptr %s", memberIndex, llvmLocalName(agg.PtrName))}
	dst := llvmLocalName(agg.PtrName)
	if info.PayloadOffset != 0 {
		tmp := freshTemp(state, "unionpayload")
		lines = append(lines, fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", tmp, llvmLocalName(agg.PtrName), info.PayloadOffset))
		dst = tmp
	}
	lines = append(lines, fmt.Sprintf("store %s, ptr %s", operandWithTemp(state, irType, val), dst))
	return strings.Join(lines, "\n"), nil
}

// emitStringConstant writes a private unnamed_addr constant to state.deferredB
// and returns its symbol name (without @).  Each distinct string value gets a
// unique symbol; identical content currently still gets a new symbol (good
// enough for functional correctness).
func emitStringConstant(state *moduleState, s string) string {
	state.nextStrConst++
	sym := fmt.Sprintf("__str%d_%s", state.nextStrConst, becommon.SanitizeIdent(state.modulePrefix))
	escaped := llvmStringLiteral(s)
	// +1 for the null terminator we always add.
	length := len(s) + 1
	fmt.Fprintf(state.deferredB, "@%s = private unnamed_addr constant [%d x i8] %s\n",
		sym, length, escaped)
	return sym
}

func lowerAggregateCallValue(state *moduleState, agg *aggregateLocal, call *mir.CallValue) (string, error) {
	if local, ok := call.Callee.(*mir.LocalValue); ok && len(call.Args) > 0 && isInterfaceAggregate(call.Args[0].Type()) {
		if field, ok := state.tempValues[local.LocalID].(*mir.FieldValue); ok {
			return lowerInterfaceCall(state, agg.Name, agg.Type, call, field)
		}
	}
	if field, ok := call.Callee.(*mir.FieldValue); ok && len(call.Args) > 0 && isInterfaceAggregate(call.Args[0].Type()) {
		return lowerInterfaceCall(state, agg.Name, agg.Type, call, field)
	}
	callee, err := lowerCallee(state, call.Callee)
	if err != nil {
		return "", err
	}
	if call.IsConstructor {
		return lowerConstructorCall(state, llvmLocalName(agg.PtrName), call, callee)
	}
	args := make([]string, 0, len(call.Args))
	externLinked := false
	if callee, ok := call.Callee.(*mir.NameValue); ok && callee != nil && callee.LinkName != "" {
		externLinked = true
	}
	for _, arg := range call.Args {
		if isAggregateType(state, arg.Type()) {
			if externLinked {
				prefix, ptr, terr := lowerInterfaceConcretePointer(state, arg, arg.Type())
				if terr != nil {
					return "", terr
				}
				if len(prefix) != 0 {
					state.pendingLines = append(state.pendingLines, prefix...)
				}
				args = append(args, fmt.Sprintf("ptr %s", ptr))
				continue
			}
			typeName, terr := llvmABITypeName(state, arg.Type())
			if terr != nil {
				return "", terr
			}
			_, align, terr := backend.AggregateSizeAlign(aggregateLayoutContext(state), arg.Type())
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

func lowerAggregateCompositeAssign(state *moduleState, agg *aggregateLocal, comp *mir.CompositeValue) (string, error) {
	// Array literal: positional items stored at successive element offsets.
	if arrType, ok := agg.Type.(*typeinfo.ArrayType); ok {
		elemSize, elemAlign, err := aggregateSizeAlignOfPrimitive(arrType.Inner)
		if err != nil {
			// try inner as aggregate
			innerSz, innerAl, err2 := backend.AggregateSizeAlign(aggregateLayoutContext(state), arrType.Inner)
			if err2 != nil {
				return "", fmt.Errorf("unsupported array element type in composite literal: %s", arrType.Inner)
			}
			elemSize = innerSz
			elemAlign = innerAl
		}
		stride := backend.AlignUpInt64(elemSize, elemAlign)
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
	if _, ok := agg.Type.(*typeinfo.SliceType); ok {
		lines, err := lowerAggregateValueToAddr(state, llvmLocalName(agg.PtrName), agg.Type, comp)
		if err != nil {
			return "", err
		}
		return strings.Join(lines, "\n"), nil
	}
	if _, ok := agg.Type.(*typeinfo.StringType); ok {
		lines, err := lowerAggregateValueToAddr(state, llvmLocalName(agg.PtrName), agg.Type, comp)
		if err != nil {
			return "", err
		}
		return strings.Join(lines, "\n"), nil
	}
	if tupleType, ok := backend.UnwrapNamed(agg.Type).(*typeinfo.TupleType); ok {
		entries, _, _, err := backend.TupleLayout(aggregateLayoutContext(state), tupleType)
		if err != nil {
			return "", err
		}
		lines := make([]string, 0, len(comp.Items)*3)
		for i, item := range comp.Items {
			if i >= len(entries) {
				break
			}
			entry := entries[i]
			addr := llvmLocalName(agg.PtrName)
			if entry.Offset != 0 {
				tmp := freshTemp(state, "addr")
				lines = append(lines, fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 %d", tmp, llvmLocalName(agg.PtrName), entry.Offset))
				addr = tmp
			}
			if isAggregateType(state, entry.Type) {
				valueLines, err := lowerAggregateValueToAddr(state, addr, entry.Type, item.Value)
				if err != nil {
					return "", err
				}
				lines = append(lines, valueLines...)
				continue
			}
			irType, err := llvmBaseType(entry.Type)
			if err != nil {
				return "", err
			}
			lowered, err := lowerValue(state, item.Value)
			if err != nil {
				return "", err
			}
			lines = append(lines, fmt.Sprintf("store %s %s, ptr %s", irType, lowered, addr))
		}
		return strings.Join(lines, "\n"), nil
	}

	structLayout, err := becommon.LookupStructLayoutFromState(state.layouts, state.layout, state.mod, agg.Type, "llvm")
	if err != nil {
		return "", err
	}
	items := make(map[string]mir.Value, len(comp.Items))
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

func lowerAggregateValueToAddr(state *moduleState, addr string, typ typeinfo.Type, value mir.Value) ([]string, error) {
	if sliceType, ok := typ.(*typeinfo.SliceType); ok {
		comp, ok := value.(*mir.CompositeValue)
		if !ok {
			return nil, fmt.Errorf("slice aggregate value must be composite")
		}
		ptrItem, lenItem, elems, err := becommon.SplitSliceComposite(comp)
		if err != nil {
			return nil, err
		}
		lines := make([]string, 0, len(comp.Items)*3+3)
		ptrLowered := "null"
		lenLowered := "0"
		if elems == nil {
			ptrLowered, err = lowerValue(state, ptrItem)
			if err != nil {
				return nil, err
			}
			lenLowered, err = lowerValue(state, lenItem)
			if err != nil {
				return nil, err
			}
		} else if len(elems) > 0 {
			elemSize, elemAlign, err := aggregateSizeAlignOfPrimitive(sliceType.Inner)
			if err != nil {
				elemSize, elemAlign, err = backend.AggregateSizeAlign(aggregateLayoutContext(state), sliceType.Inner)
			}
			if err != nil {
				return nil, err
			}
			if elemAlign < 1 {
				elemAlign = 1
			}
			stride := backend.AlignUpInt64(elemSize, elemAlign)
			total := stride * int64(len(elems))
			allocBytes := total
			if allocBytes <= 0 {
				allocBytes = 1
			}
			buf := freshTemp(state, "slice_lit_buf")
			lines = append(lines, fmt.Sprintf("%s = alloca i8, i64 %d, align %d", buf, allocBytes, elemAlign))
			for i, elem := range elems {
				offset := stride * int64(i)
				elemAddr := buf
				if offset != 0 {
					elemAddr = freshTemp(state, "slice_lit_elem_addr")
					lines = append(lines, fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 %d", elemAddr, buf, offset))
				}
				if isAggregateType(state, sliceType.Inner) {
					elemLines, err := lowerAggregateValueToAddr(state, elemAddr, sliceType.Inner, elem)
					if err != nil {
						return nil, err
					}
					lines = append(lines, elemLines...)
					continue
				}
				irType, err := llvmBaseType(sliceType.Inner)
				if err != nil {
					return nil, err
				}
				lowered, err := lowerValue(state, elem)
				if err != nil {
					return nil, err
				}
				lines = append(lines, fmt.Sprintf("store %s %s, ptr %s", irType, lowered, elemAddr))
			}
			ptrLowered = buf
			lenLowered = strconv.FormatInt(int64(len(elems)), 10)
		}
		lenAddr := freshTemp(state, "len_addr")
		lines = append(lines,
			fmt.Sprintf("store ptr %s, ptr %s", ptrLowered, addr),
			fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", lenAddr, addr),
			fmt.Sprintf("store i64 %s, ptr %s", lenLowered, lenAddr),
		)
		return lines, nil
	}
	if _, ok := typ.(*typeinfo.StringType); ok {
		switch v := value.(type) {
		case *mir.StringValue:
			ptrLowered, err := lowerValue(state, v)
			if err != nil {
				return nil, err
			}
			lenAddr := freshTemp(state, "len_addr")
			return []string{
				fmt.Sprintf("store ptr %s, ptr %s", ptrLowered, addr),
				fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", lenAddr, addr),
				fmt.Sprintf("store i64 %d, ptr %s", len(v.Value), lenAddr),
			}, nil
		case *mir.CompositeValue:
			items := make(map[string]mir.Value, len(v.Items))
			for _, item := range v.Items {
				items[item.Name] = item.Value
			}
			ptrLowered, err := lowerValue(state, items["ptr"])
			if err != nil {
				return nil, err
			}
			lenLowered, err := lowerValue(state, items["len"])
			if err != nil {
				return nil, err
			}
			lenAddr := freshTemp(state, "len_addr")
			return []string{
				fmt.Sprintf("store ptr %s, ptr %s", ptrLowered, addr),
				fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", lenAddr, addr),
				fmt.Sprintf("store i64 %s, ptr %s", lenLowered, lenAddr),
			}, nil
		}
	}
	src, err := lowerAggregateSource(state, value)
	if err != nil {
		return nil, err
	}
	size, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), typ)
	if err != nil {
		return nil, err
	}
	return []string{llvmMemcpy(addr, src, size, align)}, nil
}

func lowerInterfaceAssign(state *moduleState, dstPtr string, target typeinfo.Type, value *mir.InterfaceValue) (string, error) {
	lines, dataPtr, err := lowerInterfaceConcretePointer(state, value.Value, value.ConcreteType)
	if err != nil {
		return "", err
	}
	vtSym, methodCount, err := ensureLLVMInterfaceVTable(state, target, value)
	if err != nil {
		return "", err
	}
	vtGEP := freshTemp(state, "iface_vt")
	vtSlot := freshTemp(state, "iface_vt_addr")
	lines = append(lines,
		fmt.Sprintf("store ptr %s, ptr %s", dataPtr, dstPtr),
		fmt.Sprintf("%s = getelementptr inbounds [%d x ptr], ptr @%s, i32 0, i32 0", vtGEP, methodCount, vtSym),
		fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", vtSlot, dstPtr),
		fmt.Sprintf("store ptr %s, ptr %s", vtGEP, vtSlot),
	)
	return strings.Join(lines, "\n"), nil
}

func lowerInterfaceConcretePointer(state *moduleState, value mir.Value, concreteType typeinfo.Type) ([]string, string, error) {
	switch v := value.(type) {
	case *mir.UnaryValue:
		if v.Op == "copy" || v.Op == "take" || v.Op == "comptime" {
			return lowerInterfaceConcretePointer(state, v.Right, concreteType)
		}
	case *mir.LocalValue:
		if agg, ok := state.aggLocals[v.LocalID]; ok {
			return nil, llvmLocalName(agg.PtrName), nil
		}
		if sc, ok := state.scalarLocals[v.LocalID]; ok {
			return nil, sc.AllocaName, nil
		}
	case *mir.NameValue:
		if len(v.Path) == 1 {
			if local := becommon.FindLocalByName(state.fn, v.Path[0]); local != nil {
				if agg, ok := state.aggLocals[local.ID]; ok {
					return nil, llvmLocalName(agg.PtrName), nil
				}
				if sc, ok := state.scalarLocals[local.ID]; ok {
					return nil, sc.AllocaName, nil
				}
			}
		}
		if v.LinkName != "" {
			return nil, "@" + becommon.SanitizeIdent(v.LinkName), nil
		}
		return nil, "@" + llvmSymbol(state, v.Path), nil
	}

	if isAggregateType(state, concreteType) {
		typeName, err := llvmABITypeName(state, concreteType)
		if err != nil {
			return nil, "", err
		}
		_, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), concreteType)
		if err != nil {
			return nil, "", err
		}
		tmp := freshTemp(state, "iface_data")
		agg := &aggregateLocal{
			PtrName: strings.TrimPrefix(tmp, "%"),
			Type:    concreteType,
			Align:   align,
		}
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

	irType, err := llvmBaseType(concreteType)
	if err != nil {
		return nil, "", err
	}
	val, err := lowerValue(state, value)
	if err != nil {
		return nil, "", err
	}
	tmp := freshTemp(state, "iface_data")
	align := irTypeAlign(irType)
	lines := []string{
		fmt.Sprintf("%s = alloca %s, align %d", tmp, irType, align),
		fmt.Sprintf("store %s %s, ptr %s", irType, val, tmp),
	}
	return lines, tmp, nil
}

func lowerInterfaceCall(state *moduleState, targetName string, targetType typeinfo.Type, call *mir.CallValue, field *mir.FieldValue) (string, error) {
	recv := call.Args[0]
	method, index, err := becommon.LookupInterfaceMethodDecl(state.mod, state.modules, field.Base.Type(), field.MemberName, "llvm")
	if err != nil {
		return "", err
	}
	slotPtr, err := lowerInterfaceSlotPointer(state, recv)
	if err != nil {
		return "", err
	}
	dataTmp := freshTemp(state, "iface_data")
	vtAddr := freshTemp(state, "iface_vt_addr")
	vtTmp := freshTemp(state, "iface_vt")
	fnSlot := freshTemp(state, "iface_fnslot")
	fnTmp := freshTemp(state, "iface_fn")
	lines := []string{
		fmt.Sprintf("%s = load ptr, ptr %s", dataTmp, slotPtr),
		fmt.Sprintf("%s = getelementptr inbounds i8, ptr %s, i64 8", vtAddr, slotPtr),
		fmt.Sprintf("%s = load ptr, ptr %s", vtTmp, vtAddr),
		fmt.Sprintf("%s = getelementptr inbounds ptr, ptr %s, i64 %d", fnSlot, vtTmp, index+1),
		fmt.Sprintf("%s = load ptr, ptr %s", fnTmp, fnSlot),
	}
	args := []string{fmt.Sprintf("ptr %s", dataTmp)}
	for i, arg := range call.Args[1:] {
		expected := typeinfo.Type(nil)
		if method != nil && i < len(method.Params) {
			expected = method.Params[i].Type
		}
		if expected == nil {
			expected = arg.Type()
		}
		if isAggregateType(state, expected) {
			typeName, err := llvmABITypeName(state, expected)
			if err != nil {
				return "", err
			}
			_, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), expected)
			if err != nil {
				return "", err
			}
			aval, err := lowerValue(state, arg)
			if err != nil {
				return "", err
			}
			args = append(args, fmt.Sprintf("ptr byval(%s) align %d %s", typeName, align, aval))
		} else {
			atype, err := llvmBaseType(expected)
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
	retStr := "void"
	if targetType != nil && !isVoidType(targetType) {
		if isAggregateType(state, targetType) {
			typeName, err := llvmABITypeName(state, targetType)
			if err != nil {
				return "", err
			}
			retStr = typeName
		} else {
			irType, err := llvmBaseType(targetType)
			if err != nil {
				return "", err
			}
			retStr = irType
		}
	}
	callText := fmt.Sprintf("call %s %s(%s)", retStr, fnTmp, strings.Join(args, ", "))
	if targetName == "" || retStr == "void" {
		lines = append(lines, callText)
		return strings.Join(lines, "\n"), nil
	}
	if local := becommon.FindLocalByName(state.fn, targetName); local != nil {
		if agg, ok := state.aggLocals[local.ID]; ok {
			tmp := freshTemp(state, "iface_ret")
			lines = append(lines,
				fmt.Sprintf("%s = %s", tmp, callText),
				fmt.Sprintf("store %s %s, ptr %s", retStr, tmp, llvmLocalName(agg.PtrName)),
			)
			return strings.Join(lines, "\n"), nil
		}
	}
	lines = append(lines, fmt.Sprintf("%s = %s", llvmLocalName(targetName), callText))
	return strings.Join(lines, "\n"), nil
}

func lowerInterfaceSlotPointer(state *moduleState, value mir.Value) (string, error) {
	switch v := value.(type) {
	case *mir.LocalValue:
		if repl, ok := state.tempValues[v.LocalID]; ok && repl != nil {
			return lowerValue(state, repl)
		}
		if agg, ok := state.aggLocals[v.LocalID]; ok {
			return llvmLocalName(agg.PtrName), nil
		}
	case *mir.NameValue:
		if len(v.Path) == 1 {
			if local := becommon.FindLocalByName(state.fn, v.Path[0]); local != nil {
				if agg, ok := state.aggLocals[local.ID]; ok {
					return llvmLocalName(agg.PtrName), nil
				}
			}
		}
		if v.LinkName != "" {
			return "@" + becommon.SanitizeIdent(v.LinkName), nil
		}
		return "@" + llvmSymbol(state, v.Path), nil
	}
	return "", fmt.Errorf("unsupported interface receiver storage %T", value)
}

func ensureLLVMInterfaceVTable(state *moduleState, target typeinfo.Type, value *mir.InterfaceValue) (string, int, error) {
	if state == nil || value == nil {
		return "", 0, fmt.Errorf("invalid interface vtable request")
	}
	targetNamed, ok := target.(*typeinfo.NamedType)
	if !ok || targetNamed == nil {
		return "", 0, fmt.Errorf("interface target must be named")
	}
	key := interfaceVTableKey{iface: typeinfo.DefaultPrinter.Type(targetNamed), concrete: typeinfo.DefaultPrinter.Type(value.ConcreteType)}
	if sym, ok := state.interfaceVTables[key]; ok {
		if iface, _, err := becommon.LookupInterfaceDecl(state.mod, state.modules, target, "llvm"); err == nil && iface != nil {
			return sym, len(iface.Methods) + 1, nil
		}
		return sym, 0, nil
	}
	iface, _, err := becommon.LookupInterfaceDecl(state.mod, state.modules, target, "llvm")
	if err != nil {
		return "", 0, err
	}
	sym := becommon.SanitizeIdent("vtable__" + llvmTypeName(state, targetNamed) + "__" + becommon.SanitizeType(value.ConcreteType))
	typeInfoSym, err := emitLLVMRuntimeTypeInfo(state, sym+"__typeinfo", value.ConcreteType)
	if err != nil {
		return "", 0, err
	}
	entries := make([]string, 0, len(iface.Methods)+1)
	entries = append(entries, "ptr @"+typeInfoSym)
	for i, method := range iface.Methods {
		if method == nil {
			continue
		}
		if i >= len(value.Methods) {
			return "", 0, fmt.Errorf("missing interface method link for %s", method.Name)
		}
		wrap, err := ensureLLVMInterfaceWrapper(state, targetNamed, method, value.ConcreteType, value.Methods[i])
		if err != nil {
			return "", 0, err
		}
		entries = append(entries, "ptr @"+wrap)
	}
	fmt.Fprintf(state.deferredB, "@%s = private unnamed_addr constant [%d x ptr] [%s]\n", sym, len(entries), strings.Join(entries, ", "))
	state.interfaceVTables[key] = sym
	return sym, len(entries), nil
}

func emitLLVMRuntimeTypeInfo(state *moduleState, sym string, typ typeinfo.Type) (string, error) {
	desc := backend.DescribeRuntimeType(typ)
	nameSym := emitStringConstant(state, desc.Name)
	size, align, err := llvmRuntimeTypeSizeAlign(state, typ)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(state.deferredB,
		"@%s = private unnamed_addr constant { i32, ptr, i64, i64, i32 } { i32 %d, ptr @%s, i64 %d, i64 %d, i32 %d }\n",
		sym, desc.ID, nameSym, size, align, desc.Flags)
	return sym, nil
}

func llvmRuntimeTypeSizeAlign(state *moduleState, typ typeinfo.Type) (int64, int64, error) {
	if isAggregateType(state, typ) {
		return backend.AggregateSizeAlign(aggregateLayoutContext(state), typ)
	}
	irType, err := llvmBaseType(typ)
	if err != nil {
		return 0, 0, err
	}
	size := irTypeSize(irType)
	return size, irTypeAlign(irType), nil
}

func ensureLLVMInterfaceWrapper(state *moduleState, iface *typeinfo.NamedType, method *mir.InterfaceMethodDecl, concrete typeinfo.Type, link mir.InterfaceMethodLink) (string, error) {
	key := interfaceWrapperKey{iface: typeinfo.DefaultPrinter.Type(iface), concrete: typeinfo.DefaultPrinter.Type(concrete), method: method.Name}
	name := becommon.SanitizeIdent("ifacewrap__" + llvmTypeName(state, iface) + "__" + becommon.SanitizeType(concrete) + "__" + method.Name)
	if _, ok := state.interfaceWrappers[key]; ok {
		return name, nil
	}
	retStr := "void"
	if method.Result != nil && !isVoidType(method.Result) {
		if isAggregateType(state, method.Result) {
			typeName, err := llvmABITypeName(state, method.Result)
			if err != nil {
				return "", err
			}
			retStr = typeName
		} else {
			irType, err := llvmBaseType(method.Result)
			if err != nil {
				return "", err
			}
			retStr = irType
		}
	}
	params := []string{"ptr %data"}
	argNames := make([]string, 0, len(method.Params))
	for i, param := range method.Params {
		argName := fmt.Sprintf("%%arg%d", i)
		argNames = append(argNames, argName)
		if isAggregateType(state, param.Type) {
			typeName, err := llvmABITypeName(state, param.Type)
			if err != nil {
				return "", err
			}
			_, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), param.Type)
			if err != nil {
				return "", err
			}
			params = append(params, fmt.Sprintf("ptr byval(%s) align %d %s", typeName, align, argName))
		} else {
			irType, err := llvmBaseType(param.Type)
			if err != nil {
				return "", err
			}
			params = append(params, fmt.Sprintf("%s %s", irType, argName))
		}
	}
	callArgs := make([]string, 0, 1+len(argNames))
	body := make([]string, 0, 8)
	receiverType := typeinfo.ApplyReceiverShape(concrete, semmeta.ReceiverKindFromSyntax(method.Receiver))
	recvArg, recvPrep, err := llvmInterfaceReceiverArg(state, concrete, receiverType)
	if err != nil {
		return "", err
	}
	body = append(body, recvPrep...)
	callArgs = append(callArgs, recvArg)
	for i, param := range method.Params {
		if isAggregateType(state, param.Type) {
			typeName, err := llvmABITypeName(state, param.Type)
			if err != nil {
				return "", err
			}
			_, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), param.Type)
			if err != nil {
				return "", err
			}
			callArgs = append(callArgs, fmt.Sprintf("ptr byval(%s) align %d %s", typeName, align, argNames[i]))
		} else {
			irType, err := llvmBaseType(param.Type)
			if err != nil {
				return "", err
			}
			callArgs = append(callArgs, fmt.Sprintf("%s %s", irType, argNames[i]))
		}
	}
	callee := "@" + llvmSymbol(state, link.Path)
	if retStr == "void" {
		body = append(body, fmt.Sprintf("call void %s(%s)", callee, strings.Join(callArgs, ", ")), "ret void")
	} else {
		body = append(body, "%ret = call "+retStr+" "+callee+"("+strings.Join(callArgs, ", ")+")", "ret "+retStr+" %ret")
	}
	fmt.Fprintf(state.deferredB, "define %s @%s(%s) {\nentry:\n", retStr, name, strings.Join(params, ", "))
	for _, line := range body {
		fmt.Fprintf(state.deferredB, "  %s\n", line)
	}
	fmt.Fprintf(state.deferredB, "}\n")
	state.interfaceWrappers[key] = struct{}{}
	return name, nil
}

func llvmInterfaceReceiverArg(state *moduleState, concrete, receiverType typeinfo.Type) (string, []string, error) {
	target := receiverType
	if target == nil {
		target = concrete
	}
	if isAggregateType(state, target) {
		if !isAggregateType(state, concrete) {
			return "", nil, fmt.Errorf("interface receiver mismatch: expected aggregate receiver %s for concrete %s", typeinfo.FormatType(typeStringer{target}), typeinfo.FormatType(typeStringer{concrete}))
		}
		typeName, err := llvmABITypeName(state, target)
		if err != nil {
			return "", nil, err
		}
		_, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), target)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("ptr byval(%s) align %d %%data", typeName, align), nil, nil
	}
	irType, err := llvmBaseType(target)
	if err != nil {
		return "", nil, err
	}
	if isAggregateType(state, concrete) && irType == "ptr" {
		return "ptr %data", nil, nil
	}
	return fmt.Sprintf("%s %%recv", irType), []string{fmt.Sprintf("%%recv = load %s, ptr %%data", irType)}, nil
}

func lowerAggregateSource(state *moduleState, value mir.Value) (string, error) {
	return backend.ResolveAggregateSource(
		value,
		func(v *mir.LocalValue) (string, error) { return lowerValue(state, v) },
		func(v *mir.NameValue) (string, error) { return lowerValue(state, v) },
		func(v mir.Value) (string, error) { return lowerValue(state, v) },
		func(base mir.Value, fieldIndex int) (string, error) {
			_, addr, _, err := lowerFieldAddress(state, base, fieldIndex)
			if err != nil {
				return "", err
			}
			return addr, nil
		},
	)
}

// ---------------------------------------------------------------------------
// Terminator
// ---------------------------------------------------------------------------

// lowerPanicTerm emits a call to ferret__panic(ptr msg) followed by unreachable.
// String literals are null-terminated *i8 constants; we pass the pointer directly.
func lowerPanicTerm(state *moduleState, t *mir.PanicTerm) (string, error) {
	payload, err := backend.ClassifyPanicPayload(t, func(typ typeinfo.Type) bool {
		_, ok := backend.UnwrapNamed(typ).(*typeinfo.StringType)
		return ok
	})
	if err != nil {
		return "", err
	}
	switch payload.Kind {
	case backend.PanicPayloadLiteralString:
		sym := emitStringConstant(state, payload.Literal)
		return fmt.Sprintf("call void @ferret__panic(ptr @%s)\nunreachable", sym), nil
	case backend.PanicPayloadDynamicString:
		addr, err := backend.ResolvePanicStringAddress(
			payload.Value,
			func(v mir.Value) (string, error) { return lowerAddrOf(state, &mir.AddrOfValue{Source: v}) },
			func(v mir.Value) (string, error) { return lowerValue(state, v) },
		)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("call void @global__panic(ptr %s)\nunreachable", addr), nil
	default:
		return "", fmt.Errorf("panic: unsupported payload type %T", t.Value)
	}
}

func lowerTerm(state *moduleState, term mir.Terminator) (string, error) {
	switch t := term.(type) {
	case nil:
		return "unreachable", nil
	case *mir.ExitTerm:
		// ExitTerm is emitted for void functions that have no explicit return.
		// Emit ret void so the function terminates correctly.
		return "ret void", nil
	case *mir.JumpTerm:
		return fmt.Sprintf("br label %%%s", llvmBlockLabel(state.fn, t.TargetID)), nil
	case *mir.BranchTerm:
		cond, err := lowerCondValue(state, t.Cond)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s\nbr i1 %s, label %%%s, label %%%s",
			cond,
			freshTempRef(cond),
			llvmBlockLabel(state.fn, t.TrueID),
			llvmBlockLabel(state.fn, t.FalseID)), nil
	case *mir.ReturnTerm:
		if t.CleanupID >= 0 {
			return fmt.Sprintf("br label %%%s", llvmBlockLabel(state.fn, t.CleanupID)), nil
		}
		if t.Value == nil {
			return "ret void", nil
		}
		if call, ok := t.Value.(*mir.CallValue); ok && call.IsConstructor {
			typeName, err := llvmABITypeName(state, call.Type())
			if err != nil {
				return "", err
			}
			_, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), call.Type())
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
	case *mir.PanicTerm:
		if t.CleanupID >= 0 {
			return fmt.Sprintf("br label %%%s", llvmBlockLabel(state.fn, t.CleanupID)), nil
		}
		return lowerPanicTerm(state, t)
	case *mir.SwitchTerm:
		return "", fmt.Errorf("match lowering is not implemented yet")
	default:
		return "", fmt.Errorf("unsupported MIR terminator %T", term)
	}
}

// lowerCondValue lowers a MIR value to an i1 condition.
// Returns a multi-line string where the last line is the comparison instruction
// that defines the i1 temp. Use freshTempRef to extract the temp name.
func lowerCondValue(state *moduleState, cond mir.Value) (string, error) {
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

func lowerValue(state *moduleState, value mir.Value) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", fmt.Errorf("nil value")
	case *mir.LocalValue:
		if agg, ok := state.aggLocals[v.LocalID]; ok {
			return llvmLocalName(agg.PtrName), nil
		}
		if sc, ok := state.scalarLocals[v.LocalID]; ok {
			tmp := freshTemp(state, "ld")
			state.pendingLines = append(state.pendingLines,
				fmt.Sprintf("%s = load %s, ptr %s", tmp, sc.IRType, sc.AllocaName))
			return tmp, nil
		}
		return llvmLocalName(becommon.LocalNameByID(state.fn, v.LocalID)), nil
	case *mir.NameValue:
		if len(v.Path) == 1 {
			if local := becommon.FindLocalByName(state.fn, v.Path[0]); local != nil {
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
				return "@" + becommon.SanitizeIdent(v.LinkName), nil
			}
			return "@" + llvmSymbol(state, v.Path), nil
		}
		if !isAggregateType(state, v.Type()) {
			irType, err := llvmBaseType(v.Type())
			if err == nil && irType != "void" {
				tmp := freshTemp(state, "ld")
				sym := "@" + llvmSymbol(state, v.Path)
				if v.LinkName != "" {
					sym = "@" + becommon.SanitizeIdent(v.LinkName)
				}
				state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = load %s, ptr %s", tmp, irType, sym))
				return tmp, nil
			}
		}
		if v.LinkName != "" {
			return "@" + becommon.SanitizeIdent(v.LinkName), nil
		}
		return "@" + llvmSymbol(state, v.Path), nil
	case *mir.NumberValue:
		return llvmNumberLiteral(v.Type(), v.Value)
	case *mir.BoolValue:
		if v.Value {
			return "1", nil
		}
		return "0", nil
	case *mir.StringValue:
		// String literals are *i8 — emit a private global constant and return its address.
		sym := emitStringConstant(state, v.Value)
		return "@" + sym, nil
	case *mir.NoneValue:
		return "null", nil
	case *mir.UnaryValue:
		return lowerUnary(state, v)
	case *mir.AddrOfValue:
		return lowerAddrOf(state, v)
	case *mir.LoadValue:
		return "", fmt.Errorf("load value must be lowered in assignment/eval context")
	case *mir.BinaryValue:
		return lowerBinary(state, v)
	case *mir.CastValue:
		return lowerCast(state, v)
	case *mir.TypeTestValue:
		return lowerTypeTest(state, v)
	case *mir.CallValue:
		return "", fmt.Errorf("call value must be lowered in assignment/eval context")
	case *mir.FieldLoadValue:
		return "", fmt.Errorf("field load must be lowered in assignment context")
	case *mir.FieldValue:
		fieldIndex, err := llvmResolveFieldIndex(state, llvmFieldBaseType(state, v.Base), v.FieldIndex, v.MemberName)
		if err != nil {
			return "", err
		}
		lines, addr, fieldType, err := lowerFieldAddress(state, v.Base, fieldIndex)
		if err != nil {
			return "", err
		}
		irType, err := llvmBaseType(fieldType)
		if err != nil {
			return "", err
		}
		tmp := freshTemp(state, "fld")
		state.pendingLines = append(state.pendingLines, lines...)
		state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = load %s, ptr %s", tmp, irType, addr))
		return tmp, nil
	case *mir.IndexValue:
		return "", fmt.Errorf("index value must be lowered in assignment context")
	default:
		return "", fmt.Errorf("unsupported MIR value %T", value)
	}
}

func llvmFieldBaseType(state *moduleState, value mir.Value) typeinfo.Type {
	if value == nil {
		return nil
	}
	if typ := value.Type(); typ != nil {
		return typ
	}
	if local, ok := value.(*mir.LocalValue); ok {
		return becommon.LocalTypeByID(state.fn, local.LocalID)
	}
	return nil
}

func lowerTypeTest(state *moduleState, v *mir.TypeTestValue) (string, error) {
	if v == nil {
		return "", fmt.Errorf("nil type test")
	}
	if _, isNone := v.Left.(*mir.NoneValue); isNone {
		if _, ok := backend.UnwrapNamed(v.Left.Type()).(*typeinfo.OptionalType); ok {
			return "0", nil
		}
	}
	if !isUnionAggregate(v.Left.Type()) {
		return "", fmt.Errorf("unsupported runtime type test on %s", typeinfo.FormatType(typeStringer{v.Left.Type()}))
	}
	info, err := llvmUnionLayoutInfo(state, v.Left.Type())
	if err != nil {
		return "", err
	}
	memberIndex := 0
	if opt, ok := backend.UnwrapNamed(v.Left.Type()).(*typeinfo.OptionalType); ok {
		if !typeinfo.Equal(opt.Inner, v.Target) {
			return "", fmt.Errorf("optional type test target %s does not match %s", typeinfo.FormatType(typeStringer{v.Target}), typeinfo.FormatType(typeStringer{opt.Inner}))
		}
		memberIndex = 1
	} else {
		memberIndex, err = llvmUnionMemberIndex(info.Members, v.Target)
		if err != nil {
			return "", err
		}
	}
	src, err := lowerAggregateSource(state, v.Left)
	if err != nil {
		return "", err
	}
	tag := freshTemp(state, "uniontag")
	state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = load i32, ptr %s", tag, src))
	cmp := freshTemp(state, "istype")
	state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = icmp eq i32 %s, %d", cmp, tag, memberIndex))
	out := freshTemp(state, "istypev")
	state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = zext i1 %s to i8", out, cmp))
	return out, nil
}

func lowerAddrOf(state *moduleState, v *mir.AddrOfValue) (string, error) {
	switch src := v.Source.(type) {
	case *mir.LocalValue:
		if agg, ok := state.aggLocals[src.LocalID]; ok {
			return llvmLocalName(agg.PtrName), nil
		}
		if sc, ok := state.scalarLocals[src.LocalID]; ok {
			return sc.AllocaName, nil
		}
		return "", fmt.Errorf("addr_of on scalar SSA local not supported by llvm lowerer yet")
	case *mir.NameValue:
		if len(src.Path) == 1 {
			if local := becommon.FindLocalByName(state.fn, src.Path[0]); local != nil {
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
			return "@" + becommon.SanitizeIdent(src.LinkName), nil
		}
		return "@" + llvmSymbol(state, src.Path), nil
	default:
		return "", fmt.Errorf("unsupported addr_of source %T", v.Source)
	}
}

func lowerUnary(state *moduleState, v *mir.UnaryValue) (string, error) {
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

func lowerBinary(state *moduleState, v *mir.BinaryValue) (string, error) {
	// ?? (nil-coalesce) requires aggregate access to the optional — handle
	// it before the generic scalar paths.
	if v.Op == "??" {
		return lowerCoalesce(state, v)
	}
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

// lowerCoalesce lowers the ?? (nil-coalesce) operator for scalar inner types.
// It emits: load tag → icmp ne 0 → load payload → select → returns the result.
// The left operand must be an optional aggregate; the inner type must be scalar.
func lowerCoalesce(state *moduleState, v *mir.BinaryValue) (string, error) {
	opt, ok := backend.UnwrapNamed(v.Left.Type()).(*typeinfo.OptionalType)
	if !ok {
		return "", fmt.Errorf("?? operator requires optional left operand, got %s", typeinfo.FormatType(typeStringer{v.Left.Type()}))
	}
	innerIRType, err := llvmBaseType(opt.Inner)
	if err != nil {
		return "", fmt.Errorf("?? operator: inner type must be scalar, got %s: %w", typeinfo.FormatType(typeStringer{opt.Inner}), err)
	}

	// Degenerate: left is a literal none — always produce the fallback.
	if _, isNone := v.Left.(*mir.NoneValue); isNone {
		rhs, err := lowerValue(state, v.Right)
		if err != nil {
			return "", err
		}
		copyExpr, err := llvmCopyExpr(innerIRType, rhs)
		if err != nil {
			return "", err
		}
		return copyExpr, nil
	}

	// Get a pointer to the optional aggregate.
	src, err := lowerAggregateSource(state, v.Left)
	if err != nil {
		return "", fmt.Errorf("?? operator: cannot get optional source: %w", err)
	}

	// Compute payload offset using the union layout helper.
	info, err := llvmUnionLayoutInfo(state, v.Left.Type())
	if err != nil {
		return "", fmt.Errorf("?? operator: cannot get optional layout: %w", err)
	}

	// Load tag (i32 at offset 0).
	tag := freshTemp(state, "opttag")
	state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = load i32, ptr %s", tag, src))

	// Check if has value (tag != 0).
	hasVal := freshTemp(state, "opthasval")
	state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = icmp ne i32 %s, 0", hasVal, tag))

	// Load payload at payloadOffset.
	payload := freshTemp(state, "optpayload")
	if info.PayloadOffset != 0 {
		payloadPtr := freshTemp(state, "optpayloadptr")
		state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", payloadPtr, src, info.PayloadOffset))
		state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = load %s, ptr %s", payload, innerIRType, payloadPtr))
	} else {
		state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = load %s, ptr %s", payload, innerIRType, src))
	}

	// Lower the fallback (RHS).
	rhs, err := lowerValue(state, v.Right)
	if err != nil {
		return "", err
	}

	// Return the select expression; the caller will assign it to the target name.
	return fmt.Sprintf("select i1 %s, %s %s, %s %s", hasVal, innerIRType, payload, innerIRType, rhs), nil
}

func lowerCast(state *moduleState, v *mir.CastValue) (string, error) {
	if v == nil || v.Left == nil {
		return "", fmt.Errorf("invalid cast")
	}
	src := backend.UnwrapNamed(v.Left.Type())
	dst := backend.UnwrapNamed(v.Type())
	if call, ok, err := lowerStringSliceCast(state, src, dst, v.Left); ok || err != nil {
		return call, err
	}
	if _, ok := dst.(*typeinfo.StringType); ok {
		return lowerStringCast(state, v.Left)
	}
	if isUnionAggregate(v.Left.Type()) && !isAggregateType(state, v.Type()) {
		srcPtr, err := lowerUnionSource(state, v.Left)
		if err != nil {
			return "", err
		}
		irType, err := llvmBaseType(v.Type())
		if err != nil {
			return "", err
		}
		tmp := freshTemp(state, "unioncast")
		state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = load %s, ptr %s", tmp, irType, srcPtr))
		return llvmCopyExpr(irType, tmp)
	}
	if isAggregateType(state, v.Left.Type()) && isAggregateType(state, v.Type()) {
		return lowerValue(state, v.Left)
	}
	srcVal, err := lowerValue(state, v.Left)
	if err != nil {
		return "", err
	}
	srcBuiltin, srcIsBuiltin := src.(*typeinfo.BuiltinType)
	dstBuiltin, dstIsBuiltin := dst.(*typeinfo.BuiltinType)
	_, srcIsRawPtr := src.(*typeinfo.RawPtrType)
	_, dstIsRawPtr := dst.(*typeinfo.RawPtrType)

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
	if srcIsBuiltin && dstIsRawPtr {
		srcIR, err := llvmBaseType(src)
		if err != nil {
			return "", err
		}
		if srcVal == "0" {
			return llvmCopyExpr("ptr", "null")
		}
		return fmt.Sprintf("inttoptr %s %s to ptr", srcIR, srcVal), nil
	}
	if srcIsRawPtr && dstIsBuiltin {
		dstIR, err := llvmBaseType(dst)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("ptrtoint ptr %s to %s", srcVal, dstIR), nil
	}
	if llvmIsPointerLike(dst) {
		return llvmCopyExpr("ptr", srcVal)
	}
	return "", fmt.Errorf("unsupported cast from %s to %s", src, dst)
}

func lowerStringSliceCast(state *moduleState, src, dst typeinfo.Type, value mir.Value) (string, bool, error) {
	if _, ok := src.(*typeinfo.StringType); ok {
		elem, ok := sliceElementBuiltin(dst)
		if !ok {
			return "", false, nil
		}
		srcPtr, err := lowerAggregateSource(state, value)
		if err != nil {
			return "", true, err
		}
		switch elem {
		case "u8":
			return fmt.Sprintf("call { ptr, i64 } @ferret_global_str_bytes(ptr %s)", srcPtr), true, nil
		case "char":
			return fmt.Sprintf("call { ptr, i64 } @ferret_global_str_chars(ptr %s)", srcPtr), true, nil
		default:
			return "", false, nil
		}
	}
	if _, ok := dst.(*typeinfo.StringType); ok {
		elem, ok := sliceElementBuiltin(src)
		if !ok {
			return "", false, nil
		}
		srcPtr, err := lowerAggregateSource(state, value)
		if err != nil {
			return "", true, err
		}
		switch elem {
		case "u8":
			return fmt.Sprintf("call { ptr, i64 } @ferret_global_bytes_str(ptr %s)", srcPtr), true, nil
		case "char":
			return fmt.Sprintf("call { ptr, i64 } @ferret_global_chars_str(ptr %s)", srcPtr), true, nil
		default:
			return "", false, nil
		}
	}
	return "", false, nil
}

func isStringSliceCastPair(src, dst typeinfo.Type) bool {
	if _, ok := src.(*typeinfo.StringType); ok {
		elem, ok := sliceElementBuiltin(dst)
		return ok && (elem == "u8" || elem == "char")
	}
	if _, ok := dst.(*typeinfo.StringType); ok {
		elem, ok := sliceElementBuiltin(src)
		return ok && (elem == "u8" || elem == "char")
	}
	return false
}

func sliceElementBuiltin(typ typeinfo.Type) (string, bool) {
	sliceType, ok := typ.(*typeinfo.SliceType)
	if !ok || sliceType == nil {
		return "", false
	}
	builtin, ok := backend.UnwrapNamed(sliceType.Inner).(*typeinfo.BuiltinType)
	if !ok || builtin == nil {
		return "", false
	}
	return builtin.Name, true
}

func lowerUnionSource(state *moduleState, value mir.Value) (string, error) {
	switch v := value.(type) {
	case *mir.UnaryValue:
		if v.Op == "copy" || v.Op == "take" || v.Op == "comptime" {
			return lowerUnionSource(state, v.Right)
		}
	case *mir.LocalValue, *mir.NameValue:
		src, err := lowerAggregateSource(state, value)
		if err != nil {
			return "", err
		}
		info, err := llvmUnionLayoutInfo(state, value.Type())
		if err != nil {
			return "", err
		}
		if info.PayloadOffset == 0 {
			return src, nil
		}
		tmp := freshTemp(state, "unionpayload")
		state.pendingLines = append(state.pendingLines, fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", tmp, src, info.PayloadOffset))
		return tmp, nil
	}
	return "", fmt.Errorf("unsupported union source %T", value)
}

type backendUnionLayout struct {
	Size          int64
	Align         int64
	PayloadOffset int64
	Members       []typeinfo.Type
}

func llvmUnionLayoutInfo(state *moduleState, typ typeinfo.Type) (*backendUnionLayout, error) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		info, err := becommon.LookupNamedLayoutFromState(state.layouts, state.layout, state.mod, t, "llvm")
		if err != nil {
			return nil, err
		}
		if info == nil || info.Union == nil {
			return nil, fmt.Errorf("type %s has no union layout", t)
		}
		return &backendUnionLayout{
			Size:          info.Size,
			Align:         info.Align,
			PayloadOffset: info.Union.PayloadOffset,
			Members:       llvmUnionMemberTypes(info.Union),
		}, nil
	case *typeinfo.OptionalType:
		payloadSize, payloadAlign, err := aggregateSizeAlignOfPrimitive(t.Inner)
		if err != nil {
			payloadSize, payloadAlign, err = backend.AggregateSizeAlign(aggregateLayoutContext(state), t.Inner)
			if err != nil {
				return nil, err
			}
		}
		payloadOffset := backend.AlignUpInt64(4, payloadAlign)
		align := payloadAlign
		if align < 4 {
			align = 4
		}
		size := backend.AlignUpInt64(payloadOffset+payloadSize, align)
		return &backendUnionLayout{
			Size:          size,
			Align:         align,
			PayloadOffset: payloadOffset,
			Members:       []typeinfo.Type{t.Inner},
		}, nil
	case *typeinfo.UnionType:
		payloadSize := int64(0)
		payloadAlign := int64(1)
		for _, member := range t.Members {
			size, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), member)
			if err != nil {
				return nil, err
			}
			if size > payloadSize {
				payloadSize = size
			}
			if align > payloadAlign {
				payloadAlign = align
			}
		}
		payloadOffset := backend.AlignUpInt64(4, payloadAlign)
		align := payloadAlign
		if align < 4 {
			align = 4
		}
		size := backend.AlignUpInt64(payloadOffset+payloadSize, align)
		return &backendUnionLayout{Size: size, Align: align, PayloadOffset: payloadOffset, Members: t.Members}, nil
	default:
		return nil, fmt.Errorf("type %s has no union layout", typeinfo.FormatType(typeStringer{typ}))
	}
}

func llvmUnionMemberIndex(members []typeinfo.Type, memberType typeinfo.Type) (int, error) {
	exact := -1
	assignable := -1
	for i, member := range members {
		if typeinfo.Equal(member, memberType) {
			if exact >= 0 {
				return -1, fmt.Errorf("ambiguous union member for %s", typeinfo.FormatType(typeStringer{memberType}))
			}
			exact = i
			continue
		}
		if typeinfo.Assignable(member, memberType) {
			if assignable >= 0 {
				assignable = -2
			} else {
				assignable = i
			}
		}
	}
	if exact >= 0 {
		return exact, nil
	}
	if assignable >= 0 {
		return assignable, nil
	}
	return -1, fmt.Errorf("no union member for %s", typeinfo.FormatType(typeStringer{memberType}))
}

func llvmUnionMemberTypes(info *layout.UnionLayout) []typeinfo.Type {
	if info == nil {
		return nil
	}
	out := make([]typeinfo.Type, 0, len(info.Members))
	for _, member := range info.Members {
		if member != nil {
			out = append(out, member.Type)
		}
	}
	return out
}

func lowerStringCast(state *moduleState, value mir.Value) (string, error) {
	srcVal, err := lowerValue(state, value)
	if err != nil {
		return "", err
	}
	src := backend.UnwrapNamed(value.Type())
	srcBuiltin, ok := src.(*typeinfo.BuiltinType)
	if !ok {
		return "", fmt.Errorf("unsupported string cast source %s", src)
	}
	switch srcBuiltin.Name {
	case "i8", "i16", "i32":
		castExpr, _ := llvmIntCastOp(nil, srcBuiltin.Name, "i64", srcVal)
		return fmt.Sprintf("call { ptr, i64 } @ferret_global_i64_str(%s)", operandWithTemp(state, "i64", castExpr)), nil
	case "i64", "isize":
		if srcBuiltin.Name == "isize" {
			castExpr, _ := llvmIntCastOp(nil, srcBuiltin.Name, "i64", srcVal)
			return fmt.Sprintf("call { ptr, i64 } @ferret_global_i64_str(%s)", operandWithTemp(state, "i64", castExpr)), nil
		}
		return fmt.Sprintf("call { ptr, i64 } @ferret_global_i64_str(i64 %s)", srcVal), nil
	case "u8", "u16", "u32", "bool", "char":
		castExpr, _ := llvmIntCastOp(nil, srcBuiltin.Name, "u64", srcVal)
		return fmt.Sprintf("call { ptr, i64 } @ferret_global_u64_str(%s)", operandWithTemp(state, "i64", castExpr)), nil
	case "u64", "usize":
		if srcBuiltin.Name == "usize" {
			castExpr, _ := llvmIntCastOp(nil, srcBuiltin.Name, "u64", srcVal)
			return fmt.Sprintf("call { ptr, i64 } @ferret_global_u64_str(%s)", operandWithTemp(state, "i64", castExpr)), nil
		}
		return fmt.Sprintf("call { ptr, i64 } @ferret_global_u64_str(i64 %s)", srcVal), nil
	case "f32":
		castExpr, _ := llvmFloatCastOp("f32", "f64", srcVal)
		return fmt.Sprintf("call { ptr, i64 } @ferret_global_f64_str(%s)", operandWithTemp(state, "double", castExpr)), nil
	case "f64":
		return fmt.Sprintf("call { ptr, i64 } @ferret_global_f64_str(double %s)", srcVal), nil
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

func lowerCallee(state *moduleState, value mir.Value) (string, error) {
	switch v := value.(type) {
	case *mir.NameValue:
		if v.LinkName != "" {
			return becommon.SanitizeIdent(v.LinkName), nil
		}
		return llvmSymbol(state, v.Path), nil
	default:
		return "", fmt.Errorf("unsupported call callee %T", value)
	}
}

// ---------------------------------------------------------------------------
// Function state
// ---------------------------------------------------------------------------

func prepareFunctionState(state *moduleState, fn *mir.Function) error {
	state.fn = fn
	state.aggLocals = make(map[int]*aggregateLocal)
	state.aggParams = make(map[int]struct{})
	state.scalarLocals = make(map[int]*scalarAllocaLocal)
	state.tempValues = make(map[int]mir.Value)
	state.debugLocalVarIDs = make(map[int]int)
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
			size, align, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), local.Type)
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
			AllocaName: "%" + becommon.SanitizeIdent(local.Name) + "_alloca",
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
		if agg == nil {
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
		typeName := "i8"
		align := int64(1)
		if agg.Size > 0 {
			var err error
			typeName, err = llvmABITypeName(state, agg.Type)
			if err != nil {
				typeName = fmt.Sprintf("[%d x i8]", agg.Size)
			}
			align = normalizeAlign(agg.Align)
		}
		lines = append(lines, fmt.Sprintf("%s = alloca %s, align %d",
			llvmLocalName(agg.PtrName), typeName, align))
	}

	return lines
}

// ---------------------------------------------------------------------------
// Global composite
// ---------------------------------------------------------------------------

func lowerGlobalComposite(state *moduleState, typ typeinfo.Type, comp *mir.CompositeValue) (string, error) {
	if _, ok := typ.(*typeinfo.StringType); ok {
		return lowerGlobalStringLike(state, comp)
	}
	if _, ok := typ.(*typeinfo.SliceType); ok {
		return lowerGlobalStringLike(state, comp)
	}
	if tupleType, ok := backend.UnwrapNamed(typ).(*typeinfo.TupleType); ok {
		entries, size, _, err := backend.TupleLayout(aggregateLayoutContext(state), tupleType)
		if err != nil {
			return "", err
		}
		parts := make([]string, 0, len(entries)*2)
		offset := int64(0)
		for i, entry := range entries {
			if entry.Offset > offset {
				parts = append(parts, fmt.Sprintf("[%d x i8] zeroinitializer", entry.Offset-offset))
				offset = entry.Offset
			}
			if i >= len(comp.Items) {
				parts = append(parts, fmt.Sprintf("[%d x i8] zeroinitializer", entry.Size))
				offset += entry.Size
				continue
			}
			if isAggregateType(state, entry.Type) {
				body, err := lowerGlobalComposite(state, entry.Type, comp.Items[i].Value.(*mir.CompositeValue))
				if err != nil {
					return "", err
				}
				typeName, err := llvmABITypeName(state, entry.Type)
				if err != nil {
					return "", err
				}
				parts = append(parts, fmt.Sprintf("%s { %s }", typeName, body))
			} else {
				irType, err := llvmBaseType(entry.Type)
				if err != nil {
					return "", err
				}
				lit, err := lowerGlobalValue(state, entry.Type, comp.Items[i].Value)
				if err != nil {
					return "", err
				}
				parts = append(parts, fmt.Sprintf("%s %s", irType, lit))
			}
			offset += entry.Size
		}
		if size > offset {
			parts = append(parts, fmt.Sprintf("[%d x i8] zeroinitializer", size-offset))
		}
		return strings.Join(parts, ", "), nil
	}
	structLayout, err := becommon.LookupStructLayoutFromState(state.layouts, state.layout, state.mod, typ, "llvm")
	if err != nil {
		return "", err
	}
	items := make(map[string]mir.Value, len(comp.Items))
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

func lowerGlobalStringLike(state *moduleState, comp *mir.CompositeValue) (string, error) {
	items := make(map[string]mir.Value, len(comp.Items))
	for _, item := range comp.Items {
		items[item.Name] = item.Value
	}
	ptrLit, err := lowerGlobalValue(state, &typeinfo.RawPtrType{Inner: &typeinfo.BuiltinType{Name: "u8"}}, items["ptr"])
	if err != nil {
		return "", err
	}
	lenLit, err := lowerGlobalValue(state, &typeinfo.BuiltinType{Name: "usize"}, items["len"])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("{ ptr, i64 } { ptr %s, i64 %s }", ptrLit, lenLit), nil
}

func lowerGlobalValue(state *moduleState, typ typeinfo.Type, value mir.Value) (string, error) {
	switch v := value.(type) {
	case *mir.NumberValue:
		return llvmNumberLiteral(typ, v.Value)
	case *mir.BoolValue:
		if v.Value {
			return "1", nil
		}
		return "0", nil
	case *mir.NameValue:
		if v.LinkName != "" {
			return "@" + becommon.SanitizeIdent(v.LinkName), nil
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
	_, _, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), typ)
	return err == nil
}

func aggregateSizeAlignOfPrimitive(typ typeinfo.Type) (int64, int64, error) {
	switch t := backend.UnwrapNamed(typ).(type) {
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
	case *typeinfo.PointerType, *typeinfo.RefType, *typeinfo.RawPtrType:
		return 8, 8, nil
	}
	return 0, 0, fmt.Errorf("not a primitive type")
}

func aggregateLayoutContext(state *moduleState) backend.AggregateLayoutContext {
	return backend.AggregateLayoutContext{
		BackendName:     "llvm",
		ScalarSizeAlign: aggregateSizeAlignOfPrimitive,
		OptionalSizeFunc: func(optional typeinfo.Type) (int64, int64, error) {
			info, err := llvmUnionLayoutInfo(state, optional)
			if err != nil {
				return 0, 0, err
			}
			return info.Size, info.Align, nil
		},
		LookupNamed: func(named *typeinfo.NamedType) (*layout.TypeLayout, error) {
			return becommon.LookupNamedLayoutFromState(state.layouts, state.layout, state.mod, named, "llvm")
		},
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
		info, err := becommon.LookupNamedLayoutFromState(state.layouts, state.layout, state.mod, named, "llvm")
		if err == nil && info != nil && info.Struct != nil {
			return "%" + llvmTypeName(state, named), nil
		}
	}
	if opt, ok := typ.(*typeinfo.OptionalType); ok {
		if !backend.OptionalUsesNiche(opt.Inner) {
			info, err := llvmUnionLayoutInfo(state, typ)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("[%d x i8]", info.Size), nil
		}
	}
	if _, ok := typ.(*typeinfo.StringType); ok {
		return "{ ptr, i64 }", nil
	}
	if _, ok := typ.(*typeinfo.SliceType); ok {
		return "{ ptr, i64 }", nil
	}
	if _, ok := backend.UnwrapNamed(typ).(*typeinfo.TupleType); ok {
		size, _, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), typ)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[%d x i8]", size), nil
	}
	return llvmBaseType(typ)
}

// ---------------------------------------------------------------------------
// Type helpers
// ---------------------------------------------------------------------------

// llvmABITypeName returns the LLVM type name for function signatures, calls, globals.
func llvmABITypeName(state *moduleState, typ typeinfo.Type) (string, error) {
	if _, ok := backend.UnwrapNamed(typ).(*typeinfo.TupleType); ok {
		size, _, err := backend.AggregateSizeAlign(aggregateLayoutContext(state), typ)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[%d x i8]", size), nil
	}
	switch backend.ClassifyABIType(typ, func(named *typeinfo.NamedType) bool {
		info, err := becommon.LookupNamedLayoutFromState(state.layouts, state.layout, state.mod, named, "llvm")
		return err == nil && info != nil && info.Known && (info.Struct != nil || backend.IsNamedUnion(named) || backend.IsNamedInterface(named))
	}) {
	case backend.ABITypeNamedLayout:
		named := typ.(*typeinfo.NamedType)
		return "%" + llvmTypeName(state, named), nil
	case backend.ABITypeNamedInterface:
		return "{ ptr, ptr }", nil
	case backend.ABITypeOptionalAggregate:
		info, err := llvmUnionLayoutInfo(state, typ)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[%d x i8]", info.Size), nil
	case backend.ABITypeSliceLike:
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
	b, ok := backend.UnwrapNamed(typ).(*typeinfo.BuiltinType)
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
	typ = backend.UnwrapNamed(typ)
	if llvmIsPointerLike(typ) {
		return lit, nil
	}
	b, ok := typ.(*typeinfo.BuiltinType)
	if !ok {
		return "", fmt.Errorf("unsupported numeric literal type %s", typ)
	}
	switch b.Name {
	case "f32":
		v, err := strconv.ParseFloat(lit, 32)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("0x%016X", math.Float64bits(float64(float32(v)))), nil
	case "f64":
		v, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("0x%016X", math.Float64bits(v)), nil
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

func llvmValueNeedsCopy(v mir.Value) bool {
	switch v.(type) {
	case *mir.LocalValue, *mir.NameValue,
		*mir.NumberValue, *mir.BoolValue,
		*mir.NoneValue, *mir.AddrOfValue,
		*mir.TypeTestValue:
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
		name := becommon.SanitizeIdent(path[0])
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
	if len(path) == 2 && state != nil {
		if localMethod, ok := becommon.ResolveStaticOwnerLocalName(path, state.functions, state.globals); ok {
			return state.modulePrefix + "__" + becommon.SanitizeIdent(localMethod)
		}
	}
	clean := make([]string, 0, len(path))
	for _, part := range path {
		clean = append(clean, becommon.SanitizeIdent(part))
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
	prefix := becommon.SanitizePath(moduleKey)
	if prefix == "" {
		prefix = state.modulePrefix
	}
	return prefix + "__" + becommon.SanitizeIdent(named.Name)
}

func llvmBlockLabel(fn *mir.Function, id int) string {
	if fn != nil && id == fn.EntryID {
		return "entry"
	}
	return fmt.Sprintf("bb%d", id)
}

func llvmLocalName(name string) string {
	if name == "" {
		return "%_tmp"
	}
	return "%" + becommon.SanitizeIdent(name)
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

func irTypeSize(irType string) int64 {
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
			fmt.Fprintf(&b, "\\%02X", c)
		}
	}
	b.WriteString("\\00\"")
	return b.String()
}

type typeStringer struct {
	t typeinfo.Type
}

func (s typeStringer) String() string {
	if s.t == nil {
		return "void"
	}
	return s.t.String()
}
