package diagnostics

import (
	"compiler/internal/source"
	"compiler/internal/utils/strings"
	"fmt"
)

// Common diagnostic builders for type checker

// UndefinedSymbol creates a diagnostic for undefined symbol
func UndefinedSymbol(filepath string, loc *source.Location, name string) *Diagnostic {
	return NewError("undefined symbol: "+name).
		WithCode(ErrUndefinedSymbol).
		WithPrimaryLabel(loc, "not found in this scope").
		WithHelp("check if the symbol is declared and imported correctly")
}

// RedeclaredSymbol creates a diagnostic for redeclared symbol
func RedeclaredSymbol(filepath string, newLoc, prevLoc *source.Location, name string) *Diagnostic {
	return NewError(name+" is already declared").
		WithCode(ErrRedeclaredSymbol).
		WithPrimaryLabel(newLoc, "redeclared here").
		WithSecondaryLabel(prevLoc, "previously declared here").
		WithHelp("use a different name or remove one of the declarations")
}

// WrongArgumentCount creates a diagnostic for wrong number of arguments
func WrongArgumentCount(filepath string, loc *source.Location, expected, found int) *Diagnostic {
	return NewError("wrong number of arguments").
		WithCode(ErrWrongArgumentCount).
		WithPrimaryLabel(loc, fmt.Sprintf("expected %d %s, found %d",
			expected, strings.Pluralize("argument", "arguments", expected), found))
}

// WrongArgumentCountVariadic creates a diagnostic for wrong number of arguments to variadic function
func WrongArgumentCountVariadic(filepath string, loc *source.Location, minExpected, found int) *Diagnostic {
	return NewError("wrong number of arguments").
		WithCode(ErrWrongArgumentCount).
		WithPrimaryLabel(loc, fmt.Sprintf("expected at least %d %s, found %d",
			minExpected, strings.Pluralize("argument", "arguments", minExpected), found))
}

// ArgumentTypeMismatch creates a diagnostic for argument type mismatch
func ArgumentTypeMismatch(filepath string, loc *source.Location, param, expected, found string) *Diagnostic {

	msg := fmt.Sprintf("type mismatch in parameter '%s'", param)

	return NewError(msg).
		WithCode(ErrTypeMismatch).
		WithPrimaryLabel(loc, fmt.Sprintf("expected %s, found %s", expected, found))
}

// NotCallable creates a diagnostic for calling a non-function
func NotCallable(filepath string, loc *source.Location, typeName string) *Diagnostic {
	return NewError("cannot call non-function").
		WithCode(ErrNotCallable).
		WithPrimaryLabel(loc, fmt.Sprintf("type %s is not callable", typeName)).
		WithHelp("only functions can be called")
}

// FieldNotFound creates a diagnostic for field not found
func FieldNotFound(filepath string, loc *source.Location, fieldName, typeName string) *Diagnostic {
	return NewError("field "+fieldName+" not found").
		WithCode(ErrFieldNotFound).
		WithPrimaryLabel(loc, fmt.Sprintf("%s has no field %s", typeName, fieldName)).
		WithHelp("check the field name spelling")
}

// UncaughtError creates a diagnostic for calling a function that returns a Result type without handling the error
func UncaughtError(cache *SourceCache, filepath string, loc *source.Location, resultType string) *Diagnostic {
	var exprText string
	if cache != nil {
		exprText = loc.GetText(cache)
	}
	if exprText == "" {
		exprText = "expression"
	}
	return NewError("function returns a result type but error is not handled").
		WithCode(ErrUncaughtError).
		WithPrimaryLabel(loc, fmt.Sprintf("function returns %s", resultType)).
		WithHelp(fmt.Sprintf("add a '%s catch err { ... }' clause to handle the potential error", exprText))
}

// InvalidCatch creates a diagnostic for using catch on a non-Result function
func InvalidCatch(filepath string, loc *source.Location, actualType string) *Diagnostic {
	return NewError("cannot use 'catch' on function that does not return a result type").
		WithCode(ErrInvalidCatch).
		WithPrimaryLabel(loc, fmt.Sprintf("function returns %s, not a result type", actualType)).
		WithHelp("remove the 'catch' clause or call a function that can return an error")
}

// InvalidErrorReturn creates a diagnostic for returning error from non-Result function
func InvalidErrorReturn(filepath string, loc *source.Location, returnType string) *Diagnostic {
	return NewError("cannot return error from function that does not declare error type").
		WithCode(ErrInvalidErrorReturn).
		WithPrimaryLabel(loc, fmt.Sprintf("function returns %s, not a result type", returnType)).
		WithHelp("change function return type to 'T ! E' to allow error returns")
}

// MissingCatchFallback creates a diagnostic for catch clause without fallback or early return
func MissingCatchFallback(filepath string, loc *source.Location) *Diagnostic {
	return NewError("catch handler must either provide a fallback value or return early").
		WithCode(ErrInvalidCatch).
		WithPrimaryLabel(loc, "no fallback value provided and handler does not return").
		WithHelp("add a fallback value after the catch block or use 'return' in the handler")
}

// CatchTypeMismatch creates a diagnostic for catch fallback type mismatch
func CatchTypeMismatch(filepath string, loc *source.Location, expected, found string) *Diagnostic {
	return NewError("catch fallback type does not match expected type").
		WithCode(ErrTypeMismatch).
		WithPrimaryLabel(loc, fmt.Sprintf("expected %s, found %s", expected, found)).
		WithHelp("ensure fallback value has the same type as the success value")
}

// InvalidErrorPropagate creates a diagnostic for using !! on non-Result expression
func InvalidErrorPropagate(filepath string, loc *source.Location, actualType string) *Diagnostic {
	return NewError("cannot use '!!' on expression that does not return a result type").
		WithCode(ErrInvalidErrorPropagate).
		WithPrimaryLabel(loc, fmt.Sprintf("expression has type %s, not a result type", actualType)).
		WithHelp("'!!' can only be used on expressions that return a result type (E ! T)")
}

// ErrorPropagateNotInResultFunc creates a diagnostic for !! in a function that doesn't return a Result
func ErrorPropagateNotInResultFunc(filepath string, loc *source.Location, returnType string) *Diagnostic {
	return NewError("cannot use '!!' in function that does not return a result type").
		WithCode(ErrInvalidErrorPropagate).
		WithPrimaryLabel(loc, fmt.Sprintf("enclosing function returns %s, not a result type", returnType)).
		WithHelp("change the enclosing function's return type to a result type (E ! T) to propagate errors")
}

// ErrorPropagateTypeMismatch creates a diagnostic for !! with incompatible error types
func ErrorPropagateTypeMismatch(filepath string, loc *source.Location, innerErr, outerErr string) *Diagnostic {
	return NewError("error type mismatch in error propagation").
		WithCode(ErrInvalidErrorPropagate).
		WithPrimaryLabel(loc, fmt.Sprintf("expression error type is %s, but enclosing function error type is %s", innerErr, outerErr)).
		WithHelp("ensure the error types are compatible or use a catch clause to convert the error")
}
