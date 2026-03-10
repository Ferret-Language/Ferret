package cfg

import "sort"

type NameSet map[string]struct{}

func NewNameSet(names ...string) NameSet {
	set := make(NameSet, len(names))
	for _, name := range names {
		set.Add(name)
	}
	return set
}

func (s NameSet) Add(name string) {
	if s == nil || name == "" {
		return
	}
	s[name] = struct{}{}
}

func (s NameSet) Has(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s[name]
	return ok
}

func (s NameSet) Remove(name string) {
	if s == nil {
		return
	}
	delete(s, name)
}

func (s NameSet) Clone() NameSet {
	if len(s) == 0 {
		return NewNameSet()
	}
	out := make(NameSet, len(s))
	for name := range s {
		out[name] = struct{}{}
	}
	return out
}

func (s NameSet) Union(other NameSet) NameSet {
	out := s.Clone()
	for name := range other {
		out[name] = struct{}{}
	}
	return out
}

func (s NameSet) Difference(other NameSet) NameSet {
	if len(s) == 0 {
		return NewNameSet()
	}
	out := make(NameSet, len(s))
	for name := range s {
		if other.Has(name) {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

func (s NameSet) Equal(other NameSet) bool {
	if len(s) != len(other) {
		return false
	}
	for name := range s {
		if !other.Has(name) {
			return false
		}
	}
	return true
}

func (s NameSet) Sorted() []string {
	out := make([]string, 0, len(s))
	for name := range s {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
