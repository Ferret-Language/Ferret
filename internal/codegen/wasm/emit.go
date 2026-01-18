package wasm

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"compiler/internal/context_v2"
	"compiler/internal/mir"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
	ustrings "compiler/internal/utils/strings"
)

type Unit struct {
	Module   *context_v2.Module
	MIR      *mir.Module
	IsEntry  bool
	EntryMod string
}

type funcInfo struct {
	name      string
	fn        *mir.Function
	mod       *context_v2.Module
	valueType map[mir.ValueID]types.SemType
}

type funcSig struct {
	params  []ValType
	results []ValType
}

type Generator struct {
	ctx *context_v2.CompilerContext

	layout *mir.DataLayout
	units  []Unit

	funcs       map[string]*funcInfo
	entryModule string

	imports   map[string]funcSig
	importIDs map[string]uint32

	tableIndex  map[string]uint32 // function name -> table index for call_indirect
	typeIndices map[string]uint32 // signature string -> type index

	dataBase    uint32
	data        []byte
	stringPtrs  map[string]uint32
	dataSymbols map[string]uint32
}

func EmitProgram(ctx *context_v2.CompilerContext, units []Unit) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("wasm: missing compiler context")
	}
	gen := &Generator{
		ctx:         ctx,
		layout:      mir.NewDataLayout(4),
		units:       units,
		funcs:       make(map[string]*funcInfo),
		imports:     make(map[string]funcSig),
		importIDs:   make(map[string]uint32),
		tableIndex:  make(map[string]uint32),
		typeIndices: make(map[string]uint32),
		dataBase:    1024,
		stringPtrs:  make(map[string]uint32),
		dataSymbols: make(map[string]uint32),
	}
	for _, u := range units {
		if u.IsEntry {
			gen.entryModule = u.Module.ImportPath
			break
		}
	}
	if gen.entryModule == "" && len(units) > 0 && units[0].Module != nil {
		gen.entryModule = units[0].Module.ImportPath
	}

	if err := gen.collectFunctions(); err != nil {
		return nil, err
	}
	if err := gen.collectImports(); err != nil {
		return nil, err
	}

	mod := &ModuleBuilder{}

	importNames := make([]string, 0, len(gen.imports))
	for name := range gen.imports {
		importNames = append(importNames, name)
	}
	sortStrings(importNames)
	for _, name := range importNames {
		sig := gen.imports[name]
		typeIndex := mod.addType(sig.params, sig.results)
		index := mod.addImportFunc("ferret", name, typeIndex)
		gen.importIDs[name] = index
		gen.typeIndices[sigToString(sig)] = typeIndex
	}

	functionIndex := make(map[string]uint32)
	funcOrder := make([]string, 0, len(gen.funcs))
	for name := range gen.funcs {
		funcOrder = append(funcOrder, name)
	}
	sortStrings(funcOrder)
	baseIndex := uint32(len(importNames))
	for i, name := range funcOrder {
		functionIndex[name] = baseIndex + uint32(i)
	}

	// Populate function table with closures and function wrappers BEFORE emitting functions
	// These are functions that need to be callable via call_indirect
	tableNames := make([]string, 0)
	for _, name := range funcOrder {
		// Add closures (__func_lit__*) and function wrappers (__func_wrap__*) to table
		if strings.Contains(name, "__func_lit__") || strings.Contains(name, "__func_wrap__") || strings.Contains(name, "__iface_") {
			tableNames = append(tableNames, name)
		}
	}
	if len(tableNames) > 0 {
		mod.setTableSize(uint32(len(tableNames)))
		for _, name := range tableNames {
			if idx, ok := functionIndex[name]; ok {
				tableIdx := mod.addTableElement(idx)
				gen.tableIndex[name] = tableIdx
			}
		}
		mod.addExport("__indirect_function_table", exportKindTable, 0)
	}

	if err := gen.emitVTables(); err != nil {
		return nil, err
	}
	if err := gen.emitTypeIDs(); err != nil {
		return nil, err
	}

	for _, name := range funcOrder {
		info := gen.funcs[name]
		sig, err := gen.funcSignature(info.fn)
		if err != nil {
			return nil, err
		}
		typeIndex := mod.addType(sig.params, sig.results)
		gen.typeIndices[sigToString(sig)] = typeIndex
		locals, body, err := gen.emitFunction(info, functionIndex)
		if err != nil {
			return nil, err
		}
		mod.addFunction(typeIndex, locals, body)
	}

	for _, name := range funcOrder {
		info := gen.funcs[name]
		if info.fn != nil && info.fn.Name == "main" && gen.isEntryModule(info.mod) {
			if idx, ok := functionIndex[info.name]; ok {
				mod.addExport("main", exportKindFunc, idx)
			}
		}
	}
	mod.addExport("memory", exportKindMem, 0)

	dataEnd := gen.dataBase + uint32(len(gen.data))
	dataInit := append([]byte{0x41}, encodeU32(uint32(dataEnd))...)
	globalIndex := mod.addGlobal(valTypeI32, false, dataInit)
	mod.addExport("__data_end", exportKindGlobal, globalIndex)

	if len(gen.data) > 0 {
		mod.addData(gen.dataBase, gen.data)
	}

	mod.memoryMin = gen.memoryMinPages()
	return mod.emit(), nil
}

func (g *Generator) collectFunctions() error {
	for _, unit := range g.units {
		if unit.Module == nil || unit.MIR == nil {
			continue
		}
		for _, fn := range unit.MIR.Functions {
			if fn == nil {
				continue
			}
			name := g.funcName(fn.Name, unit.Module.ImportPath)
			if name == "" {
				continue
			}
			info := &funcInfo{
				name:      name,
				fn:        fn,
				mod:       unit.Module,
				valueType: analyzeFunctionTypes(fn),
			}
			g.funcs[name] = info
		}
	}
	if len(g.funcs) == 0 {
		return fmt.Errorf("wasm: no functions to emit")
	}
	return nil
}

