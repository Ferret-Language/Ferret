package hir

import (
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"strconv"
	"strings"
)

func Specialize(mod *Module, types *typeinfo.ModuleInfo, bindings *binding.ModuleInfo) *Module {
	if mod == nil || types == nil || bindings == nil {
		return mod
	}
	p := &specializer{
		module:    mod,
		types:     types,
		bindings:  bindings,
		templates: make(map[*symbols.Symbol]*Func),
		requests:  make(map[string]*specializationRequest),
	}
	for _, fn := range mod.Functions {
		if p.isTemplate(fn) {
			sym := bindings.FunctionSymbols[fn.Source]
			if sym != nil {
				p.templates[sym] = fn
			}
		}
	}
	if len(p.templates) == 0 {
		return mod
	}

	out := &Module{
		Key:        mod.Key,
		ImportPath: mod.ImportPath,
		FilePath:   mod.FilePath,
		Source:     mod.Source,
		Types:      append([]*TypeDecl(nil), mod.Types...),
		Globals:    make([]*Global, 0, len(mod.Globals)),
		Functions:  make([]*Func, 0, len(mod.Functions)),
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
	return out
}

type specializer struct {
	module    *Module
	types     *typeinfo.ModuleInfo
	bindings  *binding.ModuleInfo
	templates map[*symbols.Symbol]*Func
	requests  map[string]*specializationRequest
	pending   []*specializationRequest
}

type specializationRequest struct {
	template *Func
	bindings map[*typeinfo.TypeParam]typeinfo.Type
	name     string
	emitted  bool
}

func (s *specializer) isTemplate(fn *Func) bool {
	return fn != nil && fn.Source != nil && len(fn.Source.TypeParams) > 0 && !fn.IsExtern && fn.Body != nil && fn.Receiver == nil && fn.OwnerType == ""
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
		out.Value = s.cloneExpr(st.Value, bindings)
		return &out
	case *ConstStmt:
		out := *st
		out.Type = s.substituteType(st.Type, bindings)
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
		for _, arg := range ex.Args {
			out.Args = append(out.Args, s.cloneExpr(arg, bindings))
		}
		if name, fnType, ok := s.specializedCallName(ex, bindings); ok {
			out.Callee = &Ident{
				baseExpr: baseExpr{ExprType: fnType, Location: ex.Callee.Loc()},
				Path:     []string{name},
				LocalID:  -1,
			}
			return &out
		}
		out.Callee = s.cloneExpr(ex.Callee, bindings)
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
	template := s.templates[resolution.Symbol]
	if template == nil {
		return "", nil, false
	}
	fnType, ok := s.substituteType(expr.Callee.Type(), bindings).(*typeinfo.FuncType)
	if !ok || fnType == nil || len(fnType.TypeParams) != 0 {
		return "", nil, false
	}
	req := s.requestSpecialization(template, fnType)
	if req == nil {
		return "", nil, false
	}
	return req.name, fnType, true
}

func (s *specializer) requestSpecialization(template *Func, fnType *typeinfo.FuncType) *specializationRequest {
	if template == nil || template.Source == nil || fnType == nil {
		return nil
	}
	bindings := inferSpecializationBindings(template, fnType)
	if len(bindings) == 0 && len(template.Source.TypeParams) > 0 {
		return nil
	}
	name := specializedFuncName(template, bindings)
	key := name
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

func inferSpecializationBindings(template *Func, instantiated *typeinfo.FuncType) map[*typeinfo.TypeParam]typeinfo.Type {
	out := make(map[*typeinfo.TypeParam]typeinfo.Type)
	if template == nil || instantiated == nil {
		return out
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
	}
}

func specializedFuncName(template *Func, bindings map[*typeinfo.TypeParam]typeinfo.Type) string {
	var b strings.Builder
	b.WriteString(template.Name)
	b.WriteString("$")
	for i, param := range template.Source.TypeParams {
		if i > 0 {
			b.WriteString("_")
		}
		if param.Name == nil {
			b.WriteString("arg")
			b.WriteString(strconv.Itoa(i))
			continue
		}
		b.WriteString(param.Name.Text())
		b.WriteString("_")
		b.WriteString(specializedTypeTag(lookupTypeBinding(bindings, param.Name.Text())))
	}
	return b.String()
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

func (s *specializer) substituteType(typ typeinfo.Type, bindings map[*typeinfo.TypeParam]typeinfo.Type) typeinfo.Type {
	switch t := typ.(type) {
	case nil:
		return nil
	case *typeinfo.TypeParam:
		if bound := lookupBoundType(bindings, t); bound != nil {
			return bound
		}
		return typ
	case *typeinfo.PointerType:
		return &typeinfo.PointerType{Inner: s.substituteType(t.Inner, bindings)}
	case *typeinfo.RefType:
		return &typeinfo.RefType{Mutable: t.Mutable, Inner: s.substituteType(t.Inner, bindings)}
	case *typeinfo.RawPtrType:
		return &typeinfo.RawPtrType{Inner: s.substituteType(t.Inner, bindings)}
	case *typeinfo.OptionalType:
		return &typeinfo.OptionalType{Inner: s.substituteType(t.Inner, bindings)}
	case *typeinfo.ErrorUnionType:
		return &typeinfo.ErrorUnionType{
			Error: s.substituteType(t.Error, bindings),
			Value: s.substituteType(t.Value, bindings),
		}
	case *typeinfo.ArrayType:
		return &typeinfo.ArrayType{Inner: s.substituteType(t.Inner, bindings), Len: t.Len}
	case *typeinfo.SliceType:
		return &typeinfo.SliceType{Inner: s.substituteType(t.Inner, bindings)}
	case *typeinfo.TupleType:
		elems := make([]typeinfo.Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, s.substituteType(elem, bindings))
		}
		return &typeinfo.TupleType{Elems: elems}
	case *typeinfo.FuncType:
		out := &typeinfo.FuncType{
			IsUnsafe: t.IsUnsafe,
			Result:   s.substituteType(t.Result, bindings),
		}
		for _, param := range t.Params {
			out.Params = append(out.Params, typeinfo.ParamSpec{
				Name:  param.Name,
				Type:  s.substituteType(param.Type, bindings),
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
