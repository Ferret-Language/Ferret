package mir

import (
	"math/big"
	"strings"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
)

const (
	ctfeMaxEvalSteps = 100000
	ctfeMaxCallDepth = 64
)

// FunctionResolver resolves a direct callee name to its MIR function.
type FunctionResolver func(current *Module, callee *NameValue) (*Module, *Function, bool)

// EvaluateComptime evaluates compile-time expressions in MIR.
//
// It performs two actions:
// 1. folds `comptime expr` unary values into literals when evaluable.
// 2. validates arguments to `comptime` parameters at call sites.
func EvaluateComptime(diag *diagnostics.DiagnosticBag, mod *Module, resolve FunctionResolver) *Module {
	if mod == nil || resolve == nil {
		return mod
	}
	engine := &ctfeEngine{
		diag:    diag,
		resolve: resolve,
	}
	for _, global := range mod.Globals {
		if global == nil {
			continue
		}
		global.Init = engine.rewriteValue(mod, nil, global.Init, nil)
	}
	for _, fn := range mod.Functions {
		engine.rewriteFunction(mod, fn)
	}
	return mod
}

type ctfeEngine struct {
	diag           *diagnostics.DiagnosticBag
	resolve        FunctionResolver
	panicRaised    bool
	panicMessage   string
	ctfeCandidates map[int]bool
}

func (e *ctfeEngine) rewriteFunction(mod *Module, fn *Function) {
	if fn == nil {
		return
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		e.ctfeCandidates = make(map[int]bool)
		defs := make(map[int]Value)
		for _, instr := range block.Instructions {
			switch i := instr.(type) {
			case *AssignInstr:
				i.Value = e.rewriteValue(mod, fn, i.Value, defs)
				if valueMayMutateMemory(i.Value) {
					invalidateDefsOnUnknownMutation(fn, defs)
				}
				defs[i.TargetID] = i.Value
			case *ComputeInstr:
				i.Value = e.rewriteValue(mod, fn, i.Value, defs)
				if valueMayMutateMemory(i.Value) {
					invalidateDefsOnUnknownMutation(fn, defs)
				}
				defs[i.TargetID] = i.Value
			case *StoreInstr:
				i.Value = e.rewriteValue(mod, fn, i.Value, defs)
				clear(defs)
			case *StoreFieldInstr:
				i.Base = e.rewriteValue(mod, fn, i.Base, defs)
				i.Value = e.rewriteValue(mod, fn, i.Value, defs)
				clear(defs)
			case *EvalInstr:
				i.Value = e.rewriteValue(mod, fn, i.Value, defs)
				if valueMayMutateMemory(i.Value) {
					invalidateDefsOnUnknownMutation(fn, defs)
				}
			case *BindInstr:
				i.Value = e.rewriteValue(mod, fn, i.Value, defs)
				if valueMayMutateMemory(i.Value) {
					invalidateDefsOnUnknownMutation(fn, defs)
				}
			case *LockInstr:
				i.Value = e.rewriteValue(mod, fn, i.Value, defs)
				clear(defs)
			case *DeferInstr:
				for j, child := range i.Body {
					i.Body[j] = e.rewriteDeferredInstr(mod, fn, child, defs)
				}
			}
		}
		block.Terminator = e.rewriteTerminator(mod, fn, block.Terminator, defs)
		eliminateCTFEDeadTemps(fn, block, e.ctfeCandidates)
		e.ctfeCandidates = nil
	}
}

func (e *ctfeEngine) rewriteDeferredInstr(mod *Module, fn *Function, instr Instr, defs map[int]Value) Instr {
	switch i := instr.(type) {
	case nil:
		return nil
	case *AssignInstr:
		i.Value = e.rewriteValue(mod, fn, i.Value, defs)
	case *ComputeInstr:
		i.Value = e.rewriteValue(mod, fn, i.Value, defs)
	case *StoreInstr:
		i.Value = e.rewriteValue(mod, fn, i.Value, defs)
	case *StoreFieldInstr:
		i.Base = e.rewriteValue(mod, fn, i.Base, defs)
		i.Value = e.rewriteValue(mod, fn, i.Value, defs)
	case *EvalInstr:
		i.Value = e.rewriteValue(mod, fn, i.Value, defs)
	case *BindInstr:
		i.Value = e.rewriteValue(mod, fn, i.Value, defs)
	case *LockInstr:
		i.Value = e.rewriteValue(mod, fn, i.Value, defs)
	case *DeferInstr:
		for j, child := range i.Body {
			i.Body[j] = e.rewriteDeferredInstr(mod, fn, child, defs)
		}
	}
	return instr
}

func (e *ctfeEngine) rewriteTerminator(mod *Module, fn *Function, term Terminator, defs map[int]Value) Terminator {
	switch t := term.(type) {
	case nil:
		return nil
	case *BranchTerm:
		t.Cond = e.rewriteValue(mod, fn, t.Cond, defs)
		return t
	case *SwitchTerm:
		t.Value = e.rewriteValue(mod, fn, t.Value, defs)
		for i := range t.Cases {
			t.Cases[i].Expr = e.rewriteValue(mod, fn, t.Cases[i].Expr, defs)
		}
		return t
	case *ReturnTerm:
		t.Value = e.rewriteValue(mod, fn, t.Value, defs)
		return t
	case *PanicTerm:
		t.Value = e.rewriteValue(mod, fn, t.Value, defs)
		return t
	default:
		return term
	}
}

