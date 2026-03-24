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

const CompilerVersion = "0.0.2"
const FerretSourceExt = ".fer"
const LegacyFerretSourceExt = ".fer"

type Result struct {
	Entry         *context.Module
	Modules       []*context.Module
	Module        *ast.Module
	Diagnostics   *diagnostics.DiagnosticBag
	CompilerState *context.CompilerContext
}

type Compiler struct {
	ctx      *context.CompilerContext
	pipeline *pipeline.Pipeline
}

func New(rootDir, extension string, diag *diagnostics.DiagnosticBag) *Compiler {
	ctx := context.New(rootDir, extension, diag)
	if err := prelude.Load(ctx); err != nil {
		ctx.Diagnostics.Add(diagnostics.NewError(err.Error()))
	}
	return &Compiler{ctx: ctx, pipeline: pipeline.New(ctx)}
}

func NewWithConfig(cfg context.Config, diag *diagnostics.DiagnosticBag) *Compiler {
	ctx := context.NewWithConfig(cfg, diag)
	if err := prelude.Load(ctx); err != nil {
		ctx.Diagnostics.Add(diagnostics.NewError(err.Error()))
	}
	return &Compiler{ctx: ctx, pipeline: pipeline.New(ctx)}
}

func ParseFile(path string) Result {
	return ParsePath(path)
}

type parseMode uint8

const (
	parseModeFull parseMode = iota
	parseModeIDE
)

func parsePath(path string, mode parseMode) Result {
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
		ws, err := project.Load(absPath, ".fer")
		if err != nil {
			diag.Add(diagnostics.NewError(err.Error()))
			return Result{Diagnostics: diag}
		}
		c := NewWithConfig(ws.Context, diag)
		switch mode {
		case parseModeIDE:
			mods, err := c.pipeline.ParseWorkspaceForIDE()
			if err != nil {
				c.ctx.Diagnostics.Add(diagnostics.NewError(err.Error()))
			}
			return Result{
				Modules:       mods,
				Diagnostics:   c.ctx.Diagnostics,
				CompilerState: c.ctx,
			}
		default:
			return c.ParseWorkspace()
		}
	}
	ext := strings.ToLower(filepath.Ext(absPath))
	if ext != FerretSourceExt && ext != LegacyFerretSourceExt {
		diag.Add(diagnostics.NewError("unsupported source file extension"))
		return Result{Diagnostics: diag}
	}
	ws, err := project.Load(absPath, ext)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return Result{Diagnostics: diag}
	}
	c := NewWithConfig(ws.Context, diag)
	switch mode {
	case parseModeIDE:
		entry, err := c.pipeline.ParseEntryForIDE(absPath)
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
	default:
		return c.ParseEntry(absPath)
	}
}

func ParsePath(path string) Result {
	return parsePath(path, parseModeFull)
}

// ParsePathForIDE parses enough of the project/workspace to provide accurate
// name resolution and type information, but intentionally skips expensive
// backend passes (HIR lowering/specialization, MIR, comptime, ownership).
//
// Intended for editor tooling (LSP) to avoid large transient memory spikes.
func ParsePathForIDE(path string) Result {
	return parsePath(path, parseModeIDE)
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
