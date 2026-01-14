package mir

import (
	"compiler/internal/tokens"
	"compiler/internal/types"
)

// LowerSwitches rewrites switch terminators into explicit compare/jump chains.
func LowerSwitches(mod *Module) {
	if mod == nil {
		return
	}

	for _, fn := range mod.Functions {
		lowerSwitchesInFunction(fn)
	}
}

func lowerSwitchesInFunction(fn *Function) {
	if fn == nil {
		return
	}

	valueTypes := collectValueTypes(fn)
	nextValue := nextValueID(fn)
	nextBlock := nextBlockID(fn)

	for idx := 0; idx < len(fn.Blocks); idx++ {
		block := fn.Blocks[idx]
		if block == nil || block.Term == nil {
			continue
		}

		sw, ok := block.Term.(*Switch)
		if !ok {
			continue
		}

		if len(sw.Cases) == 0 {
			block.Term = &Br{Target: sw.Default, Location: sw.Location}
			continue
		}

		condType := valueTypes[sw.Cond]
		if condType == nil {
			block.Term = &Br{Target: sw.Default, Location: sw.Location}
			continue
		}

		current := block
		for caseIdx, entry := range sw.Cases {
			constID := nextValue
			nextValue++
			current.Instrs = append(current.Instrs, &Const{
				Result:   constID,
				Type:     condType,
				Value:    entry.Value,
				Location: sw.Location,
			})
			valueTypes[constID] = condType

			cmpID := nextValue
			nextValue++
			current.Instrs = append(current.Instrs, &Binary{
				Result:   cmpID,
				Op:       tokens.DOUBLE_EQUAL_TOKEN,
				Left:     sw.Cond,
				Right:    constID,
				Type:     condType,
				Location: sw.Location,
			})
			valueTypes[cmpID] = types.TypeBool

			if caseIdx < len(sw.Cases)-1 {
				checkBlock := &Block{
					ID:       nextBlock,
					Name:     "switch.check",
					Location: sw.Location,
				}
				nextBlock++
				fn.Blocks = append(fn.Blocks, checkBlock)

				current.Term = &CondBr{
					Cond:     cmpID,
					Then:     entry.Target,
					Else:     checkBlock.ID,
					Location: sw.Location,
				}
				current = checkBlock
			} else {
				current.Term = &CondBr{
					Cond:     cmpID,
					Then:     entry.Target,
					Else:     sw.Default,
					Location: sw.Location,
				}
			}
		}
	}
}

func collectValueTypes(fn *Function) map[ValueID]types.SemType {
	valueTypes := make(map[ValueID]types.SemType)

	for _, param := range fn.Params {
		valueTypes[param.ID] = param.Type
	}
	if fn.Receiver != nil {
		valueTypes[fn.Receiver.ID] = fn.Receiver.Type
	}

	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			switch i := instr.(type) {
			case *Const:
				valueTypes[i.Result] = i.Type
			case *Binary:
				if isCompareOp(i.Op) {
					valueTypes[i.Result] = types.TypeBool
				} else {
					valueTypes[i.Result] = i.Type
				}
			case *Unary:
				if i.Op == tokens.NOT_TOKEN {
					valueTypes[i.Result] = types.TypeBool
				} else {
					valueTypes[i.Result] = i.Type
				}
			case *Cast:
				valueTypes[i.Result] = i.Type
			case *Alloca:
				valueTypes[i.Result] = types.NewReference(i.Type)
			case *Load:
				valueTypes[i.Result] = i.Type
			case *PtrAdd:
				valueTypes[i.Result] = types.NewReference(i.Elem)
			case *PtrOffset:
				valueTypes[i.Result] = types.NewReference(i.Elem)
			case *Call:
				valueTypes[i.Result] = i.Type
			case *CallIndirect:
				valueTypes[i.Result] = i.Type
			case *Phi:
				valueTypes[i.Result] = i.Type
			case *ArrayGet:
				valueTypes[i.Result] = i.Type
			case *MapGet:
				valueTypes[i.Result] = i.Type
			case *OptionalNone:
				valueTypes[i.Result] = i.Type
			case *OptionalSome:
				valueTypes[i.Result] = i.Type
			case *OptionalIsSome:
				valueTypes[i.Result] = types.TypeBool
			case *OptionalUnwrap:
				valueTypes[i.Result] = i.Type
			case *UnionVariantCheck:
				valueTypes[i.Result] = types.TypeBool
			case *UnionExtract:
				valueTypes[i.Result] = i.Type
			case *ResultOk:
				valueTypes[i.Result] = i.Type
			case *ResultErr:
				valueTypes[i.Result] = i.Type
			case *ResultIsOk:
				valueTypes[i.Result] = types.TypeBool
			case *ResultUnwrap:
				valueTypes[i.Result] = i.Type
			}
		}
	}

	return valueTypes
}

func isCompareOp(op tokens.TOKEN) bool {
	switch op {
	case tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN,
		tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN,
		tokens.DOUBLE_EQUAL_TOKEN, tokens.NOT_EQUAL_TOKEN:
		return true
	default:
		return false
	}
}

func nextValueID(fn *Function) ValueID {
	var max ValueID
	for _, param := range fn.Params {
		if param.ID > max {
			max = param.ID
		}
	}
	if fn.Receiver != nil && fn.Receiver.ID > max {
		max = fn.Receiver.ID
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			if id := instrResultID(instr); id > max {
				max = id
			}
		}
	}
	return max + 1
}

func nextBlockID(fn *Function) BlockID {
	var max BlockID
	for _, block := range fn.Blocks {
		if block != nil && block.ID > max {
			max = block.ID
		}
	}
	return max + 1
}

func instrResultID(instr Instr) ValueID {
	switch i := instr.(type) {
	case *Const:
		return i.Result
	case *Binary:
		return i.Result
	case *Unary:
		return i.Result
	case *Cast:
		return i.Result
	case *Alloca:
		return i.Result
	case *Load:
		return i.Result
	case *PtrAdd:
		return i.Result
	case *PtrOffset:
		return i.Result
	case *Call:
		return i.Result
	case *CallIndirect:
		return i.Result
	case *Phi:
		return i.Result
	case *ArrayGet:
		return i.Result
	case *MapGet:
		return i.Result
	case *OptionalNone:
		return i.Result
	case *OptionalSome:
		return i.Result
	case *OptionalIsSome:
		return i.Result
	case *OptionalUnwrap:
		return i.Result
	case *UnionVariantCheck:
		return i.Result
	case *UnionExtract:
		return i.Result
	case *ResultOk:
		return i.Result
	case *ResultErr:
		return i.Result
	case *ResultIsOk:
		return i.Result
	case *ResultUnwrap:
		return i.Result
	default:
		return InvalidValue
	}
}
