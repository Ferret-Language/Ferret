package ir

import (
	"compiler/internal/cfg"
	"compiler/internal/middleend/hir"
	"compiler/internal/source"
)

func LowerModule(cfgMod *cfg.Module, hirMod *hir.Module) *Module {
	if cfgMod == nil || hirMod == nil {
		return nil
	}
	out := &Module{
		Key:        hirMod.Key,
		ImportPath: hirMod.ImportPath,
		FilePath:   hirMod.FilePath,
		Globals:    make([]*Global, 0, len(hirMod.Globals)),
		Functions:  make([]*Function, 0, len(cfgMod.Functions)),
	}
	for _, global := range hirMod.Globals {
		out.Globals = append(out.Globals, lowerGlobal(global))
	}
	for _, fn := range cfgMod.Functions {
		out.Functions = append(out.Functions, lowerFunction(fn))
	}
	return out
}

func lowerGlobal(global *hir.Global) *Global {
	if global == nil {
		return nil
	}
	return &Global{
		Name:     global.Name,
		Mutable:  global.Mutable,
		Constant: global.Constant,
		Type:     global.Type,
		Init:     lowerValue(global.Value),
		Location: global.Location,
	}
}

func lowerFunction(fn *cfg.Function) *Function {
	if fn == nil || fn.Source == nil {
		return nil
	}
	out := &Function{
		Name:     fn.Name,
		Result:   fn.Source.Result,
		EntryID:  blockID(fn.Entry),
		ExitID:   blockID(fn.Exit),
		Blocks:   make([]*Block, 0, len(fn.Blocks)),
		Location: fn.Source.Location,
	}
	if fn.Source.Receiver != nil {
		out.Receiver = &Param{Name: fn.Source.Receiver.Name, Type: fn.Source.Receiver.Type, Location: fn.Source.Receiver.Location}
	}
	for _, param := range fn.Source.Params {
		if param == nil {
			continue
		}
		out.Params = append(out.Params, &Param{Name: param.Name, Type: param.Type, IsComptime: param.IsComptime, Location: param.Location})
	}
	for _, block := range fn.Blocks {
		out.Blocks = append(out.Blocks, lowerBlock(block))
	}
	return out
}

func lowerBlock(block *cfg.Block) *Block {
	if block == nil {
		return nil
	}
	out := &Block{ID: block.ID, Instructions: make([]Instr, 0, len(block.Stmts)), Location: block.Location}
	returnValue := hir.Expr(nil)
	for _, stmt := range block.Stmts {
		if _, ok := block.Terminator.(*cfg.ReturnTerm); ok {
			if ret, ok := stmt.(*hir.ReturnStmt); ok {
				returnValue = ret.Value
				continue
			}
		}
		if instr := lowerInstr(stmt); instr != nil {
			out.Instructions = append(out.Instructions, instr)
		}
	}
	out.Terminator = lowerTerminator(block.Terminator, returnValue, block.Location)
	if out.Terminator == nil && block.Terminator == nil {
		out.Terminator = &ExitTerm{baseTerm: baseTerm{Location: block.Location}}
	}
	return out
}

func lowerInstr(stmt hir.Stmt) Instr {
	switch s := stmt.(type) {
	case nil, *hir.ReturnStmt, *hir.BreakStmt, *hir.ContinueStmt:
		return nil
	case *hir.LetStmt:
		return &BindInstr{baseInstr: baseInstr{Location: s.Loc()}, Name: s.Name, Mutable: s.Mutable, Type: s.Type, Value: lowerValue(s.Value)}
	case *hir.ConstStmt:
		return &BindInstr{baseInstr: baseInstr{Location: s.Loc()}, Name: s.Name, Constant: true, Type: s.Type, Value: lowerValue(s.Value)}
	case *hir.ExprStmt:
		return &EvalInstr{baseInstr: baseInstr{Location: s.Loc()}, Value: lowerValue(s.Value)}
	case *hir.AssignStmt:
		return &StoreInstr{baseInstr: baseInstr{Location: s.Loc()}, Target: lowerPlace(s.Left), Value: lowerValue(s.Right)}
	case *hir.DeferStmt:
		return &DeferInstr{baseInstr: baseInstr{Location: s.Loc()}, Body: lowerDeferredBody(s.Body)}
	case *hir.LockStmt:
		return &LockInstr{baseInstr: baseInstr{Location: s.Loc()}, Value: lowerValue(s.Value), Name: s.Name}
	case *hir.UnsafeStmt:
		return &UnsafeInstr{baseInstr: baseInstr{Location: s.Loc()}}
	default:
		return nil
	}
}

