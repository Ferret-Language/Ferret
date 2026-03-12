package main
import (
  "fmt"
  compilerapi "compiler/internal/compiler"
  "compiler/internal/diagnostics"
)
func main(){
  result := compilerapi.New("/tmp/cfgwarn", ".ferr", diagnostics.NewBag()).ParseEntry("/tmp/cfgwarn/main.ferr")
  for _, d := range result.Diagnostics.Diagnostics() {
    fmt.Printf("code=%s msg=%s\n", d.Code, d.Message)
  }
}
