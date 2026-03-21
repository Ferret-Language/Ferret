package ownershipv2

import (
	cfg "compiler/internal/analysis/cfg/model"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/source"
	"maps"
)

type valueInfo struct {
	typ       typeinfo.Type
	mutable   bool
	constant  bool
	moved     bool
	moveLoc   source.Location
	movedPath string
	movedSubs map[string]source.Location
	frozen    int
	mutBorrow bool
	borrowOf  int
	borrowMut bool
	borrowLoc source.Location
}

type valueScope struct {
	values map[int]*valueInfo
}

func newValueScope() *valueScope {
	return &valueScope{values: make(map[int]*valueInfo)}
}

func (s *valueScope) Declare(id int, info valueInfo) *valueInfo {
	if s == nil || id < 0 {
		return nil
	}
	slot := &valueInfo{
		typ:      info.typ,
		mutable:  info.mutable,
		constant: info.constant,
		borrowOf: -1,
	}
	if len(info.movedSubs) > 0 {
		slot.movedSubs = cloneMovedSubs(info.movedSubs)
	}
	s.values[id] = slot
	return slot
}

func (s *valueScope) Lookup(id int) (*valueInfo, bool) {
	if s == nil {
		return nil, false
	}
	info, ok := s.values[id]
	return info, ok
}

func (s *valueScope) Clone() *valueScope {
	if s == nil {
		return nil
	}
	out := newValueScope()
	for id, info := range s.values {
		if info == nil {
			continue
		}
		clone := *info
		if len(info.movedSubs) > 0 {
			clone.movedSubs = cloneMovedSubs(info.movedSubs)
		}
		out.values[id] = &clone
	}
	return out
}

func (s *valueScope) TrimToLiveOut(live cfg.LocalSet) {
	if s == nil {
		return
	}
	for id, info := range s.values {
		if info == nil || live.Has(id) {
			continue
		}
		if info.borrowOf >= 0 {
			if owner, ok := s.values[info.borrowOf]; ok && owner != nil && owner.frozen > 0 {
				owner.frozen--
			}
		}
		info.moved = false
		info.moveLoc = source.Location{}
		info.movedPath = ""
		info.movedSubs = nil
		info.frozen = 0
		info.mutBorrow = false
		info.borrowOf = -1
		info.borrowMut = false
		info.borrowLoc = source.Location{}
	}
}

func (s *valueScope) MergeFrom(other *valueScope, live cfg.LocalSet) bool {
	if s == nil || other == nil {
		return false
	}
	changed := false
	for id, incoming := range other.values {
		if incoming == nil || (len(live) > 0 && !live.Has(id)) {
			continue
		}
		current, ok := s.values[id]
		if !ok || current == nil {
			clone := *incoming
			s.values[id] = &clone
			changed = true
			continue
		}
		merged := mergeValueInfo(current, incoming)
		if !equalValueInfo(current, merged) {
			s.values[id] = merged
			changed = true
		}
	}
	return changed
}

func equalValueInfo(a, b *valueInfo) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.typ == b.typ &&
		a.mutable == b.mutable &&
		a.constant == b.constant &&
		a.moved == b.moved &&
		a.moveLoc == b.moveLoc &&
		a.movedPath == b.movedPath &&
		equalMovedSubs(a.movedSubs, b.movedSubs) &&
		a.frozen == b.frozen &&
		a.mutBorrow == b.mutBorrow &&
		a.borrowOf == b.borrowOf &&
		a.borrowMut == b.borrowMut &&
		a.borrowLoc == b.borrowLoc
}

func mergeValueInfo(a, b *valueInfo) *valueInfo {
	if a == nil && b == nil {
		return nil
	}
	if a == nil {
		clone := *b
		return &clone
	}
	if b == nil {
		clone := *a
		return &clone
	}
	out := *a
	out.mutable = a.mutable || b.mutable
	out.constant = a.constant && b.constant
	out.moved = a.moved || b.moved
	if out.moved {
		if a.moveLoc.Start != nil {
			out.moveLoc = a.moveLoc
			out.movedPath = a.movedPath
		} else {
			out.moveLoc = b.moveLoc
			out.movedPath = b.movedPath
		}
		out.movedSubs = nil
	} else {
		out.movedSubs = mergeMovedSubs(a.movedSubs, b.movedSubs)
	}
	if b.frozen > out.frozen {
		out.frozen = b.frozen
	}
	out.mutBorrow = a.mutBorrow || b.mutBorrow
	if a.borrowOf == b.borrowOf {
		out.borrowOf = a.borrowOf
		out.borrowMut = a.borrowMut || b.borrowMut
		if a.borrowLoc.Start != nil {
			out.borrowLoc = a.borrowLoc
		} else {
			out.borrowLoc = b.borrowLoc
		}
	} else {
		out.borrowOf = -1
		out.borrowMut = false
		out.borrowLoc = source.Location{}
	}
	return &out
}

func cloneMovedSubs(in map[string]source.Location) map[string]source.Location {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]source.Location, len(in))
	maps.Copy(out, in)
	return out
}

func equalMovedSubs(a, b map[string]source.Location) bool {
	if len(a) != len(b) {
		return false
	}
	for key, loc := range a {
		other, ok := b[key]
		if !ok || other != loc {
			return false
		}
	}
	return true
}

func mergeMovedSubs(a, b map[string]source.Location) map[string]source.Location {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := cloneMovedSubs(a)
	if out == nil {
		out = make(map[string]source.Location, len(b))
	}
	for key, loc := range b {
		if _, ok := out[key]; !ok {
			out[key] = loc
		}
	}
	return out
}
