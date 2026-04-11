package layoutanalysis

import (
	"fmt"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/abi"
	"compiler/internal/core/context"
	"compiler/internal/core/phase"
	"compiler/internal/tokens"
)

const (
	tagSize  = int64(4)
	tagAlign = int64(4)
)

type analyzer struct {
	ctx     *context.CompilerContext
	modules map[string]*layout.Module
	cache   map[string]*typeLayoutResult
	active  map[string]bool
}

type typeLayoutResult struct {
	size   int64
	align  int64
	known  bool
	layout *layout.TypeLayout
}

func AnalyzeModules(ctx *context.CompilerContext, mods []*context.Module) {
	if ctx == nil {
		return
	}
	a := &analyzer{
		ctx:     ctx,
		modules: make(map[string]*layout.Module, len(mods)),
		cache:   make(map[string]*typeLayoutResult),
		active:  make(map[string]bool),
	}
	for _, mod := range mods {
		a.analyzeModule(mod)
	}
}

func (a *analyzer) analyzeModule(mod *context.Module) {
	if mod == nil || mod.LoweredHIR == nil {
		return
	}
	hirMod := mod.LoweredHIR
	lm := &layout.Module{
		Key:        mod.Key,
		ImportPath: mod.ImportPath,
		FilePath:   mod.FilePath,
		Types:      make([]*layout.TypeLayout, 0, len(hirMod.Types)),
		Named:      make(map[string]*layout.TypeLayout, len(hirMod.Types)),
	}
	a.modules[mod.Key] = lm
	for _, decl := range hirMod.Types {
		if decl == nil || decl.Named == nil {
			continue
		}
		result := a.layoutNamedType(decl.Named)
		if result == nil || result.layout == nil {
			continue
		}
		lm.Types = append(lm.Types, result.layout)
		lm.Named[decl.Name] = result.layout
	}
	mod.Layout = lm
	mod.Phase = phase.PhaseLayoutComputed
}

func (a *analyzer) layoutNamedType(named *typeinfo.NamedType) *typeLayoutResult {
	if named == nil {
		return unknownLayout()
	}
	key := named.ModuleKey + "::" + named.Name
	if cached, ok := a.cache[key]; ok {
		return cached
	}
	if a.active[key] {
		return unknownLayout()
	}
	a.active[key] = true
	defer delete(a.active, key)

	result := &typeLayoutResult{
		layout: &layout.TypeLayout{
			Name:      named.Name,
			NamedType: named,
			Type:      named,
		},
	}
	if named.Decl != nil {
		result.layout.Location = named.Decl.Loc()
	}
	a.cache[key] = result

	underlying := a.findUnderlyingType(named)
	var (
		size         int64
		align        int64
		known        bool
		structLayout *layout.StructLayout
		unionLayout  *layout.UnionLayout
	)
	if unionType, ok := underlying.(*typeinfo.UnionType); ok {
		size, align, known, unionLayout = a.layoutTaggedUnionDetail(unionType.Members)
	} else {
		size, align, known, structLayout = a.layoutUnderlying(nil, underlying)
	}
	result.size = size
	result.align = align
	result.known = known
	result.layout.Size = size
	result.layout.Align = align
	result.layout.Known = known
	result.layout.Struct = structLayout
	result.layout.Union = unionLayout
	result.layout.Type = underlying
	return result
}

func (a *analyzer) findUnderlyingType(named *typeinfo.NamedType) typeinfo.Type {
	if named == nil {
		return named
	}
	mod, ok := a.ctx.GetModule(named.ModuleKey)
	if !ok || mod == nil || mod.LoweredHIR == nil {
		if named.Decl != nil {
			return named
		}
		return named
	}
	for _, decl := range mod.LoweredHIR.Types {
		if decl != nil && decl.Name == named.Name && decl.Underlying != nil {
			return decl.Underlying
		}
	}
	return named
}

