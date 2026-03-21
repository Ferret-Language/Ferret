package ownership

import (
	ownershipv2 "compiler/internal/analysis/semantics/ownershipv2"
	"compiler/internal/core/context"
)

// AnalyzeModule is kept as a compatibility entrypoint.
// Ownership analysis implementation now lives in ownershipv2.
func AnalyzeModule(ctx *context.CompilerContext, mod *context.Module) {
	ownershipv2.AnalyzeModule(ctx, mod)
}
