package main

import (
    "fmt"
    "os"
    "strings"

    "compiler/internal/backend"
    "compiler/internal/backend/registry"
    compilerapi "compiler/internal/compiler"
    "compiler/internal/context"
    "compiler/internal/project"
)

func backendLayouts(result compilerapi.Result) map[string]*layout.Module
func backendModules(result compilerapi.Result) map[string]*midmir.Module
func allModulesForBuild(result compilerapi.Result) []*context.Module

func parse(path string) compilerapi.Result {
    ws, err := project.Load(path, ".ferr")
    if err != nil { panic(err) }
    ws.Context.TargetBackend = "qbe"
    diag := diagnostics.NewDiagnosticBag(path)
    c := compilerapi.NewWithConfig(ws.Context, diag)
    return c.ParseEntry(path)
}

func main() {
    _ = strings.Builder{}
    _ = backend.Unit{}
    _ = registry.New
    fmt.Println("cannot compile helper: uses unexported funcs")
    os.Exit(1)
}
