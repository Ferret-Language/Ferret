package backend

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
)

const (
	RuntimeTypeUnknown = 0
	RuntimeTypeBool    = 1
	RuntimeTypeI8      = 2
	RuntimeTypeI16     = 3
	RuntimeTypeI32     = 4
	RuntimeTypeI64     = 5
	RuntimeTypeIsize   = 6
	RuntimeTypeU8      = 7
	RuntimeTypeU16     = 8
	RuntimeTypeU32     = 9
	RuntimeTypeU64     = 10
	RuntimeTypeUsize   = 11
	RuntimeTypeF32     = 12
	RuntimeTypeF64     = 13
	RuntimeTypeChar    = 14
	RuntimeTypeString  = 15
)

const (
	RuntimeTypeFlagPointer   = 1 << 0
	RuntimeTypeFlagNamed     = 1 << 1
	RuntimeTypeFlagInterface = 1 << 2
	RuntimeTypeFlagSlice     = 1 << 3
	RuntimeTypeFlagInteger   = 1 << 4
	RuntimeTypeFlagSigned    = 1 << 5
	RuntimeTypeFlagArray     = 1 << 6
	RuntimeTypeFlagTuple     = 1 << 7
	RuntimeTypeFlagVariants  = 1 << 8
	RuntimeTypeFlagOptional  = 1 << 9
)

type RuntimeTypeDescriptor struct {
	Name          string
	ID            uint32
	Flags         uint32
	Elem          typeinfo.Type
	Length        int64
	Stride        int64
	PayloadOffset int64
	Fields        []RuntimeTypeFieldDescriptor
	Variants      []string
}

type RuntimeTypeFieldDescriptor struct {
	Offset int64
	Type   typeinfo.Type
}

func DescribeRuntimeType(typ typeinfo.Type) RuntimeTypeDescriptor {
	desc := RuntimeTypeDescriptor{
		Name: typeinfo.DefaultPrinter.Type(typ),
	}
	if typ == nil {
		return desc
	}

	inspect := typ
	if named, ok := typ.(*typeinfo.NamedType); ok && named != nil {
		desc.Flags |= RuntimeTypeFlagNamed
		if named.Decl != nil {
			switch decl := named.Decl.Type.(type) {
			case *ast.EnumType:
				desc.Flags |= RuntimeTypeFlagVariants
				desc.Variants = make([]string, 0, len(decl.Variants))
				for _, variant := range decl.Variants {
					if variant != nil && variant.Name != nil {
						desc.Variants = append(desc.Variants, variant.Name.Text())
					}
				}
				inspect = &typeinfo.BuiltinType{Name: "i32"}
			case *ast.ErrorType:
				desc.Flags |= RuntimeTypeFlagVariants
				desc.Variants = make([]string, 0, len(decl.Members))
				for _, member := range decl.Members {
					if member != nil && member.Name != nil {
						desc.Variants = append(desc.Variants, member.Name.Text())
					}
				}
				inspect = &typeinfo.BuiltinType{Name: "i32"}
			case *ast.InterfaceType:
				desc.Flags |= RuntimeTypeFlagInterface
			}
		}
	}

	switch t := inspect.(type) {
	case *typeinfo.StringType:
		desc.ID = RuntimeTypeString
	case *typeinfo.BuiltinType:
		if signed, _, ok := tokens.ParseIntegerBuiltin(t.Name); ok {
			desc.Flags |= RuntimeTypeFlagInteger
			if signed {
				desc.Flags |= RuntimeTypeFlagSigned
			}
		}
		switch t.Name {
		case "bool":
			desc.ID = RuntimeTypeBool
		case "i8":
			desc.ID = RuntimeTypeI8
		case "i16":
			desc.ID = RuntimeTypeI16
		case "i32":
			desc.ID = RuntimeTypeI32
		case "i64":
			desc.ID = RuntimeTypeI64
		case "isize":
			desc.ID = RuntimeTypeIsize
		case "u8":
			desc.ID = RuntimeTypeU8
		case "u16":
			desc.ID = RuntimeTypeU16
		case "u32":
			desc.ID = RuntimeTypeU32
		case "u64":
			desc.ID = RuntimeTypeU64
		case "usize":
			desc.ID = RuntimeTypeUsize
		case "f32":
			desc.ID = RuntimeTypeF32
		case "f64":
			desc.ID = RuntimeTypeF64
		case "char":
			desc.ID = RuntimeTypeChar
		}
	case *typeinfo.PointerType, *typeinfo.RefType, *typeinfo.RawPtrType:
		desc.Flags |= RuntimeTypeFlagPointer
	case *typeinfo.InterfaceType:
		desc.Flags |= RuntimeTypeFlagInterface
	case *typeinfo.SliceType:
		desc.Flags |= RuntimeTypeFlagSlice
	case *typeinfo.OptionalType:
		desc.Flags |= RuntimeTypeFlagOptional
	}
	return desc
}

func DescribeRuntimeTypeLayout(ctx AggregateLayoutContext, typ typeinfo.Type) (RuntimeTypeDescriptor, error) {
	desc := DescribeRuntimeType(typ)
	switch t := typ.(type) {
	case *typeinfo.ArrayType:
		elemSize, elemAlign, err := aggregateElementSizeAlign(ctx, t.Inner)
		if err != nil {
			return desc, err
		}
		desc.Flags |= RuntimeTypeFlagArray
		desc.Elem = t.Inner
		desc.Length = t.Len
		desc.Stride = AlignUpInt64(elemSize, elemAlign)
	case *typeinfo.SliceType:
		elemSize, elemAlign, err := aggregateElementSizeAlign(ctx, t.Inner)
		if err != nil {
			return desc, err
		}
		desc.Elem = t.Inner
		desc.Stride = AlignUpInt64(elemSize, elemAlign)
	case *typeinfo.TupleType:
		entries, _, _, err := TupleLayout(ctx, t)
		if err != nil {
			return desc, err
		}
		desc.Flags |= RuntimeTypeFlagTuple
		desc.Fields = make([]RuntimeTypeFieldDescriptor, 0, len(entries))
		for _, entry := range entries {
			desc.Fields = append(desc.Fields, RuntimeTypeFieldDescriptor{
				Offset: entry.Offset,
				Type:   entry.Type,
			})
		}
	case *typeinfo.OptionalType:
		desc.Flags |= RuntimeTypeFlagOptional
		desc.Elem = t.Inner
		if !OptionalUsesNiche(t.Inner) {
			info, err := TaggedUnionLayoutInfo(ctx, []typeinfo.Type{t.Inner})
			if err != nil {
				return desc, err
			}
			desc.PayloadOffset = info.PayloadOffset
		}
	}
	return desc, nil
}
