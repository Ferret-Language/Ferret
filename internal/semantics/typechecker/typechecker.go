package typechecker

import (
	"compiler/internal/utils/numeric"
	str "compiler/internal/utils/strings"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/hir/consteval"
	"compiler/internal/semantics/narrowing"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"compiler/internal/utils"
)

var narrowingAnalyzer = narrowing.NewConditionAnalyzer()

type autoDerefKind int

const (
	autoDerefSelector autoDerefKind = iota
	autoDerefIndex
	autoDerefIncDec
)

var autoDerefKinds = []autoDerefKind{
	autoDerefSelector,
	autoDerefIndex,
}

func AutoDerefAllowed(expr ast.Expression) bool {
	return autoDerefAllowedExpr(expr)
}

func autoDerefAllowedExpr(expr ast.Expression) bool {
	kind, ok := autoDerefExprKind(expr)
	if !ok {
		return false
	}
	for _, allowed := range autoDerefKinds {
		if allowed == kind {
			return true
		}
	}
	return false
}

func autoDerefExprKind(expr ast.Expression) (autoDerefKind, bool) {
	switch expr.(type) {
	case *ast.SelectorExpr:
		return autoDerefSelector, true
	case *ast.IndexExpr:
		return autoDerefIndex, true
	case *ast.PrefixExpr, *ast.PostfixExpr:
		return autoDerefIncDec, true
	default:
		return 0, false
	}
}

func autoDerefBaseType(expr ast.Expression, typ types.SemType) types.SemType {
	// Preserve NamedType for selector expressions so method lookup works.
	// For other expressions (index/inc/dec), keep the old unwrapping behavior.
	if _, ok := expr.(*ast.SelectorExpr); ok {
		if ref, ok := typ.(*types.ReferenceType); ok {
			if autoDerefAllowedExpr(expr) {
				return ref.Inner
			}
		}
		if named, ok := typ.(*types.NamedType); ok {
			if ref, ok := named.Underlying.(*types.ReferenceType); ok && autoDerefAllowedExpr(expr) {
				return ref.Inner
			}
		}
		return typ
	}

	typ = types.UnwrapType(typ)
	if ref, ok := typ.(*types.ReferenceType); ok {
		if autoDerefAllowedExpr(expr) {
			typ = types.UnwrapType(ref.Inner)
		}
	}
	return typ
}

// Helper functions

// addParamsToScope adds function/method parameters to the given scope with their types
func addParamsToScope(ctx *context_v2.CompilerContext, mod *context_v2.Module, scope *table.SymbolTable, params []ast.Field) {
	if params == nil {
		return
	}
	for _, param := range params {
		if param.Name != nil {
			paramType := TypeFromTypeNodeWithContext(ctx, mod, param.Type)

			// Convert variadic parameters (...T) to slice type ([]T)
			// This allows the function body to iterate over the parameter
			if param.IsVariadic {
				paramType = types.NewArray(paramType, -1) // []T
			}

			psym, ok := scope.GetSymbol(param.Name.Name)
			if !ok {
				continue // should not happen but safe side
			}
			psym.Type = paramType
		}
	}
}

// setupFunctionContext sets up function scope and return type tracking.
// Returns a cleanup function that should be deferred.
func setupFunctionContext(ctx *context_v2.CompilerContext, mod *context_v2.Module, scope *table.SymbolTable, funcType *ast.FuncType) func() {
	// Enter function scope and get restore function
	restoreScope := mod.EnterScope(scope)

	// Set expected return type for validation
	var expectedReturnType types.SemType = types.TypeVoid
	if funcType != nil && funcType.Result != nil {
		expectedReturnType = TypeFromTypeNodeWithContext(ctx, mod, funcType.Result)
	}
	oldReturnType := mod.CurrentFunctionReturnType
	mod.CurrentFunctionReturnType = expectedReturnType

	// Reset deferred statements list for this function
	oldDeferredStmts := mod.CurrentDeferredStmts
	mod.CurrentDeferredStmts = nil

	// Return cleanup function that restores scope, return type, and deferred statements
	return func() {
		restoreScope()
		mod.CurrentFunctionReturnType = oldReturnType
		mod.CurrentDeferredStmts = oldDeferredStmts
	}
}

// lookupTypeSymbol finds a type symbol by name in the current module's scope.
// Returns the symbol and true if found, nil and false otherwise.
// Note: Imported types use qualified syntax (module::Type) and are resolved separately.
func lookupTypeSymbol(ctx *context_v2.CompilerContext, mod *context_v2.Module, typeName string) (*symbols.Symbol, bool) {
	// First check current module's scope
	if sym, found := mod.ModuleScope.Lookup(typeName); found {
		return sym, true
	}

	// Not in current module, search imported modules
	for _, importPath := range mod.ImportAliasMap {
		if importedMod, exists := ctx.GetModule(importPath); exists {
			if sym, ok := importedMod.ModuleScope.GetSymbol(typeName); ok && sym.Kind == symbols.SymbolType {
				return sym, true
			}
		}
	}

	return nil, false
}

// TypeCheckTopLevelSignatures resolves types, method signatures, and function signatures.
func TypeCheckTopLevelSignatures(ctx *context_v2.CompilerContext, mod *context_v2.Module) {
	if mod.AST == nil {
		return
	}

	if ctx.Config.Debug {
		fmt.Printf("    [TypeCheckTopLevelSignatures for %s]\n", mod.ImportPath)
	}

	// Reset CurrentScope to ModuleScope
	mod.CurrentScope = mod.ModuleScope

	// CRITICAL: Check type declarations FIRST so receiver types are resolved
	for _, node := range mod.AST.Nodes {
		if typeDecl, ok := node.(*ast.TypeDecl); ok {
			checkTypeDecl(ctx, mod, typeDecl)
		}
	}

	// Now process method declarations - attach them to types
	methodCount := 0
	for _, node := range mod.AST.Nodes {
		if methodDecl, ok := node.(*ast.MethodDecl); ok {
			if ctx.Config.Debug {
				fmt.Printf("      [Found method %s]\n", methodDecl.Name.Name)
			}
			checkMethodSignatureOnly(ctx, mod, methodDecl)
			methodCount++
		}
	}

	// Resolve top-level function signatures
	for _, node := range mod.AST.Nodes {
		if funcDecl, ok := node.(*ast.FuncDecl); ok {
			checkFuncSignatureOnly(ctx, mod, funcDecl)
		}
	}

	if ctx.Config.Debug {
		fmt.Printf("    [Processed %d methods]\n", methodCount)
	}
}

// TypeCheckMethodSignatures is kept for compatibility; it now runs the full top-level signature pass.
func TypeCheckMethodSignatures(ctx *context_v2.CompilerContext, mod *context_v2.Module) {
	TypeCheckTopLevelSignatures(ctx, mod)
}

func checkFuncSignatureOnly(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.FuncDecl) {
	if decl == nil || decl.Name == nil || decl.Type == nil {
		return
	}

	funcType := TypeFromTypeNodeWithContext(ctx, mod, decl.Type)
	if !isFuncLikeType(funcType) {
		return
	}

	if sym, ok := mod.CurrentScope.Lookup(decl.Name.Name); ok {
		sym.Type = funcType
	}
}

func isFuncLikeType(t types.SemType) bool {
	if t == nil {
		return false
	}
	_, ok := types.UnwrapType(t).(*types.FunctionType)
	return ok
}

func checkModuleScopeUseBeforeDecl(ctx *context_v2.CompilerContext, mod *context_v2.Module, ident *ast.IdentifierExpr) {
	if ctx == nil || mod == nil || ident == nil {
		return
	}

	sym, ok := mod.CurrentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return
	}
	if sym.DeclaredScope != mod.ModuleScope {
		return
	}

	if sym.Kind == symbols.SymbolFunction || sym.Kind == symbols.SymbolType {
		return
	}
	if sym.Decl == nil {
		return
	}
	declLoc := sym.Decl.Loc()
	useLoc := ident.Loc()
	if declLoc == nil || useLoc == nil || declLoc.Start == nil || useLoc.Start == nil {
		return
	}
	if declLoc.Filename != nil && useLoc.Filename != nil && *declLoc.Filename != *useLoc.Filename {
		return
	}
	if useLoc.Start.Index >= declLoc.Start.Index {
		return
	}

	ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("'%s' used before declaration", ident.Name)).
			WithCode(diagnostics.ErrUseBeforeDecl).
			WithPrimaryLabel(useLoc, "used before declaration").
			WithSecondaryLabel(declLoc, "declared here"),
	)
}

func referencesIdentOutsideFuncLit(expr ast.Expression, name string) (bool, *source.Location) {
	if expr == nil || name == "" {
		return false, nil
	}

	switch e := expr.(type) {
	case *ast.IdentifierExpr:
		if e.Name == name {
			return true, e.Loc()
		}
	case *ast.BasicLit:
		return false, nil
	case *ast.FuncLit:
		return false, nil
	case *ast.BinaryExpr:
		if found, loc := referencesIdentOutsideFuncLit(e.X, name); found {
			return found, loc
		}
		if found, loc := referencesIdentOutsideFuncLit(e.Y, name); found {
			return found, loc
		}
	case *ast.UnaryExpr:
		return referencesIdentOutsideFuncLit(e.X, name)
	case *ast.SpreadExpr:
		return referencesIdentOutsideFuncLit(e.X, name)
	case *ast.PrefixExpr:
		return referencesIdentOutsideFuncLit(e.X, name)
	case *ast.PostfixExpr:
		return referencesIdentOutsideFuncLit(e.X, name)
	case *ast.CallExpr:
		if found, loc := referencesIdentOutsideFuncLit(e.Fun, name); found {
			return found, loc
		}
		for _, arg := range e.Args {
			if found, loc := referencesIdentOutsideFuncLit(arg, name); found {
				return found, loc
			}
		}
		if e.Catch == nil {
			return false, nil
		}
		if found, loc := referencesIdentOutsideFuncLit(e.Catch.Fallback, name); found {
			return found, loc
		}
	case *ast.IndexExpr:
		if found, loc := referencesIdentOutsideFuncLit(e.X, name); found {
			return found, loc
		}
		if found, loc := referencesIdentOutsideFuncLit(e.Index, name); found {
			return found, loc
		}
	case *ast.SelectorExpr:
		return referencesIdentOutsideFuncLit(e.X, name)
	case *ast.ScopeResolutionExpr:
		return referencesIdentOutsideFuncLit(e.X, name)
	case *ast.CastExpr:
		return referencesIdentOutsideFuncLit(e.X, name)
	case *ast.ParenExpr:
		return referencesIdentOutsideFuncLit(e.X, name)
	case *ast.CompositeLit:
		for _, elem := range e.Elts {
			if kv, ok := elem.(*ast.KeyValueExpr); ok {
				if found, loc := referencesIdentOutsideFuncLit(kv.Value, name); found {
					return found, loc
				}
				continue
			}
			if found, loc := referencesIdentOutsideFuncLit(elem, name); found {
				return found, loc
			}
		}
	case *ast.KeyValueExpr:
		return referencesIdentOutsideFuncLit(e.Value, name)
	case *ast.CoalescingExpr:
		//return referencesIdentOutsideFuncLit(e.Cond, name) || referencesIdentOutsideFuncLit(e.Default, name)
	case *ast.RangeExpr:
		if found, loc := referencesIdentOutsideFuncLit(e.Start, name); found {
			return found, loc
		}
		if found, loc := referencesIdentOutsideFuncLit(e.End, name); found {
			return found, loc
		}
		if found, loc := referencesIdentOutsideFuncLit(e.Incr, name); found {
			return found, loc
		}
	case *ast.ForkExpr:
		return referencesIdentOutsideFuncLit(e.Call, name)
	}
	return false, nil
}

// CheckModule performs type checking on a module.
// Type declarations, method signatures, and top-level function signatures are already checked in phase 4a.
// This phase checks function bodies, variables, and method bodies.
func CheckModule(ctx *context_v2.CompilerContext, mod *context_v2.Module) {

	// Reset CurrentScope to ModuleScope before type checking
	// This ensures we're starting from the module-level scope
	mod.CurrentScope = mod.ModuleScope

	// Check all declarations in the module
	// SKIP type declarations and method signatures - already done in phase 4a
	if mod.AST != nil {
		// Check everything EXCEPT type declarations (already done in phase 4a)
		for _, node := range mod.AST.Nodes {
			if _, ok := node.(*ast.TypeDecl); !ok {
				checkNode(ctx, mod, node)
			}
		}
	}
}

// checkNode type checks a single AST node
func checkNode(ctx *context_v2.CompilerContext, mod *context_v2.Module, node ast.Node) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.VarDecl:
		checkVarDecl(ctx, mod, n, false)
	case *ast.ConstDecl:
		checkVarDecl(ctx, mod, n, true)
	case *ast.FuncDecl:
		checkFuncDecl(ctx, mod, n)
	case *ast.MethodDecl:
		checkMethodDecl(ctx, mod, n)
	case *ast.TypeDecl:
		checkTypeDecl(ctx, mod, n)
	case *ast.AssignStmt:
		checkAssignStmt(ctx, mod, n)
	case *ast.BreakStmt:
		// Break statements don't need type checking
		// Validation (checking if inside loop) is done in control flow analysis phase
	case *ast.ContinueStmt:
		// Continue statements don't need type checking
		// Validation (checking if inside loop) is done in control flow analysis phase
	case *ast.DeferStmt:
		checkDeferStmt(ctx, mod, n)
	case *ast.ReturnStmt:
		// Check return value type against function's declared return type
		if n.Result != nil {
			expectedReturnType := mod.CurrentFunctionReturnType
			if expectedReturnType == nil {
				expectedReturnType = types.TypeUnknown
			}

			// Handle Result type returns: return value! (error) or return value (success)
			if resultType, isResult := expectedReturnType.(*types.ResultType); isResult {
				if n.IsError {
					// Returning error: return "error message"!
					returnedType := checkExpr(ctx, mod, n.Result, resultType.Err)

					if ok := checkFitness(ctx, resultType.Err, n.Result, nil); !ok {
						return
					}

					// Resolve untyped literals
					if types.IsUntyped(returnedType) {
						if lit, ok := n.Result.(*ast.BasicLit); ok {
							returnedType = inferLiteralType(lit, resultType.Err)
						} else {
							returnedType = types.ResolveUntypedType(returnedType, resultType.Err)
						}
					}

					if !returnedType.Equals(types.TypeUnknown) && !resultType.Err.Equals(types.TypeUnknown) {
						compatibility := checkTypeCompatibility(returnedType, resultType.Err)
						if !isImplicitlyCompatible(compatibility) {
							returnedDesc := types.ResolveUntypedType(returnedType, resultType.Err)
							diag := diagnostics.NewError("error return type mismatch").
								WithCode(diagnostics.ErrTypeMismatch).
								WithPrimaryLabel(n.Result.Loc(),
									fmt.Sprintf("expected error type %s, found %s", resultType.Err.String(), returnedDesc))
							diag = addExplicitCastHint(ctx, diag, resultType.Err, compatibility, n.Result)
							diag = addDerefHintIfNeeded(ctx, mod, diag, resultType.Err, returnedType, n.Result)
							ctx.Diagnostics.Add(diag)
						}
					}
				} else {
					// Returning success: return value
					returnedType := checkExpr(ctx, mod, n.Result, resultType.Ok)

					if ok := checkFitness(ctx, resultType.Ok, n.Result, nil); !ok {
						return
					}

					// Resolve untyped literals
					if types.IsUntyped(returnedType) {
						if lit, ok := n.Result.(*ast.BasicLit); ok {
							returnedType = inferLiteralType(lit, resultType.Ok)
						} else {
							returnedType = types.ResolveUntypedType(returnedType, resultType.Ok)
						}
					}

					if !returnedType.Equals(types.TypeUnknown) && !resultType.Ok.Equals(types.TypeUnknown) {
						compatibility := checkTypeCompatibility(returnedType, resultType.Ok)
						if !isImplicitlyCompatible(compatibility) {
							diag := diagnostics.NewError("return type mismatch").
								WithCode(diagnostics.ErrTypeMismatch).
								WithPrimaryLabel(n.Result.Loc(),
									fmt.Sprintf("expected %s, found %s", resultType.Ok.String(), returnedType.String()))
							diag = addExplicitCastHint(ctx, diag, resultType.Ok, compatibility, n.Result)
							diag = addDerefHintIfNeeded(ctx, mod, diag, resultType.Ok, returnedType, n.Result)
							ctx.Diagnostics.Add(diag)
						}
					}
				}
			} else {
				// Non-Result type function
				if n.IsError {
					// Cannot use error return syntax in non-Result function
					ctx.Diagnostics.Add(
						diagnostics.InvalidErrorReturn(mod.FilePath, n.Loc(), expectedReturnType.String()),
					)
				} else {
					// Normal return type checking
					returnedType := checkExpr(ctx, mod, n.Result, expectedReturnType)
					if ok := checkFitness(ctx, expectedReturnType, n.Result, nil); !ok {
						return
					}
					if !expectedReturnType.Equals(types.TypeUnknown) && !returnedType.Equals(types.TypeUnknown) {
						compatibility := checkTypeCompatibility(returnedType, expectedReturnType)
						if !isImplicitlyCompatible(compatibility) {
							diag := diagnostics.NewError("type mismatch in return statement").
								WithCode(diagnostics.ErrTypeMismatch).
								WithPrimaryLabel(n.Result.Loc(),
									fmt.Sprintf("expected %s, found %s", expectedReturnType.String(), returnedType.String()))
							diag = addExplicitCastHint(ctx, diag, expectedReturnType, compatibility, n.Result)
							diag = addDerefHintIfNeeded(ctx, mod, diag, expectedReturnType, returnedType, n.Result)
							ctx.Diagnostics.Add(diag)
						}
					}
				}
			}
		}
	case *ast.IfStmt:
		// Enter if scope if it exists
		if n.Scope != nil {
			defer mod.EnterScope(n.Scope.(*table.SymbolTable))()
		}

		// Check condition
		checkExpr(ctx, mod, n.Cond, types.TypeBool)

		// Dead code detection (constant conditions) is done in control flow analysis phase

		// Analyze condition for type narrowing
		thenNarrowing, elseNarrowing := narrowingAnalyzer.AnalyzeCondition(ctx, mod, n.Cond, nil)

		// Check then branch with narrowing
		if n.Body != nil {
			applyNarrowingToBlock(ctx, mod, n.Body, thenNarrowing)
		}

		// Check else/else-if chain with narrowing
		if n.Else != nil {
			applyNarrowingToElse(ctx, mod, n.Else, elseNarrowing)
		}
	case *ast.ForStmt:
		// Enter for loop scope if it exists
		if n.Scope != nil {
			defer mod.EnterScope(n.Scope.(*table.SymbolTable))()
		}

		// Check range expression first to infer element type
		var rangeElemType types.SemType = types.TypeUnknown
		var rangeType types.SemType = types.TypeUnknown
		var rangeBaseType types.SemType = types.TypeUnknown
		isIterable := true
		if n.Range != nil {
			// Skip empty array checks for RangeExpr (e.g., 10..=15) - they're not arrays
			_, isRangeExpr := n.Range.(*ast.RangeExpr)

			rangeType = checkExpr(ctx, mod, n.Range, types.TypeUnknown)
			rangeBaseType = types.UnwrapType(rangeType)
			// Extract element type from array/map - for integer ranges this will be i32
			if _, ok := rangeBaseType.(*types.ReferenceType); ok {
				isIterable = false
				ctx.Diagnostics.Add(diagnostics.NewError(fmt.Sprintf("type '%s' is not iterable", rangeBaseType.String())).
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(n.Range.Loc(), "for loop expects an iterable value").
					WithNote("for loops do not auto-dereference references").
					WithHelp(fmt.Sprintf("dereference the expression first: for %s in *%s { ... }", n.Iterator.Loc().GetText(ctx.Diagnostics.GetSourceCache()), n.Range.Loc().GetText(ctx.Diagnostics.GetSourceCache()))))
			} else if arrayType, ok := rangeBaseType.(*types.ArrayType); ok {
				rangeElemType = arrayType.Element

				// Check for empty arrays - error: loop will never execute
				// Only check actual arrays, not range expressions (which are typed as dynamic arrays)
				if !isRangeExpr && arrayType.Length == 0 {
					ctx.Diagnostics.Add(diagnostics.NewError("for loop over empty array will never execute").
						WithPrimaryLabel(n.Range.Loc(), "this array is empty").
						WithNote("Remove this loop or use a non-empty array"))
				}
			} else if prim, ok := rangeBaseType.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
				// Forbid direct string iteration - require explicit cast to []char or []byte
				isIterable = false
				ctx.Diagnostics.Add(
					diagnostics.NewError("cannot iterate over strings directly").
						WithCode(diagnostics.ErrInvalidType).
						WithPrimaryLabel(n.Range.Loc(), "string iteration is not allowed").
						WithHelp("use explicit cast: for x in (str as []char) for character iteration or for x in (str as []byte) for byte iteration"),
				)
			} else if _, ok := rangeBaseType.(*types.MapType); !ok {
				isIterable = false
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("type '%s' is not iterable", rangeBaseType.String())).
						WithCode(diagnostics.ErrInvalidType).
						WithPrimaryLabel(n.Range.Loc(), "for loop expects an iterable value"),
				)
			}

			// Check for empty array literals (not range expressions)
			// Need to unwrap CastExpr to check underlying CompositeLit (e.g., [] as []i32)
			// Note: We only check direct literals, not variables (variables can be updated elsewhere)
			if !isRangeExpr {
				var compLit *ast.CompositeLit
				if castExpr, ok := n.Range.(*ast.CastExpr); ok {
					// Unwrap cast: [] as []i32 -> the CompositeLit is inside the cast
					if cl, ok := castExpr.X.(*ast.CompositeLit); ok {
						compLit = cl
					}
				} else if cl, ok := n.Range.(*ast.CompositeLit); ok {
					compLit = cl
				}

				if compLit != nil && len(compLit.Elts) == 0 {
					ctx.Diagnostics.Add(diagnostics.NewError("for loop over empty array literal will never execute").
						WithPrimaryLabel(n.Range.Loc(), "this array literal is empty").
						WithNote("Remove this loop or add elements to the array"))
				}
			}
		}

		// Check iterator
		if n.Iterator != nil {
			if varDecl, ok := n.Iterator.(*ast.VarDecl); ok && isIterable {
				// For VarDecl iterator: apply types and validate structure
				// For array iteration with two variables: first is index (i32), second is value (element type)
				// For single variable or range iteration: variable gets range element type
				for idx, item := range varDecl.Decls {
					// Validate iterator declaration structure
					if item.Value != nil {
						ctx.Diagnostics.Add(diagnostics.NewError("for loop iterator cannot have initializer").
							WithPrimaryLabel(item.Value.Loc(), "remove this initializer"))
					}

					// Skip placeholder '_' - it doesn't get a symbol or type
					if item.Name.Name == "_" {
						continue
					}

					if item.Type != nil {
						// Explicit type annotation - use it
						declType := TypeFromTypeNodeWithContext(ctx, mod, item.Type)
						if sym, ok := mod.CurrentScope.GetSymbol(item.Name.Name); ok {
							sym.Type = declType
						}
					} else {
						// Infer type based on position and range type
						var inferredType types.SemType
						// Check if range is a map
						if mapType, ok := rangeBaseType.(*types.MapType); ok {
							// Map iteration: key first, value second
							if len(varDecl.Decls) == 1 {
								inferredType = mapType.Key
							} else if idx == 0 {
								inferredType = mapType.Key
							} else {
								inferredType = mapType.Value
							}
						} else if _, ok := rangeBaseType.(*types.ArrayType); ok {
							// Check if range is an array (not a numeric range)
							// Array iteration
							if len(varDecl.Decls) == 2 && idx == 0 {
								// First variable in dual-iterator: index is always i32
								inferredType = types.TypeI32
							} else {
								// Second variable or single variable: gets array element type (value)
								inferredType = rangeElemType
							}
						} else if prim, ok := rangeBaseType.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
							// String iteration: index (i32) and character (byte)
							if len(varDecl.Decls) == 2 && idx == 0 {
								// First variable in dual-iterator: index is always i32
								inferredType = types.TypeI32
							} else {
								// Second variable or single variable: gets byte type
								inferredType = types.TypeByte
							}
						} else {
							// Numeric range: all variables get range element type
							inferredType = rangeElemType
						}

						// Wrap in reference type if & or &mut is used (only for second variable)
						if len(varDecl.Decls) == 2 && idx == 1 {
							if n.SecondIsRef {
								if n.SecondIsMutable {
									inferredType = types.NewMutableReference(inferredType)
								} else {
									inferredType = types.NewReference(inferredType)
								}
							}
						}

						if sym, ok := mod.CurrentScope.GetSymbol(item.Name.Name); ok {
							sym.Type = inferredType
						}
					}
				}
			} else {
				// For IdentifierExpr iterator: validate it exists
				checkNode(ctx, mod, n.Iterator)
			}
		}
		checkBlock(ctx, mod, n.Body)
	case *ast.WhileStmt:
		// Enter while loop scope if it exists
		if n.Scope != nil {
			defer mod.EnterScope(n.Scope.(*table.SymbolTable))()
		}

		if n.Cond == nil {
			ctx.Diagnostics.Add(
				diagnostics.NewError("expected condition after 'while'").
					WithPrimaryLabel(source.NewLocation(n.Filename, n.Body.Start, n.Body.Start), "add a condition value here"),
			)
			checkBlock(ctx, mod, n.Body)
			return
		}

		checkExpr(ctx, mod, n.Cond, types.TypeBool)

		// Dead code detection (constant conditions) is done in control flow analysis phase

		// Analyze condition for narrowing in loop body
		loopNarrowing, _ := narrowingAnalyzer.AnalyzeCondition(ctx, mod, n.Cond, nil)
		applyNarrowingToBlock(ctx, mod, n.Body, loopNarrowing)
	case *ast.MatchStmt:
		// Check the match expression
		matchType := checkExpr(ctx, mod, n.Expr, types.TypeUnknown)

		// Check each case clause
		for _, caseClause := range n.Cases {
			var caseNarrowing *narrowing.NarrowingContext

			if caseClause.Pattern != nil {
				// Handle different pattern types
				switch pattern := caseClause.Pattern.(type) {
				case *ast.TypeCheckPattern:
					// Type check pattern: is Type
					// Resolve the type
					targetType := TypeFromTypeNodeWithContext(ctx, mod, pattern.Type)
					if targetType == nil || targetType.Equals(types.TypeUnknown) {
						ctx.Diagnostics.Add(
							diagnostics.NewError("invalid type in type check pattern").
								WithPrimaryLabel(pattern.Loc(), "cannot resolve type").
								WithCode(diagnostics.ErrTypeMismatch),
						)
					}
					// Type check patterns are always valid for union/interface types
					// No further validation needed here

					// Apply narrowing for 'is Type' patterns
					// Create a synthetic BinaryExpr for narrowing analysis: matchExpr is Type
					syntheticIsExpr := &ast.BinaryExpr{
						X:  n.Expr,
						Op: tokens.Token{Kind: tokens.IS_TOKEN},
						Y:  &ast.TypeExpr{Type: pattern.Type},
					}
					caseNarrowing, _ = narrowingAnalyzer.AnalyzeCondition(ctx, mod, syntheticIsExpr, nil)

				case *ast.RangeCheckPattern:
					// Range check pattern: in Range or in Array
					rangeType := checkExpr(ctx, mod, pattern.Range, types.TypeUnknown)

					if rangeExpr, ok := pattern.Range.(*ast.RangeExpr); ok {
						// Range expression: numeric only
						if !matchType.Equals(types.TypeUnknown) && !rangeType.Equals(types.TypeUnknown) {
							if !types.IsNumericType(matchType) {
								ctx.Diagnostics.Add(
									diagnostics.NewError(fmt.Sprintf("range check requires numeric match expression, got '%s'", matchType.String())).
										WithPrimaryLabel(n.Expr.Loc(), "not a numeric type").
										WithCode(diagnostics.ErrTypeMismatch),
								)
							}
						}

						// Check that range bounds are numeric
						startType := inferExprType(ctx, mod, rangeExpr.Start)
						endType := inferExprType(ctx, mod, rangeExpr.End)
						startBase := types.UnwrapType(startType)
						endBase := types.UnwrapType(endType)

						if !types.IsNumericType(startBase) && !types.IsUntyped(startBase) {
							ctx.Diagnostics.Add(
								diagnostics.NewError(fmt.Sprintf("range start must be numeric, got '%s'", startType.String())).
									WithCode(diagnostics.ErrTypeMismatch).
									WithPrimaryLabel(rangeExpr.Start.Loc(), "not a numeric type"),
							)
						}

						if !types.IsNumericType(endBase) && !types.IsUntyped(endBase) {
							ctx.Diagnostics.Add(
								diagnostics.NewError(fmt.Sprintf("range end must be numeric, got '%s'", endType.String())).
									WithCode(diagnostics.ErrTypeMismatch).
									WithPrimaryLabel(rangeExpr.End.Loc(), "not a numeric type"),
							)
						}
					} else {
						// Array membership
						rangeBase := types.UnwrapType(rangeType)
						if ref, ok := rangeBase.(*types.ReferenceType); ok {
							rangeBase = ref.Inner
						}
						arrayType, ok := rangeBase.(*types.ArrayType)
						if !ok {
							ctx.Diagnostics.Add(
								diagnostics.NewError("range check pattern requires a range or array expression").
									WithPrimaryLabel(pattern.Range.Loc(), "not a range or array expression").
									WithCode(diagnostics.ErrTypeMismatch).
									WithHelp("use a range expression like '0..10' or an array like '[1, 2, 3]'"),
							)
							break
						}

						elemType := arrayType.Element
						if elemType == nil {
							elemType = types.TypeUnknown
						}
						if !matchType.Equals(types.TypeUnknown) && !elemType.Equals(types.TypeUnknown) {
							compat := checkTypeCompatibility(matchType, elemType)
							if compat == Incompatible {
								ctx.Diagnostics.Add(
									diagnostics.NewError(fmt.Sprintf("pattern type '%s' does not match array element type '%s'", matchType.String(), elemType.String())).
										WithPrimaryLabel(pattern.Range.Loc(), "incompatible pattern type").
										WithCode(diagnostics.ErrTypeMismatch).
										WithNote(fmt.Sprintf("match expression has type '%s'", matchType.String())),
								)
							}
						}
					}

				default:
					// Regular value match pattern
					patternType := checkExpr(ctx, mod, caseClause.Pattern, types.TypeUnknown)

					// Check if pattern type is compatible with match expression type
					if !matchType.Equals(types.TypeUnknown) && !patternType.Equals(types.TypeUnknown) {
						// Check if types are compatible (exact match or compatible types)
						compat := checkTypeCompatibility(patternType, matchType)
						if !isImplicitlyCompatible(compat) {
							diag := diagnostics.NewError(fmt.Sprintf("pattern type '%s' does not match match expression type '%s'", patternType.String(), matchType.String())).
								WithPrimaryLabel(caseClause.Pattern.Loc(), "incompatible pattern type").
								WithCode(diagnostics.ErrTypeMismatch).
								WithNote(fmt.Sprintf("match expression has type '%s'", matchType.String()))
							diag = addExplicitCastHint(ctx, diag, matchType, compat, caseClause.Pattern)
							ctx.Diagnostics.Add(diag)
						}
					}
				}
			}

			// Enter case body scope if it exists
			var restoreScope func()
			if caseClause.Body != nil && caseClause.Body.Scope != nil {
				restoreScope = mod.EnterScope(caseClause.Body.Scope.(*table.SymbolTable))
			}

			// Check case body with narrowing if available
			if caseNarrowing != nil {
				applyNarrowingToBlock(ctx, mod, caseClause.Body, caseNarrowing)
			} else {
				checkBlock(ctx, mod, caseClause.Body)
			}

			// Restore scope if we entered one
			if restoreScope != nil {
				restoreScope()
			}
		}
	case *ast.Block:
		checkBlock(ctx, mod, n)
	case *ast.DeclStmt:
		checkNode(ctx, mod, n.Decl)
	case *ast.ExprStmt:
		checkExpr(ctx, mod, n.X, types.TypeUnknown)
	}
}

