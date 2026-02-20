package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/types"
	"fmt"
	"strings"
)

// diagnosticTypeString renders semantic types for user-facing diagnostics,
// replacing internal mangled generic names with readable forms (e.g. Box<i32>).
func diagnosticTypeString(ctx *context_v2.CompilerContext, mod *context_v2.Module, typ types.SemType) string {
	return diagnosticTypeStringRec(ctx, mod, typ, map[string]bool{})
}

func diagnosticTypeStringRec(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	typ types.SemType,
	seen map[string]bool,
) string {
	if typ == nil {
		return "unknown"
	}

	switch t := typ.(type) {
	case *types.NamedType:
		if t == nil {
			return "unknown"
		}
		if pretty, ok := readableGenericNamedType(ctx, mod, t.Name, seen); ok {
			return pretty
		}
		return t.Name
	case *types.ArrayType:
		if t.Length < 0 {
			return fmt.Sprintf("[]%s", diagnosticTypeStringRec(ctx, mod, t.Element, seen))
		}
		return fmt.Sprintf("[%d]%s", t.Length, diagnosticTypeStringRec(ctx, mod, t.Element, seen))
	case *types.MapType:
		return fmt.Sprintf(
			"map[%s]%s",
			diagnosticTypeStringRec(ctx, mod, t.Key, seen),
			diagnosticTypeStringRec(ctx, mod, t.Value, seen),
		)
	case *types.OptionalType:
		return fmt.Sprintf("?%s", diagnosticTypeStringRec(ctx, mod, t.Inner, seen))
	case *types.ReferenceType:
		if t.Mutable {
			return fmt.Sprintf("&mut %s", diagnosticTypeStringRec(ctx, mod, t.Inner, seen))
		}
		return fmt.Sprintf("&%s", diagnosticTypeStringRec(ctx, mod, t.Inner, seen))
	case *types.HeapType:
		return fmt.Sprintf("#%s", diagnosticTypeStringRec(ctx, mod, t.Inner, seen))
	case *types.ResultType:
		return fmt.Sprintf(
			"%s ! %s",
			diagnosticTypeStringRec(ctx, mod, t.Err, seen),
			diagnosticTypeStringRec(ctx, mod, t.Ok, seen),
		)
	case *types.UnionType:
		if len(t.Variants) == 0 {
			return "union {}"
		}
		parts := make([]string, len(t.Variants))
		for i, v := range t.Variants {
			parts[i] = diagnosticTypeStringRec(ctx, mod, v, seen)
		}
		return fmt.Sprintf("union {%s}", strings.Join(parts, ", "))
	case *types.FunctionType:
		params := make([]string, len(t.Params))
		for i, p := range t.Params {
			typeStr := diagnosticTypeStringRec(ctx, mod, p.Type, seen)
			prefix := ""
			if p.IsMove {
				prefix = "@"
			}
			if p.IsVariadic {
				params[i] = fmt.Sprintf("%s: ...%s%s", p.Name, prefix, typeStr)
			} else {
				params[i] = fmt.Sprintf("%s: %s%s", p.Name, prefix, typeStr)
			}
		}
		return fmt.Sprintf("fn(%s) -> %s", strings.Join(params, ", "), diagnosticTypeStringRec(ctx, mod, t.Return, seen))
	default:
		return typ.String()
	}
}

func readableGenericNamedType(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	name string,
	seen map[string]bool,
) (string, bool) {
	if name == "" {
		return "", false
	}
	if seen[name] {
		if baseName, ok := genericBaseNameFromMangled(name); ok && baseName != "" {
			return baseName, true
		}
		return name, true
	}
	seen[name] = true
	defer delete(seen, name)

	inst, ok := lookupGenericNamedTypeInstantiationForDiag(ctx, mod, name)
	if ok && inst != nil && inst.BaseName != "" {
		if len(inst.TypeArgs) == 0 {
			return inst.BaseName, true
		}
		args := make([]string, 0, len(inst.TypeArgs))
		for _, arg := range inst.TypeArgs {
			args = append(args, diagnosticTypeStringRec(ctx, mod, arg, seen))
		}
		return fmt.Sprintf("%s<%s>", inst.BaseName, strings.Join(args, ", ")), true
	}

	// Fallback if instantiation metadata is unavailable.
	if baseName, ok := genericBaseNameFromMangled(name); ok && baseName != "" {
		return baseName, true
	}
	return "", false
}

func lookupGenericNamedTypeInstantiationForDiag(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	name string,
) (*context_v2.GenericNamedTypeInstantiation, bool) {
	if mod != nil {
		if inst, ok := mod.GenericNamedTypeInstantiation(name); ok && inst != nil {
			return inst, true
		}
	}
	if ctx != nil && mod != nil {
		for _, importPath := range mod.ImportAliasMap {
			imported, ok := ctx.GetModule(importPath)
			if !ok || imported == nil {
				continue
			}
			if inst, ok := imported.GenericNamedTypeInstantiation(name); ok && inst != nil {
				return inst, true
			}
		}
	}
	return nil, false
}
