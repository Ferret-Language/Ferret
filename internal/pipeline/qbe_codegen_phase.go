package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"compiler/colors"
	qbe "compiler/internal/codegen/qbe_embeddings"
	"compiler/internal/mir"
)

// runQBECodegenPhase runs QBE-based code generation on all MIR-generated modules.
func (p *Pipeline) runQBECodegenPhase() error {
	if err := p.ensureEntryMain(); err != nil {
		return err
	}

	outputPath := p.ctx.Config.OutputPath
	outputDir := filepath.Dir(outputPath)

	tempDir := filepath.Join(outputDir, "gen")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	modulesToGenerate := p.collectModulesForCodegen()
	if p.ctx.Config.Debug {
		colors.CYAN.Printf("  Generating QBE for %d modules: %v\n", len(modulesToGenerate), modulesToGenerate)
	}

	entryModule, importedModules := splitEntryModule(modulesToGenerate, p.ctx.EntryModule)

	var asmFiles []string
	for _, importPath := range importedModules {
		if generated, err := p.generateModuleQBE(importPath, tempDir); err == nil {
			asmFiles = append(asmFiles, generated...)
		} else {
			return err
		}
	}

	if entryModule != "" {
		if generated, err := p.generateModuleQBE(entryModule, tempDir); err == nil {
			asmFiles = append(asmFiles, generated...)
		} else {
			return err
		}
	}

	execPath := p.ctx.Config.OutputPath
	if runtime.GOOS == "windows" && !strings.HasSuffix(execPath, ".exe") {
		defaultPattern := filepath.Join(p.ctx.Config.ProjectRoot, p.ctx.Config.ProjectName)
		if execPath == defaultPattern {
			execPath += ".exe"
		}
	}

	if err := p.buildExecutableMultiple(asmFiles, execPath); err != nil {
		p.ctx.ReportError(fmt.Sprintf("build failed: %v", err), nil)
		return err
	}

	if !p.ctx.Config.KeepGenFiles {
		if err := os.RemoveAll(tempDir); err != nil {
			if p.ctx.Config.Debug {
				colors.YELLOW.Printf("  ⚠ Could not remove temp directory: %v\n", err)
			}
		} else if p.ctx.Config.Debug {
			colors.GREEN.Printf("  ✓ Removed temp directory: %s\n", tempDir)
		}
	} else if p.ctx.Config.Debug {
		colors.CYAN.Printf("  ℹ Keeping generated files in: %s\n", tempDir)
	}

	if p.ctx.Config.Debug {
		colors.PURPLE.Printf("  ✓ QBE code generation complete\n")
	}

	return nil
}

func (p *Pipeline) generateModuleQBE(importPath, tempDir string) ([]string, error) {
	module, exists := p.ctx.GetModule(importPath)
	if !exists {
		return nil, fmt.Errorf("module not found: %s", importPath)
	}

	mirModule := mir.ModuleFromModule(module)
	if mirModule == nil {
		return nil, fmt.Errorf("MIR module not found for %s", importPath)
	}

	mir.LowerSwitches(mirModule)

	gen := qbe.New(p.ctx, module, mirModule)
	ssa, err := gen.Emit()
	if err != nil {
		return nil, err
	}

	baseName := p.sanitizeModuleName(importPath)
	ssaPath := filepath.Join(tempDir, baseName+".ssa")
	if err := os.WriteFile(ssaPath, []byte(ssa), 0644); err != nil {
		return nil, fmt.Errorf("failed to write QBE SSA for %s: %w", importPath, err)
	}

	asmPath := filepath.Join(tempDir, baseName+".s")
	if err := qbe.Run(p.ctx, ssaPath, asmPath); err != nil {
		p.ctx.ReportError(err.Error(), nil)
		return nil, err
	}

	if p.ctx.Config.Debug {
		colors.GREEN.Printf("  ✓ Generated: %s, %s\n", filepath.Base(ssaPath), filepath.Base(asmPath))
	}

	return []string{asmPath}, nil
}
