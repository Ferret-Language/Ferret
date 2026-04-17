package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "build bundler: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		return fmt.Errorf("this command has no subcommands; use: go run ./bundler")
	}

	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	buildDir := filepath.Join(root, "build")
	if err := resetDir(buildDir); err != nil {
		return fmt.Errorf("prepare build dir: %w", err)
	}
	for _, legacy := range []string{"libs", "bin"} {
		if err := os.RemoveAll(filepath.Join(root, legacy)); err != nil {
			return fmt.Errorf("clean legacy %s: %w", legacy, err)
		}
	}

	coreDir := filepath.Join(buildDir, "core")
	toolchainDir := filepath.Join(buildDir, "toolchain")
	tools, err := resolveToolBinaries()
	if err != nil {
		return err
	}
	bundle32Bit, err := supportsBundled32BitTarget(tools)
	if err != nil {
		return err
	}
	if err := bundleCore(root, coreDir, bundle32Bit); err != nil {
		return err
	}
	if err := bundleToolchain(toolchainDir, tools, bundle32Bit); err != nil {
		return err
	}

	fmt.Printf("packaged\n\t- %s\n\t- %s\n", coreDir, toolchainDir)
	return nil
}
