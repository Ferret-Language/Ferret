package mir

import (
	"fmt"
	"strings"

	"compiler/internal/analysis/semantics/typeinfo"
)

func FormatModule(mod *Module) string {
	if mod == nil {
		return ""
	}
	currentModuleForFormat = mod
	defer func() { currentModuleForFormat = nil }()
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n", mod.ImportPath)
	for _, decl := range mod.Types {
		if decl == nil {
			continue
		}
		b.WriteString(formatTypeDecl(decl))
		b.WriteByte('\n')
	}
	if len(mod.Types) > 0 && (len(mod.Globals) > 0 || len(mod.Functions) > 0) {
		b.WriteByte('\n')
	}
	if len(mod.Globals) > 0 {
		b.WriteString("globals:\n")
		for _, global := range mod.Globals {
			if global == nil {
				continue
			}
			fmt.Fprintf(&b, "    %s %s: %s = %s\n", globalKeyword(global), global.Name, renderType(global.Type), formatValue(global.Init))
		}
	}
	if len(mod.Globals) > 0 && len(mod.Functions) > 0 {
		b.WriteByte('\n')
	}
	for i, fn := range mod.Functions {
		if fn == nil {
			continue
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		formatFunction(&b, fn)
	}
	return b.String()
}

func formatFunction(b *strings.Builder, fn *Function) {
	currentFnForFormat = fn
	defer func() { currentFnForFormat = nil }()
	currentBlockLabels = blockLabels(fn)
	defer func() { currentBlockLabels = nil }()
	if fn.IsExtern {
		if fn.ExternName == "" {
			b.WriteString("#[extern]\n")
		} else {
			fmt.Fprintf(b, "#[extern(%q)]\n", fn.ExternName)
		}
	}
	if fn.IsUnsafe {
		b.WriteString("unsafe ")
	}
	name := prettyFunctionName(fn)
	if strings.Contains(name, "::") && len(fn.Params) > 0 {
		fmt.Fprintf(b, "fn %s%s", name, formatMethodParams(fn.Params))
	} else {
		fmt.Fprintf(b, "fn %s%s", name, typeinfo.DefaultPrinter.ParamList(formatParams(fn.Params)))
	}
	if fn.Blocks == nil {
		fmt.Fprintf(b, " -> %s;\n", renderType(fn.Result))
		return
	}
	fmt.Fprintf(b, " -> %s {\n", renderType(fn.Result))
	if len(fn.Locals) > 0 {
		b.WriteString("locals:\n")
		for _, local := range fn.Locals {
			if local == nil {
				continue
			}
			fmt.Fprintf(b, "    %s: %s", local.Name, renderType(local.Type))
			flags := make([]string, 0, 3)
			if local.IsTemp {
				flags = append(flags, "temp")
			}
			if local.Mutable {
				flags = append(flags, "mut")
			}
			if local.Constant {
				flags = append(flags, "const")
			}
			if len(flags) > 0 {
				fmt.Fprintf(b, " [%s]", strings.Join(flags, ", "))
			}
			b.WriteByte('\n')
		}
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		fmt.Fprintf(b, "%s:\n", blockLabel(block.ID))
		for _, instr := range block.Instructions {
			fmt.Fprintf(b, "    %s\n", formatInstr(instr))
		}
		fmt.Fprintf(b, "    %s\n", formatTerminator(block.Terminator))
	}
	b.WriteString("}\n")
}

func formatParam(param *Param) string {
	if param == nil {
		return ""
	}
	return typeinfo.DefaultPrinter.NamedParamText(param.Name, param.Type, param.IsMutable)
}

func formatMethodParams(params []*Param) string {
	items := make([]string, 0, len(params))
	for i, param := range params {
		if param == nil {
			continue
		}
		if i == 0 {
			items = append(items, typeinfo.DefaultPrinter.ReceiverText("self", param.Type))
			continue
		}
		items = append(items, formatParam(param))
	}
	return typeinfo.DefaultPrinter.ParamList(items)
}

func formatParams(params []*Param) []string {
	items := make([]string, 0, len(params))
	for _, param := range params {
		if param == nil {
			continue
		}
		items = append(items, formatParam(param))
	}
	return items
}

func prettyFunctionName(fn *Function) string {
	if fn == nil {
		return ""
	}
	if fn.LinkName != "" && strings.Contains(fn.LinkName, "__") {
		parts := strings.SplitN(fn.LinkName, "__", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0] + "::" + parts[1]
		}
	}
	return fn.Name
}

