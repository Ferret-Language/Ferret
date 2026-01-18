package parser

import (
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"fmt"
	"strings"
)

// The Parser builds an AST from a token stream.
// The ParseFile function is in the pipeline package to avoid import cycles.

// Parser holds temporary state during parsing of a single file.
// This is created on-the-fly, not stored persistently.
type Parser struct {
	tokens             []tokens.Token
	current            int // current position in tokens
	diagnostics        *diagnostics.DiagnosticBag
	filepath           string
	seenNonImport      bool // Track if we've seen any non-import declaration
	lastNonCommentLine int
	hasLastNonComment  bool
}

// Parse is the internal parsing function called by the pipeline.
// It takes tokens and diagnostics as parameters to avoid import cycles.
func Parse(tokens []tokens.Token, filepath string, diag *diagnostics.DiagnosticBag) *ast.Module {
	parser := &Parser{
		tokens:        tokens,
		current:       0,
		diagnostics:   diag,
		filepath:      filepath,
		seenNonImport: false,
	}

	return parser.parseModule()
}

// parseModule parses the entire module (all top-level declarations)
func (p *Parser) parseModule() *ast.Module {
	module := &ast.Module{
		FullPath: p.filepath,
		Nodes:    []ast.Node{},
	}

	for !p.isAtEnd() {
		node := p.parseTopLevel()
		if node != nil {
			module.Nodes = append(module.Nodes, node)
		}
	}

	return module
}

// parseTopLevel parses a single top-level declaration
func (p *Parser) parseTopLevel() ast.Node {
	doc := p.takeDocComment()
	if p.match(tokens.EOF_TOKEN) {
		return nil
	}
	tok := p.peek()

	switch tok.Kind {
	case tokens.IMPORT_TOKEN:
		imp := p.parseImport()
		// Validate: imports must come before other declarations
		if p.seenNonImport {
			p.diagnostics.Add(
				diagnostics.NewError("import statements must appear before other declarations").
					WithPrimaryLabel(source.NewLocation(&p.filepath, imp.Location.Start, imp.Location.End), "cannot import here"),
			)
			return nil
		}
		return imp
	case tokens.LET_TOKEN:
		p.seenNonImport = true
		decl := p.parseVarDecl()
		p.attachDoc(decl, doc)
		return decl
	case tokens.CONST_TOKEN:
		p.seenNonImport = true
		decl := p.parseConstDecl()
		p.attachDoc(decl, doc)
		return decl
	case tokens.TYPE_TOKEN:
		p.seenNonImport = true
		decl := p.parseTypeDecl()
		p.attachDoc(decl, doc)
		return decl
	case tokens.IF_TOKEN:
		p.seenNonImport = true
		return p.parseIfStmt()
	case tokens.FOR_TOKEN:
		p.seenNonImport = true
		return p.parseForStmt()
	case tokens.WHILE_TOKEN:
		p.seenNonImport = true
		return p.parseWhileStmt()
	case tokens.FUNCTION_TOKEN:
		p.seenNonImport = true
		node := p.parseFuncDecl()
		p.attachDoc(node, doc)

		// if it's a function literal, it is an expression, so we should handle it as such
		if lit, ok := node.(*ast.FuncLit); ok {
			expr := p.parsePostfixAfter(lit)
			p.expect(tokens.SEMICOLON_TOKEN)
			return &ast.ExprStmt{
				X:        expr,
				Location: *p.safeLoc(expr),
			}
		}

		return node
	case tokens.IDENTIFIER_TOKEN:
		p.seenNonImport = true
		return p.parseExprOrAssign()
	// Allow anonymous type declarations at top-level, e.g.:
	// struct { .name: str, .age: i32, };
	// interface { method1(...), ...}
	// enum { A, B, C }
	// We'll parse the type and return a TypeDecl with a nil Name.
	case tokens.STRUCT_TOKEN, tokens.INTERFACE_TOKEN, tokens.ENUM_TOKEN:
		p.seenNonImport = true
		return p.parseAnnonType()
	default:
		// Parser's job is to parse - let parseStmt handle any statement
		// Semantic validation (like "not allowed at module level") happens in collector
		p.seenNonImport = true
		return p.parseStmt()
	}
}

func (p *Parser) parseAnnonType() *ast.TypeDecl {

	typ := p.parseType()

	p.diagnostics.Add(
		diagnostics.NewWarning("annonymous type defined").
			WithPrimaryLabel(p.safeLoc(typ), "remove this type").
			WithNote("types has to be declared with a name to be used"),
	)

	p.expect(tokens.SEMICOLON_TOKEN)

	return &ast.TypeDecl{
		Name:     &ast.IdentifierExpr{Name: "<anonymous>"},
		Type:     typ,
		Location: *source.NewLocation(&p.filepath, p.safeLoc(typ).Start, p.safeLoc(typ).End),
	}
}

func (p *Parser) takeDocComment() *ast.CommentGroup {
	group := p.collectCommentGroup()
	if group == nil {
		return nil
	}
	next := p.peek()
	if next.Kind == tokens.EOF_TOKEN {
		return nil
	}
	if next.Start.Line > group.Location.End.Line+1 {
		return nil
	}
	return group
}

