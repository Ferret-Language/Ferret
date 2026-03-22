package mir

import (
	"fmt"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/source"
)

type normalizer struct {
	tempCounter int
}

func NormalizeModule(mod *Module) *Module {
	if mod == nil {
		return nil
	}
	n := &normalizer{}
	for _, global := range mod.Globals {
		if global != nil {
			global.Init = n.normalizeGlobalValue(global.Init)
		}
	}
	for _, fn := range mod.Functions {
		n.normalizeFunction(fn)
	}
	return mod
}

func (n *normalizer) normalizeGlobalValue(value Value) Value {
	switch v := value.(type) {
	case nil, *NameValue, *LocalValue, *NumberValue, *BoolValue, *StringValue, *NoneValue:
		return v
	case *UnaryValue:
		v.Right = n.normalizeGlobalValue(v.Right)
		return v
	case *AddrOfValue:
		v.Source = n.normalizeGlobalValue(v.Source)
		return v
	case *LoadValue:
		v.Pointer = n.normalizeGlobalValue(v.Pointer)
		return v
	case *BinaryValue:
		v.Left = n.normalizeGlobalValue(v.Left)
		v.Right = n.normalizeGlobalValue(v.Right)
		return v
	case *PostfixValue:
		v.Left = n.normalizeGlobalValue(v.Left)
		return v
	case *CallValue:
		v.Callee = n.normalizeGlobalValue(v.Callee)
		for i, arg := range v.Args {
			v.Args[i] = n.normalizeGlobalValue(arg)
		}
		return v
	case *FieldValue:
		v.Base = n.normalizeGlobalValue(v.Base)
		return v
	case *CastValue:
		v.Left = n.normalizeGlobalValue(v.Left)
		return v
	case *TypeTestValue:
		v.Left = n.normalizeGlobalValue(v.Left)
		return v
	case *CompositeValue:
		for i, item := range v.Items {
			v.Items[i].Value = n.normalizeGlobalValue(item.Value)
		}
		return v
	case *InterfaceValue:
		v.Value = n.normalizeGlobalValue(v.Value)
		return v
	default:
		return v
	}
}

func (n *normalizer) normalizeFunction(fn *Function) {
	if fn == nil {
		return
	}
	n.tempCounter = len(fn.Locals)
	for _, block := range fn.Blocks {
		n.normalizeBlock(fn, block)
	}
	removeUnreachableBlocks(fn)
}

func (n *normalizer) normalizeBlock(fn *Function, block *Block) {
	if block == nil {
		return
	}
	out := make([]Instr, 0, len(block.Instructions)*2)
	for _, instr := range block.Instructions {
		out = append(out, n.normalizeInstr(fn, instr)...)
	}
	if block.Terminator != nil {
		var temps []Instr
		block.Terminator, temps = n.normalizeTerminator(fn, block.Terminator)
		out = append(out, temps...)
	}
	block.Instructions = out
}

func (n *normalizer) normalizeInstr(fn *Function, instr Instr) []Instr {
	switch i := instr.(type) {
	case nil:
		return nil
	case *BindInstr:
		temps, value := n.normalizeValue(fn, i.Value)
		i.Value = value
		return append(temps, i)
	case *AssignInstr:
		if normalizeLocalHasVoidType(fn, i.TargetID) {
			return n.normalizeVoidEffectInstr(fn, i.Location, i.Value)
		}
		temps, value := n.normalizeValueInline(fn, i.Value)
		i.Value = value
		return append(temps, i)
	case *ComputeInstr:
		if normalizeLocalHasVoidType(fn, i.TargetID) || isVoidMIRType(i.Type) {
			return n.normalizeVoidEffectInstr(fn, i.Location, i.Value)
		}
		temps, value := n.normalizeValue(fn, i.Value)
		i.Value = value
		return append(temps, i)
	case *StoreInstr:
		temps, value := n.normalizeValueInline(fn, i.Value)
		i.Value = value
		return append(temps, i)
	case *StoreFieldInstr:
		baseTemps, base := n.normalizeValue(fn, i.Base)
		valueTemps, value := n.normalizeValue(fn, i.Value)
		i.Base = base
		i.Value = value
		return append(append(baseTemps, valueTemps...), i)
	case *EvalInstr:
		temps, value := n.normalizeValueInline(fn, i.Value)
		i.Value = value
		return append(temps, i)
	case *DeferInstr:
		body := make([]Instr, 0, len(i.Body)*2)
		for _, child := range i.Body {
			body = append(body, n.normalizeInstr(fn, child)...)
		}
		i.Body = body
		return []Instr{i}
	case *LockInstr:
		temps, value := n.normalizeValue(fn, i.Value)
		i.Value = value
		return append(temps, i)
	default:
		return []Instr{instr}
	}
}

