package cli

import (
	"bufio"
	"compiler/internal/compiler"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InitCommand creates a new Ferret project with fer.ret
func InitCommand(args []string) error {
	var projectName, description, author string

	// Check if fer.ret already exists
	if _, err := os.Stat("fer.ret"); err == nil {
		return fmt.Errorf("fer.ret already exists in current directory")
	}

	reader := bufio.NewReader(os.Stdin)

	// Get project name
	if len(args) > 0 {
		projectName = args[0]
	} else {
		// Use current directory name as default
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot get current directory: %w", err)
		}
		defaultName := filepath.Base(cwd)

		fmt.Printf("Project name (%s): ", defaultName)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			projectName = defaultName
		} else {
			projectName = input
		}
	}

	// Sanitize project name
	projectName = strings.ToLower(projectName)
	projectName = strings.ReplaceAll(projectName, " ", "-")

	// Get description (optional)
	fmt.Print("Description (optional): ")
	input, _ := reader.ReadString('\n')
	description = strings.TrimSpace(input)
	if description == "" {
		description = "A new Ferret project"
	}

	// Get author (optional)
	fmt.Print("Author (optional): ")
	input, _ = reader.ReadString('\n')
	author = strings.TrimSpace(input)

	// Create fer.ret content with compiler version requirement
	content := fmt.Sprintf(`[package]
name = "%s"
version = "0.1.0"
description = "%s"
author = "%s"
compiler = "<=%s"

[dependencies]
# Add dependencies here
# neighbor-pkg = "../neighbor-pkg"
# remote-pkg = "github.com/user/repo@v1.0.0"
`, projectName, description, author, compiler.Version)

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

	fmt.Printf("\n✓ Initialized project: %s\n", projectName)
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Edit fer.ret to add dependencies")
	fmt.Println("  2. Run: ferret get")
	fmt.Println("  3. Run: ferret main.fer")

	return nil
}
