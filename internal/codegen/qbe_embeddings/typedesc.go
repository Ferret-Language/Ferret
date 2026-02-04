package qbe

import (
	runtimeabi "compiler/internal/runtime/abi"
	"compiler/internal/types"
	"fmt"
	"strings"
)

var ferretPrimitiveKindByName = runtimeabi.PrimitiveKindByName

var (
	ferretTypePointerKind   = runtimeabi.TypeKindPointer
	ferretTypeStructKind    = runtimeabi.TypeKindStruct
	ferretTypeArrayKind     = runtimeabi.TypeKindArray
	ferretTypeSliceKind     = runtimeabi.TypeKindSlice
	ferretTypeMapKind       = runtimeabi.TypeKindMap
	ferretTypeFunctionKind  = runtimeabi.TypeKindFunction
	ferretTypeInterfaceKind = runtimeabi.TypeKindInterface
)

func (g *Generator) qbePtrType() string {
	return runtimeabi.QBEWordType(g.layout.PointerSize)
}

func (g *Generator) emitTypeDescData(globalName string, kind uint32, size int, info1 string, info2 string) {
	ptrType := g.qbePtrType()
	align := maxInt(4, g.layout.PointerSize)
	if runtimeabi.QBETypeDescNeedsPad(g.layout.PointerSize) {
		fmt.Fprintf(&g.data,
			"data %s = align %d { w %d, w 0, %s %d, %s %s, %s %s }\n",
			globalName,
			align,
			kind,
			ptrType,
			size,
			ptrType,
			info1,
			ptrType,
			info2,
		)
		return
	}
	fmt.Fprintf(&g.data,
		"data %s = align %d { w %d, %s %d, %s %s, %s %s }\n",
		globalName,
		align,
		kind,
		ptrType,
		size,
		ptrType,
		info1,
		ptrType,
		info2,
	)
}

// emitTypeDescriptor generates a ferret_type_info_t structure for a given type
// This enables runtime content-based hashing for universal map keys
func (g *Generator) emitTypeDescriptor(globalName string, typ types.SemType) {
	if typ == nil {
		return
	}

	typ = types.UnwrapType(typ)

	switch t := typ.(type) {
	case *types.PrimitiveType:
		g.emitPrimitiveTypeDesc(globalName, t)
	case *types.MapType:
		g.emitMapTypeDesc(globalName, t)
	case *types.ArrayType:
		if t.Length < 0 {
			g.emitSliceTypeDesc(globalName, t)
		} else {
			g.emitArrayTypeDesc(globalName, t)
		}
	case *types.StructType:
		g.emitStructTypeDesc(globalName, t)
	case *types.ReferenceType:
		g.emitPointerTypeDesc(globalName, t)
	case *types.InterfaceType:
		g.emitInterfaceTypeDesc(globalName, t)
	case *types.NamedType:
		// For named types, emit descriptor for the underlying type
		g.emitTypeDescriptor(globalName, t.Underlying)
	default:
		// Fallback: emit a basic descriptor with pointer size
		fmt.Fprintf(&g.data, "# Unsupported type %T for %s\n", typ, globalName)
		g.emitPointerTypeDesc(globalName, types.NewReference(types.TypeVoid))
	}
}

func (g *Generator) emitPrimitiveTypeDesc(globalName string, typ *types.PrimitiveType) {
	kind, ok := ferretPrimitiveKindByName[typ.GetName()]
	if !ok {
		kind = ferretPrimitiveKindByName[types.TYPE_I32]
	}
	size := g.layout.SizeOf(typ)
	if size < 0 {
		size = 4
	}

	// Union is unused for primitives, so all zeros.
	g.emitTypeDescData(globalName, kind, size, "0", "0")
}

func (g *Generator) emitMapTypeDesc(globalName string, typ *types.MapType) {
	// Map type descriptor needs key_type and value_type pointers
	// First ensure descriptors exist for key and value types
	keyDescName := g.getOrCreateTypeDescName(typ.Key)
	valueDescName := g.getOrCreateTypeDescName(typ.Value)

	kind := ferretTypeMapKind
	size := g.layout.PointerSize
	g.emitTypeDescData(globalName, kind, size, keyDescName, valueDescName)
}

func (g *Generator) emitSliceTypeDesc(globalName string, typ *types.ArrayType) {
	// Slice descriptor needs element type pointer
	elemDescName := g.getOrCreateTypeDescName(typ.Element)

	kind := ferretTypeSliceKind
	size := g.layout.SizeOf(typ)
	g.emitTypeDescData(globalName, kind, size, elemDescName, "0")
}

func (g *Generator) emitArrayTypeDesc(globalName string, typ *types.ArrayType) {
	// Array descriptor needs element type pointer and length
	elemDescName := g.getOrCreateTypeDescName(typ.Element)
	elemSize := g.layout.SizeOf(typ.Element)
	arraySize := elemSize * typ.Length

	kind := ferretTypeArrayKind
	g.emitTypeDescData(globalName, kind, arraySize, fmt.Sprintf("%d", typ.Length), elemDescName)
}

