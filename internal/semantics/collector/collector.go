package collector

import (
	"compiler/colors"
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
	"compiler/internal/types"
	"compiler/internal/utils"
	"compiler/internal/utils/fs"
	"compiler/internal/utils/numeric"
	"fmt"
	"strings"
)

// checkModuleLevelRestriction checks if a node is not allowed at module level and reports an error if so
func checkModuleLevelRestriction(ctx *context_v2.CompilerContext, mod *context_v2.Module, node ast.Node) {
	// Only check if we're at module scope
	if mod.CurrentScope != mod.ModuleScope {
		return
	}

	// Check if this node type is not allowed at module level
	switch node.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.WhileStmt, *ast.MatchStmt,
		*ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		ctx.Diagnostics.Add(
			diagnostics.NewError("this statement is not allowed at module level").
				WithPrimaryLabel(node.Loc(), "remove this statement"),
		)
	}
}

// CollectModule builds the symbol table for a module by traversing its AST.
// It orchestrates all collection phases internally:
//   - Phase 1: Collect declarations (types, functions, methods, variables, constants) - just names/signatures
//   - Phase 2: Collect function bodies (after all declarations are known)
//   - Phase 3: Collect method bodies (after all types are known)
//
// No type checking is performed - all symbols get TYPE_UNKNOWN initially.
func CollectModule(ctx *context_v2.CompilerContext, mod *context_v2.Module) {
	// Phase 1: Collect declarations (signatures only)
	collectDeclarations(ctx, mod)

	// Phase 2: Collect function bodies (after all declarations are known)
	collectFunctionBodies(ctx, mod)

	// Phase 3: Collect method bodies (after all types are known)
	collectMethodBodies(ctx, mod)
}

// collectDeclarations collects only module-level declarations (types, top-level functions, methods, variables, constants)
// without their bodies to eliminate ordering issues.
// Methods are bound to types so their order doesn't matter - we just collect their names/signatures.
func collectDeclarations(ctx *context_v2.CompilerContext, mod *context_v2.Module) {
	// Initialize imports list if not exist
	if mod.Imports == nil {
		mod.Imports = make(map[string]*context_v2.Import)
	}

	// Initialize import alias map
	if mod.ImportAliasMap == nil {
		mod.ImportAliasMap = make(map[string]string)
	}

	// Ensure CurrentScope starts at ModuleScope
	mod.CurrentScope = mod.ModuleScope

	// Collect only module-level declarations (types, top-level functions, methods, variables, constants)
	// Methods are bound to types so their order doesn't matter - we just collect their names/signatures
	// Function and method bodies are collected in later phases
	if mod.AST != nil {
		for _, node := range mod.AST.Nodes {
			// Collect only the declaration, not the body
			collectDeclarationOnly(ctx, mod, node)
		}
	}

	// Reset CurrentScope to ModuleScope after collection
	mod.CurrentScope = mod.ModuleScope
}

// collectFunctionBodies collects function bodies in a second pass after all declarations are known.
// This ensures all symbols are available before processing function bodies.
// It also processes top-level statements to check for invalid control flow at module level.
func collectFunctionBodies(ctx *context_v2.CompilerContext, mod *context_v2.Module) {
	if mod.AST == nil {
		return
	}

	// Ensure CurrentScope starts at ModuleScope
	mod.CurrentScope = mod.ModuleScope

	// Collect function bodies and process top-level statements
	for _, node := range mod.AST.Nodes {
		switch n := node.(type) {
		case *ast.FuncDecl:
			collectFunctionBody(ctx, mod, n)
		case *ast.MethodDecl:
			// Methods are collected separately in collectMethodBodies
			continue
		case *ast.VarDecl, *ast.ConstDecl, *ast.TypeDecl, *ast.ImportStmt:
			// These are declarations, already processed in collectDeclarations
			// Skip them here
		default:
			// Process top-level statements (if, for, while, return, break, continue, etc.)
			// This will trigger the guards that check for module-level control flow
			collectNode(ctx, mod, node)
		}
	}

	// Reset CurrentScope
	mod.CurrentScope = mod.ModuleScope
}

// collectMethodBodies collects method bodies in a third pass after all types are known.
// Method signatures are already collected in the first pass.
func collectMethodBodies(ctx *context_v2.CompilerContext, mod *context_v2.Module) {
	if mod.AST == nil {
		return
	}

	// Ensure CurrentScope is at ModuleScope
	mod.CurrentScope = mod.ModuleScope

	// Collect method bodies (signatures already collected in first pass)
	for _, node := range mod.AST.Nodes {
		if methodDecl, ok := node.(*ast.MethodDecl); ok {
			collectMethodBody(ctx, mod, methodDecl)
		}
	}

	// Reset CurrentScope
	mod.CurrentScope = mod.ModuleScope
}

// collectDeclarationOnly collects only the declaration symbol, not the body.
// This is used in the first pass to eliminate ordering issues.
func collectDeclarationOnly(ctx *context_v2.CompilerContext, mod *context_v2.Module, node ast.Node) {
	switch n := node.(type) {
	case *ast.VarDecl:
		collectVarDecl(ctx, mod, n, symbols.SymbolVariable)
	case *ast.ConstDecl:
		collectVarDecl(ctx, mod, n, symbols.SymbolConstant)
	case *ast.FuncDecl:
		collectFuncDeclSignature(ctx, mod, n)
	case *ast.MethodDecl:
		collectMethodDeclSignature(ctx, mod, n)
	case *ast.TypeDecl:
		collectTypeDecl(ctx, mod, n)
	case *ast.ImportStmt:
		collectImport(ctx, mod, n)
	case *ast.DeclStmt:
		collectDeclarationOnly(ctx, mod, n.Decl)
	// Skip statements and expressions in first pass
	default:
		// Skip non-declaration nodes
	}
}

