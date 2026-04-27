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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> i32 {
    let p: Point = .{ .X = 1 }
    let q = p
    return p.X
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn Conn::Close(*self) -> void {
}

fn run(c: *Conn) -> void {
    c.Close()
    c.Close()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> i32 {
    let p: Point = .{ .X = 1 }
    while true {
        let q = p
        break
    }
    return p.X
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
fn main(items: [3]i32) -> i32 {
    let other = items
    return items[0] + other[1]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseAllowsOwningParamRebindAfterMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn main(mut p: *Conn, mut q: *Conn) -> *Conn {
    let tmp = p
    p = q
    return p
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove || diag.Code == diagnostics.ErrBorrowConflict {
			t.Fatalf("unexpected ownership diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseAllowsOwningParamLoopRebindAfterMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn main(mut p: *Conn, mut q: *Conn) -> *Conn {
    let mut i: i32 = 0
    while i < 1 {
        let tmp = p
        p = q
        i = i + 1
    }
    return p
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove || diag.Code == diagnostics.ErrBorrowConflict {
			t.Fatalf("unexpected ownership diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseAllowsLoopReinitialization(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> i32 {
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

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn useConn(c: *Conn) -> void {
}

fn run(c: *Conn) -> void {
    if 1 == 1 {
        let p = &*c
        useConn(c)
        p
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn useConn(c: *Conn) -> void {
}

fn run(c: *Conn) -> void {
    let p = &*c
    p
    useConn(c)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn borrow(c: *Conn) -> &Conn {
    return &*c
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn borrow(c: *Conn) -> &Conn {
    let p = &*c
    return p
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Point struct {
    Value: i32 = 0
}

fn read(p: &Point) -> void {
    p
}

fn run() -> void {
    let p: Point = .{}
    defer read(&p)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestOwnershipPhaseAllowsDeferredConsumingCloseAfterInterfaceWrite(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "io.fer"), `
type Writer interface {
    Write(&self, text: str) -> usize
}

fn Write(dst: Writer, text: str) -> usize {
    return dst.Write(text)
}
`)
	mustWriteOwnership(t, filepath.Join(root, "mem.fer"), `
#[extern]
fn Expose<T>(owner: *T) -> ^T;

#[extern]
fn ExposeRef<T>(owner: &*T) -> ^T;

#[extern]
fn Adopt<T>(raw: ^T) -> *T;
`)
	mustWriteOwnership(t, filepath.Join(root, "fs.fer"), `
import "mem"

type fileInner struct {
    handle: ^void
}

type File struct {
    inner: *fileInner
}

#[extern("ferret_std_fs_open")]
fn open_raw(path: &str) -> ^void;

#[extern("ferret_std_fs_write")]
fn write_raw(handle: ^void, text: &str) -> usize;

#[extern("ferret_std_fs_close")]
fn close_raw(handle: ^void) -> void;

fn Open(path: str) -> File {
    unsafe {
        return .{
            .inner = mem::Adopt(open_raw(&path) as ^fileInner)
        }
    }
}

fn File::Write(&self, text: str) -> usize {
    unsafe {
        return write_raw(mem::ExposeRef(&self.inner) as ^void, &text)
    }
}

fn File::Close(self) -> void {
    unsafe {
        close_raw(mem::Expose(self.inner) as ^void)
    }
}
`)
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
import "io"
import "fs"

fn main() -> void {
    let file = fs::Open("out.txt")
    io::Write(file, "Hello")
    defer file.Close()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove || diag.Code == diagnostics.ErrBorrowEscape {
			t.Fatalf("unexpected ownership diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseRejectsImmutableBorrowWhileMutableBorrowIsLive(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn run(mut c: *Conn) -> void {
    let m = &mut *c
    let r = &*c
    m
    r
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
fn run() -> void {
    let mut x = 10
    let y = &mut x
    print(x)
    print(*y)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestOwnershipPhaseBorrowConflictShowsBorrowOriginLabel(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
fn run() -> void {
    let mut x = 10
    let y = &mut x
    print(x)
    print(*y)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code != diagnostics.ErrBorrowConflict {
			continue
		}
		if len(diag.Labels) < 2 {
			t.Fatalf("expected borrow conflict to include secondary borrow-origin label, got %#v", diag)
		}
		found := false
		for _, label := range diag.Labels {
			if label.Message == "borrow created here" && label.Location != nil && label.Location.Filename != nil {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected borrow-origin secondary label with file location, got %#v", diag)
		}
		return
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowConflict, result.Diagnostics.Diagnostics())
}

func TestOwnershipPhaseAllowsMultipleImmutableBorrows(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Point struct {
    Value: i32 = 0
}

fn run() -> void {
    let p: Point = .{}
    let a = &p
    let b = &p
    a
    b
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Handle enum {
    stdin,
    stdout,
}

fn make(flag: bool) -> Handle {
    if flag {
        return Handle::stdin
    }
    return Handle::stdout
}

fn main() -> i32 {
    let h = make(true)
    let other = h
    let code = other as i32
    return code + (h as i32)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Handle enum {
    stdin,
    stdout,
}

fn make(flag: bool) -> Handle {
    if flag {
        return Handle::stdin
    }
    return Handle::stdout
}

fn main() -> i32 {
    let h = make(true)
    return h as i32
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestOwnershipPhaseRejectsWholeValueUseAfterFieldMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn main(n: Node) -> i32 {
    let child = n.Child
    let dup = n
    return dup.Value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn main(n: Node) -> i32 {
    let value = n.Value
    let again = n
    return again.Value + value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn main(n: Node) -> i32 {
    let child = n.Child
    return n.Value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn main(n: Node, replacement: *Node) -> i32 {
    let mut current = n
    let child = current.Child
    current.Child = replacement
    let again = current
    return again.Value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseRejectsConditionalUseAfterMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn main(mut p: *Conn, cond: bool) -> *Conn {
    if cond {
        let x = p
    }
    return p
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestOwnershipPhaseRejectsMoveTypePlainAssignmentReuse(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn main(n: Node) -> i32 {
    let other = n
    return n.Value + other.Value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestOwnershipPhaseAllowsPlainValueParamCopy(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn usePoint(p: Point) -> i32 {
    return p.X
}

fn main() -> i32 {
    let p: Point = .{ .X = 1 }
    let x = usePoint(p)
    return x + p.X
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseRejectsMoveTypePlainValueParamReuse(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn take(n: Node) -> i32 {
    return n.Value
}

fn main(n: Node) -> i32 {
    let x = take(n)
    return x + n.Value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestOwnershipPhaseAllowsPlainValueReceiverMethodReuse(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Read(self) -> i32 {
    return self.X
}

fn main() -> i32 {
    let p: Point = .{ .X = 1 }
    return p.Read() + p.Read()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseRejectsMoveTypeValueReceiverMethodReuse(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn Node::Read(self) -> i32 {
    return self.Value
}

fn main(n: Node) -> i32 {
    return n.Read() + n.Read()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestOwnershipPhaseAllowsInterfaceValueMethodReuseForCopyType(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Read(self) -> i32 {
    return self.X
}

type Reader interface {
    Read(self) -> i32
}

fn main() -> i32 {
    let p: Point = .{ .X = 1 }
    let r: Reader = p
    return r.Read() + r.Read()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			t.Fatalf("unexpected use-after-move diagnostic %#v", diag)
		}
	}
}

func TestOwnershipPhaseRejectsMoveTypeInterfaceValueMethodReuse(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn Node::Read(self) -> i32 {
    return self.Value
}

type Reader interface {
    Read(self) -> i32
}

fn main(n: Node) -> i32 {
    let r: Reader = n
    return r.Read() + r.Read()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestOwnershipPhaseRejectsMoveTypeInterfaceValueParamReuse(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn Node::Read(self) -> i32 {
    return self.Value
}

type Reader interface {
    Read(self) -> i32
}

fn use(r: Reader) -> i32 {
    return r.Read()
}

fn main(n: Node) -> i32 {
    let r: Reader = n
    return use(r) + r.Read()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestOwnershipPhaseRejectsUnknownInterfaceValueReceiverReuseAcrossCallBoundary(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
type Node struct {
    Child: *Node
    Value: i32 = 0
}

fn Node::Read(self) -> i32 {
    return self.Value
}

type Reader interface {
    Read(self) -> i32
}

fn useTwice(r: Reader) -> i32 {
    return r.Read() + r.Read()
}

fn main(n: Node) -> i32 {
    let r: Reader = n
    return useTwice(r)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestOwnershipPhaseRejectsFileUseAfterConsumingClose(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "io.fer"), `
type Writer interface {
    Write(&self, text: str) -> usize
}

fn Write(dst: Writer, text: str) -> usize {
    return dst.Write(text)
}
`)
	mustWriteOwnership(t, filepath.Join(root, "mem.fer"), `
#[extern]
fn Expose<T>(owner: *T) -> ^T;

#[extern]
fn ExposeRef<T>(owner: &*T) -> ^T;

#[extern]
fn Adopt<T>(raw: ^T) -> *T;
`)
	mustWriteOwnership(t, filepath.Join(root, "fs.fer"), `
import "mem"

type fileInner struct {
    handle: ^void
}

type File struct {
    inner: *fileInner
}

#[extern("ferret_std_fs_open")]
fn open_raw(path: &str) -> ^void;

#[extern("ferret_std_fs_write")]
fn write_raw(handle: ^void, text: &str) -> usize;

#[extern("ferret_std_fs_close")]
fn close_raw(handle: ^void) -> void;

fn Open(path: str) -> File {
    unsafe {
        return .{
            .inner = mem::Adopt(open_raw(&path) as ^fileInner)
        }
    }
}

fn File::Write(&self, text: str) -> usize {
    unsafe {
        return write_raw(mem::ExposeRef(&self.inner) as ^void, &text)
    }
}

fn File::Close(self) -> void {
    unsafe {
        close_raw(mem::Expose(self.inner) as ^void)
    }
}
`)
	mustWriteOwnership(t, filepath.Join(root, "main.fer"), `
import "io"
import "fs"

fn main() -> void {
    let file = fs::Open("out.txt")
    file.Close()
    _ = io::Write(file, "after-close")
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func mustWriteOwnership(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
