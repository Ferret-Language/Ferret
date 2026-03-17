package hir

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"fmt"
	"strings"
)

func FormatModule(mod *Module) string {
	if mod == nil {
		return ""
	}
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
	for _, global := range mod.Globals {
		if global == nil {
			continue
		}
		fmt.Fprintf(&b, "%s %s: %s = %s\n", globalKeyword(global), global.Name, typeString(global.Type), formatExpr(global.Value))
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
		formatFunc(&b, fn)
	}
	return b.String()
}

func formatFunc(b *strings.Builder, fn *Func) {
	if fn.IsBuiltin {
		b.WriteString("#[builtin]\n")
	}
	if fn.IsExtern {
		fmt.Fprintf(b, "#[extern(%q)]\n", fn.ExternName)
	}
	if fn.IsUnsafe {
		b.WriteString("unsafe ")
	}
	name := fn.Name
	if fn.OwnerType != "" {
		name = fn.OwnerType + "::" + name
	}
	if fn.OwnerType != "" {
		fmt.Fprintf(b, "fn %s%s", name, formatAttachedParams(fn.Receiver, fn.Params))
	} else {
		fmt.Fprintf(b, "fn %s%s", formatReceiver(fn.Receiver), name)
		b.WriteByte('(')
		for i, param := range fn.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(formatParam(param))
		}
		b.WriteByte(')')
	}
	if fn.Result != nil {
		fmt.Fprintf(b, " %s", typeString(fn.Result))
	}
	if fn.Body == nil {
		b.WriteString(";\n")
		return
	}
	b.WriteString(" ")
	formatBlock(b, fn.Body, 0)
	b.WriteByte('\n')
}

func formatReceiver(param *Param) string {
	if param == nil {
		return ""
	}
	return fmt.Sprintf("(%s) ", formatParam(param))
}

