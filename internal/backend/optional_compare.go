package backend

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/ir/mir"
)

// TaggedOptionalAgainstNone returns the optional operand when a binary compare
// is between a tagged optional and `none`. Niche optionals are intentionally
// excluded because they already lower as scalar/null compares.
func TaggedOptionalAgainstNone(left, right mir.Value) (mir.Value, *typeinfo.OptionalType, bool) {
	if _, isNone := left.(*mir.NoneValue); isNone {
		if opt, ok := UnwrapNamed(right.Type()).(*typeinfo.OptionalType); ok && !OptionalUsesNiche(opt.Inner) {
			return right, opt, true
		}
	}
	if _, isNone := right.(*mir.NoneValue); isNone {
		if opt, ok := UnwrapNamed(left.Type()).(*typeinfo.OptionalType); ok && !OptionalUsesNiche(opt.Inner) {
			return left, opt, true
		}
	}
	return nil, nil, false
}