func (p *Parser) collectCommentGroup() *ast.CommentGroup {
	comments := make([]tokens.Token, 0, 1)
	for p.matchRaw(tokens.COMMENT_TOKEN) {
		tok := p.advanceRaw()
		if len(comments) > 0 {
			prev := comments[len(comments)-1]
			if tok.Start.Line > prev.End.Line+1 {
				comments = comments[:0]
			}
		}
		comments = append(comments, tok)
	}
	if len(comments) == 0 {
		return nil
	}
	if p.hasLastNonComment && comments[0].Start.Line == p.lastNonCommentLine {
		return nil
	}
	text := joinCommentText(comments)
	loc := source.NewLocation(&p.filepath, &comments[0].Start, &comments[len(comments)-1].End)
	return &ast.CommentGroup{Text: text, Location: *loc}
}

func joinCommentText(comments []tokens.Token) string {
	parts := make([]string, len(comments))
	for i, comment := range comments {
		parts[i] = comment.Value
	}
	return strings.Join(parts, "\n")
}

func (p *Parser) attachDoc(node ast.Node, doc *ast.CommentGroup) {
	if doc == nil || node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.VarDecl:
		n.Doc = doc
	case *ast.ConstDecl:
		n.Doc = doc
	case *ast.TypeDecl:
		n.Doc = doc
	case *ast.FuncDecl:
		n.Doc = doc
	case *ast.MethodDecl:
		n.Doc = doc
	}
}

// parseImport: import "path";
func (p *Parser) parseImport() *ast.ImportStmt {
	start := p.peek().Start

	p.expect(tokens.IMPORT_TOKEN)

	if !p.match(tokens.STRING_TOKEN) {
		p.error("expected string literal after 'import'")
		return nil
	}

	pathTok := p.advance()

	path := &ast.BasicLit{
		Kind:     ast.STRING,
		Value:    pathTok.Value,
		Location: *source.NewLocation(&p.filepath, &pathTok.Start, &pathTok.End),
	}

	alias := &ast.IdentifierExpr{}

	if p.match(tokens.AS_TOKEN) {
		p.advance()
		al := p.expect(tokens.IDENTIFIER_TOKEN)
		alias.Name = al.Value
		alias.Location = *source.NewLocation(&p.filepath, &al.Start, &al.End)
	}

	p.expect(tokens.SEMICOLON_TOKEN)

	return &ast.ImportStmt{
		Path:     path,
		Alias:    alias,
		Location: p.makeLocation(start),
	}
}

// parseStmt parses a statement
func (p *Parser) parseStmt() ast.Node {
	doc := p.takeDocComment()
	if p.match(tokens.EOF_TOKEN) {
		return nil
	}
	tok := p.peek()

	switch tok.Kind {
	case tokens.LET_TOKEN:
		decl := p.parseVarDecl()
		p.attachDoc(decl, doc)
		return decl
	case tokens.CONST_TOKEN:
		decl := p.parseConstDecl()
		p.attachDoc(decl, doc)
		return decl
	case tokens.TYPE_TOKEN:
		decl := p.parseTypeDecl()
		p.attachDoc(decl, doc)
		return decl
	case tokens.RETURN_TOKEN:
		return p.parseReturnStmt()
	case tokens.DEFER_TOKEN:
		return p.parseDeferStmt()
	case tokens.BREAK_TOKEN:
		return p.parseBreakStmt()
	case tokens.CONTINUE_TOKEN:
		return p.parseContinueStmt()
	case tokens.IF_TOKEN:
		return p.parseIfStmt()
	case tokens.FOR_TOKEN:
		return p.parseForStmt()
	case tokens.WHILE_TOKEN:
		return p.parseWhileStmt()
	case tokens.MATCH_TOKEN:
		return p.parseMatchStmt()
	case tokens.OPEN_CURLY:
		return p.parseBlock()
	case tokens.FUNCTION_TOKEN:
		node := p.parseFuncDecl()
		p.attachDoc(node, doc)
		// if it's a function literal, it is an expression, so we should handle it as such
		if lit, ok := node.(*ast.FuncLit); ok {
			expr := p.parsePostfixAfter(lit)
			p.expect(tokens.SEMICOLON_TOKEN)
			return &ast.ExprStmt{
				X:        expr,
				Location: *p.safeLoc(expr),
			}
		}
		return node
	default:
		return p.parseExprOrAssign()
	}
}

// parseReturnStmt: return expr;
func (p *Parser) parseReturnStmt() *ast.ReturnStmt {

	start := p.expect(tokens.RETURN_TOKEN).Start

	var result ast.Expression
	var isError bool

	if !p.match(tokens.SEMICOLON_TOKEN) {
		result = p.parseExpr()
		if p.match(tokens.NOT_TOKEN) {
			p.advance()
			isError = true
		}
	}

	endToken := p.expect(tokens.SEMICOLON_TOKEN)

	// Calculate end position - use result if available, otherwise use semicolon
	var endPos *source.Position
	if result != nil && p.safeLoc(result) != nil && p.safeLoc(result).End != nil {
		endPos = p.safeLoc(result).End
	} else {
		endPos = &endToken.End
	}

	return &ast.ReturnStmt{
		Result:   result,
		IsError:  isError,
		Location: *source.NewLocation(&p.filepath, &start, endPos),
	}
}

// parseBreakStmt: break;
func (p *Parser) parseBreakStmt() *ast.BreakStmt {
	start := p.expect(tokens.BREAK_TOKEN).Start
	endToken := p.expect(tokens.SEMICOLON_TOKEN)
	return &ast.BreakStmt{
		Location: *source.NewLocation(&p.filepath, &start, &endToken.End),
	}
}

// parseContinueStmt: continue;
func (p *Parser) parseContinueStmt() *ast.ContinueStmt {
	start := p.expect(tokens.CONTINUE_TOKEN).Start
	endToken := p.expect(tokens.SEMICOLON_TOKEN)
	return &ast.ContinueStmt{
		Location: *source.NewLocation(&p.filepath, &start, &endToken.End),
	}
}

