#Requires -Version 5.1
<#
.SYNOPSIS
    Builds the Go services and (unless -SkipAndroid is used) the control APK,
    then assembles the release directory and writes manifest.json.

.DESCRIPTION
    release/versions.json is the only version source. This script first proves
    the generated version files are fresh, cleans only the resolved output
    directory beneath build/, builds Go for linux/arm64 with CGO disabled,
    optionally builds the control APK, copies the fixed artifacts, and records
    SHA-256 hashes, sizes, tool catalog fingerprint and source state in
    build/release/manifest.json.

    -SkipAndroid is development-only. It produces a manifest with
    "complete": false and no control.apk. Such a directory must never be
    deployed; scripts/verify-manifest.ps1 refuses it and scripts/deploy.ps1
    refuses it as well.

.EXAMPLE
    ./scripts/build-release.ps1
    ./scripts/build-release.ps1 -SkipAndroid
#>
param(
    [string]$Output = 'build/release',
    [switch]$SkipAndroid
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$versionsPath = Join-Path $repository 'release/versions.json'
$androidManifestPath = Join-Path $repository 'android-control/app/src/main/AndroidManifest.xml'
$androidStringsPath = Join-Path $repository 'android-control/app/src/main/res/values/strings_version.xml'
$androidApkPath = Join-Path $repository 'android-control/build/control.apk'
$sdkModule = 'github.com/modelcontextprotocol/go-sdk v1.8.0'
$mcpProtocol = '2026-07-28'

function Fail {
    param([string]$Reason)
    throw [System.InvalidOperationException]::new($Reason)
}

function Resolve-RepositoryPath {
    param([string]$Path)
    if ([System.IO.Path]::IsPathRooted($Path)) {
        return [System.IO.Path]::GetFullPath($Path)
    }
    return [System.IO.Path]::GetFullPath((Join-Path $repository $Path))
}

function Resolve-AndroidVersionName {
    param([string]$Value)
    if ($Value -match '^@string/([A-Za-z0-9_]+)$') {
        $resourceName = $Matches[1]
        if (-not (Test-Path -LiteralPath $androidStringsPath -PathType Leaf)) {
            Fail "Android string resource file is missing: $androidStringsPath"
        }
        $resourceText = [System.IO.File]::ReadAllText($androidStringsPath)
        $resourceMatch = [regex]::Match($resourceText,
            '<string name="' + [regex]::Escape($resourceName) + '">([^<]*)</string>')
        if (-not $resourceMatch.Success) {
            Fail "AndroidManifest versionName refers to missing string resource '$resourceName'."
        }
        return $resourceMatch.Groups[1].Value
    }
    return $Value
}

# ---------------------------------------------------------------- version source
if (-not (Test-Path -LiteralPath $versionsPath -PathType Leaf)) {
    Fail "Version source is missing: $versionsPath"
}
$versions = Get-Content -LiteralPath $versionsPath -Raw | ConvertFrom-Json
$releaseVersion = [string]$versions.release_version
$contractVersion = [int]$versions.contract_version
$androidVersionCode = [int]$versions.android_version_code
if ($releaseVersion -notmatch '^\d+\.\d+\.\d+$') {
    Fail "release_version must be MAJOR.MINOR.PATCH: $releaseVersion"
}
if ($contractVersion -lt 1) {
    Fail "contract_version must be a positive integer: $contractVersion"
}
if ($androidVersionCode -lt 1) {
    Fail "android_version_code must be a positive integer: $androidVersionCode"
}

# ---------------------------------------------------------------- output guard
$outputDirectory = Resolve-RepositoryPath $Output
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $repository 'build'))
$separator = [System.IO.Path]::DirectorySeparatorChar
$buildRootPrefix = $buildRoot.TrimEnd($separator) + $separator
if (-not $outputDirectory.StartsWith($buildRootPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
    Fail "Refusing to clean a release directory outside the repository build directory: $outputDirectory"
}
if (Test-Path -LiteralPath $outputDirectory) {
    Remove-Item -LiteralPath $outputDirectory -Recurse -Force
}
New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
$manifestPath = Join-Path $outputDirectory 'manifest.json'

$previousEnvironment = @{
    GOEXPERIMENT = $env:GOEXPERIMENT
    GOOS         = $env:GOOS
    GOARCH       = $env:GOARCH
}
try {
    $env:GOEXPERIMENT = 'jsonv2'
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

    # A stale generated version file would make the binary disagree with the
    # manifest, so prove freshness before anything is built.
    & (Join-Path $PSScriptRoot 'generate-version.ps1') -Check
    if ($LASTEXITCODE -ne 0) {
        Fail 'Generated version files are stale; run scripts/generate-version.ps1 and commit the result.'
    }

    & (Join-Path $PSScriptRoot 'build.ps1') -Output $outputDirectory -Version $releaseVersion
    if ($LASTEXITCODE -ne 0) {
        Fail 'Go build failed.'
    }

    if (-not $SkipAndroid) {
        & (Join-Path $repository 'android-control/build.ps1')
        if ($LASTEXITCODE -ne 0) {
            Fail 'Android build failed.'
        }
        if (-not (Test-Path -LiteralPath $androidApkPath -PathType Leaf)) {
            Fail "Android build reported success but did not produce: $androidApkPath"
        }
        Copy-Item -LiteralPath $androidApkPath -Destination (Join-Path $outputDirectory 'control.apk') -Force
    } else {
        Write-Warning 'Android build skipped: this release is incomplete and must not be deployed.'
    }

    foreach ($artifact in @(
            @{ Name = 'u2.jar'; Source = 'third_party/uiautomator/u2.jar' },
            @{ Name = 'applist.dex'; Source = 'third_party/android-applist/applist.dex' },
            @{ Name = 'service.sh'; Source = 'scripts/service.sh' })) {
        $sourcePath = Join-Path $repository $artifact.Source
        if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
            Fail "Missing fixed artifact: $sourcePath"
        }
        Copy-Item -LiteralPath $sourcePath -Destination (Join-Path $outputDirectory $artifact.Name) -Force
    }

    # ------------------------------------------------------------ artifacts
    $artifactNames = @('device-service', 'device-mcp')
    if (-not $SkipAndroid) {
        $artifactNames += 'control.apk'
    }
    $artifactNames += @('u2.jar', 'applist.dex', 'service.sh')

    $artifacts = @()
    foreach ($name in $artifactNames) {
        $artifactPath = Join-Path $outputDirectory $name
        if (-not (Test-Path -LiteralPath $artifactPath -PathType Leaf)) {
            Fail "Expected release artifact is missing: $artifactPath"
        }
        $item = Get-Item -LiteralPath $artifactPath
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $artifactPath).Hash.ToLowerInvariant()
        $artifacts += [ordered]@{
            name   = $name
            path   = $name
            sha256 = $hash
            bytes  = [long]$item.Length
        }
    }

    # ------------------------------------------------------------ go / catalog / source
    Push-Location $repository
    try {
        $goVersion = (& go env GOVERSION).Trim()
        if ($LASTEXITCODE -ne 0 -or $goVersion -eq '') {
            Fail 'go env GOVERSION failed.'
        }
        $goExperiment = (& go env GOEXPERIMENT).Trim()
        if ($LASTEXITCODE -ne 0) {
            Fail 'go env GOEXPERIMENT failed.'
        }
        $catalogText = (@(& go run ./scripts/toolcatalog) -join "`n")
        if ($LASTEXITCODE -ne 0) {
            Fail 'Tool catalog command failed.'
        }
        $sourceCommit = (& git rev-parse HEAD).Trim()
        if ($LASTEXITCODE -ne 0 -or $sourceCommit -notmatch '^[0-9a-fA-F]{40}$') {
            Fail 'git rev-parse HEAD failed.'
        }
        $statusLines = @(& git status --porcelain)
        if ($LASTEXITCODE -ne 0) {
            Fail 'git status --porcelain failed.'
        }
    } finally {
        Pop-Location
    }

    try {
        $catalog = $catalogText | ConvertFrom-Json
    } catch {
        Fail 'Tool catalog command did not print valid JSON.'
    }
    $catalogCount = [int]$catalog.count
    $catalogHash = [string]$catalog.sha256
    if ($catalogCount -lt 1 -or $catalogHash -notmatch '^[0-9a-f]{64}$') {
        Fail 'Tool catalog command printed an unexpected fingerprint.'
    }

    $relevantChanges = @($statusLines | Where-Object {
            $line = [string]$_
            if ($line.Length -lt 4) { return $false }
            $pathText = $line.Substring(3).Trim()
            if ($pathText.Contains(' -> ')) {
                $pathText = $pathText.Substring($pathText.LastIndexOf(' -> ') + 4)
            }
            $pathText = $pathText.Trim('"')
            return -not ($pathText -eq 'build' -or $pathText.StartsWith('build/') -or
                $pathText -eq '.state' -or $pathText.StartsWith('.state/'))
        })
    $sourceDirty = ($relevantChanges.Count -gt 0)

    # ------------------------------------------------------------ android identity
    if (-not (Test-Path -LiteralPath $androidManifestPath -PathType Leaf)) {
        Fail "AndroidManifest is missing: $androidManifestPath"
    }
    $androidManifest = [System.IO.File]::ReadAllText($androidManifestPath)
    $packageMatch = [regex]::Match($androidManifest, 'package="([^"]+)"')
    $versionCodeMatch = [regex]::Match($androidManifest, 'android:versionCode="(\d+)"')
    $versionNameMatch = [regex]::Match($androidManifest, 'android:versionName="([^"]+)"')
    if (-not $packageMatch.Success) { Fail 'AndroidManifest has no package attribute.' }
    if (-not $versionCodeMatch.Success) { Fail 'AndroidManifest has no android:versionCode attribute.' }
    if (-not $versionNameMatch.Success) { Fail 'AndroidManifest has no android:versionName attribute.' }
    $androidPackage = $packageMatch.Groups[1].Value
    $manifestVersionCode = [int]$versionCodeMatch.Groups[1].Value
    $androidVersionName = Resolve-AndroidVersionName $versionNameMatch.Groups[1].Value
    if ($manifestVersionCode -ne $androidVersionCode) {
        Fail "AndroidManifest versionCode $manifestVersionCode does not match release/versions.json android_version_code $androidVersionCode."
    }
    if ($androidVersionName -ne $releaseVersion) {
        Fail "Android versionName '$androidVersionName' does not match release_version $releaseVersion."
    }

    # ------------------------------------------------------------ manifest
    $builtAt = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    $manifest = [ordered]@{
        format           = 'mobile-use-vnext-release'
        version          = 1
        release_version  = $releaseVersion
        contract_version = $contractVersion
        source_commit    = $sourceCommit.ToLowerInvariant()
        source_dirty     = $sourceDirty
        built_at         = $builtAt
        go_version       = $goVersion
        go_experiment    = $goExperiment
        build_target     = [ordered]@{
            goos        = 'linux'
            goarch      = 'arm64'
            cgo_enabled = $false
        }
        mcp              = [ordered]@{
            sdk      = $sdkModule
            protocol = $mcpProtocol
        }
        android          = [ordered]@{
            package      = $androidPackage
            version_name = $androidVersionName
            version_code = $manifestVersionCode
        }
        tool_catalog     = [ordered]@{
            count  = $catalogCount
            sha256 = $catalogHash
        }
        artifacts        = $artifacts
    }
    if ($SkipAndroid) {
        $manifest['complete'] = $false
    }

    $json = $manifest | ConvertTo-Json -Depth 8
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($manifestPath, ($json.TrimEnd() + "`n"), $encoding)

    Write-Output "release manifest: $manifestPath"
    Write-Output ("release=$releaseVersion contract=$contractVersion android=$androidVersionCode source_commit=$($sourceCommit.ToLowerInvariant()) source_dirty=$sourceDirty")
    Write-Output ("tool catalog: count=$catalogCount sha256=$catalogHash")
    foreach ($artifact in $artifacts) {
        Write-Output ("artifact {0} sha256={1} bytes={2}" -f $artifact.name, $artifact.sha256, $artifact.bytes)
    }
    if ($SkipAndroid) {
        Write-Warning 'INCOMPLETE RELEASE: manifest has complete=false and no control.apk. Do not deploy it.'
    }
} finally {
    foreach ($name in $previousEnvironment.Keys) {
        if ($null -eq $previousEnvironment[$name]) {
            Remove-Item "Env:$name" -ErrorAction SilentlyContinue
        } else {
            Set-Item "Env:$name" $previousEnvironment[$name]
        }
    }
}
