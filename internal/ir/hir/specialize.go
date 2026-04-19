package hir

import (
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/semmeta"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
	"slices"
	"strconv"
	"strings"
)

func Specialize(mod *Module, types *typeinfo.ModuleInfo, bindings *binding.ModuleInfo) *Module {
	if mod == nil || types == nil || bindings == nil {
		return mod
	}
	p := newSpecializer(mod, types, bindings, nil)
	if len(p.templates) == 0 && len(p.typeTemplates) == 0 {
		return mod
	}
	p.seedBaseOutput()
	p.drainPending()
	return p.out
}

type specializer struct {
	project              *projectSpecializer
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
	out           *Module
}

type projectSpecializer struct {
	specializers        map[string]*specializer
	ordered             []*specializer
	templateBySymbol    map[symbols.SymbolID]*specializer
	typeTemplateByOwner map[string]*specializer
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

type typeParamBindingKey struct {
	Name  string
	Owner ast.Node
}

func SpecializeModules(modules []*Module, typesByModule map[string]*typeinfo.ModuleInfo, bindingsByModule map[string]*binding.ModuleInfo) map[string]*Module {
	project := &projectSpecializer{
		specializers:        make(map[string]*specializer),
		ordered:             make([]*specializer, 0, len(modules)),
		templateBySymbol:    make(map[symbols.SymbolID]*specializer),
		typeTemplateByOwner: make(map[string]*specializer),
	}
	for _, mod := range modules {
		if mod == nil || mod.Key == "" {
			continue
		}
		types := typesByModule[mod.Key]
		bindings := bindingsByModule[mod.Key]
		spec := newSpecializer(mod, types, bindings, project)
		project.specializers[mod.Key] = spec
		project.ordered = append(project.ordered, spec)
	}
	for _, spec := range project.ordered {
		spec.seedBaseOutput()
	}
	for {
		progress := false
		for _, spec := range project.ordered {
			if spec.drainPending() {
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	out := make(map[string]*Module, len(project.ordered))
	for _, spec := range project.ordered {
		if spec == nil || spec.module == nil {
			continue
		}
		if spec.out != nil {
			out[spec.module.Key] = spec.out
		} else {
			out[spec.module.Key] = spec.module
		}
	}
	return out
}

func newSpecializer(mod *Module, types *typeinfo.ModuleInfo, bindings *binding.ModuleInfo, project *projectSpecializer) *specializer {
	p := &specializer{
		project:              project,
		module:               mod,
		types:                types,
		bindings:             bindings,
		templates:            make(map[symbols.SymbolID]*Func),
		requests:             make(map[string]*specializationRequest),
		ownerMethodTemplates: make(map[string][]*Func),
		typeTemplates:        make(map[string]*TypeDecl),
		typeRequests:         make(map[string]*typeSpecializationRequest),
	}
	if mod == nil {
		return p
	}
	for _, fn := range mod.Functions {
		if !p.isTemplate(fn) {
			continue
		}
		sym := (*symbols.Symbol)(nil)
		if bindings != nil && fn.Source != nil {
			sym = bindings.FunctionSymbols[fn.Source]
		}
		if sym != nil {
			p.templates[sym.ID] = fn
			if project != nil {
				project.templateBySymbol[sym.ID] = p
			}
		}
		if fn.OwnerType != "" {
			ownerKey := mod.Key + "::" + fn.OwnerType
			p.ownerMethodTemplates[ownerKey] = append(p.ownerMethodTemplates[ownerKey], fn)
		}
	}
	for _, decl := range mod.Types {
		if !p.isTypeTemplate(decl) {
			continue
		}
		moduleKey := mod.Key
		if decl.Named != nil && decl.Named.ModuleKey != "" {
			moduleKey = decl.Named.ModuleKey
		}
		key := moduleKey + "::" + decl.Name
		p.typeTemplates[key] = decl
		if project != nil {
			project.typeTemplateByOwner[key] = p
		}
	}
	return p
}

func (s *specializer) seedBaseOutput() {
	if s == nil || s.module == nil {
		return
	}
	out := &Module{
		Key:        s.module.Key,
		ImportPath: s.module.ImportPath,
		FilePath:   s.module.FilePath,
		Source:     s.module.Source,
		Types:      make([]*TypeDecl, 0, len(s.module.Types)),
		Globals:    make([]*Global, 0, len(s.module.Globals)),
		Functions:  make([]*Func, 0, len(s.module.Functions)),
	}
	for _, decl := range s.module.Types {
		if s.isTypeTemplate(decl) {
			continue
		}
		out.Types = append(out.Types, s.cloneTypeDecl(decl, nil, ""))
	}
	for _, global := range s.module.Globals {
		out.Globals = append(out.Globals, s.cloneGlobal(global, nil))
	}
	for _, fn := range s.module.Functions {
		if s.isTemplate(fn) {
			continue
		}
		out.Functions = append(out.Functions, s.cloneFunc(fn, nil, ""))
	}
	s.out = out
}

func (s *specializer) drainPending() bool {
	if s == nil || s.out == nil {
		return false
	}
	progress := false
	for i := 0; i < len(s.pending); i++ {
		req := s.pending[i]
		if req == nil || req.emitted {
			continue
		}
		req.emitted = true
		progress = true
		s.out.Functions = append(s.out.Functions, s.cloneFunc(req.template, req.bindings, req.name))
	}
	for i := 0; i < len(s.typePending); i++ {
		req := s.typePending[i]
		if req == nil || req.emitted {
			continue
		}
		req.emitted = true
		progress = true
		s.out.Types = append(s.out.Types, s.specializeTypeDecl(req))
	}
	return progress
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
		out.Value = s.cloneExpr(st.Value, bindings)
		if (out.Type == nil || typeinfo.IsUnknown(out.Type)) && out.Value != nil {
			if valueType := out.Value.Type(); valueType != nil && !typeinfo.IsUnknown(valueType) && !typeinfo.IsInvalid(valueType) {
				out.Type = valueType
			}
		}
		s.requestInterfaceMethodSpecializations(out.Value, out.Type, bindings)
		return &out
	case *ConstStmt:
		out := *st
		out.Type = s.substituteType(st.Type, bindings)
		out.Value = s.cloneExpr(st.Value, bindings)
		if (out.Type == nil || typeinfo.IsUnknown(out.Type)) && out.Value != nil {
			if valueType := out.Value.Type(); valueType != nil && !typeinfo.IsUnknown(valueType) && !typeinfo.IsInvalid(valueType) {
				out.Type = valueType
			}
		}
		s.requestInterfaceMethodSpecializations(out.Value, out.Type, bindings)
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
	case *RangeExpr:
		out := *ex
		out.Start = s.cloneExpr(ex.Start, bindings)
		out.End = s.cloneExpr(ex.End, bindings)
		out.Step = s.cloneExpr(ex.Step, bindings)
		out.ExprType = s.substituteType(ex.Type(), bindings)
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
				ident.Path = s.specializedCalleePath(ident.Path, name, ex.Callee.SourceExpr())
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
	case *ClosureLit:
		out := *ex
		out.ExprType = s.substituteType(ex.Type(), bindings)
		out.Captures = make([]Expr, 0, len(ex.Captures))
		for _, capture := range ex.Captures {
			out.Captures = append(out.Captures, s.cloneExpr(capture, bindings))
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
			out.Items = append(out.Items, CompositeItem{Name: item.Name, Key: s.cloneExpr(item.Key, bindings), Value: s.cloneExpr(item.Value, bindings)})
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

func (s *specializer) specializedCalleePath(currentPath []string, leaf string, sourceExpr ast.Expr) []string {
	path := append([]string(nil), currentPath...)
	if len(path) == 0 {
		path = []string{leaf}
	} else {
		path[len(path)-1] = leaf
	}
	if s == nil || s.bindings == nil || sourceExpr == nil || s.module == nil {
		return path
	}
	resolution := s.bindings.Nodes[sourceExpr]
	if resolution == nil || resolution.Kind != binding.ResolutionSymbol {
		return path
	}
	if resolution.ImportPath == "" || resolution.ImportPath == s.module.ImportPath {
		return path
	}
	importParts := strings.Split(resolution.ImportPath, "/")
	tail := path
	if len(path) > 0 {
		// Preserve owner qualifiers for static owner calls by only stripping the
		// leading local/import alias portion before re-rooting to importPath.
		if len(path) >= len(importParts) && slices.Equal(path[:len(importParts)], importParts) {
			tail = path[len(importParts):]
		} else if len(path) > 1 {
			tail = path[1:]
		}
	}
	if len(tail) == 0 {
		tail = []string{leaf}
	}
	return append(append([]string(nil), importParts...), tail...)
}

func (s *specializer) specializedCallName(expr *CallExpr, bindings map[*typeinfo.TypeParam]typeinfo.Type) (string, *typeinfo.FuncType, bool) {
	if expr == nil || expr.Callee == nil {
		return "", nil, false
	}
	fnType, ok := s.substituteType(expr.Callee.Type(), bindings).(*typeinfo.FuncType)
	if !ok || fnType == nil || len(fnType.TypeParams) != 0 {
		return "", nil, false
	}
	var resolution *binding.Resolution
	if s.bindings != nil && expr.Callee != nil {
		resolution = s.bindings.Nodes[expr.Callee.SourceExpr()]
	}
	owner := s
	var template *Func
	if resolution != nil && resolution.Kind == binding.ResolutionSymbol && resolution.Symbol != nil {
		owner, template = s.templateOwnerBySymbolID(resolution.Symbol.ID)
	}
	if template == nil {
		if sel, ok := expr.Callee.(*SelectorExpr); ok && sel.Left != nil {
			receiverType := s.substituteType(sel.Left.Type(), bindings)
			if named, ok := typeinfo.ReceiverBaseNamedType(receiverType); ok && named != nil {
				var candidates []*Func
				owner, candidates = s.methodTemplatesForNamed(named)
				for _, candidate := range candidates {
					if candidate == nil || candidate.Name != sel.Name || candidate.Receiver == nil {
						continue
					}
					template = candidate
					break
				}
			}
		}
	}
	if template == nil {
		return "", nil, false
	}
	receiverType := s.substituteType(expr.MethodReceiver, bindings)
	if receiverType == nil && s.types != nil {
		if source := expr.SourceExpr(); source != nil {
			if inferred, ok := s.types.LookupMethodReceiver(source); ok {
				receiverType = s.substituteType(inferred, bindings)
			}
		}
		if receiverType == nil && expr.Callee != nil && expr.Callee.SourceExpr() != nil {
			if inferred, ok := s.types.LookupMethodReceiver(expr.Callee.SourceExpr()); ok {
				receiverType = s.substituteType(inferred, bindings)
			}
		}
	}
	if receiverType == nil && template.Receiver != nil {
		if sel, ok := expr.Callee.(*SelectorExpr); ok && sel.Left != nil {
			if key, ok := typeinfo.ReceiverKeyFromType(template.Receiver.Type); ok {
				receiverType = typeinfo.ApplyReceiverShape(s.substituteType(sel.Left.Type(), bindings), key.Kind)
			}
		}
	}
	req := owner.requestSpecialization(template, fnType, receiverType)
	if req == nil {
		return "", nil, false
	}
	return req.name, fnType, true
}

func (s *specializer) templateOwnerBySymbolID(id symbols.SymbolID) (*specializer, *Func) {
	if template := s.templates[id]; template != nil {
		return s, template
	}
	if s.project == nil {
		return nil, nil
	}
	owner := s.project.templateBySymbol[id]
	if owner == nil {
		return nil, nil
	}
	return owner, owner.templates[id]
}

func (s *specializer) methodTemplatesForNamed(named *typeinfo.NamedType) (*specializer, []*Func) {
	if named == nil {
		return nil, nil
	}
	owner := s
	if s.project != nil && named.ModuleKey != "" {
		if candidate := s.project.specializers[named.ModuleKey]; candidate != nil {
			owner = candidate
		}
	}
	key := ownerMethodTemplateKey(named)
	return owner, owner.ownerMethodTemplates[key]
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
	paramKeys := s.specializedFuncParamKeys(template)
	for _, key := range paramKeys {
		if lookupTypeBinding(bindings, key) == nil {
			return nil
		}
	}
	name := specializedFuncName(template, bindings, paramKeys)
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
		if got, ok := actual.(*typeinfo.SliceType); ok && (!p.Mutable || got.Mutable) {
			inferTypeBindings(p.Inner, got.Inner, bindings)
		} else if got, ok := actual.(*typeinfo.ArrayType); ok {
			inferTypeBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.TupleType:
		if got, ok := actual.(*typeinfo.TupleType); ok && len(p.Elems) == len(got.Elems) {
			for i := range p.Elems {
				inferTypeBindings(p.Elems[i], got.Elems[i], bindings)
			}
		}
	case *typeinfo.MapType:
		if got, ok := actual.(*typeinfo.MapType); ok {
			inferTypeBindings(p.Key, got.Key, bindings)
			inferTypeBindings(p.Value, got.Value, bindings)
		}
	case *typeinfo.NamedType:
		got, ok := actual.(*typeinfo.NamedType)
		if !ok || p.ModuleKey != got.ModuleKey || len(p.TypeArgs) != len(got.TypeArgs) {
			return
		}
		if p.Name != got.Name {
			if p.Decl == nil || got.Decl == nil || p.Decl != got.Decl {
				return
			}
		}
		if len(p.TypeArgs) != len(got.TypeArgs) {
			return
		}
		for i := range p.TypeArgs {
			inferTypeBindings(p.TypeArgs[i], got.TypeArgs[i], bindings)
		}
	}
}

func specializedFuncName(template *Func, bindings map[*typeinfo.TypeParam]typeinfo.Type, paramKeys []typeParamBindingKey) string {
	var b strings.Builder
	b.WriteString(template.Name)
	b.WriteString("$")
	for i, key := range paramKeys {
		if i > 0 {
			b.WriteString("_")
		}
		b.WriteString(key.Name)
		b.WriteString("_")
		b.WriteString(specializedTypeTag(lookupTypeBinding(bindings, key)))
	}
	return b.String()
}

func (s *specializer) specializedFuncParamKeys(template *Func) []typeParamBindingKey {
	if template == nil {
		return nil
	}
	keys := make([]typeParamBindingKey, 0, len(template.Source.TypeParams)+2)
	added := make(map[typeParamBindingKey]struct{}, len(template.Source.TypeParams)+2)
	add := func(name string, owner ast.Node) {
		if name == "" {
			return
		}
		key := typeParamBindingKey{Name: name, Owner: owner}
		if _, ok := added[key]; ok {
			return
		}
		added[key] = struct{}{}
		keys = append(keys, key)
	}
	if template.OwnerType != "" {
		for _, decl := range s.module.Types {
			if decl == nil || decl.Name != template.OwnerType || decl.Source == nil {
				continue
			}
			for _, param := range decl.Source.TypeParams {
				if param.Name == nil {
					continue
				}
				add(param.Name.Text(), decl.Source)
			}
			break
		}
	}
	if template.Receiver != nil {
		if named, ok := typeinfo.ReceiverBaseNamedType(template.Receiver.Type); ok && named != nil && named.Decl != nil {
			for _, param := range named.Decl.TypeParams {
				if param.Name == nil {
					continue
				}
				add(param.Name.Text(), named.Decl)
			}
		}
	}
	for i, param := range template.Source.TypeParams {
		name := "arg" + strconv.Itoa(i)
		if param.Name != nil {
			name = param.Name.Text()
		}
		add(name, template.Source)
	}
	return keys
}

func specializedTypeTag(typ typeinfo.Type) string {
	var b strings.Builder
	appendSpecializedTypeTag(&b, typ, make(map[typeinfo.Type]uint16))
	text := b.String()
	if text == "" {
		return "type"
	}
	return text
}

func appendSpecializedTypeTag(b *strings.Builder, typ typeinfo.Type, seen map[typeinfo.Type]uint16) {
	switch t := typ.(type) {
	case nil:
		b.WriteString("void")
	case *typeinfo.BuiltinType:
		b.WriteString(legacySpecializedTypeText(t.Name))
	case *typeinfo.StringType:
		b.WriteString("str")
	case *typeinfo.SelfType:
		b.WriteString("Self")
	case *typeinfo.TypeParam:
		if t.Name == "" {
			b.WriteString("type")
		} else {
			b.WriteString(legacySpecializedTypeText(t.Name))
		}
	case *typeinfo.PointerType:
		b.WriteString("ptr_")
		appendSpecializedTypeTag(b, t.Inner, seen)
	case *typeinfo.RefType:
		if t.Mutable {
			b.WriteString("refm_")
		} else {
			b.WriteString("ref_")
		}
		appendSpecializedTypeTag(b, t.Inner, seen)
	case *typeinfo.RawPtrType:
		b.WriteString("raw_")
		appendSpecializedTypeTag(b, t.Inner, seen)
	case *typeinfo.OptionalType:
		b.WriteString("opt_")
		appendSpecializedTypeTag(b, t.Inner, seen)
	case *typeinfo.ErrorUnionType:
		b.WriteString("eu_")
		appendSpecializedTypeTag(b, t.Error, seen)
		b.WriteString("_")
		appendSpecializedTypeTag(b, t.Value, seen)
	case *typeinfo.ArrayType:
		b.WriteString("arr_")
		b.WriteString(strconv.FormatInt(t.Len, 10))
		b.WriteString("_")
		appendSpecializedTypeTag(b, t.Inner, seen)
	case *typeinfo.SliceType:
		if t.Mutable {
			b.WriteString("mslice_")
		} else {
			b.WriteString("slice_")
		}
		appendSpecializedTypeTag(b, t.Inner, seen)
	case *typeinfo.TupleType:
		b.WriteString("tuple_")
		b.WriteString(strconv.Itoa(len(t.Elems)))
		for _, elem := range t.Elems {
			b.WriteString("_")
			appendSpecializedTypeTag(b, elem, seen)
		}
	case *typeinfo.NamedType:
		if id, ok := seen[t]; ok {
			b.WriteString("rec_")
			b.WriteString(strconv.FormatUint(uint64(id), 10))
			return
		}
		seen[t] = uint16(len(seen))
		b.WriteString("named_")
		appendSpecializedTextSegment(b, t.ModuleKey)
		b.WriteString("_")
		appendSpecializedTextSegment(b, t.Name)
		b.WriteString("_")
		b.WriteString(strconv.Itoa(len(t.TypeArgs)))
		for _, arg := range t.TypeArgs {
			b.WriteString("_")
			appendSpecializedTypeTag(b, arg, seen)
		}
	case *typeinfo.StructType:
		if id, ok := seen[t]; ok {
			b.WriteString("rec_")
			b.WriteString(strconv.FormatUint(uint64(id), 10))
			return
		}
		seen[t] = uint16(len(seen))
		b.WriteString("struct_")
		b.WriteString(strconv.Itoa(len(t.OrderedFields)))
		for _, field := range t.OrderedFields {
			if field == nil {
				continue
			}
			b.WriteString("_")
			appendSpecializedTextSegment(b, field.Name)
			b.WriteString("_")
			appendSpecializedTypeTag(b, field.Type, seen)
		}
	case *typeinfo.InterfaceType:
		if id, ok := seen[t]; ok {
			b.WriteString("rec_")
			b.WriteString(strconv.FormatUint(uint64(id), 10))
			return
		}
		seen[t] = uint16(len(seen))
		b.WriteString("iface_")
		b.WriteString(strconv.Itoa(len(t.OrderedMethods)))
		for _, method := range t.OrderedMethods {
			if method == nil {
				continue
			}
			b.WriteString("_")
			appendSpecializedTextSegment(b, method.Name)
			b.WriteString("_")
			b.WriteString(strconv.Itoa(int(method.Receiver)))
			if method.Static {
				b.WriteString("_s1")
			} else {
				b.WriteString("_s0")
			}
			b.WriteString("_")
			appendSpecializedTypeTag(b, method.Type, seen)
		}
	case *typeinfo.UnionType:
		if id, ok := seen[t]; ok {
			b.WriteString("rec_")
			b.WriteString(strconv.FormatUint(uint64(id), 10))
			return
		}
		seen[t] = uint16(len(seen))
		b.WriteString("union_")
		b.WriteString(strconv.Itoa(len(t.Members)))
		for _, member := range t.Members {
			b.WriteString("_")
			appendSpecializedTypeTag(b, member, seen)
		}
	case *typeinfo.EnumType:
		b.WriteString("enum_")
		b.WriteString(strconv.Itoa(len(t.OrderedVariants)))
		for _, variant := range t.OrderedVariants {
			b.WriteString("_")
			appendSpecializedTextSegment(b, variant)
		}
	case *typeinfo.ErrorSetType:
		b.WriteString("error_")
		b.WriteString(strconv.Itoa(len(t.OrderedMembers)))
		for _, member := range t.OrderedMembers {
			b.WriteString("_")
			appendSpecializedTextSegment(b, member)
		}
	case *typeinfo.FuncType:
		if id, ok := seen[t]; ok {
			b.WriteString("rec_")
			b.WriteString(strconv.FormatUint(uint64(id), 10))
			return
		}
		seen[t] = uint16(len(seen))
		b.WriteString("fn_")
		if t.IsUnsafe {
			b.WriteString("u1")
		} else {
			b.WriteString("u0")
		}
		b.WriteString("_tp")
		b.WriteString(strconv.Itoa(len(t.TypeParams)))
		b.WriteString("_p")
		b.WriteString(strconv.Itoa(len(t.Params)))
		for _, param := range t.Params {
			b.WriteString("_")
			appendSpecializedTypeTag(b, param.Type, seen)
		}
		b.WriteString("_r_")
		appendSpecializedTypeTag(b, t.Result, seen)
	default:
		b.WriteString(legacySpecializedTypeText(t.String()))
	}
}

var legacySpecializedTypeReplacer = strings.NewReplacer(
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

func legacySpecializedTypeText(text string) string {
	text = legacySpecializedTypeReplacer.Replace(text)
	text = strings.Trim(text, "_")
	if text == "" {
		return "type"
	}
	return text
}

func appendSpecializedTextSegment(b *strings.Builder, text string) {
	b.WriteString(strconv.Itoa(len(text)))
	if len(text) == 0 {
		return
	}
	b.WriteByte('_')
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte('x')
		b.WriteString(strconv.FormatInt(int64(ch), 16))
		b.WriteByte('x')
	}
}

func lookupTypeBinding(bindings map[*typeinfo.TypeParam]typeinfo.Type, key typeParamBindingKey) typeinfo.Type {
	if key.Name == "" {
		return nil
	}
	if bound := typeinfo.LookupTypeParamBinding(bindings, &typeinfo.TypeParam{Name: key.Name, Owner: key.Owner}); bound != nil {
		return bound
	}
	if key.Owner == nil {
		if bound := typeinfo.LookupTypeParamBinding(bindings, &typeinfo.TypeParam{Name: key.Name}); bound != nil {
			return bound
		}
		for param, bound := range bindings {
			if param != nil && param.Name == key.Name {
				return bound
			}
		}
	}
	return nil
}

func (s *specializer) requestTypeSpecialization(named *typeinfo.NamedType) *typeSpecializationRequest {
	if named == nil || named.Decl == nil || len(named.Decl.TypeParams) == 0 || len(named.TypeArgs) == 0 {
		return nil
	}
	key := named.ModuleKey + "::" + named.Name
	owner := s
	template := owner.typeTemplates[key]
	if template == nil && s.project != nil {
		if candidate := s.project.typeTemplateByOwner[key]; candidate != nil {
			owner = candidate
			template = owner.typeTemplates[key]
		}
	}
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
	if req, ok := owner.typeRequests[requestKey]; ok {
		return req
	}
	req := &typeSpecializationRequest{
		template: template,
		name:     name,
		bindings: bindings,
	}
	owner.typeRequests[requestKey] = req
	owner.typePending = append(owner.typePending, req)
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
	sourceType := typeinfo.InstantiateType(value.Type(), bindings)
	named, ok := typeinfo.ReceiverBaseNamedType(sourceType)
	if !ok || named == nil {
		return
	}
	owner, methodTemplates := s.methodTemplatesForNamed(named)
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
		wantReceiver := semmeta.ReceiverKindFromSyntax(method.Receiver)
		for _, candidate := range methodTemplates {
			if candidate == nil || candidate.Name != method.Name.Text() || candidate.Receiver == nil {
				continue
			}
			key, ok := typeinfo.ReceiverKeyFromType(candidate.Receiver.Type)
			if !ok || key.Kind != wantReceiver || len(candidate.Source.TypeParams) != 0 {
				continue
			}
			owner.requestSpecializationWithBindings(candidate, ownerBindings)
			break
		}
	}
}

func ownerMethodTemplateKey(named *typeinfo.NamedType) string {
	if named == nil {
		return ""
	}
	name := named.Name
	if named.Decl != nil && named.Decl.Name != nil && named.Decl.Name.Text() != "" {
		name = named.Decl.Name.Text()
	}
	return named.ModuleKey + "::" + name
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
		args = append(args, lookupTypeBinding(bindings, typeParamBindingKey{Name: name, Owner: template.Source}))
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
	out := typeinfo.InstantiateType(typ, bindings)
	return s.specializeNamedTypeRefs(out, make(map[typeinfo.Type]struct{}))
}

func (s *specializer) specializeNamedTypeRefs(typ typeinfo.Type, seen map[typeinfo.Type]struct{}) typeinfo.Type {
	switch t := typ.(type) {
	case nil:
		return nil
	case *typeinfo.PointerType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		t.Inner = s.specializeNamedTypeRefs(t.Inner, seen)
		return t
	case *typeinfo.RefType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		t.Inner = s.specializeNamedTypeRefs(t.Inner, seen)
		return t
	case *typeinfo.RawPtrType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		t.Inner = s.specializeNamedTypeRefs(t.Inner, seen)
		return t
	case *typeinfo.OptionalType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		t.Inner = s.specializeNamedTypeRefs(t.Inner, seen)
		return t
	case *typeinfo.ErrorUnionType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		t.Error = s.specializeNamedTypeRefs(t.Error, seen)
		t.Value = s.specializeNamedTypeRefs(t.Value, seen)
		return t
	case *typeinfo.ArrayType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		t.Inner = s.specializeNamedTypeRefs(t.Inner, seen)
		return t
	case *typeinfo.SliceType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		t.Inner = s.specializeNamedTypeRefs(t.Inner, seen)
		return t
	case *typeinfo.TupleType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		for i, elem := range t.Elems {
			t.Elems[i] = s.specializeNamedTypeRefs(elem, seen)
		}
		return t
	case *typeinfo.MapType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		t.Key = s.specializeNamedTypeRefs(t.Key, seen)
		t.Value = s.specializeNamedTypeRefs(t.Value, seen)
		return t
	case *typeinfo.NamedType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		if len(t.TypeArgs) > 0 {
			for i, arg := range t.TypeArgs {
				t.TypeArgs[i] = s.specializeNamedTypeRefs(arg, seen)
			}
			req := s.requestTypeSpecialization(t)
			if req != nil {
				t.Name = req.name
			} else if t.Decl != nil && len(t.Decl.TypeParams) == len(t.TypeArgs) {
				baseName := t.Name
				if t.Decl.Name != nil && t.Decl.Name.Text() != "" {
					baseName = t.Decl.Name.Text()
				}
				t.Name = specializedTypeNameFromArgs(baseName, t.Decl.TypeParams, t.TypeArgs)
			}
		}
		return t
	case *typeinfo.StructType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		for _, field := range t.OrderedFields {
			if field == nil {
				continue
			}
			field.Type = s.specializeNamedTypeRefs(field.Type, seen)
		}
		return t
	case *typeinfo.InterfaceType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		for _, method := range t.OrderedMethods {
			if method == nil {
				continue
			}
			if method.Type != nil {
				if fn, ok := s.specializeNamedTypeRefs(method.Type, seen).(*typeinfo.FuncType); ok {
					method.Type = fn
				}
			}
		}
		return t
	case *typeinfo.UnionType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		for i, member := range t.Members {
			t.Members[i] = s.specializeNamedTypeRefs(member, seen)
		}
		return t
	case *typeinfo.FuncType:
		if _, ok := seen[t]; ok {
			return t
		}
		seen[t] = struct{}{}
		t.Result = s.specializeNamedTypeRefs(t.Result, seen)
		for i := range t.Params {
			t.Params[i].Type = s.specializeNamedTypeRefs(t.Params[i].Type, seen)
		}
		for _, param := range t.TypeParams {
			if param == nil {
				continue
			}
			param.Constraint = s.specializeNamedTypeRefs(param.Constraint, seen)
		}
		return t
	default:
		return typ
	}
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
	case *typeinfo.MapType:
		return typeHasTypeParam(t.Key) || typeHasTypeParam(t.Value)
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
