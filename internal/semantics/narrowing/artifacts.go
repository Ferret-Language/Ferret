// Package narrowing provides type narrowing analysis and metadata storage.
package narrowing

import (
	"compiler/internal/context_v2"
	"compiler/internal/types"
)

const narrowingInfoKey = "narrowing.info"

// NarrowingKind indicates what kind of narrowing is being applied.
type NarrowingKind int

const (
	// NarrowingUnion narrows a union type to a specific variant.
	NarrowingUnion NarrowingKind = iota
	// NarrowingOptional narrows an optional type to its inner type (not none).
	NarrowingOptional
	// NarrowingInterface narrows an interface{} to a concrete type (future).
	NarrowingInterface
)

// NarrowingEntry represents a single narrowing of a value at a specific scope.
type NarrowingEntry struct {
	// Kind indicates the type of narrowing (union, optional, interface).
	Kind NarrowingKind

	// VarName is the expression key for the narrowed value.
	VarName string

	// OriginalType is the type before narrowing (e.g., union { i32, str }).
	OriginalType types.SemType

	// NarrowedType is the type after narrowing (e.g., str).
	NarrowedType types.SemType

	// VariantIndex is the index of the variant in union types (-1 for non-unions).
	VariantIndex int
}

// ScopeNarrowing holds all narrowing entries for a specific scope (block).
type ScopeNarrowing struct {
	// Entries maps expression keys to their narrowing info.
	Entries map[string]*NarrowingEntry
}

// NewScopeNarrowing creates a new scope narrowing container.
func NewScopeNarrowing() *ScopeNarrowing {
	return &ScopeNarrowing{
		Entries: make(map[string]*NarrowingEntry),
	}
}

// Add adds a narrowing entry for a variable.
func (s *ScopeNarrowing) Add(entry *NarrowingEntry) {
	if s == nil || entry == nil {
		return
	}
	s.Entries[entry.VarName] = entry
}

// Get returns the narrowing entry for a variable, if any.
func (s *ScopeNarrowing) Get(varName string) *NarrowingEntry {
	if s == nil {
		return nil
	}
	return s.Entries[varName]
}

// NarrowingInfo stores all narrowing metadata for a module.
// It maps scope identifiers (using AST node pointers as keys) to their narrowings.
type NarrowingInfo struct {
	// Scopes maps scope keys to their narrowing data.
	// The key is typically a string representation of the AST block location.
	Scopes map[string]*ScopeNarrowing
}

// NewNarrowingInfo creates a new narrowing info container.
func NewNarrowingInfo() *NarrowingInfo {
	return &NarrowingInfo{
		Scopes: make(map[string]*ScopeNarrowing),
	}
}

// GetOrCreateScope returns the scope narrowing for a key, creating it if needed.
func (n *NarrowingInfo) GetOrCreateScope(scopeKey string) *ScopeNarrowing {
	if n == nil {
		return nil
	}
	if scope, ok := n.Scopes[scopeKey]; ok {
		return scope
	}
	scope := NewScopeNarrowing()
	n.Scopes[scopeKey] = scope
	return scope
}

// GetScope returns the scope narrowing for a key, or nil if not found.
func (n *NarrowingInfo) GetScope(scopeKey string) *ScopeNarrowing {
	if n == nil {
		return nil
	}
	return n.Scopes[scopeKey]
}

// NarrowedTypesForScope returns a map of narrowed variable types for a scope key.
func NarrowedTypesForScope(info *NarrowingInfo, scopeKey string) map[string]types.SemType {
	if info == nil || scopeKey == "" {
		return nil
	}
	return NarrowedTypes(info.GetScope(scopeKey))
}

// NarrowedEntriesForScope returns a map of narrowed entries for a scope key.
func NarrowedEntriesForScope(info *NarrowingInfo, scopeKey string) map[string]*NarrowingEntry {
	if info == nil || scopeKey == "" {
		return nil
	}
	return NarrowedEntries(info.GetScope(scopeKey))
}

// NarrowedTypes returns a map of narrowed variable types for a scope.
func NarrowedTypes(scope *ScopeNarrowing) map[string]types.SemType {
	if scope == nil || len(scope.Entries) == 0 {
		return nil
	}
	out := make(map[string]types.SemType, len(scope.Entries))
	for name, entry := range scope.Entries {
		if entry == nil || entry.NarrowedType == nil {
			continue
		}
		out[name] = entry.NarrowedType
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NarrowedEntries returns a map of narrowed entries for a scope.
func NarrowedEntries(scope *ScopeNarrowing) map[string]*NarrowingEntry {
	if scope == nil || len(scope.Entries) == 0 {
		return nil
	}
	out := make(map[string]*NarrowingEntry, len(scope.Entries))
	for name, entry := range scope.Entries {
		if entry == nil {
			continue
		}
		out[name] = entry
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// StoreNarrowingInfo saves narrowing info to module artifacts.
func StoreNarrowingInfo(mod *context_v2.Module, info *NarrowingInfo) {
	if mod == nil || info == nil {
		return
	}
	if mod.Artifacts == nil {
		mod.Artifacts = make(map[string]any)
	}
	mod.Artifacts[narrowingInfoKey] = info
}

// GetNarrowingInfo retrieves narrowing info from module artifacts.
func GetNarrowingInfo(mod *context_v2.Module) *NarrowingInfo {
	if mod == nil || mod.Artifacts == nil {
		return nil
	}
	if val, ok := mod.Artifacts[narrowingInfoKey]; ok {
		if typed, ok := val.(*NarrowingInfo); ok {
			return typed
		}
	}
	return nil
}

// GetOrCreateNarrowingInfo returns existing narrowing info or creates new one.
func GetOrCreateNarrowingInfo(mod *context_v2.Module) *NarrowingInfo {
	if mod == nil {
		return nil
	}
	if info := GetNarrowingInfo(mod); info != nil {
		return info
	}
	info := NewNarrowingInfo()
	StoreNarrowingInfo(mod, info)
	return info
}

// ScopeKeyFromLocation creates a scope key from file path and line/column.
func ScopeKeyFromLocation(filePath string, line, col int) string {
	return filePath + ":" + itoa(line) + ":" + itoa(col)
}

// Simple int to string without fmt import
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
