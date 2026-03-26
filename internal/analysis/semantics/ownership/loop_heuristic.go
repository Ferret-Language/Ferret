package ownership

import (
	"strconv"

	cfg "compiler/internal/analysis/cfg/model"
	"compiler/internal/ir/mir"
)

func (a *analyzer) shouldSkipBackedgeForSingleIterationLoop(cfgFn *cfg.Function, blocks map[int]*mir.Block, from, to *cfg.Block) bool {
	if cfgFn == nil || from == nil || to == nil || blocks == nil {
		return false
	}
	if !cfgEdgeInCycle(to, from) {
		return false
	}
	head := blocks[to.ID]
	body := blocks[from.ID]
	if head == nil || body == nil {
		return false
	}
	inductionID, condOp, condLimit, ok := loopConditionInfo(head)
	if !ok {
		return false
	}
	step, ok := loopStepInfo(body, a, inductionID)
	if !ok {
		return false
	}
	init, ok := loopInitInfo(to, from, blocks, a, inductionID)
	if !ok {
		return false
	}
	return loopFallsFalseAfterOneIteration(init, step, condLimit, condOp)
}

func cfgEdgeInCycle(from, to *cfg.Block) bool {
	if from == nil || to == nil {
		return false
	}
	seen := make(map[int]struct{})
	stack := []*cfg.Block{from}
	for len(stack) > 0 {
		n := len(stack) - 1
		cur := stack[n]
		stack = stack[:n]
		if cur == nil {
			continue
		}
		if cur.ID == to.ID {
			return true
		}
		if _, ok := seen[cur.ID]; ok {
			continue
		}
		seen[cur.ID] = struct{}{}
		if cur.Terminator == nil {
			continue
		}
		for _, succ := range cur.Terminator.Successors() {
			if succ != nil {
				stack = append(stack, succ)
			}
		}
	}
	return false
}

func loopConditionInfo(head *mir.Block) (localID int, op string, limit int64, ok bool) {
	if head == nil {
		return -1, "", 0, false
	}
	branch, ok := head.Terminator.(*mir.BranchTerm)
	if !ok || branch == nil {
		return -1, "", 0, false
	}
	condLocal, ok := branch.Cond.(*mir.LocalValue)
	if !ok || condLocal == nil {
		return -1, "", 0, false
	}
	for i := len(head.Instructions) - 1; i >= 0; i-- {
		switch inst := head.Instructions[i].(type) {
		case *mir.ComputeInstr:
			if inst.TargetID != condLocal.LocalID {
				continue
			}
			if localID, op, limit, ok = compareLocalAgainstConst(resolveComputedValueBefore(head, i, inst.Value)); ok {
				return localID, op, limit, true
			}
		case *mir.AssignInstr:
			if inst.TargetID != condLocal.LocalID {
				continue
			}
			if localID, op, limit, ok = compareLocalAgainstConst(resolveComputedValueBefore(head, i, inst.Value)); ok {
				return localID, op, limit, true
			}
		}
	}
	return -1, "", 0, false
}

func compareLocalAgainstConst(value mir.Value) (localID int, op string, limit int64, ok bool) {
	bin, ok := value.(*mir.BinaryValue)
	if !ok || bin == nil {
		return -1, "", 0, false
	}
	switch bin.Op {
	case "<", "<=", ">", ">=":
	default:
		return -1, "", 0, false
	}
	if left, lok := bin.Left.(*mir.LocalValue); lok {
		if n, nok := literalInt64(bin.Right); nok {
			return left.LocalID, bin.Op, n, true
		}
	}
	if right, rok := bin.Right.(*mir.LocalValue); rok {
		if n, nok := literalInt64(bin.Left); nok {
			return right.LocalID, reverseCompareOp(bin.Op), n, true
		}
	}
	return -1, "", 0, false
}

func reverseCompareOp(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	default:
		return op
	}
}

