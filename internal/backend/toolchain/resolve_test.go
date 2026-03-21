package toolchain

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeNames(t *testing.T) {
	got := normalizeNames([]string{" clang ", "clang.exe", "", "qbe"})
	if len(got) != 2 || got[0] != "clang" || got[1] != "qbe" {
		t.Fatalf("normalizeNames mismatch: %#v", got)
	}
}

func TestExeName(t *testing.T) {
	got := exeName("ferret")
	if runtime.GOOS == "windows" {
		if got != "ferret.exe" {
			t.Fatalf("exeName mismatch on windows: %q", got)
		}
		return
	}
	if got != "ferret" {
		t.Fatalf("exeName mismatch: %q", got)
	}
}

func TestEnvHelpers(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/tmp"}
	if v, ok := lookupEnv(env, "PATH"); !ok || v != "/usr/bin" {
		t.Fatalf("lookup PATH mismatch: %q %v", v, ok)
	}
	env = setEnv(env, "PATH", "/opt/bin")
	if v, ok := lookupEnv(env, "PATH"); !ok || v != "/opt/bin" {
		t.Fatalf("set PATH mismatch: %q %v", v, ok)
	}
	env = prependPathLikeEnv(env, "PATH", []string{"/x", "/y"})
	if v, _ := lookupEnv(env, "PATH"); !strings.HasPrefix(v, "/x"+string(os.PathListSeparator)+"/y") {
		t.Fatalf("prepend PATH mismatch: %q", v)
	}
}

func TestUniqueDirs(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	dups := []string{a, filepath.Clean(a), b, ""}
	out := uniqueDirs(dups)
	if len(out) != 2 {
		t.Fatalf("uniqueDirs mismatch: %#v", out)
	}
	out = uniqueExistingDirs(append(dups, filepath.Join(a, "missing")))
	if len(out) != 2 {
		t.Fatalf("uniqueExistingDirs mismatch: %#v", out)
	}
}
