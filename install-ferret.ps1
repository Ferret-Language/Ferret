param(
    [string]$Version = "latest",
    [string]$Repo = "itsfuad/compiler",
    [string]$InstallDir = "$env:LOCALAPPDATA\Ferret"
)

$ErrorActionPreference = "Stop"

function Get-ArchName {
    switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()) {
        "x64" { return "amd64" }
        "arm64" { return "arm64" }
        default { throw "Unsupported architecture: $([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture)" }
    }
}

function Get-ReleaseApiUrl([string]$repoName, [string]$ver) {
    if ($ver -eq "latest") {
        return "https://api.github.com/repos/$repoName/releases/latest"
    }
    return "https://api.github.com/repos/$repoName/releases/tags/$ver"
}

function Ensure-UserPathContains([string]$pathEntry) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ([string]::IsNullOrWhiteSpace($userPath)) {
        $userPath = $pathEntry
    } else {
        $parts = $userPath.Split(';', [System.StringSplitOptions]::RemoveEmptyEntries)
        $exists = $false
        foreach ($part in $parts) {
            if ($part.TrimEnd('\') -ieq $pathEntry.TrimEnd('\')) {
                $exists = $true
                break
            }
        }
        if (-not $exists) {
            $userPath = "$userPath;$pathEntry"
        }
    }
    [Environment]::SetEnvironmentVariable("Path", $userPath, "User")
}

$assetName = "ferret-windows-$(Get-ArchName).zip"
$apiUrl = Get-ReleaseApiUrl -repoName $Repo -ver $Version

Write-Host "Resolving release from $apiUrl"
$release = Invoke-RestMethod -Uri $apiUrl -Headers @{ "User-Agent" = "ferret-installer" }
$asset = $release.assets | Where-Object { $_.name -eq $assetName } | Select-Object -First 1
if (-not $asset) {
    throw "Could not find release asset '$assetName' in $Repo ($Version)."
}

$tmpDir = Join-Path $env:TEMP ("ferret-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmpDir | Out-Null
try {
    $zipPath = Join-Path $tmpDir $assetName
    Write-Host "Downloading $($asset.browser_download_url)"
    Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zipPath

    if (Test-Path $InstallDir) {
        Remove-Item -Recurse -Force $InstallDir
    }
    New-Item -ItemType Directory -Path $InstallDir | Out-Null
    Expand-Archive -Path $zipPath -DestinationPath $InstallDir -Force

    $binDir = Join-Path $InstallDir "bin"
    $ferretExe = Join-Path $binDir "ferret.exe"
    if (-not (Test-Path $ferretExe)) {
        $rootExe = Join-Path $InstallDir "ferret.exe"
        if (Test-Path $rootExe) {
            New-Item -ItemType Directory -Path $binDir -Force | Out-Null
            Move-Item -Force $rootExe $ferretExe
        }
    }
    if (-not (Test-Path $ferretExe)) {
        throw "ferret.exe not found after extraction at $ferretExe"
    }

    Ensure-UserPathContains -pathEntry $binDir
    if (-not ($env:Path -split ';' | Where-Object { $_.TrimEnd('\') -ieq $binDir.TrimEnd('\') })) {
        $env:Path = "$binDir;$env:Path"
    }

    Write-Host "Ferret installed to: $InstallDir"
    Write-Host "Binary: $ferretExe"
    Write-Host "User PATH updated. Open a new terminal and run: ferret --help"
}
finally {
    if (Test-Path $tmpDir) {
        Remove-Item -Recurse -Force $tmpDir
    }
}