func (e *ctfeEngine) rewriteValue(mod *Module, fn *Function, value Value, defs map[int]Value) Value {
	switch v := value.(type) {
	case nil, *BoolValue, *NumberValue, *StringValue, *NoneValue, *LocalValue, *TempValue, *NameValue:
		return value
	case *UnaryValue:
		v.Right = e.rewriteValue(mod, fn, v.Right, defs)
		if v.Op == "comptime" {
			e.panicRaised = false
			e.panicMessage = ""
			out, ok := e.evalValue(mod, fn, v.Right, nil, defs, 0, 0)
			if !ok {
				if e.panicRaised {
					return v
				}
				loc := v.Location
				e.addError("`comptime` expression must be compile-time evaluable", diagnostics.ErrTypeMismatch, &loc, "this expression is not compile-time evaluable")
				return v
			}
			if folded, ok := ctfeToMIRValue(out, v.ExprType, v.Location); ok {
				e.markComptimeValueInputs(v.Right, defs)
				return folded
			}
			loc := v.Location
			e.addError("`comptime` expression must reduce to a foldable value", diagnostics.ErrTypeMismatch, &loc, "this expression evaluates to a non-foldable value")
			return v
		}
		return v
	case *BinaryValue:
		v.Left = e.rewriteValue(mod, fn, v.Left, defs)
		v.Right = e.rewriteValue(mod, fn, v.Right, defs)
		return v
	case *PostfixValue:
		v.Left = e.rewriteValue(mod, fn, v.Left, defs)
		return v
	case *AddrOfValue:
		v.Source = e.rewriteValue(mod, fn, v.Source, defs)
		return v
	case *LoadValue:
		v.Pointer = e.rewriteValue(mod, fn, v.Pointer, defs)
		return v
	case *CallValue:
		v.Callee = e.rewriteValue(mod, fn, v.Callee, defs)
		for i, arg := range v.Args {
			v.Args[i] = e.rewriteValue(mod, fn, arg, defs)
		}
		e.checkComptimeCallArgs(mod, fn, v, defs)
		return v
	case *FieldLoadValue:
		v.Base = e.rewriteValue(mod, fn, v.Base, defs)
		return v
	case *FieldValue:
		v.Base = e.rewriteValue(mod, fn, v.Base, defs)
		return v
	case *CastValue:
		v.Left = e.rewriteValue(mod, fn, v.Left, defs)
		return v
	case *TypeTestValue:
		v.Left = e.rewriteValue(mod, fn, v.Left, defs)
		return v
	case *CompositeValue:
		for i, item := range v.Items {
			v.Items[i].Value = e.rewriteValue(mod, fn, item.Value, defs)
		}
		return v
	case *InterfaceValue:
		v.Value = e.rewriteValue(mod, fn, v.Value, defs)
		return v
	case *IndexValue:
		v.Base = e.rewriteValue(mod, fn, v.Base, defs)
		v.Index = e.rewriteValue(mod, fn, v.Index, defs)
		return v
	default:
		return value
	}
}

func (e *ctfeEngine) checkComptimeCallArgs(mod *Module, fn *Function, call *CallValue, defs map[int]Value) {
	if callIsCompileErrorIntrinsic(call) {
		return
	}
	target, _, ok := e.resolveCall(mod, call)
	if !ok || target == nil {
		return
	}
	for i, param := range target.Params {
		if param == nil || !param.IsComptime || i >= len(call.Args) {
			continue
		}
		e.panicRaised = false
		e.panicMessage = ""
		if _, ok := e.evalValue(mod, fn, call.Args[i], nil, defs, 0, 0); ok {
			continue
		}
		if e.panicRaised {
			continue
		}
		loc := call.Args[i].Loc()
		e.addError("argument to comptime parameter must be compile-time evaluable", diagnostics.ErrTypeMismatch, &loc, "this expression is not compile-time evaluable")
	}
}

func (e *ctfeEngine) resolveCall(mod *Module, call *CallValue) (*Function, *Module, bool) {
	if e == nil || call == nil {
		return nil, nil, false
	}
	callee, ok := call.Callee.(*NameValue)
	if !ok || callee == nil {
		return nil, nil, false
	}
	owner, target, ok := e.resolve(mod, callee)
	if !ok || target == nil {
		return nil, nil, false
	}
	return target, owner, true
}

