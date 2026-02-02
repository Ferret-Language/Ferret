package pipeline

import (
	"fmt"
	"os"
	"strings"

	"compiler/colors"
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
	"compiler/internal/phase"
	"compiler/internal/semantics/table"
	"compiler/internal/source"
	"compiler/internal/utils/fs"
)

// importInfo holds import path and its source location
type importInfo struct {
	path     string
	location *source.Location
}

// processModule schedules parsing for a module exactly once (thread-safe)
func (p *Pipeline) processModule(importPath string, requestedLocation *source.Location) {
	if _, loaded := p.seen.LoadOrStore(importPath, struct{}{}); loaded {
		return
	}

	p.wg.Add(1)

	go func() {
		defer p.wg.Done()
		p.parseModule(importPath, requestedLocation)
	}()
}

// parseModule does the actual parsing work and schedules imports
func (p *Pipeline) parseModule(importPath string, requestedLocation *source.Location) {
	module, exists := p.ctx.GetModule(importPath)
	if !exists {
		modScope := table.NewSymbolTable(p.ctx.Universe)
		module = &context_v2.Module{
			FilePath:     "",
			ImportPath:   importPath,
			Type:         context_v2.ModuleLocal,
			Phase:        phase.PhaseNotStarted,
			ModuleScope:  modScope,
			CurrentScope: modScope,
			Content:      "",
			AST:          nil,
			Artifacts:    make(map[string]any),
		}
		p.ctx.AddModule(importPath, module)
	}

	var content string
	var filePath string

	// For in-memory modules (WASM/playground), use the Content field even if empty
	// Check if FilePath suggests this is an in-memory module
	if module.FilePath != "" && (strings.HasPrefix(module.FilePath, "/playground") || module.Content != "") {
		content = module.Content
		filePath = module.FilePath
	} else {
		var err error
		var modType context_v2.ModuleType

		filePath, modType, err = p.ctx.ImportPathToFilePath(importPath)
		if err != nil {
			p.ctx.Diagnostics.Add(
				diagnostics.NewError(err.Error()).
					WithPrimaryLabel(requestedLocation, ""),
			)
			return
		}

		contentBytes, err := os.ReadFile(filePath)
		if err != nil {
			errMsg := fmt.Sprintf("cannot read file %s: %v", filePath, err)
			p.ctx.Diagnostics.Add(
				diagnostics.NewError(errMsg).
					WithPrimaryLabel(requestedLocation, ""),
			)
			return
		}

		content = string(contentBytes)

		module.Mu.Lock()
		module.FilePath = filePath
		module.Type = modType
		module.Content = content
		module.Mu.Unlock()
	}

	p.ctx.Diagnostics.AddSourceContent(filePath, content)

	tokenizer := lexer.New(filePath, content, p.ctx.Diagnostics)
	tokens := tokenizer.Tokenize(false)

	if !p.ctx.AdvanceModulePhase(importPath, phase.PhaseLexed) {
		p.ctx.ReportError(fmt.Sprintf("cannot advance module %s to PhaseLexed", importPath), nil)
		return
	}

	if !p.ctx.HasModule(importPath) {
		p.ctx.AddModule(importPath, module)
	}

	astModule := parser.Parse(tokens, filePath, p.ctx.Diagnostics)

	module.Mu.Lock()
	module.AST = astModule
	module.Mu.Unlock()

	if astModule != nil && p.ctx.Config.SaveAST {
		fmt.Printf("Saving AST %s\n", astModule.FullPath)
		astModule.SaveAST()
	}

	if !p.ctx.AdvanceModulePhase(importPath, phase.PhaseParsed) {
		p.ctx.ReportError(fmt.Sprintf("cannot advance module %s to PhaseParsed", importPath), nil)
		return
	}

	if p.ctx.Config.Debug {
		colors.PURPLE.Printf("  ✓ %s\n", importPath)
	}

	if importPath != context_v2.GlobalModuleImport && p.ctx.HasModule(context_v2.GlobalModuleImport) {
		if err := p.ctx.AddDependency(importPath, context_v2.GlobalModuleImport); err != nil {
			p.ctx.ReportError(err.Error(), requestedLocation)
		}
	}

	var imports []importInfo
	if astModule != nil {
		imports = p.extractTopLevelImports(astModule)
	}

	for _, imp := range imports {
		if err := p.ctx.AddDependency(importPath, imp.path); err != nil {
			p.ctx.ReportError(err.Error(), imp.location)
			continue
		}
	}

	for _, imp := range imports {
		p.processModule(imp.path, imp.location)
	}
}

// extractTopLevelImports extracts only top-level imports (stops at first non-import)
func (p *Pipeline) extractTopLevelImports(astModule *ast.Module) []importInfo {
	var imports []importInfo

	if astModule == nil {
		return imports
	}

	if astModule.Nodes == nil {
		return imports
	}

	for _, node := range astModule.Nodes {
		if node == nil {
			continue
		}

		impStmt, ok := node.(*ast.ImportStmt)
		if !ok {
			break
		}

		if impStmt == nil {
			p.ctx.ReportError("invalid import statement: nil node", nil)
			continue
		}

		if impStmt.Path == nil {
			p.ctx.ReportError("import statement missing path", &impStmt.Location)
			continue
		}

		if impStmt.Path.Value == "" {
			p.ctx.ReportError("import statement has empty path", &impStmt.Location)
			continue
		}

		importPath := strings.Trim(impStmt.Path.Value, "\"")
		importPath = fs.NormalizePath(importPath)
		if importPath == "" {
			p.ctx.ReportError("import path is empty after normalization", &impStmt.Location)
			continue
		}

		imports = append(imports, importInfo{
			path:     importPath,
			location: &impStmt.Location,
		})
	}

	return imports
}
