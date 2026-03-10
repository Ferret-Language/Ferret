package symbols

import (
	"unicode"
	"unicode/utf8"

	"compiler/internal/frontend/ast"
	"compiler/internal/source"
)

type Kind string

const (
	SymbolImport  Kind = "import"
	SymbolVar     Kind = "var"
	SymbolConst   Kind = "const"
	SymbolType    Kind = "type"
	SymbolFunc    Kind = "func"
	SymbolMethod  Kind = "method"
	SymbolParam   Kind = "param"
	SymbolField   Kind = "field"
	SymbolStatic  Kind = "static"
	SymbolVariant Kind = "variant"
	SymbolError   Kind = "error_member"
	SymbolUnknown Kind = "unknown"
)

type Symbol struct {
	Name         string
	Kind         Kind
	Exported     bool
	Location     source.Location
	Node         ast.Node
	ReceiverType string
}

func New(name string, kind Kind, node ast.Node) *Symbol {
	loc := source.Location{}
	if node != nil {
		loc = node.Loc()
	}
	return &Symbol{
		Name:     name,
		Kind:     kind,
		Exported: isExported(name),
		Location: loc,
		Node:     node,
	}
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}
