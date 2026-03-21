package common

import (
	"fmt"
	"strings"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/ir/mir"
)

func FindLocalByName(fn *mir.Function, name string) *mir.Local {
	if fn == nil {
		return nil
	}
	for _, local := range fn.Locals {
		if local != nil && local.Name == name {
			return local
		}
	}
	return nil
}

func FindLocalByID(fn *mir.Function, id int) *mir.Local {
	if fn == nil {
		return nil
	}
	for _, local := range fn.Locals {
		if local != nil && local.ID == id {
			return local
		}
	}
	return nil
}

func LocalNameByID(fn *mir.Function, id int) string {
	if fn == nil {
		return fmt.Sprintf("t%d", id)
	}
	for _, local := range fn.Locals {
		if local != nil && local.ID == id {
			return local.Name
		}
	}
	return fmt.Sprintf("t%d", id)
}

func LocalTypeByID(fn *mir.Function, id int) typeinfo.Type {
	if fn == nil {
		return typeinfo.UnknownType{}
	}
	for _, local := range fn.Locals {
		if local != nil && local.ID == id {
			return local.Type
		}
	}
	return typeinfo.UnknownType{}
}

func SanitizePath(path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		switch r {
		case '/', ':', '.', '-', ' ':
			return true
		}
		return false
	})
	if len(parts) == 0 {
		return "mod"
	}
	for i := range parts {
		parts[i] = SanitizeIdent(parts[i])
	}
	return strings.Join(parts, "__")
}

func SanitizeType(typ typeinfo.Type) string {
	format := "void"
	if typ != nil {
		format = typeinfo.FormatType(typ)
	}
	return SanitizeIdent(strings.NewReplacer(
		"local:", "",
		"::", "__",
		"*", "ptr_",
		" ", "_",
		"?", "opt_",
		"!", "_",
		"/", "__",
	).Replace(format))
}

func SanitizeIdent(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for i, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "_"
	}
	return out
}
