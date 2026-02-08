//go:build !js && !wasm

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"compiler/cmd/cli"
	"compiler/colors"
	"compiler/internal/compiler"
)

func main() {
	// Define flags
	debug := flag.Bool("d", false, "Enable debug output")
	showVersion := flag.Bool("v", false, "Show version")
	saveAST := flag.Bool("ast", false, "Save AST")
	help := flag.Bool("h", false, "Show help")
	outputPath := flag.String("o", "", "Output executable path")
	keepGenFiles := flag.Bool("keep-gen", false, "Keep generated files")
	typecheckOnly := flag.Bool("t", false, "Stop after type checking (skip codegen)")
	target := flag.String("target", "native", "Compilation target: native | wasm")
	flag.BoolVar(debug, "debug", false, "Enable debug output")
	flag.BoolVar(showVersion, "version", false, "Show version")
	flag.BoolVar(help, "help", false, "Show help")
	flag.BoolVar(keepGenFiles, "k", false, "Keep generated files")
	flag.BoolVar(typecheckOnly, "typecheck", false, "Stop after type checking (skip codegen)")

	flag.Parse()

	// Get command/args
	args := flag.Args()

	// Check for CLI commands (before flags check)
	if len(args) > 0 {
		command := args[0]
		commandArgs := args[1:]

		// Handle package management commands
		switch command {
		case "get":
			if err := cli.GetCommand(commandArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "remove", "rm":
			if err := cli.RemoveCommand(commandArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "list", "ls":
			if err := cli.ListCommand(commandArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "update":
			if err := cli.UpdateCommand(commandArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "cleanup", "clean":
			if err := cli.CleanupCommand(commandArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "init":
			if err := cli.InitCommand(commandArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	// Handle version
	if *showVersion {
		fmt.Printf("Ferret compiler version %s\n", compiler.Version)
		os.Exit(0)
	}

	// Handle help
	if *help {
		fmt.Fprintln(os.Stdout, "Ferret - A modern programming language")
		fmt.Fprintln(os.Stdout, "\nUsage:")
		fmt.Fprintln(os.Stdout, "  ferret [command] [args]")
		fmt.Fprintln(os.Stdout, "  ferret [options] <file>")
		
		fmt.Fprintln(os.Stdout, "\nPackage Management Commands:")
		fmt.Fprintln(os.Stdout, "  init [name]              Create a new Ferret project with fer.ret")
		fmt.Fprintln(os.Stdout, "  get                      Install all dependencies from fer.ret")
		fmt.Fprintln(os.Stdout, "  get <pkg1> <pkg2>...     Install specific packages")
		fmt.Fprintln(os.Stdout, "  update                   Update all dependencies to latest compatible versions")
		fmt.Fprintln(os.Stdout, "  update <pkg1> <pkg2>...  Update specific packages")
		fmt.Fprintln(os.Stdout, "  remove <package>         Remove a dependency and cleanup orphaned deps")
		fmt.Fprintln(os.Stdout, "  list                     List all dependencies (direct and transitive)")
		fmt.Fprintln(os.Stdout, "  cleanup                  Remove unused cached packages")
		
		fmt.Fprintln(os.Stdout, "\nCompilation Options:")
		flag.PrintDefaults()
		
		fmt.Fprintln(os.Stdout, "\nExamples:")
		fmt.Fprintln(os.Stdout, "  ferret init myapp")
		fmt.Fprintln(os.Stdout, "  ferret get github.com/user/logger@^v1.0.0")
		fmt.Fprintln(os.Stdout, "  ferret update")
		fmt.Fprintln(os.Stdout, "  ferret main.fer")
		fmt.Fprintln(os.Stdout, "  ferret -o app main.fer")
		
		fmt.Fprintln(os.Stdout, "\nFor more information, visit: https://ferret-lang.org")
		os.Exit(0)
	}

	// Get entry file
	args = flag.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Error: No input file specified")
		fmt.Fprintln(os.Stderr, "\nUsage:")
		fmt.Fprintln(os.Stderr, "  ferret [options] <file>")
		fmt.Fprintln(os.Stderr, "  ferret [command] [args]")
		fmt.Fprintln(os.Stderr, "\nRun 'ferret --help' for more information")
		os.Exit(1)
	}

	// Check if there are any arguments that look like flags after the file
	// This helps catch common mistakes like: ferret file.fer -k
	if len(args) > 1 {
		for _, arg := range args[1:] {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "Warning: Flag '%s' appears after the file argument. Flags must come before the file.\n", arg)
				fmt.Fprintf(os.Stderr, "Use: ferret [options] <file>\n")
			}
		}
	}

	entryFile := args[0]

	codegenBackend := "qbe"

	targetValue := strings.ToLower(strings.TrimSpace(*target))
	switch targetValue {
	case "native":
	case "wasm":
		codegenBackend = "wasm"
	default:
		fmt.Fprintf(os.Stderr, "Unknown target: %s (expected native or wasm)\n", *target)
		os.Exit(1)
	}

	// Compile
	result := compiler.Compile(&compiler.Options{
		EntryFile:        entryFile,
		Debug:            *debug,
		SaveAST:          *saveAST,
		LogFormat:        compiler.ANSI,
		OutputExecutable: *outputPath,
		KeepGenFiles:     *keepGenFiles,
		TypecheckOnly:    *typecheckOnly,
		CodegenBackend:   codegenBackend,
	})

	// Exit code
	if !result.Success {
		colors.RED.Println(result.Output)
		os.Exit(1)
	}
}
