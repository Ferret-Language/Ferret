package ast

import "fmt"

func DeclSummary(decl Decl) string {
	switch d := decl.(type) {
	case *TypeDecl:
		return fmt.Sprintf("type %s", d.Name)
	case *ConstDecl:
		return fmt.Sprintf("const %s", d.Name)
	case *FuncDecl:
		if d.Receiver != nil {
			return fmt.Sprintf("method %s", d.Name)
		}
		return fmt.Sprintf("fn %s", d.Name)
	default:
		return fmt.Sprintf("%T", decl)
	}
}
