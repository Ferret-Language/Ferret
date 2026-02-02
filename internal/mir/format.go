package mir

import (
	"fmt"
	"os"
	"strings"

	"compiler/internal/types"
)

// FormatModule returns a readable text representation of the MIR module.
func FormatModule(mod *Module) string {
	if mod == nil {
		return ""
	}

	var b strings.Builder
	if mod.ImportPath != "" {
		fmt.Fprintf(&b, "module %s\n", mod.ImportPath)
	} else {
		b.WriteString("module <unknown>\n")
	}

	if len(mod.Globals) > 0 {
		b.WriteString("\nglobals\n")
		for _, glob := range mod.Globals {
			if glob.Name == "" {
				continue
			}
			fmt.Fprintf(&b, "  %s: %s\n", glob.Name, formatType(glob.Type))
		}
	}

	for _, fn := range mod.Functions {
		b.WriteString("\n")
		writeFunction(&b, fn)
	}

	return b.String()
}

// WriteModuleFile writes the formatted MIR module to disk.
func WriteModuleFile(mod *Module, path string) error {
	if mod == nil || path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(FormatModule(mod)), 0644)
}

func writeFunction(b *strings.Builder, fn *Function) {
	if fn == nil {
		return
	}

	fmt.Fprintf(b, "fn %s(", fn.Name)
	for i, param := range fn.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		if fn.Receiver != nil && param.ID == fn.Receiver.ID {
			fmt.Fprintf(b, "receiver %s: %s", param.Name, formatType(param.Type))
		} else {
			fmt.Fprintf(b, "%s: %s", param.Name, formatType(param.Type))
		}
	}
	fmt.Fprintf(b, ") -> %s {\n", formatType(fn.Return))

	for _, block := range fn.Blocks {
		writeBlock(b, block)
	}

	b.WriteString("}\n")
}

func writeBlock(b *strings.Builder, block *Block) {
	if block == nil {
		return
	}

	if block.Name != "" {
		fmt.Fprintf(b, "  block b%d %s:\n", block.ID, block.Name)
	} else {
		fmt.Fprintf(b, "  block b%d:\n", block.ID)
	}

	for _, instr := range block.Instrs {
		fmt.Fprintf(b, "    %s\n", formatInstr(instr))
	}

	if block.Term != nil {
		fmt.Fprintf(b, "    %s\n", formatTerm(block.Term))
	} else {
		b.WriteString("    term <nil>\n")
	}
}