// checkVarDecl type checks variable/constant declarations
func checkVarDecl(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl any, isConst bool) {
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
		// Safeguard: Skip items with invalid syntax
		// - nil Name from parser errors
		// - Name with "<error>" placeholder (parser failed to get identifier)
		if item.Name == nil || item.Name.Name == "<error>" {
			continue
		}

		name := item.Name.Name

		if name == "_" {
			ctx.Diagnostics.Add(
				diagnostics.NewError("blank identifier '_' is only allowed in for loop iterators and function parameters").
					WithPrimaryLabel(item.Name.Loc(), "cannot declare '_' here").
					WithHelp("use a real variable name"),
			)
			if item.Value != nil {
				expectedType := types.TypeUnknown
				if item.Type != nil {
					expectedType = TypeFromTypeNodeWithContext(ctx, mod, item.Type)
					checkAssignLike(ctx, mod, expectedType, item.Type, item.Value)
				} else {
					checkExpr(ctx, mod, item.Value, expectedType)
				}
			}
			continue
		}

		// Get or create the symbol (module-level or local)
		sym, ok := mod.CurrentScope.GetSymbol(name)
		if !ok {
			continue
		}

		if item.Value != nil {
			if heapUnaryExpr(item.Value) != nil {
				if isConst {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("constant '%s' cannot use heap allocation", name)).
							WithCode(diagnostics.ErrInvalidOperation).
							WithPrimaryLabel(item.Value.Loc(), "heap allocation is not allowed in constants").
							WithHelp("use a runtime variable instead of const"),
					)
				} else {
					sym.IsHeap = true
				}
			}
		}

		if item.Value != nil {
			if found, loc := referencesIdentOutsideFuncLit(item.Value, name); found {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("'%s' references itself in its initializer", name)).
						WithCode(diagnostics.ErrCircularDependency).
						WithPrimaryLabel(loc, "self reference here").
						WithSecondaryLabel(item.Name.Loc(), "declared here"),
				)
			}
		}

		// Determine the type
		if item.Type != nil {
			// Explicit type annotation
			declType := TypeFromTypeNodeWithContext(ctx, mod, item.Type)

			// Disallow 'none' as a variable/const type
			if declType.Equals(types.TypeNone) {
				declKind := "variable"
				if isConst {
					declKind = "constant"
				}
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("%s '%s' cannot have type 'none'", declKind, name)).
						WithPrimaryLabel(item.Name.Loc(), "'none' is not a valid type for variables or constants").
						WithHelp("'none' is only used as a value for optional types like ?i32"),
				)
				sym.Type = types.TypeUnknown
				continue
			}

			sym.Type = declType

			// Check initializer if present
			if item.Value != nil {
				if _, ok := types.UnwrapType(declType).(*types.ReferenceType); ok {
					initType := inferExprType(ctx, mod, item.Value)
					if !initType.Equals(types.TypeUnknown) && !isReferenceType(initType) {
						ctx.Diagnostics.Add(
							diagnostics.NewError("reference variables must be initialized with a reference").
								WithCode(diagnostics.ErrInvalidAssignment).
								WithPrimaryLabel(item.Value.Loc(), "expected a reference value").
								WithHelp("use '&' to bind a reference"),
						)
						checkExpr(ctx, mod, item.Value, declType)
						continue
					}
				}
				// Pass item.Type for type location in error messages
				checkAssignLike(ctx, mod, declType, item.Type, item.Value)
			} else if _, ok := types.UnwrapType(declType).(*types.ReferenceType); ok {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("reference '%s' must be initialized", name)).
						WithCode(diagnostics.ErrInvalidAssignment).
						WithPrimaryLabel(item.Name.Loc(), "references require an initializer").
						WithHelp("bind with '&': let r: &T = &value"),
				)
			} else if isConst {
				// Constants must have an initializer even with explicit type
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("constant '%s' must be initialized", name)).
						WithPrimaryLabel(item.Name.Loc(), "constants require an initializer"),
				)
			}
		} else if item.Value != nil {
			// Type inference from initializer
			rhsType := checkExpr(ctx, mod, item.Value, types.TypeUnknown)

			if rhsType.Equals(types.TypeUnknown) {
				if compLit, ok := item.Value.(*ast.CompositeLit); ok {
					kind := compositeLiteralKind(compLit)
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("cannot infer type for '%s' from %s literal", name, kind)).
							WithPrimaryLabel(item.Value.Loc(), "type is ambiguous").
							WithHelp(fmt.Sprintf("add an explicit type: let %s : <type> = ... or cast with `as <type>`", name)),
					)
					sym.Type = types.TypeUnknown
					continue
				}
			}

			// If the RHS is UNTYPED, finalize it to a default type
			if types.IsUntyped(rhsType) {
				if resolved, ok := resolveUntypedNumericExpr(item.Value); ok {
					rhsType = resolved
					if rhsType.Equals(types.TypeUnknown) {
						reportNumericConstTooLarge(ctx, item.Value)
					}
				} else {
					rhsType = types.ResolveUntypedType(rhsType, types.TypeUnknown)
				}
				if mod != nil {
					mod.SetExprType(item.Value, rhsType)
				}
			}

			sym.Type = rhsType

			// Check if initializer is an empty literal (array, map, or struct)
			// If it has a type (via cast or type annotation), warn to use explicit type annotation
			// If it has no type, error because type cannot be inferred
			if isEmptyLiteral(item.Value) {
				// Infer the type from the cast or composite literal to suggest the correct type
				suggestedType := inferTypeFromEmptyLiteral(ctx, mod, item.Value)
				if suggestedType != "" {
					// Has type information (e.g., [] as []i32) - warn to use explicit type annotation
					ctx.Diagnostics.Add(diagnostics.NewWarning("use explicit type annotation instead of assigning to empty value").
						WithPrimaryLabel(item.Value.Loc(), "empty literal with type inference").
						WithNote(fmt.Sprintf("use `let %s : %s;` instead", name, suggestedType)))
				} else {
					// No type information (e.g., []) - error because type cannot be inferred
					ctx.Diagnostics.Add(diagnostics.NewError("cannot infer type from empty literal").
						WithPrimaryLabel(item.Value.Loc(), "empty literal with no type information").
						WithNote(fmt.Sprintf("use explicit type annotation: `let %s : <type>;` instead", name)))
					sym.Type = types.TypeUnknown
				}
			}

		} else {
			// No type and no value - error
			if isConst {
				// Constants must have an initializer
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("constant '%s' must be initialized", name)).
						WithPrimaryLabel(item.Name.Loc(), "constants require an initializer").
						WithHelp("provide a value: const x := 42 or const x: i32 = 42"),
				)
			} else {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("cannot infer type for '%s'", name)).
						WithPrimaryLabel(item.Name.Loc(), "missing type or initializer"),
				)
			}
			sym.Type = types.TypeUnknown
		}
	}
}

// checkFuncDecl type checks a function declaration
func checkFuncDecl(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.FuncDecl) {
	// Safety check: Scope should be set during collection phase
	if decl.Scope == nil {
		ctx.ReportError(fmt.Sprintf("internal error: function '%s' has nil scope", decl.Name.Name), decl.Loc())
		return
	}

	funcScope := decl.Scope.(*table.SymbolTable)

	// Update the function symbol's type with actual function signature
	if decl.Name != nil && decl.Type != nil {
		funcType := TypeFromTypeNodeWithContext(ctx, mod, decl.Type).(*types.FunctionType)
		if sym, ok := mod.CurrentScope.Lookup(decl.Name.Name); ok {
			sym.Type = funcType
		}
	}

	// Set up function context (scope and return type)
	defer setupFunctionContext(ctx, mod, funcScope, decl.Type)()

	// Add parameters to the function scope with type information
	if decl.Type != nil {
		addParamsToScope(ctx, mod, funcScope, decl.Type.Params)
	}

	// Check the body with the function scope
	if decl.Body != nil {
		checkBlock(ctx, mod, decl.Body)
		// Return path analysis is done in control flow analysis phase, not here
	}
}

// checkSelectorExpr validates that a field or method exists on a struct
// checkBinaryExpr validates that operands of a binary expression have compatible types
func checkBinaryExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.BinaryExpr, lhsType, rhsType types.SemType) {
	// Skip if either type is unknown (error already reported)
	if lhsType.Equals(types.TypeUnknown) || rhsType.Equals(types.TypeUnknown) {
		return
	}
	lhsBase := types.UnwrapType(lhsType)
	rhsBase := types.UnwrapType(rhsType)

	switch expr.Op.Kind {
	case tokens.PLUS_TOKEN:
		lhsString := lhsBase.Equals(types.TypeString)
		rhsString := rhsBase.Equals(types.TypeString)
		lhsNumericOrUntyped := types.IsNumericType(lhsBase) || types.IsUntyped(lhsBase)
		rhsNumericOrUntyped := types.IsNumericType(rhsBase) || types.IsUntyped(rhsBase)
		rhsBool := rhsBase.Equals(types.TypeBool)

		// String concatenation rules:
		// str + str -> ok
		// str + number -> ok (number converted to string)
		// str + bool -> ok (bool converted to "true"/"false")
		// number + str -> ERROR (LHS must be string)
		if lhsString {
			if rhsString || rhsNumericOrUntyped || rhsBool {
				return // Valid string concatenation
			}
		}

		// If RHS is string but LHS is not, that's an error
		if rhsString && !lhsString {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("invalid operation: %s (mismatched types %s and %s)", expr.Op.Value, lhsType.String(), rhsType.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(expr.Loc(), fmt.Sprintf("cannot use '+' with %s and %s", lhsType.String(), rhsType.String())).
					WithHelp("for string concatenation, the left operand must be a string"),
			)
			return
		}

		// Both numeric - allow implicit widening
		if lhsNumericOrUntyped && rhsNumericOrUntyped {
			// Allow untyped operands - literal fitness is checked earlier
			if types.IsUntyped(lhsBase) || types.IsUntyped(rhsBase) {
				return
			}

			if numericCommonType(lhsType, rhsType).Equals(types.TypeUnknown) {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("mismatched types in arithmetic: %s and %s", lhsType.String(), rhsType.String())).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(expr.Loc(), "operands must have compatible numeric types").
						WithHelp(fmt.Sprintf("cast one operand to match: `%s as %s` or `%s as %s`",
							expr.X.Loc().GetText(ctx.Diagnostics.GetSourceCache()), rhsType.String(), expr.Y.Loc().GetText(ctx.Diagnostics.GetSourceCache()), lhsType.String())),
				)
				return
			}
			return
		}

		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("invalid operation: %s (mismatched types %s and %s)", expr.Op.Value, lhsType.String(), rhsType.String())).
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(expr.Loc(), fmt.Sprintf("cannot use '+' with %s and %s", lhsType.String(), rhsType.String())).
				WithHelp("'+' operator requires both operands to be numeric, or left operand to be string"),
		)
		return

	case tokens.MINUS_TOKEN, tokens.MUL_TOKEN, tokens.DIV_TOKEN, tokens.MOD_TOKEN:
		// These operators only work with numeric types
		lhsNumeric := types.IsNumericType(lhsBase) || types.IsUntyped(lhsBase)
		rhsNumeric := types.IsNumericType(rhsBase) || types.IsUntyped(rhsBase)

		if !lhsNumeric || !rhsNumeric {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("invalid operation: %s (mismatched types %s and %s)", expr.Op.Value, lhsType.String(), rhsType.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(expr.Loc(), fmt.Sprintf("cannot use '%s' operator with %s and %s", expr.Op.Value, lhsType.String(), rhsType.String())),
			)
			return
		}

		// Allow untyped operands - literal fitness is checked earlier
		if types.IsUntyped(lhsBase) || types.IsUntyped(rhsBase) {
			return
		}

		if numericCommonType(lhsType, rhsType).Equals(types.TypeUnknown) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("mismatched types in arithmetic: %s and %s", lhsType.String(), rhsType.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(expr.Loc(), "operands must have compatible numeric types").
					WithHelp(fmt.Sprintf("cast one operand to match: `%s as %s` or `%s as %s`",
						expr.X.Loc().GetText(ctx.Diagnostics.GetSourceCache()), rhsType.String(), expr.Y.Loc().GetText(ctx.Diagnostics.GetSourceCache()), lhsType.String())),
			)
			return
		}

	case tokens.BIT_AND_TOKEN, tokens.BIT_OR_TOKEN, tokens.BIT_XOR_TOKEN:
		// Allow untyped integer operands - literal fitness is checked earlier
		lhsInt := types.IsInteger(lhsBase) || types.IsUntypedInt(lhsBase)
		rhsInt := types.IsInteger(rhsBase) || types.IsUntypedInt(rhsBase)
		if !lhsInt || !rhsInt {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("invalid operation: %s (mismatched types %s and %s)", expr.Op.Value, lhsType.String(), rhsType.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(expr.Loc(), fmt.Sprintf("cannot use '%s' operator with %s and %s", expr.Op.Value, lhsType.String(), rhsType.String())).
					WithHelp("bitwise operators require integer operands"),
			)
			return
		}

		// Require exact type match once operands are typed
		if !types.IsUntyped(lhsBase) && !types.IsUntyped(rhsBase) && !lhsType.Equals(rhsType) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("mismatched types in bitwise operation: %s and %s", lhsType.String(), rhsType.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(expr.Loc(), "operands must have the same type").
					WithHelp(fmt.Sprintf("cast one operand to match: `%s as %s` or `%s as %s`",
						expr.X.Loc().GetText(ctx.Diagnostics.GetSourceCache()), lhsType.String(), expr.Y.Loc().GetText(ctx.Diagnostics.GetSourceCache()), rhsType.String())),
			)
			return
		}

	case tokens.DOUBLE_EQUAL_TOKEN, tokens.NOT_EQUAL_TOKEN:
		// Allow untyped operands for comparisons - literal fitness is checked earlier
		if types.IsUntyped(lhsType) || types.IsUntyped(rhsType) {
			return
		}

		// Special case: allow comparing optional with none
		lhsOptional := false
		rhsOptional := false
		lhsNone := lhsType == types.TypeNone
		rhsNone := rhsType == types.TypeNone

		if _, ok := lhsType.(*types.OptionalType); ok {
			lhsOptional = true
		}
		if _, ok := rhsType.(*types.OptionalType); ok {
			rhsOptional = true
		}

		// Allow ?T == none or none == ?T
		if (lhsOptional && rhsNone) || (rhsOptional && lhsNone) {
			return // Valid comparison
		}

		// Comparison operators require compatible types
		compatibility := checkTypeCompatibility(lhsType, rhsType)
		if compatibility == Incompatible {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("invalid operation: cannot compare %s and %s", lhsType.String(), rhsType.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(expr.Loc(), "incompatible types in comparison"),
			)
		}

	case tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN, tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN:
		// Allow untyped operands for ordering comparisons - literal fitness is checked earlier
		if types.IsUntyped(lhsType) || types.IsUntyped(rhsType) {
			return
		}

		// Ordering operators require numeric types
		lhsNumeric := types.IsNumericType(lhsType)
		rhsNumeric := types.IsNumericType(rhsType)

		if !lhsNumeric || !rhsNumeric {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("invalid operation: cannot compare %s and %s", lhsType.String(), rhsType.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(expr.Loc(), "ordering comparison requires numeric types"),
			)
		}

	case tokens.IN_TOKEN:
		// 'in' operator: value in range OR value in array
		if rangeExpr, ok := expr.Y.(*ast.RangeExpr); ok {
			// Range check: numeric only
			if !types.IsNumericType(lhsBase) && !types.IsUntyped(lhsBase) {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("'in' operator requires numeric value, got '%s'", lhsType.String())).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(expr.X.Loc(), "not a numeric type"),
				)
				return
			}

			// Check that range bounds are numeric
			startType := inferExprType(ctx, mod, rangeExpr.Start)
			endType := inferExprType(ctx, mod, rangeExpr.End)
			startBase := types.UnwrapType(startType)
			endBase := types.UnwrapType(endType)

			if !types.IsNumericType(startBase) && !types.IsUntyped(startBase) {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("range start must be numeric, got '%s'", startType.String())).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(rangeExpr.Start.Loc(), "not a numeric type"),
				)
			}

			if !types.IsNumericType(endBase) && !types.IsUntyped(endBase) {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("range end must be numeric, got '%s'", endType.String())).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(rangeExpr.End.Loc(), "not a numeric type"),
				)
			}
			return
		}

		// Array membership
		rhsBase := types.UnwrapType(rhsType)
		if ref, ok := rhsBase.(*types.ReferenceType); ok {
			rhsBase = ref.Inner
		}
		arrayType, ok := rhsBase.(*types.ArrayType)
		if !ok {
			ctx.Diagnostics.Add(
				diagnostics.NewError("'in' operator requires a range or array expression on the right side").
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(expr.Y.Loc(), "not a range or array expression").
					WithHelp("use a range like '0..10' or an array like '[1, 2, 3]'"),
			)
			return
		}

		elemType := arrayType.Element
		if elemType == nil {
			elemType = types.TypeUnknown
		}
		// Range expressions are typed as dynamic arrays but should be treated
		// as numeric range checks for `in` to avoid allocation in codegen.
		if _, ok := expr.Y.(*ast.RangeExpr); ok {
			return
		}
		if !types.IsUntyped(lhsType) && !types.IsUntyped(elemType) {
			compatibility := checkTypeCompatibility(lhsType, elemType)
			if compatibility == Incompatible {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("'in' operator requires compatible types, got '%s' in '%s'", lhsType.String(), elemType.String())).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(expr.Loc(), "incompatible types in membership check"),
				)
			}
		}

	case tokens.AND_TOKEN, tokens.OR_TOKEN:
		// Logical operators require bool types
		if !lhsType.Equals(types.TypeBool) || !rhsType.Equals(types.TypeBool) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("invalid operation: logical operator requires bool operands, got %s and %s", lhsType.String(), rhsType.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(expr.Loc(), "logical operators require bool types"),
			)
		}
	}
}