// collectNode processes a single AST node and declares symbols
// This is used when collecting function bodies (second pass).
// Function and method declarations must be at module level; anonymous functions are handled in expressions.
func collectNode(ctx *context_v2.CompilerContext, mod *context_v2.Module, node ast.Node) {
	// Check if this statement is not allowed at module level
	checkModuleLevelRestriction(ctx, mod, node)

	switch n := node.(type) {
	case *ast.VarDecl:
		collectVarDecl(ctx, mod, n, symbols.SymbolVariable)
	case *ast.ConstDecl:
		collectVarDecl(ctx, mod, n, symbols.SymbolConstant)
	case *ast.FuncDecl:
		if mod.CurrentScope != mod.ModuleScope {
			ctx.Diagnostics.Add(
				diagnostics.NewError("function declarations are only allowed at top level").
					WithCode(diagnostics.ErrInvalidDeclaration).
					WithPrimaryLabel(n.Loc(), "move this declaration to module scope").
					WithHelp("nested scopes only allow anonymous functions, e.g. `let f := fn(...) { ... };`"),
			)
		}
		collectFuncDeclSignature(ctx, mod, n)
	case *ast.MethodDecl:
		if mod.CurrentScope != mod.ModuleScope {
			ctx.Diagnostics.Add(
				diagnostics.NewError("method declarations are only allowed at top level").
					WithCode(diagnostics.ErrInvalidDeclaration).
					WithPrimaryLabel(n.Loc(), "move this declaration to module scope"),
			)
		}
		collectMethodDeclSignature(ctx, mod, n)
	case *ast.TypeDecl:
		collectTypeDecl(ctx, mod, n)
	case *ast.ImportStmt:
		collectImport(ctx, mod, n)
	case *ast.DeclStmt:
		collectNode(ctx, mod, n.Decl)
	case *ast.Block:
		collectBlock(ctx, mod, n)
	case *ast.IfStmt:
		collectIfStmt(ctx, mod, n)
	case *ast.ForStmt:
		collectForStmt(ctx, mod, n)
	case *ast.WhileStmt:
		collectWhileStmt(ctx, mod, n)
	case *ast.MatchStmt:
		collectMatchStmt(ctx, mod, n)
	case *ast.ReturnStmt:
		// Collect function literals in return expressions
		if n.Result != nil {
			collectExpr(ctx, mod, n.Result)
		}
	case *ast.BreakStmt:
		// Break statements don't declare anything
	case *ast.ContinueStmt:
		// Continue statements don't declare anything
	case *ast.AssignStmt:
		// Collect function literals in assignment expressions
		collectExpr(ctx, mod, n.Lhs)
		collectExpr(ctx, mod, n.Rhs)
	case *ast.ExprStmt:
		// Collect function literals in expression statements
		collectExpr(ctx, mod, n.X)
	// Other statements don't declare module-level symbols
	default:
		// Skip non-declaration nodes
	}
}

func collectBlock(ctx *context_v2.CompilerContext, mod *context_v2.Module, block *ast.Block) {
	if ctx.Config.Debug {
		colors.BROWN.Println("Entering new scope")
	}

	// Create new block scope with current scope as parent
	blockScope := table.NewSymbolTable(mod.CurrentScope)
	block.Scope = blockScope

	// Enter new block scope and ensure it's restored on exit
	defer mod.EnterScope(blockScope)()

	if ctx.Config.Debug {
		defer colors.BROWN.Println("Exiting new scope...")
	}

	for _, n := range block.Nodes {
		collectNode(ctx, mod, n)
	}
}

// collectVarDecl handles variable and constant declarations
func collectVarDecl(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl any, kind symbols.SymbolKind) {
	var declItems []ast.DeclItem

	switch d := decl.(type) {
	case *ast.VarDecl:
		declItems = d.Decls
	case *ast.ConstDecl:
		declItems = d.Decls
	default:
		return
	}

	for _, item := range declItems {
		name := item.Name.Name

		// Skip placeholder '_' - it's not a real symbol
		if name == "_" {
			// Still collect function literals in initializer if present
			if item.Value != nil {
				collectExpr(ctx, mod, item.Value)
			}
			continue
		}

		// Check for conflict with import alias
		if importPath, isImport := mod.ImportAliasMap[name]; isImport {
			imp := mod.Imports[importPath]
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("'%s' is already used as an import alias", name)).
					WithCode(diagnostics.ErrRedeclaredSymbol).
					WithPrimaryLabel(&item.Name.Location, fmt.Sprintf("cannot declare '%s' here", name)).
					WithSecondaryLabel(imp.Location, fmt.Sprintf("'%s' is the alias for this import", name)).
					WithHelp(fmt.Sprintf("rename this %s or use a different import alias", func() string {
						if kind == symbols.SymbolConstant {
							return "constant"
						}
						return "variable"
					}())),
			)
			continue
		}

		// Check for duplicate declaration
		if existing, ok := mod.CurrentScope.GetSymbol(name); ok {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("redeclaration of '%s'", name)).
					WithCode(diagnostics.ErrRedeclaredSymbol).
					WithPrimaryLabel(&item.Name.Location, fmt.Sprintf("'%s' already declared", name)).
					WithSecondaryLabel(existing.Decl.Loc(), "previous declaration here"),
			)
			continue
		}

		// Create symbol with TYPE_UNKNOWN (will be filled during type checking)
		sym := &symbols.Symbol{
			Name:     name,
			Kind:     kind,
			Type:     types.TypeUnknown,
			Exported: utils.IsExported(name),
			Decl:     item.Name,
		}

		mod.CurrentScope.Declare(name, sym)

		// Collect any function literals in the initializer expression
		if item.Value != nil {
			collectExpr(ctx, mod, item.Value)
		}
	}
}

