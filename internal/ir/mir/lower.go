package mir

import (
	cfg "compiler/internal/analysis/cfg/model"
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
	"compiler/internal/ir/hir"
	"strconv"
	"strings"
)

type lowerContext struct {
	locals       []*Local
	bindings     *binding.ModuleInfo
	globalConsts map[ast.Node]hir.Expr
	localConsts  map[string]hir.Expr
	importPath   string
	lookupMethod hir.MethodLookup
	resultType   typeinfo.Type
}

func LowerModule(cfgMod *cfg.Module, hirMod *hir.Module, bindings *binding.ModuleInfo, globalConsts map[ast.Node]hir.Expr, lookupMethod hir.MethodLookup) *Module {
	if cfgMod == nil || hirMod == nil {
		return nil
	}
	out := &Module{
		Key:        hirMod.Key,
		ImportPath: hirMod.ImportPath,
		FilePath:   hirMod.FilePath,
		Types:      make([]*TypeDecl, 0, len(hirMod.Types)),
		Globals:    make([]*Global, 0, len(hirMod.Globals)),
		Functions:  make([]*Function, 0, len(cfgMod.Functions)),
	}
	for _, decl := range hirMod.Types {
		out.Types = append(out.Types, lowerTypeDecl(decl))
	}
	for _, global := range hirMod.Globals {
		out.Globals = append(out.Globals, lowerGlobal(global, bindings, globalConsts, hirMod.ImportPath, lookupMethod))
	}
	for _, fn := range cfgMod.Functions {
		out.Functions = append(out.Functions, lowerFunction(fn, bindings, globalConsts, hirMod.ImportPath, lookupMethod))
	}
	return NormalizeModule(out)
}

func lowerTypeDecl(decl *hir.TypeDecl) *TypeDecl {
	if decl == nil {
		return nil
	}
	out := &TypeDecl{
		Name:       decl.Name,
		Named:      decl.Named,
		Underlying: decl.Underlying,
		Location:   decl.Location,
	}
	if decl.Struct != nil {
		out.Struct = lowerStructTypeDecl(decl.Struct)
	}
	if decl.Interface != nil {
		out.Interface = lowerInterfaceTypeDecl(decl.Interface)
	}
	if decl.Enum != nil {
		out.Enum = &EnumTypeDecl{Variants: append([]string(nil), decl.Enum.Variants...)}
	}
	if decl.Union != nil {
		out.Union = &UnionTypeDecl{Members: append([]typeinfo.Type(nil), decl.Union.Members...)}
	}
	if decl.Error != nil {
		out.Error = &ErrorTypeDecl{Members: append([]string(nil), decl.Error.Members...)}
	}
	return out
}

func lowerStructTypeDecl(decl *hir.StructTypeDecl) *StructTypeDecl {
	if decl == nil {
		return nil
	}
	out := &StructTypeDecl{
		Fields: make([]*StructFieldDecl, 0, len(decl.Fields)),
	}
	for _, field := range decl.Fields {
		if field == nil {
			continue
		}
		out.Fields = append(out.Fields, &StructFieldDecl{
			Name:     field.Name,
			Type:     field.Type,
			Default:  lowerValue(nil, field.Default),
			Location: field.Location,
		})
	}
	return out
}

func lowerInterfaceTypeDecl(decl *hir.InterfaceTypeDecl) *InterfaceTypeDecl {
	if decl == nil {
		return nil
	}
	out := &InterfaceTypeDecl{Methods: make([]*InterfaceMethodDecl, 0, len(decl.Methods))}
	for _, method := range decl.Methods {
		if method == nil || method.Static {
			continue
		}
		entry := &InterfaceMethodDecl{
			Receiver: method.Receiver,
			Static:   method.Static,
			Name:     method.Name,
			Result:   method.Result,
			Location: method.Location,
			Params:   make([]*Param, 0, len(method.Params)),
		}
		for _, param := range method.Params {
			if param == nil {
				continue
			}
			entry.Params = append(entry.Params, &Param{
				Name:       param.Name,
				Type:       param.Type,
				IsComptime: param.IsComptime,
				Location:   param.Location,
			})
		}
		out.Methods = append(out.Methods, entry)
	}
	return out
}

func lowerGlobal(global *hir.Global, bindings *binding.ModuleInfo, globalConsts map[ast.Node]hir.Expr, importPath string, lookupMethod hir.MethodLookup) *Global {
	if global == nil {
		return nil
	}
	lowerCtx := &lowerContext{bindings: bindings, globalConsts: globalConsts, importPath: importPath, lookupMethod: lookupMethod}
	return &Global{
		Name:     global.Name,
		Mutable:  global.Mutable,
		Constant: global.Constant,
		Type:     global.Type,
		Init:     lowerCoercedValue(lowerCtx, global.Value, global.Type),
		Location: global.Location,
	}
}