// parseDeferStmt: defer funcCall() [catch err { ... }];
func (p *Parser) parseDeferStmt() *ast.DeferStmt {
	start := p.expect(tokens.DEFER_TOKEN).Start

	// Parse the function call expression
	callExpr := p.parseExpr()

	// Ensure it's actually a call expression
	call, ok := callExpr.(*ast.CallExpr)
	if !ok {
		p.diagnostics.Add(
			diagnostics.NewError("defer requires a function call").
				WithPrimaryLabel(p.safeLoc(callExpr), "expected function call here"),
		)
		// Create a dummy call expression for error recovery
		call = &ast.CallExpr{
			Fun:      callExpr,
			Args:     []ast.Expression{},
			Location: *p.safeLoc(callExpr),
		}
	}

	// Extract catch clause from CallExpr if present (parseCallExpr already parsed it)
	// We move it to DeferStmt.Catch and clear it from CallExpr.Catch
	catchClause := call.Catch
	call.Catch = nil // Clear from CallExpr to prevent normal catch validation

	endToken := p.expect(tokens.SEMICOLON_TOKEN)

	return &ast.DeferStmt{
		Call:     call,
		Catch:    catchClause,
		Location: *source.NewLocation(&p.filepath, &start, &endToken.End),
	}
}

// parseIfStmt: if cond { } else { }
func (p *Parser) parseIfStmt() *ast.IfStmt {

	start := p.advance().Start

	cond := p.parseExpr()
	body := p.parseBlock()

	var elseNode ast.Node
	if p.match(tokens.ELSE_TOKEN) {
		p.advance()
		if p.match(tokens.IF_TOKEN) {
			elseNode = p.parseIfStmt()
		} else {
			elseNode = p.parseBlock()
		}
	}

	return &ast.IfStmt{
		Cond:     cond,
		Body:     body,
		Else:     elseNode,
		Location: p.makeLocation(start),
	}
}

// parseExprOrAssign: expr; or expr = expr; or expr += expr; or x++; etc.
func (p *Parser) parseExprOrAssign() ast.Node {

	lhs := p.parseExpr()

	// Handle nil expression (parsing error occurred)
	if lhs == nil {
		lhs = p.invalidExpr()
	}

	// Check for compound assignment operators (x += y, x -= y, etc.)
	compoundOps := []tokens.TOKEN{
		tokens.EQUALS_TOKEN,
		tokens.PLUS_EQUALS_TOKEN,
		tokens.MINUS_EQUALS_TOKEN,
		tokens.MUL_EQUALS_TOKEN,
		tokens.DIV_EQUALS_TOKEN,
		tokens.MOD_EQUALS_TOKEN,
		tokens.EXP_EQUALS_TOKEN,
		tokens.POW_EQUALS_TOKEN,
	}

	if p.match(compoundOps...) {
		op := p.advance()
		rhs := p.parseExpr()
		if rhs == nil {
			rhs = p.invalidExpr()
		}

		p.expect(tokens.SEMICOLON_TOKEN)

		return &ast.AssignStmt{
			Lhs:      lhs,
			Rhs:      rhs,
			Op:       &op,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(lhs).Start, p.safeLoc(rhs).End),
		}
	}

	// Expression statements need semicolons
	p.expect(tokens.SEMICOLON_TOKEN)

	return &ast.ExprStmt{
		X:        lhs,
		Location: p.makeLocation(*p.safeLoc(lhs).Start),
	}
}

// parseExpr parses an expression
func (p *Parser) parseExpr() ast.Expression {
	return p.parseCoalescing()
}

// parseCoalescing parses coalescing operator (a ?? b)
// Coalescing has lower precedence than logical operators and is right-associative
func (p *Parser) parseCoalescing() ast.Expression {
	left := p.parseLogicalOr()
	if left == nil {
		return nil
	}

	if p.match(tokens.COALESCING_TOKEN) {
		p.advance()                  // consume ??
		right := p.parseCoalescing() // Right-associative: recursively parse coalescing on the right
		if right == nil {
			right = p.invalidExpr()
		}
		return &ast.CoalescingExpr{
			Cond:     left,
			Default:  right,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(left).Start, p.safeLoc(right).End),
		}
	}

	return left
}

func (p *Parser) parseLogicalOr() ast.Expression {
	left := p.parseLogicalAnd()
	if left == nil {
		return nil
	}

	for p.match(tokens.OR_TOKEN) {
		op := p.advance()
		right := p.parseLogicalAnd()
		if right == nil {
			right = p.invalidExpr()
		}
		left = &ast.BinaryExpr{
			X:        left,
			Op:       op,
			Y:        right,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(left).Start, p.safeLoc(right).End),
		}
	}

	return left
}

func (p *Parser) parseLogicalAnd() ast.Expression {
	left := p.parseEquality()
	if left == nil {
		return nil
	}

	for p.match(tokens.AND_TOKEN) {
		op := p.advance()
		right := p.parseEquality()
		if right == nil {
			right = p.invalidExpr()
		}
		left = &ast.BinaryExpr{
			X:        left,
			Op:       op,
			Y:        right,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(left).Start, p.safeLoc(right).End),
		}
	}

	return left
}

