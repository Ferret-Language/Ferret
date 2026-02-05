package analysis

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/hir"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"fmt"
)

// ControlFlowGraph represents the control flow structure of a function.
type ControlFlowGraph struct {
	Entry *BasicBlock // Entry block
	Exit  *BasicBlock // Virtual exit block
}

// BasicBlock represents a sequence of statements with single entry and exit.
type BasicBlock struct {
	ID           int              // Unique identifier
	Nodes        []hir.Node       // Statements in this block
	Successors   []*BasicBlock    // Possible next blocks
	Predecessors []*BasicBlock    // Blocks that can reach this one
	Terminator   ControlFlowKind  // How this block ends
	Location     *source.Location // Location for diagnostics
	Reachable    bool             // Whether this block is reachable
	Returns      bool             // Whether this block always returns
	CanFallThru  bool             // Whether execution can fall through
	BranchKind   string           // "then", "else", "loop", etc.
	OriginNode   hir.Node         // The HIR node that created this branch
}

// ControlFlowKind represents how a basic block terminates.
type ControlFlowKind int

const (
	FlowFallthrough ControlFlowKind = iota // Normal flow to next block
	FlowReturn                             // Return statement
	FlowBreak                              // Break statement
	FlowContinue                           // Continue statement
	FlowConditional                        // If/match branch
	FlowLoop                               // Loop construct
	FlowUnreachable                        // After return/panic/etc
)

// CFGBuilder builds control flow graphs from HIR.
type CFGBuilder struct {
	ctx          *context_v2.CompilerContext
	mod          *context_v2.Module
	blockCounter int
	currentLoop  *loopContext // Stack of loop contexts for break/continue
}

// loopContext tracks the current loop for break/continue statements.
type loopContext struct {
	breakTarget    *BasicBlock
	continueTarget *BasicBlock
	parent         *loopContext
	loopStmt       hir.Node    // The loop statement (ForStmt or WhileStmt)
	bodyBlock      *BasicBlock // Loop body entry block
	hasBreak       bool        // Whether loop body contains break
	hasReturn      bool        // Whether loop body contains return
}

// NewCFGBuilder creates a new control flow graph builder.
func NewCFGBuilder(ctx *context_v2.CompilerContext, mod *context_v2.Module) *CFGBuilder {
	return &CFGBuilder{
		ctx:          ctx,
		mod:          mod,
		blockCounter: 0,
		currentLoop:  nil,
	}
}

// newBlock creates a new basic block.
func (b *CFGBuilder) newBlock() *BasicBlock {
	b.blockCounter++
	return &BasicBlock{
		ID:           b.blockCounter,
		Nodes:        make([]hir.Node, 0),
		Successors:   make([]*BasicBlock, 0),
		Predecessors: make([]*BasicBlock, 0),
		Terminator:   FlowFallthrough,
		Reachable:    false,
		Returns:      false,
		CanFallThru:  true,
	}
}

// addEdge adds a control flow edge between blocks.
func addEdge(from, to *BasicBlock) {
	if from != nil && to != nil {
		from.Successors = append(from.Successors, to)
		to.Predecessors = append(to.Predecessors, from)
	}
}

// BuildFunctionCFG builds a control flow graph for a function.
func (b *CFGBuilder) BuildFunctionCFG(funcDecl *hir.FuncDecl) *ControlFlowGraph {
	cfg := &ControlFlowGraph{
		Entry: b.newBlock(),
		Exit:  b.newBlock(),
	}

	cfg.Entry.Reachable = true
	cfg.Entry.Location = funcDecl.Loc()

	if funcDecl.Body != nil {
		current := cfg.Entry
		current = b.buildBlock(funcDecl.Body, current, cfg.Exit)

		// If current block can fall through, connect to exit.
		if current != nil && current.CanFallThru {
			addEdge(current, cfg.Exit)
		}
	} else {
		// Empty function body - direct edge to exit.
		addEdge(cfg.Entry, cfg.Exit)
	}

	return cfg
}

// BuildBlockCFG builds a control flow graph for an arbitrary block.
func (b *CFGBuilder) BuildBlockCFG(block *hir.Block) *ControlFlowGraph {
	cfg := &ControlFlowGraph{
		Entry: b.newBlock(),
		Exit:  b.newBlock(),
	}

	cfg.Entry.Reachable = true
	if block != nil {
		cfg.Entry.Location = block.Loc()
		cfg.Exit.Location = block.Loc()
	}

	current := cfg.Entry
	current = b.buildBlock(block, current, cfg.Exit)

	if current != nil && current.CanFallThru {
		addEdge(current, cfg.Exit)
	}
	if block == nil {
		addEdge(cfg.Entry, cfg.Exit)
	}

	return cfg
}

