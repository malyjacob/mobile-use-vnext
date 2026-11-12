#Requires -Version 5.1
<#
.SYNOPSIS
    Verifies one build/release/manifest.json and every artifact it names.

.DESCRIPTION
    Checks the manifest format/version, cross-checks release_version,
    contract_version and the Android identity against release/versions.json and
    AndroidManifest.xml, recomputes every artifact SHA-256 and size, recomputes
    the MCP tool catalog fingerprint with scripts/toolcatalog, and refuses any
    manifest marked complete=false or missing a required artifact.

    Verification is a release gate: it also refuses a build tree with tracked
    changes, a manifest source_commit that is not the actual git HEAD (a commit
    prefix is accepted), and source_dirty=true. Run it from the committed clean
    tree that produced the artifacts; a development tree cannot pass.

    Exit codes: 0 = PASS, 1 = FAIL with a specific message.

.EXAMPLE
    ./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
#>
param(
    [Parameter(Mandatory = $true)]
    [string]$Manifest
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$versionsPath = Join-Path $repository 'release/versions.json'
$androidManifestPath = Join-Path $repository 'android-control/app/src/main/AndroidManifest.xml'
$androidStringsPath = Join-Path $repository 'android-control/app/src/main/res/values/strings_version.xml'
$expectedArtifacts = @('device-service', 'device-mcp', 'control.apk', 'u2.jar', 'applist.dex', 'service.sh')

function Fail {
    param([string]$Reason)
    throw [System.InvalidOperationException]::new($Reason)
}

function Get-Field {
    param($Object, [string]$Name)
    if ($null -eq $Object) { return $null }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

# The manifest is only valid for the commit it was built from. Read the actual
# HEAD and refuse tracked changes: a release verification must run from the
# committed clean tree. Untracked/ignored build output is not a source change.
function Get-GitState {
    Push-Location $repository
    try {
        $commit = (& git rev-parse HEAD).Trim()
        if ($LASTEXITCODE -ne 0 -or $commit -notmatch '^[0-9a-fA-F]{40}$') {
            Fail 'git rev-parse HEAD failed.'
        }
        $trackedChanges = @(& git status --porcelain --untracked-files=no)
        if ($LASTEXITCODE -ne 0) {
            Fail 'git status --porcelain failed.'
        }
    } finally {
        Pop-Location
    }
    return [pscustomobject]@{
        Commit         = $commit.ToLowerInvariant()
        TrackedChanges = $trackedChanges
    }
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

function Get-ToolCatalogFingerprint {
    $previous = @{
        GOEXPERIMENT = $env:GOEXPERIMENT
        GOOS         = $env:GOOS
        GOARCH       = $env:GOARCH
    }
    try {
        $env:GOEXPERIMENT = 'jsonv2'
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        Push-Location $repository
        try {
            $lines = @(& go run ./scripts/toolcatalog)
            $exitCode = $LASTEXITCODE
        } finally {
            Pop-Location
        }
    } finally {
        foreach ($name in $previous.Keys) {
            if ($null -eq $previous[$name]) {
                Remove-Item "Env:$name" -ErrorAction SilentlyContinue
            } else {
                Set-Item "Env:$name" $previous[$name]
            }
        }
    }
    if ($exitCode -ne 0) {
        Fail "Tool catalog command failed with exit code $exitCode."
    }
    if ($lines.Count -ne 1) {
        Fail "Tool catalog command printed $($lines.Count) lines; expected exactly one JSON object."
    }
    try {
        return ($lines[0] | ConvertFrom-Json)
    } catch {
        Fail 'Tool catalog command did not print valid JSON.'
    }
}

try {
    if ([System.IO.Path]::IsPathRooted($Manifest)) {
        $manifestPath = [System.IO.Path]::GetFullPath($Manifest)
    } else {
        $manifestPath = [System.IO.Path]::GetFullPath((Join-Path $repository $Manifest))
    }
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        Fail "Manifest is missing: $manifestPath"
    }
    try {
        $manifestObject = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    } catch {
        Fail "Manifest is not valid JSON: $manifestPath"
    }

    $format = [string](Get-Field $manifestObject 'format')
    if ($format -ne 'mobile-use-vnext-release') {
        Fail "Manifest format is '$format', expected 'mobile-use-vnext-release'."
    }
    $manifestVersion = Get-Field $manifestObject 'version'
    if ($null -eq $manifestVersion -or [int]$manifestVersion -ne 1) {
        Fail "Manifest version is '$manifestVersion', expected 1."
    }
    $complete = Get-Field $manifestObject 'complete'
    if ($null -ne $complete -and -not [bool]$complete) {
        Fail 'Manifest is marked complete=false (partial build, typically -SkipAndroid); refusing it. Run a full release and deploy that.'
    }

    if (-not (Test-Path -LiteralPath $versionsPath -PathType Leaf)) {
        Fail "Version source is missing: $versionsPath"
    }
    $versions = Get-Content -LiteralPath $versionsPath -Raw | ConvertFrom-Json
    $expectedRelease = [string]$versions.release_version
    $expectedContract = [string]$versions.contract_version
    $expectedVersionCode = [int]$versions.android_version_code

    $manifestRelease = [string](Get-Field $manifestObject 'release_version')
    if ($manifestRelease -ne $expectedRelease) {
        Fail "Manifest release_version '$manifestRelease' does not match release/versions.json '$expectedRelease'."
    }
    $manifestContract = [string](Get-Field $manifestObject 'contract_version')
    if ($manifestContract -ne $expectedContract) {
        Fail "Manifest contract_version '$manifestContract' does not match release/versions.json '$expectedContract'."
    }
    $manifestSourceCommit = ([string](Get-Field $manifestObject 'source_commit')).ToLowerInvariant()
    $manifestSourceDirty = Get-Field $manifestObject 'source_dirty'
    if ($manifestSourceCommit -eq '' -or $null -eq $manifestSourceDirty) {
        Fail 'Manifest has no source_commit/source_dirty.'
    }
    if ($manifestSourceCommit -notmatch '^[0-9a-f]{7,40}$') {
        Fail "Manifest source_commit is not a git commit id (7-40 lowercase hex characters): $manifestSourceCommit"
    }
    if ($manifestSourceDirty -isnot [bool]) {
        Fail "Manifest source_dirty is not a JSON boolean: $manifestSourceDirty"
    }
    if ([bool]$manifestSourceDirty) {
        Fail ("Manifest source_dirty=true (source_commit={0}); a release manifest must be built from a clean tree." -f
            $manifestSourceCommit)
    }
    $gitState = Get-GitState
    if ($gitState.TrackedChanges.Count -gt 0) {
        Fail ("Build tree has {0} tracked change(s); verification requires the clean committed tree that produced the artifacts." -f
            $gitState.TrackedChanges.Count)
    }
    if (-not $gitState.Commit.StartsWith($manifestSourceCommit, [System.StringComparison]::Ordinal)) {
        Fail ("Manifest source_commit '{0}' does not match the actual HEAD '{1}'." -f
            $manifestSourceCommit, $gitState.Commit)
    }

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
    $manifestVersionCode = [int]$versionCodeMatch.Groups[1].Value
    $manifestVersionName = Resolve-AndroidVersionName $versionNameMatch.Groups[1].Value
    if ($manifestVersionCode -ne $expectedVersionCode) {
        Fail "AndroidManifest versionCode $manifestVersionCode does not match release/versions.json android_version_code $expectedVersionCode."
    }
    if ($manifestVersionName -ne $expectedRelease) {
        Fail "Android versionName '$manifestVersionName' does not match release_version '$expectedRelease'."
    }

    $android = Get-Field $manifestObject 'android'
    $manifestPackage = [string](Get-Field $android 'package')
    $manifestAndroidVersionName = [string](Get-Field $android 'version_name')
    $manifestAndroidVersionCode = Get-Field $android 'version_code'
    if ($manifestPackage -ne $packageMatch.Groups[1].Value) {
        Fail "Manifest android.package '$manifestPackage' does not match AndroidManifest '$($packageMatch.Groups[1].Value)'."
    }
    if ($manifestAndroidVersionName -ne $manifestVersionName) {
        Fail "Manifest android.version_name '$manifestAndroidVersionName' does not match AndroidManifest '$manifestVersionName'."
    }
    if ($null -eq $manifestAndroidVersionCode -or [int]$manifestAndroidVersionCode -ne $manifestVersionCode) {
        Fail "Manifest android.version_code '$manifestAndroidVersionCode' does not match AndroidManifest $manifestVersionCode."
    }

    # ------------------------------------------------------------ artifacts
    $artifactDirectory = Split-Path -Parent $manifestPath
    $artifactDirectoryPrefix = $artifactDirectory.TrimEnd([System.IO.Path]::DirectorySeparatorChar) +
        [System.IO.Path]::DirectorySeparatorChar
    $artifactList = @(Get-Field $manifestObject 'artifacts')
    if ($artifactList.Count -eq 0) {
        Fail 'Manifest has no artifacts.'
    }
    $seen = @{}
    foreach ($artifact in $artifactList) {
        $name = [string](Get-Field $artifact 'name')
        $relativePath = [string](Get-Field $artifact 'path')
        if ($name -eq '') { Fail 'Manifest contains an artifact with an empty name.' }
        if ($relativePath -eq '') { Fail "Manifest artifact '$name' has an empty path." }
        if ($seen.ContainsKey($name)) { Fail "Manifest contains duplicate artifact '$name'." }
        $seen[$name] = $true

        if ([System.IO.Path]::IsPathRooted($relativePath)) {
            Fail "Manifest artifact '$name' path must be relative: $relativePath"
        }
        $artifactPath = [System.IO.Path]::GetFullPath((Join-Path $artifactDirectory $relativePath))
        if (-not $artifactPath.StartsWith($artifactDirectoryPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            Fail "Manifest artifact '$name' path escapes the manifest directory: $relativePath"
        }
        if (-not (Test-Path -LiteralPath $artifactPath -PathType Leaf)) {
            Fail "Manifest artifact '$name' is missing: $relativePath"
        }
        $expectedBytes = Get-Field $artifact 'bytes'
        if ($null -eq $expectedBytes) {
            Fail "Manifest artifact '$name' has no byte size."
        }
        $actualBytes = (Get-Item -LiteralPath $artifactPath).Length
        if ([long]$expectedBytes -ne [long]$actualBytes) {
            Fail "Manifest artifact '$name' size mismatch: manifest=$expectedBytes actual=$actualBytes."
        }
        $expectedHash = ([string](Get-Field $artifact 'sha256')).ToLowerInvariant()
        if ($expectedHash -notmatch '^[0-9a-f]{64}$') {
            Fail "Manifest artifact '$name' has an invalid SHA-256 value."
        }
        $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $artifactPath).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash) {
            Fail "Manifest artifact '$name' SHA-256 mismatch: manifest=$expectedHash actual=$actualHash."
        }
    }
    foreach ($name in $expectedArtifacts) {
        if (-not $seen.ContainsKey($name)) {
            Fail "Manifest is missing required artifact '$name'."
        }
    }

    # ------------------------------------------------------------ tool catalog
    $toolCatalog = Get-Field $manifestObject 'tool_catalog'
    $expectedCount = Get-Field $toolCatalog 'count'
    $expectedFingerprint = ([string](Get-Field $toolCatalog 'sha256')).ToLowerInvariant()
    if ($null -eq $expectedCount) {
        Fail 'Manifest has no tool_catalog.count.'
    }
    if ($expectedFingerprint -notmatch '^[0-9a-f]{64}$') {
        Fail 'Manifest tool_catalog.sha256 is not a lowercase SHA-256 hex value.'
    }
    $catalog = Get-ToolCatalogFingerprint
    $actualCount = [int](Get-Field $catalog 'count')
    $actualFingerprint = ([string](Get-Field $catalog 'sha256')).ToLowerInvariant()
    if ($actualCount -ne [int]$expectedCount) {
        Fail "Tool catalog count mismatch: manifest=$expectedCount actual=$actualCount."
    }
    if ($actualFingerprint -ne $expectedFingerprint) {
        Fail "Tool catalog fingerprint mismatch: manifest=$expectedFingerprint actual=$actualFingerprint."
    }

    Write-Output ("PASS manifest={0} release={1} contract={2} android={3}/{4} artifacts={5} tool_catalog={6}/{7} source_commit={8} source_dirty={9} head={10}" -f
        $manifestPath, $manifestRelease, $manifestContract, $manifestAndroidVersionName,
        $manifestAndroidVersionCode, $artifactList.Count, $actualCount, $actualFingerprint,
        $manifestSourceCommit, [bool]$manifestSourceDirty, $gitState.Commit)
    exit 0
} catch {
    Write-Output ('FAIL manifest verification: ' + $_.Exception.Message)
    exit 1
}
