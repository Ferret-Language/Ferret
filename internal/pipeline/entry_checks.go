package pipeline

import (
	"errors"
	//"fmt"

	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
)

func (p *Pipeline) ensureEntryMain() error {
	if p == nil || p.ctx == nil {
		return nil
	}

	entryModule := p.ctx.EntryModule
	if entryModule == "" {
		return nil
	}

	mod, ok := p.ctx.GetModule(entryModule)
	if !ok || mod == nil || mod.AST == nil {
		return nil
	}

	if hasMainFunc(mod.AST) {
		return nil
	}

	diag := diagnostics.NewError("missing entry point: function 'main' not found in code")
	var loc *source.Location
	if len(mod.AST.Nodes) > 0 {
		lastNode := mod.AST.Nodes[len(mod.AST.Nodes)-1]
		if lastNode != nil {
			loc = lastNode.Loc()
		}
	} else {
		pos := &source.Position{Line: 1, Column: 1, Index: 0}
		loc = &source.Location{
			Filename: &entryModule,
			Start:    pos,
			End:      pos,
		}
	}

	if loc != nil {
		diag = diag.WithCodeHint(loc, "fn main() {\n    // your code\n}", diagnostics.CodeHintLabel{
			Line:    3,
			Column:  1,
			Length:  1,
			Message: "Add your main function here",
			Style:   diagnostics.Secondary,
		})
	}
	p.ctx.Diagnostics.Add(diag)
	return errors.New("missing entry point: function 'main' not found")
}

func hasMainFunc(mod *ast.Module) bool {
	if mod == nil {
		return false
	}

	for _, node := range mod.Nodes {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		if fn.Name.Name == "main" {
			return true
		}
	}

	return false
}
