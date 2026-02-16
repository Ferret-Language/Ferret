#!/usr/bin/env node

import fs from "fs";
import os from "os";
import path from "path";
import { pathToFileURL } from "url";
import { spawnSync } from "child_process";

function usage() {
  console.log(`Usage:
  node scripts/run-wasm-terminal.mjs --entry <file.fer> [--input "line1\\nline2"]
  node scripts/run-wasm-terminal.mjs --wasm <file.wasm> [--input "line1\\nline2"]

Options:
  --entry <file>       Ferret source file to compile for wasm.
  --wasm <file>        Existing wasm program to run.
  --compiler <path>    Ferret compiler binary path (default: ./bin/ferret).
  --out <file>         Output wasm path when using --entry (default: /tmp/...).
  --input <text>       Input text consumed by std/io reads.
  --help               Show this help.
`);
}

function argValue(name, fallback = "") {
  const idx = process.argv.indexOf(name);
  if (idx >= 0 && idx + 1 < process.argv.length) {
    return process.argv[idx + 1];
  }
  return fallback;
}

function hasArg(name) {
  return process.argv.includes(name);
}

function fail(message) {
  console.error(message);
  process.exit(1);
}

function makeTempRuntime(runtimeTsPath, runtimeAbiPath) {
  const original = fs.readFileSync(runtimeTsPath, "utf8");
  const abiUrl = pathToFileURL(runtimeAbiPath).href;
  const patched = original.replace(
    'from "./runtime_abi";',
    `from "${abiUrl}";`,
  );
  const tempPath = path.join(
    os.tmpdir(),
    `ferret_runtime_terminal_${process.pid}_${Date.now()}.ts`,
  );
  fs.writeFileSync(tempPath, patched, "utf8");
  return tempPath;
}

function run(cmd, args) {
  const result = spawnSync(cmd, args, { stdio: "inherit" });
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}

async function main() {
  if (hasArg("--help") || hasArg("-h")) {
    usage();
    return;
  }

  const cwd = process.cwd();
  const entry = argValue("--entry");
  const wasmArg = argValue("--wasm");
  const compiler = argValue("--compiler", path.join(".", "bin", "ferret"));
  const input = argValue("--input", "");

  if (!entry && !wasmArg) {
    usage();
    fail("error: either --entry or --wasm is required");
  }
  if (entry && wasmArg) {
    fail("error: provide only one of --entry or --wasm");
  }

  let wasmPath = wasmArg;
  if (entry) {
    wasmPath = argValue(
      "--out",
      path.join(
        os.tmpdir(),
        `ferret_program_${path.basename(entry, path.extname(entry))}_${Date.now()}.wasm`,
      ),
    );
    run(compiler, ["-target", "wasm", "-o", wasmPath, entry]);
  }

  const absWasm = path.resolve(cwd, wasmPath);
  if (!fs.existsSync(absWasm)) {
    fail(`error: wasm file not found: ${absWasm}`);
  }

  const runtimeTsPath = path.resolve(cwd, "runtime", "wasm", "runtime.ts");
  const runtimeAbiPath = path.resolve(cwd, "runtime", "wasm", "runtime_abi.ts");
  if (!fs.existsSync(runtimeTsPath) || !fs.existsSync(runtimeAbiPath)) {
    fail("error: runtime/wasm/runtime.ts or runtime_abi.ts not found");
  }

  const tempRuntimePath = makeTempRuntime(runtimeTsPath, runtimeAbiPath);
  let runtime;
  try {
    const runtimeUrl = `${pathToFileURL(tempRuntimePath).href}?v=${Date.now()}`;
    runtime = await import(runtimeUrl);
  } finally {
    try {
      fs.unlinkSync(tempRuntimePath);
    } catch {
      // Best-effort cleanup for temp runtime copy.
    }
  }

  if (!runtime || typeof runtime.createFerretRuntime !== "function") {
    fail("error: failed to load createFerretRuntime from runtime.ts");
  }

  const wasm = fs.readFileSync(absWasm);
  const rt = runtime.createFerretRuntime({
    input,
    onPrint: (text) => process.stdout.write(text),
  });
  const { instance } = await WebAssembly.instantiate(wasm, rt.imports);
  rt.bind(instance);

  try {
    if (!instance.exports || typeof instance.exports.main !== "function") {
      fail("error: wasm program does not export main");
    }
    instance.exports.main();
  } catch (err) {
    const message = err && err.message ? err.message : String(err);
    fail(`Runtime error: ${message}`);
  }
}

main().catch((err) => {
  const message = err && err.message ? err.message : String(err);
  fail(message);
});
