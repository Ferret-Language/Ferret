package hir

import (
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
	"strconv"
	"strings"
)

func Specialize(mod *Module, types *typeinfo.ModuleInfo, bindings *binding.ModuleInfo) *Module {
	if mod == nil || types == nil || bindings == nil {
		return mod
	}
	p := &specializer{
		module:               mod,
		types:                types,
		bindings:             bindings,
		templates:            make(map[symbols.SymbolID]*Func),
		requests:             make(map[string]*specializationRequest),
		ownerMethodTemplates: make(map[string][]*Func),
		typeTemplates:        make(map[string]*TypeDecl),
		typeRequests:         make(map[string]*typeSpecializationRequest),
	}
	for _, fn := range mod.Functions {
		if p.isTemplate(fn) {
			sym := bindings.FunctionSymbols[fn.Source]
			if sym != nil {
				p.templates[sym.ID] = fn
			}
			if fn.OwnerType != "" {
				ownerKey := mod.Key + "::" + fn.OwnerType
				p.ownerMethodTemplates[ownerKey] = append(p.ownerMethodTemplates[ownerKey], fn)
			}
		}
	}
	for _, decl := range mod.Types {
		if p.isTypeTemplate(decl) {
			key := decl.Named.ModuleKey + "::" + decl.Name
			p.typeTemplates[key] = decl
		}
	}
	if len(p.templates) == 0 && len(p.typeTemplates) == 0 {
		return mod
	}

	out := &Module{
		Key:        mod.Key,
		ImportPath: mod.ImportPath,
		FilePath:   mod.FilePath,
		Source:     mod.Source,
		Types:      make([]*TypeDecl, 0, len(mod.Types)),
		Globals:    make([]*Global, 0, len(mod.Globals)),
		Functions:  make([]*Func, 0, len(mod.Functions)),
	}
	for _, decl := range mod.Types {
		if p.isTypeTemplate(decl) {
			continue
		}
		out.Types = append(out.Types, p.cloneTypeDecl(decl, nil, ""))
	}
	for _, global := range mod.Globals {
		out.Globals = append(out.Globals, p.cloneGlobal(global, nil))
	}
	for _, fn := range mod.Functions {
		if p.isTemplate(fn) {
			continue
		}
		out.Functions = append(out.Functions, p.cloneFunc(fn, nil, ""))
	}
	for i := 0; i < len(p.pending); i++ {
		req := p.pending[i]
		if req == nil || req.emitted {
			continue
		}
		req.emitted = true
		out.Functions = append(out.Functions, p.cloneFunc(req.template, req.bindings, req.name))
	}
	for i := 0; i < len(p.typePending); i++ {
		req := p.typePending[i]
		if req == nil || req.emitted {
			continue
		}
		req.emitted = true
		out.Types = append(out.Types, p.specializeTypeDecl(req))
	}
	return out
}

type specializer struct {
	module               *Module
	types                *typeinfo.ModuleInfo
	bindings             *binding.ModuleInfo
	templates            map[symbols.SymbolID]*Func
	requests             map[string]*specializationRequest
	pending              []*specializationRequest
	ownerMethodTemplates map[string][]*Func

	typeTemplates map[string]*TypeDecl
	typeRequests  map[string]*typeSpecializationRequest
	typePending   []*typeSpecializationRequest
}

type specializationRequest struct {
	template *Func
	bindings map[*typeinfo.TypeParam]typeinfo.Type
	name     string
	emitted  bool
}

type typeSpecializationRequest struct {
	template *TypeDecl
	name     string
	bindings map[*typeinfo.TypeParam]typeinfo.Type
	decl     *TypeDecl
	emitted  bool
}

func (s *specializer) isTemplate(fn *Func) bool {
	if fn == nil || fn.Source == nil || fn.IsExtern || fn.Body == nil {
		return false
	}
	if len(fn.Source.TypeParams) > 0 {
		return true
	}
	if fn.Receiver != nil && typeHasTypeParam(fn.Receiver.Type) {
		return true
	}
	for _, param := range fn.Params {
		if param != nil && typeHasTypeParam(param.Type) {
			return true
		}
	}
	return typeHasTypeParam(fn.Result)
}

func (s *specializer) isTypeTemplate(decl *TypeDecl) bool {
	return decl != nil && decl.Source != nil && len(decl.Source.TypeParams) > 0
}

func (s *specializer) cloneGlobal(global *Global, bindings map[*typeinfo.TypeParam]typeinfo.Type) *Global {
	if global == nil {
		return nil
	}
	out := *global
	out.Type = s.substituteType(global.Type, bindings)
	out.Value = s.cloneExpr(global.Value, bindings)
	return &out
}

