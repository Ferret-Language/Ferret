package toml

type Value any

type Table map[string]Value

type Data struct {
	Sections     map[string]Table
	SectionOrder []string
	KeyOrder     map[string][]string
}

func NewData() Data {
	return Data{
		Sections:     make(map[string]Table),
		SectionOrder: []string{},
		KeyOrder:     make(map[string][]string),
	}
}