// collectFuncDeclSignature handles function declaration signature only (first pass).
// It declares the function symbol and creates the function scope with parameters,
// but does NOT collect the function body.
func collectFuncDeclSignature(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.FuncDecl) {
	if decl.Name == nil {
		return
	}

	name := decl.Name.Name

	// Check for conflict with import alias
	if importPath, isImport := mod.ImportAliasMap[name]; isImport {
		imp := mod.Imports[importPath]
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("'%s' is already used as an import alias", name)).
				WithCode(diagnostics.ErrRedeclaredSymbol).
				WithPrimaryLabel(decl.Loc(), fmt.Sprintf("cannot declare function '%s' here", name)).
				WithSecondaryLabel(imp.Location, fmt.Sprintf("'%s' is the alias for this import", name)).
				WithHelp("rename this function or use a different import alias"),
		)
		return
	}

	// Check for duplicate declaration
	if existing, ok := mod.CurrentScope.GetSymbol(name); ok {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("redeclaration of '%s'", name)).
				WithCode(diagnostics.ErrRedeclaredSymbol).
				WithPrimaryLabel(decl.Loc(), fmt.Sprintf("'%s' already declared", name)).
				WithSecondaryLabel(existing.Decl.Loc(), "previous declaration here"),
		)
		return
	}

	// Create symbol (type will be filled during type checking)
	sym := &symbols.Symbol{
		Name:     name,
		Kind:     symbols.SymbolFunction,
		Type:     types.NewFunction([]types.ParamType{}, types.TypeVoid), // Placeholder, will be filled during type checking
		Exported: utils.IsExported(name),
		Decl:     decl,
	}
	extern := hasExternTag(decl.Doc)
	if decl.Body == nil {
		if !extern {
			ctx.Diagnostics.Add(
				diagnostics.NewError("function declarations require a body").
					WithCode(diagnostics.ErrInvalidDeclaration).
					WithPrimaryLabel(decl.Loc(), "add a body or mark as @extern"),
			)
		}
	}
	if extern {
		if mod.Type != context_v2.ModuleBuiltin {
			ctx.Diagnostics.Add(
				diagnostics.NewError("@extern is only allowed in standard library modules").
					WithCode(diagnostics.ErrInvalidDeclaration).
					WithPrimaryLabel(decl.Loc(), "remove @extern"),
			)
		} else if decl.Body != nil {
			ctx.Diagnostics.Add(
				diagnostics.NewError("@extern functions cannot have a body").
					WithCode(diagnostics.ErrInvalidDeclaration).
					WithPrimaryLabel(decl.Body.Loc(), "remove the body"),
			)
		} else {
			if mod.ImportPath == context_v2.GlobalModuleImport && isIntrinsicBuiltin(name) {
				sym.IsBuiltin = true
				sym.BuiltinName = name
			} else {
				sym.IsNative = true
				sym.NativeName = externNativeName(mod.ImportPath, name)
			}
		}
	}

	mod.CurrentScope.Declare(name, sym)

	// Create function scope and declare parameters, but do NOT collect body
	collectFunctionSignatureOnly(ctx, mod, decl.Type, &decl.Scope, nil)
}

// collectFunctionBody collects the function body in the second pass.
func collectFunctionBody(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.FuncDecl) {
	if decl.Scope == nil {
		// Scope should have been created in first pass
		return
	}

	funcScope := decl.Scope.(*table.SymbolTable)
	defer mod.EnterScope(funcScope)()

	// Collect function body
	// The function body block should use the function scope (not create a new one)
	if decl.Body != nil {
		// Set the body block's scope to the function scope
		decl.Body.Scope = funcScope

		// Collect nodes in the body (this will create scopes for nested blocks)
		for _, n := range decl.Body.Nodes {
			collectNode(ctx, mod, n)
		}
	}
}

func hasExternTag(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	return strings.Contains(doc.Text, "@extern")
}

func isIntrinsicBuiltin(name string) bool {
	switch name {
	case "len", "append":
		return true
	default:
		return false
	}
}

func externNativeName(importPath, name string) string {
	// Convert import path to C-safe name: std/io -> std_io
	modName := strings.ReplaceAll(importPath, "/", "_")
	return fmt.Sprintf("ferret_%s_%s", modName, name)
}

