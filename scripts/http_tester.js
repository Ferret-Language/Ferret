#!/usr/bin/env node
/* eslint-disable no-console */
const { spawn, spawnSync } = require("child_process");
const http = require("http");
const path = require("path");
const os = require("os");

function argValue(name, fallback) {
  const idx = process.argv.indexOf(name);
  if (idx >= 0 && idx + 1 < process.argv.length) {
    return process.argv[idx + 1];
  }
  return fallback;
}

function hasArg(name) {
  return process.argv.includes(name);
}

const entryFile = argValue("--entry", path.join("examples", "http_server.fer"));
const host = argValue("--host", "127.0.0.1");
const port = parseInt(argValue("--port", "3000"), 10);
const skipBuild = hasArg("--skip-build");
const noStart = hasArg("--no-start");

const binExt = process.platform === "win32" ? ".exe" : "";
const tmpOut = path.join(os.tmpdir(), `ferret_http_server${binExt}`);

function run(cmd, args, opts = {}) {
  const res = spawnSync(cmd, args, { stdio: "inherit", ...opts });
  if (res.status !== 0) {
    process.exit(res.status ?? 1);
  }
}

function request(method, urlPath, body, headers = {}) {
  return new Promise((resolve, reject) => {
    const req = http.request(
      {
        hostname: host,
        port,
        path: urlPath,
        method,
        headers: {
          "Content-Length": body ? Buffer.byteLength(body) : 0,
          ...headers,
        },
      },
      (res) => {
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => {
          const data = Buffer.concat(chunks).toString("utf8");
          resolve({ status: res.statusCode, body: data });
        });
      },
    );
    req.on("error", reject);
    if (body) {
      req.write(body);
    }
    req.end();
  });
}

async function waitForServer(timeoutMs = 5000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      await request("GET", "/");
      return;
    } catch {
      await new Promise((r) => setTimeout(r, 150));
    }
  }
  throw new Error("server did not start in time");
}

async function runTests() {
  const results = [];

  results.push({
    name: "GET /",
    expected: "Hello from Ferret HTTP!",
    actual: (await request("GET", "/")).body,
  });

  results.push({
    name: "GET /hello/:name",
    expected: "Hello ferret!",
    actual: (await request("GET", "/hello/ferret")).body,
  });

  results.push({
    name: "POST /echo",
    expected: "ping",
    actual: (await request("POST", "/echo", "ping")).body,
  });

  let failed = false;
  for (const r of results) {
    if (r.actual !== r.expected) {
      failed = true;
      console.error(
        `FAIL ${r.name}\n  expected: ${JSON.stringify(r.expected)}\n  actual:   ${JSON.stringify(r.actual)}`,
      );
    } else {
      console.log(`OK   ${r.name}`);
    }
  }
  if (failed) {
    process.exitCode = 1;
  }
}

async function main() {
  if (!skipBuild) {
    console.log("[build] compiling Ferret server...");
    run(path.join(".", "bin", "ferret"), ["-o", tmpOut, entryFile]);
  }

  let serverProc = null;
  if (!noStart) {
    console.log(`[run] starting server on :${port}`);
    serverProc = spawn(tmpOut, [], { stdio: "inherit" });
  } else {
    console.log(`[run] using external server on :${port}`);
  }

  const cleanup = () => {
    if (serverProc && !serverProc.killed) {
      serverProc.kill();
    }
  };
  process.on("exit", cleanup);
  process.on("SIGINT", () => {
    cleanup();
    process.exit(1);
  });

  try {
    await waitForServer();
    await runTests();
  } finally {
    cleanup();
  }
}

main().catch((err) => {
  console.error(err && err.message ? err.message : err);
  process.exit(1);
});
