package llvm

import (
	"fmt"

	"compiler/internal/backend"
)

type lowerer struct{}

func New() backend.Lowerer { return &lowerer{} }

func (*lowerer) Target() backend.Target { return backend.TargetLLVM }

func (*lowerer) LowerModule(unit *backend.Unit) (*backend.Artifact, error) {
	if err := backend.ValidateUnit(unit); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("llvm backend lowering not implemented")
}
