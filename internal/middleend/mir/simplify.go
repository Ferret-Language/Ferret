package mir

import (
	"math/big"
	"slices"

	"compiler/internal/diagnostics"
	"compiler/internal/utils/numeric"
)

func SimplifyModule(diag *diagnostics.Bag, mod *Module) *Module {
	if mod == nil {
		return nil
	}
	for _, global := range mod.Globals {
		if global == nil {
			continue
		}
		global.Init = simplifyValue(global.Init, nil)
	}
	for _, fn := range mod.Functions {
		simplifyFunction(diag, fn)
	}
	return mod
}

func simplifyFunction(diag *diagnostics.Bag, fn *Function) {
	if fn == nil {
		return
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		consts := make(map[int]Value)
		out := make([]Instr, 0, len(block.Instructions))
		for _, instr := range block.Instructions {
			switch i := instr.(type) {
			case *AssignInstr:
				i.Value = simplifyValue(i.Value, consts)
				updateConstBinding(consts, i.TargetID, i.Value)
				out = append(out, i)
			case *ComputeInstr:
				i.Value = simplifyValue(i.Value, consts)
				if isSimpleValue(i.Value) {
					assign := &AssignInstr{
						baseInstr: baseInstr{Location: i.Location},
						TargetID:  i.TargetID,
						Value:     i.Value,
					}
					updateConstBinding(consts, i.TargetID, i.Value)
					out = append(out, assign)
					continue
				}
				delete(consts, i.TargetID)
				out = append(out, i)
			case *StoreInstr:
				i.Value = simplifyValue(i.Value, consts)
				out = append(out, i)
			case *StoreFieldInstr:
				i.Base = simplifyValue(i.Base, consts)
				i.Value = simplifyValue(i.Value, consts)
				out = append(out, i)
			case *EvalInstr:
				i.Value = simplifyValue(i.Value, consts)
				out = append(out, i)
			case *BindInstr:
				i.Value = simplifyValue(i.Value, consts)
				out = append(out, i)
			case *LockInstr:
				i.Value = simplifyValue(i.Value, consts)
				out = append(out, i)
			case *DeferInstr:
				for j, child := range i.Body {
					i.Body[j] = simplifyDeferredInstr(child)
				}
				out = append(out, i)
			default:
				out = append(out, instr)
			}
		}
		block.Instructions = out
		block.Terminator = simplifyTerminator(diag, block.Terminator, consts)
		propagateTempCopies(fn, block)
		eliminateDeadTempAssigns(fn, block)
	}
	removeUnreachableBlocks(fn)
	renumberBlocks(fn)
	pruneUnusedTempLocals(fn)
}

func propagateTempCopies(fn *Function, block *Block) {
	if fn == nil || block == nil || len(block.Instructions) == 0 {
		return
	}
	assignCount := make(map[int]int)
	useCount := make(map[int]int)
	for _, instr := range block.Instructions {
		countAssignedLocals(assignCount, instr)
		countUsedLocalsInInstr(useCount, instr)
	}
	countUsedLocalsInTerminator(useCount, block.Terminator)

	out := make([]Instr, 0, len(block.Instructions))
	for idx, instr := range block.Instructions {
		assign, ok := instr.(*AssignInstr)
		if !ok {
			out = append(out, instr)
			continue
		}
		local := lookupLocal(fn, assign.TargetID)
		if local == nil || !local.IsTemp || assignCount[assign.TargetID] != 1 || useCount[assign.TargetID] == 0 || !isSimpleValue(assign.Value) {
			out = append(out, instr)
			continue
		}
		for j := idx + 1; j < len(block.Instructions); j++ {
			replaceLocalInInstr(block.Instructions[j], assign.TargetID, assign.Value)
		}
		replaceLocalInTerminator(block.Terminator, assign.TargetID, assign.Value)
	}
	block.Instructions = out
}