func formatAttachedParams(receiver *Param, params []*Param) string {
	var b strings.Builder
	b.WriteByte('(')
	wrote := false
	if receiver != nil {
		b.WriteString(formatReceiverArg(receiver))
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

func formatReceiverArg(param *Param) string {
	if param == nil {
		return ""
	}
	switch t := param.Type.(type) {
	case *typeinfo.PointerType:
		_ = t
		return "*self"
	case *typeinfo.RefType:
		if t.Mutable {
			return "&mut self"
		}
		return "&self"
	default:
		return "self"
	}
}

func formatParam(param *Param) string {
	if param == nil {
		return ""
	}
	prefix := ""
	if param.IsComptime {
		prefix = "comptime "
	}
	return fmt.Sprintf("%s%s %s", prefix, param.Name, typeString(param.Type))
}

func formatBlock(b *strings.Builder, block *BlockStmt, indent int) {
	if block == nil {
		b.WriteString("{}")
		return
	}
	b.WriteString("{\n")
	for _, stmt := range block.Stmts {
		formatStmt(b, stmt, indent+1)
	}
	indentLine(b, indent)
	b.WriteByte('}')
}

func formatStmt(b *strings.Builder, stmt Stmt, indent int) {
	if stmt == nil {
		return
	}
	indentLine(b, indent)
	switch s := stmt.(type) {
	case *BlockStmt:
		formatBlock(b, s, indent)
	case *LetStmt:
		fmt.Fprintf(b, "let")
		if s.Mutable {
			b.WriteString(" mut")
		}
		fmt.Fprintf(b, " %s", s.Name)
		if s.Type != nil {
			fmt.Fprintf(b, ": %s", typeString(s.Type))
		}
		if s.Value != nil {
			fmt.Fprintf(b, " = %s", formatExpr(s.Value))
		}
	case *ConstStmt:
		fmt.Fprintf(b, "const %s", s.Name)
		if s.Type != nil {
			fmt.Fprintf(b, ": %s", typeString(s.Type))
		}
		if s.Value != nil {
			fmt.Fprintf(b, " = %s", formatExpr(s.Value))
		}
	case *ReturnStmt:
		b.WriteString("return")
		if s.Value != nil {
			fmt.Fprintf(b, " %s", formatExpr(s.Value))
		}
	case *ExprStmt:
		b.WriteString(formatExpr(s.Value))
	case *AssignStmt:
		fmt.Fprintf(b, "%s = %s", formatExpr(s.Left), formatExpr(s.Right))
	case *IfStmt:
		fmt.Fprintf(b, "if %s ", formatExpr(s.Cond))
		formatBlock(b, s.Then, indent)
		if s.Else != nil {
			switch elseNode := s.Else.(type) {
			case *BlockStmt:
				b.WriteString(" else ")
				formatBlock(b, elseNode, indent)
			default:
				b.WriteString(" else\n")
				formatStmt(b, elseNode, indent+1)
			}
		}
	case *MatchStmt:
		fmt.Fprintf(b, "match %s {\n", formatExpr(s.Value))
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			indentLine(b, indent+1)
			if arm.Wildcard {
				b.WriteString("_ => ")
			} else if arm.TypePattern != nil {
				fmt.Fprintf(b, "is %s", arm.TypePattern.String())
				b.WriteString(" => ")
			} else {
				fmt.Fprintf(b, "%s => ", formatExpr(arm.Pattern))
			}
			formatBlock(b, arm.Body, indent+1)
			b.WriteByte('\n')
		}
		indentLine(b, indent)
		b.WriteByte('}')
	case *WhileStmt:
		fmt.Fprintf(b, "while %s ", formatExpr(s.Cond))
		formatBlock(b, s.Body, indent)
	case *ForStmt:
		fmt.Fprintf(b, "for %s |", formatExpr(s.Iterable))
		if s.IndexName != "" {
			b.WriteString(s.IndexName)
			b.WriteString(", ")
		}
		b.WriteString(s.ValueName)
		b.WriteString("| ")
		formatBlock(b, s.Body, indent)
	case *LoopStmt:
		b.WriteString("loop ")
		formatBlock(b, s.Body, indent)
	case *LabelStmt:
		fmt.Fprintf(b, "%s:\n", s.Name)
		formatStmt(b, s.Stmt, indent+1)
		return
	case *BreakStmt:
		b.WriteString("break")
		if s.Label != "" {
			b.WriteByte(' ')
			b.WriteString(s.Label)
		}
	case *ContinueStmt:
		b.WriteString("continue")
		if s.Label != "" {
			b.WriteByte(' ')
			b.WriteString(s.Label)
		}
	case *DeferStmt:
		b.WriteString("defer ")
		b.WriteString(strings.TrimSpace(stmtInline(s.Body)))
	case *ReleaseStmt:
		fmt.Fprintf(b, "release %s", formatExpr(s.Value))
	case *PanicStmt:
		fmt.Fprintf(b, "panic %s", formatExpr(s.Value))
	case *LockStmt:
		fmt.Fprintf(b, "lock %s as %s ", formatExpr(s.Value), s.Name)
		formatBlock(b, s.Body, indent)
	case *UnsafeStmt:
		b.WriteString("unsafe ")
		formatBlock(b, s.Body, indent)
	default:
		b.WriteString("<stmt>")
	}
	b.WriteByte('\n')
}

func stmtInline(stmt Stmt) string {
	var b strings.Builder
	formatStmt(&b, stmt, 0)
	return strings.TrimSuffix(b.String(), "\n")
}

func formatExpr(expr Expr) string {
	switch e := expr.(type) {
	case nil:
		return "<nil>"
	case *Ident:
		return strings.Join(e.Path, "::")
	case *BadExpr:
		return "<bad>"
	case *NumberLit:
		return e.Value
	case *StringLit:
		return fmt.Sprintf("%q", e.Value)
	case *NoneLit:
		return "none"
	case *PrefixExpr:
		return formatPrefix(e.Op, wrapExpr(e.Right))
	case *BinaryExpr:
		return fmt.Sprintf("%s %s %s", wrapExpr(e.Left), e.Op, wrapExpr(e.Right))
	case *PostfixExpr:
		return fmt.Sprintf("%s%s", wrapExpr(e.Left), e.Op)
	case *CallExpr:
		parts := make([]string, 0, len(e.Args))
		for _, arg := range e.Args {
			parts = append(parts, formatExpr(arg))
		}
		return fmt.Sprintf("%s(%s)", wrapExpr(e.Callee), strings.Join(parts, ", "))
	case *ConstructorCallExpr:
		parts := make([]string, 0, len(e.Args))
		for _, arg := range e.Args {
			parts = append(parts, formatExpr(arg))
		}
		return fmt.Sprintf("ctor %s(%s)", strings.Join(e.Path, "::"), strings.Join(parts, ", "))
	case *SelectorExpr:
		return fmt.Sprintf("%s.%s", wrapExpr(e.Left), e.Name)
	case *CastExpr:
		return fmt.Sprintf("%s as %s", wrapExpr(e.Left), typeString(e.Type()))
	case *IsExpr:
		return fmt.Sprintf("%s is %s", wrapExpr(e.Left), typeString(e.Target))
	case *MatchExpr:
		var b strings.Builder
		fmt.Fprintf(&b, "match %s {\n", formatExpr(e.Value))
		for _, arm := range e.Arms {
			if arm == nil {
				continue
			}
			indentLine(&b, 1)
			if arm.Wildcard {
				b.WriteString("_ => ")
			} else if arm.TypePattern != nil {
				fmt.Fprintf(&b, "is %s", arm.TypePattern.String())
				b.WriteString(" => ")
			} else {
				fmt.Fprintf(&b, "%s => ", formatExpr(arm.Pattern))
			}
			formatBlock(&b, arm.Body, 1)
			b.WriteByte('\n')
		}
		b.WriteByte('}')
		return b.String()
	case *CatchExpr:
		if e.Handler != nil {
			var b strings.Builder
			fmt.Fprintf(&b, "%s catch |%s| ", wrapExpr(e.Left), e.PayloadName)
			formatBlock(&b, e.Handler, 0)
			return b.String()
		}
		return fmt.Sprintf("%s catch %s", wrapExpr(e.Left), formatExpr(e.Fallback))
	case *CompositeLit:
		parts := make([]string, 0, len(e.Items))
		for _, item := range e.Items {
			if item.Name != "" {
				parts = append(parts, fmt.Sprintf(".%s = %s", item.Name, formatExpr(item.Value)))
			} else {
				parts = append(parts, formatExpr(item.Value))
			}
		}
		return fmt.Sprintf(".{ %s }", strings.Join(parts, ", "))
	case *IndexExpr:
		return fmt.Sprintf("%s[%s]", wrapExpr(e.Left), formatExpr(e.Index))
	default:
		return "<expr>"
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
				indentLine(&b, 1)
				fmt.Fprintf(&b, "%s %s", field.Name, typeString(field.Type))
				if field.Default != nil {
					fmt.Fprintf(&b, " = %s", formatExpr(field.Default))
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
				indentLine(&b, 1)
				fmt.Fprintf(&b, "%s%s", method.Name, formatInterfaceParams(method.Receiver, method.Params))
				if method.Result != nil {
					fmt.Fprintf(&b, " %s", typeString(method.Result))
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
			parts = append(parts, typeString(member))
		}
		return fmt.Sprintf("type %s union { %s }", decl.Name, strings.Join(parts, ", "))
	case decl.Error != nil:
		return fmt.Sprintf("type %s error { %s }", decl.Name, strings.Join(decl.Error.Members, ", "))
	default:
		return fmt.Sprintf("type %s %s", decl.Name, typeString(decl.Underlying))
	}
}

func formatInterfaceParams(receiver string, params []*Param) string {
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

func wrapExpr(expr Expr) string {
	switch expr.(type) {
	case *Ident, *NumberLit, *StringLit, *NoneLit, *SelectorExpr, *CallExpr, *ConstructorCallExpr, *CompositeLit:
		return formatExpr(expr)
	default:
		return "(" + formatExpr(expr) + ")"
	}
}

func formatPrefix(op, right string) string {
	switch op {
	case "copy", "comptime", "unsafe":
		return op + " " + right
	default:
		return op + right
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

func typeString(typ fmt.Stringer) string { return typeinfo.FormatType(typ) }

func indentLine(b *strings.Builder, indent int) {
	for i := 0; i < indent; i++ {
		b.WriteString("    ")
	}
}