func lowerFunction(fn *cfg.Function, bindings *binding.ModuleInfo, globalConsts map[ast.Node]hir.Expr, importPath string, lookupMethod hir.MethodLookup) *Function {
	if fn == nil || fn.Source == nil {
		return nil
	}
	lowerCtx := newLowerContext(fn.Source, bindings, globalConsts, importPath, lookupMethod)
	out := &Function{
		Name:       fn.Name,
		LinkName:   lowerFunctionLinkName(fn.Source),
		IsUnsafe:   fn.Source.IsUnsafe,
		IsBuiltin:  fn.Source.IsBuiltin,
		IsExtern:   fn.Source.IsExtern,
		ExternName: fn.Source.ExternName,
		Result:     fn.Source.Result,
		EntryID:    blockID(fn.Entry),
		ExitID:     blockID(fn.Exit),
		Locals:     lowerCtx.locals,
		Blocks:     make([]*Block, 0, len(fn.Blocks)),
		Location:   fn.Source.Location,
	}
	if fn.Source.Body == nil {
		out.EntryID = -1
		out.ExitID = -1
		out.Blocks = nil
		return out
	}
	if fn.Source.Receiver != nil {
		out.Params = append(out.Params, &Param{
			Name:      fn.Source.Receiver.Name,
			LocalID:   fn.Source.Receiver.LocalID,
			Type:      fn.Source.Receiver.Type,
			IsMutable: true,
			Location:  fn.Source.Receiver.Location,
		})
	}
	for _, param := range fn.Source.Params {
		if param == nil {
			continue
		}
		out.Params = append(out.Params, &Param{
			Name:       param.Name,
			LocalID:    param.LocalID,
			Type:       param.Type,
			IsMutable:  param.IsMutable,
			IsComptime: param.IsComptime,
			Location:   param.Location,
		})
	}
	for _, block := range fn.Blocks {
		out.Blocks = append(out.Blocks, lowerBlock(lowerCtx, block))
	}
	return out
}

func lowerBlock(lowerCtx *lowerContext, block *cfg.Block) *Block {
	if block == nil {
		return nil
	}
	out := &Block{ID: block.ID, Instructions: make([]Instr, 0, len(block.Stmts)), Location: block.Location}
	for _, stmt := range block.Stmts {
		if instr := lowerInstr(lowerCtx, stmt); instr != nil {
			out.Instructions = append(out.Instructions, instr)
		}
	}
	out.Terminator = lowerTerminator(lowerCtx, block.Terminator, block.Location)
	if out.Terminator == nil && block.Terminator == nil {
		out.Terminator = &ExitTerm{baseTerm: baseTerm{Location: block.Location}}
	}
	return out
}

func lowerInstr(lowerCtx *lowerContext, stmt hir.Stmt) Instr {
	switch s := stmt.(type) {
	case nil, *hir.ReturnStmt, *hir.BreakStmt, *hir.ContinueStmt:
		return nil
	case *hir.LetStmt:
		if s.Value == nil {
			return nil
		}
		return &AssignInstr{baseInstr: baseInstr{Location: s.Loc()}, TargetID: s.LocalID, Value: lowerCoercedValue(lowerCtx, s.Value, s.Type)}
	case *hir.ConstStmt:
		return nil
	case *hir.ExprStmt:
		return &EvalInstr{baseInstr: baseInstr{Location: s.Loc()}, Value: lowerValue(lowerCtx, s.Value)}
	case *hir.AssignStmt:
		if ident, ok := s.Left.(*hir.Ident); ok && len(ident.Path) == 1 && ident.Path[0] == "_" {
			return &EvalInstr{baseInstr: baseInstr{Location: s.Loc()}, Value: lowerValue(lowerCtx, s.Right)}
		}
		if target := lowerAssignableTarget(lowerCtx, s.Left); target != nil {
			return &StoreInstr{baseInstr: baseInstr{Location: s.Loc()}, Target: target, Value: lowerCoercedValue(lowerCtx, s.Right, s.Left.Type())}
		}
		if ident, ok := s.Left.(*hir.Ident); ok && ident.LocalID >= 0 {
			return &AssignInstr{baseInstr: baseInstr{Location: s.Loc()}, TargetID: ident.LocalID, Value: lowerCoercedValue(lowerCtx, s.Right, s.Left.Type())}
		}
		if sel, ok := s.Left.(*hir.SelectorExpr); ok {
			if fieldIndex := lowerCtx.fieldIndex(sel.Left.Type(), sel.Name); fieldIndex >= 0 {
				return &StoreFieldInstr{
					baseInstr:  baseInstr{Location: s.Loc()},
					Base:       lowerValue(lowerCtx, sel.Left),
					FieldIndex: fieldIndex,
					Value:      lowerCoercedValue(lowerCtx, s.Right, s.Left.Type()),
				}
			}
		}
		return &StoreInstr{baseInstr: baseInstr{Location: s.Loc()}, Target: lowerPlace(lowerCtx, s.Left), Value: lowerCoercedValue(lowerCtx, s.Right, s.Left.Type())}
	case *hir.DeferStmt:
		return &DeferInstr{baseInstr: baseInstr{Location: s.Loc()}, Body: lowerDeferredBody(lowerCtx, s.Body)}
	case *hir.LockStmt:
		return &LockInstr{baseInstr: baseInstr{Location: s.Loc()}, Value: lowerValue(lowerCtx, s.Value), LocalID: s.LocalID}
	case *hir.UnsafeStmt:
		return &UnsafeInstr{baseInstr: baseInstr{Location: s.Loc()}}
	default:
		return nil
	}
}