func (g *Generator) emitStructTypeDesc(globalName string, typ *types.StructType) {
	// Struct descriptor needs field array
	// First, emit field descriptors
	var fieldDescName string
	if len(typ.Fields) > 0 {
		fieldDescName = globalName + "_fields"
		g.emitStructFields(fieldDescName, typ)
	} else {
		fieldDescName = "0" // NULL for no fields
	}

	kind := ferretTypeStructKind
	size := g.layout.SizeOf(typ)
	g.emitTypeDescData(globalName, kind, size, fmt.Sprintf("%d", len(typ.Fields)), fieldDescName)
}

func (g *Generator) emitStructFields(arrayName string, typ *types.StructType) {
	// Emit array of ferret_field_info_t
	// Each field: { .offset = X, .type = ptr }
	// Need to compute struct layout first
	structLayout := g.layout.StructLayout(typ)
	ptrType := g.qbePtrType()

	fields := make([]string, 0, len(typ.Fields)*2)
	for _, field := range typ.Fields {
		offset, ok := structLayout.FieldOffset(field.Name)
		if !ok {
			offset = 0 // Fallback
		}
		fieldTypeDesc := g.getOrCreateTypeDescName(field.Type)
		// Each ferret_field_info_t is two pointer-sized values: {offset, type_ptr}
		fields = append(fields, fmt.Sprintf("%s %d", ptrType, offset))
		fields = append(fields, fmt.Sprintf("%s %s", ptrType, fieldTypeDesc))
	}
	fmt.Fprintf(&g.data, "data %s = { %s }\n", arrayName, strings.Join(fields, ", "))
}

func (g *Generator) emitPointerTypeDesc(globalName string, typ *types.ReferenceType) {
	// Pointer descriptor needs inner type pointer
	innerDescName := g.getOrCreateTypeDescName(typ.Inner)

	kind := ferretTypePointerKind
	size := g.layout.PointerSize
	g.emitTypeDescData(globalName, kind, size, innerDescName, "0")
}

func (g *Generator) emitInterfaceTypeDesc(globalName string, typ *types.InterfaceType) {
	kind := ferretTypeInterfaceKind
	size := g.layout.PointerSize * 2
	methodCount := 0
	if typ != nil {
		methodCount = len(typ.Methods)
	}
	g.emitTypeDescData(globalName, kind, size, fmt.Sprintf("%d", methodCount), "0")
}

// getOrCreateTypeDescName ensures a type descriptor exists and returns its global name
// This handles recursive type references (e.g., map value type needs its own descriptor)
func (g *Generator) getOrCreateTypeDescName(typ types.SemType) string {
	if typ == nil {
		return "0"
	}

	// Check if already emitted in the module's TypeDescriptors
	key := typeDescriptorKey(typ)
	for existingGlobal, existingType := range g.mirMod.TypeDescriptors {
		if typeDescriptorKey(existingType) == key {
			return existingGlobal
		}
	}

	// Not found in existing descriptors - this means we need a nested descriptor
	// Don't emit it immediately - just add to mirMod.TypeDescriptors
	// It will be emitted in the main emitTypeDescriptors loop
	g.stringID++ // Reuse stringID counter for uniqueness
	newGlobalName := fmt.Sprintf("$typedesc_nested%d", g.stringID)
	g.mirMod.TypeDescriptors[newGlobalName] = typ
	return newGlobalName
}

// typeDescriptorKey generates a unique key for a type (same logic as in builder.go)
func typeDescriptorKey(typ types.SemType) string {
	return typeDescriptorKeyWithSeen(typ, make(map[types.SemType]bool))
}

func typeDescriptorKeyWithSeen(typ types.SemType, seen map[types.SemType]bool) string {
	typ = types.UnwrapType(typ)
	switch t := typ.(type) {
	case *types.PrimitiveType:
		return "prim_" + string(t.GetName())
	case *types.MapType:
		return "map_" + typeDescriptorKeyWithSeen(t.Key, seen) + "_" + typeDescriptorKeyWithSeen(t.Value, seen)
	case *types.ArrayType:
		if t.Length < 0 {
			return fmt.Sprintf("slice_%s", typeDescriptorKeyWithSeen(t.Element, seen))
		}
		return fmt.Sprintf("array_%d_%s", t.Length, typeDescriptorKeyWithSeen(t.Element, seen))
	case *types.StructType:
		if seen[typ] {
			return fmt.Sprintf("struct_rec_%p", t)
		}
		seen[typ] = true
		var sb strings.Builder
		sb.WriteString("struct{")
		for i, field := range t.Fields {
			if i > 0 {
				sb.WriteString(";")
			}
			sb.WriteString(field.Name)
			sb.WriteString(":")
			sb.WriteString(typeDescriptorKeyWithSeen(field.Type, seen))
		}
		sb.WriteString("}")
		return sb.String()
	case *types.InterfaceType:
		if len(t.Methods) == 0 {
			return "interface_empty"
		}
		if t.ID != "" {
			return "interface_" + t.ID
		}
		return fmt.Sprintf("interface_%p", t)
	case *types.ReferenceType:
		return "ref_" + typeDescriptorKeyWithSeen(t.Inner, seen)
	case *types.NamedType:
		return "named_" + t.Name + "_" + typeDescriptorKeyWithSeen(t.Underlying, seen)
	default:
		return fmt.Sprintf("unknown_%T", typ)
	}
}
