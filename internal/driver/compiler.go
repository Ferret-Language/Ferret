package compiler

import (
	"os"
	"path/filepath"
	"strings"

	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/project"
	"compiler/internal/frontend/ast"
	"compiler/internal/pipeline"
	"compiler/internal/prelude"
)

const CompilerVersion = "0.1.1"
const FerretSourceExt = ".ferr"

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
	if err := prelude.Load(ctx); err != nil {
		ctx.Diagnostics.Add(diagnostics.NewError(err.Error()))
	}
	return &Compiler{ctx: ctx, pipeline: pipeline.New(ctx)}
}

func NewWithConfig(cfg context.Config, diag *diagnostics.Bag) *Compiler {
	ctx := context.NewWithConfig(cfg, diag)
	if err := prelude.Load(ctx); err != nil {
		ctx.Diagnostics.Add(diagnostics.NewError(err.Error()))
	}
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
	if !strings.EqualFold(filepath.Ext(absPath), FerretSourceExt) {
		diag.Add(diagnostics.NewError("unsupported source file extension"))
		return Result{Diagnostics: diag}
	}
	ws, err := project.Load(absPath, FerretSourceExt)
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
