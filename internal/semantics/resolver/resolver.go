package resolver

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"

	"fmt"
)

// ResolveModule resolves imports and binds names for a module.
// It normalizes import paths, detects cycles, and connects modules.
func ResolveModule(ctx *context_v2.CompilerContext, mod *context_v2.Module) {

	if mod.AST == nil {
		return
	}

	// Walk the AST and check identifier references
	for _, node := range mod.AST.Nodes {
		resolveNode(ctx, mod, node)
	}

}

// resolveNode recursively binds names in an AST node
func resolveNode(ctx *context_v2.CompilerContext, mod *context_v2.Module, node ast.Node) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.VarDecl:
		// Check initializer expressions
		for _, decl := range n.Decls {
			if decl.Value != nil {
				resolveExpr(ctx, mod, decl.Value)
			}
		}
	case *ast.ConstDecl:
		// Check initializer expressions
		for _, decl := range n.Decls {
			if decl.Value != nil {
				resolveExpr(ctx, mod, decl.Value)
			}
		}

	case *ast.FuncDecl:
		// switch to function scope
		if n.Scope != nil {
			defer mod.EnterScope(n.Scope.(*table.SymbolTable))()
		}

		// Bind names in function body
		if n.Body != nil {
			resolveBlock(ctx, mod, n.Body)
		}

	case *ast.MethodDecl:
		// switch to method scope
		if n.Scope != nil {
			defer mod.EnterScope(n.Scope.(*table.SymbolTable))()
		}

		// Resolve receiver type (if it's a reference type)
		if n.Receiver != nil && n.Receiver.Type != nil {
			resolveTypeNode(ctx, mod, n.Receiver.Type)
		}

		// Bind names in method body
		if n.Body != nil {
			resolveBlock(ctx, mod, n.Body)
		}

	case *ast.AssignStmt:
		resolveExpr(ctx, mod, n.Lhs)
		resolveExpr(ctx, mod, n.Rhs)

	case *ast.ReturnStmt:
		if n.Result != nil {
			resolveExpr(ctx, mod, n.Result)
		}

	case *ast.BreakStmt:
		// Break statements don't need resolution (no identifiers to resolve)
		// Validation (checking if inside loop) is done in control flow analysis

	case *ast.ContinueStmt:
		// Continue statements don't need resolution (no identifiers to resolve)
		// Validation (checking if inside loop) is done in control flow analysis

	case *ast.DeferStmt:
		// Resolve the deferred call expression
		resolveExpr(ctx, mod, n.Call)
		// Resolve the catch clause if present (inline same as CallExpr)
		if n.Catch != nil {
			if n.Catch.Handler != nil {
				// Get the scope of the catch block
				scope := n.Catch.Handler.Scope.(*table.SymbolTable)
				defer mod.EnterScope(scope)()

				// Resolve the catch block
				resolveBlock(ctx, mod, n.Catch.Handler)
				// Resolve the fallback expression in catch handler scope
				if n.Catch.Fallback != nil {
					resolveExpr(ctx, mod, n.Catch.Fallback)
				}
			} else if n.Catch.Fallback != nil {
				resolveExpr(ctx, mod, n.Catch.Fallback)
			}
		}

	case *ast.IfStmt:
		// Enter if scope if it exists
		if n.Scope != nil {
			defer mod.EnterScope(n.Scope.(*table.SymbolTable))()
		}

		resolveExpr(ctx, mod, n.Cond)
		resolveBlock(ctx, mod, n.Body)
		if n.Else != nil {
			resolveNode(ctx, mod, n.Else)
		}

	case *ast.ForStmt:
		// Enter for loop scope if it exists
		if n.Scope != nil {
			defer mod.EnterScope(n.Scope.(*table.SymbolTable))()
		}

		// Resolve iterator (VarDecl or IdentifierExpr)
		if n.Iterator != nil {
			resolveNode(ctx, mod, n.Iterator)
		}
		// Resolve range expression
		if n.Range != nil {
			resolveExpr(ctx, mod, n.Range)
		}
		resolveBlock(ctx, mod, n.Body)

	case *ast.WhileStmt:
		// Enter while loop scope if it exists
		if n.Scope != nil {
			defer mod.EnterScope(n.Scope.(*table.SymbolTable))()
		}

		resolveExpr(ctx, mod, n.Cond)
		resolveBlock(ctx, mod, n.Body)

	case *ast.MatchStmt:
		// Resolve the match expression
		resolveExpr(ctx, mod, n.Expr)

		// Resolve each case clause
		for _, caseClause := range n.Cases {
			// Resolve pattern expression (if not default case)
			if caseClause.Pattern != nil {
				resolveExpr(ctx, mod, caseClause.Pattern)
			}

			// Enter case body scope if it exists
			var restoreScope func()
			if caseClause.Body != nil && caseClause.Body.Scope != nil {
				restoreScope = mod.EnterScope(caseClause.Body.Scope.(*table.SymbolTable))
			}
			// Resolve case body
			resolveBlock(ctx, mod, caseClause.Body)
			// Restore scope if we entered one
			if restoreScope != nil {
				restoreScope()
			}
		}

	case *ast.Block:
		resolveBlock(ctx, mod, n)

	case *ast.DeclStmt:
		resolveNode(ctx, mod, n.Decl)

	case *ast.ExprStmt:
		resolveExpr(ctx, mod, n.X)
	}
}

