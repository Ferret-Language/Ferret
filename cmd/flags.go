package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"compiler/cmd/cli"
	"compiler/colors"
	"compiler/internal/backend"
	"compiler/internal/core/abi"
	compiler "compiler/internal/driver"
	"compiler/internal/lsp"
)

type compilerFlags struct {
	backend    string
	outputPath string
	keepGen    bool
	debugBuild bool
	inputPath  string
}

func parseAndRunCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}

	command := args[0]
	commandArgs := args[1:]
	commandName, commandBackend, err := parseCommandBackend(command)
	if err != nil {
		colors.RED.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	switch commandName {
	case "lsp":
		colors.CYAN.Fprintln(os.Stderr, "starting Ferret LSP server...")
		if err := lsp.Run(os.Stdin, os.Stdout); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "init":
		if err := cli.InitCommand(commandArgs); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "get":
		if err := cli.GetCommand(commandArgs); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "update":
		if err := cli.UpdateCommand(commandArgs); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "sniff":
		if err := cli.SniffCommand(commandArgs); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "remove", "rm":
		if err := cli.RemoveCommand(commandArgs); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "list", "ls":
		if err := cli.ListCommand(commandArgs); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "clean":
		if err := cli.CleanupCommand(commandArgs); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "orphans":
		if err := cli.OrphansCommand(commandArgs); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "run":
		if err := runCommand(commandArgs, commandBackend); err != nil {
			if errors.Is(err, errAlreadyReported) {
				os.Exit(1)
			}
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "test":
		if err := testCommand(commandArgs, commandBackend); err != nil {
			if errors.Is(err, errAlreadyReported) {
				os.Exit(1)
			}
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "check", "lint":
		if err := checkCommand(commandArgs); err != nil {
			if errors.Is(err, errAlreadyReported) {
				os.Exit(1)
			}
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	default:
		return false
	}
}

func parseCompilerFlags() compilerFlags {
	backendTarget := flag.String("backend", "llvm", "backend target (llvm|qbe)")
	logFormat := flag.String("logformat", string(colors.LogFormatANSI), "log output format (ansi|normal|html)")
	m32 := flag.Bool("m32", false, "target 32-bit ABI")
	outputPath := flag.String("o", "", "compile and link to executable")
	keepGen := flag.Bool("keep-gen", false, "keep generated AST/HIR/MIR/backend IR in _gen directory")
	flag.BoolVar(keepGen, "k", false, "alias for -keep-gen")
	debugBuild := flag.Bool("debug", false, "enable debug build mode (emits debug info and debug-friendly codegen)")
	showVersion := flag.Bool("version", false, "show compiler version")
	flag.BoolVar(showVersion, "v", false, "alias for -version")
	showHelp := flag.Bool("help", false, "show help")
	flag.BoolVar(showHelp, "h", false, "alias for -help")

	flag.Usage = func() {
		colors.BLUE.Fprintln(os.Stderr, "Ferret compiler v"+compiler.CompilerVersion)
		colors.CYAN.Fprintln(os.Stderr, "\nUsage:")
		colors.GREEN.Fprintf(os.Stderr, "  ferret [options] <source-file-or-directory>\n")
		colors.GREEN.Fprintf(os.Stderr, "  ferret [command] [args]\n")
		colors.CYAN.Fprintln(os.Stderr, "\nOptions:")
		flag.PrintDefaults()
		colors.CYAN.Fprintln(os.Stderr, "\nCommands:")
		fmt.Fprintln(os.Stderr, "  init [name]             create a new project with fer.ret")
		fmt.Fprintln(os.Stderr, "  get [pkg ...]           install dependencies from fer.ret or specific packages")
		fmt.Fprintln(os.Stderr, "  update [pkg ...]        update locked dependencies")
		fmt.Fprintln(os.Stderr, "  sniff [pkg ...]         preview updates that ferret update would apply")
		fmt.Fprintln(os.Stderr, "  remove|rm <alias>       remove dependency alias from fer.ret and lockfile")
		fmt.Fprintln(os.Stderr, "  list|ls                 list direct and transitive dependencies")
		fmt.Fprintln(os.Stderr, "  orphans                 list orphaned cache/lock entries clean will remove")
		fmt.Fprintln(os.Stderr, "  cleanup|clean           remove orphaned cached dependencies")
		fmt.Fprintln(os.Stderr, "  check|lint [path]       typecheck file or recursively check folder (.fer only)")
		fmt.Fprintln(os.Stderr, "  run[:llvm|:qbe] [path] [args]  build and run a program (default llvm)")
		fmt.Fprintln(os.Stderr, "  test[:llvm|:qbe] [path] [args] build and run unit tests (default llvm)")
		colors.CYAN.Fprintln(os.Stderr, "\nExamples:")
		colors.GREEN.Fprintf(os.Stderr, "  ferret -backend llvm main.fer\n")
		colors.GREEN.Fprintf(os.Stderr, "  ferret -m32 -o app32 main.fer\n")
		colors.GREEN.Fprintf(os.Stderr, "  ferret -k main.fer\n")
		colors.GREEN.Fprintf(os.Stderr, "  ferret run main.fer arg1 arg2\n")
	}

	flag.Parse()
	if err := colors.SetLogFormatString(*logFormat); err != nil {
		colors.RED.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *m32 {
		if err := abi.SetSizeBits(abi.Bits32); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	} else if err := abi.SetSizeBits(0); err != nil {
		colors.RED.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if *showVersion {
		fmt.Printf("v%s\n", compiler.CompilerVersion)
		os.Exit(0)
	}
	if *showHelp {
		flag.Usage()
		os.Exit(0)
	}
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	selectedBackend := strings.ToLower(strings.TrimSpace(*backendTarget))
	if selectedBackend == "" {
		selectedBackend = "llvm"
	}
	if selectedBackend != string(backend.TargetLLVM) && selectedBackend != string(backend.TargetQBE) {
		colors.RED.Fprintf(os.Stderr, "Error: invalid backend %q (expected llvm or qbe)\n", selectedBackend)
		os.Exit(2)
	}

	return compilerFlags{
		backend:    selectedBackend,
		outputPath: *outputPath,
		keepGen:    *keepGen,
		debugBuild: *debugBuild,
		inputPath:  flag.Arg(0),
	}
}
