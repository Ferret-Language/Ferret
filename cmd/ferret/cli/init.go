package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const compilerVersion = "0.1.0"

func InitCommand(args []string) error {
	if _, err := os.Stat("fer.ret"); err == nil {
		return fmt.Errorf("fer.ret already exists in current directory")
	}

	reader := bufio.NewReader(os.Stdin)
	projectName := ""
	if len(args) > 0 {
		projectName = args[0]
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return err
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

	projectName = strings.ToLower(strings.ReplaceAll(projectName, " ", "-"))

	fmt.Print("Description (optional): ")
	description, _ := reader.ReadString('\n')
	description = strings.TrimSpace(description)
	if description == "" {
		description = "A new Ferret project"
	}

	fmt.Print("Author (optional): ")
	author, _ := reader.ReadString('\n')
	author = strings.TrimSpace(author)

	content := fmt.Sprintf(`[package]
name = %q
version = "0.1.0"
description = %q
author = %q
compiler = "<=%s"
	entry = "main.ferr"

[dependencies]
# Add dependencies here
# neighbor-pkg = "../neighbor-pkg"
# remote-pkg = "github.com/user/repo@v1.0.0"
`, projectName, description, author, compilerVersion)

	if err := os.WriteFile("fer.ret", []byte(content), 0o644); err != nil {
		return err
	}

	if _, err := os.Stat("main.ferr"); os.IsNotExist(err) {
		mainContent := `import "std/io"

fn main() i32 {
	io::Println("Hello from Ferret!")
	return 0
}
`
		if err := os.WriteFile("main.ferr", []byte(mainContent), 0o644); err != nil {
			return err
		}
		printSuccess("Created main.ferr")
	}

	printSuccess(fmt.Sprintf("Initialized project: %s", projectName))
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Edit fer.ret to add dependencies")
	fmt.Println("  2. Run: ferret get")
	fmt.Println("  3. Run: ferret main.ferr")
	return nil
}