func lowerAssignableTarget(lowerCtx *lowerContext, expr hir.Expr) Place {
	if expr == nil {
		return nil
	}
	if resolved := lowerResolvedName(lowerCtx, expr.SourceExpr(), expr.Loc(), expr.Type()); resolved != nil {
		return &DerefPlace{
			basePlace: basePlace{Location: expr.Loc()},
			Pointer: &AddrOfValue{
				baseValue: baseValue{Location: expr.Loc(), ExprType: &typeinfo.PointerType{Inner: expr.Type()}},
				Source:    resolved,
			},
		}
	}
	return nil
}

func lowerDeferredBody(lowerCtx *lowerContext, stmt hir.Stmt) []Instr {
	out := make([]Instr, 0)
	switch s := stmt.(type) {
	case nil:
		return nil
	case *hir.BlockStmt:
		for _, child := range s.Stmts {
			if instr := lowerInstr(lowerCtx, child); instr != nil {
				out = append(out, instr)
			}
		}
	case *hir.ExprStmt, *hir.AssignStmt, *hir.LetStmt, *hir.ConstStmt, *hir.LockStmt, *hir.UnsafeStmt:
		if instr := lowerInstr(lowerCtx, s); instr != nil {
			out = append(out, instr)
		}
	}
	return out
}

func lowerTerminator(lowerCtx *lowerContext, term cfg.Terminator, loc source.Location) Terminator {
	switch t := term.(type) {
	case nil:
		return nil
	case *cfg.JumpTerm:
		return &JumpTerm{baseTerm: baseTerm{Location: loc}, TargetID: blockID(t.Target)}
	case *cfg.BranchTerm:
		return &BranchTerm{baseTerm: baseTerm{Location: loc}, Cond: lowerValue(lowerCtx, t.Cond), TrueID: blockID(t.True), FalseID: blockID(t.False)}
	case *cfg.SwitchTerm:
		out := &SwitchTerm{baseTerm: baseTerm{Location: loc}, Value: lowerValue(lowerCtx, t.Value), Cases: make([]SwitchCase, 0, len(t.Cases)), DefaultID: blockID(t.Default)}
		for _, edge := range t.Cases {
			out.Cases = append(out.Cases, SwitchCase{Expr: lowerValue(lowerCtx, edge.Expr), TargetID: blockID(edge.Target)})
		}
		return out
	case *cfg.ReturnTerm:
		cleanupID := -1
		if t.Cleanup != nil {
			cleanupID = blockID(t.Cleanup)
		}
		return &ReturnTerm{baseTerm: baseTerm{Location: loc}, Value: lowerCoercedValue(lowerCtx, t.Value, lowerCtx.fnResultType()), CleanupID: cleanupID}
	case *cfg.PanicTerm:
		cleanupID := -1
		if t.Cleanup != nil {
			cleanupID = blockID(t.Cleanup)
		}
		return &PanicTerm{baseTerm: baseTerm{Location: loc}, Value: lowerValue(lowerCtx, t.Value), CleanupID: cleanupID}
	default:
		return nil
	}
}

func lowerCoercedValue(lowerCtx *lowerContext, expr hir.Expr, expected typeinfo.Type) Value {
	if expr == nil {
		return nil
	}
	if methodLinks, concreteType, ok := lowerInterfaceCoercion(lowerCtx, expr.Type(), expected); ok {
		return &InterfaceValue{
			baseValue:    baseValue{Location: expr.Loc(), ExprType: expected},
			Value:        lowerValue(lowerCtx, expr),
			ConcreteType: concreteType,
			Methods:      methodLinks,
		}
	}
	return lowerValue(lowerCtx, expr)
}

