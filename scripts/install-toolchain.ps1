$ErrorActionPreference = "Stop"

$msysRoot = "C:\msys64"
$pacman = Join-Path $msysRoot "usr\bin\pacman.exe"

if (-not (Test-Path $pacman)) {
  $winget = Get-Command winget -ErrorAction SilentlyContinue
  if (-not $winget) {
    Write-Host "MSYS2 not found and winget is unavailable. Install MSYS2 from https://www.msys2.org/ and re-run."
    exit 1
  }

  Write-Host "Installing MSYS2 via winget..."
  winget install -e --id MSYS2.MSYS2 --accept-source-agreements --accept-package-agreements
}

if (-not (Test-Path $pacman)) {
  Write-Host "MSYS2 installation not found at $msysRoot."
  exit 1
}

Write-Host "Updating MSYS2 packages..."
& $pacman -Syu --noconfirm

Write-Host "Installing MinGW toolchain..."
& $pacman -S --needed --noconfirm mingw-w64-x86_64-gcc mingw-w64-x86_64-binutils

$mingwBin = Join-Path $msysRoot "mingw64\bin"
if (-not ($env:Path -split ';' | Where-Object { $_ -eq $mingwBin })) {
  $env:Path = "$mingwBin;$env:Path"
}

Write-Host "Done. Ensure '$mingwBin' is on PATH for new terminals."
