package mir

// WalkModuleValues visits every value reachable from globals and function bodies.
func WalkModuleValues(mod *Module, visit func(Value) error) error {
	if mod == nil {
		return nil
	}
	for _, global := range mod.Globals {
		if global == nil {
			continue
		}
		if err := WalkValue(global.Init, visit); err != nil {
			return err
		}
	}
	for _, fn := range mod.Functions {
		if err := WalkFunctionValues(fn, visit); err != nil {
			return err
		}
	}
	return nil
}

func WalkFunctionValues(fn *Function, visit func(Value) error) error {
	if fn == nil {
		return nil
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instructions {
			if err := WalkInstrValues(instr, visit); err != nil {
				return err
			}
		}
		if err := WalkTerminatorValues(block.Terminator, visit); err != nil {
			return err
		}
	}
	return nil
}

func WalkInstrValues(instr Instr, visit func(Value) error) error {
	switch i := instr.(type) {
	case nil, *UnsafeInstr:
		return nil
	case *BindInstr:
		return WalkValue(i.Value, visit)
	case *AssignInstr:
		return WalkValue(i.Value, visit)
	case *ComputeInstr:
		return WalkValue(i.Value, visit)
	case *StoreInstr:
		if err := WalkPlaceValues(i.Target, visit); err != nil {
			return err
		}
		return WalkValue(i.Value, visit)
	case *AtomicAddInstr:
		if err := WalkValue(i.Pointer, visit); err != nil {
			return err
		}
		return WalkValue(i.Delta, visit)
	case *StoreFieldInstr:
		if err := WalkValue(i.Base, visit); err != nil {
			return err
		}
		return WalkValue(i.Value, visit)
	case *EvalInstr:
		return WalkValue(i.Value, visit)
	case *LockInstr:
		return WalkValue(i.Value, visit)
	case *DeferInstr:
		for _, child := range i.Body {
			if err := WalkInstrValues(child, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func WalkTerminatorValues(term Terminator, visit func(Value) error) error {
	switch t := term.(type) {
	case nil, *JumpTerm, *ExitTerm:
		return nil
	case *BranchTerm:
		return WalkValue(t.Cond, visit)
	case *SwitchTerm:
		if err := WalkValue(t.Value, visit); err != nil {
			return err
		}
		for _, kase := range t.Cases {
			if err := WalkValue(kase.Expr, visit); err != nil {
				return err
			}
		}
	case *ReturnTerm:
		return WalkValue(t.Value, visit)
	case *PanicTerm:
		return WalkValue(t.Value, visit)
	}
	return nil
}

func WalkPlaceValues(place Place, visit func(Value) error) error {
	switch p := place.(type) {
	case nil, *LocalPlace:
		return nil
	case *FieldPlace:
		return WalkPlaceValues(p.Base, visit)
	case *IndexPlace:
		if err := WalkPlaceValues(p.Base, visit); err != nil {
			return err
		}
		return WalkValue(p.Index, visit)
	case *DerefPlace:
		return WalkValue(p.Pointer, visit)
	}
	return nil
}

func WalkValue(value Value, visit func(Value) error) error {
	if value == nil {
		return nil
	}
	if visit != nil {
		if err := visit(value); err != nil {
			return err
		}
	}
	switch v := value.(type) {
	case *NameValue, *LocalValue, *TempValue, *NumberValue, *BoolValue, *StringValue, *NoneValue:
		return nil
	case *UnaryValue:
		return WalkValue(v.Right, visit)
	case *AddrOfValue:
		return WalkValue(v.Source, visit)
	case *LoadValue:
		return WalkValue(v.Pointer, visit)
	case *AtomicLoadValue:
		return WalkValue(v.Pointer, visit)
	case *BinaryValue:
		if err := WalkValue(v.Left, visit); err != nil {
			return err
		}
		return WalkValue(v.Right, visit)
	case *PostfixValue:
		return WalkValue(v.Left, visit)
	case *CallValue:
		if err := WalkValue(v.Callee, visit); err != nil {
			return err
		}
		for _, arg := range v.Args {
			if err := WalkValue(arg, visit); err != nil {
				return err
			}
		}
	case *FieldLoadValue:
		return WalkValue(v.Base, visit)
	case *FieldValue:
		return WalkValue(v.Base, visit)
	case *CastValue:
		return WalkValue(v.Left, visit)
	case *TypeTestValue:
		return WalkValue(v.Left, visit)
	case *CompositeValue:
		for _, item := range v.Items {
			if err := WalkValue(item.Key, visit); err != nil {
				return err
			}
			if err := WalkValue(item.Value, visit); err != nil {
				return err
			}
		}
	case *InterfaceValue:
		return WalkValue(v.Value, visit)
	case *IndexValue:
		if err := WalkValue(v.Base, visit); err != nil {
			return err
		}
		return WalkValue(v.Index, visit)
	}
	return nil
}