func (s *specializer) cloneTypeDecl(decl *TypeDecl, bindings map[*typeinfo.TypeParam]typeinfo.Type, name string) *TypeDecl {
	if decl == nil {
		return nil
	}
	out := *decl
	if name != "" {
		out.Name = name
	}
	if decl.Named != nil {
		named := *decl.Named
		named.Name = out.Name
		named.TypeArgs = nil
		out.Named = &named
	}
	out.Underlying = s.substituteType(decl.Underlying, bindings)
	if decl.Struct != nil {
		st := &StructTypeDecl{Fields: make([]*StructFieldDecl, 0, len(decl.Struct.Fields))}
		for _, field := range decl.Struct.Fields {
			if field == nil {
				continue
			}
			copy := *field
			copy.Type = s.substituteType(field.Type, bindings)
			copy.Default = s.cloneExpr(field.Default, bindings)
			st.Fields = append(st.Fields, &copy)
		}
		out.Struct = st
	}
	if decl.Interface != nil {
		iface := &InterfaceTypeDecl{Methods: make([]*InterfaceMethodDecl, 0, len(decl.Interface.Methods))}
		for _, method := range decl.Interface.Methods {
			if method == nil {
				continue
			}
			copy := *method
			copy.Result = s.substituteType(method.Result, bindings)
			copy.Params = make([]*Param, 0, len(method.Params))
			for _, param := range method.Params {
				if param == nil {
					continue
				}
				paramCopy := *param
				paramCopy.Type = s.substituteType(param.Type, bindings)
				copy.Params = append(copy.Params, &paramCopy)
			}
			iface.Methods = append(iface.Methods, &copy)
		}
		out.Interface = iface
	}
	if decl.Union != nil {
		union := &UnionTypeDecl{Members: make([]typeinfo.Type, 0, len(decl.Union.Members))}
		for _, member := range decl.Union.Members {
			union.Members = append(union.Members, s.substituteType(member, bindings))
		}
		out.Union = union
	}
	return &out
}

func (s *specializer) specializeTypeDecl(req *typeSpecializationRequest) *TypeDecl {
	if req == nil || req.template == nil {
		return nil
	}
	if req.decl != nil {
		return req.decl
	}
	decl := s.cloneTypeDecl(req.template, req.bindings, req.name)
	if decl == nil {
		return nil
	}
	decl.Source = nil
	if decl.Named != nil {
		decl.Named.Decl = nil
	}
	req.decl = decl
	return decl
}

func (s *specializer) cloneFunc(fn *Func, bindings map[*typeinfo.TypeParam]typeinfo.Type, name string) *Func {
	if fn == nil {
		return nil
	}
	out := *fn
	if name != "" {
		out.Name = name
	}
	out.Result = s.substituteType(fn.Result, bindings)
	if fn.Receiver != nil {
		recv := *fn.Receiver
		recv.Type = s.substituteType(fn.Receiver.Type, bindings)
		out.Receiver = &recv
		if out.OwnerType != "" {
			if named, ok := typeinfo.ReceiverBaseNamedType(recv.Type); ok && named != nil && named.Name != "" {
				out.OwnerType = named.Name
			}
		}
	}
	if len(fn.Params) > 0 {
		out.Params = make([]*Param, 0, len(fn.Params))
		for _, param := range fn.Params {
			if param == nil {
				continue
			}
			copy := *param
			copy.Type = s.substituteType(param.Type, bindings)
			out.Params = append(out.Params, &copy)
		}
	}
	out.Body = s.cloneBlock(fn.Body, bindings)
	return &out
}

func (s *specializer) cloneBlock(block *BlockStmt, bindings map[*typeinfo.TypeParam]typeinfo.Type) *BlockStmt {
	if block == nil {
		return nil
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(block.Stmts))}
	SetStmtLocation(out, block.Loc())
	for _, stmt := range block.Stmts {
		if cloned := s.cloneStmt(stmt, bindings); cloned != nil {
			out.Stmts = append(out.Stmts, cloned)
		}
	}
	return out
}

