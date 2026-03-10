package ownership_test

import (
	"os"
	"path/filepath"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	"compiler/internal/phase"
)

func TestOwnershipPhaseReportsUseAfterMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1 }
    let q = p
    return p.X
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.HIR == nil {
		t.Fatal("expected lowered HIR")
	}
	if result.Entry.LoweredHIR == nil {
		t.Fatal("expected normalized lowered HIR")
	}
	if result.Entry.CFG == nil {
		t.Fatal("expected CFG")
	}
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected ownership diagnostic")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseConsumesOwningReceiver(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn (c own *Conn) Close() void {
}

fn run(c own *Conn) void {
    c.Close()
    c.Close()
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseReportsUseAfterMoveThroughLoop(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1 }
    while true {
        let q = p
        break
    }
    return p.X
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseAllowsLoopReinitialization(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
}

fn main() i32 {
    let mut p: Point = .{ .X = 1 }
    let mut i: i32 = 0
    while i < 2 {
        let q = p
        p = .{ .X = i }
        i = i + 1
    }
    return p.X
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove || diag.Code == diagnostics.ErrBorrowConflict {
			t.Fatalf("unexpected ownership diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseFreezesBorrowedOwnerWithinBlock(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn useConn(c own *Conn) void {
}

fn run(c own *Conn) void {
    if 1 == 1 {
        let p = &*c
        useConn(c)
        p
    }
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowConflict {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowConflict, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseRejectsReturnedBorrow(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn borrow(c own *Conn) *Conn {
    return &*c
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowEscape {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowEscape, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseRejectsReturnedBorrowBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn borrow(c own *Conn) *Conn {
    let p = &*c
    return p
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowEscape {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowEscape, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseRejectsWholeValueUseAfterFieldMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Node struct {
    Child own *Node
    Value i32 = 0
}

fn main(n Node) i32 {
    let child = n.Child
    let copy = n
    return copy.Value
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseAllowsOtherFieldAfterFieldMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Node struct {
    Child own *Node
    Value i32 = 0
}

fn main(n Node) i32 {
    let child = n.Child
    return n.Value
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseAllowsFieldReinitializationAfterFieldMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Node struct {
    Child own *Node
    Value i32 = 0
}

fn main(n Node, replacement own *Node) i32 {
    let mut current = n
    let child = current.Child
    current.Child = replacement
    let again = current
    return again.Value
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func mustWriteOwnership(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