// resolveBlock binds names in a block
func resolveBlock(ctx *context_v2.CompilerContext, mod *context_v2.Module, block *ast.Block) {
	if block == nil {
		return
	}

	// Enter block scope if it exists
	if block.Scope != nil {
		defer mod.EnterScope(block.Scope.(*table.SymbolTable))()
	}

	for _, node := range block.Nodes {
		resolveNode(ctx, mod, node)
	}
}

// resolveExpr checks identifier references in expressions// resolveExpr checks identifier references in expressions
func resolveExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *ast.IdentifierExpr:
		// Check if the identifier is declared
		name := e.Name
		if name == "_" {
			ctx.Diagnostics.Add(
				diagnostics.NewError("invalid use of '_' identifier").
					WithPrimaryLabel(&e.Location, "blank identifier is only allowed in for loop iterators and function parameters").
					WithHelp("use a real variable name"),
			)
			return
		}
		if _, ok := mod.CurrentScope.Lookup(name); !ok {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("symbol '%s' not found", name)).
					WithPrimaryLabel(&e.Location, "").
					WithNote("Make sure it's imported or defined"),
			)
		}

	case *ast.BinaryExpr:
		resolveExpr(ctx, mod, e.X)
		resolveExpr(ctx, mod, e.Y)

	case *ast.UnaryExpr:
		resolveExpr(ctx, mod, e.X)

	case *ast.CallExpr:
		resolveExpr(ctx, mod, e.Fun)
		for _, arg := range e.Args {
			resolveExpr(ctx, mod, arg)
		}
		if e.Catch != nil {
			if e.Catch.Handler != nil {
				// get the scope of the catch block
				scope := e.Catch.Handler.Scope.(*table.SymbolTable)

				defer mod.EnterScope(scope)()

				// resolve the catch block
				resolveBlock(ctx, mod, e.Catch.Handler)
				// resolve the fallback expression in catch handler scope (defaultVal is also in catch block scope)
				if e.Catch.Fallback != nil {
					resolveExpr(ctx, mod, e.Catch.Fallback)
				}
			} else if e.Catch.Fallback != nil {
				resolveExpr(ctx, mod, e.Catch.Fallback)
			}
		}

	case *ast.IndexExpr:
		resolveExpr(ctx, mod, e.X)
		resolveExpr(ctx, mod, e.Index)

	case *ast.SelectorExpr:
		resolveExpr(ctx, mod, e.X)

	case *ast.ScopeResolutionExpr:
		resolveStaticAccess(ctx, mod, e)

	case *ast.CompositeLit:
		for _, elem := range e.Elts {
			if kv, ok := elem.(*ast.KeyValueExpr); ok {
				if kv.Value != nil {
					resolveExpr(ctx, mod, kv.Value)
				}
			} else {
				resolveExpr(ctx, mod, elem)
			}
		}

	case *ast.CastExpr:
		resolveExpr(ctx, mod, e.X)
		// Resolve the target type as well
		resolveTypeNode(ctx, mod, e.Type)

	case *ast.CoalescingExpr:
		resolveExpr(ctx, mod, e.Cond)
		resolveExpr(ctx, mod, e.Default)

	case *ast.FuncLit:
		// Function literals have their own scope for parameters and body
		if e.Scope != nil {
			defer mod.EnterScope(e.Scope.(*table.SymbolTable))()
		}

		// Resolve function body
		if e.Body != nil {
			resolveNode(ctx, mod, e.Body)
		}

	// Literals don't need binding
	case *ast.BasicLit:
		// No binding needed
	}
}

