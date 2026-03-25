package mir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
	"compiler/internal/ir/mir"
)

func TestPipelineGeneratesMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
    Y: i32 = 0
}

let mut GlobalPoint: Point = .{ .X = 1, .Y = 2 }

fn main() -> i32 {
    let mut p = GlobalPoint
    if p.X > 0 {
        p.X = p.X + 1
    }
    return p.X
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.MIR == nil {
		t.Fatal("expected MIR module")
	}
	if len(result.Entry.MIR.Types) != 1 {
		t.Fatalf("expected one mir type decl, got %#v", result.Entry.MIR.Types)
	}
	if len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one mir function, got %#v", result.Entry.MIR.Functions)
	}
	fn := result.Entry.MIR.Functions[0]
	if fn.EntryID < 0 {
		t.Fatalf("expected valid entry id, got %#v", fn)
	}
	foundStore := false
	foundBranch := false
	foundCompute := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instructions {
			if _, ok := instr.(*mir.StoreFieldInstr); ok {
				foundStore = true
			}
			if _, ok := instr.(*mir.ComputeInstr); ok {
				foundCompute = true
			}
		}
		if _, ok := block.Terminator.(*mir.BranchTerm); ok {
			foundBranch = true
		}
	}
	if !foundStore {
		t.Fatal("expected store_field instruction in lowered MIR")
	}
	if !foundBranch {
		t.Fatal("expected branch terminator in lowered MIR")
	}
	if !foundCompute {
		t.Fatal("expected compute instruction in normalized MIR")
	}
	for _, block := range fn.Blocks {
		switch term := block.Terminator.(type) {
		case *mir.BranchTerm:
			if _, ok := term.Cond.(*mir.LocalValue); !ok {
				t.Fatalf("expected branch condition temp, got %T", term.Cond)
			}
		case *mir.ReturnTerm:
			if term.Value != nil {
				switch term.Value.(type) {
				case *mir.LocalValue, *mir.NameValue, *mir.NumberValue, *mir.StringValue, *mir.NoneValue:
				default:
					t.Fatalf("expected normalized simple return value, got %T", term.Value)
				}
			}
		}
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "type Point struct") {
		t.Fatalf("expected type declaration in mir dump, got %q", text)
	}
	if !strings.Contains(text, "X: i32 = 0") || !strings.Contains(text, "Y: i32 = 0") {
		t.Fatalf("expected field defaults in mir dump, got %q", text)
	}
}

func TestPipelineNormalizesMethodReceiverAsFirstArgument(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Bump(&mut self) -> i32 {
    return self.X + 1
}

fn main() -> i32 {
    let mut p: Point = .{ .X = 1 }
    return (&mut p).Bump()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			compute, ok := instr.(*mir.ComputeInstr)
			if !ok {
				continue
			}
			call, ok := compute.Value.(*mir.CallValue)
			if !ok || !hasCallNamed(call, "Bump") {
				continue
			}
			if len(call.Args) == 0 {
				t.Fatalf("expected normalized receiver arg, got %#v", call)
			}
			refType, ok := call.Args[0].Type().(*typeinfo.RefType)
			if !ok || !refType.Mutable {
				t.Fatalf("expected explicit mutable ref receiver as first argument, got %T %#v", call.Args[0].Type(), call.Args[0].Type())
			}
			return
		}
	}
	t.Fatalf("expected normalized Bump call in MIR, got %#v", mainFn.Blocks)
}

func TestPipelineNormalizesAttachedReferenceReceiverFromValueCall(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Bump(&mut self) -> i32 {
    self.X++
    return self.X
}

fn main() -> i32 {
    let mut p: Point = .{ .X = 1 }
    return p.Bump()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			compute, ok := instr.(*mir.ComputeInstr)
			if !ok {
				continue
			}
			call, ok := compute.Value.(*mir.CallValue)
			if !ok || !hasCallNamed(call, "Bump") {
				continue
			}
			if len(call.Args) == 0 {
				t.Fatalf("expected normalized receiver arg, got %#v", call)
			}
			refType, ok := call.Args[0].Type().(*typeinfo.RefType)
			if !ok || !refType.Mutable {
				t.Fatalf("expected implicit mutable ref receiver as first argument, got %T %#v", call.Args[0].Type(), call.Args[0].Type())
			}
			return
		}
	}
	t.Fatalf("expected normalized Bump call in MIR, got %#v", mainFn.Blocks)
}

