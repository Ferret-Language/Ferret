package typechecker

import (
	"math/big"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/frontend/ast"
	"compiler/internal/utils/numeric"
)

func (c *checker) constExpr(mod *context.Module, expr ast.Expr, seen map[ast.Node]bool) (typeinfo.ConstValue, bool) {
	return c.constExprIn(mod, expr, &constEvalState{
		env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
		seen:     seen,
		activeFn: make(map[*ast.FuncDecl]bool),
	})
}

type constEvalState struct {
	parent   *constEvalState
	env      map[symbols.SymbolID]typeinfo.ConstValue
	seen     map[ast.Node]bool
	activeFn map[*ast.FuncDecl]bool
}

func (s *constEvalState) scoped() *constEvalState {
	if s == nil {
		return &constEvalState{
			env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
			activeFn: make(map[*ast.FuncDecl]bool),
		}
	}
	return &constEvalState{
		parent:   s,
		env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
		seen:     s.seen,
		activeFn: s.activeFn,
	}
}

func (s *constEvalState) callFrame() *constEvalState {
	if s == nil {
		return &constEvalState{
			env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
			activeFn: make(map[*ast.FuncDecl]bool),
		}
	}
	return &constEvalState{
		env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
		seen:     s.seen,
		activeFn: s.activeFn,
	}
}

func (s *constEvalState) lookup(sym *symbols.Symbol) (typeinfo.ConstValue, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.env == nil || sym == nil {
			continue
		}
		if value, ok := cur.env[sym.ID]; ok {
			return value, true
		}
	}
	return typeinfo.ConstValue{}, false
}

func (s *constEvalState) bind(sym *symbols.Symbol, value typeinfo.ConstValue) {
	if s == nil || sym == nil || !value.Valid() {
		return
	}
	if s.env == nil {
		s.env = make(map[symbols.SymbolID]typeinfo.ConstValue)
	}
	s.env[sym.ID] = value
}

func (s *constEvalState) assign(sym *symbols.Symbol, value typeinfo.ConstValue) bool {
	if s == nil || sym == nil || !value.Valid() {
		return false
	}
	for cur := s; cur != nil; cur = cur.parent {
		if cur.env == nil {
			continue
		}
		if _, ok := cur.env[sym.ID]; ok {
			cur.env[sym.ID] = value
			return true
		}
	}
	return false
}

type constFlow uint8

const (
	constFlowNone constFlow = iota
	constFlowReturn
	constFlowBreak
	constFlowContinue
)

type constExecResult struct {
	value typeinfo.ConstValue
	flow  constFlow
	ok    bool
}

const constEvalMaxLoopIterations = 10000

