package ferretlibs

import "embed"

//go:embed *.fer std/*.fer net/*.fer db/*.fer
var FS embed.FS