func (g *Generator) collectImports() error {
	if err := g.ensureImport("ferret_alloc", funcSig{
		params:  []ValType{valTypeI64},
		results: []ValType{valTypeI32},
	}); err != nil {
		return err
	}
	if err := g.ensureImport("ferret_memcpy", funcSig{
		params: []ValType{valTypeI32, valTypeI32, valTypeI64},
	}); err != nil {
		return err
	}
	if err := g.ensureImport("ferret_array_get", funcSig{
		params:  []ValType{valTypeI32, valTypeI32},
		results: []ValType{valTypeI32},
	}); err != nil {
		return err
	}
	if err := g.ensureImport("ferret_array_new", funcSig{
		params:  []ValType{valTypeI32, valTypeI32},
		results: []ValType{valTypeI32},
	}); err != nil {
		return err
	}
	if err := g.ensureImport("ferret_array_append", funcSig{
		params:  []ValType{valTypeI32, valTypeI32},
		results: []ValType{valTypeI32},
	}); err != nil {
		return err
	}
	if err := g.ensureImport("ferret_array_set", funcSig{
		params:  []ValType{valTypeI32, valTypeI32, valTypeI32},
		results: []ValType{valTypeI32},
	}); err != nil {
		return err
	}
	for _, info := range g.funcs {
		if info == nil || info.fn == nil {
			continue
		}
		for _, block := range info.fn.Blocks {
			if block == nil {
				continue
			}
			for _, instr := range block.Instrs {
				if _, ok := instr.(*mir.Alloca); ok {
					if err := g.ensureImport("ferret_alloc", funcSig{
						params:  []ValType{valTypeI64},
						results: []ValType{valTypeI32},
					}); err != nil {
						return err
					}
				}
				if _, ok := instr.(*mir.MapGet); ok {
					if err := g.ensureImport("ferret_map_get", funcSig{
						params:  []ValType{valTypeI32, valTypeI32},
						results: []ValType{valTypeI32},
					}); err != nil {
						return err
					}
					if err := g.ensureImport("ferret_map_get_optional_out", funcSig{
						params: []ValType{valTypeI32, valTypeI32, valTypeI32},
					}); err != nil {
						return err
					}
					if err := g.ensureImport("ferret_global_panic", funcSig{
						params: []ValType{valTypeI32},
					}); err != nil {
						return err
					}
				}
				if _, ok := instr.(*mir.MapSet); ok {
					if err := g.ensureImport("ferret_map_set", funcSig{
						params: []ValType{valTypeI32, valTypeI32, valTypeI32},
					}); err != nil {
						return err
					}
				}
				if bin, ok := instr.(*mir.Binary); ok && bin.Op == tokens.EXP_TOKEN {
					if err := g.ensureImport("ferret_pow", funcSig{
						params:  []ValType{valTypeF64, valTypeF64},
						results: []ValType{valTypeF64},
					}); err != nil {
						return err
					}
				}
				if opt, ok := instr.(*mir.OptionalUnwrap); ok && opt.HasDefault {
					if err := g.ensureImport("ferret_optional_unwrap_or", funcSig{
						params: []ValType{valTypeI32, valTypeI32, valTypeI32, valTypeI64},
					}); err != nil {
						return err
					}
				}
				call, ok := instr.(*mir.Call)
				if !ok || call == nil {
					continue
				}
				target, err := g.resolveCallTarget(info.mod, call.Target)
				if err != nil {
					g.reportError(err.Error(), call.Loc())
					continue
				}
				if _, ok := g.funcs[target]; ok {
					continue
				}
				sig, err := g.callSignature(info, call)
				if err != nil {
					return err
				}
				if err := g.ensureImport(target, sig); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (g *Generator) ensureImport(name string, sig funcSig) error {
	if existing, ok := g.imports[name]; ok {
		if !sigEqual(existing, sig) {
			return fmt.Errorf("wasm: conflicting signatures for import %s", name)
		}
		return nil
	}
	g.imports[name] = sig
	return nil
}

func (g *Generator) emitFunction(info *funcInfo, functionIndex map[string]uint32) ([]ValType, []byte, error) {
	if info == nil || info.fn == nil {
		return nil, nil, fmt.Errorf("wasm: missing function")
	}

	paramCount := len(info.fn.Params)
	localIndex := make(map[mir.ValueID]uint32)
	locals := make([]ValType, 0, 8)

	for i, p := range info.fn.Params {
		localIndex[p.ID] = uint32(i)
	}

	blockLocal := uint32(paramCount)
	locals = append(locals, valTypeI32)

	scratchLocal := blockLocal + 1
	locals = append(locals, valTypeI32)

	nextLocal := scratchLocal + 1
	allocLocal := func(id mir.ValueID, typ types.SemType) error {
		if id == mir.InvalidValue {
			return nil
		}
		if _, ok := localIndex[id]; ok {
			return nil
		}
		valType, err := wasmValueType(typ)
		if err != nil {
			return err
		}
		localIndex[id] = nextLocal
		locals = append(locals, valType)
		nextLocal++
		return nil
	}

	for _, block := range info.fn.Blocks {
		for _, instr := range block.Instrs {
			switch v := instr.(type) {
			case *mir.Const:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.Binary:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.Unary:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.Cast:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.Alloca:
				if err := allocLocal(v.Result, types.NewReference(v.Type)); err != nil {
					return nil, nil, err
				}
			case *mir.Load:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.PtrAdd:
				if err := allocLocal(v.Result, types.NewReference(v.Elem)); err != nil {
					return nil, nil, err
				}
			case *mir.PtrOffset:
				if err := allocLocal(v.Result, types.NewReference(v.Elem)); err != nil {
					return nil, nil, err
				}
			case *mir.Call:
				if v.Result != mir.InvalidValue && !isVoidType(v.Type) {
					if err := allocLocal(v.Result, v.Type); err != nil {
						return nil, nil, err
					}
				}
			case *mir.CallIndirect:
				if v.Result != mir.InvalidValue && !isVoidType(v.Type) {
					if err := allocLocal(v.Result, v.Type); err != nil {
						return nil, nil, err
					}
				}
			case *mir.Phi:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.ArrayGet:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.MapGet:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.OptionalNone:
				if err := allocLocal(v.Result, types.NewReference(v.Type)); err != nil {
					return nil, nil, err
				}
			case *mir.OptionalSome:
				if err := allocLocal(v.Result, types.NewReference(v.Type)); err != nil {
					return nil, nil, err
				}
			case *mir.OptionalIsSome:
				if err := allocLocal(v.Result, types.TypeBool); err != nil {
					return nil, nil, err
				}
			case *mir.OptionalUnwrap:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.UnionVariantCheck:
				if err := allocLocal(v.Result, types.TypeBool); err != nil {
					return nil, nil, err
				}
			case *mir.UnionExtract:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			case *mir.ResultOk:
				if err := allocLocal(v.Result, types.NewReference(v.Type)); err != nil {
					return nil, nil, err
				}
			case *mir.ResultErr:
				if err := allocLocal(v.Result, types.NewReference(v.Type)); err != nil {
					return nil, nil, err
				}
			case *mir.ResultIsOk:
				if err := allocLocal(v.Result, types.TypeBool); err != nil {
					return nil, nil, err
				}
			case *mir.ResultUnwrap:
				if err := allocLocal(v.Result, v.Type); err != nil {
					return nil, nil, err
				}
			}
		}
	}

	blockIndex := make(map[mir.BlockID]uint32)
	for i, block := range info.fn.Blocks {
		if block == nil {
			continue
		}
		blockIndex[block.ID] = uint32(i)
	}

	phiMoves := collectPhiMoves(info.fn.Blocks)

	var code []byte
	entryIdx := uint32(0)
	if len(info.fn.Blocks) > 0 {
		entryIdx = blockIndex[info.fn.Blocks[0].ID]
	}
	code = append(code, opcodeI32Const)
	code = append(code, encodeU32(entryIdx)...)
	code = append(code, opcodeLocalSet)
	code = append(code, encodeU32(blockLocal)...)

	code = append(code, opcodeLoop, blockTypeVoid)

	for _, block := range info.fn.Blocks {
		if block == nil {
			continue
		}
		idx := blockIndex[block.ID]
		code = append(code, opcodeLocalGet)
		code = append(code, encodeU32(blockLocal)...)
		code = append(code, opcodeI32Const)
		code = append(code, encodeU32(idx)...)
		code = append(code, opcodeI32Eq)
		code = append(code, opcodeIf, blockTypeVoid)
		for _, instr := range block.Instrs {
			if _, ok := instr.(*mir.Phi); ok {
				continue
			}
			insn, err := g.emitInstr(info, instr, localIndex, functionIndex, scratchLocal)
			if err != nil {
				return nil, nil, err
			}
			code = append(code, insn...)
		}
		term, err := g.emitTerm(info, block, localIndex, functionIndex, blockIndex, blockLocal, phiMoves)
		if err != nil {
			return nil, nil, err
		}
		code = append(code, term...)
		code = append(code, opcodeEnd)
	}

	code = append(code, opcodeBr)
	code = append(code, encodeU32(0)...)
	code = append(code, opcodeEnd)

	if info.fn != nil && !isVoidType(info.fn.Return) {
		defaultVal, err := g.emitDefaultValue(info.fn.Return)
		if err != nil {
			return nil, nil, err
		}
		code = append(code, defaultVal...)
		code = append(code, opcodeReturn)
	}

	return locals, code, nil
}

func (g *Generator) emitInstr(info *funcInfo, instr mir.Instr, locals map[mir.ValueID]uint32, fnIndex map[string]uint32, scratchLocal uint32) ([]byte, error) {
	switch v := instr.(type) {
	case *mir.Const:
		return g.emitConst(v, locals)
	case *mir.Binary:
		return g.emitBinary(info, v, locals)
	case *mir.Unary:
		return g.emitUnary(v, locals)
	case *mir.Cast:
		return g.emitCast(info, v, locals)
	case *mir.Alloca:
		return g.emitAlloca(v, locals)
	case *mir.Load:
		return g.emitLoad(v, locals)
	case *mir.Store:
		return g.emitStore(info, v, locals)
	case *mir.PtrAdd:
		return g.emitPtrAdd(v, locals)
	case *mir.PtrOffset:
		return g.emitPtrOffset(v, locals)
	case *mir.Call:
		return g.emitCall(info, v, locals, fnIndex)
	case *mir.CallIndirect:
		return g.emitCallIndirect(info, v, locals, fnIndex)
	case *mir.ArrayGet:
		return g.emitArrayGet(info, v, locals)
	case *mir.ArraySet:
		return g.emitArraySet(info, v, locals, scratchLocal)
	case *mir.MapGet:
		return g.emitMapGet(info, instr.(*mir.MapGet), locals, scratchLocal)
	case *mir.MapSet:
		return g.emitMapSet(info, instr.(*mir.MapSet), locals, scratchLocal)
	case *mir.OptionalNone:
		return g.emitOptionalNone(v, locals)
	case *mir.OptionalSome:
		return g.emitOptionalSome(v, locals)
	case *mir.OptionalIsSome:
		return g.emitOptionalIsSome(info, v, locals)
	case *mir.OptionalUnwrap:
		return g.emitOptionalUnwrap(info, v, locals, scratchLocal)
	case *mir.UnionVariantCheck:
		return g.emitUnionVariantCheck(v, locals)
	case *mir.UnionExtract:
		return g.emitUnionExtract(v, locals, scratchLocal)
	case *mir.ResultOk:
		return g.emitResultOk(v, locals)
	case *mir.ResultErr:
		return g.emitResultErr(v, locals)
	case *mir.ResultIsOk:
		return g.emitResultIsOk(info, v, locals)
	case *mir.ResultUnwrap:
		return g.emitResultUnwrap(info, v, locals)
	default:
		g.reportUnsupported("instruction", instr.Loc())
		return []byte{opcodeUnreachable}, nil
	}
}

func (g *Generator) emitTerm(info *funcInfo, block *mir.Block, locals map[mir.ValueID]uint32, fnIndex map[string]uint32, blockIndex map[mir.BlockID]uint32, blockLocal uint32, phiMoves map[mir.BlockID]map[mir.BlockID][]phiMove) ([]byte, error) {
	if block == nil || block.Term == nil {
		return nil, nil
	}
	switch t := block.Term.(type) {
	case *mir.Return:
		var out []byte
		if t.HasValue && t.Value != mir.InvalidValue {
			out = append(out, opcodeLocalGet)
			out = append(out, encodeU32(locals[t.Value])...)
		}
		out = append(out, opcodeReturn)
		return out, nil
	case *mir.Br:
		return g.emitBranch(block.ID, t.Target, locals, blockIndex, blockLocal, phiMoves), nil
	case *mir.CondBr:
		var out []byte
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[t.Cond])...)
		out = append(out, opcodeIf, blockTypeVoid)
		out = append(out, g.emitPhiMoves(block.ID, t.Then, locals, phiMoves)...)
		out = append(out, opcodeI32Const)
		out = append(out, encodeU32(blockIndex[t.Then])...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(blockLocal)...)
		out = append(out, opcodeElse)
		out = append(out, g.emitPhiMoves(block.ID, t.Else, locals, phiMoves)...)
		out = append(out, opcodeI32Const)
		out = append(out, encodeU32(blockIndex[t.Else])...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(blockLocal)...)
		out = append(out, opcodeEnd)
		out = append(out, opcodeBr)
		out = append(out, encodeU32(0)...)
		return out, nil
	case *mir.Switch:
		g.reportUnsupported("switch", t.Loc())
		return []byte{opcodeUnreachable}, nil
	case *mir.Unreachable:
		return []byte{opcodeUnreachable}, nil
	default:
		g.reportUnsupported("terminator", t.Loc())
		return []byte{opcodeUnreachable}, nil
	}
}

func (g *Generator) emitBranch(pred, target mir.BlockID, locals map[mir.ValueID]uint32, blockIndex map[mir.BlockID]uint32, blockLocal uint32, phiMoves map[mir.BlockID]map[mir.BlockID][]phiMove) []byte {
	var out []byte
	out = append(out, g.emitPhiMoves(pred, target, locals, phiMoves)...)
	out = append(out, opcodeI32Const)
	out = append(out, encodeU32(blockIndex[target])...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(blockLocal)...)
	out = append(out, opcodeBr)
	out = append(out, encodeU32(0)...)
	return out
}

func (g *Generator) emitPhiMoves(pred, succ mir.BlockID, locals map[mir.ValueID]uint32, phiMoves map[mir.BlockID]map[mir.BlockID][]phiMove) []byte {
	movesBySucc, ok := phiMoves[pred]
	if !ok {
		return nil
	}
	moves := movesBySucc[succ]
	if len(moves) == 0 {
		return nil
	}
	var out []byte
	for _, move := range moves {
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[move.src])...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[move.dst])...)
	}
	return out
}

