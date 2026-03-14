package layout

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/source"
)

type Module struct {
	Key        string
	ImportPath string
	FilePath   string
	Types      []*TypeLayout
	Named      map[string]*TypeLayout
}

type TypeLayout struct {
	Name      string
	NamedType *typeinfo.NamedType
	Type      typeinfo.Type
	Size      int64
	Align     int64
	Known     bool
	Struct    *StructLayout
	Union     *UnionLayout
	Location  source.Location
}

type StructLayout struct {
	Fields        []*FieldLayout
	PhysicalOrder []int
	Size          int64
	Align         int64
}

type FieldLayout struct {
	Name          string
	Type          typeinfo.Type
	SemanticIndex int
	PhysicalIndex int
	Offset        int64
	Size          int64
	Align         int64
	Location      source.Location
}

type UnionLayout struct {
	TagType       typeinfo.Type
	TagSize       int64
	TagAlign      int64
	TagOffset     int64
	PayloadSize   int64
	PayloadAlign  int64
	PayloadOffset int64
	Members       []*UnionMemberLayout
	Size          int64
	Align         int64
}

type UnionMemberLayout struct {
	Index    int
	Type     typeinfo.Type
	Size     int64
	Align    int64
	Offset   int64
	TagValue int64
}

func (m *Module) Lookup(name string) (*TypeLayout, bool) {
	if m == nil || m.Named == nil {
		return nil, false
	}
	layout, ok := m.Named[name]
	return layout, ok
}
