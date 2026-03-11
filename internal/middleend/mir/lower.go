package mir

import (
	"compiler/internal/cfg"
	fast "compiler/internal/frontend/ast"
	"compiler/internal/middleend/hir"
	"compiler/internal/semantics/binding"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/typeinfo"
	"compiler/internal/source"
)

type lowerContext struct {
	localsByName map[string]int
	locals       []*Local
	bindings     *binding.ModuleInfo
	globalConsts map[fast.Node]hir.Expr
	localConsts  map[string]hir.Expr
}

func LowerModule(cfgMod *cfg.Module, hirMod *hir.Module, bindings *binding.ModuleInfo, globalConsts map[fast.Node]hir.Expr) *Module {
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
		out.Globals = append(out.Globals, lowerGlobal(global, bindings, globalConsts))
	}
	for _, fn := range cfgMod.Functions {
		out.Functions = append(out.Functions, lowerFunction(fn, bindings, globalConsts))
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
		Fields:       make([]*StructFieldDecl, 0, len(decl.Fields)),
		StaticFields: make([]*StructFieldDecl, 0, len(decl.StaticFields)),
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
	for _, field := range decl.StaticFields {
		if field == nil {
			continue
		}
		out.StaticFields = append(out.StaticFields, &StructFieldDecl{
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
		if method == nil {
			continue
		}
		entry := &InterfaceMethodDecl{
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

func lowerGlobal(global *hir.Global, bindings *binding.ModuleInfo, globalConsts map[fast.Node]hir.Expr) *Global {
	if global == nil {
		return nil
	}
	lowerCtx := &lowerContext{bindings: bindings, globalConsts: globalConsts}
	return &Global{
		Name:     global.Name,
		Mutable:  global.Mutable,
		Constant: global.Constant,
		Type:     global.Type,
		Init:     lowerValue(lowerCtx, global.Value),
		Location: global.Location,
	}
}

func lowerFunction(fn *cfg.Function, bindings *binding.ModuleInfo, globalConsts map[fast.Node]hir.Expr) *Function {
	if fn == nil || fn.Source == nil {
		return nil
	}
	lowerCtx := newLowerContext(fn.Source, bindings, globalConsts)
	out := &Function{
		Name:       fn.Name,
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
		out.Receiver = &Param{
			Name:     fn.Source.Receiver.Name,
			LocalID:  lowerCtx.lookupLocalID(fn.Source.Receiver.Name),
			Type:     fn.Source.Receiver.Type,
			Location: fn.Source.Receiver.Location,
		}
	}
	for _, param := range fn.Source.Params {
		if param == nil {
			continue
		}
		out.Params = append(out.Params, &Param{
			Name:       param.Name,
			LocalID:    lowerCtx.lookupLocalID(param.Name),
			Type:       param.Type,
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
		return &AssignInstr{baseInstr: baseInstr{Location: s.Loc()}, TargetID: lowerCtx.lookupLocalID(s.Name), Value: lowerValue(lowerCtx, s.Value)}
	case *hir.ConstStmt:
		return nil
	case *hir.ExprStmt:
		return &EvalInstr{baseInstr: baseInstr{Location: s.Loc()}, Value: lowerValue(lowerCtx, s.Value)}
	case *hir.AssignStmt:
		if ident, ok := s.Left.(*hir.Ident); ok && len(ident.Path) == 1 {
			return &AssignInstr{baseInstr: baseInstr{Location: s.Loc()}, TargetID: lowerCtx.lookupLocalID(ident.Path[0]), Value: lowerValue(lowerCtx, s.Right)}
		}
		if sel, ok := s.Left.(*hir.SelectorExpr); ok {
			return &StoreFieldInstr{
				baseInstr:  baseInstr{Location: s.Loc()},
				Base:       lowerValue(lowerCtx, sel.Left),
				FieldIndex: lowerCtx.fieldIndex(sel.Left.Type(), sel.Name),
				Value:      lowerValue(lowerCtx, s.Right),
			}
		}
		return &StoreInstr{baseInstr: baseInstr{Location: s.Loc()}, Target: lowerPlace(lowerCtx, s.Left), Value: lowerValue(lowerCtx, s.Right)}
	case *hir.DeferStmt:
		return &DeferInstr{baseInstr: baseInstr{Location: s.Loc()}, Body: lowerDeferredBody(lowerCtx, s.Body)}
	case *hir.LockStmt:
		return &LockInstr{baseInstr: baseInstr{Location: s.Loc()}, Value: lowerValue(lowerCtx, s.Value), LocalID: lowerCtx.lookupLocalID(s.Name)}
	case *hir.UnsafeStmt:
		return &UnsafeInstr{baseInstr: baseInstr{Location: s.Loc()}}
	default:
		return nil
	}
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
		return &ReturnTerm{baseTerm: baseTerm{Location: loc}, Value: lowerValue(lowerCtx, t.Value), CleanupID: cleanupID}
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
			if value, ok := lowerCtx.lookupConstExpr(e.Path[0], e.SourceExpr()); ok {
				return withValueContext(lowerValue(lowerCtx, value), e.Loc(), e.Type())
			}
		}
		if value, ok := lowerCtx.lookupResolvedConstExpr(e.SourceExpr()); ok {
			return withValueContext(lowerValue(lowerCtx, value), e.Loc(), e.Type())
		}
		if len(e.Path) == 1 {
			if id, ok := lowerCtx.localID(e.Path[0]); ok {
				return &LocalValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, LocalID: id}
			}
		}
		return &NameValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Path: append([]string(nil), e.Path...)}
	case *hir.NumberLit:
		return &NumberValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Value: e.Value}
	case *hir.StringLit:
		return &StringValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Value: e.Value}
	case *hir.NoneLit:
		return &NoneValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}}
	case *hir.PrefixExpr:
		switch e.Op {
		case "&":
			return &AddrOfValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Source: lowerValue(lowerCtx, e.Right), Mutable: false}
		case "&mut":
			return &AddrOfValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Source: lowerValue(lowerCtx, e.Right), Mutable: true}
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
		out := &CallValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Callee: lowerValue(lowerCtx, e.Callee), Args: make([]Value, 0, len(e.Args))}
		for _, arg := range e.Args {
			out.Args = append(out.Args, lowerValue(lowerCtx, arg))
		}
		return out
	case *hir.SelectorExpr:
		base := lowerValue(lowerCtx, e.Left)
		if index := lowerCtx.fieldIndex(e.Left.Type(), e.Name); index >= 0 {
			return &FieldLoadValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Base: base, FieldIndex: index}
		}
		return &FieldValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Base: base, FieldIndex: -1, MemberName: e.Name}
	case *hir.CastExpr:
		return &CastValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Left: lowerValue(lowerCtx, e.Left)}
	case *hir.CompositeLit:
		out := &CompositeValue{baseValue: baseValue{Location: e.Loc(), ExprType: e.Type()}, Items: make([]CompositeItem, 0, len(e.Items))}
		for _, item := range e.Items {
			out.Items = append(out.Items, CompositeItem{Name: item.Name, Value: lowerValue(lowerCtx, item.Value)})
		}
		return out
	default:
		return nil
	}
}

