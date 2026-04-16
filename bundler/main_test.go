package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBinaryNameAndIsFile(t *testing.T) {
	got := binaryName("ferret")
	if runtime.GOOS == "windows" {
		if got != "ferret.exe" {
			t.Fatalf("binaryName mismatch: %q", got)
		}
	} else if got != "ferret" {
		t.Fatalf("binaryName mismatch: %q", got)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isFile(file) {
		t.Fatalf("expected file path to be detected")
	}
	if isFile(dir) {
		t.Fatalf("expected dir path to be false")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "nested", "dst.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile error: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("copied content mismatch: %q", string(got))
	}
}

func TestSkipBundledSharedLibraryLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		return
	}
	if !skipBundledSharedLibrary("/lib/x86_64-linux-gnu/libc.so.6") {
		t.Fatalf("expected libc to be skipped")
	}
	if skipBundledSharedLibrary("/opt/custom/libsomething.so") {
		t.Fatalf("custom library should not be skipped")
	}
}

func TestShouldSkipWindowsToolchainPath(t *testing.T) {
	cases := map[string]bool{
		"libkernel32.a":                          false,
		"crt2.o":                                 false,
		"python3.14/os.py":                       true,
		"cmake/clang/clangConfig.cmake":          true,
		"pkgconfig/zlib.pc":                      true,
		"terminfo/x/xterm":                       true,
		"clang/Driver/Driver.h":                  true,
		"clang-c/Index.h":                        true,
		"lld/Common/ErrorHandler.h":              true,
		"gcc/i686-w64-mingw32/15.2.0/libgcc.a":   false,
		"gcc/i686-w64-mingw32/15.2.0/cc1.exe":    true,
		"gcc/i686-w64-mingw32/15.2.0/plugin/x":   true,
		"gcc/i686-w64-mingw32/15.2.0/crtbegin.o": false,
	}
	for name, want := range cases {
		if got := shouldSkipWindowsToolchainPath(name); got != want {
			t.Fatalf("shouldSkipWindowsToolchainPath(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestCopyDirContentsFilteredSkipsWindowsToolchainProjects(t *testing.T) {
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	files := map[string]string{
		filepath.Join(srcRoot, "libkernel32.a"):                                    "a",
		filepath.Join(srcRoot, "python3.14", "os.py"):                              "b",
		filepath.Join(srcRoot, "cmake", "clang", "clangConfig.cmake"):              "c",
		filepath.Join(srcRoot, "gcc", "i686-w64-mingw32", "15.2.0", "libgcc.a"):    "d",
		filepath.Join(srcRoot, "gcc", "i686-w64-mingw32", "15.2.0", "cc1.exe"):     "e",
		filepath.Join(srcRoot, "gcc", "i686-w64-mingw32", "15.2.0", "plugin", "z"): "f",
		filepath.Join(srcRoot, "clang", "Driver", "Driver.h"):                      "g",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := copyDirContentsFiltered(srcRoot, dstRoot, shouldSkipWindowsToolchainPath); err != nil {
		t.Fatalf("copyDirContentsFiltered error: %v", err)
	}

	wantPresent := []string{
		filepath.Join(dstRoot, "libkernel32.a"),
		filepath.Join(dstRoot, "gcc", "i686-w64-mingw32", "15.2.0", "libgcc.a"),
	}
	for _, path := range wantPresent {
		if !isFile(path) {
			t.Fatalf("expected copied file %s", path)
		}
	}

	wantAbsent := []string{
		filepath.Join(dstRoot, "python3.14", "os.py"),
		filepath.Join(dstRoot, "cmake", "clang", "clangConfig.cmake"),
		filepath.Join(dstRoot, "gcc", "i686-w64-mingw32", "15.2.0", "cc1.exe"),
		filepath.Join(dstRoot, "gcc", "i686-w64-mingw32", "15.2.0", "plugin", "z"),
		filepath.Join(dstRoot, "clang", "Driver", "Driver.h"),
	}
	for _, path := range wantAbsent {
		if isFile(path) {
			t.Fatalf("did not expect copied file %s", path)
		}
	}
}

func TestLinux32BitHostArchSupported(t *testing.T) {
	cases := map[string]bool{
		"amd64":   true,
		"386":     true,
		"arm64":   false,
		"riscv64": false,
	}
	for goarch, want := range cases {
		if got := linux32BitHostArchSupported(goarch); got != want {
			t.Fatalf("linux32BitHostArchSupported(%q) = %v, want %v", goarch, got, want)
		}
	}
}

func TestBundledToolNames(t *testing.T) {
	linux := bundledToolNames("linux")
	for _, want := range []string{"clang", "clang++", "ld.lld", "lld", "lld-link"} {
		if !containsString(linux, want) {
			t.Fatalf("bundledToolNames(linux) missing %q in %#v", want, linux)
		}
	}
	if containsString(linux, "ld64.lld") {
		t.Fatalf("bundledToolNames(linux) should not include ld64.lld: %#v", linux)
	}

	darwin := bundledToolNames("darwin")
	if !containsString(darwin, "ld64.lld") {
		t.Fatalf("bundledToolNames(darwin) missing ld64.lld in %#v", darwin)
	}
}

func TestRequiredBundledToolNames(t *testing.T) {
	linux := requiredBundledToolNames("linux")
	if len(linux) != 1 || linux[0] != "clang" {
		t.Fatalf("requiredBundledToolNames(linux) = %#v, want only clang", linux)
	}

	darwin := requiredBundledToolNames("darwin")
	for _, want := range []string{"clang", "ld64.lld"} {
		if !containsString(darwin, want) {
			t.Fatalf("requiredBundledToolNames(darwin) missing %q in %#v", want, darwin)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestParseOtoolDependencies(t *testing.T) {
	out := `/tmp/clang:
	@rpath/libclang-cpp.dylib (compatibility version 1.0.0, current version 1.0.0)
	/usr/lib/libSystem.B.dylib (compatibility version 1.0.0, current version 1351.0.0)
	/opt/homebrew/opt/llvm/lib/libunwind.1.dylib (compatibility version 1.0.0, current version 1.0.0)
`
	got := parseOtoolDependencies(out)
	want := []string{
		"@rpath/libclang-cpp.dylib",
		"/usr/lib/libSystem.B.dylib",
		"/opt/homebrew/opt/llvm/lib/libunwind.1.dylib",
	}
	if len(got) != len(want) {
		t.Fatalf("parseOtoolDependencies len = %d, want %d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseOtoolDependencies[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseDarwinRPaths(t *testing.T) {
	out := `
Load command 10
          cmd LC_RPATH
      cmdsize 32
         path @executable_path/../lib (offset 12)
Load command 11
          cmd LC_RPATH
      cmdsize 40
         path /opt/homebrew/opt/llvm/lib (offset 12)
`
	got := parseDarwinRPaths(out)
	want := []string{"@executable_path/../lib", "/opt/homebrew/opt/llvm/lib"}
	if len(got) != len(want) {
		t.Fatalf("parseDarwinRPaths len = %d, want %d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseDarwinRPaths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveDarwinDependencyPathRPath(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	libDir := filepath.Join(root, "lib")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := filepath.Join(binDir, "clang")
	if err := os.WriteFile(loader, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(libDir, "libclang-cpp.dylib")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, include, err := resolveDarwinDependencyPath(loader, loader, "@rpath/libclang-cpp.dylib", []string{"@executable_path/../lib"})
	if err != nil {
		t.Fatalf("resolveDarwinDependencyPath returned error: %v", err)
	}
	if !include {
		t.Fatal("resolveDarwinDependencyPath should include resolved @rpath dep")
	}
	if got != want {
		t.Fatalf("resolveDarwinDependencyPath = %q, want %q", got, want)
	}
}

func TestResolveDarwinDependencyPathLoaderPath(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := filepath.Join(libDir, "libclang-cpp.dylib")
	if err := os.WriteFile(loader, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(libDir, "libLLVM.dylib")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, include, err := resolveDarwinDependencyPath(loader, filepath.Join(root, "bin", "clang"), "@loader_path/libLLVM.dylib", nil)
	if err != nil {
		t.Fatalf("resolveDarwinDependencyPath returned error: %v", err)
	}
	if !include {
		t.Fatal("resolveDarwinDependencyPath should include resolved @loader_path dep")
	}
	if got != want {
		t.Fatalf("resolveDarwinDependencyPath = %q, want %q", got, want)
	}
}

func TestDarwinBundledDependencyChangesForLibrary(t *testing.T) {
	root := t.TempDir()
	loaderDir := filepath.Join(root, "llvm", "lib")
	depDir := filepath.Join(root, "z3", "lib")
	if err := os.MkdirAll(loaderDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := filepath.Join(loaderDir, "libLLVM.dylib")
	dep := filepath.Join(depDir, "libz3.4.15.dylib")
	if err := os.WriteFile(loader, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	copied := map[string]string{
		dep: "/bundle/lib/libz3.4.15.dylib",
	}
	changes, err := darwinBundledDependencyChanges(
		loader,
		filepath.Join(root, "llvm", "bin", "clang"),
		[]string{dep},
		nil,
		copied,
		false,
	)
	if err != nil {
		t.Fatalf("darwinBundledDependencyChanges returned error: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("darwinBundledDependencyChanges len = %d, want 1 (%#v)", len(changes), changes)
	}
	if changes[0].Old != dep {
		t.Fatalf("change old = %q, want z3 dylib path", changes[0].Old)
	}
	if changes[0].New != "@loader_path/libz3.4.15.dylib" {
		t.Fatalf("change new = %q, want @loader_path/libz3.4.15.dylib", changes[0].New)
	}
}

func TestDarwinBundledDependencyChangesForBinary(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "llvm", "bin")
	libDir := filepath.Join(root, "llvm", "lib")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	clangPath := filepath.Join(binDir, "clang")
	libPath := filepath.Join(libDir, "libclang-cpp.dylib")
	if err := os.WriteFile(clangPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	copied := map[string]string{
		libPath: "/bundle/lib/libclang-cpp.dylib",
	}
	changes, err := darwinBundledDependencyChanges(
		clangPath,
		clangPath,
		[]string{"@rpath/libclang-cpp.dylib"},
		[]string{libDir},
		copied,
		true,
	)
	if err != nil {
		t.Fatalf("darwinBundledDependencyChanges returned error: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("darwinBundledDependencyChanges len = %d, want 1 (%#v)", len(changes), changes)
	}
	if changes[0].Old != "@rpath/libclang-cpp.dylib" {
		t.Fatalf("change old = %q, want @rpath/libclang-cpp.dylib", changes[0].Old)
	}
	if changes[0].New != "@executable_path/../lib/libclang-cpp.dylib" {
		t.Fatalf("change new = %q, want @executable_path/../lib/libclang-cpp.dylib", changes[0].New)
	}
}
