package mir

import (
	"reflect"
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
)

func TestWalkModuleValuesVisitsNestedCompositeKeys(t *testing.T) {
	strType := &typeinfo.StringType{}
	i32Type := &typeinfo.BuiltinType{Name: "i32"}
	keyCall := &CallValue{
		baseValue: baseValue{ExprType: strType},
		Callee:    &NameValue{baseValue: baseValue{ExprType: &typeinfo.FuncType{Result: strType}}, Path: []string{"key"}},
	}
	valueCall := &CallValue{
		baseValue: baseValue{ExprType: i32Type},
		Callee:    &NameValue{baseValue: baseValue{ExprType: &typeinfo.FuncType{Result: i32Type}}, Path: []string{"value"}},
	}
	mod := &Module{
		Globals: []*Global{
			{
				Name: "items",
				Init: &CompositeValue{
					Items: []CompositeItem{
						{Key: keyCall, Value: valueCall},
					},
				},
			},
		},
	}
	var calls []string
	if err := WalkModuleValues(mod, func(value Value) error {
		call, ok := value.(*CallValue)
		if !ok {
			return nil
		}
		name, ok := call.Callee.(*NameValue)
		if !ok || len(name.Path) == 0 {
			return nil
		}
		calls = append(calls, name.Path[0])
		return nil
	}); err != nil {
		t.Fatalf("WalkModuleValues: %v", err)
	}
	if want := []string{"key", "value"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}