func (p *Parser) parseEquality() ast.Expression {
	left := p.parseComparison()
	if left == nil {
		return nil
	}

	for p.match(tokens.DOUBLE_EQUAL_TOKEN, tokens.NOT_EQUAL_TOKEN, tokens.IS_TOKEN) {
		op := p.advance()
		var right ast.Expression

		// For 'is' operator, parse the right side as a type
		if op.Kind == tokens.IS_TOKEN {
			typeNode := p.parseType()
			// Wrap the type in a TypeExpr (which implements Expression interface)
			right = &ast.TypeExpr{
				Type:     typeNode,
				Location: *p.safeLoc(typeNode),
			}
		} else {
			right = p.parseComparison()
			if right == nil {
				right = p.invalidExpr()
			}
		}

		left = &ast.BinaryExpr{
			X:        left,
			Op:       op,
			Y:        right,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(left).Start, p.safeLoc(right).End),
		}
	}

	return left
}

func (p *Parser) parseComparison() ast.Expression {
	left := p.parseRange()
	if left == nil {
		return nil
	}

	for p.match(tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN, tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN, tokens.IN_TOKEN) {
		op := p.advance()
		right := p.parseRange()
		if right == nil {
			right = p.invalidExpr()
		}
		left = &ast.BinaryExpr{
			X:        left,
			Op:       op,
			Y:        right,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(left).Start, p.safeLoc(right).End),
		}
	}

	return left
}

// parseRange parses range expressions: start..end, start..=end, or start..end:incr
// This allows ranges to be used as standalone expressions: let arr := 0..10 or 0..=10
func (p *Parser) parseRange() ast.Expression {
	left := p.parseAdditive()
	if left == nil {
		return nil
	}

	// Check for range operators '..' or '..='
	if p.match(tokens.RANGE_TOKEN, tokens.RANGE_INCLUSIVE_TOKEN) {
		inclusive := p.match(tokens.RANGE_INCLUSIVE_TOKEN)
		p.advance()
		end := p.parseAdditive()
		if end == nil {
			end = p.invalidExpr()
		}

		// Check for optional increment (:incr)
		var incr ast.Expression
		if p.match(tokens.COLON_TOKEN) {
			p.advance()
			incr = p.parseAdditive()
			if incr == nil {
				incr = p.invalidExpr()
			}
		}

		endPos := p.safeLoc(end).End
		if incr != nil {
			endPos = p.safeLoc(incr).End
		}

		return &ast.RangeExpr{
			Start:     left,
			End:       end,
			Incr:      incr,
			Inclusive: inclusive,
			Location:  *source.NewLocation(&p.filepath, p.safeLoc(left).Start, endPos),
		}
	}

	return left
}

func (p *Parser) parseAdditive() ast.Expression {
	left := p.parseMultiplicative()
	if left == nil {
		return nil
	}

	for p.match(tokens.PLUS_TOKEN, tokens.MINUS_TOKEN) {
		op := p.advance()
		right := p.parseMultiplicative()
		if right == nil {
			right = p.invalidExpr()
		}
		left = &ast.BinaryExpr{
			X:        left,
			Op:       op,
			Y:        right,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(left).Start, p.safeLoc(right).End),
		}
	}

	return left
}

func (p *Parser) parseMultiplicative() ast.Expression {
	left := p.parseExponentiation()
	if left == nil {
		return nil
	}

	for p.match(tokens.MUL_TOKEN, tokens.DIV_TOKEN, tokens.MOD_TOKEN) {
		op := p.advance()
		right := p.parseExponentiation()
		if right == nil {
			right = p.invalidExpr()
		}
		left = &ast.BinaryExpr{
			X:        left,
			Op:       op,
			Y:        right,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(left).Start, p.safeLoc(right).End),
		}
	}

	return left
}

func (p *Parser) parseExponentiation() ast.Expression {
	left := p.parseUnary()
	if left == nil {
		return nil
	}

	// Right-associative: 2 ** 3 ** 2 = 2 ** (3 ** 2) = 512
	if p.match(tokens.EXP_TOKEN) {
		op := p.advance()
		right := p.parseExponentiation() // Recursive call for right-associativity
		if right == nil {
			right = p.invalidExpr()
		}
		left = &ast.BinaryExpr{
			X:        left,
			Op:       op,
			Y:        right,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(left).Start, p.safeLoc(right).End),
		}
	}

	return left
}

func (p *Parser) parseUnary() ast.Expression {
	return p.parseUnaryDepth(0)
}

