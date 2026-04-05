package diagnostics

import (
	"bytes"
	"compiler/colors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	compileFailedMsg          = "\nCompilation failed with %d error(s)"
	andWarningMsg             = " and %d warning(s)"
	compileSuccessWithWarning = "\nCompilation succeeded with %d warning(s)\n"
)

// DiagnosticBag collects diagnostics during compilation
type DiagnosticBag struct {
	diagnostics []*Diagnostic
	mu          sync.Mutex
	errorCount  int
	warnCount   int
	sourceCache *SourceCache
}

// NewDiagnosticBag creates a new diagnostic bag for a file
func NewDiagnosticBag(filepath string) *DiagnosticBag {
	return &DiagnosticBag{
		diagnostics: make([]*Diagnostic, 0),
		sourceCache: NewSourceCache(),
	}
}

// AddSourceContent adds source content for a file path (for in-memory compilation)
func (db *DiagnosticBag) AddSourceContent(filepath, content string) {
	db.sourceCache.AddSource(filepath, content)
}

// GetSourceCache returns the source cache for accessing source content
func (db *DiagnosticBag) GetSourceCache() *SourceCache {
	return db.sourceCache
}

// Add adds a diagnostic to the bag
func (db *DiagnosticBag) Add(diag *Diagnostic) {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.diagnostics = append(db.diagnostics, diag)

	switch diag.Severity {
	case Error:
		db.errorCount++
	case Warning:
		db.warnCount++
	}
}

// HasErrors returns true if there are any errors
func (db *DiagnosticBag) HasErrors() bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.errorCount > 0
}

// ErrorCount returns the number of errors
func (db *DiagnosticBag) ErrorCount() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.errorCount
}

// WarningCount returns the number of warnings
func (db *DiagnosticBag) WarningCount() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.warnCount
}

// Diagnostics returns a copy of all diagnostics (thread-safe)
func (db *DiagnosticBag) Diagnostics() []*Diagnostic {
	db.mu.Lock()
	defer db.mu.Unlock()
	// Return a copy to prevent races if caller iterates while other goroutines append
	result := make([]*Diagnostic, len(db.diagnostics))
	copy(result, db.diagnostics)
	return result
}

// sortDiagnostics sorts diagnostics by primary label location (file, line, column)
func sortDiagnostics(diagnostics []*Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		if len(diagnostics[i].Labels) == 0 || len(diagnostics[j].Labels) == 0 {
			return false // Keep original order if no labels
		}

		iLoc := diagnostics[i].Labels[0].Location
		jLoc := diagnostics[j].Labels[0].Location

		// Handle nil locations
		if iLoc == nil {
			return false
		}
		if jLoc == nil {
			return true
		}

		// Compare filenames
		iFile := ""
		jFile := ""
		if iLoc.Filename != nil {
			iFile = *iLoc.Filename
		}
		if jLoc.Filename != nil {
			jFile = *jLoc.Filename
		}

		if iFile != jFile {
			return iFile < jFile
		}

		// Same file, compare by line
		if iLoc.Start == nil || jLoc.Start == nil {
			return false
		}
		if iLoc.Start.Line != jLoc.Start.Line {
			return iLoc.Start.Line < jLoc.Start.Line
		}

		// Same line, keep original order (stable sort maintains phase order)
		return false
	})
}

func (db *DiagnosticBag) EmitAll() {
	emitter := NewEmitter(os.Stderr)
	db.emitFiltered(emitter, os.Stderr, func(*Diagnostic) bool { return true })
}

// EmitErrors prints only error diagnostics and an error-only summary.
func (db *DiagnosticBag) EmitErrors() {
	emitter := NewEmitter(os.Stderr)
	db.emitFiltered(emitter, os.Stderr, func(diag *Diagnostic) bool {
		return diag != nil && diag.Severity == Error
	})
}

func (db *DiagnosticBag) emitFiltered(emitter *Emitter, w io.Writer, keep func(*Diagnostic) bool) {
	db.mu.Lock()

	diagnostics := make([]*Diagnostic, len(db.diagnostics))
	copy(diagnostics, db.diagnostics)
	db.mu.Unlock()

	filtered := diagnostics[:0]
	var errors, warnings int
	for _, diag := range diagnostics {
		if keep != nil && !keep(diag) {
			continue
		}
		filtered = append(filtered, diag)
		switch diag.Severity {
		case Error:
			errors++
		case Warning:
			warnings++
		}
	}

	// Sort diagnostics by source location
	sortDiagnostics(filtered)

	for _, diag := range filtered {
		emitter.Emit(diag)
	}

	printSummary(w, errors, warnings)
}

// EmitAllToString emits all diagnostics to a string with ANSI codes, using provided source cache
func (db *DiagnosticBag) EmitAllToString() string {
	return db.emitAllToStringWithFormat(colors.LogFormatANSI)
}

// EmitAllToHTML emits all diagnostics to an HTML string, using provided source cache
func (db *DiagnosticBag) EmitAllToHTML() string {
	return db.emitAllToStringWithFormat(colors.LogFormatHTML)
}

func (db *DiagnosticBag) emitAllToStringWithFormat(format colors.LogFormat) string {
	prevFormat := colors.CurrentLogFormat()
	colors.SetLogFormat(format)
	defer colors.SetLogFormat(prevFormat)

	var buf bytes.Buffer
	emitter := &Emitter{
		cache:       db.sourceCache,
		writer:      &buf,
		highlighter: NewSyntaxHighlighter(true),
	}

	db.emitFiltered(emitter, &buf, func(*Diagnostic) bool { return true })

	return buf.String()
}

func printSummary(w io.Writer, errorCount, warnCount int) {
	if errorCount > 0 {
		colors.RED.Fprintf(w, compileFailedMsg, errorCount)
		if warnCount > 0 {
			colors.RED.Fprintf(w, andWarningMsg, warnCount)
		}
		fmt.Fprintln(w)
	} else if warnCount > 0 {
		colors.ORANGE.Fprintf(w, compileSuccessWithWarning, warnCount)
	}
}

// Clear removes all diagnostics
func (db *DiagnosticBag) Clear() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.diagnostics = make([]*Diagnostic, 0)
	db.errorCount = 0
	db.warnCount = 0
}

// ClearForFiles removes diagnostics whose primary file matches the provided set.
func (db *DiagnosticBag) ClearForFiles(files map[string]struct{}) {
	if len(files) == 0 {
		return
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	kept := db.diagnostics[:0]
	var errors, warnings int
	for _, diag := range db.diagnostics {
		filePath := diagnosticFilePath(diag)
		if filePath != "" {
			filePath = filepath.ToSlash(filePath)
		}
		if filePath != "" {
			if _, ok := files[filePath]; ok {
				continue
			}
		}
		kept = append(kept, diag)
		switch diag.Severity {
		case Error:
			errors++
		case Warning:
			warnings++
		}
	}

	db.diagnostics = kept
	db.errorCount = errors
	db.warnCount = warnings
}

func diagnosticFilePath(diag *Diagnostic) string {
	if diag == nil {
		return ""
	}
	if diag.FilePath != "" {
		return diag.FilePath
	}
	for _, label := range diag.Labels {
		if label.Style == Primary && label.Location != nil && label.Location.Filename != nil {
			return *label.Location.Filename
		}
	}
	if len(diag.Labels) > 0 {
		loc := diag.Labels[0].Location
		if loc != nil && loc.Filename != nil {
			return *loc.Filename
		}
	}
	return ""
}