func lowerDeferredBody(stmt hir.Stmt) []Instr {
	out := make([]Instr, 0)
	switch s := stmt.(type) {
	case nil:
		return nil
	case *hir.BlockStmt:
		for _, child := range s.Stmts {
			if instr := lowerInstr(child); instr != nil {
				out = append(out, instr)
			}
		}
	case *hir.ExprStmt, *hir.AssignStmt, *hir.LetStmt, *hir.ConstStmt, *hir.LockStmt, *hir.UnsafeStmt:
		if instr := lowerInstr(s); instr != nil {
			out = append(out, instr)
		}
	}
	return out
}

func lowerTerminator(term cfg.Terminator, returnValue hir.Expr, loc source.Location) Terminator {
	switch t := term.(type) {
	case nil:
		return nil
	case *cfg.JumpTerm:
		return &JumpTerm{baseTerm: baseTerm{Location: loc}, TargetID: blockID(t.Target)}
	case *cfg.BranchTerm:
		return &BranchTerm{baseTerm: baseTerm{Location: loc}, Cond: lowerValue(t.Cond), TrueID: blockID(t.True), FalseID: blockID(t.False)}
	case *cfg.SwitchTerm:
		out := &SwitchTerm{baseTerm: baseTerm{Location: loc}, Value: lowerValue(t.Value), Cases: make([]SwitchCase, 0, len(t.Cases)), DefaultID: blockID(t.Default)}
		for _, edge := range t.Cases {
			out.Cases = append(out.Cases, SwitchCase{Expr: lowerValue(edge.Expr), TargetID: blockID(edge.Target)})
		}
		return out
	case *cfg.ReturnTerm:
		return &ReturnTerm{baseTerm: baseTerm{Location: loc}, Value: lowerValue(returnValue)}
	default:
		return nil
	}
}

func lowerValue(expr hir.Expr) Value {
	switch e := expr.(type) {
	case nil:
		return nil
	case *hir.Ident:
		return &NameValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Path: append([]string(nil), e.Path...)}
	case *hir.NumberLit:
		return &NumberValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Value: e.Value}
	case *hir.StringLit:
		return &StringValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Value: e.Value}
	case *hir.NoneLit:
		return &NoneValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}}
	case *hir.PrefixExpr:
		return &UnaryValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Op: e.Op, Right: lowerValue(e.Right)}
	case *hir.UnsafeExpr:
		return &UnaryValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Op: "unsafe", Right: lowerValue(e.Value)}
	case *hir.BinaryExpr:
		return &BinaryValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Left: lowerValue(e.Left), Op: e.Op, Right: lowerValue(e.Right)}
	case *hir.PostfixExpr:
		return &PostfixValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Left: lowerValue(e.Left), Op: e.Op}
	case *hir.CallExpr:
		out := &CallValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Callee: lowerValue(e.Callee), Args: make([]Value, 0, len(e.Args))}
		for _, arg := range e.Args {
			out.Args = append(out.Args, lowerValue(arg))
		}
		return out
	case *hir.SelectorExpr:
		return &FieldValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Base: lowerValue(e.Left), Name: e.Name}
	case *hir.CastExpr:
		return &CastValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Left: lowerValue(e.Left)}
	case *hir.CompositeLit:
		out := &CompositeValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Items: make([]CompositeItem, 0, len(e.Items))}
		for _, item := range e.Items {
			out.Items = append(out.Items, CompositeItem{Name: item.Name, Value: lowerValue(item.Value)})
		}
		return out
	default:
		return nil
	}
}

func lowerPlace(expr hir.Expr) Place {
	switch e := expr.(type) {
	case nil:
		return nil
	case *hir.Ident:
		if len(e.Path) == 1 {
			return &LocalPlace{basePlace: basePlace{Location: e.Loc()}, Name: e.Path[0]}
		}
		return nil
	case *hir.SelectorExpr:
		return &FieldPlace{basePlace: basePlace{Location: e.Loc()}, Base: lowerPlace(e.Left), Name: e.Name}
	default:
		return nil
	}
}

func blockID(block *cfg.Block) int {
	if block == nil {
		return -1
	}
	return block.ID
}