func lowerValue(lowerCtx *lowerContext, expr hir.Expr) Value {
	switch e := expr.(type) {
	case nil:
		return nil
	case *hir.Ident:
		if len(e.Path) == 1 {
			switch e.Path[0] {
			case "true":
				return &BoolValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Value: true}
			case "false":
				return &BoolValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Value: false}
			}
			if lowerCtx != nil {
				if value, ok := lowerCtx.lookupConstExpr(e.Path[0], e.SourceExpr()); ok {
					return withValueContext(lowerValue(lowerCtx, value), e.Loc(), e.Type())
				}
			}
		}
		if lowerCtx != nil {
			if value, ok := lowerCtx.lookupResolvedConstExpr(e.SourceExpr()); ok {
				return withValueContext(lowerValue(lowerCtx, value), e.Loc(), e.Type())
			}
		}
		if e.LocalID >= 0 {
			return &LocalValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, LocalID: e.LocalID}
		}
		if lowerCtx == nil {
			return &NameValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Path: append([]string(nil), e.Path...)}
		}
		return lowerNameValue(lowerCtx, e.SourceExpr(), e.Loc(), e.Type(), e.Path)
	case *hir.NumberLit:
		return &NumberValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Value: e.Value}
	case *hir.StringLit:
		if _, isString := e.Type().(*typeinfo.StringType); isString {
			// String literal as str: produce a { ptr, len } composite.
			ptrType := &typeinfo.RawPtrType{Inner: &typeinfo.BuiltinType{Name: "u8"}}
			ptrVal := &StringValue{baseValue: baseValue{Location: e.Loc(), ExprType: ptrType}, Value: e.Value}
			lenVal := &NumberValue{baseValue: baseValue{ExprType: &typeinfo.BuiltinType{Name: "usize"}}, Value: strconv.FormatUint(uint64(len(e.Value)), 10)}
			return &CompositeValue{
				baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()},
				Items: []CompositeItem{
					{Name: "ptr", Value: ptrVal},
					{Name: "len", Value: lenVal},
				},
			}
		}
		return &StringValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Value: e.Value}
	case *hir.NoneLit:
		return &NoneValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}}
	case *hir.PrefixExpr:
		switch e.Op {
		case "&":
			return &AddrOfValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Source: lowerAddrSource(lowerCtx, e.Right), Mutable: false}
		case "&mut":
			return &AddrOfValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Source: lowerAddrSource(lowerCtx, e.Right), Mutable: true}
		case "@":
			return &AddrOfValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Source: lowerAddrSource(lowerCtx, e.Right), Mutable: false, Raw: true}
		case "@mut":
			return &AddrOfValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Source: lowerAddrSource(lowerCtx, e.Right), Mutable: true, Raw: true}
		case "*":
			return &LoadValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Pointer: lowerValue(lowerCtx, e.Right)}
		default:
			return &UnaryValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Op: e.Op, Right: lowerValue(lowerCtx, e.Right)}
		}
	case *hir.BinaryExpr:
		return &BinaryValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Left: lowerValue(lowerCtx, e.Left), Op: e.Op, Right: lowerValue(lowerCtx, e.Right)}
	case *hir.PostfixExpr:
		return &PostfixValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Left: lowerValue(lowerCtx, e.Left), Op: e.Op}
	case *hir.CallExpr:
		// Normalize method calls (instance.Method(...)) to direct function calls
		// by prepending the receiver as the first argument, e.g. p.Len2() → Len2(p).
		if sel, ok := e.Callee.(*hir.SelectorExpr); ok {
			if lowerCtx.fieldIndex(sel.Left.Type(), sel.Name) < 0 {
				if lowerIsInterfaceType(sel.Left.Type()) {
					out := &CallValue{
						baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()},
						Callee:    lowerValue(lowerCtx, e.Callee),
						Args:      make([]Value, 0, 1+len(e.Args)),
					}
					out.Args = append(out.Args, lowerValue(lowerCtx, sel.Left))
					var fnType *typeinfo.FuncType
					if typed, ok := e.Callee.Type().(*typeinfo.FuncType); ok {
						fnType = typed
					}
					for _, arg := range e.Args {
						expected := typeinfo.Type(nil)
						if fnType != nil && len(out.Args)-1 < len(fnType.Params) {
							expected = fnType.Params[len(out.Args)-1]
						}
						out.Args = append(out.Args, lowerCoercedValue(lowerCtx, arg, expected))
					}
					return out
				}
				if named := lowerReceiverNamed(sel.Left.Type()); named != nil {
					receiver := lowerMethodReceiverValue(lowerCtx, sel.Left, e.MethodReceiver)
					path := lowerMethodSymbolPath(lowerCtx, named, sel.Name)
					callee := &NameValue{
						baseValue: baseValue{Location: sel.Loc(), ExprType: sel.Type()},
						Path:      path,
					}
					out := &CallValue{
						baseValue:    baseValue{Location: e.Loc(), ExprType: e.Type()},
						Callee:       callee,
						Args:         make([]Value, 0, 1+len(e.Args)),
						ReceiverType: e.MethodReceiver,
					}
					out.Args = append(out.Args, receiver)
					for _, arg := range e.Args {
						out.Args = append(out.Args, lowerValue(lowerCtx, arg))
					}
					return out
				}
			}
		}
		out := &CallValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Callee: lowerValue(lowerCtx, e.Callee), Args: make([]Value, 0, len(e.Args))}
		var fnType *typeinfo.FuncType
		if typed, ok := e.Callee.Type().(*typeinfo.FuncType); ok {
			fnType = typed
		}
		for _, arg := range e.Args {
			expected := typeinfo.Type(nil)
			if fnType != nil && len(out.Args) < len(fnType.Params) {
				expected = fnType.Params[len(out.Args)]
			}
			out.Args = append(out.Args, lowerCoercedValue(lowerCtx, arg, expected))
		}
		return out
	case *hir.ConstructorCallExpr:
		callee := &NameValue{
			baseValue: baseValue{Location: e.Loc(), ExprType: &typeinfo.FuncType{Result: &typeinfo.BuiltinType{Name: "void"}}},
			Path:      append([]string(nil), e.Path...),
		}
		out := &CallValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Callee: callee, Args: make([]Value, 0, len(e.Args)), IsConstructor: true}
		for _, arg := range e.Args {
			out.Args = append(out.Args, lowerValue(lowerCtx, arg))
		}
		return out
	case *hir.SelectorExpr:
		if resolved := lowerResolvedName(lowerCtx, e.SourceExpr(), e.Loc(), e.Type()); resolved != nil {
			return resolved
		}
		base := lowerValue(lowerCtx, e.Left)
		if index := lowerCtx.fieldIndex(e.Left.Type(), e.Name); index >= 0 {
			return &FieldLoadValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Base: base, FieldIndex: index}
		}
		return &FieldValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Base: base, FieldIndex: -1, MemberName: e.Name}
	case *hir.CastExpr:
		return &CastValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Left: lowerValue(lowerCtx, e.Left)}
	case *hir.IsExpr:
		if e.StaticKnown {
			return &BoolValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Value: e.StaticValue}
		}
		return &TypeTestValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Left: lowerValue(lowerCtx, e.Left), Target: e.Target}
	case *hir.IndexExpr:
		base := lowerValue(lowerCtx, e.Left)
		index := lowerValue(lowerCtx, e.Index)
		return &IndexValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Base: base, Index: index}
	case *hir.CompositeLit:
		out := &CompositeValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Items: make([]CompositeItem, 0, len(e.Items)), ConstructorPath: append([]string(nil), e.ConstructorPath...)}
		for _, item := range e.Items {
			out.Items = append(out.Items, CompositeItem{Name: item.Name, Value: lowerValue(lowerCtx, item.Value)})
		}
		return out
	default:
		return nil
	}
}