// extractReceiverTypeName extracts the type name and optional module from a receiver's TypeNode.
// Returns the type name, module alias (empty for local types), and whether the receiver is valid.
// For anonymous structs, it generates and assigns a unique ID and reports an error.
func extractReceiverTypeName(ctx *context_v2.CompilerContext, receiverType ast.TypeNode, receiverLoc *ast.Field) (typeName string, moduleAlias string, isValid bool) {
	if receiverType == nil {
		ctx.Diagnostics.Add(
			diagnostics.NewError("method receiver must have a type").
				WithCode(diagnostics.ErrInvalidMethodReceiver).
				WithPrimaryLabel(receiverLoc.Loc(), "receiver without type"),
		)
		return utils.GenerateStructLitID(), "", false
	}

	// Check for anonymous struct type (struct { ... })
	if structType, ok := receiverType.(*ast.StructType); ok {
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot define methods on anonymous struct types").
				WithCode(diagnostics.ErrInvalidMethodReceiver).
				WithPrimaryLabel(structType.Loc(), "anonymous struct type").
				WithHelp("define a named type first: type MyType struct { ... }").
				WithNote("methods can only be defined on named types"),
		)

		return structType.ID, "", false
	}

	// check for anonymous interface, similar to anonymous structs. interfae { m1()}
	if interfaceType, ok := receiverType.(*ast.InterfaceType); ok {
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot define methods on anonymous interface type").
				WithCode(diagnostics.ErrInvalidMethodReceiver).
				WithPrimaryLabel(interfaceType.Loc(), "anonymous interface type").
				WithHelp("define a named type first: type Mytype interface { ... }").
				WithNote("methods can only be defined on named types"),
		)

	}

	// Handle direct identifier (most common case: fn (p: Point) ...)
	if ident, ok := receiverType.(*ast.IdentifierExpr); ok {
		return ident.Name, "", true
	}

	// Handle scope resolution (cross-module types: fn (p: utils::Point) ...)
	if scopeRes, ok := receiverType.(*ast.ScopeResolutionExpr); ok {
		// Return both module and type name separately
		if moduleIdent, ok := scopeRes.X.(*ast.IdentifierExpr); ok {
			return scopeRes.Selector.Name, moduleIdent.Name, true
		}
	}

	// Handle wrapped reference type (less common: fn (p: &Point) ...)
	// Note: Currently the parser doesn't generate ReferenceType for receivers,
	// but we handle it for future compatibility
	if refType, ok := receiverType.(*ast.ReferenceType); ok {
		if ident, ok := refType.Base.(*ast.IdentifierExpr); ok {
			return ident.Name, "", true
		}
	}

	// Other type nodes are not supported as receiver types
	return "<unsupported type>", "", false
}

// collectMethodDeclSignature handles method declaration signature only (first pass).
// It validates the receiver type and creates the method scope with parameters,
// but does NOT collect the method body.
// Methods are bound to types so their order doesn't matter - we just collect their names.
func collectMethodDeclSignature(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.MethodDecl) {
	if decl.Name == nil || decl.Receiver == nil {
		return
	}

	methodName := decl.Name.Name
	receiverType := decl.Receiver.Type

	extern := hasExternTag(decl.Doc)
	if decl.Body == nil {
		if !extern {
			ctx.Diagnostics.Add(
				diagnostics.NewError("method declarations require a body").
					WithCode(diagnostics.ErrInvalidDeclaration).
					WithPrimaryLabel(decl.Loc(), "add a body or mark as @extern"),
			)
		}
	}
	if extern {
		if mod.Type != context_v2.ModuleBuiltin {
			ctx.Diagnostics.Add(
				diagnostics.NewError("@extern is only allowed in standard library modules").
					WithCode(diagnostics.ErrInvalidDeclaration).
					WithPrimaryLabel(decl.Loc(), "remove @extern"),
			)
		} else if decl.Body != nil {
			ctx.Diagnostics.Add(
				diagnostics.NewError("@extern methods cannot have a body").
					WithCode(diagnostics.ErrInvalidDeclaration).
					WithPrimaryLabel(decl.Body.Loc(), "remove the body"),
			)
		}
	}

	// Extract receiver type name and module (if cross-module)
	typeName, moduleAlias, isValidReceiver := extractReceiverTypeName(ctx, receiverType, decl.Receiver)

	// Will hold the type symbol for attaching methods
	var typeSym *symbols.Symbol
	var found bool

	// Validate receiver type exists and is user-defined
	if isValidReceiver {
		// Check if it's a cross-module type
		if moduleAlias != "" {
			// DISALLOW cross-module method definitions
			// Report error but continue processing for better diagnostics
			ctx.Diagnostics.Add(
				diagnostics.NewError("cannot define methods on types from other modules").
					WithCode(diagnostics.ErrInvalidMethodReceiver).
					WithPrimaryLabel(decl.Receiver.Loc(), fmt.Sprintf("type '%s::%s' is from another module", moduleAlias, typeName)).
					WithHelp("methods must be defined in the same module as the type").
					WithNote("this restriction ensures type safety and prevents cross-module coupling"),
			)
			isValidReceiver = false

			// Still validate the import exists for better error messages
			importPath, ok := mod.ImportAliasMap[moduleAlias]
			if !ok {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("module '%s' not imported", moduleAlias)).
						WithCode(diagnostics.ErrInvalidMethodReceiver).
						WithPrimaryLabel(decl.Receiver.Loc(), fmt.Sprintf("cannot use type from non-imported module '%s'", moduleAlias)).
						WithHelp(fmt.Sprintf("add: import \"%s\"", moduleAlias)),
				)
			} else {
				// Get the imported module (for potential additional validation)
				importedMod, exists := ctx.GetModule(importPath)
				if !exists {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("imported module '%s' not found", importPath)).
							WithCode(diagnostics.ErrInvalidMethodReceiver).
							WithPrimaryLabel(decl.Receiver.Loc(), "module not loaded"),
					)
				} else {
					// Look up type in the imported module's scope
					typeSym, found = importedMod.ModuleScope.GetSymbol(typeName)
					if !found {
						ctx.Diagnostics.Add(
							diagnostics.NewError(fmt.Sprintf("type '%s' not found in module '%s'", typeName, importPath)).
								WithCode(diagnostics.ErrInvalidMethodReceiver).
								WithPrimaryLabel(decl.Receiver.Loc(), fmt.Sprintf("'%s' not found", typeName)),
						)
					}
				}
			}
		} else {
			// Local type - look in current module scope
			typeSym, found = mod.ModuleScope.Lookup(typeName)
			if !found {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("cannot define method on undefined type '%s'", typeName)).
						WithCode(diagnostics.ErrInvalidMethodReceiver).
						WithPrimaryLabel(decl.Receiver.Loc(), fmt.Sprintf("type '%s' not found", typeName)).
						WithHelp(fmt.Sprintf("declare the type first: type %s struct { ... }", typeName)),
				)
				isValidReceiver = false
			}
		}

		// Validate it's actually a type (not a variable or function)
		if found && typeSym != nil && typeSym.Kind != symbols.SymbolType {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot define method on non-type '%s'", typeName)).
					WithCode(diagnostics.ErrInvalidMethodReceiver).
					WithPrimaryLabel(decl.Receiver.Loc(), fmt.Sprintf("'%s' is not a type", typeName)),
			)
			isValidReceiver = false
		}

		// Check for duplicate method declaration on this type
		if found && typeSym != nil {
			if typeSym.Methods != nil {
				if _, exists := typeSym.Methods[methodName]; exists {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("method '%s' redeclared on type '%s'", methodName, typeName)).
							WithCode(diagnostics.ErrRedeclaredSymbol).
							WithPrimaryLabel(decl.Name.Loc(), "method redeclared here"),
					)
				}
			}
		}
	}

	// Create method scope and declare parameters, but do NOT collect body
	var receiverSym *symbols.Symbol
	if decl.Receiver.Name != nil && decl.Receiver.Name.Name != "_" {
		receiverSym = &symbols.Symbol{
			Name:     decl.Receiver.Name.Name,
			Kind:     symbols.SymbolReceiver,
			Type:     types.TypeUnknown, // Filled during type checking
			Exported: utils.IsExported(decl.Receiver.Name.Name),
			Decl:     decl.Receiver,
		}
	}

	collectFunctionSignatureOnly(ctx, mod, decl.Type, &decl.Scope, receiverSym)
}

