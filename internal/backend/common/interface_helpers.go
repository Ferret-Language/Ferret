package common

type InterfaceVTableKey struct {
	Iface    string
	Concrete string
}

type InterfaceWrapperKey struct {
	Iface    string
	Concrete string
	Method   string
}

type InterfaceHelperCache struct {
	VTables      map[InterfaceVTableKey]string
	Wrappers     map[InterfaceWrapperKey]struct{}
	RuntimeTypes map[string]string
}
