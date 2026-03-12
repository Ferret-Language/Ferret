package mir

import (
	"fmt"
	"sort"

	"compiler/internal/diagnostics"
	"compiler/internal/source"
)

func ValidateModule(bag *diagnostics.Bag, mod *Module) bool {
	if mod == nil {
		return true
	}
	ok := true
	for _, fn := range mod.Functions {
		if !validateFunction(bag, fn) {
			ok = false
		}
	}
	return ok
}

func validateFunction(bag *diagnostics.Bag, fn *Function) bool {
	if fn == nil {
		return true
	}
	if fn.Blocks == nil && fn.EntryID < 0 && fn.ExitID < 0 {
		return true
	}
	ok := true
	ids := make(map[int]*Block, len(fn.Blocks))
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		if _, exists := ids[block.ID]; exists {
			reportMIRValidation(bag, block.Location, fmt.Sprintf("duplicate MIR block id %d in function %s", block.ID, fn.Name))
			ok = false
			continue
		}
		ids[block.ID] = block
		if block.Terminator == nil {
			reportMIRValidation(bag, block.Location, fmt.Sprintf("missing terminator in MIR block %d", block.ID))
			ok = false
		}
		for _, instr := range block.Instructions {
			if instr == nil {
				reportMIRValidation(bag, block.Location, fmt.Sprintf("nil MIR instruction in block %d", block.ID))
				ok = false
				continue
			}
			if !validateInstrValueShape(bag, instr) {
				ok = false
			}
		}
		if !validateTerminatorValueShape(bag, block.Terminator) {
			ok = false
		}
	}
	if _, okEntry := ids[fn.EntryID]; !okEntry {
		reportMIRValidation(bag, fn.Location, fmt.Sprintf("missing MIR entry block %d in function %s", fn.EntryID, fn.Name))
		ok = false
	}
	if fn.ExitID >= 0 {
		if _, okExit := ids[fn.ExitID]; !okExit {
			reportMIRValidation(bag, fn.Location, fmt.Sprintf("missing MIR exit block %d in function %s", fn.ExitID, fn.Name))
			ok = false
		}
	}
	for _, block := range fn.Blocks {
		if block == nil || block.Terminator == nil {
			continue
		}
		for _, target := range terminatorTargets(block.Terminator) {
			if _, okTarget := ids[target]; !okTarget {
				reportMIRValidation(bag, block.Location, fmt.Sprintf("MIR block %d jumps to missing block %d", block.ID, target))
				ok = false
			}
		}
	}
	return ok
}

func validateInstrValueShape(bag *diagnostics.Bag, instr Instr) bool {
	switch i := instr.(type) {
	case *BindInstr:
		return requireSimpleValue(bag, i.Loc(), i.Value, "bind")
	case *AssignInstr:
		return requireNormalizedAssignable(bag, i.Loc(), i.Value, "assign")
	case *ComputeInstr:
		return requireNormalizedCompute(bag, i.Loc(), i.Value)
	case *StoreInstr:
		return requireNormalizedAssignable(bag, i.Loc(), i.Value, "store")
	case *StoreFieldInstr:
		ok := requireSimpleValue(bag, i.Loc(), i.Base, "store_field base")
		return requireSimpleValue(bag, i.Loc(), i.Value, "store_field") && ok
	case *EvalInstr:
		return requireSimpleValue(bag, i.Loc(), i.Value, "eval")
	case *DeferInstr:
		ok := true
		for _, child := range i.Body {
			if !validateInstrValueShape(bag, child) {
				ok = false
			}
		}
		return ok
	case *LockInstr:
		return requireSimpleValue(bag, i.Loc(), i.Value, "lock")
	default:
		return true
	}
}

