[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Version = "latest",

    [Parameter(Mandatory = $false)]
    [string]$Repo = "Ferret-Language/Ferret",

    [Parameter(Mandatory = $false)]
    [ValidateRange(5, 300)]
    [int]$RequestTimeoutSec = 15,

    [Parameter(Mandatory = $true)]
    [string]$OutputPath
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

function Invoke-HttpRequest {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Uri,

        [Parameter(Mandatory = $true)]
        [string]$Method
    )

    return Invoke-WebRequest -Uri $Uri -Method $Method -UseBasicParsing -MaximumRedirection 5 -TimeoutSec $RequestTimeoutSec
}

function Convert-DisplaySizeToBytes {
    param(
        [Parameter(Mandatory = $true)]
        [string]$DisplaySize
    )

    if ($DisplaySize -notmatch '^\s*(?<value>\d+(?:\.\d+)?)\s*(?<unit>bytes?|kb|mb|gb|tb)\s*$') {
        throw "Unsupported display size format: $DisplaySize"
    }

    $value = [decimal]$Matches.value
    switch ($Matches.unit.ToUpperInvariant()) {
        "BYTE" { $multiplier = [decimal]1 }
        "BYTES" { $multiplier = [decimal]1 }
        "KB" { $multiplier = [decimal]1024 }
        "MB" { $multiplier = [decimal]1048576 }
        "GB" { $multiplier = [decimal]1073741824 }
        "TB" { $multiplier = [decimal]1099511627776 }
        default { throw "Unsupported display size unit: $($Matches.unit)" }
    }

    return [Int64][Math]::Round($value * $multiplier)
}

function Get-ReleaseTag {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Repository,

        [Parameter(Mandatory = $true)]
        [string]$RequestedVersion
    )

    if ($RequestedVersion -ne "latest") {
        return $RequestedVersion
    }

    $response = Invoke-HttpRequest -Uri "https://github.com/$Repository/releases/latest" -Method Head
    $resolvedUri = $response.BaseResponse.ResponseUri.AbsoluteUri
    if ($resolvedUri -notmatch "/releases/tag/(?<tag>[^/?#]+)$") {
        throw "Could not resolve the latest release tag from $resolvedUri"
    }

    return $Matches.tag
}

function Get-PreferredArchitectures {
    $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()

    switch ($architecture) {
        "x64" { return @("amd64") }
        "arm64" { return @("arm64", "amd64") }
        default { throw "Unsupported Windows architecture: $architecture" }
    }
}

function Get-Sha256Digest {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Digest
    )

    if ($Digest -notmatch "^sha256:(?<sha>[0-9a-fA-F]{64})$") {
        throw "Unsupported digest format: $Digest"
    }

    return $Matches.sha.ToLowerInvariant()
}

function Find-Asset {
    param(
        [Parameter(Mandatory = $true)]
        $Assets,

        [Parameter(Mandatory = $true)]
        [string[]]$CandidateNames
    )

    foreach ($candidateName in $CandidateNames) {
        $asset = $Assets | Where-Object { $_.name -eq $candidateName } | Select-Object -First 1
        if ($asset) {
            return $asset
        }
    }

    return $null
}

function Get-ReleaseAssets {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Repository,

        [Parameter(Mandatory = $true)]
        [string]$ReleaseTag
    )

    $expandedAssetsUrl = "https://github.com/$Repository/releases/expanded_assets/$ReleaseTag"
    $html = Invoke-HttpRequest -Uri $expandedAssetsUrl -Method Get | Select-Object -ExpandProperty Content
    $pattern = '<a href="(?<href>/[^"]+/releases/download/[^"]+/(?<name>[^"/]+))"(?s:.*?)<span[^>]*class="Truncate-text">(?<digest>sha256:[0-9a-fA-F]{64})</span>(?s:.*?)<span[^>]*class="color-fg-muted text-right[^"]*">(?<size>[^<]+)</span>'

    $assets = @()
    foreach ($match in [regex]::Matches($html, $pattern)) {
        $assets += [pscustomobject]@{
            name = $match.Groups["name"].Value
            url = "https://github.com$($match.Groups["href"].Value)"
            digest = $match.Groups["digest"].Value
            display_size = $match.Groups["size"].Value.Trim()
        }
    }

    if (-not $assets) {
        throw "Could not find release assets on $expandedAssetsUrl"
    }

    return $assets
}

function Write-IniFile {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [hashtable]$Sections
    )

    $lines = New-Object System.Collections.Generic.List[string]

    foreach ($sectionName in $Sections.Keys) {
        [void]$lines.Add("[$sectionName]")

        $section = $Sections[$sectionName]
        foreach ($entry in $section.GetEnumerator() | Sort-Object Name) {
            [void]$lines.Add(($entry.Key + "=" + $entry.Value))
        }

        [void]$lines.Add("")
    }

    $directory = Split-Path -Parent $Path
    if ($directory -and -not (Test-Path -LiteralPath $directory)) {
        New-Item -ItemType Directory -Path $directory | Out-Null
    }

    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllLines($Path, $lines, $utf8NoBom)
}

$releaseTag = Get-ReleaseTag -Repository $Repo -RequestedVersion $Version
$assets = Get-ReleaseAssets -Repository $Repo -ReleaseTag $releaseTag

$resolvedArch = $null
$compilerAsset = $null
$toolchainAsset = $null

foreach ($arch in Get-PreferredArchitectures) {
    $compilerCandidate = "ferret-windows-$arch.zip"
    $toolchainCandidate = "ferret-toolchain-windows-$arch.zip"

    $candidateCompilerAsset = Find-Asset -Assets $assets -CandidateNames @($compilerCandidate)
    if (-not $candidateCompilerAsset) {
        continue
    }

    $candidateToolchainAsset = Find-Asset -Assets $assets -CandidateNames @($toolchainCandidate)
    if (-not $candidateToolchainAsset) {
        continue
    }

    $resolvedArch = $arch
    $compilerAsset = $candidateCompilerAsset
    $toolchainAsset = $candidateToolchainAsset
    break
}

if (-not $compilerAsset) {
    throw "Could not find a supported Ferret Windows compiler asset in release '$releaseTag'."
}

if (-not $toolchainAsset) {
    throw "Could not find a supported Ferret Windows toolchain asset in release '$releaseTag'."
}

$compilerDownloadSize = Convert-DisplaySizeToBytes -DisplaySize $compilerAsset.display_size
$toolchainDownloadSize = Convert-DisplaySizeToBytes -DisplaySize $toolchainAsset.display_size

Write-IniFile -Path $OutputPath -Sections @{
    release = @{
        tag = $releaseTag
        resolved_arch = $resolvedArch
    }
    compiler = @{
        name = $compilerAsset.name
        url = $compilerAsset.url
        sha256 = (Get-Sha256Digest -Digest $compilerAsset.digest)
        download_size = $compilerDownloadSize
    }
    toolchain = @{
        name = $toolchainAsset.name
        url = $toolchainAsset.url
        sha256 = (Get-Sha256Digest -Digest $toolchainAsset.digest)
        download_size = $toolchainDownloadSize
    }
}