func (g *Generator) emitConst(c *mir.Const, locals map[mir.ValueID]uint32) ([]byte, error) {
	if c == nil {
		return nil, nil
	}

	valType, err := wasmValueType(c.Type)
	if err != nil {
		return nil, err
	}

	var out []byte
	switch valType {
	case valTypeI32:
		if strings.HasPrefix(c.Value, "$") {
			symbol := strings.TrimPrefix(c.Value, "$")
			if ptr, ok := g.dataSymbols[symbol]; ok {
				out = append(out, opcodeI32Const)
				out = append(out, encodeS32(int32(ptr))...)
				out = append(out, opcodeLocalSet)
				out = append(out, encodeU32(locals[c.Result])...)
				return out, nil
			}
			return nil, fmt.Errorf("wasm: unknown data symbol %s", c.Value)
		}
		// Try to resolve as function name first (for closures with FunctionType)
		found := false
		for fullName, idx := range g.tableIndex {
			// Match by suffix - the const has the unqualified name, table has qualified
			// e.g., const: "__func_lit__1", table: "examples_closure_capture___func_lit__1"
			if fullName == c.Value || strings.HasSuffix(fullName, c.Value) {
				// WASM table indices are i32
				out = append(out, opcodeI32Const)
				out = append(out, encodeS32(int32(idx))...)
				out = append(out, opcodeLocalSet)
				out = append(out, encodeU32(locals[c.Result])...)
				found = true
				break
			}
		}
		if found {
			return out, nil
		}
		// Fall back to normal i32 const parsing
		value, err := parseI32Const(c.Type, c.Value, g.stringPtr)
		if err != nil {
			return nil, err
		}
		out = append(out, opcodeI32Const)
		out = append(out, encodeS32(value)...)
	case valTypeI64:
		// Try to resolve as function name first (for closures)
		for fullName, idx := range g.tableIndex {
			if fullName == c.Value || strings.HasSuffix(fullName, "_"+c.Value) {
				out = append(out, opcodeI64Const)
				out = append(out, encodeS64(int64(idx))...)
				out = append(out, opcodeLocalSet)
				out = append(out, encodeU32(locals[c.Result])...)
				return out, nil
			}
		}
		// Fall back to numeric parsing
		value, err := parseI64Const(c.Type, c.Value)
		if err != nil {
			// If numeric parsing fails and it looks like a function name, report better error
			if !strings.ContainsAny(c.Value, "0123456789") || strings.Contains(c.Value, "funclit") {
				return nil, fmt.Errorf("wasm: unresolved function name in const: %s (not in table)", c.Value)
			}
			return nil, err
		}
		out = append(out, opcodeI64Const)
		out = append(out, encodeS64(value)...)
	case valTypeF32:
		value, err := strconv.ParseFloat(stripNumeric(c.Value), 32)
		if err != nil {
			return nil, err
		}
		out = append(out, opcodeF32Const)
		out = append(out, encodeF32(float32(value))...)
	case valTypeF64:
		value, err := strconv.ParseFloat(stripNumeric(c.Value), 64)
		if err != nil {
			return nil, err
		}
		out = append(out, opcodeF64Const)
		out = append(out, encodeF64(value)...)
	default:
		return nil, fmt.Errorf("wasm: unsupported const type")
	}
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[c.Result])...)
	return out, nil
}

func (g *Generator) emitDefaultValue(typ types.SemType) ([]byte, error) {
	valType, err := wasmValueType(typ)
	if err != nil {
		return nil, err
	}
	switch valType {
	case valTypeI32:
		return []byte{opcodeI32Const, 0x00}, nil
	case valTypeI64:
		return []byte{opcodeI64Const, 0x00}, nil
	case valTypeF32:
		out := []byte{opcodeF32Const}
		out = append(out, encodeF32(0)...)
		return out, nil
	case valTypeF64:
		out := []byte{opcodeF64Const}
		out = append(out, encodeF64(0)...)
		return out, nil
	default:
		return nil, fmt.Errorf("wasm: unsupported default value type")
	}
}

func (g *Generator) emitBinary(info *funcInfo, b *mir.Binary, locals map[mir.ValueID]uint32) ([]byte, error) {
	if b == nil {
		return nil, nil
	}
	if b.Op == tokens.EXP_TOKEN {
		return g.emitPow(info, b, locals)
	}
	opType := b.Type
	if isCompareToken(b.Op) && info != nil {
		if leftType, ok := info.valueType[b.Left]; ok && leftType != nil {
			opType = leftType
		}
	}
	valType, err := wasmValueType(opType)
	if err != nil {
		return nil, err
	}
	opcode, err := binaryOpcode(b.Op, valType, isUnsignedType(opType))
	if err != nil {
		g.reportError(err.Error(), b.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[b.Left])...)
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[b.Right])...)
	out = append(out, opcode...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[b.Result])...)
	return out, nil
}

func (g *Generator) emitPow(info *funcInfo, b *mir.Binary, locals map[mir.ValueID]uint32) ([]byte, error) {
	if b == nil {
		return nil, nil
	}
	if info == nil {
		return nil, fmt.Errorf("wasm: missing pow context")
	}
	leftType := info.valueType[b.Left]
	rightType := info.valueType[b.Right]
	if leftType == nil || rightType == nil {
		return nil, fmt.Errorf("wasm: missing pow operand type")
	}
	powID, ok := g.importIDs["ferret_pow"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_pow")
	}
	var out []byte
	leftBytes, err := g.emitValueAsF64(b.Left, leftType, locals)
	if err != nil {
		return nil, err
	}
	rightBytes, err := g.emitValueAsF64(b.Right, rightType, locals)
	if err != nil {
		return nil, err
	}
	out = append(out, leftBytes...)
	out = append(out, rightBytes...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(powID)...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[b.Result])...)
	return out, nil
}

func (g *Generator) emitValueAsF64(id mir.ValueID, typ types.SemType, locals map[mir.ValueID]uint32) ([]byte, error) {
	valType, err := wasmValueType(typ)
	if err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[id])...)
	switch valType {
	case valTypeF64:
		return out, nil
	case valTypeF32:
		out = append(out, opcodeF64PromoteF32)
		return out, nil
	case valTypeI32:
		if isUnsignedType(typ) {
			out = append(out, opcodeF64ConvertI32U)
		} else {
			out = append(out, opcodeF64ConvertI32S)
		}
		return out, nil
	case valTypeI64:
		if isUnsignedType(typ) {
			out = append(out, opcodeF64ConvertI64U)
		} else {
			out = append(out, opcodeF64ConvertI64S)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("wasm: unsupported pow operand type")
	}
}

func (g *Generator) emitUnary(u *mir.Unary, locals map[mir.ValueID]uint32) ([]byte, error) {
	if u == nil {
		return nil, nil
	}
	valType, err := wasmValueType(u.Type)
	if err != nil {
		return nil, err
	}
	var out []byte
	switch u.Op {
	case tokens.MINUS_TOKEN:
		switch valType {
		case valTypeI32:
			out = append(out, opcodeI32Const)
			out = append(out, encodeS32(0)...)
			out = append(out, opcodeLocalGet)
			out = append(out, encodeU32(locals[u.X])...)
			out = append(out, opcodeI32Sub)
		case valTypeI64:
			out = append(out, opcodeI64Const)
			out = append(out, encodeS64(0)...)
			out = append(out, opcodeLocalGet)
			out = append(out, encodeU32(locals[u.X])...)
			out = append(out, opcodeI64Sub)
		case valTypeF32:
			out = append(out, opcodeLocalGet)
			out = append(out, encodeU32(locals[u.X])...)
			out = append(out, opcodeF32Neg)
		case valTypeF64:
			out = append(out, opcodeLocalGet)
			out = append(out, encodeU32(locals[u.X])...)
			out = append(out, opcodeF64Neg)
		default:
			return nil, fmt.Errorf("wasm: unsupported unary -")
		}
	case tokens.NOT_TOKEN:
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[u.X])...)
		out = append(out, opcodeI32Eqz)
	default:
		return nil, fmt.Errorf("wasm: unsupported unary op %s", u.Op)
	}
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[u.Result])...)
	return out, nil
}

func (g *Generator) emitCast(info *funcInfo, c *mir.Cast, locals map[mir.ValueID]uint32) ([]byte, error) {
	if c == nil {
		return nil, nil
	}
	if info == nil {
		return nil, fmt.Errorf("wasm: missing cast context")
	}
	fromType, ok := info.valueType[c.X]
	if !ok {
		return nil, fmt.Errorf("wasm: missing cast source type")
	}
	fromVal, err := wasmValueType(fromType)
	if err != nil {
		return nil, err
	}
	toVal, err := wasmValueType(c.Type)
	if err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[c.X])...)
	op, ok := castOpcode(fromVal, toVal, isUnsignedType(fromType))
	if !ok {
		return nil, fmt.Errorf("wasm: unsupported cast")
	}
	out = append(out, op...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[c.Result])...)
	return out, nil
}