// collectMethodBody collects the method body in the second pass.
func collectMethodBody(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.MethodDecl) {
	if decl.Scope == nil {
		// Scope should have been created in first pass
		return
	}

	methodScope := decl.Scope.(*table.SymbolTable)
	defer mod.EnterScope(methodScope)()

	// Collect method body
	// The method body block should use the method scope (not create a new one)
	if decl.Body != nil {
		// Set the body block's scope to the method scope
		decl.Body.Scope = methodScope

		// Collect nodes in the body (this will create scopes for nested blocks)
		for _, n := range decl.Body.Nodes {
			collectNode(ctx, mod, n)
		}
	}
}

// validateParams validates function parameters including variadic rules, duplicates, and type annotations
// This is shared by both named functions and function literals
func validateParams(ctx *context_v2.CompilerContext, _ *context_v2.Module, params []ast.Field) {
	if len(params) == 0 {
		return
	}

	variadicCount := 0
	variadicIndex := -1

	// Check for variadic parameters and their position
	for i, param := range params {
		if param.IsVariadic {
			variadicCount++
			variadicIndex = i
		}
	}

	// Rule: Maximum one variadic parameter
	if variadicCount > 1 {
		for _, param := range params {
			if param.IsVariadic {
				ctx.Diagnostics.Add(
					diagnostics.NewError("multiple variadic parameters").
						WithPrimaryLabel(param.Loc(), "variadic parameter here").
						WithNote("a function can have at most one variadic parameter"),
				)
			}
		}
		return
	}

	// Rule: Variadic parameter must be last
	if variadicCount == 1 && variadicIndex != len(params)-1 {
		ctx.Diagnostics.Add(
			diagnostics.NewError("variadic parameter must be last").
				WithPrimaryLabel(params[variadicIndex].Loc(), "variadic parameter not at end").
				WithHelp(fmt.Sprintf("move this parameter to the %s position (last)", numeric.NumericToOrdinal(len(params)))),
		)
	}

	// Check for duplicate parameter names
	paramNames := make(map[string]*ast.Field)
	for _, param := range params {
		if param.Name != nil {
			name := param.Name.Name
			if name == "_" {
				continue
			}
			if existing, ok := paramNames[name]; ok {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("duplicate parameter name '%s'", name)).
						WithPrimaryLabel(param.Loc(), fmt.Sprintf("'%s' already declared", name)).
						WithSecondaryLabel(existing.Loc(), "previous declaration here"),
				)
			} else {
				paramNames[name] = &param
			}
		}
	}

	// Check for missing parameter types
	for _, param := range params {
		if param.Type == nil {
			paramName := "<unnamed>"
			if param.Name != nil {
				paramName = param.Name.Name
			}
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("parameter '%s' missing type annotation", paramName)).
					WithPrimaryLabel(param.Loc(), "type annotation required").
					WithHelp("add type annotation (e.g., 'name: i32')"),
			)
		}
	}
}

