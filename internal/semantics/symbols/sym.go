package symbols

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/types"
)

// Forward declaration to avoid import cycle
type SymbolTable interface {
	Declare(name string, symbol *Symbol) error
	Lookup(name string) (*Symbol, bool)
	GetSymbol(name string) (*Symbol, bool)
}

// ConstValue forward declaration to avoid import cycle
// Actual implementation is in internal/hir/consteval
type ConstValue interface {
	IsConstant() bool
	String() string
}

// Symbol represents a declared entity (variable, function, type, etc.)
// Scopes are stored in AST nodes (FuncDecl.Scope, Block.Scope, etc.), not here.
type Symbol struct {
	Name          string
	Kind          SymbolKind
	Type          types.SemType          // Semantic type of the symbol
	OriginalType  types.SemType          // Original type before narrowing (for optional narrowing)
	Exported      bool                   // Whether symbol is accessible from other modules
	Decl          ast.Node               // AST node that declared this symbol
	DeclaredScope SymbolTable            // Scope where this symbol was declared
	Methods       map[string]*MethodInfo // Methods attached to this named type (only for SymbolType)
	ConstValue    ConstValue             // Compile-time known value (for constants and const variables)
	IsReadonly    bool                   // True for read-only variables (loop index, catch error, etc.)
	IsHeap        bool                   // True if storage is heap-allocated via '#'

	// Native function support (for @extern functions implemented in Go/C)
	IsNative   bool   // true if this function is implemented in native code (Go/C)
	NativeName string // C function name for code generation (e.g., "ferret_io_Println")
	Intrinsic  IntrinsicKind
}

// IntrinsicKind identifies compiler-known builtin functions with special lowering.
type IntrinsicKind int

const (
	IntrinsicNone IntrinsicKind = iota
	IntrinsicAddr
	IntrinsicSelfAddr
	IntrinsicReal
	IntrinsicImag
)

// MethodInfo stores information about a method attached to a named type
type MethodInfo struct {
	Name           string
	FuncType       *types.FunctionType
	Receiver       types.SemType
	ReceiverIsMove bool
	Decl           *ast.MethodDecl
	Exported       bool // Whether method is accessible from other modules
}

// SymbolKind categorizes symbols
type SymbolKind int

const (
	SymbolVariable SymbolKind = iota
	SymbolConstant
	SymbolFunction
	SymbolType
	SymbolConstraint
	SymbolTypeParameter
	SymbolParameter
	SymbolReceiver
)

func (s SymbolKind) String() string {
	switch s {
	case SymbolVariable:
		return "variable"
	case SymbolConstant:
		return "constant"
	case SymbolFunction:
		return "function"
	case SymbolType:
		return "type"
	case SymbolConstraint:
		return "constraint"
	case SymbolTypeParameter:
		return "type parameter"
	case SymbolParameter:
		return "parameter"
	case SymbolReceiver:
		return "receiver"
	}
	return "unknown"
}
