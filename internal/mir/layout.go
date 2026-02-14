package mir

import "compiler/internal/types"

// DataLayout provides target-specific size and alignment information.
type DataLayout struct {
	PointerSize  int
	PointerAlign int
}

// StructLayout describes a laid out struct type.
type StructLayout struct {
	Size   int
	Align  int
	Fields []FieldLayout
	index  map[string]FieldLayout
}

// FieldLayout describes a struct field's offset and type.
type FieldLayout struct {
	Name   string
	Offset int
	Type   types.SemType
}

// NewDataLayout constructs a layout with a target pointer size (bytes).
func NewDataLayout(pointerSize int) *DataLayout {
	if pointerSize <= 0 {
		pointerSize = 8
	}
	return &DataLayout{
		PointerSize:  pointerSize,
		PointerAlign: pointerSize,
	}
}

// SizeOf returns the size in bytes of a semantic type for this layout.
func (d *DataLayout) SizeOf(t types.SemType) int {
	if t == nil {
		return 0
	}
	t = types.UnwrapType(t)

	switch tt := t.(type) {
	case *types.PrimitiveType:
		switch tt.GetName() {
		case types.TYPE_STRING:
			return d.PointerSize
		case types.TYPE_VOID, types.TYPE_NONE:
			return 0
		default:
			return tt.Size()
		}
	case *types.ReferenceType:
		return d.PointerSize
	case *types.HeapType:
		return d.PointerSize
	case *types.ArrayType:
		if tt.Length < 0 {
			return d.PointerSize
		}
		elemSize := d.SizeOf(tt.Element)
		if elemSize < 0 {
			return -1
		}
		return elemSize * tt.Length
	case *types.MapType:
		return d.PointerSize
	case *types.InterfaceType:
		return d.PointerSize * 2
	case *types.OptionalType:
		return d.PointerSize
	case *types.ResultType:
		okSize := d.SizeOf(tt.Ok)
		errSize := d.SizeOf(tt.Err)
		if okSize < 0 || errSize < 0 {
			return -1
		}
		okAlign := d.AlignOf(tt.Ok)
		errAlign := d.AlignOf(tt.Err)
		unionAlign := max(okAlign, errAlign)
		unionSize := alignTo(max(okSize, errSize), unionAlign)
		return alignTo(unionSize+1, max(unionAlign, 1))
	case *types.StructType:
		layout := d.StructLayout(tt)
		return layout.Size
	default:
		return tt.Size()
	}
}

// AlignOf returns the alignment in bytes for a semantic type.
func (d *DataLayout) AlignOf(t types.SemType) int {
	if t == nil {
		return 1
	}
	t = types.UnwrapType(t)

	switch tt := t.(type) {
	case *types.PrimitiveType:
		switch tt.GetName() {
		case types.TYPE_STRING:
			return d.PointerAlign
		case types.TYPE_VOID, types.TYPE_NONE:
			return 1
		default:
			return clampAlign(tt.Size(), d.PointerAlign)
		}
	case *types.ReferenceType:
		return d.PointerAlign
	case *types.HeapType:
		return d.PointerAlign
	case *types.ArrayType:
		if tt.Length < 0 {
			return d.PointerAlign
		}
		return d.AlignOf(tt.Element)
	case *types.MapType:
		return d.PointerAlign
	case *types.InterfaceType:
		return d.PointerAlign
	case *types.OptionalType:
		return d.PointerAlign
	case *types.ResultType:
		return max(d.AlignOf(tt.Ok), d.AlignOf(tt.Err))
	case *types.StructType:
		layout := d.StructLayout(tt)
		return layout.Align
	default:
		return clampAlign(tt.Size(), d.PointerAlign)
	}
}

// OptionalPayloadSize returns the size in bytes of an optional payload (value + flag).
func (d *DataLayout) OptionalPayloadSize(t *types.OptionalType) int {
	if t == nil {
		return 0
	}
	valSize := d.SizeOf(t.Inner)
	if valSize < 0 {
		return -1
	}
	valAlign := d.AlignOf(t.Inner)
	return alignTo(valSize+1, max(valAlign, 1))
}

// OptionalPayloadAlign returns the alignment in bytes for an optional payload.
func (d *DataLayout) OptionalPayloadAlign(t *types.OptionalType) int {
	if t == nil {
		return 1
	}
	return max(d.AlignOf(t.Inner), 1)
}

// StructLayout computes layout for a struct type.
func (d *DataLayout) StructLayout(t *types.StructType) StructLayout {
	layout := StructLayout{
		Align:  1,
		Fields: make([]FieldLayout, 0, len(t.Fields)),
		index:  make(map[string]FieldLayout, len(t.Fields)),
	}

	offset := 0
	for _, field := range t.Fields {
		fieldAlign := d.AlignOf(field.Type)
		fieldSize := d.SizeOf(field.Type)
		if fieldSize < 0 {
			continue
		}
		offset = alignTo(offset, fieldAlign)

		entry := FieldLayout{
			Name:   field.Name,
			Offset: offset,
			Type:   field.Type,
		}

		layout.Fields = append(layout.Fields, entry)
		layout.index[field.Name] = entry

		offset += fieldSize
		layout.Align = max(layout.Align, fieldAlign)
	}

	layout.Size = alignTo(offset, layout.Align)
	return layout
}

// FieldOffset returns the byte offset for a named field.
func (l StructLayout) FieldOffset(name string) (int, bool) {
	if entry, ok := l.index[name]; ok {
		return entry.Offset, true
	}
	return 0, false
}

func alignTo(value, alignment int) int {
	if alignment <= 1 {
		return value
	}
	rem := value % alignment
	if rem == 0 {
		return value
	}
	return value + (alignment - rem)
}

func clampAlign(size, maxAlign int) int {
	if size <= 0 {
		return 1
	}
	if size > maxAlign {
		return maxAlign
	}
	return size
}
