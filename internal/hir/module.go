package hir

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/typeinfo"
	"compiler/internal/source"
)

type Module struct {
	Key        string
	ImportPath string
	FilePath   string
	Source     *ast.Module
	Globals    []*Global
	Functions  []*Func
}

type Global struct {
	Name     string
	Mutable  bool
	Constant bool
	Type     typeinfo.Type
	Value    Expr
	Location source.Location
	Source   ast.Decl
}

type Func struct {
	Name     string
	Receiver *Param
	Params   []*Param
	Result   typeinfo.Type
	Body     *BlockStmt
	Location source.Location
	Source   *ast.FuncDecl
}

type Param struct {
	Name       string
	Type       typeinfo.Type
	IsComptime bool
	Location   source.Location
}
