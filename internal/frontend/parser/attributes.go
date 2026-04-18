package parser

type DeclAttributeSpec struct {
	Name          string
	FunctionOnly  bool
	MaxArgs       int
	NoArgsMessage string
}

var declAttributeSpecs = []DeclAttributeSpec{
	{Name: "extern", FunctionOnly: true, MaxArgs: 1},
	{Name: "builtin", FunctionOnly: true, MaxArgs: 1},
	{Name: "allow_unused", MaxArgs: 0, NoArgsMessage: "#[allow_unused] does not accept arguments"},
	{Name: "if", MaxArgs: -1},
	{Name: "ifnot", MaxArgs: -1},
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
