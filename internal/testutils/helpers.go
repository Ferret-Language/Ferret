package testutils

import "compiler/internal/core/diagnostics"

func GetFirstError(diag *diagnostics.DiagnosticBag) (msg string) {
	var firstErr *diagnostics.Diagnostic
	if diag.ErrorCount() > 0 {
		firstErr = diag.Diagnostics()[0]
		msg = firstErr.Message
		if len(firstErr.Labels) > 0 {
			msg += firstErr.Labels[0].Message
		}
	}
	return msg
}