func TestPipelineLowersArrayLenToStaticValue(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn main() -> usize {
    let items: [_]i32 = [_]i32{1, 2, 3}
    return len(items)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			compute, ok := instr.(*mir.ComputeInstr)
			if !ok {
				continue
			}
			call, ok := compute.Value.(*mir.CallValue)
			if ok && hasCallNamed(call, "len") {
				t.Fatalf("expected len(array) to be lowered without runtime call, got %#v", call)
			}
		}
		if term, ok := block.Terminator.(*mir.ReturnTerm); ok {
			if number, ok := term.Value.(*mir.NumberValue); ok && number.Value == "3" {
				return
			}
		}
	}
	t.Fatalf("expected static len(array) return value, got:\n%s", mir.FormatModule(result.Entry.MIR))
}

func TestPipelineLowersForLoopIndexUpdateToHiddenCounter(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let items: [3]i32 = [3]i32{1, 2, 3}
    for items |v| {
        print(v)
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}

	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}

	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			store, ok := instr.(*mir.StoreInstr)
			if !ok {
				continue
			}
			deref, ok := store.Target.(*mir.DerefPlace)
			if !ok {
				continue
			}
			addr, ok := deref.Pointer.(*mir.AddrOfValue)
			if !ok {
				continue
			}
			local, ok := addr.Source.(*mir.LocalValue)
			if !ok {
				continue
			}
			if _, ok := local.Type().(*typeinfo.ArrayType); ok {
				t.Fatalf("for-loop counter update targeted iterable storage instead of hidden index: %#v", instr)
			}
		}
	}
}

func TestPipelineLowersInterfaceCoercionWithMutableReceiver(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Reader interface {
    read(&mut self, buf: []u8) -> i32
}

type File struct {
    value: i32 = 0
}

fn File::read(&mut self, buf: []u8) -> i32 {
    return self.value
}

fn main() -> i32 {
    let mut f: File = .{ .value = 7 }
    let r: Reader = &mut f
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			var value mir.Value
			switch ins := instr.(type) {
			case *mir.AssignInstr:
				value = ins.Value
			case *mir.ComputeInstr:
				value = ins.Value
			case *mir.EvalInstr:
				value = ins.Value
			}
			if value == nil {
				continue
			}
			iface, ok := value.(*mir.InterfaceValue)
			if !ok {
				continue
			}
			refType, ok := iface.ConcreteType.(*typeinfo.RefType)
			if !ok || !refType.Mutable {
				t.Fatalf("expected mutable reference concrete type, got %T %#v", iface.ConcreteType, iface.ConcreteType)
			}
			named, ok := refType.Inner.(*typeinfo.NamedType)
			if !ok || named.Name != "File" {
				t.Fatalf("expected &mut File concrete type, got %#v", iface.ConcreteType)
			}
			if len(iface.Methods) != 1 {
				t.Fatalf("expected one interface method link, got %#v", iface.Methods)
			}
			path := iface.Methods[0].Path
			if len(path) == 0 {
				t.Fatalf("expected method link path, got %#v", iface.Methods[0])
			}
			last := path[len(path)-1]
			if last != "File__read" && !strings.HasSuffix(last, "__File__read") {
				t.Fatalf("expected File__read method link, got %#v", iface.Methods[0])
			}
			return
		}
	}
	t.Fatalf("expected lowered interface coercion in MIR, got %#v", mainFn.Blocks)
}

