package cfg

import "sort"

// LocalSet is a set of per-function local IDs.
type LocalSet map[int]struct{}

func NewLocalSet(ids ...int) LocalSet {
	set := make(LocalSet, len(ids))
	for _, id := range ids {
		set.Add(id)
	}
	return set
}

func (s LocalSet) Add(id int) {
	if s == nil || id < 0 {
		return
	}
	s[id] = struct{}{}
}

func (s LocalSet) Has(id int) bool {
	if s == nil {
		return false
	}
	_, ok := s[id]
	return ok
}

func (s LocalSet) Remove(id int) {
	if s == nil {
		return
	}
	delete(s, id)
}

func (s LocalSet) Clone() LocalSet {
	if len(s) == 0 {
		return NewLocalSet()
	}
	out := make(LocalSet, len(s))
	for id := range s {
		out[id] = struct{}{}
	}
	return out
}

func (s LocalSet) Union(other LocalSet) LocalSet {
	out := s.Clone()
	for id := range other {
		out[id] = struct{}{}
	}
	return out
}

func (s LocalSet) Difference(other LocalSet) LocalSet {
	if len(s) == 0 {
		return NewLocalSet()
	}
	out := make(LocalSet, len(s))
	for id := range s {
		if other.Has(id) {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

func (s LocalSet) Equal(other LocalSet) bool {
	if len(s) != len(other) {
		return false
	}
	for id := range s {
		if !other.Has(id) {
			return false
		}
	}
	return true
}

func (s LocalSet) Sorted() []int {
	out := make([]int, 0, len(s))
	for id := range s {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}