// buildBlock processes a block of statements.
func (b *CFGBuilder) buildBlock(block *hir.Block, current *BasicBlock, exitBlock *BasicBlock) *BasicBlock {
	if block == nil {
		return current
	}

	// Track unreachable code ranges.
	var unreachableStart hir.Node
	var unreachableEnd hir.Node

	for _, node := range block.Nodes {
		if current == nil {
			// We hit unreachable code after return/break/continue.
			if unreachableStart == nil {
				unreachableStart = node
			}
			unreachableEnd = node
			continue
		} else {
			// Report accumulated unreachable code before processing reachable node.
			if unreachableStart != nil {
				b.reportUnreachableCodeRange(unreachableStart, unreachableEnd)
				unreachableStart = nil
				unreachableEnd = nil
			}
		}

		current = b.buildNode(node, current, exitBlock)
	}

	// Report any remaining unreachable code at end of block.
	if unreachableStart != nil {
		b.reportUnreachableCodeRange(unreachableStart, unreachableEnd)
	}

	return current
}

// buildNode processes a single HIR node and returns the next basic block.
func (b *CFGBuilder) buildNode(node hir.Node, current *BasicBlock, exitBlock *BasicBlock) *BasicBlock {
	if node == nil || current == nil {
		return current
	}

	switch n := node.(type) {
	case *hir.ReturnStmt:
		// Mark loop context if inside a loop.
		if b.currentLoop != nil {
			b.currentLoop.hasReturn = true
		}
		return b.buildReturn(n, current, exitBlock)

	case *hir.BreakStmt:
		return b.buildBreak(n, current)

	case *hir.ContinueStmt:
		return b.buildContinue(n, current)

	case *hir.IfStmt:
		return b.buildIf(n, current, exitBlock)

	case *hir.ForStmt:
		return b.buildFor(n, current, exitBlock)

	case *hir.WhileStmt:
		return b.buildWhile(n, current, exitBlock)

	case *hir.MatchStmt:
		return b.buildMatch(n, current, exitBlock)

	case *hir.Block:
		return b.buildBlock(n, current, exitBlock)

	case *hir.DeclStmt:
		// Declarations don't affect control flow.
		current.Nodes = append(current.Nodes, node)
		return current

	default:
		// Regular statements don't affect control flow.
		current.Nodes = append(current.Nodes, node)
		return current
	}
}

// buildReturn handles return statements.
func (b *CFGBuilder) buildReturn(stmt *hir.ReturnStmt, current *BasicBlock, exitBlock *BasicBlock) *BasicBlock {
	current.Nodes = append(current.Nodes, stmt)
	current.Terminator = FlowReturn
	current.Returns = true
	current.CanFallThru = false
	addEdge(current, exitBlock)
	return nil // Code after return is unreachable.
}

// buildBreak handles break statements.
func (b *CFGBuilder) buildBreak(stmt *hir.BreakStmt, current *BasicBlock) *BasicBlock {
	if b.currentLoop == nil {
		b.ctx.Diagnostics.Add(
			diagnostics.NewError("break statement outside loop").
				WithPrimaryLabel(stmt.Loc(), "not inside a loop"),
		)
		return current
	}

	// Mark that this loop has a break (escape path).
	b.currentLoop.hasBreak = true

	current.Nodes = append(current.Nodes, stmt)
	current.Terminator = FlowBreak
	current.CanFallThru = false
	addEdge(current, b.currentLoop.breakTarget)
	return nil // Code after break is unreachable.
}

// buildContinue handles continue statements.
func (b *CFGBuilder) buildContinue(stmt *hir.ContinueStmt, current *BasicBlock) *BasicBlock {
	if b.currentLoop == nil {
		b.ctx.Diagnostics.Add(
			diagnostics.NewError("continue statement outside loop").
				WithPrimaryLabel(stmt.Loc(), "not inside a loop"),
		)
		return current
	}

	current.Nodes = append(current.Nodes, stmt)
	current.Terminator = FlowContinue
	current.CanFallThru = false
	addEdge(current, b.currentLoop.continueTarget)
	return nil // Code after continue is unreachable.
}