func numericCommonType(lhsType, rhsType types.SemType) types.SemType {
	if lhsType == nil || rhsType == nil {
		return types.TypeUnknown
	}
	if lhsType.Equals(types.TypeUnknown) || rhsType.Equals(types.TypeUnknown) {
		return types.TypeUnknown
	}

	lhsBase := types.UnwrapType(lhsType)
	rhsBase := types.UnwrapType(rhsType)
	lhsNumericOrUntyped := types.IsNumericType(lhsBase) || types.IsUntyped(lhsBase)
	rhsNumericOrUntyped := types.IsNumericType(rhsBase) || types.IsUntyped(rhsBase)

	if !lhsNumericOrUntyped || !rhsNumericOrUntyped {
		return types.TypeUnknown
	}

	if types.IsUntyped(lhsBase) && types.IsUntyped(rhsBase) {
		if types.IsUntypedFloat(lhsBase) || types.IsUntypedFloat(rhsBase) {
			return types.TypeUntypedFloat
		}
		return types.TypeUntypedInt
	}
	if types.IsUntyped(lhsBase) {
		return rhsType
	}
	if types.IsUntyped(rhsBase) {
		return lhsType
	}

	if lhsType.Equals(rhsType) {
		return lhsType
	}

	if isImplicitlyCompatible(checkTypeCompatibility(lhsType, rhsType)) {
		return rhsType
	}
	if isImplicitlyCompatible(checkTypeCompatibility(rhsType, lhsType)) {
		return lhsType
	}

	return types.TypeUnknown
}

func numericBinaryResultType(op tokens.TOKEN, lhsType, rhsType types.SemType) types.SemType {
	common := numericCommonType(lhsType, rhsType)
	if common.Equals(types.TypeUnknown) {
		return types.TypeUnknown
	}
	if types.IsUntyped(common) {
		return common
	}

	if op != tokens.DIV_TOKEN {
		return common
	}

	commonBase := types.UnwrapType(common)
	if types.IsFloat(commonBase) {
		return common
	}
	if isLargeIntType(common) {
		return common
	}
	if primType, ok := commonBase.(*types.PrimitiveType); ok {
		bitWidth := types.GetNumberBitSize(primType.GetName())
		if bitWidth > 0 && bitWidth <= 32 {
			return types.TypeF32
		}
	}
	return types.TypeF64
}

func bindUntypedNumericLiteral(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression, exprType, otherType types.SemType, otherExpr ast.Expression) types.SemType {
	if !types.IsUntyped(exprType) {
		return exprType
	}

	if otherType == nil || otherType.Equals(types.TypeUnknown) || types.IsUntyped(otherType) {
		return exprType
	}

	value, ok := evaluateNumericConst(expr)
	if !ok {
		return exprType
	}

	otherBase := types.UnwrapType(otherType)
	if !types.IsNumeric(otherBase) {
		return exprType
	}

	if value.kind == ast.FLOAT && !types.IsFloat(otherBase) {
		reportNumericLiteralTypeMismatch(ctx, expr, otherExpr, "float", otherType)
		return types.TypeUnknown
	}
	if value.kind == ast.INT && !types.IsInteger(otherBase) {
		reportNumericLiteralTypeMismatch(ctx, expr, otherExpr, "integer", otherType)
		return types.TypeUnknown
	}

	if !checkFitness(ctx, otherType, expr, otherExpr) {
		return types.TypeUnknown
	}

	if mod != nil {
		mod.SetExprType(expr, otherType)
	}
	return otherType
}

func reportNumericLiteralTypeMismatch(ctx *context_v2.CompilerContext, literal ast.Expression, otherExpr ast.Expression, literalKind string, targetType types.SemType) {
	if ctx == nil || literal == nil {
		return
	}

	diag := diagnostics.NewError(fmt.Sprintf("cannot use %s literal as type '%s'", literalKind, targetType.String())).
		WithCode(diagnostics.ErrTypeMismatch).
		WithPrimaryLabel(literal.Loc(), fmt.Sprintf("%s literal", literalKind))

	if otherExpr != nil {
		diag = diag.WithSecondaryLabel(otherExpr.Loc(), fmt.Sprintf("type '%s'", targetType.String()))
	}

	ctx.Diagnostics.Add(diag)
}

// checkCastExpr validates that a cast expression is valid
func checkCastExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CastExpr, sourceType, targetType types.SemType) {
	// Skip if target type is unknown
	if targetType.Equals(types.TypeUnknown) {
		return
	}

	// Skip further validation if source type is unknown (error already reported)
	if sourceType.Equals(types.TypeUnknown) {
		return
	}

	// Allow untyped literals to be cast to any compatible type
	if types.IsUntyped(sourceType) {
		return
	}

	// Check interface casts: allow casting to interface if type implements it
	// Handle both direct InterfaceType and NamedType wrapping an InterfaceType
	var targetIface *types.InterfaceType
	if iface, ok := targetType.(*types.InterfaceType); ok {
		targetIface = iface
	} else if named, ok := targetType.(*types.NamedType); ok {
		if iface, ok := named.Underlying.(*types.InterfaceType); ok {
			targetIface = iface
		}
	}
	if targetIface != nil {
		compatible, missingMethods := analyzeInterfaceCompatibility(ctx, mod, sourceType, targetIface)
		if compatible {
			return // Valid interface cast
		}
		// Report error with detailed missing methods
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot cast %s to %s", sourceType.String(), targetType.String())).
				WithCode(diagnostics.ErrInvalidCast).
				WithPrimaryLabel(expr.Loc(), "type does not implement interface").
				WithHelp(fmt.Sprintf("missing methods: %s", strings.Join(missingMethods, ", "))),
		)
		return
	}

	// For struct types, check structural compatibility (unwrap NamedType on both sides)
	srcUnwrapped := types.UnwrapType(sourceType)
	dstUnwrapped := types.UnwrapType(targetType)

	// Handle string <-> array conversions
	srcPrim, srcIsPrim := sourceType.(*types.PrimitiveType)
	dstPrim, dstIsPrim := targetType.(*types.PrimitiveType)
	srcArr, srcIsArr := srcUnwrapped.(*types.ArrayType)
	dstArr, dstIsArr := dstUnwrapped.(*types.ArrayType)

	// str -> []char (UTF-8 decode to Unicode scalars)
	if srcIsPrim && srcPrim.GetName() == types.TYPE_STRING && dstIsArr && dstArr.Length < 0 {
		if elemPrim, ok := dstArr.Element.(*types.PrimitiveType); ok && elemPrim.GetName() == types.TYPE_CHAR {
			// Valid: str as []char
			return
		}
	}

	// str -> []byte (view/copy of UTF-8 bytes)
	if srcIsPrim && srcPrim.GetName() == types.TYPE_STRING && dstIsArr && dstArr.Length < 0 {
		if elemPrim, ok := dstArr.Element.(*types.PrimitiveType); ok && (elemPrim.GetName() == types.TYPE_BYTE || elemPrim.GetName() == types.TYPE_U8) {
			// Valid: str as []byte or str as []u8
			return
		}
	}

	// []char -> str (UTF-8 encode)
	if srcIsArr && srcArr.Length < 0 && dstIsPrim && dstPrim.GetName() == types.TYPE_STRING {
		if elemPrim, ok := srcArr.Element.(*types.PrimitiveType); ok && elemPrim.GetName() == types.TYPE_CHAR {
			// Valid: []char as str
			return
		}
	}

	// []byte -> str (interpret as UTF-8)
	if srcIsArr && srcArr.Length < 0 && dstIsPrim && dstPrim.GetName() == types.TYPE_STRING {
		if elemPrim, ok := srcArr.Element.(*types.PrimitiveType); ok && (elemPrim.GetName() == types.TYPE_BYTE || elemPrim.GetName() == types.TYPE_U8) {
			// Valid: []byte as str or []u8 as str
			return
		}
	}

	// char <-> byte conversions
	if srcIsPrim && dstIsPrim {
		if (srcPrim.GetName() == types.TYPE_CHAR && (dstPrim.GetName() == types.TYPE_BYTE || dstPrim.GetName() == types.TYPE_U8)) ||
			((srcPrim.GetName() == types.TYPE_BYTE || srcPrim.GetName() == types.TYPE_U8) && dstPrim.GetName() == types.TYPE_CHAR) {
			// Valid: char as byte, byte as char
			// char -> byte: truncate to lower 8 bits
			// byte -> char: zero-extend to 32-bit Unicode scalar
			return
		}
		// Allow integer -> char conversions (interpret as Unicode code point)
		if types.IsIntegerTypeName(srcPrim.GetName()) && dstPrim.GetName() == types.TYPE_CHAR {
			// Valid: i32 as char, etc.
			// Runtime should validate Unicode scalar range
			return
		}
		// Allow char -> integer conversions (get Unicode code point)
		if srcPrim.GetName() == types.TYPE_CHAR && types.IsIntegerTypeName(dstPrim.GetName()) {
			// Valid: char as i32, etc.
			return
		}
	}

	// Check if cast is valid
	compatibility := checkTypeCompatibility(sourceType, targetType)

	// Handle map type casts
	if srcMap, ok := srcUnwrapped.(*types.MapType); ok {
		if dstMap, ok := dstUnwrapped.(*types.MapType); ok {
			// Allow cast if key and value types are compatible
			keyCompat := checkTypeCompatibility(srcMap.Key, dstMap.Key)
			valueCompat := checkTypeCompatibility(srcMap.Value, dstMap.Value)

			if keyCompat != Incompatible && valueCompat != Incompatible {
				return // Compatible map cast
			}

			// Build error for incompatible map cast
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot cast %s to %s", sourceType.String(), targetType.String())).
					WithCode(diagnostics.ErrInvalidCast).
					WithPrimaryLabel(expr.Loc(), "incompatible map types").
					WithHelp(fmt.Sprintf("key types: %s vs %s, value types: %s vs %s",
						srcMap.Key.String(), dstMap.Key.String(),
						srcMap.Value.String(), dstMap.Value.String())),
			)
			return
		}
	}

	if srcStruct, ok := srcUnwrapped.(*types.StructType); ok {
		if dstStruct, ok := dstUnwrapped.(*types.StructType); ok {
			// Check if struct types are compatible by structure
			missingFields, mismatchedFields := analyzeStructCompatibility(srcStruct, dstStruct)
			if note := formatStructCompatibilityNote(missingFields, mismatchedFields, false); note != "" {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("cannot cast %s to %s", sourceType.String(), targetType.String())).
						WithCode(diagnostics.ErrInvalidCast).
						WithPrimaryLabel(expr.Loc(), "invalid cast").
						WithNote(note),
				)
			}
			return
		}
	}

	if isEnumType(sourceType) && isIntegerType(targetType) {
		return
	}
	if isBoolType(sourceType) && isIntegerType(targetType) {
		return
	}

	// Allow explicit casts between numeric types
	if isNumericOrBool(sourceType) && isNumericOrBool(targetType) {
		return
	}

	// Allow explicit casts involving named types:
	// 1. NamedType -> underlying type (e.g., Integer -> i32)
	// 2. NamedType -> NamedType with same underlying type (e.g., Integer -> Count where both wrap i32)
	// 3. Base type -> NamedType (already handled above if both are numeric)
	// Note: srcUnwrapped and dstUnwrapped are already declared above

	// Check if underlying types are compatible (allows named type casts)
	if isNumericOrBool(srcUnwrapped) && isNumericOrBool(dstUnwrapped) {
		// Allow cast if underlying types are compatible (all numeric types can cast to each other)
		return
	}

	// Check if underlying types are the same (allows named -> named or named -> base)
	if srcUnwrapped.Equals(dstUnwrapped) {
		return
	}

	// Otherwise, check standard compatibility
	if compatibility == Incompatible {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot cast %s to %s", sourceType.String(), targetType.String())).
				WithCode(diagnostics.ErrInvalidCast).
				WithPrimaryLabel(expr.Loc(), "invalid cast"),
		)
	}
}

func isEnumType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	_, ok := typ.(*types.EnumType)
	return ok
}

func isIntegerType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	if prim, ok := typ.(*types.PrimitiveType); ok {
		return types.IsIntegerTypeName(prim.GetName())
	}
	return false
}

func isBoolType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	if prim, ok := typ.(*types.PrimitiveType); ok {
		return prim.GetName() == types.TYPE_BOOL
	}
	return false
}

func isNumericOrBool(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	if prim, ok := typ.(*types.PrimitiveType); ok {
		if prim.GetName() == types.TYPE_BOOL {
			return true
		}
	}
	return types.IsNumericType(typ)
}

func isInterfaceType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	_, ok := typ.(*types.InterfaceType)
	return ok
}

func checkIndexExpr(ctx *context_v2.CompilerContext, expr *ast.IndexExpr, baseType, indexType types.SemType) {
	if ctx == nil || expr == nil {
		return
	}
	if baseType.Equals(types.TypeUnknown) {
		return
	}
	if indexType.Equals(types.TypeUnknown) {
		return
	}
	baseType = autoDerefBaseType(expr, baseType)
	indexType = types.UnwrapType(indexType)
	isIntegerIndex := types.IsInteger(indexType) || types.IsUntypedInt(indexType)

	if arrType, ok := baseType.(*types.ArrayType); ok {
		if !isIntegerIndex {
			ctx.Diagnostics.Add(
				diagnostics.NewError("array index must be an integer").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(expr.Index.Loc(), "expected integer index"),
			)
		}
		_ = arrType
		return
	}
	if mapType, ok := baseType.(*types.MapType); ok {
		// Check if index type can be assigned to map key type (source, target order)
		// This allows implicit boxing: i32 -> interface{}, etc.
		if checkTypeCompatibility(indexType, mapType.Key) == Incompatible {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("map index must be %s", mapType.Key.String())).
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(expr.Index.Loc(), "incompatible map key type"),
			)
		}
		if isInterfaceType(mapType.Key) && !indexType.Equals(types.TypeUnknown) && !isInterfaceType(indexType) && !types.IsMapKeyComparable(indexType) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("map key type '%s' is not comparable", indexType.String())).
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(expr.Index.Loc(), "map keys must be comparable"),
			)
		}
		return
	}
	if prim, ok := baseType.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
		// Forbid direct string indexing - require explicit cast to []char or []byte
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot index strings directly").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.Loc(), "string indexing is not allowed").
				WithHelp("use explicit cast: (str as []char)[i] for character access or (str as []byte)[i] for byte access"),
		)
		return
	}

	ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("type %s is not indexable", baseType.String())).
			WithCode(diagnostics.ErrNotIndexable).
			WithPrimaryLabel(expr.Loc(), "not indexable"),
	)
}

// checkCompositeLit validates that a composite literal matches its target type
// It handles both explicit types (from lit.Type) and inferred types (when targetType is provided)
// This function checks elements with context AND validates consistency
// Returns missing fields for struct literals, nil otherwise
func checkCompositeLit(ctx *context_v2.CompilerContext, mod *context_v2.Module, lit *ast.CompositeLit, targetType types.SemType) []string {
	// Unwrap NamedType to get the underlying type
	underlyingType := types.UnwrapType(targetType)

	// Handle arrays
	if arrayType, ok := underlyingType.(*types.ArrayType); ok {
		validateArrayLiteral(ctx, mod, lit, arrayType)
		return nil
	}

	// Handle map literals
	if mapType, ok := underlyingType.(*types.MapType); ok {
		// Only validate as map if not all keys are identifiers (structs have all IdentifierExpr keys)
		allKeysAreIdentifiers := true
		hasKeyValueExpr := false
		for _, elem := range lit.Elts {
			if kv, ok := elem.(*ast.KeyValueExpr); ok {
				hasKeyValueExpr = true
				if _, isIdent := kv.Key.(*ast.IdentifierExpr); !isIdent {
					allKeysAreIdentifiers = false
					break
				}
			}
		}
		if hasKeyValueExpr && !allKeysAreIdentifiers {
			checkMapLiteral(ctx, mod, lit, mapType)
		}
		return nil
	}

	// Handle struct literals
	if structType, ok := underlyingType.(*types.StructType); ok {
		return validateStructLiteral(ctx, mod, lit, structType, targetType)
	}

	return nil
}

// validateStructLiteral validates that a struct literal has all required fields and correct types
// Uses analyzeStructCompatibility for missing/mismatched field detection
func validateStructLiteral(ctx *context_v2.CompilerContext, mod *context_v2.Module, lit *ast.CompositeLit, structType *types.StructType, originalType types.SemType) []string {
	// Build an inferred struct type from the literal's fields
	// This allows us to reuse analyzeStructCompatibility
	var inferredFields []types.StructField
	fieldExprs := make(map[string]ast.Expression) // Track expressions for error reporting

	for _, elem := range lit.Elts {
		if kv, ok := elem.(*ast.KeyValueExpr); ok {
			if ident, ok := kv.Key.(*ast.IdentifierExpr); ok {
				// Find expected field type for contextual type checking
				var expectedType types.SemType = types.TypeUnknown
				for _, field := range structType.Fields {
					if field.Name == ident.Name {
						expectedType = field.Type
						break
					}
				}
				// Check the value expression with context
				valueType := checkExpr(ctx, mod, kv.Value, expectedType)
				// Ensure numeric literals fit the expected field type.
				_ = checkFitness(ctx, expectedType, kv.Value, nil)
				inferredFields = append(inferredFields, types.StructField{
					Name: ident.Name,
					Type: valueType,
				})
				fieldExprs[ident.Name] = kv.Value
			}
		}
	}

	// Check for unknown fields (fields in literal but not in target struct)
	for _, elem := range lit.Elts {
		if kv, ok := elem.(*ast.KeyValueExpr); ok {
			if ident, ok := kv.Key.(*ast.IdentifierExpr); ok {
				found := false
				for _, field := range structType.Fields {
					if field.Name == ident.Name {
						found = true
						break
					}
				}
				if !found {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("extra field '.%s' in struct literal", ident.Name)).
							WithCode(diagnostics.ErrExtraField).
							WithPrimaryLabel(kv.Key.Loc(), "extra field"),
					)
				}
			}
		}
	}

	// Use analyzeStructCompatibility to find missing/mismatched fields
	inferredStruct := types.NewStruct("", inferredFields)
	missingFields, mismatchedFields := analyzeStructCompatibility(inferredStruct, structType)

	// Report type mismatches with source locations
	for _, mismatch := range mismatchedFields {
		// Parse field name from mismatch string "fieldName (expected X, found Y)"
		fieldName := strings.Split(mismatch, " ")[0]
		if expr, ok := fieldExprs[fieldName]; ok {
			// Find expected type
			var expectedType types.SemType
			for _, field := range structType.Fields {
				if field.Name == fieldName {
					expectedType = field.Type
					break
				}
			}
			valueType := inferExprType(ctx, mod, expr)
			compat := checkTypeCompatibility(valueType, expectedType)
			diag := diagnostics.NewError(fmt.Sprintf("cannot use type '%s' as type '%s' in field '.%s'", valueType.String(), expectedType.String(), fieldName)).
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(expr.Loc(), fmt.Sprintf("type '%s'", valueType.String()))
			diag = addExplicitCastHint(ctx, diag, expectedType, compat, expr)
			diag = addDerefHintIfNeeded(ctx, mod, diag, expectedType, valueType, expr)
			ctx.Diagnostics.Add(diag)
		}
	}

	// Report missing fields with note for details
	if note := formatStructCompatibilityNote(missingFields, nil, true); note != "" {
		typeName := originalType.String()
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("struct literal has missing %s", str.Pluralize("field", "fields", len(missingFields)))).
				WithCode(diagnostics.ErrMissingField).
				WithPrimaryLabel(lit.Loc(), fmt.Sprintf("type '%s'", typeName)).
				WithNote(note),
		)
	}

	return missingFields
}

// validateArrayLiteral validates that all array elements match the element type
func validateArrayLiteral(ctx *context_v2.CompilerContext, mod *context_v2.Module, lit *ast.CompositeLit, arrayType *types.ArrayType) {
	for _, elem := range lit.Elts {
		if _, isKV := elem.(*ast.KeyValueExpr); !isKV {
			if spread, ok := elem.(*ast.SpreadExpr); ok {
				// Spread element must be an array/slice of the element type.
				sliceType := types.NewArray(arrayType.Element, -1)
				spreadType := checkExpr(ctx, mod, spread.X, sliceType)
				spreadType = types.UnwrapType(spreadType)
				arrType, ok := spreadType.(*types.ArrayType)
				if !ok || arrType == nil {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("spread expression in array literal must be an array, found %s", spreadType.String())).
							WithCode(diagnostics.ErrTypeMismatch).
							WithPrimaryLabel(spread.Loc(), "expected array value"),
					)
					continue
				}
				if compat := checkTypeCompatibility(arrType.Element, arrayType.Element); !isImplicitlyCompatible(compat) {
					elemTypeStr := types.ResolveUntypedType(arrType.Element, types.TypeUnknown)
					diag := diagnostics.NewError(fmt.Sprintf("spread array element type must be %s but found %s", arrayType.Element.String(), elemTypeStr)).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(spread.Loc(), fmt.Sprintf("type %s", elemTypeStr)).
						WithHelp(fmt.Sprintf("spread arrays must contain %s elements", arrayType.Element.String()))
					diag = addExplicitCastHint(ctx, diag, arrayType.Element, compat, spread.X)
					diag = addDerefHintIfNeeded(ctx, mod, diag, arrayType.Element, arrType.Element, spread.X)
					ctx.Diagnostics.Add(diag)
				}
				continue
			}

			elemType := checkExpr(ctx, mod, elem, arrayType.Element)
			if isReferenceType(arrayType.Element) && !elemType.Equals(types.TypeUnknown) && !isReferenceType(elemType) {
				ctx.Diagnostics.Add(
					diagnostics.NewError("array element must be initialized with a reference").
						WithCode(diagnostics.ErrInvalidAssignment).
						WithPrimaryLabel(elem.Loc(), "expected a reference value").
						WithHelp("use '&' to bind the element"),
				)
			}
			if compat := checkTypeCompatibility(elemType, arrayType.Element); !isImplicitlyCompatible(compat) {
				elemTypeStr := types.ResolveUntypedType(elemType, types.TypeUnknown)
				diag := diagnostics.NewError(fmt.Sprintf("array elements must all be same type, expected %s but found %s", arrayType.Element.String(), elemTypeStr)).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(elem.Loc(), fmt.Sprintf("type %s", elemTypeStr)).
					WithHelp(fmt.Sprintf("all array elements must be %s", arrayType.Element.String()))
				diag = addExplicitCastHint(ctx, diag, arrayType.Element, compat, elem)
				diag = addDerefHintIfNeeded(ctx, mod, diag, arrayType.Element, elemType, elem)
				ctx.Diagnostics.Add(diag)
			}
		}
	}
}