// parseUnaryDepth parses unary expressions with depth tracking to prevent stack overflow
func (p *Parser) parseUnaryDepth(depth int) ast.Expression {
	// Limit recursion depth to prevent stack overflow on pathological inputs like "------...x"
	const maxUnaryDepth = 100
	if depth >= maxUnaryDepth {
		tok := p.peek()
		p.diagnostics.Add(
			diagnostics.NewError("too many nested unary operators (maximum 100)").
				WithCode(diagnostics.ErrMax).
				WithPrimaryLabel(source.NewLocation(&p.filepath, &tok.Start, &tok.End), "excessive nesting"),
		)
		return p.parsePostfix()
	}

	// Handle borrow operators: & or &mut
	if p.match(tokens.BIT_AND_TOKEN) {
		op := p.advance()
		isMutable := false

		// Check if followed by 'mut' keyword
		if p.match(tokens.MUT_TOKEN) {
			p.advance()
			isMutable = true
		}

		expr := p.parseUnaryDepth(depth + 1)
		if expr == nil {
			expr = p.invalidExpr()
		}

		// Create a token representing &mut or & for the UnaryExpr
		borrowToken := op
		if isMutable {
			// Create synthetic token for &mut
			borrowToken = tokens.NewToken(tokens.MUT_TOKEN, "&mut", op.Start, op.End)
		}

		return &ast.UnaryExpr{
			Op:       borrowToken,
			X:        expr,
			Location: *source.NewLocation(&p.filepath, &op.Start, p.safeLoc(expr).End),
		}
	}

	// Handle dereference operator: *expr
	if p.match(tokens.MUL_TOKEN) {
		star := p.advance()

		// Check if this is actually a dereference by looking ahead
		// If next is an operator or delimiter, it's likely multiplication
		if p.match(tokens.SEMICOLON_TOKEN, tokens.COMMA_TOKEN, tokens.CLOSE_PAREN,
			tokens.CLOSE_BRACKET, tokens.CLOSE_CURLY, tokens.EOF_TOKEN) {
			// This looks like it should be parsed as primary (incomplete expression)
			p.current-- // Back up
			return p.parsePostfix()
		}

		expr := p.parseUnaryDepth(depth + 1)
		if expr == nil {
			expr = p.invalidExpr()
		}
		return &ast.DerefExpr{
			X:        expr,
			Location: *source.NewLocation(&p.filepath, &star.Start, p.safeLoc(expr).End),
		}
	}

	if p.match(tokens.NOT_TOKEN, tokens.MINUS_TOKEN) {
		op := p.advance()
		expr := p.parseUnaryDepth(depth + 1)
		if expr == nil {
			expr = p.invalidExpr()
		}
		return &ast.UnaryExpr{
			Op:       op,
			X:        expr,
			Location: *source.NewLocation(&p.filepath, &op.Start, p.safeLoc(expr).End),
		}
	}

	if p.match(tokens.AT_TOKEN) {
		op := p.advance()
		expr := p.parseUnaryDepth(depth + 1)
		if expr == nil {
			expr = p.invalidExpr()
		}
		return &ast.UnaryExpr{
			Op:       op,
			X:        expr,
			Location: *source.NewLocation(&p.filepath, &op.Start, p.safeLoc(expr).End),
		}
	}

	if p.match(tokens.HASH_TOKEN) {
		op := p.advance()
		expr := p.parseUnaryDepth(depth + 1)
		if expr == nil {
			expr = p.invalidExpr()
		}
		return &ast.UnaryExpr{
			Op:       op,
			X:        expr,
			Location: *source.NewLocation(&p.filepath, &op.Start, p.safeLoc(expr).End),
		}
	}

	// Prefix increment/decrement: ++a or --a
	if p.match(tokens.PLUS_PLUS_TOKEN, tokens.MINUS_MINUS_TOKEN) {
		op := p.advance()
		expr := p.parseUnaryDepth(depth + 1)
		if expr == nil {
			expr = p.invalidExpr()
		}
		return &ast.PrefixExpr{
			Op:       op,
			X:        expr,
			Location: *source.NewLocation(&p.filepath, &op.Start, p.safeLoc(expr).End),
		}
	}

	return p.parsePostfix()
}

func (p *Parser) parsePostfix() ast.Expression {
	expr := p.parsePrimary()
	return p.parsePostfixAfter(expr)
}

func (p *Parser) parsePostfixAfter(expr ast.Expression) ast.Expression {
	// If parsePrimary returned nil due to error, return nil to prevent nil pointer issues
	if expr == nil {
		return nil
	}

	for !p.isAtEnd() {
		if p.match(tokens.OPEN_PAREN) {
			expr = p.parseCallExpr(expr)
		} else if p.match(tokens.OPEN_BRACKET) {
			expr = p.parseIndexExpr(expr)
		} else if p.match(tokens.DOT_TOKEN) {
			expr = p.parseSelectorExpr(expr)
		} else if p.match(tokens.SCOPE_TOKEN) {
			expr = p.parseScopeResolutionExpr(expr)
		} else if p.match(tokens.AS_TOKEN) {
			// Type casting: expr as Type
			expr = p.parseCastExpr(expr)
		} else if p.match(tokens.PLUS_PLUS_TOKEN, tokens.MINUS_MINUS_TOKEN) {
			// Postfix increment/decrement: expr++ or expr--
			op := p.advance()
			expr = &ast.PostfixExpr{
				X:        expr,
				Op:       op,
				Location: *source.NewLocation(&p.filepath, p.safeLoc(expr).Start, &op.End),
			}
		} else {
			break
		}
	}

	return expr
}

func (p *Parser) parseCallExpr(fun ast.Expression) *ast.CallExpr {

	p.advance() // consume '('
	args := []ast.Expression{}
	if !p.match(tokens.CLOSE_PAREN) {
		args = append(args, p.parseExpr())
		for p.match(tokens.COMMA_TOKEN) {
			p.advance()
			args = append(args, p.parseExpr())
		}
	}

	end := p.expect(tokens.CLOSE_PAREN).End

	// Check for catch clause
	var catchClause *ast.CatchClause
	if p.match(tokens.CATCH_TOKEN) {
		catchClause = p.parseCatchClause()
		end = *catchClause.End
	}

	return &ast.CallExpr{
		Fun:      fun,
		Args:     args,
		Catch:    catchClause,
		Location: *source.NewLocation(&p.filepath, p.safeLoc(fun).Start, &end),
	}
}

