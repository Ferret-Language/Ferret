// defines the `Generator` type and top‑level `Emit()` pipeline 
// that calls into the emit helpers. 
// This is the entry point for generating QBE IR.

package qbe

import (
	"fmt"
	"strings"

	"compiler/internal/context_v2"
	"compiler/internal/mir"
	"compiler/internal/types"
)

type Generator struct {
	ctx    *context_v2.CompilerContext
	mod    *context_v2.Module
	mirMod *mir.Module
	layout *mir.DataLayout

	data strings.Builder
	buf  strings.Builder

	valueTypes     map[mir.ValueID]types.SemType
	stringLits     map[string]string
	enumTables     map[string]string
	enumCounts     map[string]int
	tempID         int
	stringID       int
	labelID        int
	enumTableID    int
	retOutParam    string
	retOutType     types.SemType
	entryBlock     mir.BlockID
	currentReturn  types.SemType
	mainReturnsInt bool

	hoistedAllocas   []*mir.Alloca
	hoistedAllocaIDs map[mir.ValueID]struct{}
	emittedTypeDescs map[string]bool // Track which type descriptors have been emitted
}

func New(ctx *context_v2.CompilerContext, mod *context_v2.Module, mirMod *mir.Module) *Generator {
	pointerSize := 0
	if ctx != nil && ctx.Config != nil {
		pointerSize = ctx.Config.PointerSize
	}
	return &Generator{
		ctx:              ctx,
		mod:              mod,
		mirMod:           mirMod,
		layout:           mir.NewDataLayout(pointerSize),
		valueTypes:       make(map[mir.ValueID]types.SemType),
		stringLits:       make(map[string]string),
		enumTables:       make(map[string]string),
		enumCounts:       make(map[string]int),
		emittedTypeDescs: make(map[string]bool),
	}
}

func (g *Generator) Emit() (string, error) {
	if g.mirMod == nil {
		return "", fmt.Errorf("qbe: missing MIR module")
	}

	g.emitVTables()
	g.emitGlobals()
	g.emitTypeIDs()
	g.emitTypeDescriptors()
	for _, fn := range g.mirMod.Functions {
		g.emitFunction(fn)
	}

	if g.ctx != nil && g.ctx.HasErrors() {
		return "", fmt.Errorf("qbe: codegen failed")
	}

	var out strings.Builder
	if g.data.Len() > 0 {
		out.WriteString(g.data.String())
		out.WriteString("\n")
	}
	out.WriteString(g.buf.String())
	return out.String(), nil
}

func (g *Generator) emitVTables() {
	if g == nil || g.mirMod == nil || len(g.mirMod.VTables) == 0 {
		return
	}

	align := g.layout.PointerSize
	if align < 1 {
		align = 1
	}

	for _, table := range g.mirMod.VTables {
		if table.Name == "" || len(table.Methods) == 0 {
			continue
		}
		entries := make([]string, 0, len(table.Methods))
		for _, method := range table.Methods {
			name := g.qbeFuncName(method, g.moduleImportPath())
			entries = append(entries, fmt.Sprintf("l $%s", name))
		}
		g.data.WriteString(fmt.Sprintf("data $%s = align %d { %s }\n", table.Name, align, strings.Join(entries, ", ")))
	}
}

func (g *Generator) emitGlobals() {
	if g == nil || g.mirMod == nil || len(g.mirMod.Globals) == 0 {
		return
	}
	for _, glob := range g.mirMod.Globals {
		if glob.Name == "" || glob.Type == nil {
			continue
		}
		size := g.layout.SizeOf(glob.Type)
		if size <= 0 {
			size = 1
		}
		bytes := make([]string, 0, size)
		for i := 0; i < size; i++ {
			bytes = append(bytes, "b 0")
		}
		g.data.WriteString(fmt.Sprintf("export data $%s = { %s }\n", glob.Name, strings.Join(bytes, ", ")))
	}
}

func (g *Generator) emitTypeIDs() {
	if g == nil || g.mirMod == nil || len(g.mirMod.TypeIDs) == 0 {
		return
	}

	align := g.layout.AlignOf(types.TypeU8)
	if align < 1 {
		align = 1
	}

	// Emit each type ID as a null-terminated string constant
	for globalName, typeIDString := range g.mirMod.TypeIDs {
		// Emit as byte array with null terminator
		bytes := make([]string, 0, len(typeIDString)+1)
		for _, ch := range []byte(typeIDString) {
			bytes = append(bytes, fmt.Sprintf("b %d", ch))
		}
		bytes = append(bytes, "b 0") // null terminator
		g.data.WriteString(fmt.Sprintf("data $%s = align %d { %s }\n", globalName, align, strings.Join(bytes, ", ")))
	}
}

