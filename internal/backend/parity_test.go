package backend_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/backend"
	"compiler/internal/backend/registry"
	compiler "compiler/internal/driver"
	"compiler/internal/ir/mir"
)

func TestLowerGenericConstrainedOwnerMethodParity(t *testing.T) {
	root := t.TempDir()
	src := strings.TrimSpace(`
type any interface {}

type Box<T: any> struct {
    Marker: i32 = 0
}

fn Box<T>::Id(v: T) T {
    return v
}

fn Box<T>::ConstValue() i32 {
    return 7
}

fn main() i32 {
    let x = Box<i32>::Id(11)
    let y = Box<i64>::Id(22)
    let z = Box<i32>::ConstValue()
    return x + z + (y as i32)
}
`) + "\n"
	mainPath := filepath.Join(root, "main.ferr")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write main source: %v", err)
	}
	result := compiler.ParsePath(mainPath)
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	layouts := make(map[string]*layout.Module)
	modules := make(map[string]*mir.Module)
	for _, mod := range result.Modules {
		if mod == nil {
			continue
		}
		if mod.Layout != nil {
			layouts[mod.Key] = mod.Layout
		}
		if mod.MIR != nil {
			modules[mod.Key] = mod.MIR
		}
	}
	if result.Entry != nil {
		if result.Entry.Layout != nil {
			layouts[result.Entry.Key] = result.Entry.Layout
		}
		if result.Entry.MIR != nil {
			modules[result.Entry.Key] = result.Entry.MIR
		}
	}
	unit := &backend.Unit{
		Module:  result.Entry.MIR,
		Layout:  result.Entry.Layout,
		Layouts: layouts,
		Modules: modules,
	}

	for _, target := range []backend.Target{backend.TargetLLVM, backend.TargetQBE} {
		lowerer, err := registry.New(target)
		if err != nil {
			t.Fatalf("new lowerer %s: %v", target, err)
		}
		artifact, err := lowerer.LowerModule(unit)
		if err != nil {
			t.Fatalf("lower %s: %v", target, err)
		}
		for _, want := range []string{"Box__Id_", "Box__ConstValue"} {
			if !strings.Contains(artifact.Text, want) {
				t.Fatalf("target %s expected %q in lowered output:\n%s", target, want, artifact.Text)
			}
		}
		if target == backend.TargetLLVM {
			for _, want := range []string{
				"define i32 @main__Box__Id_",
				"define i64 @main__Box__Id_",
				"call i32 @main__Box__Id_",
				"call i64 @main__Box__Id_",
			} {
				if !strings.Contains(artifact.Text, want) {
					t.Fatalf("target %s expected %q in lowered output:\n%s", target, want, artifact.Text)
				}
			}
		}
		if target == backend.TargetQBE {
			for _, want := range []string{
				"function w $main__Box__Id_",
				"function l $main__Box__Id_",
				"call $main__Box__Id_",
			} {
				if !strings.Contains(artifact.Text, want) {
					t.Fatalf("target %s expected %q in lowered output:\n%s", target, want, artifact.Text)
				}
			}
		}
	}
}