func formatInstr(instr Instr) string {
	switch i := instr.(type) {
	case *Const:
		return formatAssign(i.Result, fmt.Sprintf("const %s %s", formatType(i.Type), i.Value))
	case *Binary:
		return formatAssign(i.Result, fmt.Sprintf("%s %s %s, %s", i.Op, formatType(i.Type), formatValue(i.Left), formatValue(i.Right)))
	case *Unary:
		return formatAssign(i.Result, fmt.Sprintf("%s %s %s", i.Op, formatType(i.Type), formatValue(i.X)))
	case *Cast:
		return formatAssign(i.Result, fmt.Sprintf("cast %s %s", formatType(i.Type), formatValue(i.X)))
	case *Alloca:
		return formatAssign(i.Result, fmt.Sprintf("alloca %s", formatType(i.Type)))
	case *Load:
		return formatAssign(i.Result, fmt.Sprintf("load %s %s", formatType(i.Type), formatValue(i.Addr)))
	case *PtrAdd:
		return formatAssign(i.Result, fmt.Sprintf("ptr_add %s %s, %d", formatType(i.Elem), formatValue(i.Base), i.Offset))
	case *PtrOffset:
		return formatAssign(i.Result, fmt.Sprintf("ptr_offset %s %s, %s", formatType(i.Elem), formatValue(i.Base), formatValue(i.Offset)))
	case *Store:
		return fmt.Sprintf("store %s, %s", formatValue(i.Addr), formatValue(i.Value))
	case *Call:
		return formatAssign(i.Result, fmt.Sprintf("call %s(%s) : %s", i.Target, formatValues(i.Args), formatType(i.Type)))
	case *CallIndirect:
		return formatAssign(i.Result, fmt.Sprintf("call_indirect %s %s(%s)", formatType(i.Type), formatValue(i.Callee), formatValues(i.Args)))
	case *Phi:
		return formatAssign(i.Result, fmt.Sprintf("phi %s %s", formatType(i.Type), formatPhiIncoming(i.Incoming)))
	case *ArrayGet:
		return formatAssign(i.Result, fmt.Sprintf("array_get %s %s, %s", formatType(i.Type), formatValue(i.Array), formatValue(i.Index)))
	case *ArraySet:
		return fmt.Sprintf("array_set %s, %s, %s", formatValue(i.Array), formatValue(i.Index), formatValue(i.Value))
	case *MapGet:
		return formatAssign(i.Result, fmt.Sprintf("map_get %s %s, %s", formatType(i.Type), formatValue(i.Map), formatValue(i.Key)))
	case *MapSet:
		return fmt.Sprintf("map_set %s, %s, %s", formatValue(i.Map), formatValue(i.Key), formatValue(i.Value))
	case *OptionalNone:
		return formatAssign(i.Result, fmt.Sprintf("optional_none %s", formatType(i.Type)))
	case *OptionalSome:
		return formatAssign(i.Result, fmt.Sprintf("optional_some %s %s", formatType(i.Type), formatValue(i.Value)))
	case *OptionalIsSome:
		return formatAssign(i.Result, fmt.Sprintf("optional_is_some %s", formatValue(i.Value)))
	case *OptionalUnwrap:
		return formatAssign(i.Result, fmt.Sprintf("optional_unwrap %s %s%s", formatType(i.Type), formatValue(i.Value), formatDefault(i.Default, i.HasDefault)))
	case *UnionVariantCheck:
		return formatAssign(i.Result, fmt.Sprintf("union_variant_check %s variant=%d", formatValue(i.Value), i.VariantIndex))
	case *UnionExtract:
		return formatAssign(i.Result, fmt.Sprintf("union_extract %s %s variant=%d", formatType(i.Type), formatValue(i.Value), i.VariantIndex))
	case *ResultOk:
		return formatAssign(i.Result, fmt.Sprintf("result_ok %s %s", formatType(i.Type), formatValue(i.Value)))
	case *ResultErr:
		return formatAssign(i.Result, fmt.Sprintf("result_err %s %s", formatType(i.Type), formatValue(i.Value)))
	case *ResultIsOk:
		return formatAssign(i.Result, fmt.Sprintf("result_is_ok %s", formatValue(i.Value)))
	case *ResultUnwrap:
		return formatAssign(i.Result, fmt.Sprintf("result_unwrap %s %s%s", formatType(i.Type), formatValue(i.Value), formatDefault(i.Default, i.HasDefault)))
	default:
		return "instr <unknown>"
	}
}

func formatTerm(term Term) string {
	switch t := term.(type) {
	case *Return:
		if t.HasValue {
			return fmt.Sprintf("ret %s", formatValue(t.Value))
		}
		return "ret"
	case *Br:
		return fmt.Sprintf("br %s", formatBlock(t.Target))
	case *CondBr:
		return fmt.Sprintf("br_if %s, %s, %s", formatValue(t.Cond), formatBlock(t.Then), formatBlock(t.Else))
	case *Switch:
		return fmt.Sprintf("switch %s %s default %s", formatValue(t.Cond), formatSwitchCases(t.Cases), formatBlock(t.Default))
	case *Unreachable:
		return "unreachable"
	default:
		return "term <unknown>"
	}
}

func formatAssign(result ValueID, body string) string {
	if result == InvalidValue {
		return body
	}
	return fmt.Sprintf("%s = %s", formatValue(result), body)
}

func formatValue(id ValueID) string {
	if id == InvalidValue {
		return "%<invalid>"
	}
	return fmt.Sprintf("%%t%d", id)
}

func formatBlock(id BlockID) string {
	if id == InvalidBlock {
		return "b<invalid>"
	}
	return fmt.Sprintf("b%d", id)
}

func formatValues(values []ValueID) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, id := range values {
		parts = append(parts, formatValue(id))
	}
	return strings.Join(parts, ", ")
}

func formatPhiIncoming(incoming []PhiIncoming) string {
	if len(incoming) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(incoming))
	for _, in := range incoming {
		parts = append(parts, fmt.Sprintf("%s: %s", formatBlock(in.Pred), formatValue(in.Value)))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatSwitchCases(cases []SwitchCase) string {
	if len(cases) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(cases))
	for _, c := range cases {
		parts = append(parts, fmt.Sprintf("%s: %s", c.Value, formatBlock(c.Target)))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatDefault(value ValueID, enabled bool) string {
	if !enabled {
		return ""
	}
	return fmt.Sprintf(", default %s", formatValue(value))
}

func formatType(t types.SemType) string {
	if t == nil {
		return "void"
	}
	return t.String()
}
