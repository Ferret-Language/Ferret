package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InitCommand creates a new Ferret project with fer.ret
func InitCommand(args []string) error {
	var projectName string

	if len(args) > 0 {
		projectName = args[0]
	} else {
		// Use current directory name
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot get current directory: %w", err)
		}
		projectName = filepath.Base(cwd)
	}

	// Sanitize project name
	projectName = strings.ToLower(projectName)
	projectName = strings.ReplaceAll(projectName, " ", "-")

	// Check if fer.ret already exists
	if _, err := os.Stat("fer.ret"); err == nil {
		return fmt.Errorf("fer.ret already exists in current directory")
	}

	// Create fer.ret content
	content := fmt.Sprintf(`[package]
name = "%s"
version = "0.1.0"
description = "A new Ferret project"
author = ""

[dependencies]
# Add dependencies here
# neighbor-pkg = "../neighbor-pkg"
# remote-pkg = "github.com/user/repo@v1.0.0"

[dev]
mock_remote = false
mock_path = ""
`, projectName)

	// Write fer.ret
	if err := os.WriteFile("fer.ret", []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to create fer.ret: %w", err)
	}

	// Create main.fer if it doesn't exist
	if _, err := os.Stat("main.fer"); os.IsNotExist(err) {
		mainContent := `import "std/io";

fn main() {
    io::Println("Hello from Ferret!");
}
`
		if err := os.WriteFile("main.fer", []byte(mainContent), 0644); err != nil {
			return fmt.Errorf("failed to create main.fer: %w", err)
		}
		fmt.Println("  ✓ Created main.fer")
	}

	fmt.Printf("✓ Initialized project: %s\n", projectName)
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Edit fer.ret to add dependencies")
	fmt.Println("  2. Run: ferret get")
	fmt.Println("  3. Run: ferret main.fer")

	return nil
}
