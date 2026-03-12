package backend

import (
	"fmt"

	"compiler/internal/layout"
	midmir "compiler/internal/middleend/mir"
)

type Target string

const (
	TargetQBE  Target = "qbe"
	TargetLLVM Target = "llvm"
)

type Unit struct {
	Module  *midmir.Module
	Layout  *layout.Module
	Layouts map[string]*layout.Module
	Modules map[string]*midmir.Module
}

type Artifact struct {
	Target    Target
	ModuleKey string
	FileExt   string
	Text      string
}

type Lowerer interface {
	Target() Target
	LowerModule(*Unit) (*Artifact, error)
}

func ValidateUnit(unit *Unit) error {
	if unit == nil {
		return fmt.Errorf("nil backend unit")
	}
	if unit.Module == nil {
		return fmt.Errorf("nil MIR module")
	}
	if unit.Layout == nil {
		return fmt.Errorf("nil layout module")
	}
	if unit.Layouts == nil {
		unit.Layouts = map[string]*layout.Module{unit.Module.Key: unit.Layout}
	}
	return nil
}
