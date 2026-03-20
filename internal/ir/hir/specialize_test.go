package hir

import (
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
)

func TestLookupTypeBindingPrefersOwnerAwareKey(t *testing.T) {
	ownerA := &ast.FuncDecl{}
	ownerB := &ast.FuncDecl{}
	paramA := &typeinfo.TypeParam{Name: "T", Owner: ownerA}
	paramB := &typeinfo.TypeParam{Name: "T", Owner: ownerB}
	i32 := &typeinfo.BuiltinType{Name: "i32"}
	i64 := &typeinfo.BuiltinType{Name: "i64"}

	bindings := map[*typeinfo.TypeParam]typeinfo.Type{
		paramA: i32,
		paramB: i64,
	}

	got := lookupTypeBinding(bindings, typeParamBindingKey{Name: "T", Owner: ownerB})
	if !typeinfo.Equal(got, i64) {
		t.Fatalf("expected owner-aware binding to resolve i64, got %#v", got)
	}
}

func TestLookupTypeBindingFallsBackByNameOnlyWhenOwnerMissing(t *testing.T) {
	owner := &ast.TypeDecl{}
	param := &typeinfo.TypeParam{Name: "T", Owner: owner}
	i32 := &typeinfo.BuiltinType{Name: "i32"}
	bindings := map[*typeinfo.TypeParam]typeinfo.Type{
		param: i32,
	}

	got := lookupTypeBinding(bindings, typeParamBindingKey{Name: "T"})
	if !typeinfo.Equal(got, i32) {
		t.Fatalf("expected ownerless fallback to resolve by name, got %#v", got)
	}
}

func TestSpecializedFuncParamKeysKeepSameNamedOwnerAndFuncTypeParams(t *testing.T) {
	ownerDecl := &ast.TypeDecl{
		Name: &ast.Ident{Path: []string{"Box"}},
		TypeParams: []ast.TypeParam{
			{Name: &ast.Ident{Path: []string{"T"}}},
		},
	}
	fnDecl := &ast.FuncDecl{
		Name: &ast.Ident{Path: []string{"Pick"}},
		TypeParams: []ast.TypeParam{
			{Name: &ast.Ident{Path: []string{"T"}}},
		},
	}

	s := &specializer{
		module: &Module{
			Types: []*TypeDecl{
				{Name: "Box", Source: ownerDecl},
			},
		},
	}
	keys := s.specializedFuncParamKeys(&Func{
		Name:      "Pick",
		OwnerType: "Box",
		Source:    fnDecl,
	})
	if len(keys) != 2 {
		t.Fatalf("expected owner+function keys for same-named type params, got %#v", keys)
	}
	if keys[0].Name != "T" || keys[0].Owner != ownerDecl {
		t.Fatalf("expected first key for owner type param, got %#v", keys[0])
	}
	if keys[1].Name != "T" || keys[1].Owner != fnDecl {
		t.Fatalf("expected second key for function type param, got %#v", keys[1])
	}
}
