package qbe

import (
	"compiler/internal/types"
	"fmt"
)

var ferretPrimitiveTypeOrder = []types.TYPE_NAME{
	types.TYPE_I8,
	types.TYPE_I16,
	types.TYPE_I32,
	types.TYPE_I64,
	types.TYPE_I128,
	types.TYPE_I256,
	types.TYPE_U8,
	types.TYPE_U16,
	types.TYPE_U32,
	types.TYPE_U64,
	types.TYPE_U128,
	types.TYPE_U256,
	types.TYPE_F32,
	types.TYPE_F64,
	types.TYPE_F128,
	types.TYPE_F256,
	types.TYPE_STRING,
	types.TYPE_BYTE,
	types.TYPE_CHAR,
	types.TYPE_BOOL,
}

var ferretPrimitiveKindByName = func() map[types.TYPE_NAME]int {
	kinds := make(map[types.TYPE_NAME]int, len(ferretPrimitiveTypeOrder))
	for idx, name := range ferretPrimitiveTypeOrder {
		kinds[name] = idx
	}
	return kinds
}()

var (
	ferretTypePointerKind   = len(ferretPrimitiveTypeOrder)
	ferretTypeStructKind    = ferretTypePointerKind + 1
	ferretTypeArrayKind     = ferretTypeStructKind + 1
	ferretTypeSliceKind     = ferretTypeArrayKind + 1
	ferretTypeMapKind       = ferretTypeSliceKind + 1
	ferretTypeFunctionKind  = ferretTypeMapKind + 1
	ferretTypeInterfaceKind = ferretTypeFunctionKind + 1
)

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
		g.emitInterfaceTypeDesc(globalName)
	case *types.NamedType:
		// For named types, emit descriptor for the underlying type
		g.emitTypeDescriptor(globalName, t.Underlying)
	default:
		// Fallback: emit a basic descriptor with pointer size
		g.data.WriteString(fmt.Sprintf("# Unsupported type %T for %s\n", typ, globalName))
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

	// Memory layout: { kind (4), padding (4), size (8), union (16) }
	// Union is unused for primitives, so all zeros
	g.data.WriteString(fmt.Sprintf("data %s = { w %d, w 0, l %d, l 0, l 0 }\n",
		globalName, kind, size))
}

func (g *Generator) emitMapTypeDesc(globalName string, typ *types.MapType) {
	// Map type descriptor needs key_type and value_type pointers
	// First ensure descriptors exist for key and value types
	keyDescName := g.getOrCreateTypeDescName(typ.Key)
	valueDescName := g.getOrCreateTypeDescName(typ.Value)

	// Emit: { .kind = FERRET_TYPE_MAP, .size = sizeof(void*), .map_info = {key, value} }
	// Layout: { kind (4), padding (4), size (8), key_ptr (8), value_ptr (8) }
	kind := ferretTypeMapKind
	size := g.layout.PointerSize
	g.data.WriteString(fmt.Sprintf("data %s = { w %d, w 0, l %d, l %s, l %s }\n",
		globalName, kind, size, keyDescName, valueDescName))
}

func (g *Generator) emitSliceTypeDesc(globalName string, typ *types.ArrayType) {
	// Slice descriptor needs element type pointer
	elemDescName := g.getOrCreateTypeDescName(typ.Element)

	// Emit: { .kind = FERRET_TYPE_SLICE, .size = sizeof(ferret_slice_t), .slice_info = {elem} }
	// Layout: { kind (4), padding (4), size (8), element_ptr (8), unused (8) }
	kind := ferretTypeSliceKind
	size := g.layout.PointerSize * 3 // {data, len, cap}
	g.data.WriteString(fmt.Sprintf("data %s = { w %d, w 0, l %d, l %s, l 0 }\n",
		globalName, kind, size, elemDescName))
}