func formatInstr(instr Instr) string {
	switch i := instr.(type) {
	case nil:
		return "<nil>"
	case *BindInstr:
		kw := "let"
		if i.Constant {
			kw = "const"
		} else if i.Mutable {
			kw = "let mut"
		}
		return fmt.Sprintf("%s %s: %s = %s", kw, i.Name, renderType(i.Type), formatValue(i.Value))
	case *ComputeInstr:
		return fmt.Sprintf("%s = %s", formatLocalRef(currentFnForFormat, i.TargetID), formatValue(i.Value))
	case *AssignInstr:
		return fmt.Sprintf("%s = %s", formatLocalRef(currentFnForFormat, i.TargetID), formatValue(i.Value))
	case *StoreInstr:
		return fmt.Sprintf("store %s = %s", formatPlace(i.Target), formatValue(i.Value))
	case *StoreFieldInstr:
		return fmt.Sprintf("store_field %s %d %s", wrapValue(i.Base), i.FieldIndex, formatValue(i.Value))
	case *EvalInstr:
		return fmt.Sprintf("eval %s", formatValue(i.Value))
	case *DeferInstr:
		if len(i.Body) == 1 {
			if eval, ok := i.Body[0].(*EvalInstr); ok {
				return fmt.Sprintf("defer %s", formatValue(eval.Value))
			}
		}
		if len(i.Body) == 2 {
			compute, ok0 := i.Body[0].(*ComputeInstr)
			eval, ok1 := i.Body[1].(*EvalInstr)
			if ok0 && ok1 {
				if local, ok := eval.Value.(*LocalValue); ok && local.LocalID == compute.TargetID {
					if _, ok := compute.Value.(*CallValue); ok {
						return fmt.Sprintf("defer %s", formatValue(compute.Value))
					}
				}
			}
		}
		parts := make([]string, 0, len(i.Body))
		for _, child := range i.Body {
			parts = append(parts, formatInstr(child))
		}
		if len(parts) == 1 {
			return fmt.Sprintf("defer %s", parts[0])
		}
		return fmt.Sprintf("defer { %s }", strings.Join(parts, "; "))
	case *LockInstr:
		return fmt.Sprintf("lock %s as %s", formatValue(i.Value), formatLocalRef(currentFnForFormat, i.LocalID))
	case *UnsafeInstr:
		return "unsafe"
	default:
		return "<instr>"
	}
}

func formatTerminator(term Terminator) string {
	switch t := term.(type) {
	case nil:
		return "exit"
	case *JumpTerm:
		return fmt.Sprintf("jump %s", blockLabel(t.TargetID))
	case *BranchTerm:
		return fmt.Sprintf("branch %s -> %s, %s", formatValue(t.Cond), blockLabel(t.TrueID), blockLabel(t.FalseID))
	case *SwitchTerm:
		parts := make([]string, 0, len(t.Cases))
		for _, kase := range t.Cases {
			parts = append(parts, fmt.Sprintf("case %s: %s", formatValue(kase.Expr), blockLabel(kase.TargetID)))
		}
		parts = append(parts, fmt.Sprintf("default: %s", blockLabel(t.DefaultID)))
		return fmt.Sprintf("switch %s { %s }", formatValue(t.Value), strings.Join(parts, ", "))
	case *ReturnTerm:
		if t.Value == nil {
			if t.CleanupID >= 0 {
				return fmt.Sprintf("return unwind %s", blockLabel(t.CleanupID))
			}
			return "return"
		}
		if t.CleanupID >= 0 {
			return fmt.Sprintf("return %s unwind %s", formatValue(t.Value), blockLabel(t.CleanupID))
		}
		return fmt.Sprintf("return %s", formatValue(t.Value))
	case *PanicTerm:
		if t.CleanupID >= 0 {
			return fmt.Sprintf("panic %s unwind %s", formatValue(t.Value), blockLabel(t.CleanupID))
		}
		return fmt.Sprintf("panic %s", formatValue(t.Value))
	case *ExitTerm:
		return "exit"
	default:
		return "<term>"
	}
}