// checkMapLiteral validates map literal key/value types
func checkMapLiteral(ctx *context_v2.CompilerContext, mod *context_v2.Module, lit *ast.CompositeLit, mapType *types.MapType) {
	// Validate that the key type is comparable
	if !types.IsMapKeyComparable(mapType.Key) {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("map key type '%s' is not comparable", mapType.Key.String())).
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(lit.Loc(), "map keys must be comparable").
				WithHelp("Comparable types: primitives (i32, f64, str, bool, etc.), structs with comparable fields, fixed arrays [N]T where T is comparable, pointers, and enums.\nNon-comparable: slices []T, maps, functions, interfaces, unions, optionals, results."),
		)
	}

	// Validate that all keys have compatible types with the map's key type
	// Validate that all values have compatible types with the map's value type
	for _, elem := range lit.Elts {
		kv, ok := elem.(*ast.KeyValueExpr)
		if !ok {
			// Non key-value elements in map literal (shouldn't happen with correct parsing)
			continue
		}

		// Check key type compatibility - with contextualization
		keyType := checkExpr(ctx, mod, kv.Key, mapType.Key)
		if isInterfaceType(mapType.Key) && !keyType.Equals(types.TypeUnknown) && !isInterfaceType(keyType) && !types.IsMapKeyComparable(keyType) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("map key type '%s' is not comparable", keyType.String())).
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(kv.Key.Loc(), "map keys must be comparable"),
			)
		}
		if compat := checkTypeCompatibility(keyType, mapType.Key); !isImplicitlyCompatible(compat) {
			keyTypeStr := types.ResolveUntypedType(keyType, types.TypeUnknown)
			diag := diagnostics.NewError(fmt.Sprintf("map keys must all be same type, expected %s but found %s", mapType.Key.String(), keyTypeStr)).
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(kv.Key.Loc(), fmt.Sprintf("type %s", keyTypeStr)).
				WithHelp(fmt.Sprintf("all map keys must be %s", mapType.Key.String()))
			diag = addExplicitCastHint(ctx, diag, mapType.Key, compat, kv.Key)
			diag = addDerefHintIfNeeded(ctx, mod, diag, mapType.Key, keyType, kv.Key)
			ctx.Diagnostics.Add(diag)
		}

		// Check value type compatibility - with contextualization
		valueType := checkExpr(ctx, mod, kv.Value, mapType.Value)
		if isReferenceType(mapType.Value) && !valueType.Equals(types.TypeUnknown) && !isReferenceType(valueType) {
			ctx.Diagnostics.Add(
				diagnostics.NewError("map value must be initialized with a reference").
					WithCode(diagnostics.ErrInvalidAssignment).
					WithPrimaryLabel(kv.Value.Loc(), "expected a reference value").
					WithHelp("use '&' to bind the value"),
			)
		}
		if compat := checkTypeCompatibility(valueType, mapType.Value); !isImplicitlyCompatible(compat) {
			valueTypeStr := types.ResolveUntypedType(valueType, types.TypeUnknown)
			diag := diagnostics.NewError(fmt.Sprintf("map values must all be same type, expected %s but found %s", mapType.Value.String(), valueTypeStr)).
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(kv.Value.Loc(), fmt.Sprintf("type %s", valueTypeStr)).
				WithHelp(fmt.Sprintf("all map values must be %s", mapType.Value.String()))
			diag = addExplicitCastHint(ctx, diag, mapType.Value, compat, kv.Value)
			diag = addDerefHintIfNeeded(ctx, mod, diag, mapType.Value, valueType, kv.Value)
			ctx.Diagnostics.Add(diag)
		}
	}
}

// areStructsCompatible checks if two struct types are structurally compatible
func areStructsCompatible(src, dst *types.StructType) bool {
	missing, mismatched := analyzeStructCompatibility(src, dst)
	return len(missing) == 0 && len(mismatched) == 0
}

// analyzeStructCompatibility checks struct compatibility and returns detailed mismatch info
// Returns:
//   - missingFields: list of field names that are required in dst but missing in src
//   - mismatchedFields: list of "fieldName (expected Type, found Type)" for type mismatches
func analyzeStructCompatibility(src, dst *types.StructType) (missingFields []string, mismatchedFields []string) {
	// Check if destination has all required fields with matching or compatible types
	for _, dstField := range dst.Fields {
		found := false
		for _, srcField := range src.Fields {
			if srcField.Name == dstField.Name {
				found = true
				// Check if types match or are compatible
				if srcField.Type.Equals(dstField.Type) {
					break // Exact match
				}
				// Allow untyped literals to match concrete types
				compatibility := checkTypeCompatibility(srcField.Type, dstField.Type)
				if !isImplicitlyCompatible(compatibility) {
					// Type mismatch
					mismatchedFields = append(mismatchedFields,
						fmt.Sprintf("%s (expected %s, found %s)", dstField.Name, dstField.Type.String(), srcField.Type.String()))
				}
				break
			}
		}
		if !found {
			// Missing required field
			missingFields = append(missingFields, dstField.Name)
		}
	}
	return missingFields, mismatchedFields
}

// formatStructCompatibilityNote formats a note string for struct compatibility issues
// Used by checkCastExpr, checkAssignLike, and validateStructLiteral
func formatStructCompatibilityNote(missingFields, mismatchedFields []string, addDotPrefix bool) string {
	if len(missingFields) == 0 && len(mismatchedFields) == 0 {
		return ""
	}

	var noteParts []string
	if len(missingFields) > 0 {
		displayFields := missingFields
		if addDotPrefix {
			displayFields = make([]string, len(missingFields))
			for i, f := range missingFields {
				displayFields[i] = "." + f
			}
		}
		noteParts = append(noteParts, fmt.Sprintf("missing %s: %s",
			str.Pluralize("field", "fields", len(missingFields)),
			strings.Join(displayFields, ", ")))
	}
	if len(mismatchedFields) > 0 {
		noteParts = append(noteParts, fmt.Sprintf("type mismatch in %s: %s",
			str.Pluralize("field", "fields", len(mismatchedFields)),
			strings.Join(mismatchedFields, ", ")))
	}
	return strings.Join(noteParts, "; ")
}

func explicitDerefBase(expr ast.Expression) *ast.DerefExpr {
	switch e := expr.(type) {
	case *ast.DerefExpr:
		return e
	case *ast.ParenExpr:
		return explicitDerefBase(e.X)
	default:
		return nil
	}
}

func checkSelectorExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.SelectorExpr) {
	// Infer the base type
	baseType := inferExprType(ctx, mod, expr.X)

	// Skip validation if base type is unknown (error already reported)
	if baseType.Equals(types.TypeUnknown) {
		return
	}

	// Auto-dereference for selector expressions (method calls and field access)
	// This allows: ref.field instead of (*ref).field
	baseType = autoDerefBaseType(expr, baseType)

	fieldName := expr.Field.Name

	if autoDerefAllowedExpr(expr) {
		if derefExpr := explicitDerefBase(expr.X); derefExpr != nil {
			operandType := inferExprType(ctx, mod, derefExpr.X)
			if isReferenceType(operandType) {
				help := "remove the explicit dereference on the base value"
				if ctx != nil && derefExpr.X != nil && derefExpr.X.Loc() != nil {
					baseText := derefExpr.X.Loc().GetText(ctx.Diagnostics.GetSourceCache())
					if baseText != "" {
						help = fmt.Sprintf("use %s.%s instead of (*%s).%s", baseText, fieldName, baseText, fieldName)
					}
				}
				ctx.Diagnostics.Add(
					diagnostics.NewWarning("explicit dereference is unnecessary for selector access").
						WithPrimaryLabel(derefExpr.Loc(), "auto-dereference applies here").
						WithHelp(help),
				)
			}
		}
	}

	// Check if baseType is an interface type (or NamedType wrapping an interface)
	var interfaceType *types.InterfaceType
	if iface, ok := baseType.(*types.InterfaceType); ok {
		interfaceType = iface
	} else if named, ok := baseType.(*types.NamedType); ok {
		if iface, ok := named.Underlying.(*types.InterfaceType); ok {
			interfaceType = iface
		}
	}

	// If it's an interface, check if the method exists in the interface
	if interfaceType != nil {
		for _, method := range interfaceType.Methods {
			if method.Name == fieldName {
				return // Method exists in interface
			}
		}
		// Method not found in interface
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("method '%s' not found in interface '%s'", fieldName, baseType.String())).
				WithCode(diagnostics.ErrFieldNotFound).
				WithPrimaryLabel(expr.Field.Loc(), fmt.Sprintf("'%s' does not exist in interface", fieldName)),
		)
		return
	}

	// Handle NamedType and anonymous StructType
	var typeSym *symbols.Symbol
	var structType *types.StructType
	var typeName string

	if namedType, ok := baseType.(*types.NamedType); ok {
		// It's a named type - look up the type symbol for method resolution
		typeName = namedType.Name
		if sym, found := lookupTypeSymbol(ctx, mod, typeName); found {
			typeSym = sym
		}

		// Get the underlying struct for field access
		underlying := types.UnwrapType(namedType)
		structType, _ = underlying.(*types.StructType)
	} else if st, ok := baseType.(*types.StructType); ok {
		// Anonymous struct - no methods, only fields
		structType = st
	} else {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("type '%s' has no fields or methods", baseType.String())).
				WithCode(diagnostics.ErrFieldNotFound).
				WithPrimaryLabel(expr.Field.Loc(), "invalid selector target"),
		)
		return
	}
	// Check if it's a field
	if structType != nil {
		for _, field := range structType.Fields {
			if field.Name == fieldName {
				// Check field visibility - private fields (lowercase) have restrictions
				if !utils.IsExported(fieldName) {
					// Private field - only accessible through receiver parameter
					// Simple check: if expr.X is an identifier that refers to a receiver symbol
					isReceiver := false
					if ident, ok := expr.X.(*ast.IdentifierExpr); ok {
						// Look up the identifier - search scope chain to find receiver
						// Walk up the parent chain starting from current scope
						currentScope := mod.CurrentScope
						for currentScope != nil {
							if sym, found := currentScope.GetSymbol(ident.Name); found {
								// Found in this scope - check if it's a receiver
								if sym.Kind == symbols.SymbolReceiver {
									isReceiver = true
									break
								}
								// Found but not a receiver - this shadows any receiver in parent scopes
								// So we stop searching
								break
							}
							// Not found in this scope, check parent
							currentScope = currentScope.Parent()
						}
					}

					if !isReceiver {
						ctx.Diagnostics.Add(
							diagnostics.NewError(fmt.Sprintf("field '%s' is private", fieldName)).
								WithPrimaryLabel(expr.Field.Loc(), fmt.Sprintf("cannot access private field '%s'", fieldName)).
								WithNote("private fields (lowercase) can only be accessed through the receiver in methods"),
						)
						return
					}
				}
				return // Field exists and is accessible
			}
		}
	}

	// Check if it's a method on the named type
	if typeSym != nil && typeSym.Methods != nil {
		if _, ok := typeSym.Methods[fieldName]; ok {
			return // Method exists
		}
	}

	// Field/method not found
	if typeName != "" {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("field or method '%s' not found on type '%s'", fieldName, typeName)).
				WithCode(diagnostics.ErrFieldNotFound).
				WithPrimaryLabel(expr.Field.Loc(), fmt.Sprintf("'%s' does not exist", fieldName)),
		)
	} else {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("field '%s' not found on anonymous struct", fieldName)).
				WithCode(diagnostics.ErrFieldNotFound).
				WithPrimaryLabel(expr.Field.Loc(), fmt.Sprintf("'%s' does not exist", fieldName)),
		)
	}
}

// checkMethodSignatureOnly processes only the method signature (receiver + parameters + return type)
// and attaches the method to its type. Does NOT check the body.
func checkMethodSignatureOnly(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.MethodDecl) {
	if ctx.Config.Debug {
		fmt.Printf("        [checkMethodSignatureOnly: %s]\n", decl.Name.Name)
	}

	// Get receiver type
	if decl.Receiver == nil || decl.Receiver.Type == nil {
		if ctx.Config.Debug {
			fmt.Printf("          [No receiver]\n")
		}
		return
	}

	receiverType := TypeFromTypeNodeWithContext(ctx, mod, decl.Receiver.Type)
	receiverTypeRaw := receiverType

	// Only process valid named types
	if receiverType.Equals(types.TypeUnknown) {
		return
	}

	// Unwrap reference types: &T -> T
	receiverType = types.DereferenceType(receiverType)

	// Extract type name - only NamedType can have methods
	var typeName string
	if namedType, ok := receiverType.(*types.NamedType); ok {
		typeName = namedType.Name
	} else {
		// Anonymous types cannot have methods
		return
	}

	if typeName == "" {
		return
	}

	// Find the type symbol - could be in current module or imported module
	typeSym, found := lookupTypeSymbol(ctx, mod, typeName)

	// Type check the method signature and attach to type symbol
	if found && typeSym.Kind == symbols.SymbolType && decl.Type != nil {
		funcType := TypeFromTypeNodeWithContext(ctx, mod, decl.Type).(*types.FunctionType)

		// Attach method to the type symbol's Methods map
		if typeSym.Methods == nil {
			typeSym.Methods = make(map[string]*symbols.MethodInfo)
		}

		// Create method info and attach to type symbol
		typeSym.Methods[decl.Name.Name] = &symbols.MethodInfo{
			Name:     decl.Name.Name,
			FuncType: funcType,
			Receiver: receiverTypeRaw,
			Exported: utils.IsExported(decl.Name.Name),
		}
	}
}

// checkMethodDecl type checks method body (signature already checked in phase 4a)
func checkMethodDecl(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.MethodDecl) {
	// Safety check: Scope should be set during collection phase
	if decl.Scope == nil {
		ctx.ReportError(fmt.Sprintf("internal error: method '%s' has nil scope", decl.Name.Name), decl.Loc())
		return
	}

	methodScope := decl.Scope.(*table.SymbolTable)

	// Set up method context (scope and return type)
	defer setupFunctionContext(ctx, mod, methodScope, decl.Type)()

	// Add receiver to method scope
	if decl.Receiver != nil && decl.Receiver.Name != nil {
		receiverSym, ok := methodScope.GetSymbol(decl.Receiver.Name.Name)
		if ok && decl.Receiver.Type != nil {
			receiverType := TypeFromTypeNodeWithContext(ctx, mod, decl.Receiver.Type)
			receiverSym.Type = receiverType
		}
	}

	// Add parameters to the method scope with type information
	if decl.Type != nil {
		addParamsToScope(ctx, mod, methodScope, decl.Type.Params)
	}

	// Check the body with the method scope
	if decl.Body != nil {
		checkBlock(ctx, mod, decl.Body)
		// Return path analysis is done in control flow analysis phase, not here
	}
}

// checkTypeDecl fills in the type for user-defined type declarations
func checkTypeDecl(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *ast.TypeDecl) {
	if decl.Name == nil || decl.Type == nil {
		return
	}

	typeName := decl.Name.Name

	// Look up the type symbol (created during collection)
	sym, ok := mod.CurrentScope.Lookup(typeName)
	if !ok {
		// Symbol should exist from collection phase
		ctx.ReportError(fmt.Sprintf("internal error: type symbol '%s' not found", typeName), decl.Loc())
		return
	}

	// Convert the AST type node to a semantic type
	// Predeclare a NamedType so recursive references can resolve.
	// The underlying type is filled in after validation below.
	namedType := types.NewNamed(typeName, types.TypeUnknown)

	// Update the symbol's type
	sym.Type = namedType

	// Convert the AST type node to a semantic type
	semType := TypeFromTypeNodeWithContext(ctx, mod, decl.Type)

	if hasInvalidRecursiveType(namedType, semType, make(map[types.SemType]bool)) {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("recursive type '%s' must be optional", typeName)).
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(decl.Name.Loc(), "recursive type definition").
				WithHelp(fmt.Sprintf("use '%s?' to break the cycle", typeName)),
		)
		// Keep the placeholder to avoid infinite recursion in later phases.
		namedType.Underlying = types.TypeUnknown
		return
	}

	// Fill in the underlying type after validation.
	namedType.Underlying = semType

	// Special handling for enums: compute variant values and update variant symbols
	if enumNode, ok := decl.Type.(*ast.EnumType); ok {
		// Current value tracker for sequential numbering
		currentValue := int64(0)

		for _, variant := range enumNode.Variants {
			variantName := variant.Name.Name
			qualifiedName := typeName + "::" + variantName

			if variant.Value != nil {
				reportExplicitEnumValue(ctx, variantName, variant.Value.Loc())
			}

			// Look up the variant symbol (created during collection phase)
			variantSym, ok := mod.ModuleScope.GetSymbol(qualifiedName)
			if !ok {
				ctx.ReportError(fmt.Sprintf("internal error: enum variant symbol '%s' not found", qualifiedName), variant.Name.Loc())
				continue
			}

			// Update the variant symbol's type to the named enum type
			variantSym.Type = namedType
			variantSym.ConstValue = consteval.NewIntValue(currentValue, namedType)

			// Increment for next variant (sequential numbering)
			currentValue++
		}
	}
}

func hasInvalidRecursiveType(root *types.NamedType, t types.SemType, seen map[types.SemType]bool) bool {
	if root == nil || t == nil {
		return false
	}
	if seen[t] {
		return false
	}
	seen[t] = true

	switch tt := t.(type) {
	case *types.NamedType:
		if tt == root {
			return true
		}
		return hasInvalidRecursiveType(root, tt.Underlying, seen)
	case *types.ReferenceType:
		return hasInvalidRecursiveType(root, tt.Inner, seen)
	case *types.MapType, *types.FunctionType, *types.InterfaceType:
		return false
	case *types.ArrayType:
		if tt.Length < 0 {
			return false
		}
		return hasInvalidRecursiveType(root, tt.Element, seen)
	case *types.OptionalType:
		return false
	case *types.ResultType:
		return hasInvalidRecursiveType(root, tt.Ok, seen) || hasInvalidRecursiveType(root, tt.Err, seen)
	case *types.StructType:
		for _, field := range tt.Fields {
			if hasInvalidRecursiveType(root, field.Type, seen) {
				return true
			}
		}
		return false
	case *types.UnionType:
		// Unions CAN contain recursive references through safe indirections
		// (arrays, maps, optionals), but NOT direct self-reference
		// Example: union { T1, T2, []A } is OK
		// Example: union { T1, T2, A } is NOT OK (direct recursion)
		for _, variant := range tt.Variants {
			// Check if this variant is the root type itself (direct recursion)
			unwrapped := types.UnwrapType(variant)
			if namedVariant, ok := unwrapped.(*types.NamedType); ok && namedVariant == root {
				// Direct recursion: union contains itself directly
				return true
			}
			// Otherwise, allow recursive references through safe types (arrays, maps, etc.)
			// Don't recurse further - we only check direct containment
		}
		return false
	case *types.EnumType:
		for _, variant := range tt.Variants {
			if variant.Type != nil && hasInvalidRecursiveType(root, variant.Type, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// checkAssignStmt type checks an assignment statement
func checkAssignStmt(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *ast.AssignStmt) {
	// Handle blank identifier
	if ident, ok := stmt.Lhs.(*ast.IdentifierExpr); ok && ident.Name == "_" {
		if stmt.Rhs != nil {
			checkExpr(ctx, mod, stmt.Rhs, types.TypeUnknown)
		}
		return
	}

	// Use unified mutability checking system
	mutInfo := checkMutability(ctx, mod, stmt.Lhs)
	ignoreImmutableRef := false
	if mutInfo.Result == MutabilityImmutableRef && (stmt.Op == nil || stmt.Op.Kind == tokens.EQUALS_TOKEN) {
		if ident, ok := stmt.Lhs.(*ast.IdentifierExpr); ok && mod != nil && mod.CurrentScope != nil {
			if sym, ok := mod.CurrentScope.Lookup(ident.Name); ok {
				if _, ok := types.UnwrapType(sym.Type).(*types.ReferenceType); ok {
					ignoreImmutableRef = true
				}
			}
		}
	}
	if !ignoreImmutableRef && reportMutabilityError(ctx, mutInfo, stmt.Lhs) {
		// Error reported, but continue checking RHS for additional errors
		if stmt.Rhs != nil {
			checkExpr(ctx, mod, stmt.Rhs, types.TypeUnknown)
		}
		return
	}

	// Get the type of the LHS
	lhsType := checkExpr(ctx, mod, stmt.Lhs, types.TypeUnknown)
	lhsIsRef := isReferenceType(lhsType)
	lhsRef, _ := types.UnwrapType(lhsType).(*types.ReferenceType)
	if !lhsType.Equals(types.TypeUnknown) && !isAssignableTarget(ctx, mod, stmt.Lhs) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("invalid assignment target").
				WithCode(diagnostics.ErrInvalidAssignment).
				WithPrimaryLabel(stmt.Lhs.Loc(), "cannot assign to this expression").
				WithHelp("assignments require a variable, field, or index on an addressable value"),
		)
		if stmt.Rhs != nil {
			checkExpr(ctx, mod, stmt.Rhs, types.TypeUnknown)
		}
		return
	}

	if stmt.Rhs != nil {
		if heapUnaryExpr(stmt.Rhs) != nil {
			if stmt.Op != nil && stmt.Op.Kind != tokens.EQUALS_TOKEN {
				ctx.Diagnostics.Add(
					diagnostics.NewError("heap allocation cannot be used with compound assignment").
						WithCode(diagnostics.ErrInvalidAssignment).
						WithPrimaryLabel(stmt.Lhs.Loc(), "use '=' to bind heap storage"),
				)
				checkExpr(ctx, mod, stmt.Rhs, types.TypeUnknown)
				return
			}
			ident, ok := stmt.Lhs.(*ast.IdentifierExpr)
			if !ok {
				ctx.Diagnostics.Add(
					diagnostics.NewError("heap allocation must bind to a variable").
						WithCode(diagnostics.ErrInvalidAssignment).
						WithPrimaryLabel(stmt.Lhs.Loc(), "expected a variable name"),
				)
				checkExpr(ctx, mod, stmt.Rhs, types.TypeUnknown)
				return
			}
			if sym, ok := mod.CurrentScope.Lookup(ident.Name); ok && sym != nil {
				sym.IsHeap = true
			}
		}
	}

	assignType := lhsType
	isMapIndex := false
	if idx, ok := stmt.Lhs.(*ast.IndexExpr); ok {
		baseType := inferExprType(ctx, mod, idx.X)
		baseType = types.UnwrapType(baseType)
		if ref, ok := baseType.(*types.ReferenceType); ok {
			baseType = types.UnwrapType(ref.Inner)
		}
		if mapType, ok := baseType.(*types.MapType); ok && mapType.Value != nil {
			assignType = mapType.Value
			isMapIndex = true
		}
	}

	// Handle increment/decrement operators (x++, x--)
	if stmt.Op != nil && (stmt.Op.Kind == tokens.PLUS_PLUS_TOKEN || stmt.Op.Kind == tokens.MINUS_MINUS_TOKEN) {
		if lhsIsRef {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot use %s on a reference", stmt.Op.Value)).
					WithPrimaryLabel(stmt.Lhs.Loc(), "explicit deref required").
					WithHelp(fmt.Sprintf("dereference the target first: (*%s)%s", stmt.Lhs.Loc().GetText(ctx.Diagnostics.GetSourceCache()), stmt.Op.Value)),
			)
			return
		}
		// For ++ and --, RHS is nil
		// Check that LHS is a numeric type
		incDecType := types.UnwrapType(lhsType)
		if !types.IsNumeric(incDecType) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot use %s operator on non-numeric type '%s'", stmt.Op.Value, incDecType.String())).
					WithPrimaryLabel(stmt.Lhs.Loc(), "expected numeric type").
					WithHelp("increment/decrement operators only work on numeric types"),
			)
			return
		}
		return
	}

	// Handle compound assignment operators (x += y, x -= y, etc.)
	if stmt.Op != nil && stmt.Op.Kind != tokens.EQUALS_TOKEN {
		if lhsIsRef {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot use %s on a reference", stmt.Op.Value)).
					WithPrimaryLabel(stmt.Lhs.Loc(), "explicit deref required").
					WithHelp(fmt.Sprintf("dereference the target first: (*%s)%s", stmt.Lhs.Loc().GetText(ctx.Diagnostics.GetSourceCache()), stmt.Op.Value)),
			)
			if stmt.Rhs != nil {
				checkExpr(ctx, mod, stmt.Rhs, types.TypeUnknown)
			}
			return
		}
		// For compound assignments, we need to check that the operation is valid
		// The RHS should be compatible with the operation
		rhsType := checkExpr(ctx, mod, stmt.Rhs, types.TypeUnknown)
		if lhsIsRef {
			if !rhsType.Equals(types.TypeUnknown) && isMapIndex {
				rhsRef, ok := types.UnwrapType(rhsType).(*types.ReferenceType)
				if !ok {
					ctx.Diagnostics.Add(
						diagnostics.NewError("map value is a reference and must be assigned with '&'").
							WithCode(diagnostics.ErrInvalidAssignment).
							WithPrimaryLabel(stmt.Rhs.Loc(), "expected a reference value"),
					)
					return
				}
				if lhsRef != nil && lhsRef.Mutable && !rhsRef.Mutable {
					ctx.Diagnostics.Add(
						diagnostics.NewError("map value is a mutable reference and must be assigned with \"&'\"").
							WithCode(diagnostics.ErrInvalidAssignment).
							WithPrimaryLabel(stmt.Rhs.Loc(), "expected a mutable reference"),
					)
					return
				}
			} else if !rhsType.Equals(types.TypeUnknown) && isMapIndex && !isReferenceType(rhsType) {
				ctx.Diagnostics.Add(
					diagnostics.NewError("map value is a reference and must be assigned with '&'").
						WithCode(diagnostics.ErrInvalidAssignment).
						WithPrimaryLabel(stmt.Rhs.Loc(), "expected a reference value"),
				)
				return
			}
		}

		// Check if the operation is valid for these types
		opKind := stmt.Op.Kind
		var requiredOp tokens.TOKEN
		switch opKind {
		case tokens.PLUS_EQUALS_TOKEN:
			requiredOp = tokens.PLUS_TOKEN
		case tokens.MINUS_EQUALS_TOKEN:
			requiredOp = tokens.MINUS_TOKEN
		case tokens.MUL_EQUALS_TOKEN:
			requiredOp = tokens.MUL_TOKEN
		case tokens.DIV_EQUALS_TOKEN:
			requiredOp = tokens.DIV_TOKEN
		case tokens.MOD_EQUALS_TOKEN:
			requiredOp = tokens.MOD_TOKEN
		case tokens.EXP_EQUALS_TOKEN:
			requiredOp = tokens.BIT_XOR_TOKEN // ^= is bitwise XOR
		case tokens.POW_EQUALS_TOKEN:
			requiredOp = tokens.EXP_TOKEN // **= is power
		default:
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("unsupported compound assignment operator '%s'", stmt.Op.Value)).
					WithPrimaryLabel(stmt.Lhs.Loc(), "unknown operator"),
			)
			return
		}

		// Create a temporary binary expression to check type compatibility
		// Use the operator token from the assignment statement for proper location info
		lhsEnd := stmt.Lhs.Loc().End
		rhsStart := stmt.Rhs.Loc().Start
		if lhsEnd == nil {
			lhsEnd = stmt.Lhs.Loc().Start
		}
		if rhsStart == nil {
			rhsStart = stmt.Rhs.Loc().End
		}
		opToken := tokens.Token{
			Kind:  requiredOp,
			Value: string(requiredOp),
			Start: *lhsEnd,
			End:   *rhsStart,
		}
		tempBinExpr := &ast.BinaryExpr{
			X:        stmt.Lhs,
			Op:       opToken,
			Y:        stmt.Rhs,
			Location: stmt.Location,
		}
		// Check the binary expression to validate types
		checkBinaryExpr(ctx, mod, tempBinExpr, assignType, rhsType)
	} else {
		// Regular assignment: Check the RHS with the LHS type as context
		if lhsIsRef {
			rhsType := inferExprType(ctx, mod, stmt.Rhs)
			if !rhsType.Equals(types.TypeUnknown) && isMapIndex {
				rhsRef, ok := types.UnwrapType(rhsType).(*types.ReferenceType)
				if !ok {
					ctx.Diagnostics.Add(
						diagnostics.NewError("map value is a reference and must be assigned with '&'").
							WithCode(diagnostics.ErrInvalidAssignment).
							WithPrimaryLabel(stmt.Rhs.Loc(), "expected a reference value"),
					)
					checkExpr(ctx, mod, stmt.Rhs, assignType)
					return
				}
				if lhsRef != nil && lhsRef.Mutable && !rhsRef.Mutable {
					ctx.Diagnostics.Add(
						diagnostics.NewError("map value is a mutable reference and must be assigned with \"&'\"").
							WithCode(diagnostics.ErrInvalidAssignment).
							WithPrimaryLabel(stmt.Rhs.Loc(), "expected a mutable reference"),
					)
					checkExpr(ctx, mod, stmt.Rhs, assignType)
					return
				}
			} else if !rhsType.Equals(types.TypeUnknown) && isMapIndex && !isReferenceType(rhsType) {
				ctx.Diagnostics.Add(
					diagnostics.NewError("map value is a reference and must be assigned with '&'").
						WithCode(diagnostics.ErrInvalidAssignment).
						WithPrimaryLabel(stmt.Rhs.Loc(), "expected a reference value"),
				)
				checkExpr(ctx, mod, stmt.Rhs, assignType)
				return
			}
		}
		checkAssignLike(ctx, mod, assignType, stmt.Lhs, stmt.Rhs)
	}
}

