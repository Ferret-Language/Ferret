package typeinfo

import (
	"fmt"
	"strings"

	"compiler/internal/frontend/ast"
)

type InterfaceMethod struct {
	Receiver ReceiverKind
	Static   bool
	Name     string
	Type     *FuncType
}

func FormatType(t fmt.Stringer) string {
	if t == nil {
		return "void"
	}
	return t.String()
}

func FormatNamedDecl(name string, underlying Type) string {
	switch t := underlying.(type) {
	case *StructType:
		var b strings.Builder
		fmt.Fprintf(&b, "type %s struct {", name)
		if len(t.OrderedFields) > 0 {
			b.WriteByte('\n')
			for _, field := range t.OrderedFields {
				if field == nil {
					continue
				}
				fmt.Fprintf(&b, "    %s %s\n", field.Name, FormatType(field.Type))
			}
			b.WriteString("}")
			return b.String()
		}
		b.WriteString(" }")
		return b.String()
	case *InterfaceType:
		var b strings.Builder
		fmt.Fprintf(&b, "type %s interface {", name)
		if len(t.OrderedMethods) > 0 {
			b.WriteByte('\n')
			for _, method := range t.OrderedMethods {
				if method == nil || method.Type == nil {
					continue
				}
				params := make([]string, 0, len(method.Type.Params))
				for _, param := range method.Type.Params {
					prefix := ""
					if param.Flags.Mutable() {
						prefix += "mut "
					}
					if param.Flags.Comptime() {
						prefix += "comptime "
					}
					params = append(params, prefix+FormatType(param.Type))
				}
				fmt.Fprintf(&b, "    %s\n", ast.FormatInterfaceMethodSignatureText(method.Name, method.Receiver.Prefix(), params, FormatType(method.Type.Result)))
			}
			b.WriteString("}")
			return b.String()
		}
		b.WriteString(" }")
		return b.String()
	case *EnumType:
		return fmt.Sprintf("type %s enum { %s }", name, strings.Join(t.OrderedVariants, ", "))
	case *ErrorSetType:
		return fmt.Sprintf("type %s error { %s }", name, strings.Join(t.OrderedMembers, ", "))
	case *UnionType:
		parts := make([]string, 0, len(t.Members))
		for _, member := range t.Members {
			parts = append(parts, FormatType(member))
		}
		return fmt.Sprintf("type %s union { %s }", name, strings.Join(parts, ", "))
	default:
		return fmt.Sprintf("type %s %s", name, FormatType(underlying))
	}
}

func FormatFuncSignature(name string, fn *FuncType) string {
	if fn == nil {
		if name == "" {
			return "fn() void"
		}
		return "fn " + name + "() void"
	}
	prefix := "fn"
	if fn.IsUnsafe {
		prefix = "unsafe fn"
	}
	if name != "" {
		return fmt.Sprintf("%s %s%s%s", prefix, name, formatTypeParams(fn.TypeParams), formatSignature(fn))
	}
	return prefix + formatTypeParams(fn.TypeParams) + formatSignature(fn)
}

func FormatFuncDeclSignature(fn *ast.FuncDecl, fnType *FuncType) string {
	if fn == nil || fn.Name == nil {
		return FormatFuncSignature("", fnType)
	}
	if fnType == nil {
		return fn.Signature()
	}
	var b strings.Builder
	prefix := "fn"
	if fnType.IsUnsafe {
		prefix = "unsafe fn"
	}
	b.WriteString(prefix)
	if name := ast.FuncDeclName(fn); name != "" {
		b.WriteByte(' ')
		b.WriteString(name)
	}
	b.WriteByte('(')
	wrote := false
	paramIndex := 0
	if fn.Receiver != nil {
		b.WriteString(ast.ReceiverString(fn.Receiver))
		wrote = true
	}
	for i, param := range fn.Params {
		if wrote {
			b.WriteString(", ")
		}
		if param.IsMut {
			b.WriteString("mut ")
		}
		if param.IsComptime {
			b.WriteString("comptime ")
		}
		if param.Name != nil && param.Name.Text() != "" {
			b.WriteString(param.Name.Text())
		} else {
			b.WriteString("_")
		}
		if i < len(fnType.Params) {
			b.WriteString(": ")
			b.WriteString(FormatType(fnType.Params[i].Type))
			paramIndex = i + 1
		}
		wrote = true
	}
	for ; paramIndex < len(fnType.Params); paramIndex++ {
		if wrote {
			b.WriteString(", ")
		}
		b.WriteString(FormatType(fnType.Params[paramIndex].Type))
		wrote = true
	}
	b.WriteString(") ")
	b.WriteString(FormatType(fnType.Result))
	return b.String()
}

func FormatMethodSignature(name string, receiver Type, fn *FuncType) string {
	if receiver == nil {
		return FormatFuncSignature(name, fn)
	}
	if fn == nil {
		fn = &FuncType{}
	}
	var b strings.Builder
	prefix := "fn"
	if fn.IsUnsafe {
		prefix = "unsafe fn"
	}
	b.WriteString(prefix)
	if name != "" {
		b.WriteByte(' ')
		b.WriteString(name)
	}
	params := []string{ast.FormatReceiverText("self", FormatType(receiver))}
	for _, param := range fn.Params {
		prefix := ""
		if param.Flags.Mutable() {
			prefix += "mut "
		}
		if param.Flags.Comptime() {
			prefix += "comptime "
		}
		params = append(params, prefix+FormatType(param.Type))
	}
	b.WriteString(ast.FormatParamList(params))
	b.WriteByte(' ')
	b.WriteString(FormatType(fn.Result))
	return b.String()
}

func formatSignature(fn *FuncType) string {
	if fn == nil {
		return "()"
	}
	parts := make([]string, 0, len(fn.Params))
	for _, param := range fn.Params {
		prefix := ""
		if param.Flags.Mutable() {
			prefix += "mut "
		}
		if param.Flags.Comptime() {
			prefix += "comptime "
		}
		parts = append(parts, prefix+FormatType(param.Type))
	}
	return fmt.Sprintf("(%s) %s", strings.Join(parts, ", "), FormatType(fn.Result))
}

func formatTypeParams(params []*TypeParam) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		if param == nil {
			continue
		}
		text := param.Name
		if text == "" {
			text = "_"
		}
		if param.Constraint != nil {
			text += ": " + FormatType(param.Constraint)
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return ""
	}
	return "<" + strings.Join(parts, ", ") + ">"
}