func (n *normalizer) normalizeTerminator(fn *Function, term Terminator) (Terminator, []Instr) {
	switch t := term.(type) {
	case nil:
		return nil, nil
	case *JumpTerm, *ExitTerm:
		return term, nil
	case *BranchTerm:
		temps, cond := n.normalizeValue(fn, t.Cond)
		t.Cond = cond
		return t, temps
	case *SwitchTerm:
		temps, value := n.normalizeValue(fn, t.Value)
		t.Value = value
		for i, kase := range t.Cases {
			caseTemps, caseExpr := n.normalizeValue(fn, kase.Expr)
			temps = append(temps, caseTemps...)
			t.Cases[i].Expr = caseExpr
		}
		return t, temps
	case *ReturnTerm:
		temps, value := n.normalizeValue(fn, t.Value)
		t.Value = value
		return t, temps
	case *PanicTerm:
		temps, value := n.normalizeValue(fn, t.Value)
		t.Value = value
		return t, temps
	default:
		return term, nil
	}
}

func (n *normalizer) normalizeValue(fn *Function, value Value) ([]Instr, Value) {
	switch v := value.(type) {
	case nil, *NameValue, *LocalValue, *NumberValue, *BoolValue, *StringValue, *NoneValue:
		return nil, v
	case *UnaryValue:
		temps, right := n.normalizeValue(fn, v.Right)
		copy := *v
		copy.Right = right
		return n.wrapComputed(fn, &copy, temps)
	case *AddrOfValue:
		temps, source := n.normalizeValue(fn, v.Source)
		copy := *v
		copy.Source = source
		return n.wrapComputed(fn, &copy, temps)
	case *LoadValue:
		temps, ptr := n.normalizeValue(fn, v.Pointer)
		copy := *v
		copy.Pointer = ptr
		return n.wrapComputed(fn, &copy, temps)
	case *BinaryValue:
		leftTemps, left := n.normalizeValue(fn, v.Left)
		rightTemps, right := n.normalizeValue(fn, v.Right)
		copy := *v
		copy.Left = left
		copy.Right = right
		return n.wrapComputed(fn, &copy, append(leftTemps, rightTemps...))
	case *PostfixValue:
		temps, left := n.normalizeValue(fn, v.Left)
		copy := *v
		copy.Left = left
		return n.wrapComputed(fn, &copy, temps)
	case *CallValue:
		calleeTemps, callee := n.normalizeValue(fn, v.Callee)
		temps := append([]Instr{}, calleeTemps...)
		args := make([]Value, 0, len(v.Args))
		for _, arg := range v.Args {
			argTemps, simpleArg := n.normalizeValue(fn, arg)
			temps = append(temps, argTemps...)
			args = append(args, simpleArg)
		}
		copy := *v
		copy.Callee = callee
		copy.Args = args
		return n.wrapComputed(fn, &copy, temps)
	case *FieldLoadValue:
		temps, base := n.normalizeValue(fn, v.Base)
		copy := *v
		copy.Base = base
		return n.wrapComputed(fn, &copy, temps)
	case *IndexValue:
		baseTemps, base := n.normalizeValue(fn, v.Base)
		indexTemps, index := n.normalizeValue(fn, v.Index)
		copy := *v
		copy.Base = base
		copy.Index = index
		return n.wrapComputed(fn, &copy, append(baseTemps, indexTemps...))
	case *FieldValue:
		temps, base := n.normalizeValue(fn, v.Base)
		copy := *v
		copy.Base = base
		return n.wrapComputed(fn, &copy, temps)
	case *CastValue:
		temps, left := n.normalizeValue(fn, v.Left)
		copy := *v
		copy.Left = left
		return n.wrapComputed(fn, &copy, temps)
	case *TypeTestValue:
		temps, left := n.normalizeValue(fn, v.Left)
		copy := *v
		copy.Left = left
		return n.wrapComputed(fn, &copy, temps)
	case *CompositeValue:
		temps := make([]Instr, 0, len(v.Items))
		items := make([]CompositeItem, 0, len(v.Items))
		for _, item := range v.Items {
			itemTemps, simpleValue := n.normalizeValue(fn, item.Value)
			temps = append(temps, itemTemps...)
			items = append(items, CompositeItem{Name: item.Name, Value: simpleValue})
		}
		copy := *v
		copy.Items = items
		return n.wrapComputed(fn, &copy, temps)
	case *InterfaceValue:
		temps, inner := n.normalizeValue(fn, v.Value)
		copy := *v
		copy.Value = inner
		copy.Methods = append([]InterfaceMethodLink(nil), v.Methods...)
		return n.wrapComputed(fn, &copy, temps)
	default:
		return nil, v
	}
}