func (g *Generator) emitAlloca(a *mir.Alloca, locals map[mir.ValueID]uint32) ([]byte, error) {
	if a == nil {
		return nil, nil
	}
	size := g.layout.SizeOf(a.Type)
	if size <= 0 {
		return nil, fmt.Errorf("wasm: invalid alloca size")
	}
	var out []byte
	out = append(out, opcodeI64Const)
	out = append(out, encodeS64(int64(size))...)
	callIndex, ok := g.importIDs["ferret_alloc"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_alloc")
	}
	out = append(out, opcodeCall)
	out = append(out, encodeU32(callIndex)...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[a.Result])...)
	return out, nil
}

func (g *Generator) emitLoad(l *mir.Load, locals map[mir.ValueID]uint32) ([]byte, error) {
	if l == nil {
		return nil, nil
	}
	if resType, ok := g.resultType(l.Type); ok {
		size := g.layout.SizeOf(resType)
		if size <= 0 {
			return nil, fmt.Errorf("wasm: invalid result load size")
		}
		allocID, ok := g.importIDs["ferret_alloc"]
		if !ok {
			return nil, fmt.Errorf("wasm: missing import ferret_alloc")
		}
		var out []byte
		out = append(out, opcodeI64Const)
		out = append(out, encodeS64(int64(size))...)
		out = append(out, opcodeCall)
		out = append(out, encodeU32(allocID)...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[l.Result])...)
		copyBytes, err := g.emitMemcpy(locals[l.Result], locals[l.Addr], size)
		if err != nil {
			return nil, err
		}
		out = append(out, copyBytes...)
		return out, nil
	}
	op, err := loadOpcode(l.Type)
	if err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[l.Addr])...)
	out = append(out, op...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[l.Result])...)
	return out, nil
}

func (g *Generator) emitStore(info *funcInfo, s *mir.Store, locals map[mir.ValueID]uint32) ([]byte, error) {
	if s == nil {
		return nil, nil
	}
	if info == nil {
		return nil, fmt.Errorf("wasm: missing store context")
	}
	valType, ok := info.valueType[s.Value]
	if !ok {
		return nil, fmt.Errorf("wasm: missing store type")
	}
	return g.emitStoreValueToAddr(info, s.Value, valType, locals[s.Addr], locals)
}

func (g *Generator) emitStoreValueToAddr(_ *funcInfo, valID mir.ValueID, valType types.SemType, addrLocal uint32, locals map[mir.ValueID]uint32) ([]byte, error) {
	if valType == nil {
		return nil, fmt.Errorf("wasm: missing value type")
	}
	if resType, ok := g.resultType(valType); ok {
		size := g.layout.SizeOf(resType)
		if size <= 0 {
			return nil, fmt.Errorf("wasm: invalid result store size")
		}
		return g.emitMemcpy(addrLocal, locals[valID], size)
	}
	if g.needsByRefType(valType) {
		size := g.layout.SizeOf(valType)
		if size <= 0 {
			return nil, fmt.Errorf("wasm: invalid byref store size")
		}
		return g.emitMemcpy(addrLocal, locals[valID], size)
	}
	op, err := storeOpcode(valType)
	if err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(addrLocal)...)
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[valID])...)
	out = append(out, op...)
	return out, nil
}

func (g *Generator) emitValueAddr(valID mir.ValueID, valType types.SemType, locals map[mir.ValueID]uint32, scratchLocal uint32) ([]byte, error) {
	if valType == nil {
		return nil, fmt.Errorf("wasm: missing value type")
	}
	if g.isAddressValueType(valType) {
		out := []byte{opcodeLocalGet}
		out = append(out, encodeU32(locals[valID])...)
		return out, nil
	}
	size := g.layout.SizeOf(valType)
	if size <= 0 {
		return nil, fmt.Errorf("wasm: invalid value address size")
	}
	allocID, ok := g.importIDs["ferret_alloc"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_alloc")
	}
	var out []byte
	out = append(out, opcodeI64Const)
	out = append(out, encodeS64(int64(size))...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(allocID)...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(scratchLocal)...)
	storeBytes, err := g.emitStoreValueToAddr(nil, valID, valType, scratchLocal, locals)
	if err != nil {
		return nil, err
	}
	out = append(out, storeBytes...)
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(scratchLocal)...)
	return out, nil
}

func (g *Generator) emitMemcpy(dstLocal, srcLocal uint32, size int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("wasm: invalid memcpy size")
	}
	memcpyID, ok := g.importIDs["ferret_memcpy"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_memcpy")
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(dstLocal)...)
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(srcLocal)...)
	out = append(out, opcodeI64Const)
	out = append(out, encodeS64(int64(size))...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(memcpyID)...)
	return out, nil
}

func (g *Generator) emitArrayGet(info *funcInfo, a *mir.ArrayGet, locals map[mir.ValueID]uint32) ([]byte, error) {
	if a == nil || info == nil {
		return nil, nil
	}
	arrType, ok := g.arrayTypeOf(info.valueType[a.Array])
	if !ok || arrType == nil {
		g.reportUnsupported("array_get array", a.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	elemType := a.Type
	elemSize := g.layout.SizeOf(elemType)
	if elemSize <= 0 {
		return nil, fmt.Errorf("wasm: invalid array element size")
	}
	if arrType.Length >= 0 {
		var out []byte
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[a.Array])...)
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[a.Index])...)
		if elemSize != 1 {
			out = append(out, opcodeI32Const)
			out = append(out, encodeS32(int32(elemSize))...)
			out = append(out, opcodeI32Mul)
		}
		out = append(out, opcodeI32Add)
		if g.isAddressValueType(elemType) {
			out = append(out, opcodeLocalSet)
			out = append(out, encodeU32(locals[a.Result])...)
			return out, nil
		}
		op, err := loadOpcode(elemType)
		if err != nil {
			return nil, err
		}
		out = append(out, op...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[a.Result])...)
		return out, nil
	}

	callID, ok := g.importIDs["ferret_array_get"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_array_get")
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[a.Array])...)
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[a.Index])...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(callID)...)
	if g.isAddressValueType(elemType) {
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[a.Result])...)
		return out, nil
	}
	op, err := loadOpcode(elemType)
	if err != nil {
		return nil, err
	}
	out = append(out, op...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[a.Result])...)
	return out, nil
}

func (g *Generator) emitArraySet(info *funcInfo, a *mir.ArraySet, locals map[mir.ValueID]uint32, scratchLocal uint32) ([]byte, error) {
	if a == nil || info == nil {
		return nil, nil
	}
	arrType, ok := g.arrayTypeOf(info.valueType[a.Array])
	if !ok || arrType == nil {
		g.reportUnsupported("array_set array", a.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	elemType := arrType.Element
	elemSize := g.layout.SizeOf(elemType)
	if elemSize <= 0 {
		return nil, fmt.Errorf("wasm: invalid array element size")
	}

	if arrType.Length >= 0 {
		var out []byte
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[a.Array])...)
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[a.Index])...)
		if elemSize != 1 {
			out = append(out, opcodeI32Const)
			out = append(out, encodeS32(int32(elemSize))...)
			out = append(out, opcodeI32Mul)
		}
		out = append(out, opcodeI32Add)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(scratchLocal)...)
		storeBytes, err := g.emitStoreValueToAddr(info, a.Value, elemType, scratchLocal, locals)
		if err != nil {
			return nil, err
		}
		out = append(out, storeBytes...)
		return out, nil
	}

	callID, ok := g.importIDs["ferret_array_set"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_array_set")
	}
	valueAddr, err := g.emitValueAddr(a.Value, elemType, locals, scratchLocal)
	if err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[a.Array])...)
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[a.Index])...)
	out = append(out, valueAddr...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(callID)...)
	out = append(out, opcodeDrop)
	return out, nil
}

func (g *Generator) emitMapGet(info *funcInfo, m *mir.MapGet, locals map[mir.ValueID]uint32, scratchLocal uint32) ([]byte, error) {
	if m == nil || info == nil {
		return nil, nil
	}
	mapType, ok := g.mapTypeOf(info.valueType[m.Map])
	if !ok || mapType == nil {
		g.reportUnsupported("map_get map", m.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	keyAddr, err := g.emitValueAddr(m.Key, mapType.Key, locals, scratchLocal)
	if err != nil {
		return nil, err
	}

	if optType, ok := g.optionalType(m.Type); ok && optType != nil {
		payloadSize := g.layout.OptionalPayloadSize(optType)
		if payloadSize <= 0 {
			return nil, fmt.Errorf("wasm: invalid map_get optional payload size")
		}
		allocID, ok := g.importIDs["ferret_alloc"]
		if !ok {
			return nil, fmt.Errorf("wasm: missing import ferret_alloc")
		}
		getOptID, ok := g.importIDs["ferret_map_get_optional_out"]
		if !ok {
			return nil, fmt.Errorf("wasm: missing import ferret_map_get_optional_out")
		}

		var out []byte
		out = append(out, opcodeI64Const)
		out = append(out, encodeS64(int64(payloadSize))...)
		out = append(out, opcodeCall)
		out = append(out, encodeU32(allocID)...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[m.Result])...)

		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[m.Map])...)
		out = append(out, keyAddr...)
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[m.Result])...)
		out = append(out, opcodeCall)
		out = append(out, encodeU32(getOptID)...)
		return out, nil
	}

	getID, ok := g.importIDs["ferret_map_get"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_map_get")
	}
	panicID, ok := g.importIDs["ferret_global_panic"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_global_panic")
	}

	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[m.Map])...)
	out = append(out, keyAddr...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(getID)...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(scratchLocal)...)

	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(scratchLocal)...)
	out = append(out, opcodeI32Eqz)
	out = append(out, opcodeIf, blockTypeVoid)
	msgPtr := g.stringPtr("key not found in map")
	out = append(out, opcodeI32Const)
	out = append(out, encodeS32(msgPtr)...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(panicID)...)
	out = append(out, opcodeUnreachable)
	out = append(out, opcodeEnd)

	if g.isAddressValueType(m.Type) {
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(scratchLocal)...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[m.Result])...)
		return out, nil
	}

	loadOp, err := loadOpcode(m.Type)
	if err != nil {
		return nil, err
	}
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(scratchLocal)...)
	out = append(out, loadOp...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[m.Result])...)
	return out, nil
}

