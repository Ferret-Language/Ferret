package cfg

import (
	"compiler/internal/core/source"
	"compiler/internal/ir/hir"
)

type Module struct {
	Functions []*Function
}

type Function struct {
	Name   string
	Entry  *Block
	Exit   *Block
	Blocks []*Block
	Locals LocalSet
	Source *hir.Func
}

type Block struct {
	ID           int
	Stmts        []hir.Stmt
	Terminator   Terminator
	Reachable    bool
	Returns      bool
	Location     source.Location
	BranchKind   string
	Predecessors []*Block
	Use          LocalSet
	Def          LocalSet
	LiveIn       LocalSet
	LiveOut      LocalSet
}

type Terminator interface {
	terminatorNode()
	Successors() []*Block
}

type JumpTerm struct {
	Target *Block
}

func (*JumpTerm) terminatorNode() {}
func (t *JumpTerm) Successors() []*Block {
	if t == nil || t.Target == nil {
		return nil
	}
	return []*Block{t.Target}
}

type BranchTerm struct {
	Cond  hir.Expr
	True  *Block
	False *Block
}

func (*BranchTerm) terminatorNode() {}
func (t *BranchTerm) Successors() []*Block {
	out := make([]*Block, 0, 2)
	if t != nil && t.True != nil {
		out = append(out, t.True)
	}
	if t != nil && t.False != nil {
		out = append(out, t.False)
	}
	return out
}

type SwitchEdge struct {
	Expr   hir.Expr
	Target *Block
}

type SwitchTerm struct {
	Value   hir.Expr
	Cases   []SwitchEdge
	Default *Block
}

func (*SwitchTerm) terminatorNode() {}
func (t *SwitchTerm) Successors() []*Block {
	if t == nil {
		return nil
	}
	out := make([]*Block, 0, len(t.Cases)+1)
	for _, edge := range t.Cases {
		if edge.Target != nil {
			out = append(out, edge.Target)
		}
	}
	if t.Default != nil {
		out = append(out, t.Default)
	}
	return out
}

type ReturnTerm struct {
	Value   hir.Expr
	Cleanup *Block
}

func (*ReturnTerm) terminatorNode() {}
func (t *ReturnTerm) Successors() []*Block {
	if t == nil || t.Cleanup == nil {
		return nil
	}
	return []*Block{t.Cleanup}
}

type PanicTerm struct {
	Value   hir.Expr
	Cleanup *Block
}

func (*PanicTerm) terminatorNode() {}
func (t *PanicTerm) Successors() []*Block {
	if t == nil || t.Cleanup == nil {
		return nil
	}
	return []*Block{t.Cleanup}
}
