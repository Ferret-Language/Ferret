package narrowing

import (
	"compiler/internal/context_v2"
	"compiler/internal/frontend/ast"
	"compiler/internal/types"
)

// getTypeFromTypeNode converts an AST type node to a semantic type.
func getTypeFromTypeNode(mod *context_v2.Module, typeNode ast.TypeNode) types.SemType {
	if typeNode == nil {
		return nil
	}

	switch t := typeNode.(type) {
	case *ast.IdentifierExpr:
		// Check for primitive types first
		if primType := getPrimitiveTypeByName(t.Name); primType != nil {
			return primType
		}
		// Could be a named type - look it up in the module
		if sym, ok := mod.CurrentScope.Lookup(t.Name); ok {
			return sym.Type
		}

	case *ast.ArrayType:
		// Array type like []T or [N]T
		elemType := getTypeFromTypeNode(mod, t.ElType)
		if elemType != nil {
			length := -1 // dynamic array
			// Fixed-size array would need constant evaluation
			return types.NewArray(elemType, length)
		}

	case *ast.MapType:
		// Map type like map[K]V
		keyType := getTypeFromTypeNode(mod, t.Key)
		valueType := getTypeFromTypeNode(mod, t.Value)
		if keyType != nil && valueType != nil {
			return types.NewMap(keyType, valueType)
		}

	case *ast.InterfaceType:
		// Interface type
		if len(t.Methods) == 0 {
			return types.NewInterface(nil)
		}

	case *ast.OptionalType:
		// Optional type ?T
		innerType := getTypeFromTypeNode(mod, t.Base)
		if innerType != nil {
			return types.NewOptional(innerType)
		}
	case *ast.ReferenceType:
		innerType := getTypeFromTypeNode(mod, t.Base)
		if innerType != nil {
			if t.Mutable {
				return types.NewMutableReference(innerType)
			}
			return types.NewReference(innerType)
		}
	case *ast.HeapType:
		innerType := getTypeFromTypeNode(mod, t.Base)
		if innerType != nil {
			return types.NewHeap(innerType)
		}
	}

	return nil
}

// getPrimitiveTypeByName returns a primitive type by its name string.
func getPrimitiveTypeByName(name string) types.SemType {
	typeName := types.TYPE_NAME(name)
	switch typeName {
	case types.TYPE_I8:
		return types.TypeI8
	case types.TYPE_I16:
		return types.TypeI16
	case types.TYPE_I32:
		return types.TypeI32
	case types.TYPE_I64:
		return types.TypeI64
	case types.TYPE_U8:
		return types.TypeU8
	case types.TYPE_U16:
		return types.TypeU16
	case types.TYPE_U32:
		return types.TypeU32
	case types.TYPE_U64:
		return types.TypeU64
	case types.TYPE_F32:
		return types.TypeF32
	case types.TYPE_F64:
		return types.TypeF64
	case types.TYPE_STRING:
		return types.TypeString
	case types.TYPE_BOOL:
		return types.TypeBool
	case types.TYPE_BYTE:
		return types.TypeByte
	default:
		return nil
	}
}