func (g *Generator) emitMapSet(info *funcInfo, m *mir.MapSet, locals map[mir.ValueID]uint32, scratchLocal uint32) ([]byte, error) {
	if m == nil || info == nil {
		return nil, nil
	}
	mapType, ok := g.mapTypeOf(info.valueType[m.Map])
	if !ok || mapType == nil {
		g.reportUnsupported("map_set map", m.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	keyAddr, err := g.emitValueAddr(m.Key, mapType.Key, locals, scratchLocal)
	if err != nil {
		return nil, err
	}
	valueAddr, err := g.emitValueAddr(m.Value, mapType.Value, locals, scratchLocal)
	if err != nil {
		return nil, err
	}
	setID, ok := g.importIDs["ferret_map_set"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_map_set")
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[m.Map])...)
	out = append(out, keyAddr...)
	out = append(out, valueAddr...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(setID)...)
	return out, nil
}

func (g *Generator) emitOptionalNone(o *mir.OptionalNone, locals map[mir.ValueID]uint32) ([]byte, error) {
	if o == nil {
		return nil, nil
	}
	optType, ok := g.optionalType(o.Type)
	if !ok || optType == nil {
		g.reportUnsupported("optional_none", o.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	payloadSize := g.layout.OptionalPayloadSize(optType)
	if payloadSize <= 0 {
		return nil, fmt.Errorf("wasm: invalid optional payload size")
	}
	valSize := g.layout.SizeOf(optType.Inner)
	if valSize < 0 {
		return nil, fmt.Errorf("wasm: invalid optional inner size")
	}
	allocID, ok := g.importIDs["ferret_alloc"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_alloc")
	}

	var out []byte
	out = append(out, opcodeI64Const)
	out = append(out, encodeS64(int64(payloadSize))...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(allocID)...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[o.Result])...)

	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[o.Result])...)
	if valSize != 0 {
		out = append(out, opcodeI32Const)
		out = append(out, encodeS32(int32(valSize))...)
		out = append(out, opcodeI32Add)
	}
	out = append(out, opcodeI32Const)
	out = append(out, encodeS32(0)...)
	storeOp, err := storeOpcode(types.TypeBool)
	if err != nil {
		return nil, err
	}
	out = append(out, storeOp...)
	return out, nil
}

func (g *Generator) emitOptionalSome(o *mir.OptionalSome, locals map[mir.ValueID]uint32) ([]byte, error) {
	if o == nil {
		return nil, nil
	}
	optType, ok := g.optionalType(o.Type)
	if !ok || optType == nil {
		g.reportUnsupported("optional_some", o.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	payloadSize := g.layout.OptionalPayloadSize(optType)
	if payloadSize <= 0 {
		return nil, fmt.Errorf("wasm: invalid optional payload size")
	}
	valSize := g.layout.SizeOf(optType.Inner)
	if valSize < 0 {
		return nil, fmt.Errorf("wasm: invalid optional inner size")
	}
	allocID, ok := g.importIDs["ferret_alloc"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_alloc")
	}

	var out []byte
	out = append(out, opcodeI64Const)
	out = append(out, encodeS64(int64(payloadSize))...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(allocID)...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[o.Result])...)

	storeBytes, err := g.emitStoreValueToAddr(nil, o.Value, optType.Inner, locals[o.Result], locals)
	if err != nil {
		return nil, err
	}
	out = append(out, storeBytes...)

	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[o.Result])...)
	if valSize != 0 {
		out = append(out, opcodeI32Const)
		out = append(out, encodeS32(int32(valSize))...)
		out = append(out, opcodeI32Add)
	}
	out = append(out, opcodeI32Const)
	out = append(out, encodeS32(1)...)
	storeOp, err := storeOpcode(types.TypeBool)
	if err != nil {
		return nil, err
	}
	out = append(out, storeOp...)
	return out, nil
}

func (g *Generator) emitOptionalIsSome(info *funcInfo, o *mir.OptionalIsSome, locals map[mir.ValueID]uint32) ([]byte, error) {
	if o == nil || info == nil {
		return nil, nil
	}
	optType, ok := g.optionalType(info.valueType[o.Value])
	if !ok || optType == nil {
		g.reportUnsupported("optional_is_some", o.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	valSize := g.layout.SizeOf(optType.Inner)
	if valSize < 0 {
		return nil, fmt.Errorf("wasm: invalid optional inner size")
	}

	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[o.Value])...)
	if valSize != 0 {
		out = append(out, opcodeI32Const)
		out = append(out, encodeS32(int32(valSize))...)
		out = append(out, opcodeI32Add)
	}
	loadOp, err := loadOpcode(types.TypeBool)
	if err != nil {
		return nil, err
	}
	out = append(out, loadOp...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[o.Result])...)
	return out, nil
}

func (g *Generator) emitOptionalUnwrap(info *funcInfo, o *mir.OptionalUnwrap, locals map[mir.ValueID]uint32, scratchLocal uint32) ([]byte, error) {
	if o == nil || info == nil {
		return nil, nil
	}
	optType, ok := g.optionalType(info.valueType[o.Value])
	if !ok || optType == nil {
		g.reportUnsupported("optional_unwrap", o.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	inner := optType.Inner
	valSize := g.layout.SizeOf(inner)
	if valSize < 0 {
		return nil, fmt.Errorf("wasm: invalid optional inner size")
	}

	byRef := g.needsByRefType(inner)
	if !byRef {
		if _, ok := g.optionalType(inner); ok {
			byRef = true
		} else if _, ok := g.resultType(inner); ok {
			byRef = true
		} else if _, ok := types.UnwrapType(inner).(*types.UnionType); ok {
			byRef = true
		}
	}

	if o.HasDefault {
		unwrapID, ok := g.importIDs["ferret_optional_unwrap_or"]
		if !ok {
			return nil, fmt.Errorf("wasm: missing import ferret_optional_unwrap_or")
		}
		allocID, ok := g.importIDs["ferret_alloc"]
		if !ok {
			return nil, fmt.Errorf("wasm: missing import ferret_alloc")
		}
		if valSize <= 0 {
			return nil, fmt.Errorf("wasm: invalid optional unwrap size")
		}

		var out []byte
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[o.Value])...)
		defaultAddr, err := g.emitValueAddr(o.Default, inner, locals, scratchLocal)
		if err != nil {
			return nil, err
		}
		out = append(out, defaultAddr...)

		if byRef {
			out = append(out, opcodeI64Const)
			out = append(out, encodeS64(int64(valSize))...)
			out = append(out, opcodeCall)
			out = append(out, encodeU32(allocID)...)
			out = append(out, opcodeLocalSet)
			out = append(out, encodeU32(locals[o.Result])...)
			out = append(out, opcodeLocalGet)
			out = append(out, encodeU32(locals[o.Result])...)
		} else {
			out = append(out, opcodeI64Const)
			out = append(out, encodeS64(int64(valSize))...)
			out = append(out, opcodeCall)
			out = append(out, encodeU32(allocID)...)
			out = append(out, opcodeLocalSet)
			out = append(out, encodeU32(scratchLocal)...)
			out = append(out, opcodeLocalGet)
			out = append(out, encodeU32(scratchLocal)...)
		}

		out = append(out, opcodeI64Const)
		out = append(out, encodeS64(int64(valSize))...)
		out = append(out, opcodeCall)
		out = append(out, encodeU32(unwrapID)...)

		if !byRef {
			loadOp, err := loadOpcode(inner)
			if err != nil {
				return nil, err
			}
			out = append(out, opcodeLocalGet)
			out = append(out, encodeU32(scratchLocal)...)
			out = append(out, loadOp...)
			out = append(out, opcodeLocalSet)
			out = append(out, encodeU32(locals[o.Result])...)
		}
		return out, nil
	}

	if byRef {
		allocID, ok := g.importIDs["ferret_alloc"]
		if !ok {
			return nil, fmt.Errorf("wasm: missing import ferret_alloc")
		}
		if valSize <= 0 {
			return nil, fmt.Errorf("wasm: invalid optional unwrap size")
		}
		var out []byte
		out = append(out, opcodeI64Const)
		out = append(out, encodeS64(int64(valSize))...)
		out = append(out, opcodeCall)
		out = append(out, encodeU32(allocID)...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[o.Result])...)
		copyBytes, err := g.emitMemcpy(locals[o.Result], locals[o.Value], valSize)
		if err != nil {
			return nil, err
		}
		out = append(out, copyBytes...)
		return out, nil
	}

	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[o.Value])...)
	loadOp, err := loadOpcode(inner)
	if err != nil {
		return nil, err
	}
	out = append(out, loadOp...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[o.Result])...)
	return out, nil
}

