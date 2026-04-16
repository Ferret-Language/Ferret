# Ferret Windows Installer

This folder contains the Windows installer assets for Ferret.

The main GUI installer is built with `Inno Setup`.

The installer:

- downloads Ferret compiler and toolchain assets from GitHub releases
- resolves release metadata from standard GitHub release pages and direct asset URLs
- verifies the SHA-256 digest shown on the GitHub release assets page
- installs per-user by default into `%LocalAppData%\Programs\Ferret`
- supports all-users installation into `Program Files` when the user chooses admin install mode
- adds `Ferret\bin` to `PATH` when selected
- registers a normal Windows uninstaller in `Apps & Features`

## Files

- `ferret-installer.iss`: main Inno Setup script
- `install.ps1`: release-downloading PowerShell installer
- `install.cmd`: CMD entrypoint for the PowerShell installer
- `install.sh`: Unix release installer kept in the same installer layout
- `scripts/Resolve-FerretRelease.ps1`: helper script that resolves release metadata from GitHub

## Build

1. Install Inno Setup 6.4 or newer.
2. Open `ferret-installer.iss` in the Inno Setup Compiler, or run:

```powershell
iscc .\ferret-installer.iss
```

The generated bootstrap installer is written to `dist\FerretSetup.exe`.

In GitHub Actions release builds, that file is copied into the repo-level `dist/` output as an arch-qualified asset such as `FerretSetup-windows-amd64.exe`.

## Installer behavior

- Default release source: latest Ferret release
- Optional release source: specific tag such as `v0.0.7`
- Components:
  - `Ferret compiler` (required)
  - `Toolchain` (enabled by default)
- Task:
  - `Add Ferret to PATH`

## Uninstall behavior

The uninstaller removes:

- the Ferret install directory
- the recorded `PATH` entry if the installer added it

## Notes

- The current implementation expects both Windows assets to exist on the selected release:
  - `ferret-windows-<arch>.zip`
  - `ferret-toolchain-windows-<arch>.zip`
- On Arm64 Windows, the helper prefers native `arm64` assets when they exist and falls back to `amd64`.
- The installer shows a custom size summary for the selected components because the payload is downloaded at install time rather than bundled into the setup executable.
