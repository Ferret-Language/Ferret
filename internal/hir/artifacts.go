package hir

import (
	"compiler/internal/context_v2"
	"compiler/internal/types"
)

const (
	moduleKey        = "hir.module"
	loweredModuleKey = "hir.loweredModule"
	heapReturnKey    = "hir.heapReturns"
)

// ModuleFromModule returns the source-shaped HIR module stored on the compiler module, if any.
func ModuleFromModule(mod *context_v2.Module) *Module {
	if mod == nil || mod.Artifacts == nil {
		return nil
	}

	if val, ok := mod.Artifacts[moduleKey]; ok {
		if typed, ok := val.(*Module); ok {
			return typed
		}
	}
	return nil
}

// StoreModule saves the source-shaped HIR module on the compiler module artifacts.
func StoreModule(mod *context_v2.Module, hirMod *Module) {
	if mod == nil || hirMod == nil {
		return
	}

	if mod.Artifacts == nil {
		mod.Artifacts = make(map[string]any)
	}

	mod.Artifacts[moduleKey] = hirMod
}

// LoweredModuleFromModule returns the lowered HIR module stored on the compiler module, if any.
func LoweredModuleFromModule(mod *context_v2.Module) *Module {
	if mod == nil || mod.Artifacts == nil {
		return nil
	}

	if val, ok := mod.Artifacts[loweredModuleKey]; ok {
		if typed, ok := val.(*Module); ok {
			return typed
		}
	}
	return nil
}

// StoreLoweredModule saves the lowered HIR module on the compiler module artifacts.
func StoreLoweredModule(mod *context_v2.Module, hirMod *Module) {
	if mod == nil || hirMod == nil {
		return
	}

	if mod.Artifacts == nil {
		mod.Artifacts = make(map[string]any)
	}

	mod.Artifacts[loweredModuleKey] = hirMod
}

// HeapReturnMapFromModule returns the heap-return map stored on the compiler module, if any.
func HeapReturnMapFromModule(mod *context_v2.Module) map[string]types.SemType {
	if mod == nil || mod.Artifacts == nil {
		return nil
	}
	if val, ok := mod.Artifacts[heapReturnKey]; ok {
		if typed, ok := val.(map[string]types.SemType); ok {
			return typed
		}
	}
	return nil
}

// StoreHeapReturnMap saves the heap-return map on the compiler module artifacts.
func StoreHeapReturnMap(mod *context_v2.Module, heapReturns map[string]types.SemType) {
	if mod == nil {
		return
	}
	if mod.Artifacts == nil {
		mod.Artifacts = make(map[string]any)
	}
	if heapReturns == nil {
		heapReturns = make(map[string]types.SemType)
	}
	mod.Artifacts[heapReturnKey] = heapReturns
}
