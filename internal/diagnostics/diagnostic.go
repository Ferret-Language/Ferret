package diagnostics

import (
	"compiler/colors"
	"compiler/internal/source"
)

// Severity represents the severity level of a diagnostic
type Severity int

const (
	Error Severity = iota
	Warning
	Info
	Hint
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Info:
		return "info"
	case Hint:
		return "hint"
	default:
		return "unknown"
	}
}

// Label represents a labeled section of code in a diagnostic
type Label struct {
	Location *source.Location
	Message  string
	Style    LabelStyle
}

type LabelStyle int

const (
	Primary   LabelStyle = iota // The main error location (uses ~~~)
	Secondary                   // Additional context (uses ---)
)

// Note represents additional information attached to a diagnostic
type Note struct {
	Message string
}

// Diagnostic represents a compiler diagnostic (error, warning, etc.)
type Diagnostic struct {
	Severity Severity
	Message  string
	Code     string // Error code like "E0001"
	FilePath string // Source file for this diagnostic
	Labels   []Label
	Notes    []Note
	Help     string // Suggestion for fixing the error
	CodeHint *CodeHint
}

// CodeHint renders extra lines after the primary label.
type CodeHint struct {
	Code        string
	Labels      []CodeHintLabel
	BaseColor   colors.COLOR
	GutterColor colors.COLOR
}

// CodeHintLabel represents a label within a code hint snippet.
type CodeHintLabel struct {
	Line    int
	Column  int
	Length  int
	Message string
	Style   LabelStyle
}

// NewError creates a new error diagnostic
func NewError(message string) *Diagnostic {
	return &Diagnostic{
		Severity: Error,
		Message:  message,
		Labels:   make([]Label, 0),
		Notes:    make([]Note, 0),
	}
}

// NewWarning creates a new warning diagnostic
func NewWarning(message string) *Diagnostic {
	return &Diagnostic{
		Severity: Warning,
		Message:  message,
		Labels:   make([]Label, 0),
		Notes:    make([]Note, 0),
	}
}

// NewInfo creates a new info diagnostic
func NewInfo(message string) *Diagnostic {
	return &Diagnostic{
		Severity: Info,
		Message:  message,
		Labels:   make([]Label, 0),
		Notes:    make([]Note, 0),
	}
}

// WithCode sets the error code
func (d *Diagnostic) WithCode(code string) *Diagnostic {
	d.Code = code
	return d
}

// WithLabel adds a labeled location to the diagnostic
func (d *Diagnostic) WithLabel(loc *source.Location, message string, style LabelStyle) *Diagnostic {
	if d.FilePath == "" {
		d.FilePath = *loc.Filename
	}
	d.Labels = append(d.Labels, Label{
		Location: loc,
		Message:  message,
		Style:    style,
	})
	return d
}

// WithPrimaryLabel adds a primary labeled location
// Must be called before any WithSecondaryLabel calls
func (d *Diagnostic) WithPrimaryLabel(loc *source.Location, message string) *Diagnostic {
	// Ensure primary label is always first
	if len(d.Labels) > 0 {
		// Check if we already have a primary
		for _, label := range d.Labels {
			if label.Style == Primary {
				// Already have a primary, don't add another
				return d
			}
		}
		// We have secondary labels but no primary - insert at beginning
		d.Labels = append([]Label{{
			Location: loc,
			Message:  message,
			Style:    Primary,
		}}, d.Labels...)
		if d.FilePath == "" {
			d.FilePath = *loc.Filename
		}
		return d
	}
	return d.WithLabel(loc, message, Primary)
}

// WithSecondaryLabel adds a secondary labeled location
// Can be called multiple times to add multiple context labels
// Primary label must exist before adding secondary labels
func (d *Diagnostic) WithSecondaryLabel(loc *source.Location, message string) *Diagnostic {
	// Verify we have a primary label
	hasPrimary := false
	for _, label := range d.Labels {
		if label.Style == Primary {
			hasPrimary = true
			break
		}
	}

	if !hasPrimary {
		// No primary label exists - cannot add secondary
		// This is a programming error, so we should make it visible
		panic("Cannot add secondary label without primary label. Call WithPrimaryLabel first.")
	}

	return d.WithLabel(loc, message, Secondary)
}

// WithCodeHint adds a primary label and attaches a code hint to display.
func (d *Diagnostic) WithCodeHint(loc *source.Location, code string, labels ...CodeHintLabel) *Diagnostic {
	if loc == nil {
		return d
	}

	d.WithPrimaryLabel(loc, "")
	d.CodeHint = &CodeHint{
		Code:        code,
		Labels:      labels,
		GutterColor: colors.GREEN,
	}
	return d
}
// WithNote adds a note to the diagnostic
func (d *Diagnostic) WithNote(message string) *Diagnostic {
	d.Notes = append(d.Notes, Note{Message: message})
	return d
}

// WithHelp sets helpful suggestion for fixing the error
func (d *Diagnostic) WithHelp(help string) *Diagnostic {
	d.Help = help
	return d
}