func collectLocals(fn *hir.Func) ([]*Local, map[string]int, map[string]hir.Expr) {
	if fn == nil || fn.Body == nil {
		return nil, nil, nil
	}
	locals := make([]*Local, 0)
	byName := make(map[string]int)
	consts := make(map[string]hir.Expr)
	add := func(name string, typ typeinfo.Type, mutable, constant bool, loc source.Location) {
		if name == "" {
			return
		}
		if _, ok := byName[name]; ok {
			return
		}
		id := len(locals)
		byName[name] = id
		locals = append(locals, &Local{ID: id, Name: name, Type: typ, Mutable: mutable, Constant: constant, Location: loc})
	}
	if fn.Receiver != nil {
		add(fn.Receiver.Name, fn.Receiver.Type, true, false, fn.Receiver.Location)
	}
	for _, param := range fn.Params {
		if param == nil {
			continue
		}
		add(param.Name, param.Type, false, false, param.Location)
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
			add(s.Name, s.Type, s.Mutable, false, s.Loc())
		case *hir.ConstStmt:
			consts[s.Name] = s.Value
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
			if s.IndexName != "" {
				add(s.IndexName, &typeinfo.BuiltinType{Name: "usize"}, false, false, s.Loc())
			}
			if s.ValueName != "" {
				add(s.ValueName, typeinfo.UnknownType{}, false, false, s.Loc())
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
			add(s.Name, s.Value.Type(), true, false, s.Loc())
			walkStmt(s.Body)
		case *hir.UnsafeStmt:
			walkStmt(s.Body)
		}
	}
	walkStmt(fn.Body)
	return locals, byName, consts
}

func lowerPlace(lowerCtx *lowerContext, expr hir.Expr) Place {
	switch e := expr.(type) {
	case nil:
		return nil
	case *hir.Ident:
		if len(e.Path) == 1 {
			return &LocalPlace{basePlace: basePlace{Location: e.Loc()}, LocalID: lowerCtx.lookupLocalID(e.Path[0])}
		}
		return nil
	case *hir.SelectorExpr:
		return &FieldPlace{basePlace: basePlace{Location: e.Loc()}, Base: lowerPlace(lowerCtx, e.Left), FieldIndex: lowerCtx.fieldIndex(e.Left.Type(), e.Name)}
	default:
		return nil
	}
}

func newLowerContext(fn *hir.Func, bindings *binding.ModuleInfo, globalConsts map[fast.Node]hir.Expr) *lowerContext {
	locals, byName, consts := collectLocals(fn)
	return &lowerContext{
		locals:       locals,
		localsByName: byName,
		bindings:     bindings,
		globalConsts: globalConsts,
		localConsts:  consts,
	}
}

func (c *lowerContext) localID(name string) (int, bool) {
	if c == nil {
		return -1, false
	}
	id, ok := c.localsByName[name]
	return id, ok
}

func (c *lowerContext) lookupLocalID(name string) int {
	if id, ok := c.localID(name); ok {
		return id
	}
	return -1
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

func (c *lowerContext) lookupConstExpr(name string, source fast.Expr) (hir.Expr, bool) {
	if c == nil {
		return nil, false
	}
	if expr, ok := c.localConsts[name]; ok && expr != nil {
		return expr, true
	}
	return c.lookupResolvedConstExpr(source)
}

func (c *lowerContext) lookupResolvedConstExpr(source fast.Expr) (hir.Expr, bool) {
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

func lowerStructView(typ typeinfo.Type) (*typeinfo.StructType, bool) {
	switch t := typ.(type) {
	case *typeinfo.PointerType:
		return lowerStructView(t.Inner)
	case *typeinfo.NamedType:
		if t.Decl == nil {
			return nil, false
		}
		structDecl, ok := t.Decl.Type.(*fast.StructType)
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