func formatValue(value Value) string {
	switch v := value.(type) {
	case nil:
		return "<nil>"
	case *NameValue:
		return strings.Join(v.Path, "::")
	case *LocalValue:
		return formatLocalRef(currentFnForFormat, v.LocalID)
	case *TempValue:
		return v.Name
	case *NumberValue:
		return v.Value
	case *BoolValue:
		if v.Value {
			return "true"
		}
		return "false"
	case *StringValue:
		return fmt.Sprintf("%q", v.Value)
	case *NoneValue:
		return "none"
	case *UnaryValue:
		if v.Op == "unsafe" {
			return fmt.Sprintf("unsafe %s", wrapValue(v.Right))
		}
		return formatPrefix(v.Op, wrapValue(v.Right))
	case *AddrOfValue:
		kw := "addr_of"
		if v.Raw {
			kw = "raw_addr_of"
		}
		if v.Mutable {
			if v.Raw {
				kw = "raw_addr_of_mut"
			} else {
				kw = "addr_of_mut"
			}
		}
		return fmt.Sprintf("%s %s", kw, wrapValue(v.Source))
	case *LoadValue:
		return fmt.Sprintf("load %s", wrapValue(v.Pointer))
	case *BinaryValue:
		return fmt.Sprintf("%s %s %s", binaryOpcode(v.Op), wrapValue(v.Left), wrapValue(v.Right))
	case *PostfixValue:
		return fmt.Sprintf("%s%s", wrapValue(v.Left), v.Op)
	case *CallValue:
		args := make([]string, 0, len(v.Args))
		for _, arg := range v.Args {
			args = append(args, formatValue(arg))
		}
		return fmt.Sprintf("%s(%s)", wrapValue(v.Callee), strings.Join(args, ", "))
	case *FieldLoadValue:
		return fmt.Sprintf("field %s %d", wrapValue(v.Base), v.FieldIndex)
	case *FieldValue:
		if v.FieldIndex >= 0 {
			return fmt.Sprintf("%s.%s", wrapValue(v.Base), FieldName(v.Base.Type(), v.FieldIndex))
		}
		return fmt.Sprintf("%s.%s", wrapValue(v.Base), v.MemberName)
	case *CastValue:
		return fmt.Sprintf("%s as %s", wrapValue(v.Left), renderType(v.Type()))
	case *TypeTestValue:
		return fmt.Sprintf("%s is %s", wrapValue(v.Left), renderType(v.Target))
	case *CompositeValue:
		parts := make([]string, 0, len(v.Items))
		for _, item := range v.Items {
			if item.Name != "" {
				parts = append(parts, fmt.Sprintf(".%s = %s", item.Name, formatValue(item.Value)))
			} else if item.Key != nil {
				parts = append(parts, fmt.Sprintf("%s => %s", formatValue(item.Key), formatValue(item.Value)))
			} else {
				parts = append(parts, formatValue(item.Value))
			}
		}
		return fmt.Sprintf(".{ %s }", strings.Join(parts, ", "))
	case *InterfaceValue:
		return fmt.Sprintf("interface(%s)", formatValue(v.Value))
	case *IndexValue:
		return fmt.Sprintf("%s[%s]", wrapValue(v.Base), formatValue(v.Index))
	default:
		return "<value>"
	}
}

func formatTypeDecl(decl *TypeDecl) string {
	if decl == nil {
		return ""
	}
	switch {
	case decl.Struct != nil:
		var b strings.Builder
		fmt.Fprintf(&b, "type %s struct {", decl.Name)
		if len(decl.Struct.Fields) > 0 {
			b.WriteByte('\n')
			for _, field := range decl.Struct.Fields {
				if field == nil {
					continue
				}
				b.WriteString("    ")
				fmt.Fprintf(&b, "%s: %s", field.Name, renderType(field.Type))
				if field.Default != nil {
					fmt.Fprintf(&b, " = %s", formatValue(field.Default))
				}
				b.WriteByte('\n')
			}
			b.WriteByte('}')
			return b.String()
		}
		b.WriteString(" }")
		return b.String()
	case decl.Interface != nil:
		var b strings.Builder
		fmt.Fprintf(&b, "type %s interface {", decl.Name)
		if len(decl.Interface.Methods) > 0 {
			b.WriteByte('\n')
			for _, method := range decl.Interface.Methods {
				if method == nil {
					continue
				}
				b.WriteString("    ")
				fmt.Fprintf(&b, "%s%s", method.Name, formatInterfaceParamsMIR(method.Receiver, method.Params))
				if method.Result != nil {
					fmt.Fprintf(&b, " %s", renderType(method.Result))
				}
				b.WriteByte('\n')
			}
			b.WriteByte('}')
			return b.String()
		}
		b.WriteString(" }")
		return b.String()
	case decl.Enum != nil:
		return fmt.Sprintf("type %s enum { %s }", decl.Name, strings.Join(decl.Enum.Variants, ", "))
	case decl.Union != nil:
		parts := make([]string, 0, len(decl.Union.Members))
		for _, member := range decl.Union.Members {
			parts = append(parts, renderType(member))
		}
		return fmt.Sprintf("type %s union { %s }", decl.Name, strings.Join(parts, ", "))
	case decl.Error != nil:
		return fmt.Sprintf("type %s error { %s }", decl.Name, strings.Join(decl.Error.Members, ", "))
	default:
		return fmt.Sprintf("type %s %s", decl.Name, renderType(decl.Underlying))
	}
}