func TestPipelineLowersGenericOwnerInterfaceCallCoercion(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Shape interface {
    Draw(&self)
}

type Point<T> struct {
    Value: T
}

fn Point<T>::Draw(&self) {
}

fn drawShape(s: Shape) {
    s.Draw()
}

fn main() -> void {
    let p: Point<i32> = .{ .Value = 2 }
    drawShape(p)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}
	findInterfaceLink := func(value mir.Value) bool {
		ifaceArg, ok := value.(*mir.InterfaceValue)
		if !ok || len(ifaceArg.Methods) != 1 {
			return false
		}
		path := ifaceArg.Methods[0].Path
		return len(path) > 0 && strings.HasSuffix(path[len(path)-1], "Point$T_i32__Draw$T_i32")
	}
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			var value mir.Value
			switch ins := instr.(type) {
			case *mir.AssignInstr:
				value = ins.Value
			case *mir.ComputeInstr:
				value = ins.Value
			case *mir.EvalInstr:
				value = ins.Value
			default:
				continue
			}
			if findInterfaceLink(value) {
				return
			}
			call, ok := value.(*mir.CallValue)
			if !ok {
				continue
			}
			for _, arg := range call.Args {
				if findInterfaceLink(arg) {
					return
				}
				localArg, ok := arg.(*mir.LocalValue)
				if !ok {
					continue
				}
				for _, searchBlock := range mainFn.Blocks {
					for _, searchInstr := range searchBlock.Instructions {
						var assignedValue mir.Value
						var targetID int
						switch assign := searchInstr.(type) {
						case *mir.AssignInstr:
							assignedValue = assign.Value
							targetID = assign.TargetID
						case *mir.ComputeInstr:
							assignedValue = assign.Value
							targetID = assign.TargetID
						default:
							continue
						}
						if targetID != localArg.LocalID {
							continue
						}
						if findInterfaceLink(assignedValue) {
							return
						}
					}
				}
			}
		}
	}
	t.Fatalf("expected specialized interface coercion for Point<i32>::Draw, got %#v", mainFn.Blocks)
}

func TestPipelineLowersVariadicSpreadCall(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn sum(nums: ...i32) -> i32 {
    return nums[0]
}

fn main(items: []i32) -> i32 {
    return sum(items...)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			compute, ok := instr.(*mir.ComputeInstr)
			if !ok {
				continue
			}
			call, ok := compute.Value.(*mir.CallValue)
			if !ok || !hasCallNamed(call, "sum") {
				continue
			}
			if len(call.Args) != 1 {
				t.Fatalf("expected spread call lowered to single slice arg, got %#v", call.Args)
			}
			if _, ok := call.Args[0].Type().(*typeinfo.SliceType); !ok {
				t.Fatalf("expected slice arg type, got %T %#v", call.Args[0].Type(), call.Args[0].Type())
			}
			if _, ok := call.Args[0].(*mir.CompositeValue); ok {
				t.Fatalf("expected direct spread slice passthrough, got packed composite %#v", call.Args[0])
			}
			return
		}
	}
	t.Fatalf("expected lowered sum call in MIR, got %#v", mainFn.Blocks)
}

func TestPipelineNormalizesStoreCallValueIntoTemp(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn inc(x: i32) -> i32 {
    return x + 1
}

fn main() -> i32 {
    let mut x = 1
    x = inc(x)
    return x
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}

	foundCallCompute := false
	foundStore := false
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			switch ins := instr.(type) {
			case *mir.ComputeInstr:
				call, ok := ins.Value.(*mir.CallValue)
				if ok && hasCallNamed(call, "inc") {
					foundCallCompute = true
				}
			case *mir.StoreInstr:
				foundStore = true
				if _, ok := ins.Value.(*mir.CallValue); ok {
					t.Fatalf("expected store call value to be normalized into a temp, got %#v", ins)
				}
			}
		}
	}
	if !foundCallCompute {
		t.Fatalf("expected inc call to be lowered into a compute temp, got %#v", mainFn.Blocks)
	}
	if !foundStore {
		t.Fatalf("expected mutable local assignment to lower into a store, got %#v", mainFn.Blocks)
	}
}

