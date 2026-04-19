package usage_test

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
	"compiler/internal/testutils"
)

func TestUsageWarnsUnusedImport(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `import "std/io"

fn main() -> i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected usage-analyzed entry phase, got %#v", result.Entry)
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedImport && diag.Message == `unused import "std/io"` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unused import warning, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestUsageWarnsUnusedPrivateTopLevelSymbols(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `type point struct {
}

let hiddenValue: i32 = 1
let deadValue: i32 = 2

fn helper() -> i32 {
    return hiddenValue
}

fn main() -> i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	codes := map[string]bool{}
	for _, diag := range result.Diagnostics.Diagnostics() {
		codes[diag.Code] = true
	}
	if !codes[diagnostics.WarnUnusedPrivateType] {
		t.Fatalf("expected unused private type warning, got %#v", result.Diagnostics.Diagnostics())
	}
	if !codes[diagnostics.WarnUnusedPrivateFunction] {
		t.Fatalf("expected unused private function warning, got %#v", result.Diagnostics.Diagnostics())
	}
	if !codes[diagnostics.WarnUnusedPrivateBinding] {
		t.Fatalf("expected unused private binding warning, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestUsageAllowUnusedSuppressesUnusedFunctionWarning(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `#[allow_unused]
fn helper() -> i32 {
    return 42
}

fn main() -> i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedPrivateFunction && diag.Message == `unused private function "helper"` {
			t.Fatalf("did not expect unused warning for #[allow_unused] function, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageDoesNotWarnUsedOrExportedSymbols(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `
type Point struct {
}

let hiddenValue: i32 = 1

fn helper() -> i32 {
    return hiddenValue
}

fn main() -> i32 {
    println("ok")
    return helper()
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", testutils.GetFirstError(result.Diagnostics))
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		switch diag.Code {
		case diagnostics.WarnUnusedImport, diagnostics.WarnUnusedPrivateFunction, diagnostics.WarnUnusedPrivateType, diagnostics.WarnUnusedPrivateBinding:
			t.Fatalf("did not expect usage warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageDoesNotWarnOnPreludeBuiltinWrapper(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `fn main() -> void {
    println("ok")
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedPrivateFunction && diag.Message == `unused private function "println"` {
			t.Fatalf("did not expect prelude wrapper unused warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageWarnsUnusedLocalsAndParametersInUsedFunction(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `fn helper(x: i32, y: i32, z: i32) -> i32 {
    let alive = x
    let dead = y
    return alive
}

fn main() -> i32 {
    return helper(1, 2, 3)
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	foundParam := false
	foundLocal := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		switch diag.Code {
		case diagnostics.WarnUnusedParameter:
			if diag.Message == `unused parameter "z"` {
				foundParam = true
			}
		case diagnostics.WarnUnusedLocal:
			if diag.Message == `unused local "dead"` {
				foundLocal = true
			}
		}
	}
	if !foundParam {
		t.Fatalf("expected unused parameter warning, got %#v", result.Diagnostics.Diagnostics())
	}
	if !foundLocal {
		t.Fatalf("expected unused local warning, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestUsageSkipsLocalWarningsInUnusedFunction(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `fn helper(x: i32, y: i32) -> i32 {
    let dead = y
    return x
}

fn main() -> i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	foundUnusedFunction := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		switch diag.Code {
		case diagnostics.WarnUnusedPrivateFunction:
			if diag.Message == `unused private function "helper"` {
				foundUnusedFunction = true
			}
		case diagnostics.WarnUnusedParameter, diagnostics.WarnUnusedLocal:
			t.Fatalf("did not expect local warnings for unused function, got %#v", result.Diagnostics.Diagnostics())
		}
	}
	if !foundUnusedFunction {
		t.Fatalf("expected unused function warning, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestUsageDoesNotCountAssignmentTargetAsUse(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `fn main() -> i32 {
    let mut a = 10
    a = 12
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedLocal && diag.Message == `unused local "a"` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unused local warning for write-only assignment, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestUsageTreatsDiscardAssignmentAsUse(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `fn main() -> i32 {
    let a = 10
    _ = a
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedLocal && diag.Message == `unused local "a"` {
			t.Fatalf("did not expect unused warning after discard assignment, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageWarnsWhenMutableBindingIsNeverModified(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `fn main() -> i32 {
    let mut a = 10
    _ = a
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnmodifiedMutable && diag.Message == `"a" is never modified` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected never-modified mutable warning, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestUsageDoesNotWarnWhenMutableBindingIsAssigned(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `fn main() -> i32 {
    let mut a = 10
    a = 12
    _ = a
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnmodifiedMutable && diag.Message == `"a" is never modified` {
			t.Fatalf("did not expect never-modified mutable warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageDoesNotWarnWhenMutableBindingIsUsedByMutableMethodCall(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Incr(&mut self) -> void {
    self.X++
}

fn main() -> void {
    let mut p: Point = .{ .X = 1 }
    p.Incr()
    _ = p
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnmodifiedMutable && diag.Message == `"p" is never modified` {
			t.Fatalf("did not expect never-modified mutable warning, got %#v", result.Diagnostics.Diagnostics())
		}
		if diag.Code == diagnostics.WarnUnusedLocal && diag.Message == `unused local "p"` {
			t.Fatalf("did not expect unused local warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageDoesNotWarnWhenMutableBindingIsPassedToMutParam(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `
fn bump(mut value: i32) -> void {
    value = value + 1
}

fn main() -> void {
    let mut a = 10
    bump(a)
    _ = a
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnmodifiedMutable && diag.Message == `"a" is never modified` {
			t.Fatalf("did not expect never-modified mutable warning, got %#v", result.Diagnostics.Diagnostics())
		}
		if diag.Code == diagnostics.WarnUnusedLocal && diag.Message == `unused local "a"` {
			t.Fatalf("did not expect unused local warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageDoesNotWarnWhenMutableBindingIsModifiedThroughMutRef(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `fn main() -> void {
    let mut x = 10;
    let y = &mut x;
    *y = 12;
    _ = x
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnmodifiedMutable && diag.Message == `"x" is never modified` {
			t.Fatalf("did not expect never-modified mutable warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageDoesNotWarnWhenMutableBindingIsModifiedThroughLambdaCapture(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `fn main() -> void {
    let mut x = 1
    let apply = () => { x = 2 }
    apply()
    _ = x
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnmodifiedMutable && diag.Message == `"x" is never modified` {
			t.Fatalf("did not expect never-modified mutable warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageDoesNotWarnOnMutableSliceElementMutation(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `
fn fill(mut items: []i32) -> void {
    items[0] = 9
}

fn main() -> void {
    let mut items: [3]i32 = [3]i32{1, 2, 3}
    fill(items)
    _ = items
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedParameter && diag.Message == `unused parameter "items"` {
			t.Fatalf("did not expect unused parameter warning, got %#v", result.Diagnostics.Diagnostics())
		}
		if diag.Code == diagnostics.WarnUnmodifiedMutable && diag.Message == `"items" is never modified` {
			t.Fatalf("did not expect never-modified mutable warning, got %#v", result.Diagnostics.Diagnostics())
		}
		if diag.Code == diagnostics.WarnUnusedLocal && diag.Message == `unused local "items"` {
			t.Fatalf("did not expect unused local warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageDoesNotWarnOnDirectMutableSliceElementAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `
fn main(mut items: []i32) -> void {
    items[0] = 9
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedParameter && diag.Message == `unused parameter "items"` {
			t.Fatalf("did not expect unused parameter warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func TestUsageDoesNotWarnOnUnusedSelfReceiver(t *testing.T) {
	root := t.TempDir()
	mustWriteUsage(t, filepath.Join(root, "main.fer"), `
type Point struct {}

fn Point::Show(&self) -> void {}

fn main() -> void {
    let p: Point = .{}
    p.Show()
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedParameter && diag.Message == `unused parameter "self"` {
			t.Fatalf("did not expect unused self receiver warning, got %#v", result.Diagnostics.Diagnostics())
		}
	}
}

func mustWriteUsage(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
