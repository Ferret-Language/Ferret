package semmeta

type ValueFlags uint8

const (
	FlagMutable ValueFlags = 1 << iota
	FlagVariadic
)

func (f ValueFlags) Mutable() bool {
	return f&FlagMutable != 0
}

func (f ValueFlags) Variadic() bool {
	return f&FlagVariadic != 0
}

type ValueSpec[T any] struct {
	Name       string
	Type       T
	Flags      ValueFlags
	HasDefault bool
}

type ReceiverKind uint8

const (
	ReceiverValue ReceiverKind = iota
	ReceiverRef
	ReceiverRefMut
	ReceiverPtr
	ReceiverRawPtr
)

func ReceiverKindFromSyntax(receiver string) ReceiverKind {
	switch receiver {
	case "&":
		return ReceiverRef
	case "&mut ":
		return ReceiverRefMut
	case "*":
		return ReceiverPtr
	case "^":
		return ReceiverRawPtr
	case "^const ":
		return ReceiverRawPtr
	default:
		return ReceiverValue
	}
}

func (k ReceiverKind) Prefix() string {
	switch k {
	case ReceiverRef:
		return "&"
	case ReceiverRefMut:
		return "&mut "
	case ReceiverPtr:
		return "*"
	case ReceiverRawPtr:
		return "^"
	default:
		return ""
	}
}

type ReceiverKey struct {
	Kind     ReceiverKind
	TypeName string
}

func (k ReceiverKey) String() string {
	return k.Kind.Prefix() + k.TypeName
}
