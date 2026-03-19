package ast

import "strings"

// TypeString renders a type expression using Ferret source syntax.
func TypeString(typ TypeExpr) string {
	switch t := typ.(type) {
	case nil:
		return "void"
	case *NamedType:
		return strings.Join(t.Path, "::")
	case *PointerType:
		return "*" + TypeString(t.Inner)
	case *RefType:
		if t.Mutable {
			return "&mut " + TypeString(t.Inner)
		}
		return "&" + TypeString(t.Inner)
	case *RawPtrType:
		return "^" + TypeString(t.Inner)
	case *SelfType:
		return "Self"
	case *OptionalType:
		return "?" + TypeString(t.Inner)
	case *ErrorUnionType:
		return TypeString(t.Error) + "!" + TypeString(t.Value)
	case *ArrayType:
		return "[" + ExprString(t.Size) + "]" + TypeString(t.Inner)
	case *SliceType:
		return "[]" + TypeString(t.Inner)
	case *TupleType:
		parts := make([]string, 0, len(t.Elems))
		for _, elem := range t.Elems {
			parts = append(parts, TypeString(elem))
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *StructType:
		return "struct"
	case *InterfaceType:
		return "interface"
	case *EnumType:
		return "enum"
	case *UnionType:
		return "union"
	case *ErrorType:
		return "error"
	default:
		return "<unknown>"
	}
}

// ExprString renders expression snippets used inside type syntax (like array sizes).
func ExprString(expr Expr) string {
	switch e := expr.(type) {
	case nil:
		return "_"
	case *NumberLit:
		return e.Value
	case *StringLit:
		return e.Value
	case *Ident:
		return e.Text()
	case *PrefixExpr:
		return e.Op + ExprString(e.Right)
	case *PostfixExpr:
		return ExprString(e.Left) + e.Op
	case *BinaryExpr:
		return ExprString(e.Left) + " " + e.Op + " " + ExprString(e.Right)
	case *SelectorExpr:
		return ExprString(e.Left) + "." + e.Name.Text()
	case *IndexExpr:
		return ExprString(e.Left) + "[" + ExprString(e.Index) + "]"
	default:
		return "_"
	}
}

func ReceiverString(recv *Receiver) string {
	if recv == nil {
		return ""
	}
	name := "self"
	if recv.Name != nil && recv.Name.Text() != "" {
		name = recv.Name.Text()
	}
	if name == "self" {
		switch t := recv.Type.(type) {
		case *RefType:
			if t.Mutable {
				return "&mut self"
			}
			return "&self"
		case *PointerType:
			return "*self"
		case *RawPtrType:
			return "^self"
		case *NamedType:
			return "self"
		}
	}
	return name + ": " + TypeString(recv.Type)
}

func ParamString(param Param) string {
	var b strings.Builder
	if param.IsComptime {
		b.WriteString("comptime ")
	}
	if param.IsMut {
		b.WriteString("mut ")
	}
	name := "_"
	if param.Name != nil && param.Name.Text() != "" {
		name = param.Name.Text()
	}
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(TypeString(param.Type))
	return b.String()
}

func TypeParamString(param TypeParam) string {
	if param.Name == nil || param.Name.Text() == "" {
		return "_"
	}
	if param.Constraint == nil {
		return param.Name.Text()
	}
	return param.Name.Text() + ": " + TypeString(param.Constraint)
}

func typeParamListString(params []TypeParam) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, TypeParamString(param))
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

func FuncSignature(fn *FuncDecl) string {
	if fn == nil || fn.Name == nil {
		return "fn <unknown>() void"
	}
	prefix := "fn"
	if fn.IsUnsafe {
		prefix = "unsafe fn"
	}
	name := fn.Name.Text()
	if fn.OwnerType != nil && len(fn.OwnerType.Path) > 0 {
		name = strings.Join(fn.OwnerType.Path, "::") + "::" + name
	}
	name += typeParamListString(fn.TypeParams)

	params := make([]string, 0, len(fn.Params)+1)
	if fn.Receiver != nil {
		params = append(params, ReceiverString(fn.Receiver))
	}
	for _, param := range fn.Params {
		params = append(params, ParamString(param))
	}

	result := "void"
	if fn.Result != nil {
		result = TypeString(fn.Result)
	}
	return prefix + " " + name + "(" + strings.Join(params, ", ") + ") " + result
}

func interfaceMethodSignature(method *InterfaceMethod) string {
	if method == nil || method.Name == nil {
		return ""
	}
	params := make([]string, 0, len(method.Params)+1)
	if method.Receiver != "" {
		params = append(params, method.Receiver+"self")
	}
	for _, param := range method.Params {
		params = append(params, ParamString(param))
	}
	result := "void"
	if method.Result != nil {
		result = TypeString(method.Result)
	}
	return method.Name.Text() + "(" + strings.Join(params, ", ") + ") " + result
}

func TypeDeclString(decl *TypeDecl) string {
	if decl == nil || decl.Name == nil {
		return "type <unknown>"
	}
	name := decl.Name.Text() + typeParamListString(decl.TypeParams)
	switch t := decl.Type.(type) {
	case *StructType:
		var b strings.Builder
		b.WriteString("type " + name + " struct {\n")
		for _, field := range t.Fields {
			if field == nil || field.Name == nil {
				continue
			}
			line := "    " + field.Name.Text() + ": " + TypeString(field.Type)
			if field.Default != nil {
				defaultValue := ExprString(field.Default)
				if defaultValue == "" || defaultValue == "_" {
					defaultValue = "..."
				}
				line += " = " + defaultValue
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("}")
		return b.String()
	case *InterfaceType:
		var b strings.Builder
		b.WriteString("type " + name + " interface {\n")
		for _, method := range t.Methods {
			sig := interfaceMethodSignature(method)
			if sig == "" {
				continue
			}
			b.WriteString("    " + sig + "\n")
		}
		b.WriteString("}")
		return b.String()
	case *EnumType:
		names := make([]string, 0, len(t.Variants))
		for _, variant := range t.Variants {
			if variant == nil || variant.Name == nil {
				continue
			}
			names = append(names, variant.Name.Text())
		}
		return "type " + name + " enum { " + strings.Join(names, ", ") + " }"
	case *ErrorType:
		names := make([]string, 0, len(t.Members))
		for _, member := range t.Members {
			if member == nil || member.Name == nil {
				continue
			}
			names = append(names, member.Name.Text())
		}
		return "type " + name + " error { " + strings.Join(names, ", ") + " }"
	case *UnionType:
		members := make([]string, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, TypeString(member))
		}
		return "type " + name + " union { " + strings.Join(members, ", ") + " }"
	default:
		return "type " + name + " " + TypeString(decl.Type)
	}
}

func (p Param) Text() string {
	return ParamString(p)
}

func (p TypeParam) Text() string {
	return TypeParamString(p)
}

func (r *Receiver) Text() string {
	return ReceiverString(r)
}

func (d *FuncDecl) Signature() string {
	return FuncSignature(d)
}

func (d *TypeDecl) Text() string {
	return TypeDeclString(d)
}