func (n *normalizer) normalizeValueInline(fn *Function, value Value) ([]Instr, Value) {
	switch v := value.(type) {
	case nil, *NameValue, *LocalValue, *NumberValue, *BoolValue, *StringValue, *NoneValue:
		return nil, v
	case *UnaryValue:
		var temps []Instr
		var right Value
		if isVoidMIRType(v.Type()) && isVoidEffectWrapperOp(v.Op) {
			temps, right = n.normalizeValueInline(fn, v.Right)
		} else {
			temps, right = n.normalizeValue(fn, v.Right)
		}
		copy := *v
		copy.Right = right
		return temps, &copy
	case *AddrOfValue:
		temps, source := n.normalizeValue(fn, v.Source)
		copy := *v
		copy.Source = source
		return temps, &copy
	case *LoadValue:
		temps, ptr := n.normalizeValue(fn, v.Pointer)
		copy := *v
		copy.Pointer = ptr
		return temps, &copy
	case *BinaryValue:
		leftTemps, left := n.normalizeValue(fn, v.Left)
		rightTemps, right := n.normalizeValue(fn, v.Right)
		copy := *v
		copy.Left = left
		copy.Right = right
		return append(leftTemps, rightTemps...), &copy
	case *PostfixValue:
		temps, left := n.normalizeValue(fn, v.Left)
		copy := *v
		copy.Left = left
		return temps, &copy
	case *CallValue:
		calleeTemps, callee := n.normalizeValue(fn, v.Callee)
		temps := append([]Instr{}, calleeTemps...)
		args := make([]Value, 0, len(v.Args))
		for _, arg := range v.Args {
			argTemps, simpleArg := n.normalizeValue(fn, arg)
			temps = append(temps, argTemps...)
			args = append(args, simpleArg)
		}
		copy := *v
		copy.Callee = callee
		copy.Args = args
		return temps, &copy
	case *FieldLoadValue:
		temps, base := n.normalizeValue(fn, v.Base)
		copy := *v
		copy.Base = base
		return temps, &copy
	case *IndexValue:
		baseTemps, base := n.normalizeValue(fn, v.Base)
		indexTemps, index := n.normalizeValue(fn, v.Index)
		copy := *v
		copy.Base = base
		copy.Index = index
		return append(baseTemps, indexTemps...), &copy
	case *FieldValue:
		temps, base := n.normalizeValue(fn, v.Base)
		copy := *v
		copy.Base = base
		return temps, &copy
	case *CastValue:
		temps, left := n.normalizeValue(fn, v.Left)
		copy := *v
		copy.Left = left
		return temps, &copy
	case *TypeTestValue:
		temps, left := n.normalizeValue(fn, v.Left)
		copy := *v
		copy.Left = left
		return temps, &copy
	case *CompositeValue:
		temps := make([]Instr, 0, len(v.Items))
		items := make([]CompositeItem, 0, len(v.Items))
		for _, item := range v.Items {
			itemTemps, simpleValue := n.normalizeValue(fn, item.Value)
			temps = append(temps, itemTemps...)
			items = append(items, CompositeItem{Name: item.Name, Value: simpleValue})
		}
		copy := *v
		copy.Items = items
		return temps, &copy
	case *InterfaceValue:
		temps, inner := n.normalizeValue(fn, v.Value)
		copy := *v
		copy.Value = inner
		copy.Methods = append([]InterfaceMethodLink(nil), v.Methods...)
		return temps, &copy
	default:
		return nil, v
	}
}

