package main

import (
  "fmt"
  compiler "compiler/internal/driver"
  "compiler/internal/core/diagnostics"
  "compiler/internal/analysis/semantics/typeinfo"
  "compiler/internal/ir/hir"
)

func dumpCall(label string, call *hir.CallExpr) {
  if call == nil { return }
  ft, _ := call.Callee.Type().(*typeinfo.FuncType)
  if ft == nil || len(ft.Params)==0 { fmt.Println(label, "no fn params"); return }
  t := ft.Params[0].Type
  named, _ := t.(*typeinfo.NamedType)
  if named == nil {
    fmt.Printf("%s param0 type: %T %v\n", label, t, t)
    return
  }
  fmt.Printf("%s param0 named: %s module=%s declNil=%v typeArgs=%d\n", label, named.Name, named.ModuleKey, named.Decl==nil, len(named.TypeArgs))
}

func main() {
  result := compiler.New("/tmp", ".ferr", diagnostics.NewBag()).ParseEntry("/tmp/repro_iface_generic.ferr")
  if result.Diagnostics.HasErrors() { fmt.Println(result.Diagnostics.Diagnostics()); return }
  for _, fn := range result.Entry.HIR.Functions {
    if fn.Name != "main" { continue }
    es, _ := fn.Body.Stmts[2].(*hir.ExprStmt)
    call, _ := es.Value.(*hir.CallExpr)
    dumpCall("HIR", call)
  }
  for _, fn := range result.Entry.LoweredHIR.Functions {
    if fn.Name != "main" { continue }
    es, _ := fn.Body.Stmts[2].(*hir.ExprStmt)
    call, _ := es.Value.(*hir.CallExpr)
    dumpCall("LoweredHIR", call)
  }
}
