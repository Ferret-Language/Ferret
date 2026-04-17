package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

	"compiler/internal/core/abi"
	"compiler/internal/core/context"
)

func bundleCore(root, bundleDir string, bundle32Bit bool) error {
	binDir := filepath.Join(bundleDir, "bin")
	libsDir := filepath.Join(bundleDir, "libs")
	for _, dir := range []string{bundleDir, binDir, libsDir} {
		if err := resetDir(dir); err != nil {
			return fmt.Errorf("prepare %s: %w", dir, err)
		}
	}

	if err := syncFerretLibs(filepath.Join(root, context.STD_LIB_DEV), libsDir); err != nil {
		return err
	}
	if err := buildRuntimeLib(filepath.Join(root, "runtime"), libsDir, 0); err != nil {
		return err
	}
	if bundle32Bit {
		if err := buildRuntimeLib(filepath.Join(root, "runtime"), libsDir, abi.Bits32); err != nil {
			return err
		}
	}
	if err := buildCompiler(root, filepath.Join(binDir, binaryName("ferret"))); err != nil {
		return err
	}
	for _, name := range []string{"README.md", "LICENSE"} {
		src := filepath.Join(root, name)
		if isFile(src) {
			if err := copyFile(src, filepath.Join(bundleDir, name)); err != nil {
				return fmt.Errorf("copy %s: %w", name, err)
			}
		}
	}
	return nil
}

func bundleToolchain(bundleDir string, tools map[string]string, bundle32Bit bool) error {
	binDir := filepath.Join(bundleDir, "bin")
	libDir := filepath.Join(bundleDir, "lib")
	for _, dir := range []string{bundleDir, binDir, libDir} {
		if err := resetDir(dir); err != nil {
			return fmt.Errorf("prepare %s: %w", dir, err)
		}
	}

	for _, name := range sortedKeys(tools) {
		if err := copyFile(tools[name], filepath.Join(binDir, name)); err != nil {
			return fmt.Errorf("copy tool %s: %w", name, err)
		}
	}

	clangPath := tools[binaryName("clang")]
	if err := copyClangResources(clangPath, libDir); err != nil {
		return err
	}
	if err := copyPlatformToolchainRuntime(bundleDir, libDir, toolPaths(tools), clangPath); err != nil {
		return err
	}
	if runtime.GOOS == "linux" && bundle32Bit {
		if err := copyLinux32ToolchainSupport(clangPath, bundleDir); err != nil {
			return err
		}
	}
	return nil
}

func resolveToolBinaries() (map[string]string, error) {
	tools := map[string]string{}

	for _, name := range bundledToolNames(runtime.GOOS) {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		tools[filepath.Base(path)] = path
	}
	for _, name := range requiredBundledToolNames(runtime.GOOS) {
		if _, ok := tools[binaryName(name)]; !ok {
			return nil, fmt.Errorf("bundle toolchain: %s is required in PATH for %s release packaging", name, runtime.GOOS)
		}
	}
	if runtime.GOOS == "windows" {
		clangPath := tools[binaryName("clang")]
		root64, root32 := windowsMingwRoots(clangPath)
		if root64 == "" || root32 == "" {
			return nil, fmt.Errorf("bundle toolchain: windows clang must come from an MSYS2 mingw64 root with sibling mingw32 (got %s)", clangPath)
		}
	}
	return tools, nil
}

func bundledToolNames(goos string) []string {
	names := []string{"clang", "clang++", "ld.lld", "lld", "lld-link"}
	if goos == "darwin" {
		names = append(names, "ld64.lld")
	}
	return names
}

func requiredBundledToolNames(goos string) []string {
	names := []string{"clang"}
	if goos == "darwin" {
		names = append(names, "ld64.lld")
	}
	return names
}