// checkDeferStmt type checks a defer statement
// Defer must be a function call, and catch blocks are diagnostic-only
func checkDeferStmt(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *ast.DeferStmt) {
	// Track this defer statement for code generation (LIFO execution)
	mod.CurrentDeferredStmts = append(mod.CurrentDeferredStmts, stmt)

	// Set flag to skip Result validation (defer allows Result without catch)
	mod.InDeferContext = true
	defer func() { mod.InDeferContext = false }()

	// Type check the deferred call (note: stmt.Call.Catch is always nil after parsing)
	callType := checkExpr(ctx, mod, stmt.Call, types.TypeUnknown)

	// Now handle the catch clause specially for defer (it's stored in stmt.Catch, not stmt.Call.Catch)
	if stmt.Catch != nil {
		// Get the function's return type to determine error type
		funType := inferExprType(ctx, mod, stmt.Call.Fun)
		funcType, ok := funType.(*types.FunctionType)
		if !ok {
			// Error already reported
			return
		}

		resultType, isResult := funcType.Return.(*types.ResultType)
		if !isResult {
			// Function doesn't return an error type, but catch was provided
			ctx.Diagnostics.Add(
				diagnostics.NewError("catch clause requires a function that may fail").
					WithCode(diagnostics.ErrInvalidCatch).
					WithPrimaryLabel(stmt.Catch.Loc(), fmt.Sprintf("function returns %s, not an error type", callType.String())).
					WithHelp("remove the catch clause or call a function that returns a Result type"),
			)
			return
		}

		// Validate catch clause
		if stmt.Catch.Handler != nil {
			// Get the scope of the catch block
			scope := stmt.Catch.Handler.Scope.(*table.SymbolTable)
			defer mod.EnterScope(scope)()

			// Set the catch error identifier type to the error type
			if stmt.Catch.ErrIdent != nil {
				if sym, ok := mod.CurrentScope.Lookup(stmt.Catch.ErrIdent.Name); ok {
					sym.Type = resultType.Err
				}
			}

			// Check that the handler block doesn't contain return statements
			validateDeferCatchHandler(ctx, mod, stmt.Catch.Handler)

			// Check the catch block
			checkBlock(ctx, mod, stmt.Catch.Handler)
		}

		// Enforce diagnostic-only catch: no fallback allowed in defer
		if stmt.Catch.Fallback != nil {
			ctx.Diagnostics.Add(
				diagnostics.NewError("defer catch cannot have a fallback value").
					WithCode(diagnostics.ErrInvalidDefer).
					WithPrimaryLabel(stmt.Catch.Fallback.Loc(), "fallback not allowed in defer catch").
					WithHelp("defer catch is diagnostic-only; use it only for logging or cleanup"),
			)
		}
	}

	// Defer itself has type void (doesn't produce a value)
	// This is already enforced by the statement-level context
}

func validateDeferCatchHandler(ctx *context_v2.CompilerContext, mod *context_v2.Module, node ast.Node) {
	switch n := node.(type) {
	case *ast.ReturnStmt:
		// Allow void return to exit catch block early
		if n.Result != nil {
			ctx.Diagnostics.Add(
				diagnostics.NewError("return with value not allowed in defer catch").
					WithCode(diagnostics.ErrInvalidDefer).
					WithPrimaryLabel(n.Loc(), "cannot return value from defer catch").
					WithHelp("defer catch is diagnostic-only; use void return to exit early"),
			)
		}

	case *ast.Block:
		for _, child := range n.Nodes {
			validateDeferCatchHandler(ctx, mod, child)
		}

	case *ast.IfStmt:
		validateDeferCatchHandler(ctx, mod, n.Body)
		if n.Else != nil {
			validateDeferCatchHandler(ctx, mod, n.Else)
		}

	case *ast.ForStmt:
		validateDeferCatchHandler(ctx, mod, n.Body)

	case *ast.WhileStmt:
		validateDeferCatchHandler(ctx, mod, n.Body)
	}
}

func isAssignableTarget(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression) bool {
	if expr == nil {
		return false
	}

	switch e := expr.(type) {
	case *ast.IdentifierExpr:
		return true
	case *ast.ScopeResolutionExpr:
		sym, ok := resolveScopeResolutionSymbol(ctx, mod, e)
		if !ok || sym == nil {
			return false
		}
		switch sym.Kind {
		case symbols.SymbolVariable, symbols.SymbolConstant:
			return true
		default:
			return false
		}
	case *ast.DerefExpr:
		// Dereferenced references are assignable (*ref = value)
		return true
	case *ast.ParenExpr:
		return isAssignableTarget(ctx, mod, e.X)
	case *ast.SelectorExpr:
		baseType := inferExprType(ctx, mod, e.X)
		if isReferenceType(baseType) {
			return isAssignableTarget(ctx, mod, e.X)
		}
		return isAssignableTarget(ctx, mod, e.X)
	case *ast.IndexExpr:
		baseType := inferExprType(ctx, mod, e.X)
		baseType = types.UnwrapType(baseType)
		if _, ok := baseType.(*types.MapType); ok {
			return isAssignableTarget(ctx, mod, e.X)
		}
		if isReferenceType(baseType) {
			return isAssignableTarget(ctx, mod, e.X)
		}
		return isAssignableTarget(ctx, mod, e.X)
	default:
		return false
	}
}

func isBorrowableTarget(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression) bool {
	if expr == nil {
		return false
	}

	switch e := expr.(type) {
	case *ast.IdentifierExpr:
		// Const variables CAN be borrowed with shared references (&)
		// Mutable reference (&mut) is checked separately in checkUnaryExpr
		// So const variables are borrowable here
		return true
	case *ast.ScopeResolutionExpr:
		sym, ok := resolveScopeResolutionSymbol(ctx, mod, e)
		if !ok || sym == nil {
			return false
		}
		switch sym.Kind {
		case symbols.SymbolVariable, symbols.SymbolConstant:
			return true
		default:
			return false
		}
	case *ast.ParenExpr:
		return isBorrowableTarget(ctx, mod, e.X)
	case *ast.SelectorExpr:
		return isBorrowableTarget(ctx, mod, e.X)
	case *ast.DerefExpr:
		baseType := inferExprType(ctx, mod, e.X)
		if baseType == nil || baseType.Equals(types.TypeUnknown) {
			return false
		}
		baseType = types.UnwrapType(baseType)
		_, ok := baseType.(*types.ReferenceType)
		return ok
	case *ast.IndexExpr:
		baseType := inferExprType(ctx, mod, e.X)
		if baseType == nil || baseType.Equals(types.TypeUnknown) {
			return false
		}
		baseType = autoDerefBaseType(e, baseType)
		if _, ok := baseType.(*types.MapType); ok {
			return isBorrowableTarget(ctx, mod, e.X)
		}
		if _, ok := baseType.(*types.ArrayType); ok {
			return isBorrowableTarget(ctx, mod, e.X)
		}
		return false
	default:
		return false
	}
}

func checkBorrowExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.UnaryExpr, operandType types.SemType) {
	if ctx == nil || mod == nil || expr == nil {
		return
	}
	if expr.Op.Kind != tokens.BIT_AND_TOKEN && expr.Op.Kind != tokens.MUT_TOKEN {
		return
	}
	if operandType == nil || operandType.Equals(types.TypeUnknown) {
		return
	}
	if isReferenceType(operandType) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot take reference of a reference").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.Loc(), "nested references are not allowed"),
		)
		return
	}
	if expr.Op.Kind == tokens.MUT_TOKEN {
		switch target := expr.X.(type) {
		case *ast.IdentifierExpr:
			if sym, found := mod.CurrentScope.Lookup(target.Name); found {
				if sym.Kind == symbols.SymbolConstant || sym.IsReadonly {
					ctx.Diagnostics.Add(
						diagnostics.NewError("cannot take mutable reference of a read-only value").
							WithCode(diagnostics.ErrInvalidOperation).
							WithPrimaryLabel(expr.X.Loc(), "value is not mutable"),
					)
					return
				}
			}
		case *ast.ScopeResolutionExpr:
			if sym, ok := resolveScopeResolutionSymbol(ctx, mod, target); ok && sym != nil {
				if sym.Kind == symbols.SymbolConstant || sym.IsReadonly {
					ctx.Diagnostics.Add(
						diagnostics.NewError("cannot take mutable reference of a read-only value").
							WithCode(diagnostics.ErrInvalidOperation).
							WithPrimaryLabel(expr.X.Loc(), "value is not mutable"),
					)
					return
				}
			}
		}
	}
	if !isBorrowableTarget(ctx, mod, expr.X) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot take reference of this expression").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.X.Loc(), "not an addressable value").
				WithHelp("borrow a variable, dereferenced reference, field, array element, or map element"),
		)
	}
}

func resolveScopeResolutionSymbol(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.ScopeResolutionExpr) (*symbols.Symbol, bool) {
	if ctx == nil || mod == nil || expr == nil || expr.Selector == nil {
		return nil, false
	}
	ident, ok := expr.X.(*ast.IdentifierExpr)
	if !ok || ident == nil {
		return nil, false
	}
	leftName := ident.Name
	rightName := expr.Selector.Name
	if leftName == "" || rightName == "" {
		return nil, false
	}
	if typeSym, ok := mod.CurrentScope.Lookup(leftName); ok && typeSym.Kind == symbols.SymbolType {
		return nil, false
	}
	importPath, ok := mod.ImportAliasMap[leftName]
	if !ok {
		return nil, false
	}
	importedMod, exists := ctx.GetModule(importPath)
	if !exists {
		return nil, false
	}
	sym, ok := importedMod.ModuleScope.GetSymbol(rightName)
	if !ok {
		return nil, false
	}
	return sym, true
}

func checkMoveExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.UnaryExpr, operandType types.SemType) {
	if ctx == nil || mod == nil || expr == nil {
		return
	}
	if expr.Op.Kind != tokens.AT_TOKEN {
		return
	}
	var (
		sym           *symbols.Symbol
		found         bool
		symName       string
		isModuleScope bool
	)
	switch target := expr.X.(type) {
	case *ast.IdentifierExpr:
		symName = target.Name
		sym, found = mod.CurrentScope.Lookup(target.Name)
		if !found || sym == nil {
			return
		}
	case *ast.ScopeResolutionExpr:
		sym, found = resolveScopeResolutionSymbol(ctx, mod, target)
		if !found || sym == nil {
			return
		}
		symName = sym.Name
		isModuleScope = true
	default:
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot move non-lvalue").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.X.Loc(), "expected a variable name").
				WithHelp("use '@name' to move from a binding"),
		)
		return
	}
	if sym.Kind == symbols.SymbolConstant || sym.IsReadonly {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot move from %s '%s'", sym.Kind.String(), symName)).
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.X.Loc(), "value is read-only"),
		)
		return
	}
	if sym.Kind != symbols.SymbolVariable && sym.Kind != symbols.SymbolParameter && sym.Kind != symbols.SymbolReceiver {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot move from %s '%s'", sym.Kind.String(), symName)).
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.X.Loc(), "not a movable binding").
				WithHelp("move only local variables or parameters"),
		)
		return
	}
	if isModuleScope || (mod.ModuleScope != nil && sym.DeclaredScope == mod.ModuleScope) {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot move from module-level binding '%s'", symName)).
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.X.Loc(), "module scope values cannot be moved"),
		)
		return
	}
	if operandType != nil && !operandType.Equals(types.TypeUnknown) && isReferenceType(operandType) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot move from reference").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.X.Loc(), "reference values are not movable"),
		)
	}
}

func checkHeapExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.UnaryExpr, operandType types.SemType) {
	if ctx == nil || mod == nil || expr == nil {
		return
	}
	if expr.Op.Kind != tokens.HASH_TOKEN {
		return
	}
	if operandType == nil || operandType.Equals(types.TypeUnknown) {
		return
	}
	operandType = types.UnwrapType(operandType)
	if arr, ok := operandType.(*types.ArrayType); ok && arr.Length < 0 {
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot heap allocate dynamic array").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.Loc(), "dynamic arrays are already heap-allocated").
				WithHelp("remove '#' from the value"),
		)
		return
	}
	if _, ok := operandType.(*types.MapType); ok {
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot heap allocate map").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.Loc(), "maps are already heap-allocated").
				WithHelp("remove '#' from the value"),
		)
		return
	}
	if prim, ok := operandType.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
		ctx.Diagnostics.Add(
			diagnostics.NewError("cannot heap allocate string").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.Loc(), "strings are already heap-allocated").
				WithHelp("remove '#' from the value"),
		)
	}
}

func heapUnaryExpr(expr ast.Expression) *ast.UnaryExpr {
	for expr != nil {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.X
		case *ast.CastExpr:
			expr = e.X
		case *ast.UnaryExpr:
			if e.Op.Kind == tokens.HASH_TOKEN {
				return e
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

func isReferenceType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	if _, ok := typ.(*types.ReferenceType); ok {
		return true
	}
	if _, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		return true
	}
	return false
}

func reportExplicitEnumValue(ctx *context_v2.CompilerContext, name string, loc *source.Location) {
	if ctx == nil || loc == nil {
		return
	}
	label := "explicit enum values are not supported"
	if name != "" {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("enum variant '%s' cannot have an explicit value", name)).
				WithPrimaryLabel(loc, label).
				WithHelp("remove the '= value' and rely on auto-assigned tags"),
		)
		return
	}
	ctx.Diagnostics.Add(
		diagnostics.NewError("enum variants cannot have explicit values").
			WithPrimaryLabel(loc, label).
			WithHelp("remove the '= value' and rely on auto-assigned tags"),
	)
}

func checkIncDecTarget(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression, target ast.Expression, targetType types.SemType, op tokens.Token) types.SemType {
	// Use unified mutability checking system
	mutInfo := checkMutability(ctx, mod, target)
	if reportMutabilityError(ctx, mutInfo, target) {
		return targetType
	}

	if targetType == nil || targetType.Equals(types.TypeUnknown) {
		return targetType
	}

	// Also check the targetType for direct immutable reference (for cases like dereferenced refs)
	if ref, ok := types.UnwrapType(targetType).(*types.ReferenceType); ok {
		if !ref.Mutable {
			ctx.Diagnostics.Add(
				diagnostics.NewError("cannot modify through immutable reference").
					WithCode(diagnostics.ErrInvalidAssignment).
					WithPrimaryLabel(target.Loc(), "immutable reference"),
			)
			return targetType
		}
	}

	targetType = types.UnwrapType(targetType)
	if _, ok := targetType.(*types.ReferenceType); ok {
		if !autoDerefAllowedExpr(expr) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot use %s on a reference", op.Value)).
					WithPrimaryLabel(target.Loc(), "explicit deref required").
					WithHelp(fmt.Sprintf("dereference the target first: (*%s)%s", target.Loc().GetText(ctx.Diagnostics.GetSourceCache()), op.Value)),
			)
			return targetType
		}
		refType := targetType.(*types.ReferenceType)
		targetType = refType.Inner
	}
	if !types.IsNumeric(targetType) {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot use %s operator on non-numeric type '%s'", op.Value, targetType.String())).
				WithPrimaryLabel(target.Loc(), "expected numeric type").
				WithHelp("increment/decrement operators only work on numeric types"),
		)
	}
	return targetType
}

func checkUnaryOp(ctx *context_v2.CompilerContext, expr *ast.UnaryExpr, operandType types.SemType) {
	if ctx == nil || expr == nil || operandType == nil {
		return
	}

	operandBase := types.UnwrapType(operandType)
	if ref, ok := operandBase.(*types.ReferenceType); ok {
		if unaryOpAllowsType(expr.Op.Kind, ref.Inner) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot use %s on a reference", expr.Op.Value)).
					WithPrimaryLabel(expr.Loc(), "explicit deref required").
					WithHelp(fmt.Sprintf("dereference the value first: %s*value", expr.Op.Value)),
			)
			return
		}
	}

	switch expr.Op.Kind {
	case tokens.NOT_TOKEN:
		if !operandBase.Equals(types.TypeBool) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot use %s on type '%s'", expr.Op.Value, operandBase.String())).
					WithPrimaryLabel(expr.Loc(), "expected bool type").
					WithHelp("logical not requires a bool operand"),
			)
		}
	case tokens.BIT_NOT_TOKEN:
		if !types.IsInteger(operandBase) && !types.IsUntypedInt(operandBase) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot use %s on type '%s'", expr.Op.Value, operandBase.String())).
					WithPrimaryLabel(expr.Loc(), "expected integer type").
					WithHelp("bitwise not requires an integer operand"),
			)
		}
	case tokens.PLUS_TOKEN, tokens.MINUS_TOKEN:
		if !types.IsNumericType(operandBase) && !types.IsUntyped(operandBase) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot use %s on type '%s'", expr.Op.Value, operandBase.String())).
					WithPrimaryLabel(expr.Loc(), "expected numeric type").
					WithHelp("unary +/- operators only work on numeric types"),
			)
		}
	}
}

func unaryOpAllowsType(op tokens.TOKEN, typ types.SemType) bool {
	if typ == nil {
		return false
	}
	base := types.UnwrapType(typ)
	switch op {
	case tokens.NOT_TOKEN:
		return base.Equals(types.TypeBool)
	case tokens.BIT_NOT_TOKEN:
		return types.IsInteger(base) || types.IsUntypedInt(base)
	case tokens.PLUS_TOKEN, tokens.MINUS_TOKEN:
		return types.IsNumericType(base) || types.IsUntyped(base)
	default:
		return false
	}
}

// checkBlock type checks a block of statements
func checkBlock(ctx *context_v2.CompilerContext, mod *context_v2.Module, block *ast.Block) {
	if block == nil {
		return
	}

	// Enter block scope if it exists
	// Some blocks (like function body) use their parent scope
	if block.Scope != nil {
		scope := block.Scope.(*table.SymbolTable)
		defer mod.EnterScope(scope)()
	}

	for _, node := range block.Nodes {
		checkNode(ctx, mod, node)
	}
}

