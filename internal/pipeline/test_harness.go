package pipeline

import (
	"fmt"
	"strings"

	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
)

func synthesizeTestHarness(mod *ast.Module, force bool, selectedTest string) bool {
	if mod == nil || len(mod.Decls) == 0 {
		return false
	}

	var tests []*ast.FuncDecl
	hasUserMain := false
	for _, decl := range mod.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil {
			continue
		}
		if fn.IsTest {
			tests = append(tests, fn)
			continue
		}
		if !fn.IsSynthetic && fn.Receiver == nil && fn.Name != nil && fn.Name.Text() == "main" {
			hasUserMain = true
		}
	}
	if len(tests) == 0 {
		return false
	}
	if selectedTest != "" {
		tests = selectTestsByName(tests, selectedTest)
		if len(tests) == 0 {
			return false
		}
	}
	if hasUserMain && !force {
		return false
	}

	loc := syntheticModuleLocation(mod)
	if force {
		filtered := mod.Decls[:0]
		for _, decl := range mod.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn != nil && !fn.IsSynthetic && !fn.IsTest && fn.Receiver == nil && fn.Name != nil && fn.Name.Text() == "main" {
				continue
			}
			filtered = append(filtered, decl)
		}
		mod.Decls = filtered
	}
	body := &ast.BlockStmt{
		Stmts:    make([]ast.Stmt, 0, len(tests)+1),
		Location: loc,
	}
	for _, testFn := range tests {
		if testFn == nil || testFn.Name == nil {
			continue
		}
		body.Stmts = append(body.Stmts, exprStmt(callExpr(loc, testFn.Name.Text())))
	}
	body.Stmts = append(body.Stmts, &ast.ReturnStmt{Value: intLit(loc, 0), Location: loc})
	mod.Decls = append(mod.Decls, &ast.FuncDecl{
		Name:        &ast.Ident{Path: []string{"main"}, Location: loc},
		IsSynthetic: true,
		Doc:         nil,
		Attrs:       nil,
		Params:      nil,
		Result:      namedType(loc, "i32"),
		Body:        body,
		Location:    loc,
	})
	return true
}

func selectTestsByName(tests []*ast.FuncDecl, selectedTest string) []*ast.FuncDecl {
	selectedTest = strings.TrimSpace(selectedTest)
	if selectedTest == "" {
		return tests
	}
	filtered := make([]*ast.FuncDecl, 0, 1)
	for _, fn := range tests {
		if fn == nil || fn.Name == nil {
			continue
		}
		if fn.Name.Text() == selectedTest || fn.TestName == selectedTest {
			filtered = append(filtered, fn)
		}
	}
	return filtered
}

func syntheticModuleLocation(mod *ast.Module) source.Location {
	if mod != nil {
		for _, decl := range mod.Decls {
			if decl != nil {
				return decl.Loc()
			}
		}
	}
	pos := source.NewPosition()
	return source.NewLocation(fmt.Sprintf("%s", mod.FilePath), pos, pos)
}

func namedType(loc source.Location, name string) ast.TypeExpr {
	return &ast.NamedType{Path: []string{name}, Location: loc}
}

func identExpr(loc source.Location, name string) *ast.Ident {
	return &ast.Ident{Path: []string{name}, Location: loc}
}

func intLit(loc source.Location, value int64) *ast.NumberLit {
	return &ast.NumberLit{Value: fmt.Sprintf("%d", value), Location: loc}
}

func callExpr(loc source.Location, name string, args ...ast.Expr) *ast.CallExpr {
	return &ast.CallExpr{
		Callee:   identExpr(loc, name),
		Args:     args,
		Location: loc,
	}
}

func binaryExpr(loc source.Location, left ast.Expr, op string, right ast.Expr) *ast.BinaryExpr {
	return &ast.BinaryExpr{Left: left, Op: op, Right: right, Location: loc}
}

func exprStmt(expr ast.Expr) *ast.ExprStmt {
	return &ast.ExprStmt{Value: expr, Location: expr.Loc()}
}