// buildIf handles if statements with optional else.
func (b *CFGBuilder) buildIf(stmt *hir.IfStmt, current *BasicBlock, exitBlock *BasicBlock) *BasicBlock {
	// Add condition to current block.
	current.Nodes = append(current.Nodes, stmt)
	current.Terminator = FlowConditional

	// Create blocks for if and else branches.
	ifBlock := b.newBlock()
	ifBlock.Reachable = current.Reachable
	ifBlock.Location = stmt.Body.Loc()
	ifBlock.BranchKind = "if"
	ifBlock.OriginNode = stmt
	addEdge(current, ifBlock)

	// Process if branch.
	afterIf := b.buildBlock(stmt.Body, ifBlock, exitBlock)

	// Process else branch (if exists).
	var afterElse *BasicBlock
	if stmt.Else != nil {
		elseBlock := b.newBlock()
		elseBlock.Reachable = current.Reachable
		elseBlock.Location = stmt.Else.Loc()
		elseBlock.BranchKind = "else"
		elseBlock.OriginNode = stmt
		addEdge(current, elseBlock)

		// Check if else is another if-stmt (else if) or a block.
		if elseIf, ok := stmt.Else.(*hir.IfStmt); ok {
			afterElse = b.buildIf(elseIf, elseBlock, exitBlock)
		} else if elseBlockHIR, ok := stmt.Else.(*hir.Block); ok {
			afterElse = b.buildBlock(elseBlockHIR, elseBlock, exitBlock)
		} else {
			afterElse = b.buildNode(stmt.Else, elseBlock, exitBlock)
		}
	}

	// Merge point after if-else.
	mergeBlock := b.newBlock()
	mergeBlock.Location = stmt.Loc()

	// Connect branches to merge.
	if afterIf != nil && afterIf.CanFallThru {
		addEdge(afterIf, mergeBlock)
		mergeBlock.Reachable = true
	}

	if stmt.Else != nil {
		// Has else branch.
		if afterElse != nil && afterElse.CanFallThru {
			addEdge(afterElse, mergeBlock)
			mergeBlock.Reachable = true
		}

		// If both branches return/break/continue, merge is unreachable.
		bothReturn := (afterIf == nil || !afterIf.CanFallThru) &&
			(afterElse == nil || !afterElse.CanFallThru)
		if bothReturn {
			mergeBlock.Reachable = false
			return nil
		}
	} else {
		// No else branch - always has fallthrough path.
		addEdge(current, mergeBlock)
		mergeBlock.Reachable = true
	}

	return mergeBlock
}

// buildFor handles for loops.
func (b *CFGBuilder) buildFor(stmt *hir.ForStmt, current *BasicBlock, exitBlock *BasicBlock) *BasicBlock {
	// For loops iterate over a range - always finite, cannot be infinite.
	// Create loop header.
	headerBlock := b.newBlock()
	headerBlock.Reachable = current.Reachable
	headerBlock.Location = stmt.Loc()
	headerBlock.Terminator = FlowLoop
	addEdge(current, headerBlock)

	// Create loop body.
	bodyBlock := b.newBlock()
	bodyBlock.Reachable = headerBlock.Reachable
	bodyBlock.Location = stmt.Body.Loc()
	addEdge(headerBlock, bodyBlock)

	// After block (loop exit when range is exhausted).
	afterBlock := b.newBlock()
	afterBlock.Location = stmt.Loc()
	addEdge(headerBlock, afterBlock)

	// Set up loop context for break/continue.
	oldLoop := b.currentLoop
	b.currentLoop = &loopContext{
		breakTarget:    afterBlock,
		continueTarget: headerBlock, // Continue goes back to range check.
		parent:         oldLoop,
	}

	// Build loop body.
	lastBlock := b.buildBlock(stmt.Body, bodyBlock, exitBlock)

	// Connect last block back to header for next iteration.
	if lastBlock != nil && lastBlock.CanFallThru {
		addEdge(lastBlock, headerBlock)
	}

	// Restore loop context.
	b.currentLoop = oldLoop

	afterBlock.Reachable = true // Can always exit when range is exhausted.
	return afterBlock
}

