package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/table"
	"compiler/internal/source"
	"compiler/internal/types"
	"testing"
)

func TestTypeFromTypeNodeLargeIntegers(t *testing.T) {
	tests := []struct {
		typeName string
		expected types.SemType
	}{
		{"i128", types.TypeI128},
		{"u128", types.TypeU128},
		{"i256", types.TypeI256},
		{"u256", types.TypeU256},
	}

	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			// Create an AST IdentifierExpr representing the type name
			typeNode := &ast.IdentifierExpr{
				Name:     tt.typeName,
				Location: source.Location{},
			}

			result := typeFromTypeNode(typeNode)
			if !result.Equals(tt.expected) {
				t.Errorf("typeFromTypeNode(%s) = %s, want %s", tt.typeName, result, tt.expected)
			}

			// Verify it's numeric
			if !types.IsNumeric(result) {
				t.Errorf("typeFromTypeNode(%s) should be numeric", tt.typeName)
			}
		})
	}
}

func TestTypeFromTypeNodeEnum(t *testing.T) {
	enumNode := &ast.EnumType{
		Variants: []ast.Field{
			{Name: &ast.IdentifierExpr{Name: "Red", Location: source.Location{}}},
			{Name: &ast.IdentifierExpr{Name: "Green", Location: source.Location{}}},
		},
		ID: "enum1",
	}

	typ := TypeFromTypeNodeWithContext(nil, nil, enumNode)
	enumType, ok := typ.(*types.EnumType)
	if !ok {
		t.Fatalf("TypeFromTypeNodeWithContext(enum) returned %T, want *types.EnumType", typ)
	}
	if enumType.ID != "enum1" {
		t.Fatalf("enum ID = %q, want %q", enumType.ID, "enum1")
	}
	if len(enumType.Variants) != 2 {
		t.Fatalf("enum variants = %d, want 2", len(enumType.Variants))
	}
	if enumType.Variants[0].Name != "Red" || enumType.Variants[1].Name != "Green" {
		t.Fatalf("enum variant names = %q, %q", enumType.Variants[0].Name, enumType.Variants[1].Name)
	}
}

func TestTypeFromTypeNodeHeap(t *testing.T) {
	typeNode := &ast.HeapType{
		Base: &ast.IdentifierExpr{Name: "i32", Location: source.Location{}},
	}
	typ := TypeFromTypeNodeWithContext(nil, nil, typeNode)
	heap, ok := typ.(*types.HeapType)
	if !ok {
		t.Fatalf("TypeFromTypeNodeWithContext(heap) returned %T, want *types.HeapType", typ)
	}
	if !heap.Inner.Equals(types.TypeI32) {
		t.Fatalf("heap inner type = %s, want %s", heap.Inner.String(), types.TypeI32.String())
	}
}

func TestTypeFromTypeNodeCompilerResource_LocalModuleBlocked(t *testing.T) {
	ctx := context_v2.New(&context_v2.Config{Extension: ".fer"}, false)
	ctx.Diagnostics = diagnostics.NewDiagnosticBag("")

	mod := &context_v2.Module{
		Type:         context_v2.ModuleLocal,
		ModuleScope:  table.NewSymbolTable(ctx.Universe),
		CurrentScope: table.NewSymbolTable(ctx.Universe),
	}
	typeNode := &ast.IdentifierExpr{Name: "__file", Location: source.Location{}}

	got := TypeFromTypeNodeWithContext(ctx, mod, typeNode)
	if !got.Equals(types.TypeUnknown) {
		t.Fatalf("TypeFromTypeNodeWithContext(__file) in local module = %s, want unknown", got.String())
	}
	if !ctx.Diagnostics.HasErrors() {
		t.Fatalf("expected diagnostic for compiler-internal type usage")
	}
}

func TestTypeFromTypeNodeCompilerResource_BuiltinModuleAllowed(t *testing.T) {
	ctx := context_v2.New(&context_v2.Config{Extension: ".fer"}, false)
	ctx.Diagnostics = diagnostics.NewDiagnosticBag("")

	modScope := table.NewSymbolTable(ctx.Universe)
	mod := &context_v2.Module{
		Type:         context_v2.ModuleBuiltin,
		ModuleScope:  modScope,
		CurrentScope: modScope,
	}
	typeNode := &ast.IdentifierExpr{Name: "__file", Location: source.Location{}}

	got := TypeFromTypeNodeWithContext(ctx, mod, typeNode)
	if got.Equals(types.TypeUnknown) {
		t.Fatalf("TypeFromTypeNodeWithContext(__file) in builtin module should resolve")
	}
	if !types.IsResourceType(got) {
		t.Fatalf("expected __file to resolve to a resource type, got %s", got.String())
	}
	if ctx.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics for builtin module compiler resource type usage")
	}
}