// parseCatchClause parses a catch clause: catch [ident] [block] [fallback]
// Examples:
//   - catch 0                    -> just fallback
//   - catch err { ... } 0        -> error handler + fallback
//   - catch { ... } 0            -> handler + fallback
//   - catch err { return 0; }    -> error handler only
func (p *Parser) parseCatchClause() *ast.CatchClause {

	start := p.expect(tokens.CATCH_TOKEN).Start // consume 'catch'

	var errIdent *ast.IdentifierExpr
	var handler *ast.Block
	var fallback ast.Expression

	// Check if we have an error identifier (only when a handler block follows)
	if p.match(tokens.IDENTIFIER_TOKEN) && p.next().Kind == tokens.OPEN_CURLY {
		errIdent = p.parseIdentifier()
	}

	// Check if we have a handler block
	if p.match(tokens.OPEN_CURLY) {
		handler = p.parseBlock()
	}

	// Check if we have a fallback value
	// Fallback is not present if we're at statement-ending tokens
	if !p.match(tokens.SEMICOLON_TOKEN, tokens.COMMA_TOKEN, tokens.CLOSE_PAREN, tokens.CLOSE_BRACKET, tokens.CLOSE_CURLY) && !p.isAtEnd() {
		fallback = p.parseExpr()
	}

	return &ast.CatchClause{
		ErrIdent: errIdent,
		Handler:  handler,
		Fallback: fallback,
		Location: p.makeLocation(start),
	}
}

func (p *Parser) parseIndexExpr(x ast.Expression) *ast.IndexExpr {
	start := p.advance().Start // consume '['
	index := p.parseExpr()
	if index == nil {
		index = p.invalidExpr()
	}
	end := p.expect(tokens.CLOSE_BRACKET).End

	return &ast.IndexExpr{
		X:        x,
		Index:    index,
		Location: *source.NewLocation(&p.filepath, &start, &end),
	}
}

func (p *Parser) parseSelectorExpr(x ast.Expression) *ast.SelectorExpr {
	p.advance() // consume '.'
	sel := p.parseIdentifier()
	return &ast.SelectorExpr{
		X:        x,
		Field:    sel,
		Location: *source.NewLocation(&p.filepath, p.safeLoc(x).Start, p.safeLoc(sel).End),
	}
}

func (p *Parser) parseScopeResolutionExpr(x ast.Expression) *ast.ScopeResolutionExpr {
	p.advance() // consume '::'
	member := p.parseIdentifier()
	return &ast.ScopeResolutionExpr{
		X:        x,
		Selector: member,
		Location: *source.NewLocation(&p.filepath, p.safeLoc(x).Start, p.safeLoc(member).End),
	}
}

func (p *Parser) parseCastExpr(x ast.Expression) *ast.CastExpr {
	p.advance() // consume 'as'
	targetType := p.parseType()
	return &ast.CastExpr{
		X:        x,
		Type:     targetType,
		Location: *source.NewLocation(&p.filepath, p.safeLoc(x).Start, p.safeLoc(targetType).End),
	}
}

func (p *Parser) isCompositeLiteral() bool {

	savedPos := p.current
	p.advance() // consume the {

	// Empty {} or starts with .field = val (struct literal)
	if p.match(tokens.DOT_TOKEN, tokens.CLOSE_CURLY) {
		p.current = savedPos
		return true
	}

	// Check for key => val (map literal)
	// We need to parse ahead to see if there's a =>
	if p.match(tokens.IDENTIFIER_TOKEN, tokens.NUMBER_TOKEN, tokens.STRING_TOKEN, tokens.OPEN_PAREN, tokens.OPEN_BRACKET) {
		// Try to skip past a potential key expression
		p.advance()
		if p.match(tokens.FAT_ARROW_TOKEN) {
			p.current = savedPos
			return true
		}
	}

	p.current = savedPos
	return false
}

func (p *Parser) parseCompositeLiteralElements() []ast.Expression {
	elts := []ast.Expression{}

	if p.match(tokens.CLOSE_CURLY) {
		return elts
	}

	for !(p.match(tokens.CLOSE_CURLY) || p.isAtEnd()) {
		elt := p.parseCompositeLiteralElement()
		if elt != nil {
			elts = append(elts, elt)
		}

		if p.match(tokens.CLOSE_CURLY) {
			break
		}

		if p.checkTrailing(tokens.COMMA_TOKEN, tokens.CLOSE_CURLY, "composite literal") {
			p.advance() // skip the token
			break
		}

		p.expect(tokens.COMMA_TOKEN)
	}

	return elts
}

func (p *Parser) parseCompositeLiteralElement() ast.Expression {
	if p.match(tokens.DOT_TOKEN) {
		p.advance()
		name := p.parseIdentifier()
		p.expect(tokens.EQUALS_TOKEN)
		val := p.parseExpr()
		return &ast.KeyValueExpr{
			Key:      name,
			Value:    val,
			Location: *source.NewLocation(&p.filepath, name.Start, p.safeLoc(val).End),
		}
	}

	key := p.parseExpr()
	if p.match(tokens.FAT_ARROW_TOKEN) {
		p.advance()
		val := p.parseExpr()
		return &ast.KeyValueExpr{
			Key:      key,
			Value:    val,
			Location: *source.NewLocation(&p.filepath, p.safeLoc(key).Start, p.safeLoc(val).End),
		}
	}

	return key
}