func validateTerminatorValueShape(bag *diagnostics.Bag, term Terminator) bool {
	switch t := term.(type) {
	case *BranchTerm:
		return requireSimpleValue(bag, t.Loc(), t.Cond, "branch")
	case *SwitchTerm:
		ok := requireSimpleValue(bag, t.Loc(), t.Value, "switch")
		for _, kase := range t.Cases {
			if !requireSimpleValue(bag, t.Loc(), kase.Expr, "switch case") {
				ok = false
			}
		}
		return ok
	case *ReturnTerm:
		return requireSimpleValue(bag, t.Loc(), t.Value, "return")
	case *PanicTerm:
		return requireSimpleValue(bag, t.Loc(), t.Value, "panic")
	default:
		return true
	}
}

func requireSimpleValue(bag *diagnostics.Bag, loc source.Location, value Value, usage string) bool {
	if isSimpleValue(value) {
		return true
	}
	reportMIRValidation(bag, loc, fmt.Sprintf("non-simple MIR value in %s", usage))
	return false
}

func requireNormalizedCompute(bag *diagnostics.Bag, loc source.Location, value Value) bool {
	if value == nil || isSimpleValue(value) {
		reportMIRValidation(bag, loc, "compute instruction must hold a non-simple value")
		return false
	}
	if !childrenAreSimple(value) {
		reportMIRValidation(bag, loc, "compute instruction still contains nested complex values")
		return false
	}
	return true
}

func requireNormalizedAssignable(bag *diagnostics.Bag, loc source.Location, value Value, usage string) bool {
	if value == nil || isSimpleValue(value) {
		return true
	}
	if !childrenAreSimple(value) {
		reportMIRValidation(bag, loc, fmt.Sprintf("%s instruction still contains nested complex values", usage))
		return false
	}
	return true
}

func childrenAreSimple(value Value) bool {
	switch v := value.(type) {
	case *UnaryValue:
		return isSimpleValue(v.Right)
	case *AddrOfValue:
		return isSimpleValue(v.Source)
	case *LoadValue:
		return isSimpleValue(v.Pointer)
	case *BinaryValue:
		return isSimpleValue(v.Left) && isSimpleValue(v.Right)
	case *PostfixValue:
		return isSimpleValue(v.Left)
	case *CallValue:
		if !isSimpleValue(v.Callee) {
			return false
		}
		for _, arg := range v.Args {
			if !isSimpleValue(arg) {
				return false
			}
		}
		return true
	case *FieldLoadValue:
		return isSimpleValue(v.Base)
	case *FieldValue:
		return isSimpleValue(v.Base)
	case *IndexValue:
		return isSimpleValue(v.Base) && isSimpleValue(v.Index)
	case *CastValue:
		return isSimpleValue(v.Left)
	case *CompositeValue:
		for _, item := range v.Items {
			if !isSimpleValue(item.Value) {
				return false
			}
		}
		return true
	case *InterfaceValue:
		return isSimpleValue(v.Value)
	default:
		return false
	}
}

func terminatorTargets(term Terminator) []int {
	switch t := term.(type) {
	case *JumpTerm:
		return []int{t.TargetID}
	case *BranchTerm:
		return []int{t.TrueID, t.FalseID}
	case *SwitchTerm:
		out := make([]int, 0, len(t.Cases)+1)
		for _, kase := range t.Cases {
			out = append(out, kase.TargetID)
		}
		out = append(out, t.DefaultID)
		sort.Ints(out)
		return out
	case *PanicTerm:
		if t.CleanupID >= 0 {
			return []int{t.CleanupID}
		}
		return nil
	case *ReturnTerm:
		if t.CleanupID >= 0 {
			return []int{t.CleanupID}
		}
		return nil
	default:
		return nil
	}
}

func reportMIRValidation(bag *diagnostics.Bag, loc source.Location, message string) {
	if bag == nil {
		return
	}
	bag.Add(
		diagnostics.NewError(message).
			WithCode(diagnostics.ErrInvalidOperation).
			WithPrimaryLabel(&loc, "MIR validation failed"),
	)
}
