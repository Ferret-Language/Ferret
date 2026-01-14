package typechecker

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/types"
	"math/big"
	"testing"
)

func TestResolveUntypedInt(t *testing.T) {
	defaultName := types.DEFAULT_INT_TYPE
	maxDefault := maxValueForType(defaultName)
	minDefault := minValueForType(defaultName)

	tests := []struct {
		name     string
		value    string
		wantType types.TYPE_NAME
	}{
		{
			name:     "small value uses default",
			value:    "100",
			wantType: defaultName,
		},
		{
			name:     "default max fits in default",
			value:    maxDefault.String(),
			wantType: defaultName,
		},
		{
			name:     "default min fits in default",
			value:    minDefault.String(),
			wantType: defaultName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lit := &ast.BasicLit{
				Kind:  ast.INT,
				Value: tt.value,
			}
			result := inferLiteralType(lit, types.TypeUnknown)

			name, ok := types.GetPrimitiveName(result)
			if !ok {
				t.Fatalf("inferLiteralType(%q) did not return a primitive type", tt.value)
			}

			if name != tt.wantType {
				t.Errorf("inferLiteralType(%q) = %s, want %s", tt.value, name, tt.wantType)
			}
		})
	}
}

func maxValueForType(name types.TYPE_NAME) *big.Int {
	bitSize := types.GetNumberBitSize(name)
	if bitSize == 0 {
		return big.NewInt(0)
	}
	if types.IsUnsigned(name) {
		max := new(big.Int).Lsh(big.NewInt(1), uint(bitSize))
		return max.Sub(max, big.NewInt(1))
	}
	max := new(big.Int).Lsh(big.NewInt(1), uint(bitSize-1))
	return max.Sub(max, big.NewInt(1))
}

func minValueForType(name types.TYPE_NAME) *big.Int {
	if types.IsUnsigned(name) {
		return big.NewInt(0)
	}
	bitSize := types.GetNumberBitSize(name)
	if bitSize == 0 {
		return big.NewInt(0)
	}
	return new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), uint(bitSize-1)))
}