func (p *Parser) parsePrimary() ast.Expression {
	tok := p.peek()

	switch tok.Kind {
	case tokens.NUMBER_TOKEN:
		p.advance()
		// Determine if int or float by checking for '.' or 'e'/'E'
		kind := ast.INT
		if strings.ContainsRune(tok.Value, '.') || strings.ContainsRune(tok.Value, 'e') || strings.ContainsRune(tok.Value, 'E') {
			kind = ast.FLOAT
		}
		return &ast.BasicLit{
			Kind:     kind,
			Value:    tok.Value,
			Location: *source.NewLocation(&p.filepath, &tok.Start, &tok.End),
		}

	case tokens.STRING_TOKEN:
		p.advance()
		return &ast.BasicLit{
			Kind:     ast.STRING,
			Value:    tok.Value,
			Location: *source.NewLocation(&p.filepath, &tok.Start, &tok.End),
		}

	case tokens.BYTE_TOKEN:
		p.advance()
		return &ast.BasicLit{
			Kind:     ast.BYTE,
			Value:    tok.Value,
			Location: *source.NewLocation(&p.filepath, &tok.Start, &tok.End),
		}

	case tokens.CHAR_TOKEN:
		p.advance()
		return &ast.BasicLit{
			Kind:     ast.CHAR,
			Value:    tok.Value,
			Location: *source.NewLocation(&p.filepath, &tok.Start, &tok.End),
		}

	case tokens.IDENTIFIER_TOKEN:
		return p.parseIdentifier()

	case tokens.OPEN_PAREN:
		p.advance()
		expr := p.parseExpr()
		p.expect(tokens.CLOSE_PAREN)
		return expr

	case tokens.OPEN_BRACKET:
		return p.parseArrayLiteral()

	case tokens.MAP_TOKEN:
		// Map type in expression context (for composite literals)
		// e.g., map[str]i32{...}
		mapType := p.parseType()
		// MapType implements both TypeExpr() and Expr()
		if expr, ok := mapType.(ast.Expression); ok {
			return expr
		}
		p.error("map type cannot be used as expression")
		return nil

	case tokens.FUNCTION_TOKEN:
		// Anonymous function: fn (params) -> return { body }
		start := p.peek().Start
		p.advance() // consume 'fn'

		// Must have parameters for anonymous function
		if !p.match(tokens.OPEN_PAREN) {
			p.error("anonymous function must have parameters or ()")
			return nil
		}

		// parseFunctionParams will consume the '(' itself
		params := p.parseFunctionParams()
		p.expect(tokens.CLOSE_PAREN)

		// Parse as function literal
		return p.parseFuncLit(start, params)

	case tokens.ENUM_TOKEN:
		// Anonymous enum type in expression context: enum { A, B, C }
		// This allows constructs like: enum { A, B, C }::A
		return p.parseEnumType()

	case tokens.OPEN_CURLY:
		// Anonymous struct/map literal: { .field = value } or { key => value }
		if !p.isCompositeLiteral() {
			p.error("unexpected '{' in expression context")
			return nil
		}
		start := p.advance().Start // consume '{'
		elems := p.parseCompositeLiteralElements()
		end := p.expect(tokens.CLOSE_CURLY).End

		// Create a composite literal with nil type (anonymous)
		return &ast.CompositeLit{
			Type:     nil, // Anonymous struct/map literal
			Elts:     elems,
			Location: *source.NewLocation(&p.filepath, &start, &end),
		}

	default:

		tok := p.peek()

		// Create a detailed error message with helpful labels
		diag := diagnostics.NewError(fmt.Sprintf("unexpected token '%s' in expression", tok.Value)).
			WithCode(diagnostics.ErrUnexpectedToken).
			WithPrimaryLabel(source.NewLocation(&p.filepath, &tok.Start, &tok.End),
				fmt.Sprintf("cannot use '%s' here", tok.Value))

		// Add context-specific help messages
		if tok.Kind == tokens.SEMICOLON_TOKEN {
			diag = diag.WithHelp("Unexpected semicolon - check if previous statement is complete")
		} else {
			diag = diag.WithHelp("Expected a value, identifier, literal, or expression here")
		}

		p.diagnostics.Add(diag)

		end := p.advance().End // Skip only the invalid token

		// Try to continue parsing - maybe the next token starts a valid expression
		// This allows recovery like: let x := . {.a = 1}
		if !p.isAtEnd() {
			// Recursively try to parse what follows as primary expression
			return p.parsePrimary()
		}

		// If at end, return invalid
		return &ast.Invalid{
			Location: *source.NewLocation(&p.filepath, &tok.Start, &end),
		}
	}
}

func (p *Parser) parseArrayLiteral() *ast.CompositeLit {

	start := p.expect(tokens.OPEN_BRACKET).Start

	elems := []ast.Expression{}
	if !p.match(tokens.CLOSE_BRACKET) {
		elem := p.parseExpr()
		if elem != nil {
			elems = append(elems, elem)
		}
		for p.match(tokens.COMMA_TOKEN) {
			p.advance()
			if p.match(tokens.CLOSE_BRACKET) {
				break
			}
			elem := p.parseExpr()
			if elem != nil {
				elems = append(elems, elem)
			}
		}
	}

	end := p.expect(tokens.CLOSE_BRACKET).End

	return &ast.CompositeLit{
		Elts:     elems,
		Location: *source.NewLocation(&p.filepath, &start, &end),
	}
}

func (p *Parser) parseIdentifier() *ast.IdentifierExpr {
	if !p.match(tokens.IDENTIFIER_TOKEN) {
		p.error(fmt.Sprintf("expected identifier, got %s", p.peek().Value))
		return &ast.IdentifierExpr{Name: "<error>"}
	}

	tok := p.advance()
	return &ast.IdentifierExpr{
		Name:     tok.Value,
		Location: *source.NewLocation(&p.filepath, &tok.Start, &tok.End),
	}
}

// Helper methods

func (p *Parser) isAtEnd() bool {
	if p.current >= len(p.tokens) {
		return true
	}
	idx := p.nextNonCommentIndex(p.current)
	if idx >= len(p.tokens) {
		return true
	}
	return p.tokens[idx].Kind == tokens.EOF_TOKEN
}

