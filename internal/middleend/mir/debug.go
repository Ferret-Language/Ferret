package mir

func DebugModule(mod *Module) any {
	if mod == nil {
		return nil
	}
	globals := make([]any, 0, len(mod.Globals))
	for _, global := range mod.Globals {
		globals = append(globals, map[string]any{
			"name":     global.Name,
			"mutable":  global.Mutable,
			"constant": global.Constant,
			"type":     typeString(global.Type),
			"init":     debugValue(global.Init),
		})
	}
	funcs := make([]any, 0, len(mod.Functions))
	for _, fn := range mod.Functions {
		locals := make([]any, 0, len(fn.Locals))
		for _, local := range fn.Locals {
			if local == nil {
				continue
			}
			locals = append(locals, map[string]any{
				"id":       local.ID,
				"name":     local.Name,
				"type":     typeString(local.Type),
				"mutable":  local.Mutable,
				"constant": local.Constant,
				"temp":     local.IsTemp,
			})
		}
		blocks := make([]any, 0, len(fn.Blocks))
		for _, block := range fn.Blocks {
			instrs := make([]any, 0, len(block.Instructions))
			for _, instr := range block.Instructions {
				instrs = append(instrs, debugInstr(instr))
			}
			blocks = append(blocks, map[string]any{
				"id":           block.ID,
				"instructions": instrs,
				"terminator":   debugTerm(block.Terminator),
			})
		}
		funcs = append(funcs, map[string]any{
			"name":   fn.Name,
			"entry":  fn.EntryID,
			"exit":   fn.ExitID,
			"result": typeString(fn.Result),
			"locals": locals,
			"blocks": blocks,
		})
	}
	return map[string]any{
		"key":         mod.Key,
		"import_path": mod.ImportPath,
		"globals":     globals,
		"functions":   funcs,
	}
}

func debugInstr(instr Instr) any {
	switch i := instr.(type) {
	case *BindInstr:
		return map[string]any{"kind": "bind", "name": i.Name, "mutable": i.Mutable, "constant": i.Constant, "type": typeString(i.Type), "value": debugValue(i.Value)}
	case *AssignInstr:
		return map[string]any{"kind": "assign", "target": i.TargetID, "value": debugValue(i.Value)}
	case *ComputeInstr:
		return map[string]any{"kind": "compute", "target": i.TargetID, "type": typeString(i.Type), "value": debugValue(i.Value)}
	case *StoreInstr:
		return map[string]any{"kind": "store", "target": debugPlace(i.Target), "value": debugValue(i.Value)}
	case *StoreFieldInstr:
		return map[string]any{"kind": "store_field", "base": debugValue(i.Base), "field_index": i.FieldIndex, "value": debugValue(i.Value)}
	case *EvalInstr:
		return map[string]any{"kind": "eval", "value": debugValue(i.Value)}
	case *DeferInstr:
		body := make([]any, 0, len(i.Body))
		for _, child := range i.Body {
			body = append(body, debugInstr(child))
		}
		return map[string]any{"kind": "defer", "body": body}
	case *LockInstr:
		return map[string]any{"kind": "lock", "local": i.LocalID, "value": debugValue(i.Value)}
	case *UnsafeInstr:
		return map[string]any{"kind": "unsafe"}
	default:
		return nil
	}
}

func debugTerm(term Terminator) any {
	switch t := term.(type) {
	case *JumpTerm:
		return map[string]any{"kind": "jump", "target": t.TargetID}
	case *BranchTerm:
		return map[string]any{"kind": "branch", "cond": debugValue(t.Cond), "true": t.TrueID, "false": t.FalseID}
	case *SwitchTerm:
		cases := make([]any, 0, len(t.Cases))
		for _, kase := range t.Cases {
			cases = append(cases, map[string]any{"expr": debugValue(kase.Expr), "target": kase.TargetID})
		}
		return map[string]any{"kind": "switch", "value": debugValue(t.Value), "cases": cases, "default": t.DefaultID}
	case *ReturnTerm:
		return map[string]any{"kind": "return", "value": debugValue(t.Value), "cleanup": t.CleanupID}
	case *PanicTerm:
		return map[string]any{"kind": "panic", "value": debugValue(t.Value), "cleanup": t.CleanupID}
	case *ExitTerm:
		return map[string]any{"kind": "exit"}
	default:
		return nil
	}
}