// collectTypeDecl handles type declarations
func collectTypeDecl(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.TypeDecl) {
	name := decl.Name.Name

	// Check for conflict with import alias
	if importPath, isImport := mod.ImportAliasMap[name]; isImport {
		imp := mod.Imports[importPath]
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("'%s' is already used as an import alias", name)).
				WithCode(diagnostics.ErrRedeclaredSymbol).
				WithPrimaryLabel(decl.Loc(), fmt.Sprintf("cannot declare type '%s' here", name)).
				WithSecondaryLabel(imp.Location, fmt.Sprintf("'%s' is the alias for this import", name)).
				WithHelp("rename this type or use a different import alias"),
		)
		return
	}

	// Check for duplicate declaration
	if existing, ok := mod.CurrentScope.GetSymbol(name); ok {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("redeclaration of '%s'", name)).
				WithCode(diagnostics.ErrRedeclaredSymbol).
				WithPrimaryLabel(decl.Loc(), fmt.Sprintf("'%s' already declared", name)).
				WithSecondaryLabel(existing.Decl.Loc(), "previous declaration here"),
		)
		return
	}

	// Create symbol
	// Note: Type symbols no longer need their own scope since methods are stored
	// in module scope with qualified names ("TypeName::methodName")
	sym := &symbols.Symbol{
		Name:     name,
		Kind:     symbols.SymbolType,
		Type:     types.TypeUnknown, // The actual type will be resolved later
		Exported: utils.IsExported(name),
		Decl:     decl,
	}

	mod.CurrentScope.Declare(name, sym)

	// Special handling for enums: register variants as placeholders
	// The actual values will be computed during type checking
	if enumType, ok := decl.Type.(*ast.EnumType); ok {
		for _, variant := range enumType.Variants {
			variantName := variant.Name.Name
			qualifiedName := name + "::" + variantName

			// Check for duplicate variant name
			if existing, ok := mod.ModuleScope.GetSymbol(qualifiedName); ok {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("duplicate enum variant '%s'", variantName)).
						WithCode(diagnostics.ErrRedeclaredSymbol).
						WithPrimaryLabel(variant.Name.Loc(), "variant already defined").
						WithSecondaryLabel(existing.Decl.Loc(), "previous definition here"),
				)
				continue
			}

			// Register variant as a placeholder symbol
			// Type will be filled in during type checking
			// Variants inherit the exported status from their enum type
			variantSym := &symbols.Symbol{
				Name:     qualifiedName,
				Kind:     symbols.SymbolConstant,
				Type:     types.TypeUnknown, // Will be set to the enum type during type checking
				Exported: utils.IsExported(name),
				Decl:     variant.Name,
			}
			mod.ModuleScope.Declare(qualifiedName, variantSym)
		}
	}
}

// collectImport handles import statements
func collectImport(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *ast.ImportStmt) {
	// BasicLit has a Value field containing the string value
	path := stmt.Path.Value
	alias := ""
	if stmt.Alias != nil && stmt.Alias.Name != "" {
		alias = stmt.Alias.Name
	} else {
		alias = fs.LastPart(path)
	}

	// Get or create import entry
	imp, exists := mod.Imports[path]
	if !exists {
		imp = &context_v2.Import{
			Path:     path,
			Alias:    alias,
			Location: stmt.Loc(),
		}
		mod.Imports[path] = imp
	}

	if oldImpPath, exists := mod.ImportAliasMap[alias]; exists {
		oldImp := mod.Imports[oldImpPath]
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("duplicate import alias '%s'", alias)).
				WithCode(diagnostics.ErrRedeclaredSymbol).
				WithPrimaryLabel(imp.Location, fmt.Sprintf("'%s' already used for another import", alias)).
				WithSecondaryLabel(oldImp.Location, "previous import here").
				WithHelp(fmt.Sprintf("use a different alias: import \"%s\" as %s_alt", imp.Path, alias)),
		)
	} else {
		mod.ImportAliasMap[alias] = imp.Path
	}

	// Append alias and location
	imp.Alias = alias
	imp.Location = stmt.Loc()
}

// collectIfStmt handles if statements and their scopes
func collectIfStmt(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *ast.IfStmt) {
	// Create scope for if statement
	ifScope := table.NewSymbolTable(mod.CurrentScope)
	stmt.Scope = ifScope

	// Enter if scope for the body
	{
		defer mod.EnterScope(ifScope)()
		// Collect symbols in the if body
		if stmt.Body != nil {
			collectNode(ctx, mod, stmt.Body)
		}
	}

	// Process else branch (which may be another IfStmt or Block)
	// Note: else branch uses the parent scope, not the if scope
	if stmt.Else != nil {
		collectNode(ctx, mod, stmt.Else)
	}
}

// collectForStmt handles for loops and their scopes
func collectForStmt(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *ast.ForStmt) {
	// Create scope for for loop
	forScope := table.NewSymbolTable(mod.CurrentScope)
	stmt.Scope = forScope

	// Enter for scope and ensure it's restored on exit
	defer mod.EnterScope(forScope)()

	// Collect iterator (VarDecl or IdentifierExpr)
	if stmt.Iterator != nil {
		if varDecl, ok := stmt.Iterator.(*ast.VarDecl); ok {
			checkForIteratorShadowing(ctx, mod, varDecl)
		}
		collectNode(ctx, mod, stmt.Iterator)
		markForIteratorIndexReadOnly(mod, stmt.Iterator)
	}

	// Collect symbols in the for body
	if stmt.Body != nil {
		collectNode(ctx, mod, stmt.Body)
	}
}

