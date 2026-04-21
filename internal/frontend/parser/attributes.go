package parser

type DeclAttributeSpec struct {
	Name          string
	FunctionOnly  bool
	MaxArgs       int
	NoArgsMessage string
	Doc           string
}

var declAttributeSpecs = []DeclAttributeSpec{
	{Name: "extern", FunctionOnly: true, MaxArgs: 1, Doc: "Marks a function as externally linked. Optional argument overrides the linked symbol name."},
	{Name: "builtin", FunctionOnly: true, MaxArgs: 1, Doc: "Marks a function as compiler/runtime-provided. Optional argument overrides the linked symbol name."},
	{Name: "allow_unused", MaxArgs: 0, NoArgsMessage: "#[allow_unused] does not accept arguments", Doc: "Suppresses unused diagnostics for the annotated declaration."},
	{Name: "if", MaxArgs: -1, Doc: "Includes the annotated declaration only when the compile-time condition matches."},
	{Name: "ifnot", MaxArgs: -1, Doc: "Includes the annotated declaration only when the compile-time condition does not match."},
}

var declAttributeSpecByName = buildDeclAttributeSpecByName(declAttributeSpecs)

func buildDeclAttributeSpecByName(specs []DeclAttributeSpec) map[string]DeclAttributeSpec {
	byName := make(map[string]DeclAttributeSpec, len(specs))
	for _, spec := range specs {
		if spec.Name == "" {
			continue
		}
		byName[spec.Name] = spec
	}
	return byName
}

func LookupDeclAttributeSpec(name string) (DeclAttributeSpec, bool) {
	spec, ok := declAttributeSpecByName[name]
	return spec, ok
}

func DeclAttributeNames() []string {
	names := make([]string, 0, len(declAttributeSpecs))
	for _, spec := range declAttributeSpecs {
		if spec.Name == "" {
			continue
		}
		names = append(names, spec.Name)
	}
	return names
}