func loopStepInfo(body *mir.Block, a *analyzer, localID int) (int64, bool) {
	if body == nil || a == nil || localID < 0 {
		return 0, false
	}
	for i := len(body.Instructions) - 1; i >= 0; i-- {
		switch inst := body.Instructions[i].(type) {
		case *mir.AssignInstr:
			if inst.TargetID != localID {
				continue
			}
			return inductionStep(body, i, inst.Value, localID)
		case *mir.StoreInstr:
			root, path, ok := a.localPlacePath(inst.Target)
			if !ok || path != "" || root != localID {
				continue
			}
			return inductionStep(body, i, inst.Value, localID)
		}
	}
	return 0, false
}

func inductionStep(block *mir.Block, before int, value mir.Value, localID int) (int64, bool) {
	value = resolveComputedValueBefore(block, before, value)
	bin, ok := value.(*mir.BinaryValue)
	if !ok || bin == nil {
		return 0, false
	}
	switch bin.Op {
	case "+":
		if id, ok := localValueID(bin.Left); ok && id == localID {
			if k, ok := literalInt64(bin.Right); ok {
				return k, true
			}
		}
		if id, ok := localValueID(bin.Right); ok && id == localID {
			if k, ok := literalInt64(bin.Left); ok {
				return k, true
			}
		}
	case "-":
		if id, ok := localValueID(bin.Left); ok && id == localID {
			if k, ok := literalInt64(bin.Right); ok {
				return -k, true
			}
		}
	}
	return 0, false
}

func localValueID(value mir.Value) (int, bool) {
	local, ok := value.(*mir.LocalValue)
	if !ok || local == nil {
		return -1, false
	}
	return local.LocalID, true
}

func loopInitInfo(head, backedge *cfg.Block, blocks map[int]*mir.Block, a *analyzer, localID int) (int64, bool) {
	if head == nil || a == nil || localID < 0 {
		return 0, false
	}
	for _, pred := range head.Predecessors {
		if pred == nil || (backedge != nil && pred.ID == backedge.ID) {
			continue
		}
		block := blocks[pred.ID]
		if block == nil {
			continue
		}
		if init, ok := lastAssignedLocalConst(block, a, localID); ok {
			return init, true
		}
	}
	return 0, false
}

func lastAssignedLocalConst(block *mir.Block, a *analyzer, localID int) (int64, bool) {
	if block == nil || a == nil || localID < 0 {
		return 0, false
	}
	for i := len(block.Instructions) - 1; i >= 0; i-- {
		switch inst := block.Instructions[i].(type) {
		case *mir.AssignInstr:
			if inst.TargetID != localID {
				continue
			}
			return literalInt64(resolveComputedValueBefore(block, i, inst.Value))
		case *mir.StoreInstr:
			root, path, ok := a.localPlacePath(inst.Target)
			if !ok || path != "" || root != localID {
				continue
			}
			return literalInt64(resolveComputedValueBefore(block, i, inst.Value))
		}
	}
	return 0, false
}

func resolveComputedValueBefore(block *mir.Block, before int, value mir.Value) mir.Value {
	if block == nil || before <= 0 {
		return value
	}
	seen := map[int]struct{}{}
	for {
		local, ok := value.(*mir.LocalValue)
		if !ok || local == nil {
			return value
		}
		if _, dup := seen[local.LocalID]; dup {
			return value
		}
		seen[local.LocalID] = struct{}{}
		resolved := false
		for i := before - 1; i >= 0; i-- {
			inst, ok := block.Instructions[i].(*mir.ComputeInstr)
			if !ok || inst.TargetID != local.LocalID {
				continue
			}
			value = inst.Value
			before = i
			resolved = true
			break
		}
		if !resolved {
			return value
		}
	}
}

func literalInt64(value mir.Value) (int64, bool) {
	num, ok := value.(*mir.NumberValue)
	if !ok || num == nil {
		return 0, false
	}
	i, err := strconv.ParseInt(num.Value, 10, 64)
	if err != nil {
		return 0, false
	}
	return i, true
}

func loopFallsFalseAfterOneIteration(init, step, limit int64, op string) bool {
	switch op {
	case "<":
		return step > 0 && init < limit && init+step >= limit
	case "<=":
		return step > 0 && init <= limit && init+step > limit
	case ">":
		return step < 0 && init > limit && init+step <= limit
	case ">=":
		return step < 0 && init >= limit && init+step < limit
	default:
		return false
	}
}
