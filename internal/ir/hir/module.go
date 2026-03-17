package hir

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
)

type Module struct {
	Key        string
	ImportPath string
	FilePath   string
	Source     *ast.Module
	Types      []*TypeDecl
	Globals    []*Global
	Functions  []*Func
}

type TypeDecl struct {
	Name       string
	Named      *typeinfo.NamedType
	Underlying typeinfo.Type
	Struct     *StructTypeDecl
	Interface  *InterfaceTypeDecl
	Enum       *EnumTypeDecl
	Union      *UnionTypeDecl
	Error      *ErrorTypeDecl
	Location   source.Location
	Source     *ast.TypeDecl
}

type StructTypeDecl struct {
	Fields       []*StructFieldDecl
	StaticFields []*StructFieldDecl
}

type StructFieldDecl struct {
	Name     string
	Type     typeinfo.Type
	Default  Expr
	Location source.Location
}

type InterfaceTypeDecl struct {
	Methods []*InterfaceMethodDecl
}

type InterfaceMethodDecl struct {
	Receiver string
	Name     string
	Params   []*Param
	Result   typeinfo.Type
	Location source.Location
}

type EnumTypeDecl struct {
	Variants []string
}

type UnionTypeDecl struct {
	Members []typeinfo.Type
}

type ErrorTypeDecl struct {
	Members []string
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
	Name       string
	IsUnsafe   bool
	IsBuiltin  bool
	IsExtern   bool
	ExternName string
	Receiver   *Param
	Params     []*Param
	Result     typeinfo.Type
	Body       *BlockStmt
	// LocalCount is the number of local slots in the function. Generator seeds
	// this with resolver-bound locals; HIR lowering may append temporaries.
	LocalCount int
	Location   source.Location
	Source     *ast.FuncDecl
}

type Param struct {
	Name       string
	LocalID    int
	Type       typeinfo.Type
	IsComptime bool
	Location   source.Location
}