// checkExpr type checks an expression and returns its type.
// expected provides contextual type information (TypeUnknown if no context).
// This function now uses the separate inference and compatibility modules.
func checkExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression, expected types.SemType) types.SemType {
	if expr == nil {
		return types.TypeUnknown
	}

	// Recursively validate subexpressions before type inference
	switch e := expr.(type) {
	case *ast.TypeExpr:
		// TypeExpr wraps a type node for use in expression context (e.g., 'is' operator)
		// Return the semantic type directly
		semType := TypeFromTypeNodeWithContext(ctx, mod, e.Type)
		mod.SetExprType(expr, semType)
		return semType
	case *ast.IdentifierExpr:
		checkModuleScopeUseBeforeDecl(ctx, mod, e)
		sym, ok := mod.CurrentScope.Lookup(e.Name)
		if ok && sym != nil {
			mod.SetExprType(expr, sym.Type)
			return sym.Type
		}
		return types.TypeUnknown
	case *ast.EnumType:
		for _, variant := range e.Variants {
			if variant.Value != nil {
				name := ""
				if variant.Name != nil {
					name = variant.Name.Name
				}
				reportExplicitEnumValue(ctx, name, variant.Value.Loc())
			}
		}
	case *ast.CallExpr:
		checkCallExpr(ctx, mod, e)
		// Validate catch clause if present
		if e.Catch != nil {
			checkCatchClause(ctx, mod, e)
		}
		// Compute return type using inferExprType which handles Result type unwrapping
		// when a catch clause is present
		callReturnType := inferExprType(ctx, mod, e)
		mod.SetExprType(expr, callReturnType)
		return callReturnType

	case *ast.SpreadExpr:
		// Spread is only valid in call arguments or array literals; type check inner expression.
		innerType := checkExpr(ctx, mod, e.X, expected)
		mod.SetExprType(expr, innerType)
		return innerType

	case *ast.SelectorExpr:
		// Validate base expression first
		checkExpr(ctx, mod, e.X, types.TypeUnknown)
		checkSelectorExpr(ctx, mod, e)

	case *ast.ScopeResolutionExpr:
		checkExpr(ctx, mod, e.X, types.TypeUnknown)

	case *ast.RangeExpr:
		checkExpr(ctx, mod, e.Start, types.TypeUnknown)
		checkExpr(ctx, mod, e.End, types.TypeUnknown)
		if e.Incr != nil {
			checkExpr(ctx, mod, e.Incr, types.TypeUnknown)
		}
		rangeType := inferRangeExprType(ctx, mod, e)
		mod.SetExprType(expr, rangeType)
		return rangeType

	case *ast.BinaryExpr:
		// Recursively check operands
		lhsType := checkExpr(ctx, mod, e.X, types.TypeUnknown)

		if e.Op.Kind == tokens.IS_TOKEN {
			// Special handling for 'is' operator - RHS is a TypeExpr
			mod.SetExprType(expr, types.TypeBool)

			// Extract the actual type from TypeExpr
			var rhsType types.SemType
			if typeExpr, ok := e.Y.(*ast.TypeExpr); ok {
				rhsType = TypeFromTypeNodeWithContext(ctx, mod, typeExpr.Type)
			} else {
				// Fallback: try to infer type from expression (for backward compatibility)
				rhsType = checkExpr(ctx, mod, e.Y, types.TypeUnknown)
			}

			lhsUnwrapped := types.UnwrapType(lhsType)
			if unionType, ok := lhsUnwrapped.(*types.UnionType); ok {
				// rhsType should be a type that matches a variant
				if slices.ContainsFunc(unionType.Variants, rhsType.Equals) {
					// Valid, return bool
					return types.TypeBool
				}
				// Not a valid variant
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("'is' operator: type '%s' is not a variant of union '%s'", rhsType.String(), lhsType.String())).
						WithPrimaryLabel(e.Y.Loc(), "not a variant").
						WithSecondaryLabel(e.X.Loc(), "union type"),
				)
				return types.TypeBool // Still return bool, error reported
			} else if ifaceType, ok := lhsUnwrapped.(*types.InterfaceType); ok && len(ifaceType.Methods) == 0 {
				// Allow 'is' on empty interface{} - can check for any type at runtime
				// No need to validate rhsType - any type is valid
				return types.TypeBool
			} else {
				// Allow 'is' on concrete types; this becomes a compile-time check.
				if rhsType == nil || rhsType.Equals(types.TypeUnknown) {
					ctx.Diagnostics.Add(
						diagnostics.NewError("invalid type in 'is' operator").
							WithPrimaryLabel(e.Y.Loc(), "cannot resolve type").
							WithCode(diagnostics.ErrTypeMismatch),
					)
				}
				return types.TypeBool
			}
		}

		// For non-'is' operators, check RHS normally, then bind untyped literals to typed operands.
		var rhsType types.SemType
		if e.Op.Kind == tokens.AND_TOKEN || e.Op.Kind == tokens.OR_TOKEN {
			thenNarrowing, elseNarrowing := narrowingAnalyzer.AnalyzeCondition(ctx, mod, e.X, nil)
			var rhsNarrowing *narrowing.NarrowingContext
			if e.Op.Kind == tokens.AND_TOKEN {
				rhsNarrowing = thenNarrowing
			} else {
				rhsNarrowing = elseNarrowing
			}
			withNarrowedExprTypes(mod, rhsNarrowing, func() {
				rhsType = checkExpr(ctx, mod, e.Y, types.TypeUnknown)
			})
		} else {
			rhsType = checkExpr(ctx, mod, e.Y, types.TypeUnknown)
		}

		lhsType = bindUntypedNumericLiteral(ctx, mod, e.X, lhsType, rhsType, e.Y)
		rhsType = bindUntypedNumericLiteral(ctx, mod, e.Y, rhsType, lhsType, e.X)
		{
			// Validate operand type compatibility for regular binary ops
			checkBinaryExpr(ctx, mod, e, lhsType, rhsType)
			// Return the result type - for most binary ops, it's the same as lhsType
			var resultType types.SemType
			switch e.Op.Kind {
			case tokens.PLUS_TOKEN:
				// PLUS can be string concatenation or arithmetic
				// String concatenation: str + anything → str
				if types.UnwrapType(lhsType).Equals(types.TypeString) {
					resultType = types.TypeString
				} else {
					// Arithmetic: result widens to a compatible numeric type
					resultType = numericBinaryResultType(e.Op.Kind, lhsType, rhsType)
				}
			case tokens.MINUS_TOKEN, tokens.MUL_TOKEN, tokens.DIV_TOKEN, tokens.MOD_TOKEN,
				tokens.BIT_AND_TOKEN, tokens.BIT_OR_TOKEN, tokens.BIT_XOR_TOKEN:
				if e.Op.Kind == tokens.BIT_AND_TOKEN || e.Op.Kind == tokens.BIT_OR_TOKEN || e.Op.Kind == tokens.BIT_XOR_TOKEN {
					resultType = lhsType
				} else {
					resultType = numericBinaryResultType(e.Op.Kind, lhsType, rhsType)
				}
			case tokens.EXP_TOKEN:
				// Power operator: use the larger type for large primitives, f64 otherwise
				resultType = types.GetPowerResultType(lhsType, rhsType)
			case tokens.DOUBLE_EQUAL_TOKEN, tokens.NOT_EQUAL_TOKEN, tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN,
				tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN, tokens.AND_TOKEN, tokens.OR_TOKEN, tokens.IN_TOKEN:
				resultType = types.TypeBool
			default:
				resultType = types.TypeUnknown
			}
			mod.SetExprType(expr, resolveNumericExprTypeForModule(ctx, expr, expected, resultType))
			return resultType
		}

	case *ast.UnaryExpr:
		// Recursively check operand
		operandType := checkExpr(ctx, mod, e.X, types.TypeUnknown)
		if e.Op.Kind == tokens.BIT_AND_TOKEN || e.Op.Kind == tokens.MUT_TOKEN {
			checkBorrowExpr(ctx, mod, e, operandType)
			// Return reference type
			refType := types.NewReference(operandType)
			if e.Op.Kind == tokens.MUT_TOKEN {
				refType.Mutable = true
			}
			mod.SetExprType(expr, refType)
			return refType
		}
		if e.Op.Kind == tokens.AT_TOKEN {
			checkMoveExpr(ctx, mod, e, operandType)
			mod.SetExprType(expr, operandType)
			return operandType
		}
		if e.Op.Kind == tokens.HASH_TOKEN {
			checkHeapExpr(ctx, mod, e, operandType)
			mod.SetExprType(expr, operandType)
			return operandType
		}

		checkUnaryOp(ctx, e, operandType)

		// For other unary ops like -, return operand type
		mod.SetExprType(expr, resolveNumericExprTypeForModule(ctx, expr, expected, operandType))
		return operandType

	case *ast.DerefExpr:
		// Check operand and verify it's a reference type
		operandType := checkExpr(ctx, mod, e.X, types.TypeUnknown)
		unwrapped := types.UnwrapType(operandType)

		if refType, ok := unwrapped.(*types.ReferenceType); ok {
			// Return the inner type
			mod.SetExprType(expr, refType.Inner)
			return refType.Inner
		}

		// Not a reference type - error
		if !operandType.Equals(types.TypeUnknown) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot dereference non-reference type '%s'", operandType.String())).
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(e.Loc(), fmt.Sprintf("type '%s' is not a reference", operandType.String())).
					WithHelp("dereference operator '*' requires a reference type (&T or &mut T)"),
			)
		}
		mod.SetExprType(expr, types.TypeUnknown)
		return types.TypeUnknown

	case *ast.BasicLit:
		// Default numeric literals to the configured default types when there is
		// no concrete expected type (or expected is itself untyped).
		if (expected.Equals(types.TypeUnknown) || types.IsUntyped(expected)) && (e.Kind == ast.INT || e.Kind == ast.FLOAT) {
			if e.Kind == ast.INT {
				defaultType := types.FromTypeName(types.DEFAULT_INT_TYPE)
				if !fitsInType(e.Value, defaultType) {
					minUnsigned, minSigned := getMinimumTypeOptionsForValue(e.Value)
					suggestions := formatIntegerTypeSuggestions(types.DEFAULT_INT_TYPE, minUnsigned, minSigned)
					diag := diagnostics.NewError(fmt.Sprintf("integer literal %s does not fit in default type %s", e.Value, defaultType.String())).
						WithPrimaryLabel(e.Loc(), fmt.Sprintf("does not fit in %s", defaultType.String())).
						WithNote(fmt.Sprintf("default integer type is %s", defaultType.String()))
					if suggestions != "" {
						diag = diag.WithHelp(fmt.Sprintf("use an explicit cast: `%s as %s`", e.GetText(ctx.Diagnostics.GetSourceCache()), suggestions))
					} else {
						diag = diag.WithHelp("value exceeds maximum supported integer size (256-bit)")
					}
					ctx.Diagnostics.Add(diag)
				}
				mod.SetExprType(expr, defaultType)
				return defaultType
			}
			defaultType := types.FromTypeName(types.DEFAULT_FLOAT_TYPE)
			if !fitsInType(e.Value, defaultType) {
				digits := countSignificantDigits(e.Value)
				minType := getMinimumFloatTypeForDigits(digits)
				diag := diagnostics.NewError(fmt.Sprintf("float literal has too many significant digits for default type %s", defaultType.String())).
					WithPrimaryLabel(e.Loc(), fmt.Sprintf("%d significant digits", digits)).
					WithNote(fmt.Sprintf("default float type is %s", defaultType.String()))
				if minType != "exceeds f256 precision" {
					diag = diag.WithHelp(fmt.Sprintf("use an explicit cast: `%s as %s`", e.GetText(ctx.Diagnostics.GetSourceCache()), minType))
				} else {
					diag = diag.WithHelp("cast to proper size to select a wider float type")
				}
				ctx.Diagnostics.Add(diag)
			}
			litType := inferLiteralType(e, types.TypeUnknown)
			mod.SetExprType(expr, litType)
			return litType
		}

		expectedForLit := expected
		if e.Kind == ast.INT || e.Kind == ast.FLOAT {
			expectedForLit = types.UnwrapOptionalType(expected)
		}
		litType := inferLiteralType(e, expectedForLit)
		if optType, ok := expected.(*types.OptionalType); ok && litType.Equals(optType.Inner) {
			litType = expected
		}
		mod.SetExprType(expr, litType)
		return litType
	case *ast.PrefixExpr:
		// Validate ++/-- target and operand type
		targetType := checkExpr(ctx, mod, e.X, types.TypeUnknown)
		targetType = checkIncDecTarget(ctx, mod, e, e.X, targetType, e.Op)
		// Return the target type
		mod.SetExprType(expr, targetType)
		return targetType
	case *ast.PostfixExpr:
		// Validate ++/-- target and operand type
		targetType := checkExpr(ctx, mod, e.X, types.TypeUnknown)
		targetType = checkIncDecTarget(ctx, mod, e, e.X, targetType, e.Op)
		// Return the target type
		mod.SetExprType(expr, targetType)
		return targetType

	case *ast.IndexExpr:
		// Check both array and index expressions
		baseType := checkExpr(ctx, mod, e.X, types.TypeUnknown)
		indexType := checkExpr(ctx, mod, e.Index, types.TypeUnknown)
		checkIndexExpr(ctx, e, baseType, indexType)
		// Return element type
		resolvedBase := autoDerefBaseType(e, baseType)
		if arrType, ok := resolvedBase.(*types.ArrayType); ok {
			mod.SetExprType(expr, arrType.Element)
			return arrType.Element
		}
		if mapType, ok := resolvedBase.(*types.MapType); ok {
			mod.SetExprType(expr, mapType.Value)
			return mapType.Value
		}
		if prim, ok := resolvedBase.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
			mod.SetExprType(expr, types.TypeByte)
			return types.TypeByte
		}
		return types.TypeUnknown

	case *ast.ParenExpr:
		// Check inner expression
		innerType := checkExpr(ctx, mod, e.X, expected)
		if mod != nil {
			mod.SetExprType(expr, resolveNumericExprTypeForModule(ctx, expr, expected, innerType))
		}
		return innerType

	case *ast.CastExpr:
		// Check expression being cast and validate cast compatibility
		targetType := TypeFromTypeNodeWithContext(ctx, mod, e.Type)
		// For composite literals, provide target type as context to allow untyped literal contextualization
		sourceType := checkExpr(ctx, mod, e.X, targetType)
		checkCastExpr(ctx, mod, e, sourceType, targetType)
		mod.SetExprType(expr, targetType)
		return targetType

	case *ast.CompositeLit:
		// Determine target type for validation
		var targetType types.SemType
		if e.Type != nil {
			// Explicit type: use it as target
			targetType = TypeFromTypeNodeWithContext(ctx, mod, e.Type)
		} else if !expected.Equals(types.TypeUnknown) {
			// Expected type provided: use it as target
			if refType, ok := types.UnwrapType(expected).(*types.ReferenceType); ok {
				targetType = refType.Inner
			} else if _, ok := types.UnwrapType(expected).(*types.UnionType); ok {
				targetType = inferCompositeLitType(ctx, mod, e)
			} else if _, ok := types.UnwrapType(expected).(*types.InterfaceType); ok {
				// If expected type is an interface, infer the actual struct/array/map type
				// The compatibility will be checked later in checkAssignLike
				targetType = inferCompositeLitType(ctx, mod, e)
			} else {
				targetType = expected
			}
		} else {
			// No explicit type and no expected type: infer type
			targetType = inferExprType(ctx, mod, e)
		}

		// checkCompositeLit handles everything: element checking with context AND validation
		// Missing fields info will be handled in checkAssignLike for better error messages
		if !targetType.Equals(types.TypeUnknown) {
			checkCompositeLit(ctx, mod, e, targetType)
		}
		mod.SetExprType(expr, targetType)
		return targetType
	}

	// First, infer the type based on the expression structure
	inferredType := inferExprType(ctx, mod, expr)

	// Check if literal is too large for any supported type
	if lit, ok := expr.(*ast.BasicLit); ok {
		if inferredType.Equals(types.TypeUnknown) {
			// Literal doesn't fit in maximum type - report error
			switch lit.Kind {
			case ast.INT:
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("integer literal %s exceeds maximum supported integer size (256-bit)", lit.Value)).
						WithPrimaryLabel(lit.Loc(), "too large value").
						WithNote("maximum supported integer type is i256 (256-bit signed integer)").
						WithHelp("consider using a string representation or splitting the value"),
				)
			case ast.FLOAT:
				digits := countSignificantDigits(lit.Value)
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("float literal has %d significant digits, exceeds maximum supported precision (f256, ~71 digits)", digits)).
						WithPrimaryLabel(lit.Loc(), "too large value").
						WithNote("maximum supported float type is f256 with ~71 significant digits").
						WithHelp("consider reducing precision or using a different representation"),
				)
			}
			return types.TypeUnknown
		}
	}

	// Apply contextual typing for literals when expected type is provided
	// Note: Struct literals, string literals, bool literals have concrete types immediately
	resultType := inferredType

	// If we have an expected type and the expression is a literal, contextualize it directly
	// This ensures literals are contextualized to the expected type when provided
	if !expected.Equals(types.TypeUnknown) {
		if lit, ok := expr.(*ast.BasicLit); ok {
			// If expected is optional, contextualize to inner type
			expectedForLit := types.UnwrapOptionalType(expected)
			// For numeric literals, always try to contextualize to expected type
			// inferLiteralType will return expected type if compatible, or default if not
			if lit.Kind == ast.INT || lit.Kind == ast.FLOAT {
				resultType = inferLiteralType(lit, expectedForLit)
			}
		}
	}

	// Special handling for CompositeLit: if expected type is known and matches structure,
	// adopt the expected type (for better compatibility with named types)
	if compLit, ok := expr.(*ast.CompositeLit); ok && compLit.Type == nil {
		if !expected.Equals(types.TypeUnknown) {
			// Unwrap expected to get underlying struct or array
			expectedUnwrapped := types.UnwrapType(expected)
			if expectedStruct, ok := expectedUnwrapped.(*types.StructType); ok {
				// For empty composite literals, adopt the expected struct type
				// This allows `let p: Point = {}` to work (with missing fields errors)
				if inferredType.Equals(types.TypeUnknown) {
					resultType = expected
					// Validate the composite literal against the expected struct type
					checkCompositeLit(ctx, mod, compLit, expected)
				} else if inferredStruct, ok := inferredType.(*types.StructType); ok {
					// Check if inferred struct is compatible with expected
					if areStructsCompatible(inferredStruct, expectedStruct) {
						// Use the expected type (preserves named type)
						resultType = expected
					}
				}
			} else if _, ok := expectedUnwrapped.(*types.ArrayType); ok {
				// For array literals without explicit type, adopt the expected array type
				// This allows [1, 2, 3] to adopt type [3]i32 when that's expected
				resultType = expected
				// Validate the composite literal against the expected array type
				checkCompositeLit(ctx, mod, compLit, expected)
			}
		}
	}

	// Handle optional type wrapping (T -> ?T)
	if !expected.Equals(types.TypeUnknown) {
		if optType, ok := expected.(*types.OptionalType); ok {
			// If assigning non-optional to optional, check if inner types match
			if !resultType.Equals(types.TypeUnknown) && resultType.Equals(optType.Inner) {
				// Value matches inner type, allow implicit wrapping
				resultType = expected
			}
		}
	}

	// Keep untyped literals untyped for better error messages
	// They will be resolved in specific contexts that need concrete types

	if mod != nil {
		mod.SetExprType(expr, resultType)
	}

	return resultType
}

// checkAssignLike checks assignment-like operations with better error reporting
// typeNode: optional AST node for the target type (for location info)
// valueExpr: the value expression being assigned
// targetType: the expected/target type
func checkAssignLike(ctx *context_v2.CompilerContext, mod *context_v2.Module, leftType types.SemType, leftNode ast.Node, rightNode ast.Expression) {
	// Pass leftType as expected type so composite literals can be contextualized
	rhsType := checkExpr(ctx, mod, rightNode, leftType)

	// Special check for integer literals: ensure they fit in the target type
	if ok := checkFitness(ctx, leftType, rightNode, leftNode); !ok {
		return
	}

	// Check type compatibility (use context-aware version for interface checking)
	compatibility := checkTypeCompatibilityWithContext(ctx, mod, rhsType, leftType)

	// Try breaking narrowing if incompatible or explicit
	breakNarrowing := false
	if compatibility == Incompatible || compatibility == ExplicitCastable {
		if ident, ok := leftNode.(*ast.IdentifierExpr); ok {
			if sym, found := mod.CurrentScope.Lookup(ident.Name); found && sym.OriginalType != nil {
				origCompat := checkTypeCompatibilityWithContext(ctx, mod, rhsType, sym.OriginalType)
				if origCompat == Identical || origCompat == ImplicitCastable {
					// Break narrowing
					sym.Type = sym.OriginalType
					sym.OriginalType = nil
					breakNarrowing = true
				}
			}
		}
	}

	if breakNarrowing {
		return
	}

	switch compatibility {
	case Identical, ImplicitCastable:
		return

	case ExplicitCastable:
		// For identical types, don't require explicit cast
		if rhsType.Equals(leftType) {
			return
		}
		// Requires explicit cast - use centralized hint system
		diag := diagnostics.NewError(getConversionError(rhsType, leftType, compatibility)).
			WithPrimaryLabel(rightNode.Loc(), fmt.Sprintf("type '%s'", rhsType.String())).
			WithSecondaryLabel(leftNode.Loc(), fmt.Sprintf("type '%s'", leftType.String()))
		diag = addExplicitCastHint(ctx, diag, leftType, compatibility, rightNode)
		diag = addDerefHintIfNeeded(ctx, mod, diag, leftType, rhsType, rightNode)
		ctx.Diagnostics.Add(diag)

	case Incompatible:
		// Cannot convert - create user-friendly error message (no hint, not castable)
		rhsUnwrapped := types.UnwrapType(rhsType)
		leftUnwrapped := types.UnwrapType(leftType)

		// Check if target is an interface - provide detailed missing methods
		if leftIface, isIface := leftUnwrapped.(*types.InterfaceType); isIface {
			_, missingMethods := analyzeInterfaceCompatibility(ctx, mod, rhsType, leftIface)
			if len(missingMethods) > 0 {
				errorMsg := getConversionError(rhsType, leftType, compatibility)
				diag := diagnostics.NewError(errorMsg).
					WithHelp(fmt.Sprintf("missing methods: %s", strings.Join(missingMethods, ", ")))

				if leftNode != nil {
					valueDesc := formatValueDescription(rhsType, rightNode)
					diag = diag.WithPrimaryLabel(rightNode.Loc(), valueDesc).
						WithSecondaryLabel(leftNode.Loc(), fmt.Sprintf("type '%s'", leftType.String()))
				} else {
					diag = diag.WithPrimaryLabel(rightNode.Loc(), fmt.Sprintf("expected '%s', got '%s'", leftType.String(), rhsType.String()))
				}

				diag = addDerefHintIfNeeded(ctx, mod, diag, leftType, rhsType, rightNode)
				ctx.Diagnostics.Add(diag)
				return
			}
		}

		var missingFields []string
		var mismatchedFields []string
		isStructCompatibility := false

		if rhsStruct, ok := rhsUnwrapped.(*types.StructType); ok {
			if leftStruct, ok := leftUnwrapped.(*types.StructType); ok {
				// Both are structs - get detailed compatibility info
				missingFields, mismatchedFields = analyzeStructCompatibility(rhsStruct, leftStruct)
				isStructCompatibility = true
			}
		}

		var errorMsg string
		if types.IsUntyped(rhsType) {
			// For untyped literals, use more intuitive message
			if types.IsUntypedInt(rhsType) {
				errorMsg = fmt.Sprintf("cannot use integer literal as type '%s'", leftType.String())
			} else if types.IsUntypedFloat(rhsType) {
				errorMsg = fmt.Sprintf("cannot use float literal as type '%s'", leftType.String())
			} else {
				errorMsg = getConversionError(rhsType, leftType, compatibility)
			}
		} else {
			errorMsg = getConversionError(rhsType, leftType, compatibility)
		}

		diag := diagnostics.NewError(errorMsg)

		// Add dual labels if we have type node location
		if leftNode != nil {
			// Format value description (special handling for untyped literals)
			valueDesc := formatValueDescription(rhsType, rightNode)
			diag = diag.WithPrimaryLabel(rightNode.Loc(), valueDesc).
				WithSecondaryLabel(leftNode.Loc(), fmt.Sprintf("type '%s'", leftType.String()))
		} else {
			diag = diag.WithPrimaryLabel(rightNode.Loc(), fmt.Sprintf("expected '%s', got '%s'", leftType.String(), rhsType.String()))
		}

		// Add helpful note for struct compatibility issues
		if isStructCompatibility {
			if note := formatStructCompatibilityNote(missingFields, mismatchedFields, false); note != "" {
				diag = diag.WithNote(note)
			}
		}

		diag = addDerefHintIfNeeded(ctx, mod, diag, leftType, rhsType, rightNode)
		ctx.Diagnostics.Add(diag)
	}
}

func addExplicitCastHint(ctx *context_v2.CompilerContext, diag *diagnostics.Diagnostic, target types.SemType, compatibility TypeCompatibility, expr ast.Expression) *diagnostics.Diagnostic {
	if diag == nil || compatibility != ExplicitCastable {
		return diag
	}
	exprText := ""
	if expr != nil && expr.Loc() != nil {
		exprText = expr.Loc().GetText(ctx.Diagnostics.GetSourceCache())
	}
	hint := getConversionHint(target, compatibility, exprText)
	if hint == "" {
		return diag
	}
	if diag.Help != "" {
		hint = fmt.Sprintf("%s; %s", diag.Help, hint)
	}
	return diag.WithHelp(hint)
}

func addDerefHintIfNeeded(ctx *context_v2.CompilerContext, mod *context_v2.Module, diag *diagnostics.Diagnostic, expected, actual types.SemType, expr ast.Expression) *diagnostics.Diagnostic {
	if diag == nil || expected == nil || actual == nil {
		return diag
	}
	if _, ok := types.UnwrapType(expected).(*types.ReferenceType); ok {
		return diag
	}
	refType, ok := types.UnwrapType(actual).(*types.ReferenceType)
	if !ok {
		return diag
	}

	expectedBase := types.UnwrapOptionalType(expected)
	compatibility := checkTypeCompatibility(refType.Inner, expectedBase)
	if ctx != nil && mod != nil {
		compatibility = checkTypeCompatibilityWithContext(ctx, mod, refType.Inner, expectedBase)
	}
	if !isImplicitlyCompatible(compatibility) {
		return diag
	}

	exprText := ""
	if expr != nil && expr.Loc() != nil && ctx != nil {
		exprText = expr.Loc().GetText(ctx.Diagnostics.GetSourceCache())
	}
	hint := "dereference: *value"
	if exprText != "" {
		hint = fmt.Sprintf("dereference: *%s", exprText)
	}
	if diag.Help != "" {
		hint = fmt.Sprintf("%s; %s", diag.Help, hint)
	}
	return diag.WithHelp(hint)
}

