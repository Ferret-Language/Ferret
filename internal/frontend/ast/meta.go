package ast

import "compiler/internal/core/source"

type CommentGroup struct {
	Text     string
	Location source.Location
}

type Attribute struct {
	Name     string
	Args     []string
	Location source.Location
}
