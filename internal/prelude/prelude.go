package prelude

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"compiler/internal/context"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
	"compiler/internal/phase"
	"compiler/internal/semantics/collector"
	"compiler/internal/semantics/resolver"
	"compiler/internal/semantics/typechecker"
)

const globalModuleKey = "builtin:global"

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
	candidates := make([]string, 0, 8)
	if ctx.Config.StdlibRoot != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(ctx.Config.StdlibRoot), "global.ferr"))
	}
	for current := filepath.Clean(ctx.Config.RootDir); ; current = filepath.Dir(current) {
		candidates = append(candidates,
			filepath.Join(current, "ferret_libs_dev", "global.ferr"),
			filepath.Join(current, "libs", "global.ferr"),
		)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidates = append(candidates,
			filepath.Join(execDir, "..", "ferret_libs_dev", "global.ferr"),
			filepath.Join(execDir, "..", "libs", "global.ferr"),
		)
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
		candidates = append(candidates,
			filepath.Join(root, "ferret_libs_dev", "global.ferr"),
			filepath.Join(root, "libs", "global.ferr"),
		)
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		clean := filepath.Clean(candidate)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		info, err := os.Stat(clean)
		if err == nil && !info.IsDir() {
			return clean, nil
		}
	}
	return "", fmt.Errorf("global prelude not found in ferret_libs_dev or libs")
}