func collectLocals(fn *hir.Func) ([]*Local, map[string]hir.Expr) {
	if fn == nil {
		return nil, nil
	}
	if fn.LocalCount < 0 {
		fn.LocalCount = 0
	}
	locals := make([]*Local, fn.LocalCount)
	consts := make(map[string]hir.Expr)

	set := func(id int, name string, typ typeinfo.Type, mutable, constant bool, loc source.Location) {
		if id < 0 || id >= len(locals) {
			return
		}
		if typ == nil {
			typ = typeinfo.UnknownType{}
		}
		if locals[id] == nil {
			locals[id] = &Local{ID: id, Name: name, Type: typ, Mutable: mutable, Constant: constant, Location: loc}
			return
		}
		// Prefer first-seen location and "real" type information.
		if locals[id].Name == "" && name != "" {
			locals[id].Name = name
		}
		if typeinfo.IsUnknown(locals[id].Type) && !typeinfo.IsUnknown(typ) {
			locals[id].Type = typ
		}
		locals[id].Mutable = locals[id].Mutable || mutable
		locals[id].Constant = locals[id].Constant || constant
		if locals[id].Location.Start == nil && loc.Start != nil {
			locals[id].Location = loc
		}
	}

	if fn.Receiver != nil {
		set(fn.Receiver.LocalID, fn.Receiver.Name, fn.Receiver.Type, true, false, fn.Receiver.Location)
	}
	for _, param := range fn.Params {
		if param == nil {
			continue
		}
		set(param.LocalID, param.Name, param.Type, false, false, param.Location)
	}

	var walkStmt func(hir.Stmt)
	walkStmt = func(stmt hir.Stmt) {
		switch s := stmt.(type) {
		case nil:
			return
		case *hir.BlockStmt:
			for _, child := range s.Stmts {
				walkStmt(child)
			}
		case *hir.LetStmt:
			set(s.LocalID, s.Name, s.Type, s.Mutable, false, s.Loc())
		case *hir.ConstStmt:
			// Consts are compile-time only; don't create runtime locals.
			if s.Name != "" {
				consts[s.Name] = s.Value
			}
		case *hir.IfStmt:
			walkStmt(s.Then)
			walkStmt(s.Else)
		case *hir.MatchStmt:
			for _, arm := range s.Arms {
				if arm != nil {
					walkStmt(arm.Body)
				}
			}
		case *hir.WhileStmt:
			walkStmt(s.Body)
		case *hir.ForStmt:
			if s.IndexID >= 0 {
				set(s.IndexID, s.IndexName, &typeinfo.BuiltinType{Name: "usize"}, false, false, s.Loc())
			}
			if s.ValueID >= 0 {
				set(s.ValueID, s.ValueName, typeinfo.UnknownType{}, false, false, s.Loc())
			}
			walkStmt(s.Body)
		case *hir.LoopStmt:
			walkStmt(s.Init)
			walkStmt(s.Post)
			walkStmt(s.Body)
		case *hir.LabelStmt:
			walkStmt(s.Stmt)
		case *hir.DeferStmt:
			walkStmt(s.Body)
		case *hir.LockStmt:
			set(s.LocalID, s.Name, s.Value.Type(), true, false, s.Loc())
			walkStmt(s.Body)
		case *hir.UnsafeStmt:
			walkStmt(s.Body)
		}
	}
	if fn.Body != nil {
		walkStmt(fn.Body)
	}

	for i := range locals {
		if locals[i] != nil {
			continue
		}
		locals[i] = &Local{
			ID:       i,
			Name:     "__local" + strconv.Itoa(i),
			Type:     typeinfo.UnknownType{},
			Mutable:  false,
			Constant: false,
			Location: source.Location{},
		}
	}

	return locals, consts
}

