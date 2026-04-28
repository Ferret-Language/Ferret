package ast

import "fmt"

func DeclSummary(decl Decl) string {
	switch d := decl.(type) {
	case *TypeDecl:
		return fmt.Sprintf("type %s", d.Name.Text())
	case *ConstDecl:
		return fmt.Sprintf("const %s", d.Name.Text())
	case *LetDecl:
		if d.IsAtomic {
			return fmt.Sprintf("let atomic %s", d.Name.Text())
		}
		if d.IsMut {
			return fmt.Sprintf("let mut %s", d.Name.Text())
		}
		return fmt.Sprintf("let %s", d.Name.Text())
	case *FuncDecl:
		if d.IsTest {
			return fmt.Sprintf("test %q", d.TestName)
		}
		if d.Receiver != nil {
			return fmt.Sprintf("method %s", d.Name.Text())
		}
		return fmt.Sprintf("fn %s", d.Name.Text())
	default:
		return fmt.Sprintf("%T", decl)
	}
}