func (a *analyzer) layoutUnderlying(syntax any, typ typeinfo.Type) (int64, int64, bool, *layout.StructLayout) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		res := a.layoutNamedType(t)
		if res == nil {
			return 0, 1, false, nil
		}
		return res.size, res.align, res.known, res.layout.Struct
	case *typeinfo.BuiltinType:
		return builtinLayout(t.Name)
	case *typeinfo.PointerType:
		ptrSize := abi.PointerBytes()
		return ptrSize, ptrSize, true, nil
	case *typeinfo.RefType:
		ptrSize := abi.PointerBytes()
		return ptrSize, ptrSize, true, nil
	case *typeinfo.RawPtrType:
		ptrSize := abi.PointerBytes()
		return ptrSize, ptrSize, true, nil
	case *typeinfo.FuncType:
		ptrSize := abi.PointerBytes()
		return ptrSize, ptrSize, true, nil
	case *typeinfo.StringType:
		ptrSize := abi.PointerBytes()
		return ptrSize * 2, ptrSize, true, nil
	case *typeinfo.SliceType:
		ptrSize := abi.PointerBytes()
		return ptrSize * 2, ptrSize, true, nil
	case *typeinfo.MapType:
		ptrSize := abi.PointerBytes()
		return ptrSize, ptrSize, true, nil
	case *typeinfo.ArrayType:
		if t.Len < 0 {
			return 0, 1, false, nil
		}
		elemSize, elemAlign, known, _ := a.layoutUnderlying(nil, t.Inner)
		if !known {
			return 0, maxInt64(1, elemAlign), false, nil
		}
		stride := alignUp(elemSize, elemAlign)
		return stride * t.Len, maxInt64(1, elemAlign), true, nil
	case *typeinfo.TupleType:
		return a.layoutSequential(t.Elems)
	case *typeinfo.StructType:
		return a.layoutStruct(t)
	case *typeinfo.EnumType, *typeinfo.ErrorSetType:
		return tagSize, tagAlign, true, nil
	case *typeinfo.InterfaceType:
		ptrSize := abi.PointerBytes()
		return ptrSize * 2, ptrSize, true, nil
	case *typeinfo.OptionalType:
		return a.layoutOptional(t.Inner)
	case *typeinfo.ErrorUnionType:
		return a.layoutTaggedUnion([]typeinfo.Type{t.Error, t.Value})
	case *typeinfo.UnionType:
		return a.layoutTaggedUnion(t.Members)
	case typeinfo.UnknownType, typeinfo.InvalidType:
		return 0, 1, false, nil
	case nil:
		return 0, 1, true, nil
	default:
		_ = syntax
		return 0, 1, false, nil
	}
}

func (a *analyzer) layoutOptional(inner typeinfo.Type) (int64, int64, bool, *layout.StructLayout) {
	if a.optionalUsesNiche(inner) {
		return a.layoutUnderlying(nil, inner)
	}
	return a.layoutTaggedUnion([]typeinfo.Type{inner})
}

func (a *analyzer) optionalUsesNiche(typ typeinfo.Type) bool {
	switch t := typ.(type) {
	case nil:
		return false
	case *typeinfo.NamedType:
		return a.optionalUsesNiche(a.findUnderlyingType(t))
	case *typeinfo.PointerType:
		return true
	case *typeinfo.BuiltinType:
		switch t.Name {
		case "bool", "char":
			return true
		}
	case *typeinfo.EnumType, *typeinfo.ErrorSetType:
		return true
	}
	return false
}

func (a *analyzer) layoutSequential(elems []typeinfo.Type) (int64, int64, bool, *layout.StructLayout) {
	offset := int64(0)
	maxAlign := int64(1)
	known := true
	for _, elem := range elems {
		size, align, elemKnown, _ := a.layoutUnderlying(nil, elem)
		if align <= 0 {
			align = 1
		}
		offset = alignUp(offset, align)
		offset += size
		maxAlign = maxInt64(maxAlign, align)
		known = known && elemKnown
	}
	return alignUp(offset, maxAlign), maxAlign, known, nil
}