func (g *Generator) emitUnionVariantCheck(u *mir.UnionVariantCheck, locals map[mir.ValueID]uint32) ([]byte, error) {
	if u == nil {
		return nil, nil
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[u.Value])...)
	loadOp, err := loadOpcode(types.TypeI32)
	if err != nil {
		return nil, err
	}
	out = append(out, loadOp...)
	out = append(out, opcodeI32Const)
	out = append(out, encodeS32(int32(u.VariantIndex))...)
	out = append(out, opcodeI32Eq)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[u.Result])...)
	return out, nil
}

func (g *Generator) emitUnionExtract(u *mir.UnionExtract, locals map[mir.ValueID]uint32, scratchLocal uint32) ([]byte, error) {
	if u == nil {
		return nil, nil
	}
	variantType := u.Type
	if variantType == nil {
		g.reportUnsupported("union_extract type", u.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	variantSize := g.layout.SizeOf(variantType)
	if variantSize < 0 {
		return nil, fmt.Errorf("wasm: invalid union variant size")
	}

	byRef := g.needsByRefType(variantType)
	if !byRef {
		if _, ok := g.optionalType(variantType); ok {
			byRef = true
		} else if _, ok := g.resultType(variantType); ok {
			byRef = true
		} else if _, ok := types.UnwrapType(variantType).(*types.UnionType); ok {
			byRef = true
		}
	}

	if byRef {
		if variantSize <= 0 {
			return nil, fmt.Errorf("wasm: invalid union extract size")
		}
		allocID, ok := g.importIDs["ferret_alloc"]
		if !ok {
			return nil, fmt.Errorf("wasm: missing import ferret_alloc")
		}
		var out []byte
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[u.Value])...)
		out = append(out, opcodeI32Const)
		out = append(out, encodeS32(4)...)
		out = append(out, opcodeI32Add)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(scratchLocal)...)

		out = append(out, opcodeI64Const)
		out = append(out, encodeS64(int64(variantSize))...)
		out = append(out, opcodeCall)
		out = append(out, encodeU32(allocID)...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[u.Result])...)

		copyBytes, err := g.emitMemcpy(locals[u.Result], scratchLocal, variantSize)
		if err != nil {
			return nil, err
		}
		out = append(out, copyBytes...)
		return out, nil
	}

	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[u.Value])...)
	out = append(out, opcodeI32Const)
	out = append(out, encodeS32(4)...)
	out = append(out, opcodeI32Add)
	loadOp, err := loadOpcode(variantType)
	if err != nil {
		return nil, err
	}
	out = append(out, loadOp...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[u.Result])...)
	return out, nil
}

func (g *Generator) emitResultOk(r *mir.ResultOk, locals map[mir.ValueID]uint32) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	resType, ok := g.resultType(r.Type)
	if !ok || resType == nil {
		g.reportUnsupported("result_ok", r.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	resSize := g.layout.SizeOf(resType)
	if resSize <= 0 {
		return nil, fmt.Errorf("wasm: invalid result size")
	}
	tagOffset, ok := g.resultTagOffset(resType, r.Loc())
	if !ok {
		return []byte{opcodeUnreachable}, nil
	}
	allocID, ok := g.importIDs["ferret_alloc"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_alloc")
	}
	var out []byte
	out = append(out, opcodeI64Const)
	out = append(out, encodeS64(int64(resSize))...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(allocID)...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[r.Result])...)

	storeBytes, err := g.emitStoreValueToAddr(nil, r.Value, resType.Ok, locals[r.Result], locals)
	if err != nil {
		return nil, err
	}
	out = append(out, storeBytes...)

	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[r.Result])...)
	if tagOffset != 0 {
		out = append(out, opcodeI32Const)
		out = append(out, encodeS32(int32(tagOffset))...)
		out = append(out, opcodeI32Add)
	}
	out = append(out, opcodeI32Const)
	out = append(out, encodeS32(1)...)
	storeOp, err := storeOpcode(types.TypeBool)
	if err != nil {
		return nil, err
	}
	out = append(out, storeOp...)
	return out, nil
}

func (g *Generator) emitResultErr(r *mir.ResultErr, locals map[mir.ValueID]uint32) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	resType, ok := g.resultType(r.Type)
	if !ok || resType == nil {
		g.reportUnsupported("result_err", r.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	resSize := g.layout.SizeOf(resType)
	if resSize <= 0 {
		return nil, fmt.Errorf("wasm: invalid result size")
	}
	tagOffset, ok := g.resultTagOffset(resType, r.Loc())
	if !ok {
		return []byte{opcodeUnreachable}, nil
	}
	allocID, ok := g.importIDs["ferret_alloc"]
	if !ok {
		return nil, fmt.Errorf("wasm: missing import ferret_alloc")
	}
	var out []byte
	out = append(out, opcodeI64Const)
	out = append(out, encodeS64(int64(resSize))...)
	out = append(out, opcodeCall)
	out = append(out, encodeU32(allocID)...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[r.Result])...)

	storeBytes, err := g.emitStoreValueToAddr(nil, r.Value, resType.Err, locals[r.Result], locals)
	if err != nil {
		return nil, err
	}
	out = append(out, storeBytes...)

	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[r.Result])...)
	if tagOffset != 0 {
		out = append(out, opcodeI32Const)
		out = append(out, encodeS32(int32(tagOffset))...)
		out = append(out, opcodeI32Add)
	}
	out = append(out, opcodeI32Const)
	out = append(out, encodeS32(0)...)
	storeOp, err := storeOpcode(types.TypeBool)
	if err != nil {
		return nil, err
	}
	out = append(out, storeOp...)
	return out, nil
}

func (g *Generator) emitResultIsOk(info *funcInfo, r *mir.ResultIsOk, locals map[mir.ValueID]uint32) ([]byte, error) {
	if r == nil || info == nil {
		return nil, nil
	}
	resType, ok := g.resultType(info.valueType[r.Value])
	if !ok || resType == nil {
		g.reportUnsupported("result_is_ok", r.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	tagOffset, ok := g.resultTagOffset(resType, r.Loc())
	if !ok {
		return []byte{opcodeUnreachable}, nil
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[r.Value])...)
	if tagOffset != 0 {
		out = append(out, opcodeI32Const)
		out = append(out, encodeS32(int32(tagOffset))...)
		out = append(out, opcodeI32Add)
	}
	loadOp, err := loadOpcode(types.TypeBool)
	if err != nil {
		return nil, err
	}
	out = append(out, loadOp...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[r.Result])...)
	return out, nil
}

func (g *Generator) emitResultUnwrap(info *funcInfo, r *mir.ResultUnwrap, locals map[mir.ValueID]uint32) ([]byte, error) {
	if r == nil || info == nil {
		return nil, nil
	}
	if r.HasDefault {
		g.reportUnsupported("result_unwrap default", r.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	inner := r.Type
	if inner == nil {
		g.reportUnsupported("result_unwrap type", r.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	valSize := g.layout.SizeOf(inner)
	if valSize < 0 {
		return nil, fmt.Errorf("wasm: invalid result unwrap size")
	}

	byRef := g.needsByRefType(inner)
	if !byRef {
		if _, ok := g.optionalType(inner); ok {
			byRef = true
		} else if _, ok := g.resultType(inner); ok {
			byRef = true
		}
	}

	if byRef {
		allocID, ok := g.importIDs["ferret_alloc"]
		if !ok {
			return nil, fmt.Errorf("wasm: missing import ferret_alloc")
		}
		if valSize <= 0 {
			return nil, fmt.Errorf("wasm: invalid result unwrap size")
		}
		var out []byte
		out = append(out, opcodeI64Const)
		out = append(out, encodeS64(int64(valSize))...)
		out = append(out, opcodeCall)
		out = append(out, encodeU32(allocID)...)
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[r.Result])...)
		copyBytes, err := g.emitMemcpy(locals[r.Result], locals[r.Value], valSize)
		if err != nil {
			return nil, err
		}
		out = append(out, copyBytes...)
		return out, nil
	}

	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[r.Value])...)
	loadOp, err := loadOpcode(inner)
	if err != nil {
		return nil, err
	}
	out = append(out, loadOp...)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[r.Result])...)
	return out, nil
}

func (g *Generator) emitPtrAdd(p *mir.PtrAdd, locals map[mir.ValueID]uint32) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[p.Base])...)
	out = append(out, opcodeI32Const)
	out = append(out, encodeS32(int32(p.Offset))...)
	out = append(out, opcodeI32Add)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[p.Result])...)
	return out, nil
}

func (g *Generator) emitPtrOffset(p *mir.PtrOffset, locals map[mir.ValueID]uint32) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	var out []byte
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[p.Base])...)
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[p.Offset])...)
	out = append(out, opcodeI32Add)
	out = append(out, opcodeLocalSet)
	out = append(out, encodeU32(locals[p.Result])...)
	return out, nil
}

func (g *Generator) emitCall(info *funcInfo, c *mir.Call, locals map[mir.ValueID]uint32, fnIndex map[string]uint32) ([]byte, error) {
	if c == nil || info == nil {
		return nil, nil
	}
	target, err := g.resolveCallTarget(info.mod, c.Target)
	if err != nil {
		g.reportError(err.Error(), c.Loc())
		return []byte{opcodeUnreachable}, nil
	}
	var out []byte
	for _, arg := range c.Args {
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[arg])...)
	}
	if idx, ok := fnIndex[target]; ok {
		out = append(out, opcodeCall)
		out = append(out, encodeU32(idx)...)
	} else if idx, ok := g.importIDs[target]; ok {
		out = append(out, opcodeCall)
		out = append(out, encodeU32(idx)...)
	} else {
		return nil, fmt.Errorf("wasm: missing call target %s", target)
	}

	if c.Result != mir.InvalidValue && !isVoidType(c.Type) {
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[c.Result])...)
	}
	return out, nil
}

