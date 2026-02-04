//go:build js && wasm

package compiler

import (
	"io/fs"
	"path"
	"strings"

	ferretlibs "compiler/ferret_libs"
	"compiler/internal/context_v2"
	"compiler/internal/phase"
	"compiler/internal/semantics/table"
)

var embeddedStdlib = ferretlibs.FS

func loadEmbeddedBuiltins(ctx *context_v2.CompilerContext) {
	if ctx == nil {
		return
	}

	_ = fs.WalkDir(embeddedStdlib, ".", func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(filePath, ctx.Config.Extension) {
			return nil
		}
		content, readErr := embeddedStdlib.ReadFile(filePath)
		if readErr != nil {
			return nil
		}

		importPath := embeddedImportPath(filePath, ctx.Config.Extension)
		if importPath == "" {
			return nil
		}

		fileID := "embed://" + importPath + ctx.Config.Extension
		mod, exists := ctx.GetModule(importPath)
		if !exists || mod == nil {
			scope := table.NewSymbolTable(ctx.Universe)
			if importPath == context_v2.GlobalModuleImport {
				scope = ctx.Universe
			}
			mod = &context_v2.Module{
				ImportPath:   importPath,
				Type:         context_v2.ModuleBuiltin,
				Phase:        phase.PhaseNotStarted,
				ModuleScope:  scope,
				CurrentScope: scope,
				Artifacts:    make(map[string]any),
			}
			ctx.AddModule(importPath, mod)
		}

		mod.FilePath = fileID
		mod.Type = context_v2.ModuleBuiltin
		mod.Content = string(content)
		return nil
	})
}

func embeddedImportPath(embedPath, ext string) string {
	cleaned := path.Clean(strings.TrimPrefix(embedPath, "./"))
	cleaned = strings.TrimPrefix(cleaned, "ferret_libs/")
	cleaned = strings.TrimSuffix(cleaned, ext)
	return path.Clean(cleaned)
}