// buildWhile handles while loops.
func (b *CFGBuilder) buildWhile(stmt *hir.WhileStmt, current *BasicBlock, exitBlock *BasicBlock) *BasicBlock {
	// Check if condition is literally true (infinite loop candidate).
	// Missing conditions are handled as a typechecking error; avoid piling on CFG errors.
	isInfiniteCond := isLiteralTrue(stmt.Cond)

	// Extract variables used in the condition.
	conditionVars := extractVariablesFromExpr(stmt.Cond)

	// Create loop header (condition check).
	headerBlock := b.newBlock()
	headerBlock.Reachable = current.Reachable
	headerBlock.Location = stmt.Loc()
	headerBlock.Terminator = FlowLoop
	addEdge(current, headerBlock)

	// Create loop body.
	bodyBlock := b.newBlock()
	bodyBlock.Reachable = headerBlock.Reachable
	bodyBlock.Location = stmt.Body.Loc()
	addEdge(headerBlock, bodyBlock)

	// After block (loop exit).
	afterBlock := b.newBlock()
	afterBlock.Location = stmt.Loc()
	if !isInfiniteCond {
		addEdge(headerBlock, afterBlock) // Condition false exits loop.
	}

	// Set up loop context for break/continue.
	oldLoop := b.currentLoop
	b.currentLoop = &loopContext{
		breakTarget:    afterBlock,
		continueTarget: headerBlock, // Continue goes back to condition.
		parent:         oldLoop,
		loopStmt:       stmt,
		bodyBlock:      bodyBlock,
		hasBreak:       false,
		hasReturn:      false,
	}

	// Process body.
	afterBody := b.buildBlock(stmt.Body, bodyBlock, exitBlock)

	// Check if loop body has break or return (escape paths).
	loopHasBreak := b.currentLoop.hasBreak
	loopHasReturn := b.currentLoop.hasReturn

	// Check if condition variables are modified in the loop body.
	conditionVarsModified := false
	wrongDirectionModifications := make(map[string]bool) // Track if modification goes in wrong direction.

	if len(conditionVars) > 0 {
		modifiedVars := extractModifiedVariables(stmt.Body)
		modificationInfo := extractModificationInfo(stmt.Body)

		for _, condVar := range conditionVars {
			if modifiedVars[condVar] {
				conditionVarsModified = true

				// Check if the modification is in the wrong direction.
				if isWrongDirection(stmt.Cond, condVar, modificationInfo[condVar]) {
					wrongDirectionModifications[condVar] = true
				}
			}
		}
	}

	// Connect body back to header.
	if afterBody != nil && afterBody.CanFallThru {
		addEdge(afterBody, headerBlock)
	}

	// Restore loop context.
	b.currentLoop = oldLoop

	// Check for infinite loop without escape.
	// All of these are ERRORS (not warnings) - they're obvious infinite loops:
	// 1. while true with no break/return - definitely infinite
	// 2. Condition variable modified in WRONG direction AND no break/return - obviously infinite
	// 3. Condition variables NEVER modified AND no break/return - definitely infinite
	if !loopHasBreak && !loopHasReturn {
		if isInfiniteCond {
			// Definite infinite loop: while true { ... }.
			b.ctx.Diagnostics.Add(
				diagnostics.NewError("infinite loop without escape").
					WithPrimaryLabel(stmt.Loc(), "this loop has no way to exit").
					WithNote("add a break statement, return statement, or use a non-constant condition").
					WithHelp("consider adding a break statement inside the loop"),
			)
		} else if len(wrongDirectionModifications) > 0 {
			// Obvious infinite loop: modification goes in wrong direction.
			vars := make([]string, 0, len(wrongDirectionModifications))
			for v := range wrongDirectionModifications {
				vars = append(vars, v)
			}
			b.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("infinite loop: variable(s) %v modified in wrong direction", vars)).
					WithPrimaryLabel(stmt.Loc(), "this loop will never terminate").
					WithNote("the loop condition requires the variable to change in the opposite direction").
					WithHelp("check the loop condition and ensure modifications work toward termination"),
			)
		} else if len(conditionVars) > 0 && !conditionVarsModified {
			// Definite infinite loop: condition variables never modified.
			b.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("infinite loop: variable(s) %v never modified", conditionVars)).
					WithPrimaryLabel(stmt.Loc(), "this loop has no way to exit").
					WithNote("the variables in the loop condition are not modified in the loop body").
					WithHelp("modify the condition variable(s) or add a break statement"),
			)
		}
	}

	afterBlock.Reachable = !isInfiniteCond || loopHasBreak
	return afterBlock
}

