package gen

import (
	"fmt"
	"strings"
)

const refHeapParamPrefix = "__ref_heap$"
const outHeapParamName = "__out_heap"
const selfHeapParamName = "__self_heap"

func refHeapParamName(index int) string {
	return fmt.Sprintf("%s%d", refHeapParamPrefix, index)
}

func isRefHeapParamName(name string) bool {
	return strings.HasPrefix(name, refHeapParamPrefix)
}
