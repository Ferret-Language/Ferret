package diagnostics

// Error codes for Ferret compiler
const (
	ErrMax = "E0010"

	// Lexer errors (L prefix)
	ErrUnexpectedCharacter = "L0001"
	ErrUnterminatedString  = "L0002"
	ErrInvalidNumber       = "L0003"
	ErrInvalidEscape       = "L0004"
	ErrUnterminatedComment = "L0005"
	ErrUnterminatedBlock   = "L0006"
	ErrUnterminatedExpr    = "L0007"

	// Parser errors (P prefix)
	ErrUnexpectedToken    = "P0001"
	ErrExpectedToken      = "P0002"
	ErrInvalidExpression  = "P0003"
	ErrInvalidStatement   = "P0004"
	ErrInvalidDeclaration = "D0005"
	ErrMissingIdentifier  = "P0006"
	ErrMissingType        = "P0007"
	ErrMissingInitializer = "P0008"
	ErrMissingSemiCol     = "P0009"

	// Type checker errors (T prefix)
	ErrTypeMismatch            = "T0001"
	ErrUndefinedSymbol         = "T0002"
	ErrRedeclaredSymbol        = "T0003"
	ErrInvalidOperation        = "T0004"
	ErrNotCallable             = "T0005"
	ErrWrongArgumentCount      = "T0006"
	ErrInvalidAssignment       = "T0007"
	ErrNotIndexable            = "T0008"
	ErrArrayOutOfBounds        = "T0009"
	ErrFieldNotFound           = "T0010"
	ErrMethodNotFound          = "T0011"
	ErrInterfaceNotImplemented = "T0012"
	ErrCircularDependency      = "T0013"
	ErrInvalidCast             = "T0014"
	ErrNonExhaustiveMatch      = "T0015"
	ErrInvalidReturn           = "T0016"
	ErrMissingReturn           = "T0017"
	ErrConstantReassignment    = "T0018"
	ErrInvalidBreak            = "T0019"
	ErrInvalidContinue         = "T0020"
	ErrInvalidType             = "T0021"
	ErrInvalidMethodReceiver   = "T0022"
	ErrUncaughtError           = "T0023"
	ErrInvalidCatch            = "T0024"
	ErrInvalidErrorReturn      = "T0025"
	ErrMissingCatch            = "T0026"
	ErrUseBeforeDecl           = "T0027"
	ErrArrayIndexNotConst      = "T0028"
	ErrMissingField            = "T0029"
	ErrUnknownField            = "T0030"

	// Module/Import errors (M prefix)
	ErrModuleNotFound    = "M0001"
	ErrCyclicImport      = "M0002"
	ErrInvalidImportPath = "M0003"
	ErrSymbolNotExported = "M0004"
	ErrAmbiguousImport   = "M0005"

	// Style/Info codes (S prefix)
	InfoTrailingComma        = "S0001"
	InfoUnnecessarySemicolon = "S0002"

	// Warnings (W prefix)
	WarnUnreachableCode        = "W0001"
	WarnConstantConditionTrue  = "W0002"
	WarnConstantConditionFalse = "W0003"
	WarnDeadCode               = "W0004"
	WarnValueReceiverMutation  = "W0005"
)
