# Ferret WASM Runtime (Browser)

This runtime provides the minimal JS imports the browser-only WASM backend expects.

## Runtime source

The canonical source is `runtime/wasm/runtime.ts`.
The playground consumes a copied TS file at `../website/src/lib/runtime.ts` (updated by the
`scripts/build-playground-wasm.*` scripts).

## Build the compiler (WASM)

From the repo root:

```bash
scripts/build-playground-wasm.sh
```

On Windows:

```bat
scripts\\build-playground-wasm.bat
```

This builds `bin/ferret.wasm` and, if the website repo is a sibling, automatically
copies it to `../website/public/ferret2.wasm`.

### Manual build + copy

```bash
GOOS=js GOARCH=wasm go build -o bin/ferret.wasm main_wasm.go
cp bin/ferret.wasm ../website/public/ferret2.wasm
```

Windows copy:

```bat
copy /Y bin\\ferret.wasm ..\\website\\public\\ferret2.wasm
```

Notes:
- `ferret_libs` is embedded into the compiler wasm. Rebuild after updating libs.
- The playground fetches `ferret2.wasm` by default.

Usage (sketch):

```ts
import { createFerretRuntime } from "./runtime.ts";

const rt = createFerretRuntime();
const { instance } = await WebAssembly.instantiateStreaming(fetch("program.wasm"), rt.imports);
rt.bind(instance);
instance.exports.main();
```

Run WASM in terminal (no browser):

```bash
# Compile + run
node scripts/run-wasm-terminal.mjs --entry examples/proposals/demo2.fer

# Run existing wasm
node scripts/run-wasm-terminal.mjs --wasm /tmp/program.wasm

# Provide stdin lines for std/io::Read*
node scripts/run-wasm-terminal.mjs --entry examples/basic.fer --input "hello\n42"
```

Notes:
- `ferret_alloc`, `ferret_array_*`, and `ferret_std_io_Print*` are implemented in JS.
- `__data_end` is exported by the compiler to seed the JS heap pointer.