func eliminateDeadTempAssigns(fn *Function, block *Block) {
	if fn == nil || block == nil || len(block.Instructions) == 0 {
		return
	}
	live := usedLocalsInTerminator(block.Terminator)
	out := make([]Instr, 0, len(block.Instructions))
	for i := len(block.Instructions) - 1; i >= 0; i-- {
		instr := block.Instructions[i]
		drop := false
		switch ins := instr.(type) {
		case *AssignInstr:
			if local := lookupLocal(fn, ins.TargetID); local != nil && local.IsTemp && !live[ins.TargetID] && isSimpleValue(ins.Value) {
				drop = true
			}
			delete(live, ins.TargetID)
			addUsedLocalsFromValue(live, ins.Value)
		case *ComputeInstr:
			delete(live, ins.TargetID)
			addUsedLocalsFromValue(live, ins.Value)
		case *StoreInstr:
			addUsedLocalsFromPlace(live, ins.Target)
			addUsedLocalsFromValue(live, ins.Value)
		case *StoreFieldInstr:
			addUsedLocalsFromValue(live, ins.Base)
			addUsedLocalsFromValue(live, ins.Value)
		case *EvalInstr:
			addUsedLocalsFromValue(live, ins.Value)
		case *BindInstr:
			addUsedLocalsFromValue(live, ins.Value)
		case *LockInstr:
			delete(live, ins.LocalID)
			addUsedLocalsFromValue(live, ins.Value)
		case *DeferInstr:
			for _, child := range ins.Body {
				addUsedLocalsFromInstr(live, child)
			}
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

func simplifyDeferredInstr(instr Instr) Instr {
	switch i := instr.(type) {
	case nil:
		return nil
	case *AssignInstr:
		i.Value = simplifyValue(i.Value, nil)
	case *ComputeInstr:
		i.Value = simplifyValue(i.Value, nil)
		if isSimpleValue(i.Value) {
			return &AssignInstr{baseInstr: baseInstr{Location: i.Location}, TargetID: i.TargetID, Value: i.Value}
		}
	case *StoreInstr:
		i.Value = simplifyValue(i.Value, nil)
	case *StoreFieldInstr:
		i.Base = simplifyValue(i.Base, nil)
		i.Value = simplifyValue(i.Value, nil)
	case *EvalInstr:
		i.Value = simplifyValue(i.Value, nil)
	case *BindInstr:
		i.Value = simplifyValue(i.Value, nil)
	case *LockInstr:
		i.Value = simplifyValue(i.Value, nil)
	case *DeferInstr:
		for j, child := range i.Body {
			i.Body[j] = simplifyDeferredInstr(child)
		}
	}
	return instr
}

func simplifyTerminator(diag *diagnostics.Bag, term Terminator, consts map[int]Value) Terminator {
	switch t := term.(type) {
	case nil:
		return nil
	case *BranchTerm:
		t.Cond = simplifyValue(t.Cond, consts)
		if b, ok := boolConstant(t.Cond); ok {
			reportConstantCondition(diag, t.Cond, b)
			target := t.FalseID
			if b {
				target = t.TrueID
			}
			return &JumpTerm{baseTerm: baseTerm{Location: t.Location}, TargetID: target}
		}
		return t
	case *SwitchTerm:
		t.Value = simplifyValue(t.Value, consts)
		for i, kase := range t.Cases {
			t.Cases[i].Expr = simplifyValue(kase.Expr, consts)
		}
		for _, kase := range t.Cases {
			if simpleEqual(t.Value, kase.Expr) {
				return &JumpTerm{baseTerm: baseTerm{Location: t.Location}, TargetID: kase.TargetID}
			}
		}
		return t
	case *ReturnTerm:
		t.Value = simplifyValue(t.Value, consts)
		return t
	case *PanicTerm:
		t.Value = simplifyValue(t.Value, consts)
		return t
	default:
		return term
	}
}

func reportConstantCondition(diag *diagnostics.Bag, value Value, truth bool) {
	if diag == nil || value == nil {
		return
	}
	loc := value.Loc()
	msg := "condition is always false"
	code := diagnostics.WarnConstantConditionFalse
	label := "this condition always evaluates to false"
	if truth {
		msg = "condition is always true"
		code = diagnostics.WarnConstantConditionTrue
		label = "this condition always evaluates to true"
	}
	diag.Add(
		diagnostics.NewWarning(msg).
			WithCode(code).
			WithPrimaryLabel(&loc, label),
	)
}

func simplifyValue(value Value, consts map[int]Value) Value {
	switch v := value.(type) {
	case nil:
		return nil
	case *BoolValue, *NumberValue, *StringValue, *NoneValue:
		return v
	case *NameValue:
		return v
	case *LocalValue:
		if consts != nil {
			if replacement, ok := consts[v.LocalID]; ok && replacement != nil {
				return replacement
			}
		}
		return v
	case *TempValue:
		return v
	case *UnaryValue:
		v.Right = simplifyValue(v.Right, consts)
		return foldUnary(v)
	case *BinaryValue:
		v.Left = simplifyValue(v.Left, consts)
		v.Right = simplifyValue(v.Right, consts)
		return foldBinary(v)
	case *PostfixValue:
		v.Left = simplifyValue(v.Left, consts)
		return v
	case *AddrOfValue:
		v.Source = simplifyValue(v.Source, consts)
		return v
	case *LoadValue:
		v.Pointer = simplifyValue(v.Pointer, consts)
		return v
	case *CallValue:
		v.Callee = simplifyValue(v.Callee, consts)
		for i, arg := range v.Args {
			v.Args[i] = simplifyValue(arg, consts)
		}
		return v
	case *FieldLoadValue:
		v.Base = simplifyValue(v.Base, consts)
		return v
	case *FieldValue:
		v.Base = simplifyValue(v.Base, consts)
		return v
	case *CastValue:
		v.Left = simplifyValue(v.Left, consts)
		return v
	case *CompositeValue:
		for i, item := range v.Items {
			v.Items[i].Value = simplifyValue(item.Value, consts)
		}
		return v
	default:
		return value
	}
}

func updateConstBinding(consts map[int]Value, localID int, value Value) {
	if consts == nil {
		return
	}
	if isFoldedConst(value) {
		consts[localID] = value
		return
	}
	delete(consts, localID)
}

func isFoldedConst(value Value) bool {
	switch value.(type) {
	case *BoolValue, *NumberValue, *StringValue, *NoneValue:
		return true
	default:
		return false
	}
}

func boolConstant(value Value) (bool, bool) {
	switch v := value.(type) {
	case *BoolValue:
		return v.Value, true
	}
	return false, false
}

func foldUnary(v *UnaryValue) Value {
	if v == nil {
		return nil
	}
	switch v.Op {
	case "!":
		if b, ok := boolConstant(v.Right); ok {
			return &BoolValue{baseValue: baseValue{Location: v.Location, ExprType: v.ExprType}, Value: !b}
		}
	case "-":
		if n, ok := v.Right.(*NumberValue); ok {
			if i, ok := parseBigInt(n.Value); ok {
				i.Neg(i)
				return &NumberValue{baseValue: baseValue{Location: v.Location, ExprType: v.ExprType}, Value: i.String()}
			}
		}
	}
	return v
}

func foldBinary(v *BinaryValue) Value {
	if v == nil {
		return nil
	}
	if left, right, ok := intsOf(v.Left, v.Right); ok {
		switch v.Op {
		case "+":
			return intValue(v, new(big.Int).Add(left, right))
		case "-":
			return intValue(v, new(big.Int).Sub(left, right))
		case "*":
			return intValue(v, new(big.Int).Mul(left, right))
		case "/":
			if right.Sign() != 0 {
				return intValue(v, new(big.Int).Quo(left, right))
			}
		case "%":
			if right.Sign() != 0 {
				return intValue(v, new(big.Int).Rem(left, right))
			}
		case "==":
			return boolValue(v, left.Cmp(right) == 0)
		case "!=":
			return boolValue(v, left.Cmp(right) != 0)
		case "<":
			return boolValue(v, left.Cmp(right) < 0)
		case "<=":
			return boolValue(v, left.Cmp(right) <= 0)
		case ">":
			return boolValue(v, left.Cmp(right) > 0)
		case ">=":
			return boolValue(v, left.Cmp(right) >= 0)
		}
	}
	if lb, lok := boolConstant(v.Left); lok {
		if rb, rok := boolConstant(v.Right); rok {
			switch v.Op {
			case "&&":
				return boolValue(v, lb && rb)
			case "||":
				return boolValue(v, lb || rb)
			case "==":
				return boolValue(v, lb == rb)
			case "!=":
				return boolValue(v, lb != rb)
			}
		}
	}
	if ls, ok := v.Left.(*StringValue); ok {
		if rs, ok := v.Right.(*StringValue); ok {
			switch v.Op {
			case "+":
				return &StringValue{baseValue: baseValue{Location: v.Location, ExprType: v.ExprType}, Value: ls.Value + rs.Value}
			case "==":
				return boolValue(v, ls.Value == rs.Value)
			case "!=":
				return boolValue(v, ls.Value != rs.Value)
			}
		}
	}
	if _, lok := v.Left.(*NoneValue); lok {
		if _, rok := v.Right.(*NoneValue); rok {
			switch v.Op {
			case "==":
				return boolValue(v, true)
			case "!=":
				return boolValue(v, false)
			}
		}
	}
	return v
}

func intsOf(left, right Value) (*big.Int, *big.Int, bool) {
	li, lok := intOf(left)
	ri, rok := intOf(right)
	if !lok || !rok {
		return nil, nil, false
	}
	return li, ri, true
}

func intOf(value Value) (*big.Int, bool) {
	n, ok := value.(*NumberValue)
	if !ok {
		return nil, false
	}
	return parseBigInt(n.Value)
}

func parseBigInt(raw string) (*big.Int, bool) {
	if raw == "" {
		return nil, false
	}
	if slices.ContainsFunc([]string{".", "e", "E", "i"}, func(s string) bool { return contains(raw, s) }) {
		return nil, false
	}
	v, err := numeric.StringToBigInt(raw)
	return v, err == nil
}

func contains(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func intValue(src *BinaryValue, v *big.Int) Value {
	return &NumberValue{baseValue: baseValue{Location: src.Location, ExprType: src.ExprType}, Value: v.String()}
}

func boolValue(src *BinaryValue, v bool) Value {
	return &BoolValue{baseValue: baseValue{Location: src.Location, ExprType: src.ExprType}, Value: v}
}

func simpleEqual(left, right Value) bool {
	switch l := left.(type) {
	case *BoolValue:
		if r, ok := right.(*BoolValue); ok {
			return l.Value == r.Value
		}
	case *NumberValue:
		if r, ok := right.(*NumberValue); ok {
			li, lok := parseBigInt(l.Value)
			ri, rok := parseBigInt(r.Value)
			return lok && rok && li.Cmp(ri) == 0
		}
	case *StringValue:
		if r, ok := right.(*StringValue); ok {
			return l.Value == r.Value
		}
	case *NoneValue:
		_, ok := right.(*NoneValue)
		return ok
	}
	return false
}

func renumberBlocks(fn *Function) {
	if fn == nil {
		return
	}
	idMap := make(map[int]int, len(fn.Blocks))
	next := 1
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		if block.ID == fn.EntryID {
			idMap[block.ID] = 0
			continue
		}
		idMap[block.ID] = next
		next++
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		block.ID = idMap[block.ID]
		switch t := block.Terminator.(type) {
		case *JumpTerm:
			t.TargetID = idMap[t.TargetID]
		case *BranchTerm:
			t.TrueID = idMap[t.TrueID]
			t.FalseID = idMap[t.FalseID]
		case *SwitchTerm:
			t.DefaultID = idMap[t.DefaultID]
			for i := range t.Cases {
				t.Cases[i].TargetID = idMap[t.Cases[i].TargetID]
			}
		case *ReturnTerm:
			if t.CleanupID >= 0 {
				t.CleanupID = idMap[t.CleanupID]
			}
		case *PanicTerm:
			if t.CleanupID >= 0 {
				t.CleanupID = idMap[t.CleanupID]
			}
		}
	}
	if fn.EntryID >= 0 {
		fn.EntryID = idMap[fn.EntryID]
	}
	if fn.ExitID >= 0 {
		if mapped, ok := idMap[fn.ExitID]; ok {
			fn.ExitID = mapped
		} else {
			fn.ExitID = -1
		}
	}
}

func pruneUnusedTempLocals(fn *Function) {
	if fn == nil || len(fn.Locals) == 0 {
		return
	}
	used := make(map[int]bool)
	for _, param := range fn.Params {
		if param != nil && param.LocalID >= 0 {
			used[param.LocalID] = true
		}
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instructions {
			markUsedLocalsInInstr(used, instr)
		}
		markUsedLocalsInTerminator(used, block.Terminator)
	}
	oldToNew := make(map[int]int, len(fn.Locals))
	locals := make([]*Local, 0, len(fn.Locals))
	for _, local := range fn.Locals {
		if local == nil {
			continue
		}
		if local.IsTemp && !used[local.ID] {
			continue
		}
		oldToNew[local.ID] = len(locals)
		copyLocal := *local
		copyLocal.ID = len(locals)
		locals = append(locals, &copyLocal)
	}
	if len(locals) == len(fn.Locals) {
		return
	}
	fn.Locals = locals
	for _, param := range fn.Params {
		if param != nil && param.LocalID >= 0 {
			param.LocalID = oldToNew[param.LocalID]
		}
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instructions {
			remapInstrLocals(instr, oldToNew)
		}
		remapTerminatorLocals(block.Terminator, oldToNew)
	}
}

func markUsedLocalsInInstr(dst map[int]bool, instr Instr) {
	switch i := instr.(type) {
	case *AssignInstr:
		dst[i.TargetID] = true
		addUsedLocalsFromValue(dst, i.Value)
	case *ComputeInstr:
		dst[i.TargetID] = true
		addUsedLocalsFromValue(dst, i.Value)
	case *StoreInstr:
		addUsedLocalsFromPlace(dst, i.Target)
		addUsedLocalsFromValue(dst, i.Value)
	case *StoreFieldInstr:
		addUsedLocalsFromValue(dst, i.Base)
		addUsedLocalsFromValue(dst, i.Value)
	case *EvalInstr:
		addUsedLocalsFromValue(dst, i.Value)
	case *BindInstr:
		addUsedLocalsFromValue(dst, i.Value)
	case *LockInstr:
		dst[i.LocalID] = true
		addUsedLocalsFromValue(dst, i.Value)
	case *DeferInstr:
		for _, child := range i.Body {
			markUsedLocalsInInstr(dst, child)
		}
	}
}

func markUsedLocalsInTerminator(dst map[int]bool, term Terminator) {
	for id := range usedLocalsInTerminator(term) {
		dst[id] = true
	}
}

func remapInstrLocals(instr Instr, idMap map[int]int) {
	switch i := instr.(type) {
	case *AssignInstr:
		i.TargetID = idMap[i.TargetID]
		remapValueLocals(i.Value, idMap)
	case *ComputeInstr:
		i.TargetID = idMap[i.TargetID]
		remapValueLocals(i.Value, idMap)
	case *StoreInstr:
		remapPlaceLocals(i.Target, idMap)
		remapValueLocals(i.Value, idMap)
	case *StoreFieldInstr:
		remapValueLocals(i.Base, idMap)
		remapValueLocals(i.Value, idMap)
	case *EvalInstr:
		remapValueLocals(i.Value, idMap)
	case *BindInstr:
		remapValueLocals(i.Value, idMap)
	case *LockInstr:
		i.LocalID = idMap[i.LocalID]
		remapValueLocals(i.Value, idMap)
	case *DeferInstr:
		for _, child := range i.Body {
			remapInstrLocals(child, idMap)
		}
	}
}

func remapTerminatorLocals(term Terminator, idMap map[int]int) {
	switch t := term.(type) {
	case *BranchTerm:
		remapValueLocals(t.Cond, idMap)
	case *SwitchTerm:
		remapValueLocals(t.Value, idMap)
		for i := range t.Cases {
			remapValueLocals(t.Cases[i].Expr, idMap)
		}
	case *ReturnTerm:
		remapValueLocals(t.Value, idMap)
	case *PanicTerm:
		remapValueLocals(t.Value, idMap)
	}
}

func remapPlaceLocals(place Place, idMap map[int]int) {
	switch p := place.(type) {
	case *LocalPlace:
		p.LocalID = idMap[p.LocalID]
	case *FieldPlace:
		remapPlaceLocals(p.Base, idMap)
	case *IndexPlace:
		remapPlaceLocals(p.Base, idMap)
		remapValueLocals(p.Index, idMap)
	}
}

func remapValueLocals(value Value, idMap map[int]int) {
	switch v := value.(type) {
	case nil:
		return
	case *LocalValue:
		v.LocalID = idMap[v.LocalID]
	case *UnaryValue:
		remapValueLocals(v.Right, idMap)
	case *BinaryValue:
		remapValueLocals(v.Left, idMap)
		remapValueLocals(v.Right, idMap)
	case *PostfixValue:
		remapValueLocals(v.Left, idMap)
	case *AddrOfValue:
		remapValueLocals(v.Source, idMap)
	case *LoadValue:
		remapValueLocals(v.Pointer, idMap)
	case *CallValue:
		remapValueLocals(v.Callee, idMap)
		for _, arg := range v.Args {
			remapValueLocals(arg, idMap)
		}
	case *FieldLoadValue:
		remapValueLocals(v.Base, idMap)
	case *FieldValue:
		remapValueLocals(v.Base, idMap)
	case *CastValue:
		remapValueLocals(v.Left, idMap)
	case *CompositeValue:
		for _, item := range v.Items {
			remapValueLocals(item.Value, idMap)
		}
	case *IndexValue:
		remapValueLocals(v.Base, idMap)
		remapValueLocals(v.Index, idMap)
	}
}

func lookupLocal(fn *Function, id int) *Local {
	if fn == nil || id < 0 || id >= len(fn.Locals) {
		return nil
	}
	return fn.Locals[id]
}

func countAssignedLocals(dst map[int]int, instr Instr) {
	switch i := instr.(type) {
	case *AssignInstr:
		dst[i.TargetID]++
	case *ComputeInstr:
		dst[i.TargetID]++
	case *LockInstr:
		dst[i.LocalID]++
	}
}

func usedLocalsInTerminator(term Terminator) map[int]bool {
	out := make(map[int]bool)
	switch t := term.(type) {
	case *BranchTerm:
		addUsedLocalsFromValue(out, t.Cond)
	case *SwitchTerm:
		addUsedLocalsFromValue(out, t.Value)
		for _, kase := range t.Cases {
			addUsedLocalsFromValue(out, kase.Expr)
		}
	case *ReturnTerm:
		addUsedLocalsFromValue(out, t.Value)
	case *PanicTerm:
		addUsedLocalsFromValue(out, t.Value)
	}
	return out
}

func countUsedLocalsInInstr(dst map[int]int, instr Instr) {
	switch i := instr.(type) {
	case *AssignInstr:
		countUsedLocalsInValue(dst, i.Value)
	case *ComputeInstr:
		countUsedLocalsInValue(dst, i.Value)
	case *StoreInstr:
		countUsedLocalsInPlace(dst, i.Target)
		countUsedLocalsInValue(dst, i.Value)
	case *StoreFieldInstr:
		countUsedLocalsInValue(dst, i.Base)
		countUsedLocalsInValue(dst, i.Value)
	case *EvalInstr:
		countUsedLocalsInValue(dst, i.Value)
	case *BindInstr:
		countUsedLocalsInValue(dst, i.Value)
	case *LockInstr:
		countUsedLocalsInValue(dst, i.Value)
	case *DeferInstr:
		for _, child := range i.Body {
			countUsedLocalsInInstr(dst, child)
		}
	}
}

func countUsedLocalsInTerminator(dst map[int]int, term Terminator) {
	switch t := term.(type) {
	case *BranchTerm:
		countUsedLocalsInValue(dst, t.Cond)
	case *SwitchTerm:
		countUsedLocalsInValue(dst, t.Value)
		for _, kase := range t.Cases {
			countUsedLocalsInValue(dst, kase.Expr)
		}
	case *ReturnTerm:
		countUsedLocalsInValue(dst, t.Value)
	case *PanicTerm:
		countUsedLocalsInValue(dst, t.Value)
	}
}

func addUsedLocalsFromInstr(dst map[int]bool, instr Instr) {
	switch i := instr.(type) {
	case *AssignInstr:
		addUsedLocalsFromValue(dst, i.Value)
	case *ComputeInstr:
		addUsedLocalsFromValue(dst, i.Value)
	case *StoreInstr:
		addUsedLocalsFromPlace(dst, i.Target)
		addUsedLocalsFromValue(dst, i.Value)
	case *StoreFieldInstr:
		addUsedLocalsFromValue(dst, i.Base)
		addUsedLocalsFromValue(dst, i.Value)
	case *EvalInstr:
		addUsedLocalsFromValue(dst, i.Value)
	case *BindInstr:
		addUsedLocalsFromValue(dst, i.Value)
	case *LockInstr:
		addUsedLocalsFromValue(dst, i.Value)
	case *DeferInstr:
		for _, child := range i.Body {
			addUsedLocalsFromInstr(dst, child)
		}
	}
}

func addUsedLocalsFromPlace(dst map[int]bool, place Place) {
	switch p := place.(type) {
	case *LocalPlace:
		dst[p.LocalID] = true
	case *FieldPlace:
		addUsedLocalsFromPlace(dst, p.Base)
	case *IndexPlace:
		addUsedLocalsFromPlace(dst, p.Base)
		addUsedLocalsFromValue(dst, p.Index)
	}
}

func countUsedLocalsInPlace(dst map[int]int, place Place) {
	switch p := place.(type) {
	case *LocalPlace:
		dst[p.LocalID]++
	case *FieldPlace:
		countUsedLocalsInPlace(dst, p.Base)
	case *IndexPlace:
		countUsedLocalsInPlace(dst, p.Base)
		countUsedLocalsInValue(dst, p.Index)
	}
}

func addUsedLocalsFromValue(dst map[int]bool, value Value) {
	switch v := value.(type) {
	case nil:
		return
	case *LocalValue:
		dst[v.LocalID] = true
	case *UnaryValue:
		addUsedLocalsFromValue(dst, v.Right)
	case *BinaryValue:
		addUsedLocalsFromValue(dst, v.Left)
		addUsedLocalsFromValue(dst, v.Right)
	case *PostfixValue:
		addUsedLocalsFromValue(dst, v.Left)
	case *AddrOfValue:
		addUsedLocalsFromValue(dst, v.Source)
	case *LoadValue:
		addUsedLocalsFromValue(dst, v.Pointer)
	case *CallValue:
		addUsedLocalsFromValue(dst, v.Callee)
		for _, arg := range v.Args {
			addUsedLocalsFromValue(dst, arg)
		}
	case *FieldLoadValue:
		addUsedLocalsFromValue(dst, v.Base)
	case *FieldValue:
		addUsedLocalsFromValue(dst, v.Base)
	case *CastValue:
		addUsedLocalsFromValue(dst, v.Left)
	case *CompositeValue:
		for _, item := range v.Items {
			addUsedLocalsFromValue(dst, item.Value)
		}
	case *IndexValue:
		addUsedLocalsFromValue(dst, v.Base)
		addUsedLocalsFromValue(dst, v.Index)
	}
}

func countUsedLocalsInValue(dst map[int]int, value Value) {
	switch v := value.(type) {
	case nil:
		return
	case *LocalValue:
		dst[v.LocalID]++
	case *UnaryValue:
		countUsedLocalsInValue(dst, v.Right)
	case *BinaryValue:
		countUsedLocalsInValue(dst, v.Left)
		countUsedLocalsInValue(dst, v.Right)
	case *PostfixValue:
		countUsedLocalsInValue(dst, v.Left)
	case *AddrOfValue:
		countUsedLocalsInValue(dst, v.Source)
	case *LoadValue:
		countUsedLocalsInValue(dst, v.Pointer)
	case *CallValue:
		countUsedLocalsInValue(dst, v.Callee)
		for _, arg := range v.Args {
			countUsedLocalsInValue(dst, arg)
		}
	case *FieldLoadValue:
		countUsedLocalsInValue(dst, v.Base)
	case *FieldValue:
		countUsedLocalsInValue(dst, v.Base)
	case *CastValue:
		countUsedLocalsInValue(dst, v.Left)
	case *CompositeValue:
		for _, item := range v.Items {
			countUsedLocalsInValue(dst, item.Value)
		}
	case *IndexValue:
		countUsedLocalsInValue(dst, v.Base)
		countUsedLocalsInValue(dst, v.Index)
	}
}

func replaceLocalInInstr(instr Instr, localID int, replacement Value) {
	switch i := instr.(type) {
	case *AssignInstr:
		i.Value = replaceLocalInValue(i.Value, localID, replacement)
	case *ComputeInstr:
		i.Value = replaceLocalInValue(i.Value, localID, replacement)
	case *StoreInstr:
		i.Target = replaceLocalInPlace(i.Target, localID, replacement)
		i.Value = replaceLocalInValue(i.Value, localID, replacement)
	case *StoreFieldInstr:
		i.Base = replaceLocalInValue(i.Base, localID, replacement)
		i.Value = replaceLocalInValue(i.Value, localID, replacement)
	case *EvalInstr:
		i.Value = replaceLocalInValue(i.Value, localID, replacement)
	case *BindInstr:
		i.Value = replaceLocalInValue(i.Value, localID, replacement)
	case *LockInstr:
		i.Value = replaceLocalInValue(i.Value, localID, replacement)
	case *DeferInstr:
		for _, child := range i.Body {
			replaceLocalInInstr(child, localID, replacement)
		}
	}
}

func replaceLocalInTerminator(term Terminator, localID int, replacement Value) {
	switch t := term.(type) {
	case *BranchTerm:
		t.Cond = replaceLocalInValue(t.Cond, localID, replacement)
	case *SwitchTerm:
		t.Value = replaceLocalInValue(t.Value, localID, replacement)
		for i := range t.Cases {
			t.Cases[i].Expr = replaceLocalInValue(t.Cases[i].Expr, localID, replacement)
		}
	case *ReturnTerm:
		t.Value = replaceLocalInValue(t.Value, localID, replacement)
	case *PanicTerm:
		t.Value = replaceLocalInValue(t.Value, localID, replacement)
	}
}

func replaceLocalInPlace(place Place, localID int, replacement Value) Place {
	switch p := place.(type) {
	case *LocalPlace:
		if p.LocalID == localID {
			if repl, ok := replacement.(*LocalValue); ok {
				return &LocalPlace{basePlace: p.basePlace, LocalID: repl.LocalID}
			}
		}
		return p
	case *FieldPlace:
		p.Base = replaceLocalInPlace(p.Base, localID, replacement)
		return p
	case *IndexPlace:
		p.Base = replaceLocalInPlace(p.Base, localID, replacement)
		p.Index = replaceLocalInValue(p.Index, localID, replacement)
		return p
	default:
		return place
	}
}

func replaceLocalInValue(value Value, localID int, replacement Value) Value {
	switch v := value.(type) {
	case nil:
		return nil
	case *LocalValue:
		if v.LocalID == localID {
			return cloneSimpleValue(replacement)
		}
		return v
	case *UnaryValue:
		v.Right = replaceLocalInValue(v.Right, localID, replacement)
		return v
	case *BinaryValue:
		v.Left = replaceLocalInValue(v.Left, localID, replacement)
		v.Right = replaceLocalInValue(v.Right, localID, replacement)
		return v
	case *PostfixValue:
		v.Left = replaceLocalInValue(v.Left, localID, replacement)
		return v
	case *AddrOfValue:
		v.Source = replaceLocalInValue(v.Source, localID, replacement)
		return v
	case *LoadValue:
		v.Pointer = replaceLocalInValue(v.Pointer, localID, replacement)
		return v
	case *CallValue:
		v.Callee = replaceLocalInValue(v.Callee, localID, replacement)
		for i, arg := range v.Args {
			v.Args[i] = replaceLocalInValue(arg, localID, replacement)
		}
		return v
	case *FieldLoadValue:
		v.Base = replaceLocalInValue(v.Base, localID, replacement)
		return v
	case *FieldValue:
		v.Base = replaceLocalInValue(v.Base, localID, replacement)
		return v
	case *CastValue:
		v.Left = replaceLocalInValue(v.Left, localID, replacement)
		return v
	case *CompositeValue:
		for i, item := range v.Items {
			v.Items[i].Value = replaceLocalInValue(item.Value, localID, replacement)
		}
		return v
	case *IndexValue:
		v.Base = replaceLocalInValue(v.Base, localID, replacement)
		v.Index = replaceLocalInValue(v.Index, localID, replacement)
		return v
	default:
		return value
	}
}

func cloneSimpleValue(value Value) Value {
	switch v := value.(type) {
	case *LocalValue:
		copy := *v
		return &copy
	case *NameValue:
		copy := *v
		copy.Path = append([]string(nil), v.Path...)
		return &copy
	case *NumberValue:
		copy := *v
		return &copy
	case *BoolValue:
		copy := *v
		return &copy
	case *StringValue:
		copy := *v
		return &copy
	case *NoneValue:
		copy := *v
		return &copy
	default:
		return value
	}
}