func formatInterfaceParamsMIR(receiver string, params []*Param) string {
	var b strings.Builder
	b.WriteByte('(')
	wrote := false
	switch receiver {
	case "&":
		b.WriteString("&self")
		wrote = true
	case "&mut ":
		b.WriteString("&mut self")
		wrote = true
	case "*":
		b.WriteString("*self")
		wrote = true
	}
	for _, param := range params {
		if param == nil {
			continue
		}
		if wrote {
			b.WriteString(", ")
		}
		b.WriteString(formatParam(param))
		wrote = true
	}
	b.WriteByte(')')
	return b.String()
}

var currentFnForFormat *Function
var currentModuleForFormat *Module
var currentBlockLabels map[int]string

func formatLocalRef(fn *Function, id int) string {
	if fn == nil {
		return fmt.Sprintf("%%%d", id)
	}
	if local := fn.LocalByID(id); local != nil {
		return local.Name
	}
	return fmt.Sprintf("%%%d", id)
}

func wrapValue(value Value) string {
	switch value.(type) {
	case *NameValue, *LocalValue, *NumberValue, *BoolValue, *StringValue, *NoneValue, *FieldLoadValue, *FieldValue, *CallValue, *CompositeValue:
		return formatValue(value)
	default:
		return "(" + formatValue(value) + ")"
	}
}

func formatPlace(place Place) string {
	switch p := place.(type) {
	case nil:
		return "<nil>"
	case *LocalPlace:
		return formatLocalRef(currentFnForFormat, p.LocalID)
	case *FieldPlace:
		return fmt.Sprintf("field %s %d", formatPlace(p.Base), p.FieldIndex)
	case *IndexPlace:
		return fmt.Sprintf("index %s [%s]", formatPlace(p.Base), formatValue(p.Index))
	case *DerefPlace:
		return fmt.Sprintf("deref %s", formatValue(p.Pointer))
	default:
		return "<place>"
	}
}

func globalKeyword(global *Global) string {
	if global == nil {
		return "let"
	}
	if global.Constant {
		return "const"
	}
	if global.Mutable {
		return "let mut"
	}
	return "let"
}

func renderType(typ fmt.Stringer) string {
	if typ == nil {
		return "void"
	}
	if currentModuleForFormat != nil {
		return typeinfo.DefaultPrinter.WithLocalStrip(currentModuleForFormat.ImportPath).Type(typ)
	}
	return typeinfo.DefaultPrinter.Type(typ)
}

func formatPrefix(op, right string) string {
	switch op {
	case "copy", "comptime", "comptime_soft", "unsafe", "take", "&mut":
		if op == "comptime_soft" {
			op = "comptime"
		}
		return op + " " + right
	default:
		return op + right
	}
}

func binaryOpcode(op string) string {
	switch op {
	case "+":
		return "add"
	case "-":
		return "sub"
	case "*":
		return "mul"
	case "/":
		return "div"
	case "%":
		return "mod"
	case "==":
		return "eq"
	case "!=":
		return "neq"
	case "<":
		return "lt"
	case "<=":
		return "le"
	case ">":
		return "gt"
	case ">=":
		return "ge"
	case "&&":
		return "and"
	case "||":
		return "or"
	default:
		return op
	}
}

func blockLabels(fn *Function) map[int]string {
	if fn == nil {
		return map[int]string{}
	}
	labels := make(map[int]string, len(fn.Blocks))
	next := 1
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		if block.ID == fn.EntryID {
			labels[block.ID] = "entry"
			continue
		}
		labels[block.ID] = fmt.Sprintf("bb%d", next)
		next++
	}
	return labels
}

func blockLabel(id int) string {
	if currentBlockLabels != nil {
		if label, ok := currentBlockLabels[id]; ok {
			return label
		}
	}
	if id < 0 {
		return "<none>"
	}
	return fmt.Sprintf("bb%d", id)
}