func (g *Generator) emitTypeDescriptors() {
	if g == nil || g.mirMod == nil || len(g.mirMod.TypeDescriptors) == 0 {
		return
	}

	// Emit ferret_type_info_t structures for each type used as a map key
	// Keep iterating until no new descriptors are added (handles nested types)
	for {
		anyNew := false
		for globalName, typ := range g.mirMod.TypeDescriptors {
			if !g.emittedTypeDescs[globalName] {
				g.emitTypeDescriptor(globalName, typ)
				g.emittedTypeDescs[globalName] = true
				anyNew = true
			}
		}
		if !anyNew {
			break
		}
	}
}

func (g *Generator) emitFunction(fn *mir.Function) {
	if fn == nil {
		return
	}

	g.valueTypes = make(map[mir.ValueID]types.SemType)
	g.tempID = 0
	g.retOutParam = ""
	g.retOutType = nil
	g.entryBlock = mir.InvalidBlock
	g.currentReturn = fn.Return
	g.mainReturnsInt = false
	g.collectAllocas(fn)
	if len(fn.Blocks) > 0 {
		g.entryBlock = fn.Blocks[0].ID
	}

	fnName := g.qbeFuncName(fn.Name, g.moduleImportPath())
	mainReturnsInt := false
	retType := ""
	if optType, ok := g.optionalType(fn.Return); ok {
		g.retOutType = optType
		g.retOutParam = "%ret"
	} else if resType, ok := g.resultType(fn.Return); ok {
		g.retOutType = resType
		g.retOutParam = "%ret"
	} else {
		retType, mainReturnsInt = g.qbeReturnType(fn.Name, fn.Return)
	}
	g.mainReturnsInt = mainReturnsInt

	g.buf.WriteString("export function")
	if retType != "" {
		g.buf.WriteString(" " + retType)
	}
	g.buf.WriteString(" $" + fnName + "(")

	startIdx := 0
	hasParams := len(fn.Params) > 0
	if hasParams && fn.Params[0].IsEnv {
		g.buf.WriteString("env " + g.valueName(fn.Params[0].ID))
		g.valueTypes[fn.Params[0].ID] = fn.Params[0].Type
		startIdx = 1
	}

	if g.retOutType != nil {
		if startIdx > 0 {
			g.buf.WriteString(", ")
		}
		g.buf.WriteString("l " + g.retOutParam)
	}

	for i := startIdx; i < len(fn.Params); i++ {
		param := fn.Params[i]
		paramSem := normalizeLargeValueType(param.Type)
		paramType, err := g.qbeType(paramSem)
		if err != nil {
			g.reportError(err.Error(), &param.Location)
			paramType = "w"
		}
		if i > startIdx || g.retOutType != nil || startIdx > 0 {
			g.buf.WriteString(", ")
		}
		g.buf.WriteString(paramType + " " + g.valueName(param.ID))
		g.valueTypes[param.ID] = paramSem
	}

	g.buf.WriteString(") {\n")

	for _, block := range fn.Blocks {
		g.emitBlock(block, mainReturnsInt)
	}

	g.buf.WriteString("}\n\n")
}

func (g *Generator) emitBlock(block *mir.Block, mainReturnsInt bool) {
	if block == nil {
		return
	}

	g.buf.WriteString(g.blockName(block.ID))
	g.buf.WriteString("\n")

	for _, instr := range block.Instrs {
		if _, ok := instr.(*mir.Phi); ok {
			g.emitInstr(instr)
		}
	}

	if block.ID == g.entryBlock && len(g.hoistedAllocas) > 0 {
		for _, alloc := range g.hoistedAllocas {
			g.emitAlloca(alloc)
		}
	}

	for _, instr := range block.Instrs {
		if _, ok := instr.(*mir.Phi); ok {
			continue
		}
		if alloc, ok := instr.(*mir.Alloca); ok {
			if _, hoisted := g.hoistedAllocaIDs[alloc.Result]; hoisted {
				continue
			}
		}
		g.emitInstr(instr)
	}

	g.emitTerm(block.Term, mainReturnsInt)
}

func (g *Generator) collectAllocas(fn *mir.Function) {
	g.hoistedAllocas = g.hoistedAllocas[:0]
	if g.hoistedAllocaIDs == nil {
		g.hoistedAllocaIDs = make(map[mir.ValueID]struct{})
	} else {
		for id := range g.hoistedAllocaIDs {
			delete(g.hoistedAllocaIDs, id)
		}
	}
	if fn == nil {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			alloc, ok := instr.(*mir.Alloca)
			if !ok || alloc == nil {
				continue
			}
			g.hoistedAllocas = append(g.hoistedAllocas, alloc)
			g.hoistedAllocaIDs[alloc.Result] = struct{}{}
		}
	}
}