func checkForIteratorShadowing(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.VarDecl) {
	if decl == nil || mod.CurrentScope == nil {
		return
	}

	parent := mod.CurrentScope.Parent()
	if parent == nil {
		return
	}

	for _, item := range decl.Decls {
		if item.Name == nil || item.Name.Name == "_" {
			continue
		}

		if existing, ok := parent.Lookup(item.Name.Name); ok && isValueSymbol(existing.Kind) {
			diag := diagnostics.NewWarning(fmt.Sprintf("loop variable '%s' shadows an outer declaration", item.Name.Name)).
				WithCode(diagnostics.ErrRedeclaredSymbol).
				WithPrimaryLabel(&item.Name.Location, fmt.Sprintf("'%s' already declared in an outer scope", item.Name.Name)).
				WithHelp("use a different loop variable name")
			if existing.Decl != nil {
				diag = diag.WithSecondaryLabel(existing.Decl.Loc(), "previous declaration here")
			}
			ctx.Diagnostics.Add(diag)
		}
	}
}

func isValueSymbol(kind symbols.SymbolKind) bool {
	switch kind {
	case symbols.SymbolVariable, symbols.SymbolConstant, symbols.SymbolParameter, symbols.SymbolReceiver:
		return true
	default:
		return false
	}
}

func markForIteratorIndexReadOnly(mod *context_v2.Module, iterator ast.Node) {
	varDecl, ok := iterator.(*ast.VarDecl)
	if !ok || varDecl == nil || len(varDecl.Decls) < 2 {
		return
	}

	first := varDecl.Decls[0]
	if first.Name == nil || first.Name.Name == "_" {
		return
	}

	if sym, ok := mod.CurrentScope.GetSymbol(first.Name.Name); ok {
		sym.IsReadonly = true
	}
}

// collectWhileStmt handles while loops and their scopes
func collectWhileStmt(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *ast.WhileStmt) {
	// Create scope for while loop
	whileScope := table.NewSymbolTable(mod.CurrentScope)
	stmt.Scope = whileScope

	// Enter while scope and ensure it's restored on exit
	defer mod.EnterScope(whileScope)()

	// Collect symbols in the while body
	if stmt.Body != nil {
		collectNode(ctx, mod, stmt.Body)
	}
}

// collectMatchStmt handles match statements and their case scopes
func collectMatchStmt(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *ast.MatchStmt) {
	// Collect the match expression (may contain function literals)
	if stmt.Expr != nil {
		collectExpr(ctx, mod, stmt.Expr)
	}

	// Collect each case clause
	for _, caseClause := range stmt.Cases {
		// Create scope for each case body
		caseScope := table.NewSymbolTable(mod.CurrentScope)
		caseClause.Body.Scope = caseScope

		// Enter case scope and ensure it's restored on exit
		defer mod.EnterScope(caseScope)()

		// Collect symbols in the case body
		if caseClause.Body != nil {
			collectNode(ctx, mod, caseClause.Body)
		}
	}
}

// collectExpr recursively walks expressions to find and collect function literals
func collectExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *ast.FuncLit:
		collectFuncLit(ctx, mod, e)

	case *ast.BinaryExpr:
		collectExpr(ctx, mod, e.X)
		collectExpr(ctx, mod, e.Y)

	case *ast.UnaryExpr:
		collectExpr(ctx, mod, e.X)

	case *ast.CallExpr:
		collectExpr(ctx, mod, e.Fun)
		for _, arg := range e.Args {
			collectExpr(ctx, mod, arg)
		}
		// Handle catch clause: create handler block scope, declare error identifier, then collect block
		if e.Catch != nil && e.Catch.Handler != nil {
			// Create handler block scope (same as collectBlock does)
			handlerScope := table.NewSymbolTable(mod.CurrentScope)
			e.Catch.Handler.Scope = handlerScope

			defer mod.EnterScope(handlerScope)()

			// If error identifier is provided, declare it in the handler scope BEFORE collecting block nodes
			if e.Catch.ErrIdent != nil {
				// catch identifier is new on handler scope, so parent variable wont used if same name
				mod.CurrentScope.Declare(e.Catch.ErrIdent.Name, &symbols.Symbol{
					Name:       e.Catch.ErrIdent.Name,
					Kind:       symbols.SymbolVariable,
					Type:       types.TypeUnknown,
					Decl:       e.Catch.ErrIdent,
					Exported:   false, // catch identifier is not exported
					IsReadonly: true,
				})
			}

			// Collect handler block nodes
			for _, n := range e.Catch.Handler.Nodes {
				collectNode(ctx, mod, n)
			}
		}
		// Collect fallback expression if present (in handler scope if one exists)
		if e.Catch != nil && e.Catch.Fallback != nil {
			collectExpr(ctx, mod, e.Catch.Fallback)
		}

	case *ast.IndexExpr:
		collectExpr(ctx, mod, e.X)
		collectExpr(ctx, mod, e.Index)

	case *ast.SelectorExpr:
		collectExpr(ctx, mod, e.X)

	case *ast.ScopeResolutionExpr:
		collectExpr(ctx, mod, e.X)

	case *ast.CompositeLit:
		for _, elem := range e.Elts {
			if kv, ok := elem.(*ast.KeyValueExpr); ok {
				if kv.Value != nil {
					collectExpr(ctx, mod, kv.Value)
				}
			} else {
				collectExpr(ctx, mod, elem)
			}
		}

	case *ast.CastExpr:
		collectExpr(ctx, mod, e.X)

	case *ast.CoalescingExpr:
		collectExpr(ctx, mod, e.Cond)
		collectExpr(ctx, mod, e.Default)

	// Literals and identifiers don't need collection
	default:
		// Skip
	}
}

