package prelude

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/analysis/semantics/collector"
	"compiler/internal/analysis/semantics/resolver"
	"compiler/internal/analysis/semantics/typechecker"
	"compiler/internal/core/context"
	"compiler/internal/core/phase"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
)

const globalModuleKey = "builtin:global"

var ExecutablePath = os.Executable

func Load(ctx *context.CompilerContext) error {
	if ctx == nil || ctx.Prelude != nil {
		return nil
	}
	path, err := findGlobalPrelude(ctx)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	mod := &context.Module{
		Key:        globalModuleKey,
		ImportPath: "global",
		FilePath:   filepath.Clean(path),
		Content:    string(content),
		Origin:     context.ModuleOriginStdlib,
		Phase:      phase.PhaseLoaded,
	}
	ctx.Diagnostics.AddSourceContent(mod.FilePath, mod.Content)
	mod.Tokens = lexer.New(mod.FilePath, mod.Content, ctx.Diagnostics).Tokenize()
	mod.Phase = phase.PhaseTokenized
	mod.AST = parser.Parse(mod.FilePath, mod.Tokens, ctx.Diagnostics)
	mod.Phase = phase.PhaseParsed
	collector.CollectModule(ctx, mod)
	resolver.ResolveModule(ctx, mod)
	typechecker.CheckModule(ctx, mod)
	ctx.Prelude = mod
	for _, sym := range mod.ModuleScope.Symbols() {
		_ = ctx.Universe.Declare(sym)
	}
	return nil
}

func findGlobalPrelude(ctx *context.CompilerContext) (string, error) {
	if ctx.Config.StdlibRoot != "" {
		candidate := filepath.Clean(filepath.Join(filepath.Dir(ctx.Config.StdlibRoot), "global.fer"))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	if execPath, err := ExecutablePath(); err == nil {
		execDir := filepath.Dir(execPath)
		candidate := filepath.Clean(filepath.Join(execDir, "..", "libs", "global.fer"))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("global prelude not found in packaged libs beside the executable")
}
