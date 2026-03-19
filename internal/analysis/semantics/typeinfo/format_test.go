package typeinfo

import "testing"

func TestFormatFuncSignature(t *testing.T) {
	fn := &FuncType{
		IsUnsafe:       true,
		TypeParams:     []*TypeParam{{Name: "T"}, {Name: "U", Constraint: &BuiltinType{Name: "bool"}}},
		Params:         []Type{&BuiltinType{Name: "i32"}, &BuiltinType{Name: "i64"}},
		MutParams:      []bool{false, true},
		ComptimeParams: []bool{true, false},
		Result:         &BuiltinType{Name: "bool"},
	}

	got := FormatFuncSignature("Point::Calc", fn)
	want := "unsafe fn Point::Calc<T, U: bool>(comptime i32, mut i64) bool"
	if got != want {
		t.Fatalf("unexpected signature:\nwant: %q\ngot:  %q", want, got)
	}
}