func (p *Parser) peek() tokens.Token {
	idx := p.nextNonCommentIndex(p.current)
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[idx]
}

func (p *Parser) previous() tokens.Token {
	return p.tokens[p.current-1]
}

func (p *Parser) next() tokens.Token {
	idx := p.nextNonCommentIndex(p.current)
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	idx = p.nextNonCommentIndex(idx + 1)
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[idx]
}

func (p *Parser) advance() tokens.Token {
	idx := p.nextNonCommentIndex(p.current)
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	p.current = idx + 1
	tok := p.tokens[idx]
	if tok.Kind != tokens.COMMENT_TOKEN {
		p.lastNonCommentLine = tok.End.Line
		p.hasLastNonComment = true
	}
	return tok
}

func (p *Parser) peekRaw() tokens.Token {
	if p.current >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.current]
}

func (p *Parser) advanceRaw() tokens.Token {
	if p.current >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	p.current++
	tok := p.tokens[p.current-1]
	if tok.Kind != tokens.COMMENT_TOKEN {
		p.lastNonCommentLine = tok.End.Line
		p.hasLastNonComment = true
	}
	return tok
}

func (p *Parser) matchRaw(kinds ...tokens.TOKEN) bool {
	for _, kind := range kinds {
		if p.peekRaw().Kind == kind {
			return true
		}
	}
	return false
}

func (p *Parser) match(kinds ...tokens.TOKEN) bool {
	for _, kind := range kinds {
		if p.peek().Kind == kind {
			return true
		}
	}
	return false
}

func (p *Parser) expect(kind tokens.TOKEN) tokens.Token {
	return p.expectError(kind, fmt.Sprintf("unexpected token %v, expected %s", p.peek().Value, kind))
}

func (p *Parser) expectError(kind tokens.TOKEN, msg string) tokens.Token {

	if p.match(kind) {
		return p.advance()
	}

	if kind == tokens.SEMICOLON_TOKEN {
		//tok := p.peek()
		// p.diagnostics.Add(
		// 	diagnostics.NewError(fmt.Sprintf("expected ';' at end of statement, got %s", tok.Value)).
		// 		WithCode(diagnostics.ErrUnterminatedExpr).
		// 		WithPrimaryLabel(source.NewLocation(&p.filepath, &tok.Start, &tok.End), "missing semicolon").
		// 		WithNote("Every statement must end with a semicolon"),
		// )

		// highlight the end of the previous token as the likely location
		prevTok := p.previous()
		loc := source.NewLocation(&p.filepath, &prevTok.End, &prevTok.End)
		p.diagnostics.Add(
			diagnostics.NewError("expected ';' at end of statement").
				WithCode(diagnostics.ErrUnterminatedExpr).
				WithPrimaryLabel(loc, "add semicolon here").
				WithNote("Every statement must end with a semicolon"),
		)

		return p.peek()
	}

	p.error(msg)
	return p.advance()
}

// error reports a parsing error to the diagnostics
func (p *Parser) error(msg string) {
	tok := p.peek()
	loc := source.NewLocation(&p.filepath, &tok.Start, &tok.End)
	p.diagnostics.Add(
		diagnostics.NewError(msg).
			WithCode(diagnostics.ErrUnexpectedToken).
			WithPrimaryLabel(loc, ""),
	)
}

// safeLoc safely gets the location from an expression, returning a fallback if nil
func (p *Parser) safeLoc(expr ast.Node) *source.Location {
	if expr == nil || expr.Loc() == nil {
		tok := p.peek()
		return source.NewLocation(&p.filepath, &tok.Start, &tok.End)
	}
	return expr.Loc()
}

// invalidExpr creates an Invalid expression node at the current position
func (p *Parser) invalidExpr() *ast.Invalid {
	tok := p.peek()
	return &ast.Invalid{
		Location: *source.NewLocation(&p.filepath, &tok.Start, &tok.End),
	}
}

func (p *Parser) nextNonCommentIndex(start int) int {
	idx := start
	for idx < len(p.tokens) && p.tokens[idx].Kind == tokens.COMMENT_TOKEN {
		idx++
	}
	return idx
}

// checkTrailing checks if the current position is at a closing delimiter
// after a comma was just consumed, and emits an info diagnostic if so.
// This provides a consistent message across all composite structures.
//
// This function should be called AFTER consuming a comma/separator token.
//
// Parameters:
//   - closingToken: The expected closing token (e.g., CLOSE_CURLY, CLOSE_PAREN)
//   - contextName: Name of the context for the diagnostic message (e.g., "struct literal", "map literal")
//
// Returns true if we're at the closing delimiter (indicating a trailing comma was found).
func (p *Parser) checkTrailing(target, closingToken tokens.TOKEN, contextName string) bool {

	if p.match(target) && p.next().Kind == closingToken {
		// We just consumed a comma and we're at the closing delimiter
		// This means there was a trailing comma
		token := p.peek()
		loc := source.NewLocation(&p.filepath, &token.Start, &token.End)
		p.diagnostics.Add(
			diagnostics.NewInfo(fmt.Sprintf("trailing %s in %s", target, contextName)).
				WithCode(diagnostics.InfoTrailingComma).
				WithPrimaryLabel(loc, fmt.Sprintf("remove this trailing `%s`", target)).
				WithNote(fmt.Sprintf("Trailing %s are allowed but excluding it keeps code clean and consistent", target)),
		)
		return true
	}
	return false
}

// makeLocation creates a source location from start to current position
func (p *Parser) makeLocation(start source.Position) source.Location {
	end := p.previous().End
	return *source.NewLocation(&p.filepath, &start, &end)
}
