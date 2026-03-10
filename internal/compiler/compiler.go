package compiler

import (
	"os"
	"path/filepath"

	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/pipeline"
	"compiler/internal/project"
)

type Result struct {
	Entry         *context.Module
	Modules       []*context.Module
	Module        *ast.Module
	Diagnostics   *diagnostics.Bag
	CompilerState *context.CompilerContext
}

type Compiler struct {
	ctx      *context.CompilerContext
	pipeline *pipeline.Pipeline
}

func New(rootDir, extension string, diag *diagnostics.Bag) *Compiler {
	ctx := context.New(rootDir, extension, diag)
	return &Compiler{ctx: ctx, pipeline: pipeline.New(ctx)}
}

func NewWithConfig(cfg context.Config, diag *diagnostics.Bag) *Compiler {
	ctx := context.NewWithConfig(cfg, diag)
	return &Compiler{ctx: ctx, pipeline: pipeline.New(ctx)}
}

func ParseFile(path string) Result {
	return ParsePath(path)
}

func ParsePath(path string) Result {
	absPath, err := filepath.Abs(path)
	diag := diagnostics.NewDiagnosticBag(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return Result{Diagnostics: diag}
	}
	info, err := os.Stat(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return Result{Diagnostics: diag}
	}
	if info.IsDir() {
		ws, err := project.Load(absPath, ".ferr")
		if err != nil {
			diag.Add(diagnostics.NewError(err.Error()))
			return Result{Diagnostics: diag}
		}
		compiler := NewWithConfig(ws.Context, diag)
		return compiler.ParseWorkspace()
	}
	ws, err := project.Load(absPath, filepath.Ext(absPath))
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return Result{Diagnostics: diag}
	}
	compiler := NewWithConfig(ws.Context, diag)
	return compiler.ParseEntry(absPath)
}

func (c *Compiler) ParseEntry(entryFile string) Result {
	entry, err := c.pipeline.ParseEntry(entryFile)
	if err != nil {
		c.ctx.Diagnostics.Add(diagnostics.NewError(err.Error()))
	}
	result := Result{
		Entry:         entry,
		Modules:       c.ctx.Modules(),
		Diagnostics:   c.ctx.Diagnostics,
		CompilerState: c.ctx,
	}
	if entry != nil {
		result.Module = entry.AST
	}
	return result
}

func (c *Compiler) ParseWorkspace() Result {
	mods, err := c.pipeline.ParseWorkspace()
	if err != nil {
		c.ctx.Diagnostics.Add(diagnostics.NewError(err.Error()))
	}
	return Result{
		Modules:       mods,
		Diagnostics:   c.ctx.Diagnostics,
		CompilerState: c.ctx,
	}
}