func (g *Generator) emitArrayTypeDesc(globalName string, typ *types.ArrayType) {
	// Array descriptor needs element type pointer and length
	elemDescName := g.getOrCreateTypeDescName(typ.Element)
	elemSize := g.layout.SizeOf(typ.Element)
	arraySize := elemSize * typ.Length

	// Emit: { .kind = FERRET_TYPE_ARRAY, .size = elem_size * length, .array_info = {length, elem} }
	// Layout: { kind (4), padding (4), size (8), length (8), element_ptr (8) }
	kind := ferretTypeArrayKind
	g.data.WriteString(fmt.Sprintf("data %s = { w %d, w 0, l %d, l %d, l %s }\n",
		globalName, kind, arraySize, typ.Length, elemDescName))
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

	// Emit: { .kind = FERRET_TYPE_STRUCT, .size = sizeof(struct), .struct_info = {field_count, fields} }
	// Layout: { kind (4), padding (4), size (8), field_count (8), fields_ptr (8) }
	kind := ferretTypeStructKind
	size := g.layout.SizeOf(typ)
	g.data.WriteString(fmt.Sprintf("data %s = { w %d, w 0, l %d, l %d, l %s }\n",
		globalName, kind, size, len(typ.Fields), fieldDescName))
}

func (g *Generator) emitStructFields(arrayName string, typ *types.StructType) {
	// Emit array of ferret_field_info_t
	// Each field: { .offset = X, .type = ptr }
	// Need to compute struct layout first
	structLayout := g.layout.StructLayout(typ)

	fields := make([]string, 0, len(typ.Fields)*2)
	for _, field := range typ.Fields {
		offset, ok := structLayout.FieldOffset(field.Name)
		if !ok {
			offset = 0 // Fallback
		}
		fieldTypeDesc := g.getOrCreateTypeDescName(field.Type)
		// Each ferret_field_info_t is two longs: {offset, type_ptr}
		fields = append(fields, fmt.Sprintf("l %d", offset))
		fields = append(fields, fmt.Sprintf("l %s", fieldTypeDesc))
	}
	g.data.WriteString(fmt.Sprintf("data %s = { %s }\n", arrayName, joinStrings(fields, ", ")))
}

func (g *Generator) emitPointerTypeDesc(globalName string, typ *types.ReferenceType) {
	// Pointer descriptor needs inner type pointer
	innerDescName := g.getOrCreateTypeDescName(typ.Inner)

	// Emit: { .kind = FERRET_TYPE_POINTER, .size = sizeof(void*), .pointer_info = {pointee} }
	// Layout: { kind (4), padding (4), size (8), pointee_ptr (8), unused (8) }
	kind := ferretTypePointerKind
	size := g.layout.PointerSize
	g.data.WriteString(fmt.Sprintf("data %s = { w %d, w 0, l %d, l %s, l 0 }\n",
		globalName, kind, size, innerDescName))
}

func (g *Generator) emitInterfaceTypeDesc(globalName string) {
	// Interface uses pointer equality
	// Emit: { .kind = FERRET_TYPE_INTERFACE, .size = sizeof(void*) }
	// Layout: { kind (4), padding (4), size (8), unused (8), unused (8) }
	kind := ferretTypeInterfaceKind
	size := g.layout.PointerSize
	g.data.WriteString(fmt.Sprintf("data %s = { w %d, w 0, l %d, l 0, l 0 }\n",
		globalName, kind, size))
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
	typ = types.UnwrapType(typ)
	switch t := typ.(type) {
	case *types.PrimitiveType:
		return "prim_" + string(t.GetName())
	case *types.MapType:
		return "map_" + typeDescriptorKey(t.Key) + "_" + typeDescriptorKey(t.Value)
	case *types.ArrayType:
		if t.Length < 0 {
			return fmt.Sprintf("slice_%s", typeDescriptorKey(t.Element))
		}
		return fmt.Sprintf("array_%d_%s", t.Length, typeDescriptorKey(t.Element))
	case *types.StructType:
		return fmt.Sprintf("struct_%p", t)
	case *types.InterfaceType:
		return "interface"
	case *types.ReferenceType:
		return "ref_" + typeDescriptorKey(t.Inner)
	case *types.NamedType:
		return "named_" + t.Name + "_" + typeDescriptorKey(t.Underlying)
	default:
		return fmt.Sprintf("unknown_%T", typ)
	}
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
