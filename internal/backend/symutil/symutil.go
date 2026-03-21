package symutil

import "strings"

// LocalModulePrefix returns the canonical local symbol prefix for a module
// import path (for example, "std/io" -> "std__io__").
func LocalModulePrefix(importPath string) string {
	if importPath == "" {
		return ""
	}
	return strings.ReplaceAll(importPath, "/", "__") + "__"
}

// AddLinkLeafAlias inserts the local leaf name derived from linkName into
// names when linkName belongs to the current module prefix.
func AddLinkLeafAlias(names map[string]struct{}, localPrefix, linkName string) {
	if names == nil || localPrefix == "" || linkName == "" {
		return
	}
	if !strings.HasPrefix(linkName, localPrefix) {
		return
	}
	leaf := strings.TrimPrefix(linkName, localPrefix)
	if leaf == "" {
		return
	}
	names[leaf] = struct{}{}
}

// ResolveStaticOwnerLocalName canonicalizes Type::Method references into the
// local method symbol form Type__Method when present in local tables.
func ResolveStaticOwnerLocalName(path []string, functions, globals map[string]struct{}) (string, bool) {
	if len(path) != 2 {
		return "", false
	}
	local := path[0] + "__" + path[1]
	if _, ok := functions[local]; ok {
		return local, true
	}
	if _, ok := globals[local]; ok {
		return local, true
	}
	return "", false
}