// resolveStaticAccess handles scope resolution expressions like module::symbol or EnumType::Variant
func resolveStaticAccess(ctx *context_v2.CompilerContext, mod *context_v2.Module, e *ast.ScopeResolutionExpr) {
	// Handle X::Y resolution where X can be:
	// 1. Module alias (module::symbol)
	// 2. Enum type (EnumType::Variant)

	if ident, ok := e.X.(*ast.IdentifierExpr); ok {
		leftName := ident.Name
		rightName := e.Selector.Name

		// First check if leftName is an enum type in current scope
		if sym, ok := mod.CurrentScope.Lookup(leftName); ok && sym.Kind == symbols.SymbolType {
			// It's a type - check if it's an enum by looking up the qualified variant name
			qualifiedVariantName := leftName + "::" + rightName

			if variantSym, ok := mod.ModuleScope.GetSymbol(qualifiedVariantName); ok {
				// Valid enum variant access
				// The variant symbol exists and has been registered during type checking
				_ = variantSym // Validation successful
				return
			} else {
				// Type exists but variant doesn't
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("variant '%s' not found in enum '%s'", rightName, leftName)).
						WithPrimaryLabel(e.Selector.Loc(), fmt.Sprintf("'%s' is not a variant of '%s'", rightName, leftName)),
				)
				return
			}
		}

		// Not an enum type, check if it's a module alias
		importPath, ok := mod.ImportAliasMap[leftName]
		if !ok {
			// Not a module either - report error
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("'%s' is not a module or enum type", leftName)).
					WithPrimaryLabel(ident.Loc(), fmt.Sprintf("'%s' not found", leftName)).
					WithNote("Make sure to import the module or declare the enum type first"),
			)
			return
		}

		// It's a module - proceed with module::symbol resolution
		importedMod, exists := ctx.GetModule(importPath)
		if !exists {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("imported module '%s' not found", importPath)).
					WithPrimaryLabel(ident.Loc(), fmt.Sprintf("module '%s' not loaded", leftName)).
					WithNote("This is likely a compiler bug"),
			)
			return
		}

		// Look up symbol in the imported module's module scope
		sym, ok := importedMod.ModuleScope.GetSymbol(rightName)
		if !ok {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("symbol '%s' not found in module '%s'", rightName, importPath)).
					WithPrimaryLabel(e.Selector.Loc(), fmt.Sprintf("'%s' not found", rightName)),
			)
			return
		}

		// Check if symbol is exported
		if !sym.Exported {

			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("symbol '%s' is not exported from module '%s'", rightName, importPath)).
					WithPrimaryLabel(e.Selector.Loc(), fmt.Sprintf("'%s' is private", rightName)).
					WithSecondaryLabel(sym.Decl.Loc(), "declared here").
					WithNote("only symbols starting with uppercase letters are exported"),
			)
			return
		}

		mod.Imports[importPath].IsUsed = true

	} else {
		// For anonymous types (enum{...}::variant, struct{...}, etc.)
		// Just resolve the expression - validation happens in typechecker
		resolveExpr(ctx, mod, e.X)
	}
}

// resolveTypeNode resolves type references in type nodes
func resolveTypeNode(ctx *context_v2.CompilerContext, mod *context_v2.Module, typeNode ast.TypeNode) {
	if typeNode == nil {
		return
	}

	switch t := typeNode.(type) {
	case *ast.ScopeResolutionExpr:
		// Handle module::Type references
		resolveStaticAccess(ctx, mod, t)

	case *ast.ReferenceType:
		// Resolve the type name
		if ident, ok := t.Base.(*ast.IdentifierExpr); ok {
			// Look up the type in the current scope
			sym, found := mod.CurrentScope.Lookup(ident.Name)
			if !found {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("undefined type '%s'", ident.Name)).
						WithPrimaryLabel(ident.Loc(), fmt.Sprintf("'%s' not declared", ident.Name)),
				)
			} else if sym.Kind != symbols.SymbolType {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("'%s' is not a type", ident.Name)).
						WithPrimaryLabel(ident.Loc(), fmt.Sprintf("'%s' is a %v", ident.Name, sym.Kind)),
				)
			}
		}

	case *ast.ArrayType:
		// Resolve element type
		resolveTypeNode(ctx, mod, t.ElType)

	case *ast.OptionalType:
		// Resolve inner type
		resolveTypeNode(ctx, mod, t.Base)

	case *ast.ResultType:
		// Resolve value and error types
		resolveTypeNode(ctx, mod, t.Value)
		resolveTypeNode(ctx, mod, t.Error)

	case *ast.MapType:
		// Resolve key and value types
		resolveTypeNode(ctx, mod, t.Key)
		resolveTypeNode(ctx, mod, t.Value)

	case *ast.StructType:
		// Resolve field types
		for i := range t.Fields {
			resolveTypeNode(ctx, mod, t.Fields[i].Type)
		}

	case *ast.FuncType:
		// Resolve parameter types
		for i := range t.Params {
			resolveTypeNode(ctx, mod, t.Params[i].Type)
		}
		// Resolve return type
		if t.Result != nil {
			resolveTypeNode(ctx, mod, t.Result)
		}

	// Other type nodes don't need resolution (primitives, etc.)
	default:
		// Nothing to resolve
	}
}
