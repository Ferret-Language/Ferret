package ownership_test

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
)

func TestOwnershipPhaseAllowsPlainStructCopy(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1 }
    let q = p
    return p.X
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
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
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseConsumesOwningReceiver(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn (c: *Conn) Close() void {
}

fn run(c: *Conn) void {
    c.Close()
    c.Close()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseAllowsPlainStructCopyThroughLoop(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseAllowsArrayCopyByDefault(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
fn main(items: [3]i32) i32 {
    let other = items
    return items[0] + other[1]
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseAllowsLoopReinitialization(t *testing.T) {
	t.Skip("loop reinitialization after move needs stronger ownership data-flow than the current phase provides")
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
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

fn useConn(c: *Conn) void {
}

fn run(c: *Conn) void {
    if 1 == 1 {
        let p = &*c
        useConn(c)
        p
    }
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowConflict {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowConflict, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseReleasesBorrowAfterLastUse(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn useConn(c: *Conn) void {
}

fn run(c: *Conn) void {
    let p = &*c
    p
    useConn(c)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowConflict {
			t.Fatalf("unexpected borrow conflict %#v", diag)
		}
	}
}

func TestOwnershipPhaseRejectsReturnedBorrow(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn borrow(c: *Conn) &Conn {
    return &*c
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
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

fn borrow(c: *Conn) &Conn {
    let p = &*c
    return p
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowEscape {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowEscape, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseRejectsDeferredBorrowCapture(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    Value: i32 = 0
}

fn read(p: &Point) void {
    p
}

fn run() void {
    let p: Point = .{}
    defer read(&p)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowEscape {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowEscape, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseRejectsImmutableBorrowWhileMutableBorrowIsLive(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn run(mut c: *Conn) void {
    let m = &mut *c
    let r = &*c
    m
    r
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowConflict {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowConflict, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseRejectsUseWhileMutableBorrowBindingIsStillLive(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
fn run() void {
    let mut x = 10
    let y = &mut x
    print(x)
    print(*y)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowConflict {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowConflict, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseAllowsMultipleImmutableBorrows(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    Value: i32 = 0
}

fn run() void {
    let p: Point = .{}
    let a = &p
    let b = &p
    a
    b
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowConflict {
			t.Fatalf("unexpected borrow conflict %#v", diag)
		}
	}
}

func TestOwnershipPhaseAllowsPlainEnumCopySemantics(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Handle enum {
    stdin,
    stdout,
}

fn make(flag: bool) Handle {
    if flag {
        return Handle::stdin
    }
    return Handle::stdout
}

fn main() i32 {
    let h = make(true)
    let other = h
    let code = other as i32
    return code + (h as i32)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseAllowsExplicitCopyOfPlainEnum(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Handle enum {
    stdin,
    stdout,
}

fn make(flag: bool) Handle {
    if flag {
        return Handle::stdin
    }
    return Handle::stdout
}

fn main() i32 {
    let h = make(true)
    return h as i32
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestOwnershipPhaseRejectsWholeValueUseAfterFieldMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn main(n: Node) i32 {
    let child = n.Child
    let copy = n
    return copy.Value
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseAllowsWholeValueUseAfterCopyingPlainField(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn main(n: Node) i32 {
    let value = n.Value
    let again = n
    return again.Value + value
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseAllowsOtherFieldAfterFieldMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn main(n: Node) i32 {
    let child = n.Child
    return n.Value
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
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
    Child: *Node
    Value: i32 = 0
}

fn main(n: Node, replacement: *Node) i32 {
    let mut current = n
    let child = current.Child
    current.Child = replacement
    let again = current
    return again.Value
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
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