func (s *specializer) cloneStmt(stmt Stmt, bindings map[*typeinfo.TypeParam]typeinfo.Type) Stmt {
	switch st := stmt.(type) {
	case nil:
		return nil
	case *BlockStmt:
		return s.cloneBlock(st, bindings)
	case *LetStmt:
		out := *st
		out.Type = s.substituteType(st.Type, bindings)
		s.requestInterfaceMethodSpecializations(st.Value, st.Type, bindings)
		out.Value = s.cloneExpr(st.Value, bindings)
		return &out
	case *ConstStmt:
		out := *st
		out.Type = s.substituteType(st.Type, bindings)
		s.requestInterfaceMethodSpecializations(st.Value, st.Type, bindings)
		out.Value = s.cloneExpr(st.Value, bindings)
		return &out
	case *ReturnStmt:
		out := *st
		out.Value = s.cloneExpr(st.Value, bindings)
		return &out
	case *ExprStmt:
		out := *st
		out.Value = s.cloneExpr(st.Value, bindings)
		return &out
	case *AssignStmt:
		out := *st
		out.Left = s.cloneExpr(st.Left, bindings)
		s.requestInterfaceMethodSpecializations(st.Right, st.Left.Type(), bindings)
		out.Right = s.cloneExpr(st.Right, bindings)
		return &out
	case *IfStmt:
		out := *st
		out.Cond = s.cloneExpr(st.Cond, bindings)
		out.Then = s.cloneBlock(st.Then, bindings)
		out.Else = s.cloneStmt(st.Else, bindings)
		return &out
	case *MatchStmt:
		out := *st
		out.Value = s.cloneExpr(st.Value, bindings)
		out.Arms = make([]*MatchArm, 0, len(st.Arms))
		for _, arm := range st.Arms {
			if arm == nil {
				continue
			}
			copy := *arm
			copy.Pattern = s.cloneExpr(arm.Pattern, bindings)
			copy.TypePattern = s.substituteType(arm.TypePattern, bindings)
			copy.Body = s.cloneBlock(arm.Body, bindings)
			out.Arms = append(out.Arms, &copy)
		}
		return &out
	case *WhileStmt:
		out := *st
		out.Cond = s.cloneExpr(st.Cond, bindings)
		out.Body = s.cloneBlock(st.Body, bindings)
		return &out
	case *ForStmt:
		out := *st
		out.Iterable = s.cloneExpr(st.Iterable, bindings)
		out.Body = s.cloneBlock(st.Body, bindings)
		return &out
	case *LoopStmt:
		out := *st
		out.Init = s.cloneStmt(st.Init, bindings)
		out.Cond = s.cloneExpr(st.Cond, bindings)
		out.Post = s.cloneStmt(st.Post, bindings)
		out.Body = s.cloneBlock(st.Body, bindings)
		return &out
	case *LabelStmt:
		out := *st
		out.Stmt = s.cloneStmt(st.Stmt, bindings)
		return &out
	case *DeferStmt:
		out := *st
		out.Body = s.cloneStmt(st.Body, bindings)
		return &out
	case *ReleaseStmt:
		out := *st
		out.Value = s.cloneExpr(st.Value, bindings)
		return &out
	case *PanicStmt:
		out := *st
		out.Value = s.cloneExpr(st.Value, bindings)
		return &out
	case *LockStmt:
		out := *st
		out.Value = s.cloneExpr(st.Value, bindings)
		out.Body = s.cloneBlock(st.Body, bindings)
		return &out
	case *UnsafeStmt:
		out := *st
		out.Body = s.cloneBlock(st.Body, bindings)
		return &out
	default:
		return stmt
	}
}

