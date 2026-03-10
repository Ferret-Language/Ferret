package layout

import (
	"compiler/internal/semantics/typeinfo"
	"compiler/internal/source"
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

func (m *Module) Lookup(name string) (*TypeLayout, bool) {
	if m == nil || m.Named == nil {
		return nil, false
	}
	layout, ok := m.Named[name]
	return layout, ok
}