// reportUnreachableCodeRange reports a diagnostic for a range of unreachable code.
func (b *CFGBuilder) reportUnreachableCodeRange(start, end hir.Node) {
	if start == nil {
		return
	}

	// Create a location that spans from start to end.
	startLoc := start.Loc()
	endLoc := end.Loc()
	if endLoc == nil {
		endLoc = startLoc
	}

	// Merge locations.
	rangeLocation := &source.Location{
		Filename: startLoc.Filename,
		Start:    startLoc.Start,
		End:      endLoc.End,
	}

	b.ctx.Diagnostics.Add(
		diagnostics.NewError("unreachable code").
			WithPrimaryLabel(rangeLocation, "this code will never execute").
			WithHelp("remove this code or restructure control flow"),
	)
}

// buildMatch handles match statements.
// Match statements are similar to if-else chains - each case is a branch.
func (b *CFGBuilder) buildMatch(stmt *hir.MatchStmt, current *BasicBlock, exitBlock *BasicBlock) *BasicBlock {
	// Add match statement to current block.
	current.Nodes = append(current.Nodes, stmt)
	current.Terminator = FlowConditional

	// Create merge block after all cases.
	mergeBlock := b.newBlock()
	mergeBlock.Location = stmt.Loc()

	// Process each case.
	for _, caseClause := range stmt.Cases {
		// Create block for this case.
		caseBlock := b.newBlock()
		caseBlock.Reachable = current.Reachable
		caseBlock.Location = caseClause.Body.Loc()
		caseBlock.BranchKind = "case"
		caseBlock.OriginNode = stmt
		addEdge(current, caseBlock)

		// Process case body.
		afterCase := b.buildBlock(caseClause.Body, caseBlock, exitBlock)

		// Connect case body to merge block if it can fall through.
		if afterCase != nil && afterCase.CanFallThru {
			addEdge(afterCase, mergeBlock)
			mergeBlock.Reachable = true
		}
	}

	// Return merge block (or nil if no cases can fall through).
	if mergeBlock.Reachable {
		return mergeBlock
	}
	return nil
}

// AnalyzeReturns checks if a function returns in all paths.
func AnalyzeReturns(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	funcDecl *hir.FuncDecl,
	cfg *ControlFlowGraph,
) {
	// Check if function requires a return value.
	requiresReturn := funcDecl.Type != nil &&
		funcDecl.Type.Return != nil &&
		!isVoidType(funcDecl.Type.Return)

	if !requiresReturn {
		return // void functions don't need return.
	}

	// Check if all paths lead to return.
	if !AllPathsReturn(cfg) {
		returnType := "value"
		if funcDecl.Type != nil && funcDecl.Type.Return != nil {
			returnType = funcDecl.Type.Return.String()
		}

		diag := diagnostics.NewError(
			fmt.Sprintf("not all code paths in function '%s' return a value of type %s",
				getFuncName(funcDecl), returnType)).
			WithPrimaryLabel(funcDecl.Name.Loc(),
				"missing return on some paths")

		// Find which branches are missing returns.
		missingBranches := findMissingReturnBranches(cfg)

		if len(missingBranches) > 0 {
			// Add secondary labels for each branch that's missing a return.
			for _, block := range missingBranches {
				if block.Location != nil {
					msg := "missing return in this branch"

					// Determine the branch type based on BranchKind.
					if block.BranchKind == "else" {
						msg = fmt.Sprintf("missing return in else at line %d", block.Location.Start.Line)
					} else if block.BranchKind == "if" {
						msg = fmt.Sprintf("missing return in if at line %d", block.Location.Start.Line)
					} else if block.BranchKind != "" {
						msg = fmt.Sprintf("missing return in %s at line %d", block.BranchKind, block.Location.Start.Line)
					}

					diag.WithSecondaryLabel(block.Location, msg)
				}
			}
		}

		diag.WithHelp("make sure every branch returns, or add a final return at the end of the function")
		ctx.Diagnostics.Add(diag)
	}
}

// AllPathsReturn checks if all paths through the CFG lead to a return.
func AllPathsReturn(cfg *ControlFlowGraph) bool {
	// DFS to check if we can reach exit without returning.
	visited := make(map[*BasicBlock]bool)
	return !canReachExitWithoutReturn(cfg.Entry, cfg.Exit, visited)
}

// MissingReturnBranches returns branch blocks that reach exit without returning.
func MissingReturnBranches(cfg *ControlFlowGraph) []*BasicBlock {
	return findMissingReturnBranches(cfg)
}

