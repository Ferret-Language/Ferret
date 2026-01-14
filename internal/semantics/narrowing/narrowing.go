package narrowing

import (
	"compiler/internal/context_v2"
	"compiler/internal/frontend/ast"
	"compiler/internal/types"
)

// NarrowingContext tracks type narrowing information for variables within a scope.
// Used for type narrowing based on conditions.
type NarrowingContext struct {
	// Entries maps expression keys to their narrowing entries.
	Entries map[string]*NarrowingEntry

	// Parent allows nested scopes (for nested if statements, loops, etc.)
	Parent *NarrowingContext
}

// NewNarrowingContext creates a new narrowing context
func NewNarrowingContext(parent *NarrowingContext) *NarrowingContext {
	return &NarrowingContext{
		Entries: make(map[string]*NarrowingEntry),
		Parent:  parent,
	}
}

// Narrow records that an expression key has been narrowed to a specific type.
func (nc *NarrowingContext) Narrow(key string, entry *NarrowingEntry) {
	if nc == nil {
		return
	}
	if key == "" || entry == nil {
		return
	}
	nc.Entries[key] = entry
}

// GetEntry returns the narrowing entry for an expression key, if any.
func (nc *NarrowingContext) GetEntry(key string) (*NarrowingEntry, bool) {
	if nc == nil {
		return nil, false
	}

	// Check current scope
	if entry, ok := nc.Entries[key]; ok {
		return entry, true
	}

	// Check parent scopes
	if nc.Parent != nil {
		return nc.Parent.GetEntry(key)
	}

	return nil, false
}

// GetNarrowedType returns the narrowed type for an expression key, if any.
func (nc *NarrowingContext) GetNarrowedType(key string) (types.SemType, bool) {
	entry, ok := nc.GetEntry(key)
	if !ok || entry == nil || entry.NarrowedType == nil {
		return nil, false
	}
	return entry.NarrowedType, true
}

// NarrowingAnalyzer defines the interface for type-specific narrowing analysis
type NarrowingAnalyzer interface {
	// AnalyzeCondition checks if a condition narrows types and returns then/else contexts
	AnalyzeCondition(ctx *context_v2.CompilerContext, mod *context_v2.Module, condition ast.Expression, parent *NarrowingContext) (*NarrowingContext, *NarrowingContext)
}