func (g *Generator) emitInstr(instr mir.Instr) {
	switch i := instr.(type) {
	case *mir.Const:
		g.emitConst(i)
	case *mir.Binary:
		g.emitBinary(i)
	case *mir.Unary:
		g.emitUnary(i)
	case *mir.Cast:
		g.emitCast(i)
	case *mir.Alloca:
		g.emitAlloca(i)
	case *mir.Load:
		g.emitLoad(i)
	case *mir.PtrAdd:
		g.emitPtrAdd(i)
	case *mir.PtrOffset:
		g.emitPtrOffset(i)
	case *mir.Store:
		g.emitStore(i)
	case *mir.Call:
		g.emitCall(i)
	case *mir.CallIndirect:
		g.emitCallIndirect(i)
	case *mir.Phi:
		g.emitPhi(i)
	case *mir.ArrayGet:
		g.emitArrayGet(i)
	case *mir.ArraySet:
		g.emitArraySet(i)
	case *mir.MapGet:
		g.emitMapGet(i)
	case *mir.MapSet:
		g.emitMapSet(i)
	case *mir.OptionalNone:
		g.emitOptionalNone(i)
	case *mir.OptionalSome:
		g.emitOptionalSome(i)
	case *mir.OptionalIsSome:
		g.emitOptionalIsSome(i)
	case *mir.OptionalUnwrap:
		g.emitOptionalUnwrap(i)
	case *mir.UnionVariantCheck:
		g.emitUnionVariantCheck(i)
	case *mir.UnionExtract:
		g.emitUnionExtract(i)
	case *mir.ResultOk:
		g.emitResultOk(i)
	case *mir.ResultErr:
		g.emitResultErr(i)
	case *mir.ResultIsOk:
		g.emitResultIsOk(i)
	case *mir.ResultUnwrap:
		g.emitResultUnwrap(i)
	default:
		g.reportUnsupported("instruction", instr.Loc())
	}
}

func (g *Generator) emitTerm(term mir.Term, mainReturnsInt bool) {
	if term == nil {
		g.reportUnsupported("terminator", nil)
		g.emitFallbackReturn()
		return
	}

	switch t := term.(type) {
	case *mir.Return:
		if g.retOutType != nil {
			if t.HasValue {
				if _, ok := g.optionalType(g.retOutType); ok {
					g.emitOptionalCopy(g.retOutParam, g.valueName(t.Value), g.retOutType, &t.Location)
				} else if _, ok := g.resultType(g.retOutType); ok {
					g.emitResultCopy(g.retOutParam, g.valueName(t.Value), g.retOutType, &t.Location)
				}
			}
			g.emitLine("ret")
			return
		}
		if t.HasValue {
			g.emitLine(fmt.Sprintf("ret %s", g.valueName(t.Value)))
			return
		}
		if mainReturnsInt {
			g.emitLine("ret 0")
			return
		}
		g.emitLine("ret")
	case *mir.Br:
		g.emitLine(fmt.Sprintf("jmp %s", g.blockName(t.Target)))
	case *mir.CondBr:
		g.emitLine(fmt.Sprintf("jnz %s, %s, %s", g.valueName(t.Cond), g.blockName(t.Then), g.blockName(t.Else)))
	case *mir.Switch:
		g.reportUnsupported("switch", t.Loc())
		g.emitLine(fmt.Sprintf("jmp %s", g.blockName(t.Default)))
	case *mir.Unreachable:
		g.emitFallbackReturn()
	default:
		g.reportUnsupported("terminator", t.Loc())
		g.emitFallbackReturn()
	}
}

func (g *Generator) emitFallbackReturn() {
	if g == nil {
		return
	}
	if g.retOutType != nil {
		g.emitLine("ret")
		return
	}
	if g.mainReturnsInt {
		g.emitLine("ret 0")
		return
	}
	if g.currentReturn == nil || g.currentReturn.Equals(types.TypeVoid) {
		g.emitLine("ret")
		return
	}
	qbeType, err := g.qbeType(g.currentReturn)
	if err != nil || qbeType == "" {
		g.emitLine("ret")
		return
	}
	switch qbeType {
	case "s":
		g.emitLine("ret s_0")
	case "d":
		g.emitLine("ret d_0")
	default:
		g.emitLine("ret 0")
	}
}