// canReachExitWithoutReturn checks if exit is reachable without a return.
func canReachExitWithoutReturn(current, exit *BasicBlock, visited map[*BasicBlock]bool) bool {
	if current == nil || visited[current] {
		return false
	}

	visited[current] = true

	// If we reached exit and this path doesn't return, we found a problem.
	if current == exit {
		return true
	}

	// If this block returns, this path is OK.
	if current.Returns {
		return false
	}

	// Check all successors.
	for _, succ := range current.Successors {
		if canReachExitWithoutReturn(succ, exit, visited) {
			return true
		}
	}

	return false
}

// findMissingReturnBranches identifies blocks that can reach exit without returning.
func findMissingReturnBranches(cfg *ControlFlowGraph) []*BasicBlock {
	// First, find all blocks that can reach exit without returning.
	blocksReachingExit := make(map[*BasicBlock]bool)
	visited := make(map[*BasicBlock]bool)

	var findReachingExit func(*BasicBlock)
	findReachingExit = func(block *BasicBlock) {
		if block == nil || visited[block] || block == cfg.Exit {
			return
		}

		visited[block] = true

		// Check if this block reaches exit without returning.
		for _, succ := range block.Successors {
			if succ == cfg.Exit && !block.Returns {
				blocksReachingExit[block] = true
			}
		}

		// Recurse.
		for _, succ := range block.Successors {
			findReachingExit(succ)
		}
	}

	findReachingExit(cfg.Entry)

	// Now trace backwards from blocks reaching exit to find ALL their branch origins.
	missing := make([]*BasicBlock, 0)
	seen := make(map[*BasicBlock]bool)

	for block := range blocksReachingExit {
		// If this block has a branch kind, it's a branch origin.
		if block.BranchKind != "" {
			if !seen[block] {
				missing = append(missing, block)
				seen[block] = true
			}
			continue
		}

		// Otherwise, trace backwards to find ALL branches that lead here.
		branches := traceToAllBranches(block)
		for _, branchBlock := range branches {
			if !seen[branchBlock] {
				missing = append(missing, branchBlock)
				seen[branchBlock] = true
			}
		}
	}

	return missing
}

// traceToAllBranches traces backwards from a block to find ALL branch origins.
func traceToAllBranches(block *BasicBlock) []*BasicBlock {
	branches := make([]*BasicBlock, 0)
	visited := make(map[*BasicBlock]bool)
	queue := []*BasicBlock{block}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if visited[current] {
			continue
		}
		visited[current] = true

		// If we found a branch, collect it and don't traverse further back.
		if current.BranchKind != "" {
			branches = append(branches, current)
			continue // Don't traverse past branch origins.
		}

		// Otherwise, check all predecessors.
		queue = append(queue, current.Predecessors...)
	}

	return branches
}

// isLiteralTrue checks if an expression is literally the boolean true.
func isLiteralTrue(expr hir.Expr) bool {
	if expr == nil {
		return false
	}

	// Check for Literal with BOOL kind.
	if lit, ok := expr.(*hir.Literal); ok {
		return lit.Kind == hir.LiteralBool && lit.Value == "true"
	}

	// Check for identifier "true" (true/false are identifiers).
	if ident, ok := expr.(*hir.Ident); ok {
		return ident.Name == "true"
	}

	return false
}

// extractVariablesFromExpr extracts all variable names used in an expression.
func extractVariablesFromExpr(expr hir.Expr) []string {
	vars := make([]string, 0)
	if expr == nil {
		return vars
	}

	switch e := expr.(type) {
	case *hir.Ident:
		// Only include if it's not a literal constant (true, false, none).
		if e.Name != "true" && e.Name != "false" && e.Name != "none" {
			vars = append(vars, e.Name)
		}
	case *hir.BinaryExpr:
		vars = append(vars, extractVariablesFromExpr(e.X)...)
		vars = append(vars, extractVariablesFromExpr(e.Y)...)
	case *hir.UnaryExpr:
		vars = append(vars, extractVariablesFromExpr(e.X)...)
	case *hir.SpreadExpr:
		vars = append(vars, extractVariablesFromExpr(e.X)...)
	case *hir.ParenExpr:
		vars = append(vars, extractVariablesFromExpr(e.X)...)
	case *hir.CallExpr:
		vars = append(vars, extractVariablesFromExpr(e.Fun)...)
		for _, arg := range e.Args {
			vars = append(vars, extractVariablesFromExpr(arg)...)
		}
	case *hir.ArrayLenExpr:
		vars = append(vars, extractVariablesFromExpr(e.X)...)
	case *hir.StringLenExpr:
		vars = append(vars, extractVariablesFromExpr(e.X)...)
	case *hir.MapIterInitExpr:
		vars = append(vars, extractVariablesFromExpr(e.Map)...)
	case *hir.MapIterNextExpr:
		vars = append(vars, extractVariablesFromExpr(e.Map)...)
		vars = append(vars, extractVariablesFromExpr(e.Iter)...)
	case *hir.SelectorExpr:
		vars = append(vars, extractVariablesFromExpr(e.X)...)
	case *hir.IndexExpr:
		vars = append(vars, extractVariablesFromExpr(e.X)...)
		vars = append(vars, extractVariablesFromExpr(e.Index)...)
	}

	return vars
}