func (g *Generator) emitCallIndirect(info *funcInfo, c *mir.CallIndirect, locals map[mir.ValueID]uint32, fnIndex map[string]uint32) ([]byte, error) {
	if c == nil || info == nil {
		return nil, nil
	}

	// Build function signature for type index lookup
	sig, err := g.callIndirectSignature(info, c)
	if err != nil {
		return nil, err
	}

	// Look up the type index for this signature
	sigStr := sigToString(sig)
	typeIndex, ok := g.typeIndices[sigStr]
	if !ok {
		return nil, fmt.Errorf("wasm: missing type index for call_indirect signature %s", sigStr)
	}

	var out []byte

	// Push arguments onto stack first
	for _, arg := range c.Args {
		out = append(out, opcodeLocalGet)
		out = append(out, encodeU32(locals[arg])...)
	}

	// Load function table index from closure env
	// The callee is a pointer to the closure env
	// The first field (__fn) at offset 0 contains the table index (stored as u64)
	out = append(out, opcodeLocalGet)
	out = append(out, encodeU32(locals[c.Callee])...)
	// Load the function pointer (stored as i64)
	out = append(out, opcodeI64Load)
	out = append(out, 0x00, 0x00) // alignment=0, offset=0
	// Convert i64 to i32 for table index
	out = append(out, opcodeI32WrapI64)

	// Emit call_indirect with proper type index and table index 0
	out = append(out, opcodeCallIndirect)
	out = append(out, encodeU32(typeIndex)...)
	out = append(out, 0x00) // table index (always 0 for single table)

	if c.Result != mir.InvalidValue && !isVoidType(c.Type) {
		out = append(out, opcodeLocalSet)
		out = append(out, encodeU32(locals[c.Result])...)
	}
	return out, nil
}

func (g *Generator) callIndirectSignature(info *funcInfo, call *mir.CallIndirect) (funcSig, error) {
	params := make([]ValType, 0, len(call.Args))
	for _, arg := range call.Args {
		typ, ok := info.valueType[arg]
		if !ok {
			return funcSig{}, fmt.Errorf("wasm: missing arg type for call_indirect")
		}
		v, err := wasmValueType(typ)
		if err != nil {
			return funcSig{}, err
		}
		params = append(params, v)
	}
	results := []ValType{}
	if !isVoidType(call.Type) {
		ret, err := wasmValueType(call.Type)
		if err != nil {
			return funcSig{}, err
		}
		results = append(results, ret)
	}
	return funcSig{params: params, results: results}, nil
}

func (g *Generator) callSignature(info *funcInfo, call *mir.Call) (funcSig, error) {
	params := make([]ValType, 0, len(call.Args))
	for _, arg := range call.Args {
		typ, ok := info.valueType[arg]
		if !ok {
			return funcSig{}, fmt.Errorf("wasm: missing arg type for %s", call.Target)
		}
		v, err := wasmValueType(typ)
		if err != nil {
			return funcSig{}, err
		}
		params = append(params, v)
	}
	results := []ValType{}
	if !isVoidType(call.Type) {
		ret, err := wasmValueType(call.Type)
		if err != nil {
			return funcSig{}, err
		}
		results = append(results, ret)
	}
	return funcSig{params: params, results: results}, nil
}

func (g *Generator) funcSignature(fn *mir.Function) (funcSig, error) {
	if fn == nil {
		return funcSig{}, fmt.Errorf("wasm: missing function")
	}
	params := make([]ValType, 0, len(fn.Params))
	for _, p := range fn.Params {
		v, err := wasmValueType(p.Type)
		if err != nil {
			return funcSig{}, err
		}
		params = append(params, v)
	}
	results := []ValType{}
	if !isVoidType(fn.Return) {
		ret, err := wasmValueType(fn.Return)
		if err != nil {
			return funcSig{}, err
		}
		results = append(results, ret)
	}
	return funcSig{params: params, results: results}, nil
}

func (g *Generator) funcName(name, importPath string) string {
	base := sanitizeName(name)
	if name != "main" && importPath != "" && importPath != g.entryModule {
		prefix := sanitizeName(ustrings.ToIdentifier(importPath))
		return prefix + "_" + base
	}
	return base
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "::", "_")
	name = strings.ReplaceAll(name, "/", "_")
	return name
}

func (g *Generator) resolveCallTarget(mod *context_v2.Module, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("wasm: invalid call target")
	}
	if strings.HasPrefix(target, "ferret_") {
		return target, nil
	}
	if strings.Contains(target, "::") {
		parts := strings.Split(target, "::")
		moduleAlias := strings.Join(parts[:len(parts)-1], "::")
		funcName := parts[len(parts)-1]
		importPath := ""
		if mod != nil && mod.ImportAliasMap != nil {
			importPath = mod.ImportAliasMap[moduleAlias]
		}
		if importPath == "" {
			return "", fmt.Errorf("wasm: unknown module alias %q", moduleAlias)
		}
		if g.ctx != nil {
			if imported, ok := g.ctx.GetModule(importPath); ok && imported.ModuleScope != nil {
				if sym, ok := imported.ModuleScope.GetSymbol(funcName); ok && sym.IsNative && sym.NativeName != "" {
					return sym.NativeName, nil
				}
			}
		}
		return g.funcName(funcName, importPath), nil
	}
	if mod != nil && mod.ModuleScope != nil {
		if sym, ok := mod.ModuleScope.Lookup(target); ok && sym.IsNative && sym.NativeName != "" {
			return sym.NativeName, nil
		}
	}
	if mod != nil {
		return g.funcName(target, mod.ImportPath), nil
	}
	return target, nil
}

func (g *Generator) isEntryModule(mod *context_v2.Module) bool {
	return mod != nil && mod.ImportPath == g.entryModule
}

func (g *Generator) reportError(msg string, loc *source.Location) {
	if g.ctx == nil {
		return
	}
	g.ctx.ReportError(msg, loc)
}

func (g *Generator) reportUnsupported(what string, loc *source.Location) {
	g.reportError(fmt.Sprintf("wasm: unsupported %s", what), loc)
}

func (g *Generator) memoryMinPages() uint32 {
	size := g.dataBase + uint32(len(g.data))
	if size == 0 {
		return 1
	}
	const page = 65536
	pages := (size + page - 1) / page
	if pages == 0 {
		pages = 1
	}
	return pages
}

func (g *Generator) stringPtr(value string) int32 {
	if ptr, ok := g.stringPtrs[value]; ok {
		return int32(ptr)
	}
	offset := g.dataBase + uint32(len(g.data))
	g.stringPtrs[value] = offset
	g.data = append(g.data, []byte(value)...)
	g.data = append(g.data, 0)
	for len(g.data)%4 != 0 {
		g.data = append(g.data, 0)
	}
	return int32(offset)
}

func (g *Generator) addDataSymbol(name string, data []byte, align uint32) uint32 {
	if ptr, ok := g.dataSymbols[name]; ok {
		return ptr
	}
	if align > 0 {
		offset := g.dataBase + uint32(len(g.data))
		if rem := offset % align; rem != 0 {
			pad := align - rem
			g.data = append(g.data, make([]byte, pad)...)
		}
	}
	offset := g.dataBase + uint32(len(g.data))
	g.dataSymbols[name] = offset
	g.data = append(g.data, data...)
	return offset
}

func (g *Generator) emitTypeIDs() error {
	for _, unit := range g.units {
		if unit.MIR == nil || len(unit.MIR.TypeIDs) == 0 {
			continue
		}
		for globalName, typeID := range unit.MIR.TypeIDs {
			ptr := uint32(g.stringPtr(typeID))
			if existing, ok := g.dataSymbols[globalName]; ok && existing != ptr {
				return fmt.Errorf("wasm: conflicting type ID symbol %s", globalName)
			}
			g.dataSymbols[globalName] = ptr
		}
	}
	return nil
}

func (g *Generator) emitVTables() error {
	for _, unit := range g.units {
		if unit.Module == nil || unit.MIR == nil || len(unit.MIR.VTables) == 0 {
			continue
		}
		for _, table := range unit.MIR.VTables {
			if table.Name == "" || len(table.Methods) == 0 {
				continue
			}
			entries := make([]byte, 0, len(table.Methods)*4)
			for _, method := range table.Methods {
				fullName := g.funcName(method, unit.Module.ImportPath)
				tableIdx, ok := g.tableIndex[fullName]
				if !ok {
					for name, idx := range g.tableIndex {
						if name == method || strings.HasSuffix(name, "_"+method) {
							tableIdx = idx
							ok = true
							break
						}
					}
				}
				if !ok {
					return fmt.Errorf("wasm: missing table index for vtable method %s", method)
				}
				buf := make([]byte, 4)
				binary.LittleEndian.PutUint32(buf, tableIdx)
				entries = append(entries, buf...)
			}
			g.addDataSymbol(table.Name, entries, 4)
		}
	}
	return nil
}