func (c *checker) constExprIn(mod *context.Module, expr ast.Expr, state *constEvalState) (typeinfo.ConstValue, bool) {
	switch e := expr.(type) {
	case nil:
		return typeinfo.ConstValue{}, false
	case *ast.NumberLit:
		value, err := numeric.StringToBigInt(e.Value)
		if err != nil {
			return typeinfo.ConstValue{}, false
		}
		return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: value}, true
	case *ast.StringLit:
		return typeinfo.ConstValue{Kind: typeinfo.ConstString, String: e.Value}, true
	case *ast.NoneLit:
		return typeinfo.ConstValue{Kind: typeinfo.ConstNone}, true
	case *ast.Ident:
		switch e.Text() {
		case "_":
			return typeinfo.ConstValue{}, false
		case "true":
			return typeinfo.ConstValue{Kind: typeinfo.ConstBool, Bool: true}, true
		case "false":
			return typeinfo.ConstValue{Kind: typeinfo.ConstBool, Bool: false}, true
		case "none":
			return typeinfo.ConstValue{Kind: typeinfo.ConstNone}, true
		}
		res := c.lookupTypeResolution(mod, e)
		if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
			return typeinfo.ConstValue{}, false
		}
		if value, ok := state.lookup(res.Symbol); ok {
			return value, true
		}
		if res.Symbol.Node == nil {
			return typeinfo.ConstValue{}, false
		}
		owner := c.findModuleForSymbol(res.Symbol)
		if owner == nil {
			owner = mod
		}
		info := c.info
		if owner != nil && owner != c.mod {
			info = owner.Types
		}
		if info != nil {
			if value, ok := info.LookupConstValue(res.Symbol.Node); ok {
				return value, true
			}
		}
		if res.Symbol.Kind != symbols.SymbolConst {
			return typeinfo.ConstValue{}, false
		}
		if state.seen == nil {
			state.seen = make(map[ast.Node]bool)
		}
		if state.seen[res.Symbol.Node] {
			return typeinfo.ConstValue{}, false
		}
		state.seen[res.Symbol.Node] = true
		defer delete(state.seen, res.Symbol.Node)
		switch n := res.Symbol.Node.(type) {
		case *ast.ConstDecl:
			return c.constExprIn(owner, n.Value, state)
		case *ast.ConstStmt:
			return c.constExprIn(owner, n.Value, state)
		default:
			return typeinfo.ConstValue{}, false
		}
	case *ast.PrefixExpr:
		right, ok := c.constExprIn(mod, e.Right, state)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		return typeinfo.ApplyConstUnary(e.Op, right)
	case *ast.BinaryExpr:
		left, ok := c.constExprIn(mod, e.Left, state)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		right, ok := c.constExprIn(mod, e.Right, state)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		return typeinfo.ApplyConstBinary(e.Op, left, right)
	case *ast.CallExpr:
		return c.constCall(mod, e, state)
	case *ast.CompositeLit:
		if !e.Tuple {
			return typeinfo.ConstValue{}, false
		}
		elems := make([]typeinfo.ConstValue, 0, len(e.Items))
		for _, item := range e.Items {
			value, ok := c.constExprIn(mod, item.Value, state)
			if !ok {
				return typeinfo.ConstValue{}, false
			}
			elems = append(elems, value)
		}
		return typeinfo.ConstValue{Kind: typeinfo.ConstSequence, Elems: elems}, true
	case *ast.IndexExpr:
		base, ok := c.constExprIn(mod, e.Left, state)
		if !ok || base.Kind != typeinfo.ConstSequence {
			return typeinfo.ConstValue{}, false
		}
		index, ok := c.constExprIn(mod, e.Index, state)
		if !ok || index.Kind != typeinfo.ConstInt || index.Int == nil || !index.Int.IsInt64() {
			return typeinfo.ConstValue{}, false
		}
		i := int(index.Int.Int64())
		if i < 0 || i >= len(base.Elems) {
			return typeinfo.ConstValue{}, false
		}
		return base.Elems[i], true
	case *ast.CastExpr:
		return c.constExprIn(mod, e.Left, state)
	default:
		return typeinfo.ConstValue{}, false
	}
}

func (c *checker) constCall(mod *context.Module, call *ast.CallExpr, state *constEvalState) (typeinfo.ConstValue, bool) {
	if call == nil || state == nil {
		return typeinfo.ConstValue{}, false
	}
	args := make([]typeinfo.ConstValue, 0, len(call.Args))
	for _, arg := range call.Args {
		value, ok := c.constExprIn(mod, arg, state)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		args = append(args, value)
	}
	if c.isForeignLenCall(call.Callee) {
		if len(args) != 1 {
			return typeinfo.ConstValue{}, false
		}
		switch args[0].Kind {
		case typeinfo.ConstString:
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(len(args[0].String)))}, true
		case typeinfo.ConstSequence:
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(len(args[0].Elems)))}, true
		default:
			return typeinfo.ConstValue{}, false
		}
	}
	res := c.lookupTypeResolution(mod, call.Callee)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return typeinfo.ConstValue{}, false
	}
	fn, ok := res.Symbol.Node.(*ast.FuncDecl)
	if !ok || fn == nil || fn.IsExtern || fn.Body == nil || len(call.TypeArgs) != 0 {
		return typeinfo.ConstValue{}, false
	}
	owner := c.findModuleForSymbol(res.Symbol)
	if owner == nil {
		owner = mod
	}
	if state.activeFn[fn] {
		return typeinfo.ConstValue{}, false
	}
	frame := state.callFrame()
	state.activeFn[fn] = true
	defer delete(state.activeFn, fn)
	if len(fn.Params) != len(args) {
		return typeinfo.ConstValue{}, false
	}
	for i, param := range fn.Params {
		res := c.lookupTypeResolution(owner, param.Name)
		if res == nil || res.Symbol == nil {
			return typeinfo.ConstValue{}, false
		}
		frame.bind(res.Symbol, args[i])
	}
	result := c.constBlockResult(owner, fn.Body, frame)
	if !result.ok || result.flow != constFlowReturn {
		return typeinfo.ConstValue{}, false
	}
	return result.value, true
}