func (a *analyzer) layoutStruct(st *typeinfo.StructType) (int64, int64, bool, *layout.StructLayout) {
	if st == nil {
		return 0, 1, false, nil
	}
	fields := make([]*layout.FieldLayout, 0, len(st.OrderedFields))
	order := make([]int, 0, len(st.OrderedFields))
	offset := int64(0)
	maxAlign := int64(1)
	known := true
	for i, field := range st.OrderedFields {
		if field == nil {
			continue
		}
		size, align, fieldKnown, _ := a.layoutUnderlying(nil, field.Type)
		if align <= 0 {
			align = 1
		}
		offset = alignUp(offset, align)
		fields = append(fields, &layout.FieldLayout{
			Name:          field.Name,
			Type:          field.Type,
			SemanticIndex: i,
			PhysicalIndex: len(order),
			Offset:        offset,
			Size:          size,
			Align:         align,
		})
		order = append(order, i)
		offset += size
		maxAlign = maxInt64(maxAlign, align)
		known = known && fieldKnown
	}
	total := alignUp(offset, maxAlign)
	return total, maxAlign, known, &layout.StructLayout{
		Fields:        fields,
		PhysicalOrder: order,
		Size:          total,
		Align:         maxAlign,
	}
}

func (a *analyzer) layoutTaggedUnion(members []typeinfo.Type) (int64, int64, bool, *layout.StructLayout) {
	size, align, known, u := a.layoutTaggedUnionDetail(members)
	return size, align, known, unionStructLayout(u)
}

func (a *analyzer) layoutTaggedUnionDetail(members []typeinfo.Type) (int64, int64, bool, *layout.UnionLayout) {
	payloadSize := int64(0)
	payloadAlign := int64(1)
	known := true
	memberLayouts := make([]*layout.UnionMemberLayout, 0, len(members))
	for _, member := range members {
		size, align, memberKnown, _ := a.layoutUnderlying(nil, member)
		payloadSize = maxInt64(payloadSize, size)
		payloadAlign = maxInt64(payloadAlign, align)
		known = known && memberKnown
		memberLayouts = append(memberLayouts, &layout.UnionMemberLayout{
			Index: len(memberLayouts),
			Type:  member,
			Size:  size,
			Align: align,
		})
	}
	align := maxInt64(tagAlign, payloadAlign)
	payloadOffset := alignUp(tagSize, payloadAlign)
	size := alignUp(payloadOffset+payloadSize, align)
	for _, member := range memberLayouts {
		member.Offset = payloadOffset
		member.TagValue = int64(member.Index)
	}
	return size, align, known, &layout.UnionLayout{
		TagType:       &typeinfo.BuiltinType{Name: "i32"},
		TagSize:       tagSize,
		TagAlign:      tagAlign,
		TagOffset:     0,
		PayloadSize:   payloadSize,
		PayloadAlign:  payloadAlign,
		PayloadOffset: payloadOffset,
		Members:       memberLayouts,
		Size:          size,
		Align:         align,
	}
}

func builtinLayout(name string) (int64, int64, bool, *layout.StructLayout) {
	if _, bits, ok := tokens.ParseIntegerBuiltin(name); ok {
		size := int64((bits + 7) / 8)
		return size, size, true, nil
	}
	switch name {
	case "void":
		return 0, 1, true, nil
	case "bool":
		return 1, 1, true, nil
	case "f32", "char":
		return 4, 4, true, nil
	case "f64":
		return 8, 8, true, nil
	default:
		return 0, 1, false, nil
	}
}

func unknownLayout() *typeLayoutResult {
	return &typeLayoutResult{size: 0, align: 1, known: false}
}

func alignUp(value, align int64) int64 {
	if align <= 1 {
		return value
	}
	rem := value % align
	if rem == 0 {
		return value
	}
	return value + (align - rem)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func unionStructLayout(u *layout.UnionLayout) *layout.StructLayout {
	if u == nil {
		return nil
	}
	return &layout.StructLayout{
		Fields: []*layout.FieldLayout{
			{
				Name:          "$tag",
				Type:          u.TagType,
				SemanticIndex: 0,
				PhysicalIndex: 0,
				Offset:        u.TagOffset,
				Size:          u.TagSize,
				Align:         u.TagAlign,
			},
			{
				Name:          "$payload",
				Type:          nil,
				SemanticIndex: 1,
				PhysicalIndex: 1,
				Offset:        u.PayloadOffset,
				Size:          u.PayloadSize,
				Align:         u.PayloadAlign,
			},
		},
		PhysicalOrder: []int{0, 1},
		Size:          u.Size,
		Align:         u.Align,
	}
}

func DebugModule(mod *layout.Module) string {
	if mod == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d", mod.ImportPath, len(mod.Types))
}
