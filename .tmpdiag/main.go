package main
import (
  "fmt"
  "os"
  "path/filepath"
  compiler "compiler/internal/driver"
  "compiler/internal/core/diagnostics"
)
func write(path, content string){ _ = os.MkdirAll(filepath.Dir(path),0o755); _ = os.WriteFile(path, []byte(content),0o644) }
func main(){
  dir, _ := os.MkdirTemp("", "ferret-repro-")
  defer os.RemoveAll(dir)
  write(filepath.Join(dir, "main.fer"), os.Args[1])
  result := compiler.New(dir, ".fer", diagnostics.NewDiagnosticBag("")) .ParseEntry(filepath.Join(dir, "main.fer"))
  if result.Entry != nil { fmt.Printf("phase=%v\n", result.Entry.Phase) }
  for _, d := range result.Diagnostics.Diagnostics() {
    if d == nil { continue }
    fmt.Printf("%s: %s\n", d.Code, d.Message)
  }
}