func debugValue(value Value) any {
	switch v := value.(type) {
	case *NameValue:
		return map[string]any{"kind": "name", "path": append([]string(nil), v.Path...), "type": typeString(v.Type())}
	case *LocalValue:
		return map[string]any{"kind": "local", "id": v.LocalID, "type": typeString(v.Type())}
	case *TempValue:
		return map[string]any{"kind": "temp", "name": v.Name, "type": typeString(v.Type())}
	case *NumberValue:
		return map[string]any{"kind": "number", "value": v.Value, "type": typeString(v.Type())}
	case *BoolValue:
		return map[string]any{"kind": "bool", "value": v.Value, "type": typeString(v.Type())}
	case *StringValue:
		return map[string]any{"kind": "string", "value": v.Value, "type": typeString(v.Type())}
	case *NoneValue:
		return map[string]any{"kind": "none", "type": typeString(v.Type())}
	case *UnaryValue:
		return map[string]any{"kind": "unary", "op": v.Op, "right": debugValue(v.Right), "type": typeString(v.Type())}
	case *AddrOfValue:
		return map[string]any{"kind": "addr_of", "mutable": v.Mutable, "source": debugValue(v.Source), "type": typeString(v.Type())}
	case *LoadValue:
		return map[string]any{"kind": "load", "pointer": debugValue(v.Pointer), "type": typeString(v.Type())}
	case *BinaryValue:
		return map[string]any{"kind": "binary", "op": v.Op, "left": debugValue(v.Left), "right": debugValue(v.Right), "type": typeString(v.Type())}
	case *PostfixValue:
		return map[string]any{"kind": "postfix", "op": v.Op, "left": debugValue(v.Left), "type": typeString(v.Type())}
	case *CallValue:
		args := make([]any, 0, len(v.Args))
		for _, arg := range v.Args {
			args = append(args, debugValue(arg))
		}
		return map[string]any{"kind": "call", "callee": debugValue(v.Callee), "args": args, "type": typeString(v.Type())}
	case *FieldLoadValue:
		return map[string]any{"kind": "field_load", "base": debugValue(v.Base), "field_index": v.FieldIndex, "type": typeString(v.Type())}
	case *FieldValue:
		return map[string]any{"kind": "member", "base": debugValue(v.Base), "field_index": v.FieldIndex, "member_name": v.MemberName, "type": typeString(v.Type())}
	case *CastValue:
		return map[string]any{"kind": "cast", "left": debugValue(v.Left), "type": typeString(v.Type())}
	case *CompositeValue:
		items := make([]any, 0, len(v.Items))
		for _, item := range v.Items {
			items = append(items, map[string]any{"name": item.Name, "value": debugValue(item.Value)})
		}
		return map[string]any{"kind": "composite", "items": items, "type": typeString(v.Type())}
	case *IndexValue:
		return map[string]any{"kind": "index", "base": debugValue(v.Base), "index": debugValue(v.Index), "type": typeString(v.Type())}
	default:
		return nil
	}
}

func debugPlace(place Place) any {
	switch p := place.(type) {
	case *LocalPlace:
		return map[string]any{"kind": "local", "id": p.LocalID}
	case *FieldPlace:
		return map[string]any{"kind": "field", "base": debugPlace(p.Base), "field_index": p.FieldIndex}
	case *IndexPlace:
		return map[string]any{"kind": "index_place", "base": debugPlace(p.Base), "index": debugValue(p.Index)}
	case *DerefPlace:
		return map[string]any{"kind": "deref_place", "pointer": debugValue(p.Pointer)}
	default:
		return nil
	}
}

func typeString(t interface{ String() string }) string {
	if t == nil {
		return "void"
	}
	return t.String()
}