// formatValueDescription formats a user-friendly description for a value in error messages.
// For untyped literals, it shows the minimum type needed (e.g., "integer literal (needs i32)").
func formatValueDescription(typ types.SemType, expr ast.Expression) string {
	// Handle untyped literals specially - show what type they would resolve to
	if types.IsUntyped(typ) {
		if basicLit, ok := expr.(*ast.BasicLit); ok {
			// Resolve to see what type it needs
			// TODO : change later to min type
			resolvedType := inferLiteralType(basicLit, types.TypeUnknown)
			if types.IsUntypedInt(typ) {
				return fmt.Sprintf("integer literal (needs %s)", resolvedType.String())
			} else if types.IsUntypedFloat(typ) {
				return fmt.Sprintf("float literal (needs %s)", resolvedType.String())
			}
		}
		// Fallback for non-literal untyped expressions
		if types.IsUntypedInt(typ) {
			return "integer value"
		} else if types.IsUntypedFloat(typ) {
			return "float value"
		}
	}
	return fmt.Sprintf("type '%s'", typ.String())
}

type numericConst struct {
	kind     ast.LiteralKind
	intVal   *big.Int
	floatVal *big.Float
}

func evaluateNumericConst(expr ast.Expression) (numericConst, bool) {
	if expr == nil {
		return numericConst{}, false
	}

	switch e := expr.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case ast.INT:
			val, ok := parseIntLiteral(e.Value)
			if !ok {
				return numericConst{}, false
			}
			return numericConst{kind: ast.INT, intVal: val}, true
		case ast.FLOAT:
			val, ok := parseFloatLiteral(e.Value)
			if !ok {
				return numericConst{}, false
			}
			return numericConst{kind: ast.FLOAT, floatVal: val}, true
		}

	case *ast.UnaryExpr:
		if e.Op.Kind != tokens.MINUS_TOKEN && e.Op.Kind != tokens.PLUS_TOKEN {
			return numericConst{}, false
		}
		inner, ok := evaluateNumericConst(e.X)
		if !ok {
			return numericConst{}, false
		}
		switch inner.kind {
		case ast.INT:
			val := new(big.Int).Set(inner.intVal)
			if e.Op.Kind == tokens.MINUS_TOKEN {
				val.Neg(val)
			}
			return numericConst{kind: ast.INT, intVal: val}, true
		case ast.FLOAT:
			val := new(big.Float).SetPrec(256).Set(inner.floatVal)
			if e.Op.Kind == tokens.MINUS_TOKEN {
				val.Neg(val)
			}
			return numericConst{kind: ast.FLOAT, floatVal: val}, true
		}

	case *ast.BinaryExpr:
		if !isNumericConstOp(e.Op.Kind) {
			return numericConst{}, false
		}
		left, ok := evaluateNumericConst(e.X)
		if !ok {
			return numericConst{}, false
		}
		right, ok := evaluateNumericConst(e.Y)
		if !ok {
			return numericConst{}, false
		}
		kind := ast.INT
		if left.kind == ast.FLOAT || right.kind == ast.FLOAT {
			kind = ast.FLOAT
		}
		if kind == ast.FLOAT {
			return evalFloatBinaryConst(left, right, e.Op.Kind)
		}
		return evalIntBinaryConst(left, right, e.Op.Kind)

	case *ast.ParenExpr:
		return evaluateNumericConst(e.X)
	}

	return numericConst{}, false
}

func computeRangeConstLength(expr *ast.RangeExpr) (int, bool) {
	if expr == nil {
		return 0, false
	}

	start, ok := evaluateNumericConst(expr.Start)
	if !ok || start.kind != ast.INT || start.intVal == nil {
		return 0, false
	}
	end, ok := evaluateNumericConst(expr.End)
	if !ok || end.kind != ast.INT || end.intVal == nil {
		return 0, false
	}

	step := numericConst{kind: ast.INT, intVal: big.NewInt(1)}
	if expr.Incr != nil {
		step, ok = evaluateNumericConst(expr.Incr)
		if !ok || step.kind != ast.INT || step.intVal == nil {
			return 0, false
		}
	}

	return consteval.RangeLengthFromInts(start.intVal, end.intVal, step.intVal, expr.Inclusive)
}

func isNumericConstOp(op tokens.TOKEN) bool {
	switch op {
	case tokens.PLUS_TOKEN, tokens.MINUS_TOKEN, tokens.MUL_TOKEN, tokens.DIV_TOKEN, tokens.MOD_TOKEN,
		tokens.EXP_TOKEN, tokens.BIT_AND_TOKEN, tokens.BIT_OR_TOKEN, tokens.BIT_XOR_TOKEN:
		return true
	default:
		return false
	}
}

func evalIntBinaryConst(left, right numericConst, op tokens.TOKEN) (numericConst, bool) {
	if left.intVal == nil || right.intVal == nil {
		return numericConst{}, false
	}
	lhs := new(big.Int).Set(left.intVal)
	rhs := new(big.Int).Set(right.intVal)

	switch op {
	case tokens.PLUS_TOKEN:
		return numericConst{kind: ast.INT, intVal: new(big.Int).Add(lhs, rhs)}, true
	case tokens.MINUS_TOKEN:
		return numericConst{kind: ast.INT, intVal: new(big.Int).Sub(lhs, rhs)}, true
	case tokens.MUL_TOKEN:
		return numericConst{kind: ast.INT, intVal: new(big.Int).Mul(lhs, rhs)}, true
	case tokens.DIV_TOKEN:
		if rhs.Sign() == 0 {
			return numericConst{}, false
		}
		return numericConst{kind: ast.INT, intVal: new(big.Int).Div(lhs, rhs)}, true
	case tokens.MOD_TOKEN:
		if rhs.Sign() == 0 {
			return numericConst{}, false
		}
		return numericConst{kind: ast.INT, intVal: new(big.Int).Mod(lhs, rhs)}, true
	case tokens.EXP_TOKEN:
		if rhs.Sign() < 0 {
			return numericConst{}, false
		}
		return numericConst{kind: ast.INT, intVal: new(big.Int).Exp(lhs, rhs, nil)}, true
	case tokens.BIT_AND_TOKEN:
		return numericConst{kind: ast.INT, intVal: new(big.Int).And(lhs, rhs)}, true
	case tokens.BIT_OR_TOKEN:
		return numericConst{kind: ast.INT, intVal: new(big.Int).Or(lhs, rhs)}, true
	case tokens.BIT_XOR_TOKEN:
		return numericConst{kind: ast.INT, intVal: new(big.Int).Xor(lhs, rhs)}, true
	default:
		return numericConst{}, false
	}
}

func evalFloatBinaryConst(left, right numericConst, op tokens.TOKEN) (numericConst, bool) {
	lhs := numericConstToFloat(left)
	rhs := numericConstToFloat(right)
	if lhs == nil || rhs == nil {
		return numericConst{}, false
	}

	switch op {
	case tokens.PLUS_TOKEN:
		return numericConst{kind: ast.FLOAT, floatVal: new(big.Float).SetPrec(256).Add(lhs, rhs)}, true
	case tokens.MINUS_TOKEN:
		return numericConst{kind: ast.FLOAT, floatVal: new(big.Float).SetPrec(256).Sub(lhs, rhs)}, true
	case tokens.MUL_TOKEN:
		return numericConst{kind: ast.FLOAT, floatVal: new(big.Float).SetPrec(256).Mul(lhs, rhs)}, true
	case tokens.DIV_TOKEN:
		if rhs.Sign() == 0 {
			return numericConst{}, false
		}
		return numericConst{kind: ast.FLOAT, floatVal: new(big.Float).SetPrec(256).Quo(lhs, rhs)}, true
	default:
		return numericConst{}, false
	}
}

func numericConstToFloat(value numericConst) *big.Float {
	switch value.kind {
	case ast.FLOAT:
		return new(big.Float).SetPrec(256).Set(value.floatVal)
	case ast.INT:
		if value.intVal == nil {
			return nil
		}
		return new(big.Float).SetPrec(256).SetInt(value.intVal)
	default:
		return nil
	}
}

func parseIntLiteral(value string) (*big.Int, bool) {
	sign := 1
	if strings.HasPrefix(value, "+") {
		value = value[1:]
	} else if strings.HasPrefix(value, "-") {
		sign = -1
		value = value[1:]
	}
	if value == "" {
		return nil, false
	}
	abs, err := numeric.StringToBigInt(value)
	if err != nil {
		return nil, false
	}
	if sign < 0 {
		abs.Neg(abs)
	}
	return abs, true
}

func parseFloatLiteral(value string) (*big.Float, bool) {
	cleaned := strings.ReplaceAll(value, "_", "")
	val, _, err := big.ParseFloat(cleaned, 10, 256, big.ToNearestEven)
	if err != nil {
		return nil, false
	}
	return val, true
}

func numericConstValueString(value numericConst) string {
	switch value.kind {
	case ast.INT:
		if value.intVal != nil {
			return value.intVal.String()
		}
	case ast.FLOAT:
		if value.floatVal != nil {
			return value.floatVal.Text('g', -1)
		}
	}
	return ""
}

func resolveUntypedNumericExpr(expr ast.Expression) (types.SemType, bool) {
	value, ok := evaluateNumericConst(expr)
	if !ok {
		return types.TypeUnknown, false
	}

	litValue := numericConstValueString(value)
	if litValue == "" {
		return types.TypeUnknown, true
	}

	lit := &ast.BasicLit{
		Kind:  value.kind,
		Value: litValue,
	}
	return inferLiteralType(lit, types.TypeUnknown), true
}

func reportNumericConstTooLarge(ctx *context_v2.CompilerContext, expr ast.Expression) {
	if ctx == nil || expr == nil {
		return
	}
	value, ok := evaluateNumericConst(expr)
	if !ok {
		return
	}
	litValue := numericConstValueString(value)
	if litValue == "" {
		return
	}
	switch value.kind {
	case ast.INT:
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("integer literal %s exceeds maximum supported integer size (256-bit)", litValue)).
				WithPrimaryLabel(expr.Loc(), "too large value").
				WithNote("maximum supported integer type is i256 (256-bit signed integer)").
				WithHelp("consider using a string representation or splitting the value"),
		)
	case ast.FLOAT:
		digits := countSignificantDigits(litValue)
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("float literal has %d significant digits, exceeds maximum supported precision (f256, ~71 digits)", digits)).
				WithPrimaryLabel(expr.Loc(), "too large value").
				WithNote("maximum supported float type is f256 with ~71 significant digits").
				WithHelp("consider reducing precision or using a different representation"),
		)
	}
}

func resolveNumericExprTypeForModule(_ *context_v2.CompilerContext, expr ast.Expression, expected, resultType types.SemType) types.SemType {
	if !types.IsUntyped(resultType) {
		return resultType
	}

	expectedBase := types.UnwrapOptionalType(expected)
	expectedUnwrapped := types.UnwrapType(expectedBase)
	if !expected.Equals(types.TypeUnknown) && types.IsNumeric(expectedUnwrapped) {
		if value, ok := evaluateNumericConst(expr); ok {
			valueStr := numericConstValueString(value)
			if valueStr != "" {
				if value.kind == ast.INT && types.IsInteger(expectedUnwrapped) && fitsInType(valueStr, expectedUnwrapped) {
					return expected
				}
				if value.kind == ast.FLOAT && types.IsFloat(expectedUnwrapped) && fitsInType(valueStr, expectedUnwrapped) {
					return expected
				}
			}
		}
	}

	if resolved, ok := resolveUntypedNumericExpr(expr); ok && !resolved.Equals(types.TypeUnknown) {
		return resolved
	}

	return resultType
}

func formatIntegerTypeSuggestions(targetName types.TYPE_NAME, minUnsigned, minSigned types.TYPE_NAME) string {
	var parts []string
	if types.IsUnsigned(targetName) {
		if minUnsigned != types.TYPE_UNKNOWN {
			parts = append(parts, string(minUnsigned))
		}
	} else if types.IsSigned(targetName) {
		if minUnsigned != types.TYPE_UNKNOWN {
			parts = append(parts, string(minUnsigned))
		}
		if minSigned != types.TYPE_UNKNOWN {
			parts = append(parts, string(minSigned))
		}
	} else {
		if minUnsigned != types.TYPE_UNKNOWN {
			parts = append(parts, string(minUnsigned))
		}
		if minSigned != types.TYPE_UNKNOWN {
			parts = append(parts, string(minSigned))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	if len(parts) == 2 {
		return fmt.Sprintf("%s or %s", parts[0], parts[1])
	}

	// join the last part with 'or' and others with ','
	if len(parts) > 2 {
		return fmt.Sprintf("%s, or %s", strings.Join(parts[:len(parts)-1], ", "), parts[len(parts)-1])
	}

	return parts[0]
}

func checkFitness(ctx *context_v2.CompilerContext, targetType types.SemType, valueExpr ast.Expression, typeNode ast.Node) bool {
	value, ok := evaluateNumericConst(valueExpr)
	if !ok {
		return true
	}
	valueStr := numericConstValueString(value)
	if valueStr == "" {
		return true
	}
	targetBase := types.UnwrapType(targetType)

	// Check integer literal overflow
	// Check literal directly (works for both untyped and already-resolved literals)
	if value.kind == ast.INT {
		if targetName, ok := types.GetPrimitiveName(targetBase); ok && types.IsIntegerTypeName(targetName) {
			// Use big.Int for all range checking (simpler, consistent)
			if !fitsInType(valueStr, targetBase) {
				// Get minimum types that can hold this value
				minUnsigned, minSigned := getMinimumTypeOptionsForValue(valueStr)
				suggestions := formatIntegerTypeSuggestions(targetName, minUnsigned, minSigned)
				hasSuggestion := len(suggestions) > 0
				hasSigned := minSigned != types.TYPE_UNKNOWN

				defaultType := types.FromTypeName(types.DEFAULT_INT_TYPE)
				if typeNode == nil && targetBase.Equals(defaultType) {
					diag := diagnostics.NewError(fmt.Sprintf("integer literal %s does not fit in default type %s", valueStr, defaultType.String())).
						WithPrimaryLabel(valueExpr.Loc(), fmt.Sprintf("does not fit in %s", defaultType.String())).
						WithNote(fmt.Sprintf("default integer type is %s", defaultType.String()))
					if hasSuggestion {
						diag = diag.WithHelp(fmt.Sprintf("use an explicit cast: `%s as %s`", valueExpr.Loc().GetText(ctx.Diagnostics.GetSourceCache()), suggestions))
					} else {
						diag = diag.WithHelp("cast to proper size to select a larger integer type")
					}
					ctx.Diagnostics.Add(diag)
					return false
				}

				// Build error message
				diag := diagnostics.NewError(fmt.Sprintf("integer literal %s overflows %s", valueStr, targetType.String()))

				if typeNode != nil {
					// Show value location (primary) and type location (secondary)
					if hasSuggestion {
						diag = diag.WithPrimaryLabel(valueExpr.Loc(), fmt.Sprintf("at least %s required to store this value", suggestions)).
							WithSecondaryLabel(typeNode.Loc(), fmt.Sprintf("type '%s'", targetType.String()))
					} else if hasSigned {
						diag = diag.WithPrimaryLabel(valueExpr.Loc(), "value is negative; requires signed integer type").
							WithSecondaryLabel(typeNode.Loc(), fmt.Sprintf("type '%s'", targetType.String()))
					} else {
						diag = diag.WithPrimaryLabel(valueExpr.Loc(), "exceeds maximum supported integer size (256-bit)").
							WithSecondaryLabel(typeNode.Loc(), fmt.Sprintf("type '%s'", targetType.String()))
					}
				} else {
					if hasSuggestion {
						diag = diag.WithPrimaryLabel(valueExpr.Loc(), fmt.Sprintf("at least %s required, got %s", suggestions, targetType.String()))
					} else if hasSigned {
						diag = diag.WithPrimaryLabel(valueExpr.Loc(), fmt.Sprintf("value is negative; cannot use %s", targetType.String()))
					} else {
						diag = diag.WithPrimaryLabel(valueExpr.Loc(), "exceeds maximum supported integer size (256-bit)")
					}
				}

				diag = diag.WithNote(fmt.Sprintf("%s can hold values in range: %s", targetType.String(), getTypeRange(targetBase)))

				ctx.Diagnostics.Add(diag)
				return false
			}
		}
	}

	// Check float literal precision
	// Check literal directly (works for both untyped and already-resolved literals)
	if value.kind == ast.FLOAT {
		if targetName, ok := types.GetPrimitiveName(targetBase); ok && types.IsFloatTypeName(targetName) {
			if !fitsInType(valueStr, targetBase) {
				// Get minimum type that can hold this precision
				digits := countSignificantDigits(valueStr)
				minType := getMinimumFloatTypeForDigits(digits)

				defaultType := types.FromTypeName(types.DEFAULT_FLOAT_TYPE)
				if typeNode == nil && targetBase.Equals(defaultType) {
					diag := diagnostics.NewError(fmt.Sprintf("float literal has too many significant digits for default type %s", defaultType.String())).
						WithPrimaryLabel(valueExpr.Loc(), fmt.Sprintf("%d significant digits", digits)).
						WithNote(fmt.Sprintf("default float type is %s", defaultType.String()))
					if minType != "exceeds f256 precision" {
						diag = diag.WithHelp(fmt.Sprintf("use an explicit cast: `%s as %s`", valueExpr.Loc().GetText(ctx.Diagnostics.GetSourceCache()), minType))
					} else {
						diag = diag.WithHelp("cast to proper size to select a wider float type")
					}
					ctx.Diagnostics.Add(diag)
					return false
				}

				// Build error message
				diag := diagnostics.NewError(fmt.Sprintf("float literal has too many significant digits for %s", targetType.String()))

				if typeNode != nil {
					if minType != "exceeds f256 precision" {
						diag = diag.WithPrimaryLabel(valueExpr.Loc(), fmt.Sprintf("%d significant digits (needs %s)", digits, minType)).
							WithSecondaryLabel(typeNode.Loc(), fmt.Sprintf("type '%s' supports ~%d digits", targetType.String(), getFloatPrecision(targetName)))
					} else {
						diag = diag.WithPrimaryLabel(valueExpr.Loc(), fmt.Sprintf("%d significant digits", digits)).
							WithSecondaryLabel(typeNode.Loc(), fmt.Sprintf("type '%s'", targetType.String())).
							WithNote("This literal exceeds max f256 precision limit (~71 digits)")
					}
				} else {
					diag = diag.WithPrimaryLabel(valueExpr.Loc(), fmt.Sprintf("%d significant digits, need %s but got %s", digits, minType, targetType.String()))
				}

				ctx.Diagnostics.Add(diag)
				return false
			}
		}
	}

	return true
}

// TypeFromTypeNodeWithContext resolves type nodes including user-defined types by looking them up in the symbol table
// TypeFromTypeNodeWithContext converts AST type nodes to semantic types with full context.
// This allows resolving user-defined types, module-qualified types, etc.
// Exported for use by codegen and other packages that need type resolution.
func TypeFromTypeNodeWithContext(ctx *context_v2.CompilerContext, mod *context_v2.Module, typeNode ast.TypeNode) types.SemType {
	if typeNode == nil {
		return types.TypeUnknown
	}

	switch t := typeNode.(type) {
	case *ast.ScopeResolutionExpr:
		// Handle module::Type references (requires context)
		if ctx == nil || mod == nil {
			return types.TypeUnknown
		}
		if moduleIdent, ok := t.X.(*ast.IdentifierExpr); ok {
			moduleAlias := moduleIdent.Name
			typeName := t.Selector.Name

			// Look up the import
			importPath, ok := mod.ImportAliasMap[moduleAlias]
			if !ok {
				// Module not imported - error should have been reported in resolver
				return types.TypeUnknown
			}

			// Get the imported module
			importedMod, exists := ctx.GetModule(importPath)
			if !exists {
				// Module not loaded - error should have been reported
				return types.TypeUnknown
			}

			// Look up the type in the imported module's scope
			sym, ok := importedMod.ModuleScope.GetSymbol(typeName)
			if !ok || sym.Kind != symbols.SymbolType {
				// Type not found - error should have been reported in resolver
				return types.TypeUnknown
			}

			// Return the resolved type
			return sym.Type
		}
		return types.TypeUnknown

	case *ast.IdentifierExpr:
		// If we have context, try to look up user-defined type in symbol table first
		if ctx != nil && mod != nil {
			sym, ok := mod.CurrentScope.Lookup(t.Name)
			if ok && sym.Kind == symbols.SymbolType {
				// Return the resolved user-defined type
				return sym.Type
			}
		}

		// Not a user-defined type (or no context), check if it's a primitive type
		primitiveType := types.FromTypeName(types.TYPE_NAME(t.Name))
		if !primitiveType.Equals(types.TypeUnknown) {
			return primitiveType
		}

		// Type not found - report error
		if ctx != nil {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("undefined type '%s'", t.Name)).
					WithCode(diagnostics.ErrUndefinedSymbol).
					WithPrimaryLabel(t.Loc(), "type not found").
					WithHelp("check the spelling or import the module that defines this type"),
			)
		}
		return types.TypeUnknown

	case *ast.ArrayType:
		// Array type: [N]T or []T
		elementType := TypeFromTypeNodeWithContext(ctx, mod, t.ElType)
		length := -1 // Dynamic array by default
		if t.Len != nil {
			// Extract constant length from array size expression (only if we have context for const eval)
			if ctx != nil && mod != nil {
				if constLength, ok := extractConstantIndex(t.Len); ok && constLength >= 0 {
					length = constLength
				}
			}
		}
		return types.NewArray(elementType, length)

	case *ast.OptionalType:
		// Optional type: ?T
		innerType := TypeFromTypeNodeWithContext(ctx, mod, t.Base)
		return types.NewOptional(innerType)

	case *ast.UnionType:
		// Union type: union { T1, T2, ..., TN }
		variants := make([]types.SemType, len(t.Variants))
		for i, variant := range t.Variants {
			variants[i] = TypeFromTypeNodeWithContext(ctx, mod, variant)
		}
		return types.NewUnion(variants)

	case *ast.ReferenceType:
		// Reference type: &T
		innerType := TypeFromTypeNodeWithContext(ctx, mod, t.Base)
		if isReferenceType(innerType) {
			if ctx != nil {
				ctx.Diagnostics.Add(
					diagnostics.NewError("nested references are not supported").
						WithCode(diagnostics.ErrInvalidType).
						WithPrimaryLabel(t.Base.Loc(), "use a single '&' reference"),
				)
			}
			return types.TypeUnknown
		}
		if t.Mutable {
			return types.NewMutableReference(innerType)
		}
		return types.NewReference(innerType)

	case *ast.ResultType:
		// Result type: T ! E
		okType := TypeFromTypeNodeWithContext(ctx, mod, t.Value)
		errType := TypeFromTypeNodeWithContext(ctx, mod, t.Error)
		return types.NewResult(okType, errType)

	case *ast.StructType:
		// Anonymous struct
		fields := make([]types.StructField, len(t.Fields))
		for i, f := range t.Fields {
			fieldName := ""
			if f.Name != nil {
				fieldName = f.Name.Name
			}
			fields[i] = types.StructField{
				Name: fieldName,
				Type: TypeFromTypeNodeWithContext(ctx, mod, f.Type),
			}
		}
		structType := types.NewStruct("", fields)
		// Propagate the ID from AST to semantic type for identity tracking
		structType.ID = t.ID
		return structType
	case *ast.EnumType:
		variants := make([]types.EnumVariant, len(t.Variants))
		for i, v := range t.Variants {
			name := ""
			if v.Name != nil {
				name = v.Name.Name
			}
			variants[i] = types.EnumVariant{
				Name:  name,
				Value: int64(i),
				Type:  nil,
			}
		}
		enumType := types.NewEnum("", variants)
		enumType.ID = t.ID
		return enumType

	case *ast.FuncType:
		// Function type: fn(T1, T2) -> R
		params := make([]types.ParamType, len(t.Params))
		for i, param := range t.Params {
			params[i].Name = param.Name.Name
			params[i].Type = TypeFromTypeNodeWithContext(ctx, mod, param.Type)
			params[i].IsVariadic = param.IsVariadic
		}
		returnType := types.TypeVoid
		if t.Result != nil {
			returnType = TypeFromTypeNodeWithContext(ctx, mod, t.Result)
		}
		return types.NewFunction(params, returnType)

	case *ast.MapType:
		// Map type: map[K]V
		keyType := TypeFromTypeNodeWithContext(ctx, mod, t.Key)
		valueType := TypeFromTypeNodeWithContext(ctx, mod, t.Value)
		if ctx != nil && mod != nil && !keyType.Equals(types.TypeUnknown) && !types.IsMapKeyComparable(keyType) {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("map key type '%s' is not comparable", keyType.String())).
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(t.Key.Loc(), "map keys must be comparable").
					WithHelp("Comparable types: primitives (i32, f64, str, bool, byte, char), structs with comparable fields, fixed arrays [N]T (not slices), pointers, and enums.\nNon-comparable: slices []T, maps, functions, interfaces, unions, optionals, results."),
			)
		}
		return types.NewMap(keyType, valueType)

	case *ast.InterfaceType:
		// Interface type: interface { method1(...), method2(...) }
		methods := make([]types.InterfaceMethod, 0, len(t.Methods))
		for _, m := range t.Methods {
			if m.Type == nil {
				continue
			}
			// m.Type should be a FuncType
			if funcType, ok := m.Type.(*ast.FuncType); ok {
				params := make([]types.ParamType, len(funcType.Params))
				for j, param := range funcType.Params {
					params[j].Name = param.Name.Name
					params[j].Type = TypeFromTypeNodeWithContext(ctx, mod, param.Type)
					params[j].IsVariadic = param.IsVariadic
				}
				returnType := types.TypeVoid
				if funcType.Result != nil {
					returnType = TypeFromTypeNodeWithContext(ctx, mod, funcType.Result)
				}
				methods = append(methods, types.InterfaceMethod{
					Name:     m.Name.Name,
					FuncType: types.NewFunction(params, returnType),
				})
			}
		}
		interfaceType := types.NewInterface(methods)
		// Propagate the ID from AST to semantic type for identity tracking
		interfaceType.ID = t.ID
		return interfaceType

	default:
		// For unknown types, try primitive lookup
		if ident, ok := typeNode.(*ast.IdentifierExpr); ok {
			return types.FromTypeName(types.TYPE_NAME(ident.Name))
		}
		return types.TypeUnknown
	}
}