func (s *specializer) cloneExpr(expr Expr, bindings map[*typeinfo.TypeParam]typeinfo.Type) Expr {
	switch ex := expr.(type) {
	case nil:
		return nil
	case *BadExpr:
		out := *ex
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *Ident:
		out := *ex
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *NumberLit:
		out := *ex
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *StringLit:
		out := *ex
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *NoneLit:
		out := *ex
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *PrefixExpr:
		out := *ex
		out.Right = s.cloneExpr(ex.Right, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *BinaryExpr:
		left := s.cloneExpr(ex.Left, bindings)
		right := s.cloneExpr(ex.Right, bindings)
		out := *ex
		out.Left = left
		out.Right = right
		out.ExprType = s.specializeBinaryType(ex, bindings, left, right)
		return &out
	case *PostfixExpr:
		out := *ex
		out.Left = s.cloneExpr(ex.Left, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *CallExpr:
		out := *ex
		out.MethodReceiver = s.substituteType(ex.MethodReceiver, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
		out.Args = make([]Expr, 0, len(ex.Args))
		var calleeFnType *typeinfo.FuncType
		if fnType, ok := s.substituteType(ex.Callee.Type(), bindings).(*typeinfo.FuncType); ok {
			calleeFnType = fnType
		}
		for _, arg := range ex.Args {
			if calleeFnType != nil && len(out.Args) < len(calleeFnType.Params) {
				s.requestInterfaceMethodSpecializations(arg, calleeFnType.Params[len(out.Args)].Type, bindings)
			}
			out.Args = append(out.Args, s.cloneExpr(arg, bindings))
		}
		clonedCallee := s.cloneExpr(ex.Callee, bindings)
		if name, fnType, ok := s.specializedCallName(ex, bindings); ok {
			if sel, ok := clonedCallee.(*SelectorExpr); ok {
				sel.Name = name
				sel.ExprType = fnType
				out.Callee = sel
				return &out
			}
			if ident, ok := clonedCallee.(*Ident); ok {
				path := append([]string(nil), ident.Path...)
				if len(path) == 0 {
					path = []string{name}
				} else {
					path[len(path)-1] = name
				}
				ident.Path = path
				ident.ExprType = fnType
				ident.Source = nil
				out.Callee = ident
				return &out
			}
			out.Callee = &Ident{
				baseExpr: baseExpr{ExprType: fnType, Location: ex.Callee.Loc()},
				Path:     []string{name},
				LocalID:  -1,
			}
			return &out
		}
		out.Callee = clonedCallee
		return &out
	case *ConstructorCallExpr:
		out := *ex
		out.ExprType = s.substituteType(ex.Type(), bindings)
		out.Args = make([]Expr, 0, len(ex.Args))
		for _, arg := range ex.Args {
			out.Args = append(out.Args, s.cloneExpr(arg, bindings))
		}
		return &out
	case *SelectorExpr:
		out := *ex
		out.Left = s.cloneExpr(ex.Left, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *CastExpr:
		out := *ex
		out.Left = s.cloneExpr(ex.Left, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *IsExpr:
		out := *ex
		out.Left = s.cloneExpr(ex.Left, bindings)
		out.Target = s.substituteType(ex.Target, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *MatchExpr:
		out := *ex
		out.Value = s.cloneExpr(ex.Value, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
		out.Arms = make([]*MatchArm, 0, len(ex.Arms))
		for _, arm := range ex.Arms {
			if arm == nil {
				continue
			}
			copy := *arm
			copy.Pattern = s.cloneExpr(arm.Pattern, bindings)
			copy.TypePattern = s.substituteType(arm.TypePattern, bindings)
			copy.Body = s.cloneBlock(arm.Body, bindings)
			out.Arms = append(out.Arms, &copy)
		}
		return &out
	case *CatchExpr:
		out := *ex
		out.Left = s.cloneExpr(ex.Left, bindings)
		out.Fallback = s.cloneExpr(ex.Fallback, bindings)
		out.Handler = s.cloneBlock(ex.Handler, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	case *CompositeLit:
		out := *ex
		out.ExprType = s.substituteType(ex.Type(), bindings)
		out.Items = make([]CompositeItem, 0, len(ex.Items))
		for _, item := range ex.Items {
			out.Items = append(out.Items, CompositeItem{Name: item.Name, Value: s.cloneExpr(item.Value, bindings)})
		}
		return &out
	case *IndexExpr:
		out := *ex
		out.Left = s.cloneExpr(ex.Left, bindings)
		out.Index = s.cloneExpr(ex.Index, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
		return &out
	default:
		return expr
	}
}

func (s *specializer) specializedCallName(expr *CallExpr, bindings map[*typeinfo.TypeParam]typeinfo.Type) (string, *typeinfo.FuncType, bool) {
	if expr == nil || expr.Callee == nil || expr.Callee.SourceExpr() == nil {
		return "", nil, false
	}
	resolution := s.bindings.Nodes[expr.Callee.SourceExpr()]
	if resolution == nil || resolution.Kind != binding.ResolutionSymbol || resolution.Symbol == nil {
		return "", nil, false
	}
	template := s.templates[resolution.Symbol.ID]
	if template == nil {
		return "", nil, false
	}
	fnType, ok := s.substituteType(expr.Callee.Type(), bindings).(*typeinfo.FuncType)
	if !ok || fnType == nil || len(fnType.TypeParams) != 0 {
		return "", nil, false
	}
	receiverType := expr.MethodReceiver
	if receiverType == nil && s.types != nil {
		if source := expr.SourceExpr(); source != nil {
			if inferred, ok := s.types.LookupMethodReceiver(source); ok {
				receiverType = inferred
			}
		}
		if receiverType == nil && expr.Callee != nil && expr.Callee.SourceExpr() != nil {
			if inferred, ok := s.types.LookupMethodReceiver(expr.Callee.SourceExpr()); ok {
				receiverType = inferred
			}
		}
	}
	if receiverType == nil && template.Receiver != nil {
		if sel, ok := expr.Callee.(*SelectorExpr); ok && sel.Left != nil {
			if key, ok := typeinfo.ReceiverKeyFromType(template.Receiver.Type); ok {
				receiverType = typeinfo.ApplyReceiverShape(sel.Left.Type(), key.Kind)
			}
		}
	}
	req := s.requestSpecialization(template, fnType, receiverType)
	if req == nil {
		return "", nil, false
	}
	return req.name, fnType, true
}

func (s *specializer) requestSpecialization(template *Func, fnType *typeinfo.FuncType, receiverType typeinfo.Type) *specializationRequest {
	if template == nil || template.Source == nil || fnType == nil {
		return nil
	}
	bindings := inferSpecializationBindings(template, fnType, receiverType)
	return s.requestSpecializationWithBindings(template, bindings)
}

func (s *specializer) requestSpecializationWithBindings(template *Func, inferred map[*typeinfo.TypeParam]typeinfo.Type) *specializationRequest {
	if template == nil || template.Source == nil {
		return nil
	}
	bindings := make(map[*typeinfo.TypeParam]typeinfo.Type, len(inferred))
	for param, bound := range inferred {
		if param == nil || bound == nil {
			continue
		}
		bindings[param] = bound
	}
	for _, name := range specializedFuncParamNames(template) {
		if lookupTypeBinding(bindings, name) == nil {
			return nil
		}
	}
	name := specializedFuncName(template, bindings)
	key := name + "#" + template.OwnerType
	if template.Receiver != nil && template.Receiver.Type != nil {
		key += "#" + template.Receiver.Type.String()
	}
	if req, ok := s.requests[key]; ok {
		return req
	}
	req := &specializationRequest{
		template: template,
		bindings: bindings,
		name:     name,
	}
	s.requests[key] = req
	s.pending = append(s.pending, req)
	return req
}

func inferSpecializationBindings(template *Func, instantiated *typeinfo.FuncType, receiverType typeinfo.Type) map[*typeinfo.TypeParam]typeinfo.Type {
	out := make(map[*typeinfo.TypeParam]typeinfo.Type)
	if template == nil || instantiated == nil {
		return out
	}
	if template.Receiver != nil && receiverType != nil {
		inferTypeBindings(template.Receiver.Type, receiverType, out)
	}
	for i, param := range template.Params {
		if param == nil || i >= len(instantiated.Params) {
			continue
		}
		inferTypeBindings(param.Type, instantiated.Params[i].Type, out)
	}
	inferTypeBindings(template.Result, instantiated.Result, out)
	return out
}

func inferTypeBindings(pattern, actual typeinfo.Type, bindings map[*typeinfo.TypeParam]typeinfo.Type) {
	switch p := pattern.(type) {
	case nil:
		return
	case *typeinfo.TypeParam:
		if actual != nil {
			bindings[p] = actual
		}
	case *typeinfo.PointerType:
		if got, ok := actual.(*typeinfo.PointerType); ok {
			inferTypeBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.RefType:
		if got, ok := actual.(*typeinfo.RefType); ok {
			inferTypeBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.RawPtrType:
		if got, ok := actual.(*typeinfo.RawPtrType); ok {
			inferTypeBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.OptionalType:
		if got, ok := actual.(*typeinfo.OptionalType); ok {
			inferTypeBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.ErrorUnionType:
		if got, ok := actual.(*typeinfo.ErrorUnionType); ok {
			inferTypeBindings(p.Error, got.Error, bindings)
			inferTypeBindings(p.Value, got.Value, bindings)
		}
	case *typeinfo.ArrayType:
		if got, ok := actual.(*typeinfo.ArrayType); ok {
			inferTypeBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.SliceType:
		if got, ok := actual.(*typeinfo.SliceType); ok {
			inferTypeBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.TupleType:
		if got, ok := actual.(*typeinfo.TupleType); ok && len(p.Elems) == len(got.Elems) {
			for i := range p.Elems {
				inferTypeBindings(p.Elems[i], got.Elems[i], bindings)
			}
		}
	case *typeinfo.NamedType:
		got, ok := actual.(*typeinfo.NamedType)
		if !ok || p.Name != got.Name || len(p.TypeArgs) != len(got.TypeArgs) {
			return
		}
		for i := range p.TypeArgs {
			inferTypeBindings(p.TypeArgs[i], got.TypeArgs[i], bindings)
		}
	}
}

func specializedFuncName(template *Func, bindings map[*typeinfo.TypeParam]typeinfo.Type) string {
	var b strings.Builder
	b.WriteString(template.Name)
	b.WriteString("$")
	for i, name := range specializedFuncParamNames(template) {
		if i > 0 {
			b.WriteString("_")
		}
		b.WriteString(name)
		b.WriteString("_")
		b.WriteString(specializedTypeTag(lookupTypeBinding(bindings, name)))
	}
	return b.String()
}

func specializedFuncParamNames(template *Func) []string {
	if template == nil {
		return nil
	}
	names := make([]string, 0, len(template.Source.TypeParams)+2)
	added := make(map[string]struct{}, len(template.Source.TypeParams)+2)
	if template.Receiver != nil {
		if named, ok := typeinfo.ReceiverBaseNamedType(template.Receiver.Type); ok && named != nil && named.Decl != nil {
			for _, param := range named.Decl.TypeParams {
				if param.Name == nil {
					continue
				}
				name := param.Name.Text()
				if _, ok := added[name]; ok {
					continue
				}
				added[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	for i, param := range template.Source.TypeParams {
		name := "arg" + strconv.Itoa(i)
		if param.Name != nil {
			name = param.Name.Text()
		}
		if _, ok := added[name]; ok {
			continue
		}
		added[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func specializedTypeTag(typ typeinfo.Type) string {
	if typ == nil {
		return "void"
	}
	replacer := strings.NewReplacer(
		" ", "_",
		":", "_",
		"/", "_",
		"\\", "_",
		"*", "ptr_",
		"&", "ref_",
		"^", "raw_",
		"?", "opt_",
		"[", "arr_",
		"]", "_",
		"(", "t_",
		")", "_",
		",", "_",
		".", "_",
	)
	text := replacer.Replace(typ.String())
	text = strings.Trim(text, "_")
	if text == "" {
		return "type"
	}
	return text
}

func lookupTypeBinding(bindings map[*typeinfo.TypeParam]typeinfo.Type, name string) typeinfo.Type {
	for param, bound := range bindings {
		if param != nil && param.Name == name {
			return bound
		}
	}
	return nil
}

func (s *specializer) requestTypeSpecialization(named *typeinfo.NamedType) *typeSpecializationRequest {
	if named == nil || named.Decl == nil || len(named.Decl.TypeParams) == 0 || len(named.TypeArgs) == 0 {
		return nil
	}
	key := named.ModuleKey + "::" + named.Name
	template := s.typeTemplates[key]
	if template == nil {
		return nil
	}
	if len(named.TypeArgs) != len(named.Decl.TypeParams) {
		return nil
	}
	bindings := specializationBindings(named.Decl, named.TypeArgs)
	if len(bindings) == 0 {
		return nil
	}
	name := specializedTypeName(template, bindings)
	requestKey := named.ModuleKey + "::" + name
	if req, ok := s.typeRequests[requestKey]; ok {
		return req
	}
	req := &typeSpecializationRequest{
		template: template,
		name:     name,
		bindings: bindings,
	}
	s.typeRequests[requestKey] = req
	s.typePending = append(s.typePending, req)
	return req
}

func (s *specializer) requestInterfaceMethodSpecializations(value Expr, expected typeinfo.Type, bindings map[*typeinfo.TypeParam]typeinfo.Type) {
	if value == nil || expected == nil {
		return
	}
	expectedNamed, ok := s.substituteType(expected, bindings).(*typeinfo.NamedType)
	if !ok || expectedNamed == nil || expectedNamed.Decl == nil {
		return
	}
	ifaceDecl, ok := expectedNamed.Decl.Type.(*ast.InterfaceType)
	if !ok || ifaceDecl == nil {
		return
	}
	sourceType := s.substituteTypeWithoutTypeSpecialization(value.Type(), bindings)
	named, ok := typeinfo.ReceiverBaseNamedType(sourceType)
	if !ok || named == nil {
		return
	}
	methodTemplates := s.ownerMethodTemplates[named.ModuleKey+"::"+named.Name]
	if len(methodTemplates) == 0 {
		return
	}
	ownerBindings := specializationBindings(named.Decl, named.TypeArgs)
	if len(ownerBindings) == 0 {
		return
	}
	for _, method := range ifaceDecl.Methods {
		if method == nil || method.Static || method.Name == nil {
			continue
		}
		wantReceiver := typeinfo.ReceiverKindFromSyntax(method.Receiver)
		for _, candidate := range methodTemplates {
			if candidate == nil || candidate.Name != method.Name.Text() || candidate.Receiver == nil {
				continue
			}
			key, ok := typeinfo.ReceiverKeyFromType(candidate.Receiver.Type)
			if !ok || key.Kind != wantReceiver || len(candidate.Source.TypeParams) != 0 {
				continue
			}
			s.requestSpecializationWithBindings(candidate, ownerBindings)
			break
		}
	}
}

func specializedTypeName(template *TypeDecl, bindings map[*typeinfo.TypeParam]typeinfo.Type) string {
	if template == nil || template.Source == nil {
		return ""
	}
	args := make([]typeinfo.Type, 0, len(template.Source.TypeParams))
	for _, param := range template.Source.TypeParams {
		name := ""
		if param.Name != nil {
			name = param.Name.Text()
		}
		args = append(args, lookupTypeBinding(bindings, name))
	}
	return specializedTypeNameFromArgs(template.Name, template.Source.TypeParams, args)
}

func specializedTypeNameFromArgs(baseName string, params []ast.TypeParam, args []typeinfo.Type) string {
	if len(params) == 0 || len(args) == 0 {
		return baseName
	}
	var b strings.Builder
	b.WriteString(baseName)
	b.WriteString("$")
	for i, param := range params {
		if i > 0 {
			b.WriteString("_")
		}
		if param.Name == nil {
			b.WriteString("arg")
			b.WriteString(strconv.Itoa(i))
		} else {
			b.WriteString(param.Name.Text())
		}
		b.WriteString("_")
		if i < len(args) {
			b.WriteString(specializedTypeTag(args[i]))
		} else {
			b.WriteString("type")
		}
	}
	return b.String()
}

func specializationBindings(decl *ast.TypeDecl, args []typeinfo.Type) map[*typeinfo.TypeParam]typeinfo.Type {
	if decl == nil || len(decl.TypeParams) == 0 || len(args) == 0 || len(decl.TypeParams) != len(args) {
		return nil
	}
	out := make(map[*typeinfo.TypeParam]typeinfo.Type, len(args))
	for i, param := range decl.TypeParams {
		if param.Name == nil || args[i] == nil {
			continue
		}
		out[&typeinfo.TypeParam{Name: param.Name.Text(), Owner: decl}] = args[i]
	}
	return out
}

func (s *specializer) substituteType(typ typeinfo.Type, bindings map[*typeinfo.TypeParam]typeinfo.Type) typeinfo.Type {
	return s.substituteTypeInternal(typ, bindings, true)
}

func (s *specializer) substituteTypeWithoutTypeSpecialization(typ typeinfo.Type, bindings map[*typeinfo.TypeParam]typeinfo.Type) typeinfo.Type {
	return s.substituteTypeInternal(typ, bindings, false)
}

func (s *specializer) substituteTypeInternal(typ typeinfo.Type, bindings map[*typeinfo.TypeParam]typeinfo.Type, specializeNamed bool) typeinfo.Type {
	switch t := typ.(type) {
	case nil:
		return nil
	case *typeinfo.TypeParam:
		if bound := lookupBoundType(bindings, t); bound != nil {
			return bound
		}
		return typ
	case *typeinfo.PointerType:
		return &typeinfo.PointerType{Inner: s.substituteTypeInternal(t.Inner, bindings, specializeNamed)}
	case *typeinfo.RefType:
		return &typeinfo.RefType{Mutable: t.Mutable, Inner: s.substituteTypeInternal(t.Inner, bindings, specializeNamed)}
	case *typeinfo.RawPtrType:
		return &typeinfo.RawPtrType{Inner: s.substituteTypeInternal(t.Inner, bindings, specializeNamed)}
	case *typeinfo.OptionalType:
		return &typeinfo.OptionalType{Inner: s.substituteTypeInternal(t.Inner, bindings, specializeNamed)}
	case *typeinfo.ErrorUnionType:
		return &typeinfo.ErrorUnionType{
			Error: s.substituteTypeInternal(t.Error, bindings, specializeNamed),
			Value: s.substituteTypeInternal(t.Value, bindings, specializeNamed),
		}
	case *typeinfo.ArrayType:
		return &typeinfo.ArrayType{Inner: s.substituteTypeInternal(t.Inner, bindings, specializeNamed), Len: t.Len}
	case *typeinfo.SliceType:
		return &typeinfo.SliceType{Inner: s.substituteTypeInternal(t.Inner, bindings, specializeNamed)}
	case *typeinfo.TupleType:
		elems := make([]typeinfo.Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, s.substituteTypeInternal(elem, bindings, specializeNamed))
		}
		return &typeinfo.TupleType{Elems: elems}
	case *typeinfo.NamedType:
		out := &typeinfo.NamedType{
			ModuleKey: t.ModuleKey,
			Name:      t.Name,
			Decl:      t.Decl,
		}
		if len(t.TypeArgs) > 0 {
			out.TypeArgs = make([]typeinfo.Type, 0, len(t.TypeArgs))
			for _, arg := range t.TypeArgs {
				out.TypeArgs = append(out.TypeArgs, s.substituteTypeInternal(arg, bindings, specializeNamed))
			}
			if specializeNamed {
				req := s.requestTypeSpecialization(out)
				if req != nil {
					out.Name = req.name
					out.TypeArgs = nil
					out.Decl = nil
				} else if out.Decl != nil && len(out.Decl.TypeParams) == len(out.TypeArgs) {
					out.Name = specializedTypeNameFromArgs(out.Name, out.Decl.TypeParams, out.TypeArgs)
					out.TypeArgs = nil
					out.Decl = nil
				}
			}
		}
		return out
	case *typeinfo.StructType:
		fields := make(map[string]*typeinfo.StructField, len(t.Fields))
		ordered := make([]*typeinfo.StructField, 0, len(t.OrderedFields))
		for _, field := range t.OrderedFields {
			if field == nil {
				continue
			}
			copy := &typeinfo.StructField{
				Name:       field.Name,
				IsPub:      field.IsPub,
				Type:       s.substituteTypeInternal(field.Type, bindings, specializeNamed),
				HasDefault: field.HasDefault,
			}
			fields[copy.Name] = copy
			ordered = append(ordered, copy)
		}
		return &typeinfo.StructType{Fields: fields, OrderedFields: ordered}
	case *typeinfo.InterfaceType:
		methods := make(map[string]*typeinfo.FuncType, len(t.Methods))
		methodReceivers := make(map[string]typeinfo.ReceiverKind, len(t.MethodReceivers))
		methodStatic := make(map[string]bool, len(t.MethodStatic))
		ordered := make([]*typeinfo.InterfaceMethod, 0, len(t.OrderedMethods))
		for _, method := range t.OrderedMethods {
			if method == nil {
				continue
			}
			fn, _ := s.substituteTypeInternal(method.Type, bindings, specializeNamed).(*typeinfo.FuncType)
			copy := &typeinfo.InterfaceMethod{
				Receiver: method.Receiver,
				Static:   method.Static,
				Name:     method.Name,
				Type:     fn,
			}
			methods[copy.Name] = fn
			methodReceivers[copy.Name] = copy.Receiver
			methodStatic[copy.Name] = copy.Static
			ordered = append(ordered, copy)
		}
		return &typeinfo.InterfaceType{
			Methods:         methods,
			MethodReceivers: methodReceivers,
			MethodStatic:    methodStatic,
			OrderedMethods:  ordered,
		}
	case *typeinfo.UnionType:
		members := make([]typeinfo.Type, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, s.substituteTypeInternal(member, bindings, specializeNamed))
		}
		return &typeinfo.UnionType{Members: members}
	case *typeinfo.FuncType:
		out := &typeinfo.FuncType{
			IsUnsafe: t.IsUnsafe,
			Result:   s.substituteTypeInternal(t.Result, bindings, specializeNamed),
		}
		for _, param := range t.Params {
			out.Params = append(out.Params, typeinfo.ParamSpec{
				Name:  param.Name,
				Type:  s.substituteTypeInternal(param.Type, bindings, specializeNamed),
				Flags: param.Flags,
			})
		}
		return out
	default:
		return typ
	}
}

func lookupBoundType(bindings map[*typeinfo.TypeParam]typeinfo.Type, target *typeinfo.TypeParam) typeinfo.Type {
	if target == nil {
		return nil
	}
	if bound := bindings[target]; bound != nil {
		return bound
	}
	for param, bound := range bindings {
		if typeinfo.Equal(param, target) {
			return bound
		}
	}
	if target.Name == "" {
		return nil
	}
	for param, bound := range bindings {
		if param != nil && param.Name == target.Name {
			return bound
		}
	}
	return nil
}

func (s *specializer) specializeBinaryType(expr *BinaryExpr, bindings map[*typeinfo.TypeParam]typeinfo.Type, left, right Expr) typeinfo.Type {
	if expr == nil {
		return nil
	}
	typ := s.substituteType(expr.Type(), bindings)
	if typ != nil && !typeinfo.IsUnknown(typ) {
		return typ
	}
	leftType := typeinfo.Type(nil)
	rightType := typeinfo.Type(nil)
	if left != nil {
		leftType = left.Type()
	}
	if right != nil {
		rightType = right.Type()
	}
	switch expr.Op {
	case "+", "-", "*", "/", "%":
		if result := typeinfo.CommonNumericType(leftType, rightType); result != nil {
			return result
		}
	case "==", "!=", "<", "<=", ">", ">=", "&&", "||":
		return &typeinfo.BuiltinType{Name: "bool"}
	case "??":
		if opt, ok := leftType.(*typeinfo.OptionalType); ok {
			return opt.Inner
		}
	}
	return typ
}

func typeHasTypeParam(typ typeinfo.Type) bool {
	switch t := typ.(type) {
	case nil:
		return false
	case *typeinfo.TypeParam:
		return true
	case *typeinfo.PointerType:
		return typeHasTypeParam(t.Inner)
	case *typeinfo.RefType:
		return typeHasTypeParam(t.Inner)
	case *typeinfo.RawPtrType:
		return typeHasTypeParam(t.Inner)
	case *typeinfo.OptionalType:
		return typeHasTypeParam(t.Inner)
	case *typeinfo.ErrorUnionType:
		return typeHasTypeParam(t.Error) || typeHasTypeParam(t.Value)
	case *typeinfo.ArrayType:
		return typeHasTypeParam(t.Inner)
	case *typeinfo.SliceType:
		return typeHasTypeParam(t.Inner)
	case *typeinfo.TupleType:
		for _, elem := range t.Elems {
			if typeHasTypeParam(elem) {
				return true
			}
		}
		return false
	case *typeinfo.NamedType:
		for _, arg := range t.TypeArgs {
			if typeHasTypeParam(arg) {
				return true
			}
		}
		return false
	case *typeinfo.FuncType:
		for _, param := range t.Params {
			if typeHasTypeParam(param.Type) {
				return true
			}
		}
		return typeHasTypeParam(t.Result)
	case *typeinfo.StructType:
		for _, field := range t.OrderedFields {
			if field != nil && typeHasTypeParam(field.Type) {
				return true
			}
		}
		return false
	case *typeinfo.UnionType:
		for _, member := range t.Members {
			if typeHasTypeParam(member) {
				return true
			}
		}
		return false
	case *typeinfo.InterfaceType:
		for _, method := range t.OrderedMethods {
			if method != nil && method.Type != nil && typeHasTypeParam(method.Type) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