func lowerPlace(lowerCtx *lowerContext, expr hir.Expr) Place {
	if target := lowerAssignableTarget(lowerCtx, expr); target != nil {
		return target
	}
	switch e := expr.(type) {
	case nil:
		return nil
	case *hir.Ident:
		if e.LocalID >= 0 {
			return &LocalPlace{basePlace: basePlace{Location: e.Loc()}, LocalID: e.LocalID}
		}
		return nil
	case *hir.SelectorExpr:
		return &FieldPlace{basePlace: basePlace{Location: e.Loc()}, Base: lowerPlace(lowerCtx, e.Left), FieldIndex: lowerCtx.fieldIndex(e.Left.Type(), e.Name)}
	case *hir.IndexExpr:
		index := lowerValue(lowerCtx, e.Index)
		return &IndexPlace{basePlace: basePlace{Location: e.Loc()}, Base: lowerPlace(lowerCtx, e.Left), Index: index}
	case *hir.PrefixExpr:
		if e.Op == "*" {
			return &DerefPlace{basePlace: basePlace{Location: e.Loc()}, Pointer: lowerValue(lowerCtx, e.Right)}
		}
		return nil
	default:
		return nil
	}
}

func newLowerContext(fn *hir.Func, bindings *binding.ModuleInfo, globalConsts map[ast.Node]hir.Expr, importPath string, lookupMethod hir.MethodLookup) *lowerContext {
	locals, consts := collectLocals(fn)
	return &lowerContext{
		locals:       locals,
		bindings:     bindings,
		globalConsts: globalConsts,
		localConsts:  consts,
		importPath:   importPath,
		lookupMethod: lookupMethod,
		resultType:   fn.Result,
	}
}

func (c *lowerContext) fnResultType() typeinfo.Type {
	if c == nil {
		return nil
	}
	return c.resultType
}

func (c *lowerContext) fieldIndex(typ typeinfo.Type, name string) int {
	structType, ok := lowerStructView(typ)
	if !ok {
		return -1
	}
	for index, field := range structType.OrderedFields {
		if field != nil && field.Name == name {
			return index
		}
	}
	return -1
}

func (c *lowerContext) lookupConstExpr(name string, source ast.Expr) (hir.Expr, bool) {
	if c == nil {
		return nil, false
	}
	if expr, ok := c.localConsts[name]; ok && expr != nil {
		return expr, true
	}
	return c.lookupResolvedConstExpr(source)
}

func (c *lowerContext) lookupResolvedConstExpr(source ast.Expr) (hir.Expr, bool) {
	if c == nil || c.bindings == nil || source == nil {
		return nil, false
	}
	resolution, ok := c.bindings.Nodes[source]
	if !ok || resolution == nil || resolution.Kind != binding.ResolutionSymbol || resolution.Symbol == nil {
		return nil, false
	}
	if resolution.Symbol.Kind != symbols.SymbolConst {
		return nil, false
	}
	if expr, ok := c.globalConsts[resolution.Symbol.Node]; ok && expr != nil {
		return expr, true
	}
	return nil, false
}

func (c *lowerContext) lookupResolution(source ast.Expr) (*binding.Resolution, bool) {
	if c == nil || c.bindings == nil || source == nil {
		return nil, false
	}
	resolution, ok := c.bindings.Nodes[source]
	if !ok || resolution == nil {
		return nil, false
	}
	return resolution, true
}

func lowerResolvedName(c *lowerContext, source ast.Expr, loc source.Location, typ typeinfo.Type) Value {
	if c == nil || source == nil {
		return nil
	}
	resolution, ok := c.lookupResolution(source)
	if !ok || resolution.Kind != binding.ResolutionSymbol || resolution.Symbol == nil {
		return nil
	}
	if lit := lowerResolvedScalarValue(resolution, loc, typ); lit != nil {
		return lit
	}
	out := &NameValue{
		baseValue: baseValue{Location: loc, ExprType: typ},
		Path:      canonicalResolvedPath(c, resolution),
	}
	if fn, ok := resolution.Symbol.Node.(*ast.FuncDecl); ok {
		if fn.IsExtern && fn.ExternName != "" {
			out.LinkName = fn.ExternName
		}
	}
	return out
}