// typeFromTypeNode converts AST type nodes to semantic types (without context)
// This is a convenience wrapper that calls TypeFromTypeNodeWithContext with nil context.
// For user-defined types, this will only work for primitives.
func typeFromTypeNode(typeNode ast.TypeNode) types.SemType {
	// Use context version with nil - it will handle primitives correctly
	// but won't resolve user-defined types (which is fine for this use case)
	return TypeFromTypeNodeWithContext(nil, nil, typeNode)
}

// checkCallExpr validates function call expressions
// This includes:
// - Verifying the called expression is actually a function
// - Checking argument count (regular and variadic functions)
// - Validating argument types against parameter types
func checkCallExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr) {
	// 0. Validate the callee expression (ordering rules, selectors, nested calls, etc.)
	checkExpr(ctx, mod, expr.Fun, types.TypeUnknown)

	// 1. Infer the type of the expression being called
	funType := inferExprType(ctx, mod, expr.Fun)

	// 2. Check if it's unknown (error already reported in resolution phase)
	if funType.Equals(types.TypeUnknown) {
		// Still type check arguments to find additional errors
		for _, arg := range expr.Args {
			checkExpr(ctx, mod, arg, types.TypeUnknown)
		}
		return
	}

	// 3. Check if it's a function type
	funcType, ok := funType.(*types.FunctionType)
	if !ok {
		ctx.Diagnostics.Add(
			diagnostics.NotCallable(mod.FilePath, expr.Fun.Loc(), funType.String()),
		)
		// Still type check arguments
		for _, arg := range expr.Args {
			checkExpr(ctx, mod, arg, types.TypeUnknown)
		}
		return
	}

	// 4. Validate argument count
	validateCallArgumentCount(ctx, mod, expr, funcType)

	// 5. Validate argument types
	validateCallArgumentTypes(ctx, mod, expr, funcType)
}

// validateCallArgumentCount checks if the number of arguments matches the function signature
func validateCallArgumentCount(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr, funcType *types.FunctionType) {
	argCount := len(expr.Args)
	paramCount := len(funcType.Params)

	// Check if function is variadic
	isVariadic := paramCount > 0 && funcType.Params[paramCount-1].IsVariadic

	if isVariadic {
		// Variadic function: need at least (paramCount - 1) arguments
		minRequired := paramCount - 1
		if argCount < minRequired {
			ctx.Diagnostics.Add(
				diagnostics.WrongArgumentCountVariadic(
					mod.FilePath,
					&expr.Location,
					minRequired,
					argCount,
				),
			)
		}
	} else {
		// Regular function: need exactly paramCount arguments
		if argCount != paramCount {
			ctx.Diagnostics.Add(
				diagnostics.WrongArgumentCount(
					mod.FilePath,
					&expr.Location,
					paramCount,
					argCount,
				),
			)
		}
	}
}

// validateCallArgumentTypes checks if argument types are compatible with parameter types
func validateCallArgumentTypes(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr, funcType *types.FunctionType) {
	paramCount := len(funcType.Params)
	argCount := len(expr.Args)

	// Determine if function is variadic
	isVariadic := paramCount > 0 && funcType.Params[paramCount-1].IsVariadic

	// Check regular parameters
	regularParamCount := paramCount
	if isVariadic {
		regularParamCount = paramCount - 1
	}

	// Detect spread arguments
	firstSpread := -1
	for i, arg := range expr.Args {
		if _, ok := arg.(*ast.SpreadExpr); ok {
			firstSpread = i
			break
		}
	}
	if firstSpread >= 0 {
		if !isVariadic {
			ctx.Diagnostics.Add(
				diagnostics.NewError("cannot use spread argument with non-variadic function").
					WithPrimaryLabel(expr.Args[firstSpread].Loc(), "spread argument here").
					WithHelp("remove '...' or call a variadic function"),
			)
		}
		if firstSpread < regularParamCount {
			ctx.Diagnostics.Add(
				diagnostics.NewError("spread argument must come after regular parameters").
					WithPrimaryLabel(expr.Args[firstSpread].Loc(), "spread argument here").
					WithHelp("move '...' after the regular parameters"),
			)
		}
	}

	// Validate regular parameters
	for i := 0; i < regularParamCount && i < argCount; i++ {
		param := funcType.Params[i]
		arg := expr.Args[i]
		if spread, ok := arg.(*ast.SpreadExpr); ok {
			checkExpr(ctx, mod, spread.X, param.Type)
			continue
		}

		// Infer argument type with parameter type as context
		argType := checkExpr(ctx, mod, arg, param.Type)
		if refParam, ok := types.UnwrapType(param.Type).(*types.ReferenceType); ok && !argType.Equals(types.TypeUnknown) {
			refArg, isRefArg := types.UnwrapType(argType).(*types.ReferenceType)
			if !isRefArg {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("argument '%s' must be a reference", param.Name)).
						WithCode(diagnostics.ErrInvalidAssignment).
						WithPrimaryLabel(arg.Loc(), "expected a reference value").
						WithHelp("use '&' to pass a reference"),
				)
				continue
			}
			if refParam.Mutable && !refArg.Mutable {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("argument '%s' must be a mutable reference", param.Name)).
						WithCode(diagnostics.ErrInvalidAssignment).
						WithPrimaryLabel(arg.Loc(), "expected a mutable reference").
						WithHelp("use '&mut' to pass a mutable reference"),
				)
				continue
			}
		}

		if ok := checkFitness(ctx, param.Type, arg, nil); !ok {
			continue
		}

		// Check compatibility (use WithContext to handle interfaces properly)
		compatibility := checkTypeCompatibilityWithContext(ctx, mod, argType, param.Type)

		if !isImplicitlyCompatible(compatibility) {
			// Format the argument type in a user-friendly way
			argTypeDesc := types.ResolveUntypedType(argType, param.Type)
			diag := diagnostics.ArgumentTypeMismatch(
				mod.FilePath,
				arg.Loc(),
				param.Name,
				param.Type.String(),
				argTypeDesc.String(),
			)
			diag = addExplicitCastHint(ctx, diag, param.Type, compatibility, arg)
			diag = addDerefHintIfNeeded(ctx, mod, diag, param.Type, argType, arg)
			ctx.Diagnostics.Add(diag)
		}
	}

	// Validate variadic arguments
	// If function is variadic, all arguments (including those that match regular params if any)
	// beyond regularParamCount are checked against the variadic element type
	if isVariadic {
		variadicParam := funcType.Params[paramCount-1]
		variadicElemType := variadicParam.Type // The element type (not array type)

		// For variadic functions, check all arguments starting from regularParamCount
		// If there are no regular params (regularParamCount == 0), check all arguments
		startIdx := regularParamCount
		if regularParamCount == 0 {
			startIdx = 0 // Check all arguments against variadic type
		}

		for i := startIdx; i < argCount; i++ {
			arg := expr.Args[i]
			if spread, ok := arg.(*ast.SpreadExpr); ok {
				sliceType := types.NewArray(variadicElemType, -1)
				argType := checkExpr(ctx, mod, spread.X, sliceType)
				if ok := checkFitness(ctx, sliceType, spread.X, nil); !ok {
					continue
				}
				compatibility := checkTypeCompatibilityWithContext(ctx, mod, argType, sliceType)
				if !isImplicitlyCompatible(compatibility) {
					argTypeDesc := types.ResolveUntypedType(argType, sliceType)
					diag := diagnostics.ArgumentTypeMismatch(
						mod.FilePath,
						spread.Loc(),
						variadicParam.Name,
						sliceType.String(),
						argTypeDesc.String(),
					)
					diag = addExplicitCastHint(ctx, diag, sliceType, compatibility, spread.X)
					ctx.Diagnostics.Add(diag)
				}
				continue
			}

			// Infer argument type with variadic element type as context
			argType := checkExpr(ctx, mod, arg, variadicElemType)
			if refParam, ok := types.UnwrapType(variadicElemType).(*types.ReferenceType); ok && !argType.Equals(types.TypeUnknown) {
				refArg, isRefArg := types.UnwrapType(argType).(*types.ReferenceType)
				if !isRefArg {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("argument '%s' must be a reference", variadicParam.Name)).
							WithCode(diagnostics.ErrInvalidAssignment).
							WithPrimaryLabel(arg.Loc(), "expected a reference value").
							WithHelp("use '&' to pass a reference"),
					)
					continue
				}
				if refParam.Mutable && !refArg.Mutable {
					ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("argument '%s' must be a mutable reference", variadicParam.Name)).
							WithCode(diagnostics.ErrInvalidAssignment).
							WithPrimaryLabel(arg.Loc(), "expected a mutable reference").
							WithHelp("use '&mut' to pass a mutable reference"),
					)
					continue
				}
			}

			if ok := checkFitness(ctx, variadicElemType, arg, nil); !ok {
				continue
			}

			// Check compatibility with variadic element type (use WithContext to handle interfaces properly)
			compatibility := checkTypeCompatibility(argType, variadicElemType)

			if !isImplicitlyCompatible(compatibility) {
				// Format the argument type in a user-friendly way
				argTypeDesc := types.ResolveUntypedType(argType, variadicElemType)
				diag := diagnostics.ArgumentTypeMismatch(
					mod.FilePath,
					arg.Loc(),
					variadicParam.Name,
					variadicElemType.String(),
					argTypeDesc.String(),
				)
				diag = addExplicitCastHint(ctx, diag, variadicElemType, compatibility, arg)
				diag = addDerefHintIfNeeded(ctx, mod, diag, variadicElemType, argType, arg)
				ctx.Diagnostics.Add(diag)
			}
		}
	}

	// Type check any remaining arguments (in case of wrong count)
	// This helps find multiple errors in a single pass
	maxChecked := argCount
	if isVariadic {
		maxChecked = argCount // Already checked all
	} else {
		maxChecked = regularParamCount
	}

	for i := maxChecked; i < argCount; i++ {
		checkExpr(ctx, mod, expr.Args[i], types.TypeUnknown)
	}

	// Validate Result type handling: check if function returns Result and needs catch
	validateResultTypeHandling(ctx, mod, expr, funcType)
}

// validateResultTypeHandling checks if Result types are properly handled with catch clauses
func validateResultTypeHandling(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr, funcType *types.FunctionType) {
	returnType := funcType.Return
	_, isResult := returnType.(*types.ResultType)

	if isResult {
		// Function returns a Result type
		if expr.Catch == nil {
			// In defer context, Result without catch is allowed (catch is optional)
			if mod.InDeferContext {
				return
			}
			// No catch clause - error must be handled
			ctx.Diagnostics.Add(
				diagnostics.UncaughtError(ctx.Diagnostics.GetSourceCache(), mod.FilePath, expr.Loc(), returnType.String()),
			)
		}
	} else {
		// Function does not return a Result type
		if expr.Catch != nil {
			// Cannot use catch on non-Result function
			ctx.Diagnostics.Add(
				diagnostics.InvalidCatch(mod.FilePath, expr.Catch.Loc(), returnType.String()),
			)
		}
	}
}

// checkCatchClause validates catch clause semantics
func checkCatchClause(ctx *context_v2.CompilerContext, mod *context_v2.Module, callExpr *ast.CallExpr) {
	catch := callExpr.Catch
	if catch == nil {
		return
	}

	// Get the function's return type to determine error type
	funType := inferExprType(ctx, mod, callExpr.Fun)
	funcType, ok := funType.(*types.FunctionType)
	if !ok {
		return // Error already reported in checkCallExpr
	}

	resultType, isResult := funcType.Return.(*types.ResultType)
	if !isResult {
		return // Error already reported in validateResultTypeHandling
	}

	if catch.Handler != nil {
		// get the scope of the catch block
		scope := catch.Handler.Scope.(*table.SymbolTable)
		defer mod.EnterScope(scope)()
		// set the catch error identifier type to the error type
		if catch.ErrIdent != nil {
			if sym, ok := mod.CurrentScope.Lookup(catch.ErrIdent.Name); ok {
				sym.Type = resultType.Err
			}
		}
		// check the catch block
		checkBlock(ctx, mod, catch.Handler)
	}
	// check the fallback expression - must match the OK type of the result
	if catch.Fallback != nil {
		// Use checkAssignLike to validate type compatibility and report errors
		checkAssignLike(ctx, mod, resultType.Ok, nil, catch.Fallback)
	}
}

// applyNarrowingToBlock temporarily overrides symbol types based on narrowing context
// and checks the block. Uses defer to automatically restore original types.
// Also stores narrowing info in module artifacts for HIR/MIR code generation.
func applyNarrowingToBlock(ctx *context_v2.CompilerContext, mod *context_v2.Module, block *ast.Block, nc *narrowing.NarrowingContext) {
	if block == nil {
		return
	}

	if nc == nil {
		checkBlock(ctx, mod, block)
		return
	}

	// Collect all narrowed entries from this context and parent chain
	narrowedEntries := make(map[string]*narrowing.NarrowingEntry)
	collectNarrowedEntries(nc, narrowedEntries)

	if len(narrowedEntries) == 0 {
		checkBlock(ctx, mod, block)
		return
	}

	// Store narrowing info in artifacts for code generation
	storeNarrowingArtifacts(mod, block, narrowedEntries)

	// Apply narrowed expression types during this block.
	prevExprTypes := mod.NarrowedExprTypes
	mod.NarrowedExprTypes = mergeNarrowedExprTypes(prevExprTypes, narrowedEntries)
	defer func() {
		mod.NarrowedExprTypes = prevExprTypes
	}()

	// Apply narrowing using defer for automatic restoration.
	narrowedSymbols := narrowedSymbolTypes(mod, narrowedEntries)
	defer restoreSymbolTypes(mod, narrowedSymbols)()

	// Apply narrowed types
	for varName, narrowedType := range narrowedSymbols {
		if sym, ok := mod.CurrentScope.Lookup(varName); ok {
			if sym.OriginalType == nil {
				if entry, ok := narrowedEntries[varName]; ok && entry != nil && entry.OriginalType != nil {
					sym.OriginalType = entry.OriginalType
				} else {
					sym.OriginalType = sym.Type
				}
			}
			sym.Type = narrowedType
		}
	}

	// Check the block with narrowed types
	checkBlock(ctx, mod, block)
}

// storeNarrowingArtifacts stores narrowing information in module artifacts
// so it can be used during HIR/MIR code generation.
func storeNarrowingArtifacts(mod *context_v2.Module, block *ast.Block, narrowedEntries map[string]*narrowing.NarrowingEntry) {
	if mod == nil || block == nil || len(narrowedEntries) == 0 {
		return
	}

	info := narrowing.GetOrCreateNarrowingInfo(mod)
	scopeKey := narrowing.ScopeKeyFromLocation(mod.FilePath, block.Location.Start.Line, block.Location.Start.Column)
	scope := info.GetOrCreateScope(scopeKey)

	for key, entry := range narrowedEntries {
		if entry == nil || entry.NarrowedType == nil {
			continue
		}

		stored := *entry
		stored.VarName = key

		if stored.OriginalType == nil {
			if sym, ok := mod.CurrentScope.Lookup(key); ok && sym != nil {
				if sym.OriginalType != nil {
					stored.OriginalType = sym.OriginalType
				} else {
					stored.OriginalType = sym.Type
				}
			}
		}

		if stored.OriginalType != nil && stored.Kind == narrowing.NarrowingUnion {
			if unionType, ok := types.UnwrapType(stored.OriginalType).(*types.UnionType); ok && stored.VariantIndex < 0 {
				for i, variant := range unionType.Variants {
					if stored.NarrowedType.Equals(variant) {
						stored.VariantIndex = i
						break
					}
				}
			}
		} else if stored.OriginalType != nil && stored.Kind == narrowing.NarrowingOptional {
			// No additional metadata required.
		} else if stored.OriginalType != nil && stored.Kind == narrowing.NarrowingInterface {
			// No additional metadata required.
		}

		scope.Add(&stored)
	}
}

// collectNarrowedEntries walks the narrowing context chain and collects all narrowed entries.
// Child contexts override parent contexts for the same expression key.
func collectNarrowedEntries(nc *narrowing.NarrowingContext, result map[string]*narrowing.NarrowingEntry) {
	if nc == nil || result == nil {
		return
	}

	if nc.Parent != nil {
		collectNarrowedEntries(nc.Parent, result)
	}

	for key, entry := range nc.Entries {
		result[key] = entry
	}
}

func mergeNarrowedExprTypes(prev map[string]types.SemType, entries map[string]*narrowing.NarrowingEntry) map[string]types.SemType {
	if len(entries) == 0 {
		return prev
	}
	next := make(map[string]types.SemType, len(prev)+len(entries))
	for key, typ := range prev {
		next[key] = typ
	}
	for key, entry := range entries {
		if entry == nil || entry.NarrowedType == nil {
			continue
		}
		next[key] = entry.NarrowedType
	}
	return next
}

func narrowedSymbolTypes(mod *context_v2.Module, entries map[string]*narrowing.NarrowingEntry) map[string]types.SemType {
	if mod == nil || len(entries) == 0 {
		return nil
	}
	out := make(map[string]types.SemType)
	for key, entry := range entries {
		if entry == nil || entry.NarrowedType == nil {
			continue
		}
		if _, ok := mod.CurrentScope.Lookup(key); ok {
			out[key] = entry.NarrowedType
		}
	}
	return out
}

func withNarrowedExprTypes(mod *context_v2.Module, nc *narrowing.NarrowingContext, fn func()) {
	if fn == nil {
		return
	}
	if mod == nil || nc == nil {
		fn()
		return
	}

	entries := make(map[string]*narrowing.NarrowingEntry)
	collectNarrowedEntries(nc, entries)
	if len(entries) == 0 {
		fn()
		return
	}

	prev := mod.NarrowedExprTypes
	mod.NarrowedExprTypes = mergeNarrowedExprTypes(prev, entries)
	defer func() {
		mod.NarrowedExprTypes = prev
	}()

	fn()
}

// restoreSymbolTypes returns a function that restores original types
// This allows using defer for automatic cleanup
func restoreSymbolTypes(mod *context_v2.Module, narrowedVars map[string]types.SemType) func() {
	// Capture original types at the time of narrowing
	originalTypes := make(map[string]types.SemType)

	for varName := range narrowedVars {
		if sym, ok := mod.CurrentScope.Lookup(varName); ok {
			originalTypes[varName] = sym.Type
		}
	}

	// Return restoration function
	return func() {
		for varName, origType := range originalTypes {
			if sym, ok := mod.CurrentScope.Lookup(varName); ok {
				sym.Type = origType
				sym.OriginalType = nil
			}
		}
	}
}

// applyNarrowingToElse handles else and else-if branches with narrowing
func applyNarrowingToElse(ctx *context_v2.CompilerContext, mod *context_v2.Module, elseNode ast.Node, elseNarrowing *narrowing.NarrowingContext) {
	if elseNode == nil {
		return
	}

	// Check if it's an else-if (IfStmt) or plain else (Block)
	switch e := elseNode.(type) {
	case *ast.IfStmt:
		// Enter scope if exists
		if e.Scope != nil {
			defer mod.EnterScope(e.Scope.(*table.SymbolTable))()
		}

		// Check condition under else-branch narrowing
		withNarrowedExprTypes(mod, elseNarrowing, func() {
			checkExpr(ctx, mod, e.Cond, types.TypeBool)
		})

		// For else-if, re-analyze the condition with parent narrowing from else branch
		elseIfThenNarrowing, elseIfElseNarrowing := narrowingAnalyzer.AnalyzeCondition(ctx, mod, e.Cond, elseNarrowing)

		// Check then branch with combined narrowing
		if e.Body != nil {
			applyNarrowingToBlock(ctx, mod, e.Body, elseIfThenNarrowing)
		}

		// Recursively handle nested else-if/else
		if e.Else != nil {
			applyNarrowingToElse(ctx, mod, e.Else, elseIfElseNarrowing)
		}

	case *ast.Block:
		// Plain else block - apply narrowing from condition
		applyNarrowingToBlock(ctx, mod, e, elseNarrowing)

	default:
		// Fallback for unexpected node types
		checkNode(ctx, mod, elseNode)
	}
}

// extractConstantIndex attempts to extract a compile-time constant integer from an expression
// Returns the integer value and whether it's a constant
func extractConstantIndex(expr ast.Expression) (int, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		// Direct integer literal
		if e.Kind == ast.INT {
			// Parse the integer value
			var val int64
			fmt.Sscanf(e.Value, "%d", &val)
			return int(val), true
		}

	case *ast.UnaryExpr:
		// Handle negative literals: -5
		if e.Op.Kind == tokens.MINUS_TOKEN {
			if val, ok := extractConstantIndex(e.X); ok {
				return -val, true
			}
		}

		// Could extend to handle constant folding of expressions like 2+3
		// For now, only handle direct literals
	}

	return 0, false
}

// isEmptyLiteral checks if an expression is an empty literal (array, map, or struct)
func isEmptyLiteral(expr ast.Expression) bool {
	if expr == nil {
		return false
	}

	var compLit *ast.CompositeLit
	if castExpr, ok := expr.(*ast.CastExpr); ok {
		// Unwrap cast: [] as []i32, {} as Point, map[str]i32{} as MapType
		if cl, ok := castExpr.X.(*ast.CompositeLit); ok {
			compLit = cl
		}
	} else if cl, ok := expr.(*ast.CompositeLit); ok {
		compLit = cl
	}

	if compLit != nil && len(compLit.Elts) == 0 {
		return true
	}

	return false
}

func compositeLiteralKind(lit *ast.CompositeLit) string {
	if lit == nil {
		return "composite"
	}

	if len(lit.Elts) == 0 {
		return "composite"
	}

	allKeyValue := true
	hasStructSyntax := false
	hasMapSyntax := false

	for _, elem := range lit.Elts {
		kv, ok := elem.(*ast.KeyValueExpr)
		if !ok {
			allKeyValue = false
			break
		}
		if _, ok := kv.Key.(*ast.IdentifierExpr); ok {
			hasStructSyntax = true
		} else {
			hasMapSyntax = true
		}
	}

	if !allKeyValue {
		return "array"
	}
	if hasStructSyntax && !hasMapSyntax {
		return "struct"
	}
	if hasMapSyntax {
		return "map"
	}

	return "composite"
}

// inferTypeFromEmptyLiteral attempts to infer the type from an empty literal to suggest the correct type annotation
// Returns the type string if it can be inferred, empty string otherwise
func inferTypeFromEmptyLiteral(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression) string {
	if expr == nil {
		return ""
	}

	// Handle cast expressions: [] as []i32, {} as Point
	if castExpr, ok := expr.(*ast.CastExpr); ok {
		if castExpr.Type != nil {
			return TypeFromTypeNodeWithContext(ctx, mod, castExpr.Type).String()
		}
	}

	// Handle composite literals with explicit type: map[str]i32{}
	if compLit, ok := expr.(*ast.CompositeLit); ok {
		if compLit.Type != nil {
			return TypeFromTypeNodeWithContext(ctx, mod, compLit.Type).String()
		}
	}

	return ""
}