// collectFuncLit handles function literal collection
// Function literals are expressions that create closures with their own scope
func collectFuncLit(ctx *context_v2.CompilerContext, mod *context_v2.Module, lit *ast.FuncLit) {
	if ctx.Config.Debug {
		colors.BROWN.Println("Entering function literal scope")
	}

	// Collect function scope and body using shared helper
	collectFunctionScope(ctx, mod, lit.Type, lit.Body, &lit.Scope, nil)

	if ctx.Config.Debug {
		colors.BROWN.Println("Exiting function literal scope")
	}
}

// collectFunctionSignatureOnly creates a new scope for a function and declares parameters,
// but does NOT collect the function body. Used in first pass for top-level functions.
func collectFunctionSignatureOnly(ctx *context_v2.CompilerContext, mod *context_v2.Module, funcType *ast.FuncType, scopePtr *ast.SymbolTable, receiverSym *symbols.Symbol) {
	// Create new scope with current scope as parent
	newScope := table.NewSymbolTable(mod.CurrentScope)

	// Assign the scope to the pointer so the caller can access it
	*scopePtr = newScope

	// Enter function scope and ensure it's restored on exit
	defer mod.EnterScope(newScope)()

	if receiverSym != nil {
		// just like parameters, declare the receiver in the scope
		mod.CurrentScope.Declare(receiverSym.Name, receiverSym)
	}

	// Validate and declare parameters
	if funcType != nil && funcType.Params != nil {
		validateParams(ctx, mod, funcType.Params)
		// Declare parameters in function scope
		for _, param := range funcType.Params {
			if param.Name == nil || param.Name.Name == "_" {
				continue
			}
			psym := &symbols.Symbol{
				Name:     param.Name.Name,
				Kind:     symbols.SymbolParameter,
				Decl:     &param,
				Exported: false,
				Type:     types.TypeUnknown, // Will be filled during type checking
			}

			err := mod.CurrentScope.Declare(param.Name.Name, psym)
			if err != nil {
				// if parameter name conflicts with receiver name, return an error
				if receiverSym != nil && param.Name.Name == receiverSym.Name {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("receiver name '%s' conflicts with parameter name", receiverSym.Name)).
							WithPrimaryLabel(receiverSym.Decl.Loc(), "receiver declared here"),
					)
					continue
				} else {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("duplicate parameter '%s'", param.Name.Name)).
							WithPrimaryLabel(param.Loc(), "parameter already declared"),
					)
				}
			}
		}
	}
}

// collectFunctionScope creates a new scope for a function (named or literal),
// validates and declares parameters, and collects the function body.
// This is used for function literals and method bodies (which are collected in their respective phases).
func collectFunctionScope(ctx *context_v2.CompilerContext, mod *context_v2.Module, funcType *ast.FuncType, body *ast.Block, scopePtr *ast.SymbolTable, receiverSym *symbols.Symbol) {
	// Create new scope with current scope as parent
	// For function literals, this enables closure (capturing parent variables)
	newScope := table.NewSymbolTable(mod.CurrentScope)

	// Assign the scope to the pointer so the caller can access it
	*scopePtr = newScope

	// Enter function scope and ensure it's restored on exit
	defer mod.EnterScope(newScope)()

	if receiverSym != nil {
		// just like parameters, declare the receiver in the scope
		mod.CurrentScope.Declare(receiverSym.Name, receiverSym)
	}

	// Validate and declare parameters
	if funcType != nil && funcType.Params != nil {
		validateParams(ctx, mod, funcType.Params)
		// Declare parameters in function scope
		for _, param := range funcType.Params {
			if param.Name == nil || param.Name.Name == "_" {
				continue
			}
			psym := &symbols.Symbol{
				Name:     param.Name.Name,
				Kind:     symbols.SymbolParameter,
				Decl:     &param,
				Exported: false,
				Type:     types.TypeUnknown, // Will be filled during type checking
			}

			err := mod.CurrentScope.Declare(param.Name.Name, psym)
			if err != nil {
				// if parameter name conflicts with receiver name, return an error
				if receiverSym != nil && param.Name.Name == receiverSym.Name {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("receiver name '%s' conflicts with parameter name", receiverSym.Name)).
							WithPrimaryLabel(receiverSym.Decl.Loc(), "receiver declared here"),
					)
					continue
				} else {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("duplicate parameter '%s'", param.Name.Name)).
							WithPrimaryLabel(param.Loc(), "parameter already declared"),
					)
				}
			}
		}
	}

	// Collect local variables and nested declarations in function body
	// The function body is a block, so we need to ensure it has a scope
	// However, we don't create a new scope for it since we're already in the function scope
	// We just need to set the body's scope to the function scope and collect its nodes
	if body != nil {
		// Set the body block's scope to the function scope (not a new scope)
		// This ensures the block has a scope for later phases
		body.Scope = newScope

		// Collect nodes in the body
		for _, n := range body.Nodes {
			collectNode(ctx, mod, n)
		}
	}
}