func lowerResolvedScalarValue(resolution *binding.Resolution, loc source.Location, typ typeinfo.Type) Value {
	if resolution == nil || resolution.Symbol == nil {
		return nil
	}
	switch resolution.Symbol.Kind {
	case symbols.SymbolVariant:
		if ordinal, ok := lookupEnumOrdinal(typ, resolution.Symbol.Name); ok {
			return &NumberValue{baseValue: baseValue{Location: loc, ExprType: typ}, Value: strconv.Itoa(ordinal)}
		}
	case symbols.SymbolError:
		if ordinal, ok := lookupErrorOrdinal(typ, resolution.Symbol.Name); ok {
			return &NumberValue{baseValue: baseValue{Location: loc, ExprType: typ}, Value: strconv.Itoa(ordinal)}
		}
	}
	return nil
}

func lookupEnumOrdinal(typ typeinfo.Type, name string) (int, bool) {
	if named, ok := typ.(*typeinfo.NamedType); ok && named != nil && named.Decl != nil {
		if decl, ok := named.Decl.Type.(*ast.EnumType); ok {
			for i, variant := range decl.Variants {
				if variant != nil && variant.Name != nil && variant.Name.Text() == name {
					return i, true
				}
			}
		}
	}
	if enumTyp, ok := typ.(*typeinfo.EnumType); ok && enumTyp != nil && enumTyp.VariantOrdinals != nil {
		ordinal, ok := enumTyp.VariantOrdinals[name]
		return ordinal, ok
	}
	return 0, false
}

func lookupErrorOrdinal(typ typeinfo.Type, name string) (int, bool) {
	if named, ok := typ.(*typeinfo.NamedType); ok && named != nil && named.Decl != nil {
		if decl, ok := named.Decl.Type.(*ast.ErrorType); ok {
			for i, member := range decl.Members {
				if member != nil && member.Name != nil && member.Name.Text() == name {
					return i, true
				}
			}
		}
	}
	if errTyp, ok := typ.(*typeinfo.ErrorSetType); ok && errTyp != nil && errTyp.MemberOrdinals != nil {
		ordinal, ok := errTyp.MemberOrdinals[name]
		return ordinal, ok
	}
	return 0, false
}

func lowerAddrSource(c *lowerContext, expr hir.Expr) Value {
	if expr == nil {
		return nil
	}
	if ident, ok := expr.(*hir.Ident); ok && ident.LocalID >= 0 {
		return &LocalValue{
			baseValue: baseValue{Location: ident.Loc(), ExprType: ident.Type()},
			LocalID:   ident.LocalID,
		}
	}
	if resolved := lowerResolvedName(c, expr.SourceExpr(), expr.Loc(), expr.Type()); resolved != nil {
		return resolved
	}
	return lowerValue(c, expr)
}

func lowerMethodReceiverValue(c *lowerContext, expr hir.Expr, receiverType typeinfo.Type) Value {
	if expr == nil {
		return nil
	}
	if receiverType == nil || typeinfo.Equal(expr.Type(), receiverType) {
		return lowerValue(c, expr)
	}
	if ref, ok := receiverType.(*typeinfo.RefType); ok {
		return &AddrOfValue{
			baseValue: baseValue{Location: expr.Loc(), ExprType: receiverType},
			Source:    lowerAddrSource(c, expr),
			Mutable:   ref.Mutable,
		}
	}
	return lowerValue(c, expr)
}

func lowerNameValue(c *lowerContext, source ast.Expr, loc source.Location, typ typeinfo.Type, fallback []string) Value {
	if resolved := lowerResolvedName(c, source, loc, typ); resolved != nil {
		return resolved
	}
	return &NameValue{baseValue: baseValue{Location: loc, ExprType: typ}, Path: append([]string(nil), fallback...)}
}

func canonicalResolvedPath(c *lowerContext, resolution *binding.Resolution) []string {
	if resolution == nil || resolution.Symbol == nil {
		return nil
	}
	name := resolution.Symbol.Name
	if (resolution.Symbol.Kind == symbols.SymbolStatic || resolution.Symbol.Kind == symbols.SymbolFunc) && resolution.Symbol.OwnerType != "" {
		name = resolution.Symbol.OwnerType + "__" + name
	}
	if resolution.ImportPath == "" || resolution.ImportPath == c.importPath {
		return []string{name}
	}
	parts := strings.Split(resolution.ImportPath, "/")
	parts = append(parts, name)
	return parts
}

func lowerStructView(typ typeinfo.Type) (*typeinfo.StructType, bool) {
	switch t := typ.(type) {
	case *typeinfo.PointerType:
		return lowerStructView(t.Inner)
	case *typeinfo.RefType:
		return lowerStructView(t.Inner)
	case *typeinfo.RawPtrType:
		return lowerStructView(t.Inner)
	case *typeinfo.NamedType:
		if t.Decl == nil {
			return nil, false
		}
		structDecl, ok := t.Decl.Type.(*ast.StructType)
		if !ok {
			return nil, false
		}
		fields := make([]*typeinfo.StructField, 0, len(structDecl.Fields))
		fieldMap := make(map[string]*typeinfo.StructField, len(structDecl.Fields))
		for _, field := range structDecl.Fields {
			if field == nil {
				continue
			}
			entry := &typeinfo.StructField{Name: field.Name.Text()}
			fields = append(fields, entry)
			fieldMap[field.Name.Text()] = entry
		}
		return &typeinfo.StructType{Fields: fieldMap, OrderedFields: fields}, true
	case *typeinfo.StructType:
		return t, true
	default:
		return nil, false
	}
}

