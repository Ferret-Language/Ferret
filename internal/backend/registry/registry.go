package registry

import (
	"fmt"
	"sort"

	"compiler/internal/backend"
	"compiler/internal/backend/llvm"
	"compiler/internal/backend/qbe"
)

func New(target backend.Target) (backend.Lowerer, error) {
	switch target {
	case backend.TargetQBE:
		return qbe.New(), nil
	case backend.TargetLLVM:
		return llvm.New(), nil
	default:
		return nil, fmt.Errorf("unknown backend target %q", target)
	}
}

func Targets() []backend.Target {
	out := []backend.Target{backend.TargetLLVM, backend.TargetQBE}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
