# Ferret Compiler

This folder contains the Ferret compiler, installer scripts, and local build scripts.

## Install from GitHub Releases

The install scripts download the latest Ferret release from `Ferret-Language/Ferret`, extract the compiler bundle and toolchain, and add `ferret` to your `PATH`.

Supported release installers:

- Linux: `amd64`, `arm64`
- macOS: `amd64`, `arm64`
- Windows: `amd64`, `arm64`

### Linux

```bash
curl -fsSL https://raw.githubusercontent.com/Ferret-Language/Ferret/main/compiler/install-ferret.sh | bash
```

If you prefer `wget`:

```bash
wget -qO- https://raw.githubusercontent.com/Ferret-Language/Ferret/main/compiler/install-ferret.sh | bash
```

Install a specific release tag:

```bash
curl -fsSL https://raw.githubusercontent.com/Ferret-Language/Ferret/main/compiler/install-ferret.sh | bash -s -- v0.1.0
```

Default install location:

```text
~/.local/ferret
```

### macOS

Use the same installer script:

```bash
curl -fsSL https://raw.githubusercontent.com/Ferret-Language/Ferret/main/compiler/install-ferret.sh | bash
```

Install a specific release tag:

```bash
curl -fsSL https://raw.githubusercontent.com/Ferret-Language/Ferret/main/compiler/install-ferret.sh | bash -s -- v0.1.0
```

Default install location:

```text
~/.local/ferret
```

### Windows PowerShell

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/Ferret-Language/Ferret/main/compiler/install-ferret.ps1 -OutFile install-ferret.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\install-ferret.ps1
```

Install a specific release tag:

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/Ferret-Language/Ferret/main/compiler/install-ferret.ps1 -OutFile install-ferret.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\install-ferret.ps1 -Version v0.1.0
```

### Windows CMD

Download and run the wrapper script:

```bat
curl -fsSLO https://raw.githubusercontent.com/Ferret-Language/Ferret/main/compiler/install-ferret.cmd
install-ferret.cmd
```

Default install location:

```text
%LOCALAPPDATA%\Ferret
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