// extractModifiedVariables extracts all variable names that are modified in a block.
func extractModifiedVariables(block *hir.Block) map[string]bool {
	modified := make(map[string]bool)
	if block == nil {
		return modified
	}

	for _, node := range block.Nodes {
		extractModifiedFromNode(node, modified)
	}

	return modified
}

// extractModifiedFromNode recursively extracts modified variables from a node.
func extractModifiedFromNode(node hir.Node, modified map[string]bool) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *hir.AssignStmt:
		// Extract variable name from left-hand side.
		if ident, ok := n.Lhs.(*hir.Ident); ok {
			modified[ident.Name] = true
		}
	case *hir.VarDecl:
		// Variable declarations with initialization count as modification.
		for _, decl := range n.Decls {
			if decl.Value != nil && decl.Name != nil {
				modified[decl.Name.Name] = true
			}
		}
	case *hir.IfStmt:
		extractModifiedFromNode(n.Body, modified)
		if n.Else != nil {
			extractModifiedFromNode(n.Else, modified)
		}
	case *hir.ForStmt:
		if n.Body != nil {
			extractModifiedFromNode(n.Body, modified)
		}
	case *hir.WhileStmt:
		if n.Body != nil {
			extractModifiedFromNode(n.Body, modified)
		}
	case *hir.Block:
		for _, stmt := range n.Nodes {
			extractModifiedFromNode(stmt, modified)
		}
	case *hir.DeclStmt:
		extractModifiedFromNode(n.Decl, modified)
	case *hir.ExprStmt:
		// Check if expression statement modifies variables (e.g., a++, --a).
		switch expr := n.X.(type) {
		case *hir.PostfixExpr:
			if ident, ok := expr.X.(*hir.Ident); ok {
				modified[ident.Name] = true
			}
		case *hir.PrefixExpr:
			if ident, ok := expr.X.(*hir.Ident); ok {
				modified[ident.Name] = true
			}
		}
	}
}

// ModificationKind represents how a variable is modified.
type ModificationKind int

const (
	ModUnknown    ModificationKind = iota // Unknown modification (complex expression, function call, etc.)
	ModIncrement                          // a++, a = a + 1, a += 1
	ModDecrement                          // a--, a = a - 1, a -= 1
	ModAssignment                         // a = expr (not increment/decrement)
)

// extractModificationInfo extracts detailed modification information for variables.
func extractModificationInfo(block *hir.Block) map[string]ModificationKind {
	info := make(map[string]ModificationKind)
	if block == nil {
		return info
	}

	for _, node := range block.Nodes {
		extractModificationKindFromNode(node, info)
	}

	return info
}