func withValueContext(value Value, loc source.Location, typ typeinfo.Type) Value {
	switch v := value.(type) {
	case nil:
		return nil
	case *NameValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		copy.Path = append([]string(nil), v.Path...)
		return &copy
	case *LocalValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *NumberValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *BoolValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *StringValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *NoneValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *UnaryValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *AddrOfValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *LoadValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *BinaryValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *PostfixValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *CallValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		copy.Args = append([]Value(nil), v.Args...)
		return &copy
	case *FieldLoadValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *FieldValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *CastValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		return &copy
	case *CompositeValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		copy.Items = append([]CompositeItem(nil), v.Items...)
		return &copy
	case *InterfaceValue:
		copy := *v
		copy.Location = loc
		copy.ExprType = typ
		copy.Methods = append([]InterfaceMethodLink(nil), v.Methods...)
		return &copy
	default:
		return value
	}
}

func blockID(block *cfg.Block) int {
	if block == nil {
		return -1
	}
	return block.ID
}

// lowerReceiverNamed unwraps receiver view types to get the underlying NamedType for a method receiver.
func lowerReceiverNamed(typ typeinfo.Type) *typeinfo.NamedType {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		if t.Decl != nil {
			if _, ok := t.Decl.Type.(*ast.InterfaceType); ok {
				return nil
			}
		}
		return t
	case *typeinfo.RefType:
		return lowerReceiverNamed(t.Inner)
	case *typeinfo.PointerType:
		return lowerReceiverNamed(t.Inner)
	}
	return nil
}

func lowerInterfaceCoercion(lowerCtx *lowerContext, source, target typeinfo.Type) ([]InterfaceMethodLink, typeinfo.Type, bool) {
	targetIface, ok := lowerInterfaceMethods(target)
	if !ok {
		return nil, nil, false
	}
	if source == nil || typeinfo.Equal(source, target) || lowerIsInterfaceType(source) {
		return nil, nil, false
	}
	if lowerCtx == nil || lowerCtx.lookupMethod == nil {
		return nil, nil, false
	}
	out := make([]InterfaceMethodLink, 0, len(targetIface))
	for _, method := range targetIface {
		if method == nil || method.Static {
			continue
		}
		name := method.Name.Text()
		path, ok := lowerCtx.lookupMethod(source, name)
		if !ok || len(path) == 0 {
			return nil, nil, false
		}
		out = append(out, InterfaceMethodLink{Name: name, Path: append([]string(nil), path...)})
	}
	return out, source, true
}

func lowerInterfaceMethods(typ typeinfo.Type) ([]*ast.InterfaceMethod, bool) {
	named, ok := typ.(*typeinfo.NamedType)
	if !ok || named == nil || named.Decl == nil {
		return nil, false
	}
	ifaceDecl, ok := named.Decl.Type.(*ast.InterfaceType)
	if !ok || ifaceDecl == nil {
		return nil, false
	}
	out := make([]*ast.InterfaceMethod, 0, len(ifaceDecl.Methods))
	for _, method := range ifaceDecl.Methods {
		if method != nil && method.Name != nil {
			out = append(out, method)
		}
	}
	return out, true
}

func lowerIsInterfaceType(typ typeinfo.Type) bool {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		if t.Decl == nil {
			return false
		}
		_, ok := t.Decl.Type.(*ast.InterfaceType)
		return ok
	case *typeinfo.InterfaceType:
		return true
	default:
		return false
	}
}

// lowerMethodSymbolPath builds the NameValue.Path for a method call.
// ModuleKey format is "origin:importPath" (e.g. "local:math/vec2").
// Same-module methods get a single-element path so qbeSymbol can add the module prefix.
func lowerMethodSymbolPath(c *lowerContext, named *typeinfo.NamedType, methodName string) []string {
	if named == nil {
		return []string{methodName}
	}
	leaf := lowerMethodLinkLeaf(named.Name, methodName)
	importPath := named.ModuleKey
	if _, after, ok := strings.Cut(named.ModuleKey, ":"); ok {
		importPath = after
	}
	if importPath == "" || importPath == c.importPath {
		return []string{leaf}
	}
	parts := strings.Split(importPath, "/")
	return append(parts, leaf)
}

func lowerFunctionLinkName(fn *hir.Func) string {
	if fn == nil {
		return ""
	}
	if fn.OwnerType != "" {
		return lowerMethodLinkLeaf(fn.OwnerType, fn.Name)
	}
	if fn.Receiver == nil {
		return ""
	}
	if named := lowerReceiverNamed(fn.Receiver.Type); named != nil {
		return lowerMethodLinkLeaf(named.Name, fn.Name)
	}
	return ""
}

func lowerMethodLinkLeaf(typeName, methodName string) string {
	if typeName == "" {
		return methodName
	}
	return typeName + "__" + methodName
}
