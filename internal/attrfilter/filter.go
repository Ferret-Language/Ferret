package attrfilter

import (
	"fmt"

	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
)

func FilterModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.AST == nil {
		return
	}
	filtered := make([]ast.Decl, 0, len(mod.AST.Decls))
	for _, decl := range mod.AST.Decls {
		include, ok := includeDecl(ctx, decl)
		if !ok {
			continue
		}
		if include {
			filtered = append(filtered, decl)
		}
	}
	mod.AST.Decls = filtered
}

func includeDecl(ctx *context.CompilerContext, decl ast.Decl) (bool, bool) {
	attrs := declAttrs(decl)
	if len(attrs) == 0 {
		return true, true
	}
	for _, attr := range attrs {
		if attr.Name != "if" {
			continue
		}
		ok, valid := evalIfAttr(ctx, attr)
		if !valid {
			loc := attr.Location
			ctx.Diagnostics.Add(
				diagnostics.NewError("invalid #[if(...)] attribute").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "supported forms are #[if(debug)], #[if(not, debug)], #[if(target_os, \"...\")], #[if(target_arch, \"...\")], #[if(not, target_os, \"...\")], and #[if(not, target_arch, \"...\")]"),
			)
			return false, false
		}
		if !ok {
			return false, true
		}
	}
	return true, true
}

func declAttrs(decl ast.Decl) []ast.Attribute {
	switch d := decl.(type) {
	case *ast.LetDecl:
		return d.Attrs
	case *ast.ConstDecl:
		return d.Attrs
	case *ast.TypeDecl:
		return d.Attrs
	case *ast.FuncDecl:
		return d.Attrs
	default:
		return nil
	}
}

func evalIfAttr(ctx *context.CompilerContext, attr ast.Attribute) (bool, bool) {
	if ctx == nil {
		return false, false
	}
	switch len(attr.Args) {
	case 1:
		switch attr.Args[0] {
		case "debug":
			return ctx.Config.BuildDebug, true
		default:
			return false, false
		}
	case 2:
		if attr.Args[0] == "not" {
			switch attr.Args[1] {
			case "debug":
				return !ctx.Config.BuildDebug, true
			default:
				return false, false
			}
		}
		return matchTargetAttr(ctx, attr.Args[0], attr.Args[1])
	case 3:
		if attr.Args[0] != "not" {
			return false, false
		}
		match, ok := matchTargetAttr(ctx, attr.Args[1], attr.Args[2])
		if !ok {
			return false, false
		}
		return !match, true
	default:
		return false, false
	}
}

func matchTargetAttr(ctx *context.CompilerContext, key, value string) (bool, bool) {
	switch key {
	case "target_os":
		return ctx.Config.TargetOS == value, true
	case "target_arch":
		return ctx.Config.TargetArch == value, true
	default:
		return false, false
	}
}

func ExplainConfig(ctx *context.CompilerContext) string {
	if ctx == nil {
		return ""
	}
	return fmt.Sprintf("target_os=%q, target_arch=%q, debug=%t", ctx.Config.TargetOS, ctx.Config.TargetArch, ctx.Config.BuildDebug)
}