// extractModificationKindFromNode recursively extracts modification kinds from a node.
func extractModificationKindFromNode(node hir.Node, info map[string]ModificationKind) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *hir.AssignStmt:
		if ident, ok := n.Lhs.(*hir.Ident); ok {
			varName := ident.Name

			// Check if it's increment: a = a + 1 or a += 1.
			if binExpr, ok := n.Rhs.(*hir.BinaryExpr); ok {
				switch binExpr.Op.Kind {
				case tokens.PLUS_TOKEN:
					if leftIdent, ok := binExpr.X.(*hir.Ident); ok && leftIdent.Name == varName {
						if lit, ok := binExpr.Y.(*hir.Literal); ok && lit.Value == "1" {
							info[varName] = ModIncrement
							return
						}
					}
				case tokens.MINUS_TOKEN:
					if leftIdent, ok := binExpr.X.(*hir.Ident); ok && leftIdent.Name == varName {
						if lit, ok := binExpr.Y.(*hir.Literal); ok && lit.Value == "1" {
							info[varName] = ModDecrement
							return
						}
					}
				}
			}

			// Otherwise it's a general assignment.
			if _, exists := info[varName]; !exists {
				info[varName] = ModAssignment
			}
		}
	case *hir.PostfixExpr:
		if ident, ok := n.X.(*hir.Ident); ok {
			switch n.Op.Kind {
			case tokens.PLUS_PLUS_TOKEN:
				info[ident.Name] = ModIncrement
			case tokens.MINUS_MINUS_TOKEN:
				info[ident.Name] = ModDecrement
			}
		}
	case *hir.PrefixExpr:
		if ident, ok := n.X.(*hir.Ident); ok {
			switch n.Op.Kind {
			case tokens.PLUS_PLUS_TOKEN:
				info[ident.Name] = ModIncrement
			case tokens.MINUS_MINUS_TOKEN:
				info[ident.Name] = ModDecrement
			}
		}
	case *hir.IfStmt:
		extractModificationKindFromNode(n.Body, info)
		if n.Else != nil {
			extractModificationKindFromNode(n.Else, info)
		}
	case *hir.ForStmt:
		if n.Body != nil {
			extractModificationKindFromNode(n.Body, info)
		}
	case *hir.WhileStmt:
		if n.Body != nil {
			extractModificationKindFromNode(n.Body, info)
		}
	case *hir.Block:
		for _, stmt := range n.Nodes {
			extractModificationKindFromNode(stmt, info)
		}
	case *hir.DeclStmt:
		extractModificationKindFromNode(n.Decl, info)
	case *hir.ExprStmt:
		// Handle expression statements (a++, --a, etc.).
		switch expr := n.X.(type) {
		case *hir.PostfixExpr:
			if ident, ok := expr.X.(*hir.Ident); ok {
				switch expr.Op.Kind {
				case tokens.PLUS_PLUS_TOKEN:
					info[ident.Name] = ModIncrement
				case tokens.MINUS_MINUS_TOKEN:
					info[ident.Name] = ModDecrement
				}
			}
		case *hir.PrefixExpr:
			if ident, ok := expr.X.(*hir.Ident); ok {
				switch expr.Op.Kind {
				case tokens.PLUS_PLUS_TOKEN:
					info[ident.Name] = ModIncrement
				case tokens.MINUS_MINUS_TOKEN:
					info[ident.Name] = ModDecrement
				}
			}
		}
	}
}

// isWrongDirection checks if a variable modification goes in the wrong direction.
// Returns true if the loop will obviously never terminate due to wrong direction.
func isWrongDirection(condition hir.Expr, varName string, modKind ModificationKind) bool {
	if condition == nil || modKind == ModUnknown || modKind == ModAssignment {
		return false // Can't determine for unknown/complex modifications.
	}

	// Extract comparison operator and check if varName is on the left or right.
	if binExpr, ok := condition.(*hir.BinaryExpr); ok {
		var isVarOnLeft bool
		var hasVar bool

		if ident, ok := binExpr.X.(*hir.Ident); ok && ident.Name == varName {
			isVarOnLeft = true
			hasVar = true
		} else if ident, ok := binExpr.Y.(*hir.Ident); ok && ident.Name == varName {
			isVarOnLeft = false
			hasVar = true
		}

		if !hasVar {
			return false
		}

		// Check if modification direction is wrong for the condition.
		// while a > N: needs a--, wrong if a++
		// while a < N: needs a++, wrong if a--
		// while a >= N: needs a--, wrong if a++
		// while a <= N: needs a++, wrong if a--
		op := binExpr.Op.Kind
		if isVarOnLeft {
			switch op {
			case tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN:
				// Need to decrease, so increment is wrong.
				return modKind == ModIncrement
			case tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN:
				// Need to increase, so decrement is wrong.
				return modKind == ModDecrement
			}
		} else {
			// Variable on right side, logic is reversed.
			switch op {
			case tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN:
				// N > a means a needs to increase.
				return modKind == ModDecrement
			case tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN:
				// N < a means a needs to decrease.
				return modKind == ModIncrement
			}
		}
	}

	return false
}

// isVoidType checks if a semantic type represents void.
func isVoidType(typ types.SemType) bool {
	if typ == nil {
		return true
	}

	if typ.Equals(types.TypeVoid) {
		return true
	}

	if prim, ok := types.UnwrapType(typ).(*types.PrimitiveType); ok {
		return prim.GetName() == types.TYPE_VOID
	}

	return false
}

// getFuncName returns the function name or "<anonymous>" for lambdas.
func getFuncName(funcDecl *hir.FuncDecl) string {
	if funcDecl.Name != nil {
		return funcDecl.Name.Name
	}
	return "<anonymous>"
}