func (c *checker) constBlockResult(mod *context.Module, block *ast.BlockStmt, state *constEvalState) constExecResult {
	if block == nil {
		return constExecResult{ok: false}
	}
	scope := state.scoped()
	for _, stmt := range block.Stmts {
		result := c.constStmtResult(mod, stmt, scope)
		if !result.ok || result.flow != constFlowNone {
			return result
		}
	}
	return constExecResult{ok: true}
}

func (c *checker) constStmtResult(mod *context.Module, stmt ast.Stmt, state *constEvalState) constExecResult {
	switch s := stmt.(type) {
	case nil:
		return constExecResult{ok: true}
	case *ast.BlockStmt:
		return c.constBlockResult(mod, s, state)
	case *ast.LetStmt:
		value, ok := c.constExprIn(mod, s.Value, state)
		if !ok {
			return constExecResult{ok: false}
		}
		if res := c.lookupTypeResolution(mod, s.Name); res != nil && res.Symbol != nil {
			state.bind(res.Symbol, value)
		}
		return constExecResult{ok: true}
	case *ast.ConstStmt:
		value, ok := c.constExprIn(mod, s.Value, state)
		if !ok {
			return constExecResult{ok: false}
		}
		if res := c.lookupTypeResolution(mod, s.Name); res != nil && res.Symbol != nil {
			state.bind(res.Symbol, value)
		}
		return constExecResult{ok: true}
	case *ast.ReturnStmt:
		value, ok := c.constExprIn(mod, s.Value, state)
		return constExecResult{value: value, flow: constFlowReturn, ok: ok}
	case *ast.ExprStmt:
		_, ok := c.constExprIn(mod, s.Value, state)
		return constExecResult{ok: ok}
	case *ast.AssignStmt:
		value, ok := c.constExprIn(mod, s.Right, state)
		if !ok {
			return constExecResult{ok: false}
		}
		ident, ok := s.Left.(*ast.Ident)
		if !ok {
			return constExecResult{ok: false}
		}
		res := c.lookupTypeResolution(mod, ident)
		if res == nil || res.Symbol == nil || !state.assign(res.Symbol, value) {
			return constExecResult{ok: false}
		}
		return constExecResult{ok: true}
	case *ast.IfStmt:
		cond, ok := c.constExprIn(mod, s.Cond, state)
		if !ok || cond.Kind != typeinfo.ConstBool {
			return constExecResult{ok: false}
		}
		if cond.Bool {
			return c.constBlockResult(mod, s.Then, state)
		}
		if s.Else == nil {
			return constExecResult{ok: true}
		}
		return c.constStmtResult(mod, s.Else, state)
	case *ast.WhileStmt:
		for i := 0; i < constEvalMaxLoopIterations; i++ {
			cond, ok := c.constExprIn(mod, s.Cond, state)
			if !ok || cond.Kind != typeinfo.ConstBool {
				return constExecResult{ok: false}
			}
			if !cond.Bool {
				return constExecResult{ok: true}
			}
			result := c.constBlockResult(mod, s.Body, state)
			if !result.ok {
				return result
			}
			switch result.flow {
			case constFlowNone, constFlowContinue:
				continue
			case constFlowBreak:
				return constExecResult{ok: true}
			case constFlowReturn:
				return result
			}
		}
		return constExecResult{ok: false}
	case *ast.BreakStmt:
		return constExecResult{flow: constFlowBreak, ok: true}
	case *ast.ContinueStmt:
		return constExecResult{flow: constFlowContinue, ok: true}
	default:
		return constExecResult{ok: false}
	}
}
