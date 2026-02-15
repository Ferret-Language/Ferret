package context_v2

// GlobalModuleImport is the implicit prelude module loaded into the universe scope.
const GlobalModuleImport = "global"

var compilerBuiltinHandleTypeNames = []string{
	"__file",
	"__tcp_listener",
	"__tcp_conn",
	"__http_app",
	"__http_response",
	"__stream",
}

var compilerBuiltinHandleTypeSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(compilerBuiltinHandleTypeNames))
	for _, name := range compilerBuiltinHandleTypeNames {
		out[name] = struct{}{}
	}
	return out
}()

var compilerResourceHandleTypeSet = map[string]struct{}{
	"__file":          {},
	"__tcp_listener":  {},
	"__tcp_conn":      {},
	"__http_app":      {},
	"__http_response": {},
}

// CompilerBuiltinHandleTypeNames returns compiler-owned builtin handle type names.
func CompilerBuiltinHandleTypeNames() []string {
	out := make([]string, len(compilerBuiltinHandleTypeNames))
	copy(out, compilerBuiltinHandleTypeNames)
	return out
}

// IsCompilerBuiltinHandleTypeName reports whether name is a compiler-owned builtin handle type.
func IsCompilerBuiltinHandleTypeName(name string) bool {
	_, ok := compilerBuiltinHandleTypeSet[name]
	return ok
}

// IsCompilerResourceHandleTypeName reports whether name is a resource-owning builtin handle type.
func IsCompilerResourceHandleTypeName(name string) bool {
	_, ok := compilerResourceHandleTypeSet[name]
	return ok
}