func analyzeFunctionTypes(fn *mir.Function) map[mir.ValueID]types.SemType {
	typesMap := make(map[mir.ValueID]types.SemType)
	if fn == nil {
		return typesMap
	}
	for _, p := range fn.Params {
		typesMap[p.ID] = p.Type
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			switch v := instr.(type) {
			case *mir.Const:
				typesMap[v.Result] = v.Type
			case *mir.Binary:
				if isCompareToken(v.Op) {
					typesMap[v.Result] = types.TypeBool
				} else {
					typesMap[v.Result] = v.Type
				}
			case *mir.Unary:
				if v.Op == tokens.NOT_TOKEN {
					typesMap[v.Result] = types.TypeBool
				} else {
					typesMap[v.Result] = v.Type
				}
			case *mir.Cast:
				typesMap[v.Result] = v.Type
			case *mir.Alloca:
				typesMap[v.Result] = types.NewReference(v.Type)
			case *mir.Load:
				typesMap[v.Result] = v.Type
			case *mir.PtrAdd:
				typesMap[v.Result] = types.NewReference(v.Elem)
			case *mir.PtrOffset:
				typesMap[v.Result] = types.NewReference(v.Elem)
			case *mir.Call:
				typesMap[v.Result] = v.Type
			case *mir.CallIndirect:
				typesMap[v.Result] = v.Type
			case *mir.Phi:
				typesMap[v.Result] = v.Type
			case *mir.ArrayGet:
				typesMap[v.Result] = v.Type
			case *mir.MapGet:
				typesMap[v.Result] = v.Type
			case *mir.OptionalNone:
				typesMap[v.Result] = types.NewReference(v.Type)
			case *mir.OptionalSome:
				typesMap[v.Result] = types.NewReference(v.Type)
			case *mir.OptionalIsSome:
				typesMap[v.Result] = types.TypeBool
			case *mir.OptionalUnwrap:
				typesMap[v.Result] = v.Type
			case *mir.UnionVariantCheck:
				typesMap[v.Result] = types.TypeBool
			case *mir.UnionExtract:
				typesMap[v.Result] = v.Type
			case *mir.ResultOk:
				typesMap[v.Result] = types.NewReference(v.Type)
			case *mir.ResultErr:
				typesMap[v.Result] = types.NewReference(v.Type)
			case *mir.ResultIsOk:
				typesMap[v.Result] = types.TypeBool
			case *mir.ResultUnwrap:
				typesMap[v.Result] = v.Type
			}
		}
	}
	return typesMap
}

type phiMove struct {
	dst mir.ValueID
	src mir.ValueID
}

func collectPhiMoves(blocks []*mir.Block) map[mir.BlockID]map[mir.BlockID][]phiMove {
	out := make(map[mir.BlockID]map[mir.BlockID][]phiMove)
	for _, block := range blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			phi, ok := instr.(*mir.Phi)
			if !ok {
				continue
			}
			for _, incoming := range phi.Incoming {
				if _, ok := out[incoming.Pred]; !ok {
					out[incoming.Pred] = make(map[mir.BlockID][]phiMove)
				}
				out[incoming.Pred][block.ID] = append(out[incoming.Pred][block.ID], phiMove{
					dst: phi.Result,
					src: incoming.Value,
				})
			}
		}
	}
	return out
}

func (g *Generator) optionalType(typ types.SemType) (*types.OptionalType, bool) {
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = types.UnwrapType(ref.Inner)
	}
	opt, ok := typ.(*types.OptionalType)
	return opt, ok
}

func (g *Generator) resultType(typ types.SemType) (*types.ResultType, bool) {
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = types.UnwrapType(ref.Inner)
	}
	res, ok := typ.(*types.ResultType)
	return res, ok
}

func (g *Generator) mapTypeOf(typ types.SemType) (*types.MapType, bool) {
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = types.UnwrapType(ref.Inner)
	}
	if named, ok := typ.(*types.NamedType); ok {
		typ = named.Underlying
	}
	mt, ok := typ.(*types.MapType)
	return mt, ok
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func alignToSize(size, align int) int {
	if align <= 1 {
		return size
	}
	rem := size % align
	if rem == 0 {
		return size
	}
	return size + (align - rem)
}

func (g *Generator) resultTagOffset(res *types.ResultType, loc *source.Location) (int, bool) {
	if res == nil {
		return 0, false
	}
	okSize := g.layout.SizeOf(res.Ok)
	errSize := g.layout.SizeOf(res.Err)
	if okSize < 0 || errSize < 0 {
		g.reportUnsupported("result size", loc)
		return 0, false
	}
	okAlign := g.layout.AlignOf(res.Ok)
	errAlign := g.layout.AlignOf(res.Err)
	unionAlign := maxInt(okAlign, errAlign)
	if unionAlign < 1 {
		unionAlign = 1
	}
	unionSize := alignToSize(maxInt(okSize, errSize), unionAlign)
	return unionSize, true
}

func (g *Generator) arrayTypeOf(typ types.SemType) (*types.ArrayType, bool) {
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = types.UnwrapType(ref.Inner)
	}
	arr, ok := typ.(*types.ArrayType)
	return arr, ok
}

func (g *Generator) isAddressValueType(typ types.SemType) bool {
	if g.needsByRefType(typ) {
		return true
	}
	if _, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		return true
	}
	if _, ok := g.resultType(typ); ok {
		return true
	}
	return false
}

func (g *Generator) needsByRefType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	if _, ok := typ.(*types.ReferenceType); ok {
		return false
	}
	if isLargePrimitiveType(typ) {
		return true
	}
	if _, ok := typ.(*types.StructType); ok {
		return true
	}
	if arr, ok := typ.(*types.ArrayType); ok && arr.Length >= 0 {
		return true
	}
	if _, ok := typ.(*types.InterfaceType); ok {
		return true
	}
	if named, ok := typ.(*types.NamedType); ok {
		if _, ok := named.Underlying.(*types.InterfaceType); ok {
			return true
		}
	}
	return false
}

func isLargePrimitiveType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	prim, ok := typ.(*types.PrimitiveType)
	if !ok {
		return false
	}
	switch prim.GetName() {
	case types.TYPE_I128, types.TYPE_U128, types.TYPE_I256, types.TYPE_U256,
		types.TYPE_F128, types.TYPE_F256:
		return true
	default:
		return false
	}
}

func wasmValueType(typ types.SemType) (ValType, error) {
	if typ == nil {
		return valTypeI32, nil
	}
	typ = types.UnwrapType(typ)
	switch t := typ.(type) {
	case *types.PrimitiveType:
		switch t.GetName() {
		case types.TYPE_I64, types.TYPE_U64:
			return valTypeI64, nil
		case types.TYPE_F32:
			return valTypeF32, nil
		case types.TYPE_F64:
			return valTypeF64, nil
		case types.TYPE_F128, types.TYPE_F256, types.TYPE_I128, types.TYPE_I256, types.TYPE_U128, types.TYPE_U256:
			return valTypeI32, nil
		default:
			return valTypeI32, nil
		}
	case *types.ReferenceType:
		return valTypeI32, nil
	case *types.ArrayType, *types.MapType, *types.StructType, *types.UnionType, *types.InterfaceType, *types.OptionalType, *types.ResultType:
		return valTypeI32, nil
	default:
		return valTypeI32, nil
	}
}

func isVoidType(typ types.SemType) bool {
	if typ == nil {
		return true
	}
	if prim, ok := types.UnwrapType(typ).(*types.PrimitiveType); ok {
		name := prim.GetName()
		return name == types.TYPE_VOID || name == types.TYPE_NONE
	}
	return false
}

func isUnsignedType(typ types.SemType) bool {
	if prim, ok := types.UnwrapType(typ).(*types.PrimitiveType); ok {
		switch prim.GetName() {
		case types.TYPE_U8, types.TYPE_U16, types.TYPE_U32, types.TYPE_U64, types.TYPE_U128, types.TYPE_U256:
			return true
		case types.TYPE_BOOL, types.TYPE_BYTE, types.TYPE_CHAR:
			return true
		}
	}
	return false
}

func parseI32Const(typ types.SemType, value string, stringPtr func(string) int32) (int32, error) {
	if prim, ok := types.UnwrapType(typ).(*types.PrimitiveType); ok {
		switch prim.GetName() {
		case types.TYPE_STRING:
			return stringPtr(value), nil
		case types.TYPE_CHAR:
			// Char literals are stored as the character itself, convert to Unicode code point
			runes := []rune(value)
			if len(runes) != 1 {
				return 0, fmt.Errorf("invalid char literal %q", value)
			}
			return int32(runes[0]), nil
		case types.TYPE_BYTE:
			// Byte literals are stored as the character itself, convert to byte value
			if len(value) != 1 {
				return 0, fmt.Errorf("invalid byte literal %q", value)
			}
			return int32(byte(value[0])), nil
		}
	}
	switch strings.ToLower(value) {
	case "true":
		return 1, nil
	case "false":
		return 0, nil
	}
	clean := stripNumeric(value)
	v, err := strconv.ParseInt(clean, 10, 32)
	if err != nil {
		u, uerr := strconv.ParseUint(clean, 10, 32)
		if uerr != nil {
			return 0, err
		}
		return int32(u), nil
	}
	return int32(v), nil
}

func parseI64Const(typ types.SemType, value string) (int64, error) {
	if prim, ok := types.UnwrapType(typ).(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
		return 0, fmt.Errorf("wasm: string const cannot be i64")
	}
	clean := stripNumeric(value)
	v, err := strconv.ParseInt(clean, 10, 64)
	if err != nil {
		u, uerr := strconv.ParseUint(clean, 10, 64)
		if uerr != nil {
			return 0, err
		}
		return int64(u), nil
	}
	return v, nil
}

func stripNumeric(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "_", "")
}

func sigEqual(a, b funcSig) bool {
	if len(a.params) != len(b.params) || len(a.results) != len(b.results) {
		return false
	}
	for i := range a.params {
		if a.params[i] != b.params[i] {
			return false
		}
	}
	for i := range a.results {
		if a.results[i] != b.results[i] {
			return false
		}
	}
	return true
}

func sigToString(sig funcSig) string {
	var parts []string
	parts = append(parts, "(")
	for i, p := range sig.params {
		if i > 0 {
			parts = append(parts, ",")
		}
		parts = append(parts, fmt.Sprintf("%d", p))
	}
	parts = append(parts, ")->(")
	for i, r := range sig.results {
		if i > 0 {
			parts = append(parts, ",")
		}
		parts = append(parts, fmt.Sprintf("%d", r))
	}
	parts = append(parts, ")")
	return strings.Join(parts, "")
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func isCompareToken(op tokens.TOKEN) bool {
	switch op {
	case tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN,
		tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN,
		tokens.DOUBLE_EQUAL_TOKEN, tokens.NOT_EQUAL_TOKEN:
		return true
	default:
		return false
	}
}
