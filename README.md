# Ferret Compiler
[![release](https://github.com/Ferret-Language/Ferret/actions/workflows/release.yml/badge.svg)](https://github.com/Ferret-Language/Ferret/actions/workflows/release.yml)

This folder contains the Ferret compiler, the bundled toolchain/build logic, and installer assets under `installers/`.

## Install from GitHub Releases

The release installer entrypoints live under `installers/`. The shell and PowerShell installers download the latest Ferret release from `Ferret-Language/Ferret`, extract the compiler bundle and toolchain, and add `ferret` to your `PATH`.

Supported release installers:

- Linux: `amd64`, `arm64`
- macOS: `amd64`, `arm64`
- Windows: `amd64`, `arm64`

### Linux

```bash
curl -fsSL https://raw.githubusercontent.com/Ferret-Language/Ferret/refs/heads/main/installers/install.sh | bash
```

If you prefer `wget`:

```bash
wget -qO- https://raw.githubusercontent.com/Ferret-Language/Ferret/refs/heads/main/installers/install.sh | bash
```

Install a specific release tag:

```bash
curl -fsSL https://raw.githubusercontent.com/Ferret-Language/Ferret/refs/heads/main/installers/install.sh | bash -s -- v0.1.0
```

Default install location:

```text
~/.local/ferret
```

Binary location:

```text
~/.local/ferret/core/bin/ferret
```

### macOS

Use the same installer script:

```bash
curl -fsSL https://raw.githubusercontent.com/Ferret-Language/Ferret/refs/heads/main/installers/install.sh | bash
```

Install a specific release tag:

```bash
curl -fsSL https://raw.githubusercontent.com/Ferret-Language/Ferret/refs/heads/main/installers/install.sh | bash -s -- v0.1.0
```

Default install location:

```text
~/.local/ferret
```

### Windows PowerShell

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/Ferret-Language/Ferret/refs/heads/main/installers/install.ps1 -OutFile ferret.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\ferret.ps1
```

Install a specific release tag:

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/Ferret-Language/Ferret/refs/heads/main/installers/install.ps1 -OutFile ferret.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\ferret.ps1 -Version v0.1.0
```

### Windows CMD

Download and run the CMD entrypoint:

```bat
curl -fsSLO https://raw.githubusercontent.com/Ferret-Language/Ferret/refs/heads/main/installers/install.cmd
ferret.cmd
```

Default install location:

```text
%LOCALAPPDATA%\Ferret
```

Binary location:

```text
%LOCALAPPDATA%\Ferret\core\bin\ferret.exe
```

## Verify the installation

Open a new terminal and run:

```bash
ferret --help
```

## Build from source

Prerequisite:

- Go must be installed and available in `PATH`.

### Linux and macOS

```bash
./build.sh
```

### Windows

```bat
build.bat
```

The packaged output is written under:

```text
build/core
build/toolchain
```
