package pipeline

import (
	"fmt"

	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
)

func synthesizeTestHarness(mod *ast.Module, force bool) bool {
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
		Stmts:    make([]ast.Stmt, 0, len(tests)*5+2),
		Location: loc,
	}
	for i, testFn := range tests {
		if testFn == nil || testFn.Name == nil {
			continue
		}
		testName := &ast.StringLit{Value: testFn.TestName, Location: loc}
		beforeName := fmt.Sprintf("__ferret_test_before_%d", i)
		afterName := fmt.Sprintf("__ferret_test_after_%d", i)

		body.Stmts = append(body.Stmts,
			exprStmt(callExpr(loc, "__test_begin", testName)),
			letStmt(loc, beforeName, callExpr(loc, "__test_failure_count")),
			exprStmt(callExpr(loc, testFn.Name.Text())),
			letStmt(loc, afterName, callExpr(loc, "__test_failure_count")),
			&ast.IfStmt{
				Cond:     binaryExpr(loc, identExpr(loc, afterName), "==", identExpr(loc, beforeName)),
				Then:     blockStmt(loc, exprStmt(callExpr(loc, "__test_mark_pass", testName))),
				Else:     blockStmt(loc, exprStmt(callExpr(loc, "__test_mark_fail", testName))),
				Location: loc,
			},
		)
	}
	body.Stmts = append(body.Stmts,
		exprStmt(callExpr(loc, "__test_summary")),
		&ast.ReturnStmt{Value: callExpr(loc, "__test_exit_code"), Location: loc},
	)
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

func letStmt(loc source.Location, name string, value ast.Expr) *ast.LetStmt {
	return &ast.LetStmt{Name: identExpr(loc, name), Value: value, Location: loc}
}

func blockStmt(loc source.Location, stmts ...ast.Stmt) *ast.BlockStmt {
	return &ast.BlockStmt{Stmts: stmts, Location: loc}
}