func TestPipelineLowersConstrainedGenericMethodCallWithSpecializedOwnerPath(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Shape interface {
    Draw(&self)
}

type Circle<T> struct {
    Rad: T
}

fn Circle<T>::New(v: T) -> Self {
    return .{ .Rad = v }
}

fn Circle<T>::Draw(&self) {
}

fn drawShape<T: Shape>(s: T) -> void {
    s.Draw()
}

fn main() -> void {
    let cir = Circle<i32>::New(1)
    drawShape(cir)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var drawShapeFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && strings.HasPrefix(fn.Name, "drawShape$") {
			drawShapeFn = fn
			break
		}
	}
	if drawShapeFn == nil {
		t.Fatalf("expected specialized drawShape function, got %#v", result.Entry.MIR.Functions)
	}
	found := false
	for _, block := range drawShapeFn.Blocks {
		for _, instr := range block.Instructions {
			call, ok := instrValue(instr).(*mir.CallValue)
			if !ok {
				continue
			}
			callee, ok := call.Callee.(*mir.NameValue)
			if !ok || len(callee.Path) == 0 {
				continue
			}
			last := callee.Path[len(callee.Path)-1]
			if strings.Contains(last, "Circle$T_i32__Draw$T_i32") {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("expected constrained generic call to use specialized Draw path, got blocks %#v\nmodule:\n%s", drawShapeFn.Blocks, mir.FormatModule(result.Entry.MIR))
	}
}

func instrValue(instr mir.Instr) mir.Value {
	switch ins := instr.(type) {
	case *mir.AssignInstr:
		return ins.Value
	case *mir.ComputeInstr:
		return ins.Value
	case *mir.EvalInstr:
		return ins.Value
	default:
		return nil
	}
}

func TestPipelineLowersRawAddressCoercion(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> i32 {
    let mut p: Point = .{}
    unsafe {
        let rp: ^Point = &mut p
        let x = (*rp).X
        return x
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			var value mir.Value
			var typ typeinfo.Type
			switch ins := instr.(type) {
			case *mir.BindInstr:
				if ins.Name != "rp" {
					continue
				}
				value, typ = ins.Value, ins.Type
			case *mir.AssignInstr:
				value = ins.Value
			case *mir.ComputeInstr:
				value, typ = ins.Value, ins.Type
			default:
				continue
			}
			addr, ok := value.(*mir.AddrOfValue)
			if !ok || !addr.Raw || !addr.Mutable {
				continue
			}
			if typ == nil {
				typ = value.Type()
			}
			if _, ok := typ.(*typeinfo.RawPtrType); !ok {
				t.Fatalf("expected raw pointer type, got %#v", typ)
			}
			return
		}
	}
	t.Fatalf("expected lowered raw address bind in MIR, got %#v", mainFn.Blocks)
}

func TestPipelineLowersStringLiteralDataAsRawPointer(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn main() -> str {
    return "hi"
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			compute, ok := instr.(*mir.ComputeInstr)
			if !ok {
				continue
			}
			comp, ok := compute.Value.(*mir.CompositeValue)
			if !ok {
				continue
			}
			for _, item := range comp.Items {
				if item.Name != "ptr" {
					continue
				}
				if _, ok := item.Value.Type().(*typeinfo.RawPtrType); !ok {
					t.Fatalf("expected string ptr item to lower as raw pointer, got %T %#v", item.Value.Type(), item.Value.Type())
				}
				return
			}
		}
	}
	t.Fatalf("expected lowered string composite in MIR, got %#v", mainFn.Blocks)
}

func TestPipelinePreservesAnnotatedUnionLocalTypeInMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i32 {
    let value: Token = 1
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one MIR function, got %#v", result.Entry)
	}
	fn := result.Entry.MIR.Functions[0]
	if len(fn.Locals) != 1 {
		t.Fatalf("expected one MIR local, got %#v", fn.Locals)
	}
	if got := fn.Locals[0].Type.String(); got != "Token" {
		t.Fatalf("expected MIR local type Token, got %q", got)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "value: Token") {
		t.Fatalf("expected annotated union local in MIR dump, got %q", text)
	}
}

func TestPipelineGeneratesExplicitAddrOfAndLoadInMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn probe(p: *Point) -> void {
    let q = &*p
    q
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one MIR function, got %#v", result.Entry)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "load p") {
		t.Fatalf("expected explicit load in MIR dump, got %q", text)
	}
	if !strings.Contains(text, "addr_of") {
		t.Fatalf("expected explicit addr_of in MIR dump, got %q", text)
	}
}

func TestPipelinePreservesAddrOfLocalInsteadOfFoldingToConstant(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let mut x = 10;
    let y = &mut x;
    *y = 12;
    _ = *y;
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	text := mir.FormatModule(result.Entry.MIR)
	if strings.Contains(text, "addr_of_mut 10") {
		t.Fatalf("did not expect addr_of of folded constant, got %q", text)
	}
	if !strings.Contains(text, "addr_of_mut x") {
		t.Fatalf("expected addr_of_mut x in MIR dump, got %q", text)
	}
}