func (n *normalizer) wrapComputed(fn *Function, value Value, temps []Instr) ([]Instr, Value) {
	temp := n.newTemp(fn, value)
	instr := &ComputeInstr{baseInstr: baseInstr{Location: value.Loc()}, TargetID: temp.LocalID, Type: value.Type(), Value: value}
	return append(temps, instr), temp
}

func (n *normalizer) normalizeVoidEffectInstr(fn *Function, loc source.Location, value Value) []Instr {
	effect, ok := unwrapVoidEffectValue(value)
	if !ok {
		return nil
	}
	temps, normalized := n.normalizeValueInline(fn, effect)
	return append(temps, &EvalInstr{baseInstr: baseInstr{Location: loc}, Value: normalized})
}

func normalizeLocalHasVoidType(fn *Function, id int) bool {
	if fn == nil || id < 0 || id >= len(fn.Locals) || fn.Locals[id] == nil {
		return false
	}
	return isVoidMIRType(fn.Locals[id].Type)
}

func isVoidMIRType(typ typeinfo.Type) bool {
	return typ != nil && typeinfo.IsBuiltinNamed(typ, "void")
}

func unwrapVoidEffectValue(value Value) (Value, bool) {
	switch v := value.(type) {
	case nil:
		return nil, false
	case *CallValue:
		return v, true
	case *UnaryValue:
		switch v.Op {
		case "comptime", "copy", "take", "unsafe", "?":
			return unwrapVoidEffectValue(v.Right)
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

func isVoidEffectWrapperOp(op string) bool {
	switch op {
	case "comptime", "copy", "take", "unsafe", "?":
		return true
	default:
		return false
	}
}

func (n *normalizer) newTemp(fn *Function, value Value) *LocalValue {
	id := n.tempCounter
	n.tempCounter++
	if fn != nil {
		fn.Locals = append(fn.Locals, &Local{
			ID:       id,
			Name:     fmt.Sprintf("_t%d", id),
			Type:     value.Type(),
			Mutable:  false,
			Constant: false,
			IsTemp:   true,
			Location: value.Loc(),
		})
	}
	return &LocalValue{baseValue: baseValue{Location: value.Loc(), ExprType: value.Type()}, LocalID: id}
}

func isSimpleValue(value Value) bool {
	switch value.(type) {
	case nil, *NameValue, *LocalValue, *NumberValue, *BoolValue, *StringValue, *NoneValue:
		return true
	default:
		return false
	}
}

func removeUnreachableBlocks(fn *Function) {
	if fn == nil || len(fn.Blocks) == 0 || fn.EntryID < 0 {
		return
	}
	reachable := make(map[int]struct{}, len(fn.Blocks))
	queue := []int{fn.EntryID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, ok := reachable[id]; ok {
			continue
		}
		reachable[id] = struct{}{}
		block := findBlock(fn.Blocks, id)
		if block == nil || block.Terminator == nil {
			continue
		}
		for _, target := range terminatorTargets(block.Terminator) {
			if _, ok := reachable[target]; !ok {
				queue = append(queue, target)
			}
		}
	}
	out := make([]*Block, 0, len(fn.Blocks))
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		if _, ok := reachable[block.ID]; ok {
			out = append(out, block)
		}
	}
	fn.Blocks = out
	if fn.ExitID >= 0 {
		if _, ok := reachable[fn.ExitID]; !ok {
			fn.ExitID = -1
		}
	}
}

func findBlock(blocks []*Block, id int) *Block {
	for _, block := range blocks {
		if block != nil && block.ID == id {
			return block
		}
	}
	return nil
}