func (e *ctfeEngine) markComptimeValueInputs(value Value, defs map[int]Value) {
	if e == nil || e.ctfeCandidates == nil || value == nil {
		return
	}
	seenLocals := make(map[int]bool)
	var visit func(Value)
	visit = func(v Value) {
		switch x := v.(type) {
		case nil:
			return
		case *LocalValue:
			if x.LocalID < 0 || seenLocals[x.LocalID] {
				return
			}
			seenLocals[x.LocalID] = true
			e.ctfeCandidates[x.LocalID] = true
			if defs != nil {
				if def, ok := defs[x.LocalID]; ok {
					visit(def)
				}
			}
		case *UnaryValue:
			visit(x.Right)
		case *BinaryValue:
			visit(x.Left)
			visit(x.Right)
		case *PostfixValue:
			visit(x.Left)
		case *AddrOfValue:
			visit(x.Source)
		case *LoadValue:
			visit(x.Pointer)
		case *CallValue:
			visit(x.Callee)
			for _, arg := range x.Args {
				visit(arg)
			}
		case *FieldLoadValue:
			visit(x.Base)
		case *FieldValue:
			visit(x.Base)
		case *CastValue:
			visit(x.Left)
		case *TypeTestValue:
			visit(x.Left)
		case *CompositeValue:
			for _, item := range x.Items {
				visit(item.Value)
			}
		case *InterfaceValue:
			visit(x.Value)
		case *IndexValue:
			visit(x.Base)
			visit(x.Index)
		}
	}
	visit(value)
}