func TestPipelineLowersPanicToMIRTerminator(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn fail() -> void {
    panic "bad"
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected MIR functions, got %#v", result.Entry)
	}
	var fn *mir.Function
	for _, candidate := range result.Entry.MIR.Functions {
		if candidate.Name == "fail" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatalf("expected MIR function fail, got %#v", result.Entry.MIR.Functions)
	}
	if len(fn.Blocks) == 0 {
		t.Fatalf("expected MIR blocks, got %#v", fn)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "panic ") || !strings.Contains(text, "\"bad\"") {
		t.Fatalf("expected lowered panic sequence in MIR dump, got %q", text)
	}
}

func TestPipelineLowersDeferredPanicCleanupToMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn close() -> void {}

fn fail() -> void {
    defer close()
    panic "bad"
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	var fn *mir.Function
	for _, candidate := range result.Entry.MIR.Functions {
		if candidate.Name == "fail" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatalf("expected MIR function fail, got %#v", result.Entry.MIR.Functions)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "defer close()") || !strings.Contains(text, "panic ") || !strings.Contains(text, "close()") {
		t.Fatalf("expected deferred panic cleanup sequence in MIR dump, got %q", text)
	}
}

func TestPipelineLowersDeferredReturnCleanupToMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn close() -> void {}

fn run() -> i32 {
    defer close()
    return 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	var fn *mir.Function
	for _, candidate := range result.Entry.MIR.Functions {
		if candidate.Name == "run" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatalf("expected MIR function run, got %#v", result.Entry.MIR.Functions)
	}
	found := false
	for _, block := range fn.Blocks {
		term, ok := block.Terminator.(*mir.ReturnTerm)
		if !ok {
			continue
		}
		found = true
		if term.CleanupID < 0 {
			t.Fatalf("expected return cleanup id, got %#v", term)
		}
		break
	}
	if !found {
		t.Fatalf("expected return terminator in MIR, got %#v", fn.Blocks)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "return 1 unwind") {
		t.Fatalf("expected return unwind in MIR dump, got %q", text)
	}
}

func TestPipelineDoesNotLowerImplicitLifecycleHooksToMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
    Y: i32 = 0
}

fn Point::New() -> Point {
    return .{}
}

fn Point::Bump(*self) {
	self.Y = self.Y + 1
}

fn Point::Drop(*self) -> void {
    _ = self
}

fn main() -> i32 {
	let p: Point = .{ .X = 3, .Y = 4 }
    let q: Point = .{}
    return p.X + q.X
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}

	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}

	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			switch ins := instr.(type) {
			case *mir.AssignInstr:
				_ = ins
			case *mir.ComputeInstr:
				_ = ins
			case *mir.EvalInstr:
				_ = ins
			case *mir.DeferInstr:
				t.Fatalf("did not expect implicit defer in MIR, got %#v", ins)
			}
		}
	}
}

func TestPipelineHandlesLocalShadowingInMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let x = 1
    if true {
        let x = 2
        x
    }
    return x
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one MIR function, got %#v", result.Entry)
	}
	fn := result.Entry.MIR.Functions[0]

	outerID := -1
	innerID := -1
	for _, local := range fn.Locals {
		if local == nil {
			continue
		}
		if local.Name == "x" {
			outerID = local.ID
		}
		if strings.HasPrefix(local.Name, "x#") {
			innerID = local.ID
		}
	}
	if outerID < 0 || innerID < 0 {
		t.Fatalf("expected shadowed locals x and x#..., got locals %#v", fn.Locals)
	}

	foundReturn := false
	for _, block := range fn.Blocks {
		term, ok := block.Terminator.(*mir.ReturnTerm)
		if !ok || term == nil {
			continue
		}
		foundReturn = true
		local, ok := term.Value.(*mir.LocalValue)
		if !ok || local == nil {
			t.Fatalf("expected return value to be a local, got %T", term.Value)
		}
		if local.LocalID != outerID {
			t.Fatalf("expected return to use outer x local id %d, got %d", outerID, local.LocalID)
		}
	}
	if !foundReturn {
		t.Fatalf("expected return terminator, got blocks %#v", fn.Blocks)
	}
}

func hasCallNamed(call *mir.CallValue, name string) bool {
	if call == nil {
		return false
	}
	callee, ok := call.Callee.(*mir.NameValue)
	if !ok || len(callee.Path) == 0 {
		return false
	}
	last := callee.Path[len(callee.Path)-1]
	return last == name || strings.HasSuffix(last, "__"+name)
}

func mustWriteIR(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
