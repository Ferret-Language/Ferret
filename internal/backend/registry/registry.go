package registry

import (
	"fmt"

	"compiler/internal/backend"
	"compiler/internal/backend/llvm"
)

func New(target backend.Target) (backend.Lowerer, error) {
	switch target {
	case backend.TargetLLVM:
		return llvm.New(), nil
	default:
		return nil, fmt.Errorf("unknown backend target %q", target)
	}
}

func Targets() []backend.Target {
	return []backend.Target{backend.TargetLLVM}
}