func eliminateCTFEDeadTemps(fn *Function, block *Block, candidates map[int]bool) {
	if fn == nil || block == nil || len(candidates) == 0 || len(block.Instructions) == 0 {
		return
	}
	live := usedLocalsInTerminator(block.Terminator)
	out := make([]Instr, 0, len(block.Instructions))
	for i := len(block.Instructions) - 1; i >= 0; i-- {
		instr := block.Instructions[i]
		drop := false
		switch ins := instr.(type) {
		case *AssignInstr:
			if candidates[ins.TargetID] {
				if local := lookupLocal(fn, ins.TargetID); local != nil && local.IsTemp && !live[ins.TargetID] {
					drop = true
				}
			}
			if !drop {
				delete(live, ins.TargetID)
				addUsedLocalsFromValue(live, ins.Value)
			}
		case *ComputeInstr:
			if candidates[ins.TargetID] {
				if local := lookupLocal(fn, ins.TargetID); local != nil && local.IsTemp && !live[ins.TargetID] {
					drop = true
				}
			}
			if !drop {
				delete(live, ins.TargetID)
				addUsedLocalsFromValue(live, ins.Value)
			}
		default:
			addUsedLocalsFromInstr(live, instr)
		}
		if !drop {
			out = append(out, instr)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	block.Instructions = out
}

type ctfeValueKind uint8

const (
	ctfeInvalid ctfeValueKind = iota
	ctfeInt
	ctfeBool
	ctfeString
	ctfeNone
	ctfeObject
	ctfeSequence
	ctfePointer
)

type ctfeValue struct {
	kind   ctfeValueKind
	intVal *big.Int
	boolV  bool
	strVal string
	fields []ctfeValue
	elems  []ctfeValue
	ptr    int
}

func (e *ctfeEngine) evalValue(mod *Module, fn *Function, value Value, locals map[int]ctfeValue, defs map[int]Value, depth int, steps int) (ctfeValue, bool) {
	if depth > ctfeMaxCallDepth || steps > ctfeMaxEvalSteps || value == nil {
		return ctfeValue{}, false
	}
	switch v := value.(type) {
	case *BoolValue:
		return ctfeValue{kind: ctfeBool, boolV: v.Value}, true
	case *StringValue:
		return ctfeValue{kind: ctfeString, strVal: v.Value}, true
	case *NoneValue:
		return ctfeValue{kind: ctfeNone}, true
	case *NumberValue:
		n, ok := parseBigInt(v.Value)
		if !ok {
			return ctfeValue{}, false
		}
		return ctfeValue{kind: ctfeInt, intVal: n}, true
	case *NameValue:
		if fn == nil || len(v.Path) != 1 {
			return ctfeValue{}, false
		}
		name := v.Path[0]
		resolveByID := func(localID int) (ctfeValue, bool) {
			if localID < 0 {
				return ctfeValue{}, false
			}
			if locals != nil {
				if out, ok := locals[localID]; ok {
					return out, true
				}
			}
			if defs != nil {
				if replacement, ok := defs[localID]; ok && replacement != nil {
					return e.evalValue(mod, fn, replacement, locals, defs, depth, steps+1)
				}
			}
			return ctfeValue{}, false
		}
		for _, param := range fn.Params {
			if param != nil && param.Name == name {
				return resolveByID(param.LocalID)
			}
		}
		for _, local := range fn.Locals {
			if local != nil && local.Name == name {
				return resolveByID(local.ID)
			}
		}
		return ctfeValue{}, false
	case *LocalValue:
		if locals != nil {
			if out, ok := locals[v.LocalID]; ok {
				return out, true
			}
		}
		if defs != nil {
			if replacement, ok := defs[v.LocalID]; ok && replacement != nil {
				return e.evalValue(mod, fn, replacement, locals, defs, depth, steps+1)
			}
		}
		return ctfeValue{}, false
	case *UnaryValue:
		right, ok := e.evalValue(mod, fn, v.Right, locals, defs, depth, steps+1)
		if !ok {
			return ctfeValue{}, false
		}
		switch v.Op {
		case "comptime", "copy", "take", "unsafe", "?":
			return right, true
		case "!":
			if right.kind != ctfeBool {
				return ctfeValue{}, false
			}
			return ctfeValue{kind: ctfeBool, boolV: !right.boolV}, true
		case "-":
			if right.kind != ctfeInt || right.intVal == nil {
				return ctfeValue{}, false
			}
			out := new(big.Int).Set(right.intVal)
			out.Neg(out)
			return ctfeValue{kind: ctfeInt, intVal: out}, true
		default:
			return ctfeValue{}, false
		}
	case *BinaryValue:
		left, ok := e.evalValue(mod, fn, v.Left, locals, defs, depth, steps+1)
		if !ok {
			return ctfeValue{}, false
		}
		right, ok := e.evalValue(mod, fn, v.Right, locals, defs, depth, steps+1)
		if !ok {
			return ctfeValue{}, false
		}
		return ctfeApplyBinary(v.Op, left, right)
	case *CastValue:
		return e.evalValue(mod, fn, v.Left, locals, defs, depth, steps+1)
	case *CallValue:
		if callIsCompileErrorIntrinsic(v) {
			msg, ok := e.evalCompileErrorMessage(mod, fn, v, locals, defs, depth, steps+1)
			if !ok {
				// Defer diagnostics until this comptime path is executed with concrete inputs.
				e.panicRaised = true
				return ctfeValue{}, false
			}
			e.raiseCompileTimeError(v.Location, msg, "compile_error invoked during compile-time evaluation")
			return ctfeValue{}, false
		}
		target, owner, ok := e.resolveCall(mod, v)
		if !ok || target == nil {
			return ctfeValue{}, false
		}
		args := make([]ctfeValue, 0, len(v.Args))
		for _, arg := range v.Args {
			evaluated, ok := e.evalValue(mod, fn, arg, locals, defs, depth, steps+1)
			if !ok {
				return ctfeValue{}, false
			}
			args = append(args, evaluated)
		}
		return e.execFunction(owner, target, args, locals, depth+1, steps+1)
	case *CompositeValue:
		if _, isString := v.Type().(*typeinfo.StringType); isString {
			for _, item := range v.Items {
				if item.Name != "ptr" {
					continue
				}
				ptrVal, ok := e.evalValue(mod, fn, item.Value, locals, defs, depth, steps+1)
				if !ok || ptrVal.kind != ctfeString {
					return ctfeValue{}, false
				}
				return ctfeValue{kind: ctfeString, strVal: ptrVal.strVal}, true
			}
			return ctfeValue{}, false
		}
		ptrItem := -1
		lenItem := -1
		for i, item := range v.Items {
			switch item.Name {
			case "ptr":
				ptrItem = i
			case "len":
				lenItem = i
			}
		}
		if ptrItem >= 0 && lenItem >= 0 {
			ptrVal, ok := e.evalValue(mod, fn, v.Items[ptrItem].Value, locals, defs, depth, steps+1)
			if !ok || ptrVal.kind != ctfeString {
				return ctfeValue{}, false
			}
			lenVal, ok := e.evalValue(mod, fn, v.Items[lenItem].Value, locals, defs, depth, steps+1)
			if !ok || lenVal.kind != ctfeInt {
				return ctfeValue{}, false
			}
			return ctfeValue{kind: ctfeString, strVal: ptrVal.strVal}, true
		}
		if isArrayOrTupleType(v.Type()) {
			elems := make([]ctfeValue, 0, len(v.Items))
			for _, item := range v.Items {
				ev, ok := e.evalValue(mod, fn, item.Value, locals, defs, depth, steps+1)
				if !ok {
					return ctfeValue{}, false
				}
				elems = append(elems, ev)
			}
			return ctfeValue{kind: ctfeSequence, elems: elems}, true
		}
		names := ctfeStructFieldOrder(v.Type())
		if len(names) == 0 {
			return ctfeValue{}, false
		}
		fields := make([]ctfeValue, len(names))
		for i := range fields {
			fields[i] = ctfeValue{kind: ctfeInvalid}
		}
		for _, item := range v.Items {
			idx := ctfeFieldIndexByName(names, item.Name)
			if idx < 0 {
				return ctfeValue{}, false
			}
			fv, ok := e.evalValue(mod, fn, item.Value, locals, defs, depth, steps+1)
			if !ok {
				return ctfeValue{}, false
			}
			fields[idx] = fv
		}
		return ctfeValue{kind: ctfeObject, fields: fields}, true
	case *FieldLoadValue:
		base, ok := e.evalValue(mod, fn, v.Base, locals, defs, depth, steps+1)
		if !ok {
			return ctfeValue{}, false
		}
		return ctfeFieldAt(base, v.FieldIndex, locals)
	case *FieldValue:
		if v.FieldIndex < 0 {
			return ctfeValue{}, false
		}
		base, ok := e.evalValue(mod, fn, v.Base, locals, defs, depth, steps+1)
		if !ok {
			return ctfeValue{}, false
		}
		return ctfeFieldAt(base, v.FieldIndex, locals)
	case *AddrOfValue:
		switch src := v.Source.(type) {
		case *LocalValue:
			return ctfeValue{kind: ctfePointer, ptr: src.LocalID}, true
		case *NameValue:
			if fn == nil || len(src.Path) != 1 {
				return ctfeValue{}, false
			}
			name := src.Path[0]
			for _, param := range fn.Params {
				if param != nil && param.Name == name {
					return ctfeValue{kind: ctfePointer, ptr: param.LocalID}, true
				}
			}
			for _, local := range fn.Locals {
				if local != nil && local.Name == name {
					return ctfeValue{kind: ctfePointer, ptr: local.ID}, true
				}
			}
			return ctfeValue{}, false
		default:
			return ctfeValue{}, false
		}
	case *LoadValue:
		ptr, ok := e.evalValue(mod, fn, v.Pointer, locals, defs, depth, steps+1)
		if !ok || ptr.kind != ctfePointer || locals == nil {
			return ctfeValue{}, false
		}
		out, ok := locals[ptr.ptr]
		return out, ok
	case *IndexValue:
		base, ok := e.evalValue(mod, fn, v.Base, locals, defs, depth, steps+1)
		if !ok {
			return ctfeValue{}, false
		}
		index, ok := e.evalValue(mod, fn, v.Index, locals, defs, depth, steps+1)
		if !ok || index.kind != ctfeInt || index.intVal == nil || !index.intVal.IsInt64() {
			return ctfeValue{}, false
		}
		return ctfeIndexAt(base, int(index.intVal.Int64()), locals)
	default:
		return ctfeValue{}, false
	}
}

func (e *ctfeEngine) execFunction(mod *Module, fn *Function, args []ctfeValue, parent map[int]ctfeValue, depth int, steps int) (ctfeValue, bool) {
	if fn == nil || mod == nil || depth > ctfeMaxCallDepth || steps > ctfeMaxEvalSteps {
		return ctfeValue{}, false
	}
	blocks := make(map[int]*Block, len(fn.Blocks))
	for _, block := range fn.Blocks {
		if block != nil {
			blocks[block.ID] = block
		}
	}
	locals := make(map[int]ctfeValue, len(fn.Locals))
	aliasTargets := make(map[int]int)
	nextAliasID := -1
	for i, param := range fn.Params {
		if param == nil || i >= len(args) {
			continue
		}
		arg := args[i]
		if parent != nil && arg.kind == ctfePointer {
			if pointee, ok := parent[arg.ptr]; ok {
				aliasID := nextAliasID
				nextAliasID--
				locals[param.LocalID] = ctfeValue{kind: ctfePointer, ptr: aliasID}
				locals[aliasID] = pointee
				aliasTargets[aliasID] = arg.ptr
				continue
			}
		}
		locals[param.LocalID] = arg
	}
	writeBackAliases := func() {
		if parent == nil || len(aliasTargets) == 0 {
			return
		}
		for aliasID, parentID := range aliasTargets {
			if value, ok := locals[aliasID]; ok {
				parent[parentID] = value
			}
		}
	}

	current := fn.EntryID
	for steps < ctfeMaxEvalSteps {
		steps++
		block := blocks[current]
		if block == nil {
			return ctfeValue{}, false
		}
		for _, instr := range block.Instructions {
			switch i := instr.(type) {
			case *AssignInstr:
				v, ok := e.evalValue(mod, fn, i.Value, locals, nil, depth, steps)
				if !ok {
					return ctfeValue{}, false
				}
				locals[i.TargetID] = v
			case *ComputeInstr:
				v, ok := e.evalValue(mod, fn, i.Value, locals, nil, depth, steps)
				if !ok {
					return ctfeValue{}, false
				}
				locals[i.TargetID] = v
			case *StoreInstr:
				v, ok := e.evalValue(mod, fn, i.Value, locals, nil, depth, steps)
				if !ok {
					return ctfeValue{}, false
				}
				switch target := i.Target.(type) {
				case *LocalPlace:
					locals[target.LocalID] = v
				case *DerefPlace:
					p, ok := e.evalValue(mod, fn, target.Pointer, locals, nil, depth, steps)
					if !ok || p.kind != ctfePointer {
						return ctfeValue{}, false
					}
					locals[p.ptr] = v
				default:
					return ctfeValue{}, false
				}
			case *StoreFieldInstr:
				base, ok := e.evalValue(mod, fn, i.Base, locals, nil, depth, steps)
				if !ok {
					return ctfeValue{}, false
				}
				value, ok := e.evalValue(mod, fn, i.Value, locals, nil, depth, steps)
				if !ok {
					return ctfeValue{}, false
				}
				if base.kind == ctfePointer {
					target, ok := locals[base.ptr]
					if !ok || target.kind != ctfeObject || i.FieldIndex < 0 || i.FieldIndex >= len(target.fields) {
						return ctfeValue{}, false
					}
					target.fields[i.FieldIndex] = value
					locals[base.ptr] = target
					continue
				}
				if base.kind != ctfeObject || i.FieldIndex < 0 || i.FieldIndex >= len(base.fields) {
					return ctfeValue{}, false
				}
				base.fields[i.FieldIndex] = value
			case *EvalInstr:
				if _, ok := e.evalValue(mod, fn, i.Value, locals, nil, depth, steps); !ok {
					return ctfeValue{}, false
				}
			case *BindInstr:
				if _, ok := e.evalValue(mod, fn, i.Value, locals, nil, depth, steps); !ok {
					return ctfeValue{}, false
				}
			}
		}
		switch t := block.Terminator.(type) {
		case *JumpTerm:
			current = t.TargetID
		case *BranchTerm:
			cond, ok := e.evalValue(mod, fn, t.Cond, locals, nil, depth, steps)
			if !ok || cond.kind != ctfeBool {
				return ctfeValue{}, false
			}
			if cond.boolV {
				current = t.TrueID
			} else {
				current = t.FalseID
			}
		case *ReturnTerm:
			writeBackAliases()
			if t.Value == nil {
				return ctfeValue{kind: ctfeNone}, true
			}
			return e.evalValue(mod, fn, t.Value, locals, nil, depth, steps)
		case *PanicTerm:
			var payload ctfeValue
			hasPayload := false
			if t.Value != nil {
				if v, ok := e.evalValue(mod, fn, t.Value, locals, nil, depth, steps); ok {
					payload = v
					hasPayload = true
				}
			}
			e.raiseCompileTimePanic(t.Loc(), payload, hasPayload)
			return ctfeValue{}, false
		case *ExitTerm:
			writeBackAliases()
			return ctfeValue{kind: ctfeNone}, true
		default:
			return ctfeValue{}, false
		}
	}
	return ctfeValue{}, false
}

func ctfeApplyBinary(op string, left, right ctfeValue) (ctfeValue, bool) {
	switch op {
	case "add":
		op = "+"
	case "sub":
		op = "-"
	case "mul":
		op = "*"
	case "div":
		op = "/"
	case "mod":
		op = "%"
	case "eq":
		op = "=="
	case "neq":
		op = "!="
	case "lt":
		op = "<"
	case "le":
		op = "<="
	case "gt":
		op = ">"
	case "ge":
		op = ">="
	case "and":
		op = "&&"
	case "or":
		op = "||"
	}
	switch op {
	case "&&", "||":
		if left.kind != ctfeBool || right.kind != ctfeBool {
			return ctfeValue{}, false
		}
		if op == "&&" {
			return ctfeValue{kind: ctfeBool, boolV: left.boolV && right.boolV}, true
		}
		return ctfeValue{kind: ctfeBool, boolV: left.boolV || right.boolV}, true
	case "+", "-", "*", "/", "%", "==", "!=", "<", "<=", ">", ">=":
		if left.kind == ctfeString && right.kind == ctfeString {
			switch op {
			case "+":
				return ctfeValue{kind: ctfeString, strVal: left.strVal + right.strVal}, true
			case "==":
				return ctfeValue{kind: ctfeBool, boolV: left.strVal == right.strVal}, true
			case "!=":
				return ctfeValue{kind: ctfeBool, boolV: left.strVal != right.strVal}, true
			default:
				return ctfeValue{}, false
			}
		}
		if left.kind == ctfeNone && right.kind == ctfeNone {
			switch op {
			case "==":
				return ctfeValue{kind: ctfeBool, boolV: true}, true
			case "!=":
				return ctfeValue{kind: ctfeBool, boolV: false}, true
			default:
				return ctfeValue{}, false
			}
		}
		if left.kind == ctfeBool && right.kind == ctfeBool {
			switch op {
			case "==":
				return ctfeValue{kind: ctfeBool, boolV: left.boolV == right.boolV}, true
			case "!=":
				return ctfeValue{kind: ctfeBool, boolV: left.boolV != right.boolV}, true
			default:
				return ctfeValue{}, false
			}
		}
		if left.kind != ctfeInt || right.kind != ctfeInt || left.intVal == nil || right.intVal == nil {
			return ctfeValue{}, false
		}
		l := new(big.Int).Set(left.intVal)
		r := new(big.Int).Set(right.intVal)
		switch op {
		case "+":
			return ctfeValue{kind: ctfeInt, intVal: new(big.Int).Add(l, r)}, true
		case "-":
			return ctfeValue{kind: ctfeInt, intVal: new(big.Int).Sub(l, r)}, true
		case "*":
			return ctfeValue{kind: ctfeInt, intVal: new(big.Int).Mul(l, r)}, true
		case "/":
			if r.Sign() == 0 {
				return ctfeValue{}, false
			}
			return ctfeValue{kind: ctfeInt, intVal: new(big.Int).Quo(l, r)}, true
		case "%":
			if r.Sign() == 0 {
				return ctfeValue{}, false
			}
			return ctfeValue{kind: ctfeInt, intVal: new(big.Int).Rem(l, r)}, true
		case "==":
			return ctfeValue{kind: ctfeBool, boolV: l.Cmp(r) == 0}, true
		case "!=":
			return ctfeValue{kind: ctfeBool, boolV: l.Cmp(r) != 0}, true
		case "<":
			return ctfeValue{kind: ctfeBool, boolV: l.Cmp(r) < 0}, true
		case "<=":
			return ctfeValue{kind: ctfeBool, boolV: l.Cmp(r) <= 0}, true
		case ">":
			return ctfeValue{kind: ctfeBool, boolV: l.Cmp(r) > 0}, true
		case ">=":
			return ctfeValue{kind: ctfeBool, boolV: l.Cmp(r) >= 0}, true
		}
	}
	return ctfeValue{}, false
}

func ctfeFieldAt(base ctfeValue, index int, locals map[int]ctfeValue) (ctfeValue, bool) {
	if index < 0 {
		return ctfeValue{}, false
	}
	if base.kind == ctfePointer {
		if locals == nil {
			return ctfeValue{}, false
		}
		target, ok := locals[base.ptr]
		if !ok || target.kind != ctfeObject {
			return ctfeValue{}, false
		}
		if index >= len(target.fields) {
			return ctfeValue{}, false
		}
		return target.fields[index], true
	}
	if base.kind != ctfeObject || index >= len(base.fields) {
		return ctfeValue{}, false
	}
	return base.fields[index], true
}

func ctfeIndexAt(base ctfeValue, index int, locals map[int]ctfeValue) (ctfeValue, bool) {
	if index < 0 {
		return ctfeValue{}, false
	}
	switch base.kind {
	case ctfeSequence:
		if index >= len(base.elems) {
			return ctfeValue{}, false
		}
		return base.elems[index], true
	case ctfePointer:
		if locals == nil {
			return ctfeValue{}, false
		}
		target, ok := locals[base.ptr]
		if !ok || target.kind != ctfeSequence || index >= len(target.elems) {
			return ctfeValue{}, false
		}
		return target.elems[index], true
	default:
		return ctfeValue{}, false
	}
}

func invalidateDefsOnUnknownMutation(fn *Function, defs map[int]Value) {
	if defs == nil {
		return
	}
	for id := range defs {
		local := lookupLocal(fn, id)
		if local == nil || local.Mutable {
			delete(defs, id)
		}
	}
}

func ctfeToMIRValue(v ctfeValue, typ typeinfo.Type, loc source.Location) (Value, bool) {
	switch v.kind {
	case ctfeInt:
		if v.intVal == nil {
			return nil, false
		}
		return &NumberValue{baseValue: baseValue{Location: loc, ExprType: typ}, Value: v.intVal.String()}, true
	case ctfeBool:
		return &BoolValue{baseValue: baseValue{Location: loc, ExprType: typ}, Value: v.boolV}, true
	case ctfeString:
		return &StringValue{baseValue: baseValue{Location: loc, ExprType: typ}, Value: v.strVal}, true
	case ctfeNone:
		return &NoneValue{baseValue: baseValue{Location: loc, ExprType: typ}}, true
	case ctfeObject:
		names := ctfeStructFieldOrder(typ)
		if len(names) == 0 || len(names) != len(v.fields) {
			return nil, false
		}
		items := make([]CompositeItem, 0, len(v.fields))
		for i, field := range v.fields {
			fieldType := ctfeFieldTypeAt(typ, i)
			mirValue, ok := ctfeToMIRValue(field, fieldType, loc)
			if !ok {
				return nil, false
			}
			items = append(items, CompositeItem{Name: names[i], Value: mirValue})
		}
		return &CompositeValue{
			baseValue: baseValue{Location: loc, ExprType: typ},
			Items:     items,
		}, true
	case ctfeSequence:
		items := make([]CompositeItem, 0, len(v.elems))
		for i, elem := range v.elems {
			elemType := ctfeSeqElemTypeAt(typ, i)
			mirValue, ok := ctfeToMIRValue(elem, elemType, loc)
			if !ok {
				return nil, false
			}
			items = append(items, CompositeItem{Name: "", Value: mirValue})
		}
		return &CompositeValue{
			baseValue: baseValue{Location: loc, ExprType: typ},
			Items:     items,
		}, true
	default:
		return nil, false
	}
}

func ctfeSeqElemTypeAt(typ typeinfo.Type, index int) typeinfo.Type {
	if index < 0 {
		return typeinfo.UnknownType{}
	}
	switch t := typ.(type) {
	case *typeinfo.ArrayType:
		if t == nil {
			return typeinfo.UnknownType{}
		}
		return t.Inner
	case *typeinfo.TupleType:
		if t == nil || index >= len(t.Elems) {
			return typeinfo.UnknownType{}
		}
		return t.Elems[index]
	case *typeinfo.RefType:
		return ctfeSeqElemTypeAt(t.Inner, index)
	case *typeinfo.PointerType:
		return ctfeSeqElemTypeAt(t.Inner, index)
	default:
		return typeinfo.UnknownType{}
	}
}

func isArrayOrTupleType(typ typeinfo.Type) bool {
	switch t := typ.(type) {
	case *typeinfo.ArrayType, *typeinfo.TupleType:
		return true
	case *typeinfo.RefType:
		return isArrayOrTupleType(t.Inner)
	case *typeinfo.PointerType:
		return isArrayOrTupleType(t.Inner)
	default:
		return false
	}
}

func ctfeFieldTypeAt(typ typeinfo.Type, index int) typeinfo.Type {
	switch t := typ.(type) {
	case *typeinfo.StructType:
		if t != nil && index >= 0 && index < len(t.OrderedFields) && t.OrderedFields[index] != nil {
			return t.OrderedFields[index].Type
		}
	case *typeinfo.NamedType:
		if t != nil && t.Decl != nil {
			if st, ok := t.Decl.Type.(*ast.StructType); ok {
				if index >= 0 && index < len(st.Fields) && st.Fields[index] != nil {
					return tFieldTypeFromDecl(st.Fields[index])
				}
			}
		}
	case *typeinfo.RefType:
		return ctfeFieldTypeAt(t.Inner, index)
	case *typeinfo.PointerType:
		return ctfeFieldTypeAt(t.Inner, index)
	}
	return typeinfo.UnknownType{}
}

func tFieldTypeFromDecl(field *ast.FieldDecl) typeinfo.Type {
	if field == nil || field.Type == nil {
		return typeinfo.UnknownType{}
	}
	// MIR already tracks concrete types on values; declaration fallback only.
	return typeinfo.UnknownType{}
}

func ctfeStructFieldOrder(typ typeinfo.Type) []string {
	switch t := typ.(type) {
	case *typeinfo.StructType:
		if t == nil || len(t.OrderedFields) == 0 {
			return nil
		}
		out := make([]string, 0, len(t.OrderedFields))
		for _, field := range t.OrderedFields {
			if field == nil || field.Name == "" {
				return nil
			}
			out = append(out, field.Name)
		}
		return out
	case *typeinfo.NamedType:
		if t == nil || t.Decl == nil {
			return nil
		}
		st, ok := t.Decl.Type.(*ast.StructType)
		if !ok || st == nil || len(st.Fields) == 0 {
			return nil
		}
		out := make([]string, 0, len(st.Fields))
		for _, field := range st.Fields {
			if field == nil || field.Name == nil || field.Name.Text() == "" {
				return nil
			}
			out = append(out, field.Name.Text())
		}
		return out
	case *typeinfo.RefType:
		return ctfeStructFieldOrder(t.Inner)
	case *typeinfo.PointerType:
		return ctfeStructFieldOrder(t.Inner)
	default:
		return nil
	}
}

func ctfeFieldIndexByName(names []string, name string) int {
	for i, field := range names {
		if field == name {
			return i
		}
	}
	return -1
}

func (e *ctfeEngine) addError(message, code string, loc *source.Location, label string) {
	if e == nil || e.diag == nil {
		return
	}
	d := diagnostics.NewError(message).WithCode(code)
	if loc != nil {
		d = d.WithPrimaryLabel(loc, label)
	}
	e.diag.Add(d)
}

func callIsCompileErrorIntrinsic(call *CallValue) bool {
	if call == nil {
		return false
	}
	name, ok := call.Callee.(*NameValue)
	if !ok || name == nil || len(name.Path) == 0 {
		return false
	}
	return name.Path[len(name.Path)-1] == "compile_error"
}

func (e *ctfeEngine) evalCompileErrorMessage(mod *Module, fn *Function, call *CallValue, locals map[int]ctfeValue, defs map[int]Value, depth int, steps int) (string, bool) {
	if call == nil {
		return "compile-time error", true
	}
	if len(call.Args) != 1 {
		return "compile_error requires exactly one argument", true
	}
	msg, ok := e.evalValue(mod, fn, call.Args[0], locals, defs, depth, steps+1)
	if !ok {
		return "", false
	}
	if msg.kind != ctfeString {
		return "compile_error message must be string", true
	}
	text := strings.TrimSpace(msg.strVal)
	if text == "" {
		return "compile-time error", true
	}
	return "compile-time error: " + text, true
}

func (e *ctfeEngine) raiseCompileTimeError(loc source.Location, message string, label string) {
	if e == nil {
		return
	}
	e.panicRaised = true
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "compile-time error"
	}
	e.panicMessage = msg
	e.addError(msg, diagnostics.ErrInvalidOperation, &loc, label)
}

func (e *ctfeEngine) raiseCompileTimePanic(loc source.Location, payload ctfeValue, hasPayload bool) {
	msg := "compile-time panic"
	if hasPayload {
		text := ctfeValueText(payload)
		if text != "" {
			msg = "compile-time panic: " + text
		}
	}
	e.raiseCompileTimeError(loc, msg, "panic triggered during compile-time evaluation")
}

func ctfeValueText(v ctfeValue) string {
	switch v.kind {
	case ctfeString:
		return v.strVal
	case ctfeInt:
		if v.intVal == nil {
			return ""
		}
		return v.intVal.String()
	case ctfeBool:
		if v.boolV {
			return "true"
		}
		return "false"
	case ctfeNone:
		return "none"
	default:
		return ""
	}
}
