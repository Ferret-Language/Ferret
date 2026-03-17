package typeinfo

import (
	"fmt"
	"strings"
)

type InterfaceMethod struct {
	Receiver string
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
		if len(t.OrderedFields) > 0 || len(t.OrderedStaticFields) > 0 {
			b.WriteByte('\n')
			for _, field := range t.OrderedFields {
				if field == nil {
					continue
				}
				fmt.Fprintf(&b, "    %s %s\n", field.Name, FormatType(field.Type))
			}
			for _, field := range t.OrderedStaticFields {
				if field == nil {
					continue
				}
				fmt.Fprintf(&b, "    static %s %s\n", field.Name, FormatType(field.Type))
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
				switch {
				case method.Static:
					fmt.Fprintf(&b, "    %s()%s\n", method.Name, formatResult(method.Type))
				case len(method.Type.Params) == 0:
					fmt.Fprintf(&b, "    %s(%sself)%s\n", method.Name, method.Receiver, formatResult(method.Type))
				default:
					fmt.Fprintf(&b, "    %s(%sself, %s)%s\n", method.Name, method.Receiver, formatParams(method.Type), formatResult(method.Type))
				}
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

func formatSignature(fn *FuncType) string {
	if fn == nil {
		return "()"
	}
	return fmt.Sprintf("(%s)%s", formatParams(fn), formatResult(fn))
}

func formatParams(fn *FuncType) string {
	if fn == nil {
		return ""
	}
	parts := make([]string, 0, len(fn.Params))
	for i, param := range fn.Params {
		prefix := ""
		if i < len(fn.ComptimeParams) && fn.ComptimeParams[i] {
			prefix = "comptime "
		}
		parts = append(parts, prefix+FormatType(param))
	}
	return strings.Join(parts, ", ")
}

func formatResult(fn *FuncType) string {
	if fn == nil || fn.Result == nil {
		return ""
	}
	if IsBuiltinNamed(fn.Result, "void") {
		return ""
	}
	return " " + FormatType(fn.Result)
}
