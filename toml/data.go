package toml

type TOMLValue any

type TOMLTable map[string]TOMLValue

type TOMLData struct {
	Sections     map[string]TOMLTable
	SectionOrder []string
	KeyOrder     map[string][]string // Track key order within each section
}

func NewTOMLData() TOMLData {
	return TOMLData{
		Sections:     make(map[string]TOMLTable),
		SectionOrder: []string{},
		KeyOrder:     make(map[string][]string),
	}
}
