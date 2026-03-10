package ast

import "fmt"

func DeclSummary(decl Decl) string {
	switch d := decl.(type) {
	case *TypeDecl:
		return fmt.Sprintf("type %s", d.Name)
	case *ConstDecl:
		return fmt.Sprintf("const %s", d.Name)
	case *LetDecl:
		if d.IsMut {
			return fmt.Sprintf("let mut %s", d.Name)
		}
		return fmt.Sprintf("let %s", d.Name)
	case *FuncDecl:
		if d.Receiver != nil {
			if d.IsConstructor {
				return fmt.Sprintf("ctor %s", d.Name)
			}
			if d.IsDestructor {
				return fmt.Sprintf("method ~%s", d.Name)
			}
			return fmt.Sprintf("method %s", d.Name)
		}
		if d.IsDestructor {
			return fmt.Sprintf("fn ~%s", d.Name)
		}
		return fmt.Sprintf("fn %s", d.Name)
	default:
		return fmt.Sprintf("%T", decl)
	}
}
