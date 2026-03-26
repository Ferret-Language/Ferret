package ast

func CloneExprWithNodeMap(expr Expr) (Expr, map[Node]Node) {
	return CloneExprWithNodeMapAndSubstitute(expr, nil)
}

func CloneExprWithNodeMapAndSubstitute(expr Expr, substitute func(Node) Expr) (Expr, map[Node]Node) {
	mapping := make(map[Node]Node)

	var cloneType func(TypeExpr) TypeExpr
	var cloneExpr func(Expr) Expr
	var cloneStmt func(Stmt) Stmt
	var cloneBlock func(*BlockStmt) *BlockStmt
	var cloneIdent func(*Ident) *Ident
	var cloneMatchArms func([]*MatchArm) []*MatchArm

	cloneIdent = func(id *Ident) *Ident {
		if id == nil {
			return nil
		}
		out := &Ident{
			Path:     append([]string(nil), id.Path...),
			Location: id.Location,
		}
		mapping[id] = out
		if len(id.TypeArgs) > 0 {
			out.TypeArgs = make([]TypeExpr, 0, len(id.TypeArgs))
			for _, arg := range id.TypeArgs {
				out.TypeArgs = append(out.TypeArgs, cloneType(arg))
			}
		}
		return out
	}

	cloneType = func(typ TypeExpr) TypeExpr {
		switch t := typ.(type) {
		case nil:
			return nil
		case *NamedType:
			out := &NamedType{Path: append([]string(nil), t.Path...), Location: t.Location}
			mapping[t] = out
			if len(t.TypeArgs) > 0 {
				out.TypeArgs = make([]TypeExpr, 0, len(t.TypeArgs))
				for _, arg := range t.TypeArgs {
					out.TypeArgs = append(out.TypeArgs, cloneType(arg))
				}
			}
			return out
		case *PointerType:
			out := &PointerType{Inner: cloneType(t.Inner), Location: t.Location}
			mapping[t] = out
			return out
		case *RefType:
			out := &RefType{Mutable: t.Mutable, Inner: cloneType(t.Inner), Location: t.Location}
			mapping[t] = out
			return out
		case *RawPtrType:
			out := &RawPtrType{Const: t.Const, Inner: cloneType(t.Inner), Location: t.Location}
			mapping[t] = out
			return out
		case *SelfType:
			out := &SelfType{Location: t.Location}
			mapping[t] = out
			return out
		case *OptionalType:
			out := &OptionalType{Inner: cloneType(t.Inner), Location: t.Location}
			mapping[t] = out
			return out
		case *ApproxType:
			out := &ApproxType{Inner: cloneType(t.Inner), Location: t.Location}
			mapping[t] = out
			return out
		case *ErrorUnionType:
			out := &ErrorUnionType{Error: cloneType(t.Error), Value: cloneType(t.Value), Location: t.Location}
			mapping[t] = out
			return out
		case *ArrayType:
			out := &ArrayType{Size: cloneExpr(t.Size), Inner: cloneType(t.Inner), Location: t.Location}
			mapping[t] = out
			return out
		case *SliceType:
			out := &SliceType{Mutable: t.Mutable, Inner: cloneType(t.Inner), Location: t.Location}
			mapping[t] = out
			return out
		case *TupleType:
			out := &TupleType{Location: t.Location}
			mapping[t] = out
			if len(t.Elems) > 0 {
				out.Elems = make([]TypeExpr, 0, len(t.Elems))
				for _, elem := range t.Elems {
					out.Elems = append(out.Elems, cloneType(elem))
				}
			}
			return out
		case *StructType:
			out := &StructType{Location: t.Location}
			mapping[t] = out
			if len(t.Fields) > 0 {
				out.Fields = make([]*FieldDecl, 0, len(t.Fields))
				for _, field := range t.Fields {
					if field == nil {
						continue
					}
					copy := &FieldDecl{
						Name:     cloneIdent(field.Name),
						Type:     cloneType(field.Type),
						Default:  cloneExpr(field.Default),
						Location: field.Location,
					}
					mapping[field] = copy
					out.Fields = append(out.Fields, copy)
				}
			}
			return out
		case *InterfaceType:
			out := &InterfaceType{Location: t.Location}
			mapping[t] = out
			if len(t.Methods) > 0 {
				out.Methods = make([]*InterfaceMethod, 0, len(t.Methods))
				for _, method := range t.Methods {
					if method == nil {
						continue
					}
					copy := &InterfaceMethod{
						Receiver: method.Receiver,
						Static:   method.Static,
						Name:     cloneIdent(method.Name),
						Result:   cloneType(method.Result),
						Location: method.Location,
						Params:   make([]Param, 0, len(method.Params)),
					}
					for _, param := range method.Params {
						copy.Params = append(copy.Params, Param{
							Name:       cloneIdent(param.Name),
							IsMut:      param.IsMut,
							IsComptime: param.IsComptime,
							IsVariadic: param.IsVariadic,
							Type:       cloneType(param.Type),
							Default:    cloneExpr(param.Default),
							Location:   param.Location,
						})
					}
					out.Methods = append(out.Methods, copy)
				}
			}
			return out
		case *EnumType:
			out := &EnumType{Location: t.Location}
			mapping[t] = out
			if len(t.Variants) > 0 {
				out.Variants = make([]*EnumVariant, 0, len(t.Variants))
				for _, variant := range t.Variants {
					if variant == nil {
						continue
					}
					copy := &EnumVariant{Name: cloneIdent(variant.Name), Location: variant.Location}
					mapping[variant] = copy
					out.Variants = append(out.Variants, copy)
				}
			}
			return out
		case *UnionType:
			out := &UnionType{Location: t.Location}
			mapping[t] = out
			if len(t.Members) > 0 {
				out.Members = make([]TypeExpr, 0, len(t.Members))
				for _, member := range t.Members {
					out.Members = append(out.Members, cloneType(member))
				}
			}
			return out
		case *ErrorType:
			out := &ErrorType{Location: t.Location}
			mapping[t] = out
			if len(t.Members) > 0 {
				out.Members = make([]*ErrorMember, 0, len(t.Members))
				for _, member := range t.Members {
					if member == nil {
						continue
					}
					copy := &ErrorMember{Name: cloneIdent(member.Name), Location: member.Location}
					mapping[member] = copy
					out.Members = append(out.Members, copy)
				}
			}
			return out
		default:
			return nil
		}
	}

	cloneBlock = func(block *BlockStmt) *BlockStmt {
		if block == nil {
			return nil
		}
		out := &BlockStmt{Location: block.Location}
		mapping[block] = out
		if len(block.Stmts) > 0 {
			out.Stmts = make([]Stmt, 0, len(block.Stmts))
			for _, stmt := range block.Stmts {
				out.Stmts = append(out.Stmts, cloneStmt(stmt))
			}
		}
		return out
	}

	cloneMatchArms = func(arms []*MatchArm) []*MatchArm {
		if len(arms) == 0 {
			return nil
		}
		out := make([]*MatchArm, 0, len(arms))
		for _, arm := range arms {
			if arm == nil {
				continue
			}
			out = append(out, &MatchArm{
				Pattern:     cloneExpr(arm.Pattern),
				TypePattern: cloneType(arm.TypePattern),
				Wildcard:    arm.Wildcard,
				Body:        cloneBlock(arm.Body),
				Location:    arm.Location,
			})
		}
		return out
	}

	cloneStmt = func(stmt Stmt) Stmt {
		switch s := stmt.(type) {
		case nil:
			return nil
		case *BlockStmt:
			return cloneBlock(s)
		case *LetStmt:
			out := &LetStmt{Name: cloneIdent(s.Name), IsMut: s.IsMut, Type: cloneType(s.Type), Value: cloneExpr(s.Value), Location: s.Location}
			mapping[s] = out
			return out
		case *ConstStmt:
			out := &ConstStmt{Name: cloneIdent(s.Name), Type: cloneType(s.Type), Value: cloneExpr(s.Value), Location: s.Location}
			mapping[s] = out
			return out
		case *ReturnStmt:
			out := &ReturnStmt{Value: cloneExpr(s.Value), Location: s.Location}
			mapping[s] = out
			return out
		case *ExprStmt:
			out := &ExprStmt{Value: cloneExpr(s.Value), Location: s.Location}
			mapping[s] = out
			return out
		case *AssignStmt:
			out := &AssignStmt{Left: cloneExpr(s.Left), Right: cloneExpr(s.Right), Location: s.Location}
			mapping[s] = out
			return out
		case *IfStmt:
			out := &IfStmt{Cond: cloneExpr(s.Cond), Then: cloneBlock(s.Then), Else: cloneStmt(s.Else), Location: s.Location}
			mapping[s] = out
			return out
		case *MatchStmt:
			out := &MatchStmt{Value: cloneExpr(s.Value), Arms: cloneMatchArms(s.Arms), Location: s.Location}
			mapping[s] = out
			return out
		case *WhileStmt:
			out := &WhileStmt{Cond: cloneExpr(s.Cond), Body: cloneBlock(s.Body), Location: s.Location}
			mapping[s] = out
			return out
		case *ForStmt:
			out := &ForStmt{Iterable: cloneExpr(s.Iterable), Index: cloneIdent(s.Index), Value: cloneIdent(s.Value), Body: cloneBlock(s.Body), Location: s.Location}
			mapping[s] = out
			return out
		case *LabelStmt:
			out := &LabelStmt{Name: cloneIdent(s.Name), Stmt: cloneStmt(s.Stmt), Location: s.Location}
			mapping[s] = out
			return out
		case *BreakStmt:
			out := &BreakStmt{Label: cloneIdent(s.Label), Location: s.Location}
			mapping[s] = out
			return out
		case *ContinueStmt:
			out := &ContinueStmt{Label: cloneIdent(s.Label), Location: s.Location}
			mapping[s] = out
			return out
		case *DeferStmt:
			out := &DeferStmt{Body: cloneStmt(s.Body), Location: s.Location}
			mapping[s] = out
			return out
		case *ReleaseStmt:
			out := &ReleaseStmt{Value: cloneExpr(s.Value), Location: s.Location}
			mapping[s] = out
			return out
		case *PanicStmt:
			out := &PanicStmt{Value: cloneExpr(s.Value), Location: s.Location}
			mapping[s] = out
			return out
		case *LockStmt:
			out := &LockStmt{Value: cloneExpr(s.Value), Name: cloneIdent(s.Name), Body: cloneBlock(s.Body), Location: s.Location}
			mapping[s] = out
			return out
		case *UnsafeStmt:
			out := &UnsafeStmt{Body: cloneBlock(s.Body), Location: s.Location}
			mapping[s] = out
			return out
		default:
			return nil
		}
	}

	cloneExpr = func(expr Expr) Expr {
		if substitute != nil {
			if replacement := substitute(expr); replacement != nil {
				return replacement
			}
		}
		switch e := expr.(type) {
		case nil:
			return nil
		case *Ident:
			return cloneIdent(e)
		case *BadExpr:
			out := &BadExpr{Location: e.Location}
			mapping[e] = out
			return out
		case *NumberLit:
			out := &NumberLit{Value: e.Value, Location: e.Location}
			mapping[e] = out
			return out
		case *StringLit:
			out := &StringLit{Value: e.Value, Location: e.Location}
			mapping[e] = out
			return out
		case *NoneLit:
			out := &NoneLit{Location: e.Location}
			mapping[e] = out
			return out
		case *PrefixExpr:
			out := &PrefixExpr{Op: e.Op, Right: cloneExpr(e.Right), Location: e.Location}
			mapping[e] = out
			return out
		case *SpreadExpr:
			out := &SpreadExpr{Right: cloneExpr(e.Right), Location: e.Location}
			mapping[e] = out
			return out
		case *BinaryExpr:
			out := &BinaryExpr{Left: cloneExpr(e.Left), Op: e.Op, Right: cloneExpr(e.Right), Location: e.Location}
			mapping[e] = out
			return out
		case *RangeExpr:
			out := &RangeExpr{
				Start:     cloneExpr(e.Start),
				End:       cloneExpr(e.End),
				Step:      cloneExpr(e.Step),
				Inclusive: e.Inclusive,
				Location:  e.Location,
			}
			mapping[e] = out
			return out
		case *PostfixExpr:
			out := &PostfixExpr{Left: cloneExpr(e.Left), Op: e.Op, Location: e.Location}
			mapping[e] = out
			return out
		case *CallExpr:
			out := &CallExpr{Callee: cloneExpr(e.Callee), Location: e.Location}
			mapping[e] = out
			if len(e.TypeArgs) > 0 {
				out.TypeArgs = make([]TypeExpr, 0, len(e.TypeArgs))
				for _, arg := range e.TypeArgs {
					out.TypeArgs = append(out.TypeArgs, cloneType(arg))
				}
			}
			if len(e.Args) > 0 {
				out.Args = make([]Expr, 0, len(e.Args))
				for _, arg := range e.Args {
					out.Args = append(out.Args, cloneExpr(arg))
				}
			}
			return out
		case *SelectorExpr:
			out := &SelectorExpr{Left: cloneExpr(e.Left), Name: cloneIdent(e.Name), Location: e.Location}
			mapping[e] = out
			return out
		case *CastExpr:
			out := &CastExpr{Left: cloneExpr(e.Left), Type: cloneType(e.Type), Location: e.Location}
			mapping[e] = out
			return out
		case *IsExpr:
			out := &IsExpr{Left: cloneExpr(e.Left), Type: cloneType(e.Type), Location: e.Location}
			mapping[e] = out
			return out
		case *MatchExpr:
			out := &MatchExpr{Value: cloneExpr(e.Value), Arms: cloneMatchArms(e.Arms), Location: e.Location}
			mapping[e] = out
			return out
		case *CatchExpr:
			out := &CatchExpr{
				Left:     cloneExpr(e.Left),
				Fallback: cloneExpr(e.Fallback),
				Payload:  cloneIdent(e.Payload),
				Handler:  cloneBlock(e.Handler),
				Location: e.Location,
			}
			mapping[e] = out
			return out
		case *CompositeLit:
			out := &CompositeLit{Type: cloneType(e.Type), Tuple: e.Tuple, Location: e.Location}
			mapping[e] = out
			if len(e.Items) > 0 {
				out.Items = make([]CompositeItem, 0, len(e.Items))
				for _, item := range e.Items {
					out.Items = append(out.Items, CompositeItem{
						Name:  cloneIdent(item.Name),
						Value: cloneExpr(item.Value),
					})
				}
			}
			return out
		case *IndexExpr:
			out := &IndexExpr{Left: cloneExpr(e.Left), Index: cloneExpr(e.Index), Location: e.Location}
			mapping[e] = out
			return out
		default:
			return nil
		}
	}

	return cloneExpr(expr), mapping
}
