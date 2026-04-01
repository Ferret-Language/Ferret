package ast

import "strings"

// TypeString renders a type expression using Ferret source syntax.
func TypeString(typ TypeExpr) string {
	switch t := typ.(type) {
	case nil:
		return "void"
	case *NamedType:
		name := strings.Join(t.Path, "::")
		if len(t.TypeArgs) == 0 {
			return name
		}
		args := make([]string, 0, len(t.TypeArgs))
		for _, arg := range t.TypeArgs {
			args = append(args, TypeString(arg))
		}
		return name + "<" + strings.Join(args, ", ") + ">"
	case *PointerType:
		return "*" + TypeString(t.Inner)
	case *RefType:
		if t.Mutable {
			return "&mut " + TypeString(t.Inner)
		}
		return "&" + TypeString(t.Inner)
	case *RawPtrType:
		if t.Const {
			return "^const " + TypeString(t.Inner)
		}
		return "^" + TypeString(t.Inner)
	case *SelfType:
		return "Self"
	case *OptionalType:
		return "?" + TypeString(t.Inner)
	case *ApproxType:
		return "~" + TypeString(t.Inner)
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
		var b strings.Builder
		b.WriteString("struct")
		if len(t.Fields) == 0 {
			b.WriteString(" {}")
			return b.String()
		}
		b.WriteString(" {\n")
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
		b.WriteString("interface")
		if len(t.Methods) == 0 {
			b.WriteString(" {}")
			return b.String()
		}
		b.WriteString(" {\n")
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
		return "enum { " + strings.Join(names, ", ") + " }"
	case *UnionType:
		members := make([]string, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, TypeString(member))
		}
		return "union { " + strings.Join(members, ", ") + " }"
	case *ErrorType:
		names := make([]string, 0, len(t.Members))
		for _, member := range t.Members {
			if member == nil || member.Name == nil {
				continue
			}
			names = append(names, member.Name.Text())
		}
		return "error { " + strings.Join(names, ", ") + " }"
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
	case *SpreadExpr:
		return ExprString(e.Right) + "..."
	case *PostfixExpr:
		return ExprString(e.Left) + e.Op
	case *BinaryExpr:
		return ExprString(e.Left) + " " + e.Op + " " + ExprString(e.Right)
	case *RangeExpr:
		op := ".."
		if e.Inclusive {
			op = "..="
		}
		out := ExprString(e.Start) + op + ExprString(e.End)
		if e.Step != nil {
			out += ":" + ExprString(e.Step)
		}
		return out
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
	return FormatReceiverText(name, TypeString(recv.Type))
}

func FormatNamedParamText(name, typeText string, isMut bool) string {
	var b strings.Builder
	if isMut {
		b.WriteString("mut ")
	}
	if name == "" {
		name = "_"
	}
	b.WriteString(name)
	if typeText != "" {
		b.WriteString(": ")
		b.WriteString(typeText)
	}
	return b.String()
}

func FormatReceiverText(name, typeText string) string {
	if name == "" {
		name = "self"
	}
	if name == "self" {
		switch {
		case strings.HasPrefix(typeText, "&mut "):
			return "&mut self"
		case strings.HasPrefix(typeText, "&"):
			return "&self"
		case strings.HasPrefix(typeText, "*"):
			return "*self"
		case strings.HasPrefix(typeText, "^"):
			if strings.HasPrefix(typeText, "^const ") {
				return "^const self"
			}
			return "^self"
		case typeText != "":
			return "self"
		}
	}
	return name + ": " + typeText
}

func FormatParamList(items []string) string {
	return "(" + strings.Join(items, ", ") + ")"
}

func ParamString(param Param) string {
	name := "_"
	if param.Name != nil && param.Name.Text() != "" {
		name = param.Name.Text()
	}
	typeText := ""
	if param.Type != nil {
		typeText = TypeString(param.Type)
	}
	if param.IsVariadic {
		if slice, ok := param.Type.(*SliceType); ok && slice != nil {
			typeText = "..." + TypeString(slice.Inner)
		} else {
			typeText = "..." + typeText
		}
	}
	text := FormatNamedParamText(name, typeText, param.IsMut)
	if param.Default != nil {
		text += " = " + ExprString(param.Default)
	}
	return text
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

func FuncDeclName(fn *FuncDecl) string {
	if fn == nil || fn.Name == nil {
		return ""
	}
	if fn.IsTest {
		return `test "` + fn.TestName + `"`
	}
	name := fn.Name.Text()
	if fn.OwnerType != nil && len(fn.OwnerType.Path) > 0 {
		return strings.Join(fn.OwnerType.Path, "::") + "::" + name
	}
	return name
}

func FuncSignature(fn *FuncDecl) string {
	if fn == nil || fn.Name == nil {
		return "fn <unknown>() void"
	}
	if fn.IsTest {
		return `test "` + fn.TestName + `"`
	}
	prefix := "fn"
	if fn.IsUnsafe {
		prefix = "unsafe fn"
	}
	name := FuncDeclName(fn)
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
		result = "-> " + TypeString(fn.Result)
	}
	return prefix + " " + name + FormatParamList(params) + " " + result
}

func FormatInterfaceMethodSignatureText(name, receiver string, params []string, result string) string {
	if receiver != "" {
		params = append([]string{receiver + "self"}, params...)
	}
	if result == "" {
		result = "void"
	} else {
		result = "-> " + result
	}
	return name + FormatParamList(params) + " " + result
}

func interfaceMethodSignature(method *InterfaceMethod) string {
	if method == nil || method.Name == nil {
		return ""
	}
	params := make([]string, 0, len(method.Params)+1)
	for _, param := range method.Params {
		params = append(params, ParamString(param))
	}
	result := "void"
	if method.Result != nil {
		result = TypeString(method.Result)
	}
	return FormatInterfaceMethodSignatureText(method.Name.Text(), method.Receiver, params, result)
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
