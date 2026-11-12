#Requires -Version 5.1
<#
.SYNOPSIS
    Builds the Windows host tools and their manifest, or verifies an existing one.

.DESCRIPTION
    release/versions.json is the only release and contract source. The build
    path proves the generated version files are fresh with
    scripts/generate-version.ps1 -Check, then compiles build/device-mcp.exe and
    build/mcp-probe.exe for windows/amd64 with CGO_ENABLED=0 and
    GOEXPERIMENT=jsonv2 using `go build -trimpath -buildvcs=false` and the same
    ReleaseVersion -ldflags injection as the device build. It records the
    source identity and the tool catalog count/fingerprint exported from the
    same source tree in build/host-tools-manifest.json.

    Each executable is built into a private staging directory beside the final
    paths; only the two executables and the manifest are replaced. The build
    directory is never cleared recursively.

    -VerifyOnly performs no build and does not touch a device. It is the release
    gate: it validates the host-tools manifest format/target/release/contract/
    source/catalog identity, both executable sizes and SHA-256 values, requires
    the manifest and the current tree to be at HEAD with source_dirty=false,
    requires the complete phone release manifest build/release/manifest.json to
    be the companion of this release, and verifies the shared commit/contract/
    catalog correspondence. A missing or partial phone manifest, a non-companion
    release, and companion SKIP all fail; a failed verification never rewrites
    the manifest to make it match.

    The build path refuses a dirty source tree unless -AllowDirtySource is
    passed explicitly. That flag is development-only: the manifest records
    source_dirty=true and can never pass -VerifyOnly. The separate
    `device-mcp.exe -version` output is only a release label and does not
    replace any check.

    Exit codes: 0 = PASS, 1 = FAIL.

.EXAMPLE
    ./scripts/build-host-tools.ps1
    ./scripts/build-host-tools.ps1 -VerifyOnly
    ./scripts/build-host-tools.ps1 -AllowDirtySource
#>
param(
    [switch]$VerifyOnly,
    [switch]$AllowDirtySource
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$script:repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$script:buildRoot = [System.IO.Path]::GetFullPath((Join-Path $script:repository 'build'))
$script:manifestPath = Join-Path $script:buildRoot 'host-tools-manifest.json'
$script:manifestFormat = 'mobile-use-vnext-host-tools'
$script:manifestVersion = 1
$script:releaseVersion = ''
$script:contractVersion = ''
$script:requiredArtifacts = @('device-mcp', 'mcp-probe')

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

function Read-ReleaseSource {
    $versionsPath = Join-Path $script:repository 'release/versions.json'
    if (-not (Test-Path -LiteralPath $versionsPath -PathType Leaf)) {
        Fail "Version source is missing: $versionsPath"
    }
    try {
        $versions = Get-Content -LiteralPath $versionsPath -Raw | ConvertFrom-Json
    } catch {
        Fail "Version source is not valid JSON: $versionsPath"
    }
    $release = [string](Get-Field $versions 'release_version')
    $contract = [string](Get-Field $versions 'contract_version')
    if ($release -notmatch '^\d+\.\d+\.\d+$') {
        Fail "release_version must be MAJOR.MINOR.PATCH: $release"
    }
    if ($contract -notmatch '^[1-9][0-9]{0,2}$') {
        Fail "contract_version must be a positive integer: $contract"
    }
    $script:releaseVersion = $release
    $script:contractVersion = $contract
    return [pscustomobject]@{
        Release  = $release
        Contract = $contract
    }
}

# The catalog identity must come from the same source tree as the executables.
# scripts/toolcatalog is a pure projection of internal/mcp's fixed catalog: it
# reads no configuration, device or host state and only prints one JSON object.
function Get-ToolCatalogFingerprint {
    $previous = @{
        GOEXPERIMENT = $env:GOEXPERIMENT
        GOOS         = $env:GOOS
        GOARCH       = $env:GOARCH
        CGO_ENABLED  = $env:CGO_ENABLED
    }
    try {
        $env:GOEXPERIMENT = 'jsonv2'
        $env:CGO_ENABLED = '0'
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        Push-Location $script:repository
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
        $catalog = $lines[0] | ConvertFrom-Json
    } catch {
        Fail 'Tool catalog command did not print valid JSON.'
    }
    $count = [int](Get-Field $catalog 'count')
    $fingerprint = ([string](Get-Field $catalog 'sha256')).ToLowerInvariant()
    if ($count -lt 1) {
        Fail "Tool catalog count is not positive: $count"
    }
    if ($fingerprint -notmatch '^[0-9a-f]{64}$') {
        Fail 'Tool catalog command printed an invalid SHA-256 fingerprint.'
    }
    return [pscustomobject]@{
        Count  = $count
        SHA256 = $fingerprint
    }
}

# Source identity follows the release manifest convention: the actual git HEAD
# at build time and whether relevant tracked/untracked source differs. Build
# output and private .state files do not make a source tree dirty.
function Get-SourceState {
    Push-Location $script:repository
    try {
        $commit = (& git rev-parse HEAD).Trim()
        if ($LASTEXITCODE -ne 0 -or $commit -notmatch '^[0-9a-fA-F]{40}$') {
            Fail 'git rev-parse HEAD failed.'
        }
        $statusLines = @(& git status --porcelain)
        if ($LASTEXITCODE -ne 0) {
            Fail 'git status --porcelain failed.'
        }
    } finally {
        Pop-Location
    }
    $relevant = @($statusLines | Where-Object {
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
    return [pscustomobject]@{
        Commit = $commit.ToLowerInvariant()
        Dirty  = ($relevant.Count -gt 0)
    }
}

function Get-ArtifactRecord {
    param([string]$Name, [string]$Path, [string]$RelativePath)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        Fail "Expected host tool is missing: $Path"
    }
    $item = Get-Item -LiteralPath $Path
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
    return [ordered]@{
        name   = $Name
        path   = $RelativePath
        sha256 = $hash
        bytes  = [long]$item.Length
    }
}

# Replace exactly one staged file. The staging file lives on the same volume as
# the destination so File.Replace is atomic per path; no build directory sweep
# is performed here.
function Publish-StagedFile {
    param([string]$StagedPath, [string]$DestinationPath)
    $parent = Split-Path -Parent $DestinationPath
    if (-not (Test-Path -LiteralPath $parent -PathType Container)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
    if (Test-Path -LiteralPath $DestinationPath -PathType Leaf) {
        $backup = Join-Path $parent ('.' + [System.IO.Path]::GetFileName($DestinationPath) +
            '.previous-' + ([guid]::NewGuid().ToString('N')))
        [System.IO.File]::Replace($StagedPath, $DestinationPath, $backup, $true)
        if (Test-Path -LiteralPath $backup) {
            # A process may still be executing the previous image. The
            # replacement itself is already complete; deleting the backup is
            # best effort and must not turn a successful publish into a failure.
            try {
                Remove-Item -LiteralPath $backup -Force
            } catch {
                Write-Warning ("previous image is still in use; kept as backup " + $backup)
            }
        }
    } else {
        [System.IO.File]::Move($StagedPath, $DestinationPath)
    }
}

function Invoke-HostBuild {
    $source = Read-ReleaseSource
    & (Join-Path $PSScriptRoot 'generate-version.ps1') -Check
    if ($LASTEXITCODE -ne 0) {
        Fail 'Generated version files are stale; run scripts/generate-version.ps1 and commit the result.'
    }

    $catalog = Get-ToolCatalogFingerprint
    $gitState = Get-SourceState
    if ($gitState.Dirty -and -not $AllowDirtySource) {
        Fail (("Source tree is dirty (source_commit={0}); commit the source before building a release manifest, " +
            'or pass -AllowDirtySource for a development-only build. A -AllowDirtySource manifest records ' +
            'source_dirty=true and cannot pass -VerifyOnly.') -f $gitState.Commit)
    }

    New-Item -ItemType Directory -Path $script:buildRoot -Force | Out-Null
    $staging = Join-Path $script:buildRoot ('.host-tools-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $staging -Force | Out-Null

    $previous = @{
        GOEXPERIMENT = $env:GOEXPERIMENT
        GOOS         = $env:GOOS
        GOARCH       = $env:GOARCH
        CGO_ENABLED  = $env:CGO_ENABLED
    }
    try {
        $env:GOEXPERIMENT = 'jsonv2'
        $env:GOOS = 'windows'
        $env:GOARCH = 'amd64'
        $env:CGO_ENABLED = '0'
        $ldflags = '-s -w -X github.com/malyjacob/mobile-use-vnext/internal/protocol.ReleaseVersion=' + $source.Release
        Push-Location $script:repository
        try {
            $targets = @(
                @{ Name = 'device-mcp'; Package = './cmd/device-mcp'; File = 'device-mcp.exe' },
                @{ Name = 'mcp-probe'; Package = './scripts/mcp-probe'; File = 'mcp-probe.exe' }
            )
            foreach ($target in $targets) {
                $outputPath = Join-Path $staging $target.File
                & go build -trimpath -buildvcs=false -ldflags $ldflags -o $outputPath $target.Package
                if ($LASTEXITCODE -ne 0) {
                    Fail "go build exited with code $LASTEXITCODE for $($target.Package)"
                }
            }
        } finally {
            Pop-Location
        }

        # The injected label is a cheap cross-check that the same version source
        # reached the adapter build; it is not a substitute for the manifest.
        $versionOutput = @(& (Join-Path $staging 'device-mcp.exe') '-version')
        if ($LASTEXITCODE -ne 0 -or (($versionOutput -join '').Trim() -ne $source.Release)) {
            Fail 'device-mcp.exe -version did not report the release/versions.json release version.'
        }

        $artifacts = @(
            (Get-ArtifactRecord -Name 'device-mcp' -Path (Join-Path $staging 'device-mcp.exe') -RelativePath 'device-mcp.exe'),
            (Get-ArtifactRecord -Name 'mcp-probe' -Path (Join-Path $staging 'mcp-probe.exe') -RelativePath 'mcp-probe.exe')
        )
        $builtAt = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
        $manifest = [ordered]@{
            format           = $script:manifestFormat
            version          = $script:manifestVersion
            release_version  = $source.Release
            contract_version = [int]$source.Contract
            source_commit    = $gitState.Commit
            source_dirty     = $gitState.Dirty
            built_at         = $builtAt
            go_version       = ((& go env GOVERSION) | Out-String).Trim()
            go_experiment    = 'jsonv2'
            build_target     = [ordered]@{
                goos        = 'windows'
                goarch      = 'amd64'
                cgo_enabled = $false
            }
            tool_catalog     = [ordered]@{
                count  = $catalog.Count
                sha256 = $catalog.SHA256
            }
            artifacts        = $artifacts
        }
        $stagedManifest = Join-Path $staging 'host-tools-manifest.json'
        $encoding = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText($stagedManifest,
            (($manifest | ConvertTo-Json -Depth 8).TrimEnd() + "`n"), $encoding)

        Publish-StagedFile -StagedPath (Join-Path $staging 'device-mcp.exe') `
            -DestinationPath (Join-Path $script:buildRoot 'device-mcp.exe')
        Publish-StagedFile -StagedPath (Join-Path $staging 'mcp-probe.exe') `
            -DestinationPath (Join-Path $script:buildRoot 'mcp-probe.exe')
        Publish-StagedFile -StagedPath $stagedManifest -DestinationPath $script:manifestPath

        Write-Output "host tools manifest: $script:manifestPath"
        Write-Output ("release={0} contract={1} source_commit={2} source_dirty={3}" -f
            $source.Release, $source.Contract, $gitState.Commit, $gitState.Dirty)
        Write-Output ("tool catalog: count={0} sha256={1}" -f $catalog.Count, $catalog.SHA256)
        foreach ($artifact in $artifacts) {
            Write-Output ("artifact {0} sha256={1} bytes={2}" -f
                $artifact.name, $artifact.sha256, $artifact.bytes)
        }
        if ($gitState.Dirty) {
            Write-Output 'RESULT PASS (development build: -AllowDirtySource, source_dirty=true, not releasable; cannot pass -VerifyOnly)'
        } else {
            Write-Output 'RESULT PASS'
        }
    } finally {
        foreach ($name in $previous.Keys) {
            if ($null -eq $previous[$name]) {
                Remove-Item "Env:$name" -ErrorAction SilentlyContinue
            } else {
                Set-Item "Env:$name" $previous[$name]
            }
        }
        if (Test-Path -LiteralPath $staging) {
            Remove-Item -LiteralPath $staging -Recurse -Force
        }
    }
}

function Test-PhoneManifestCompanion {
    param($HostManifest, $HostSource)
    $phoneManifestPath = Join-Path $script:buildRoot 'release/manifest.json'
    if (-not (Test-Path -LiteralPath $phoneManifestPath -PathType Leaf)) {
        Fail (("Phone release manifest is missing: {0}. Build the complete phone release first " +
            '(scripts/build-release.ps1 without -SkipAndroid); a host-only check is not a release gate.') -f
            $phoneManifestPath)
    }
    try {
        $phone = Get-Content -LiteralPath $phoneManifestPath -Raw | ConvertFrom-Json
    } catch {
        Fail 'Phone release manifest is not valid JSON.'
    }
    $phoneFormat = [string](Get-Field $phone 'format')
    if ($phoneFormat -ne 'mobile-use-vnext-release') {
        Fail "Phone release manifest format is '$phoneFormat', expected 'mobile-use-vnext-release'."
    }
    $phoneComplete = Get-Field $phone 'complete'
    if ($null -ne $phoneComplete -and $phoneComplete -isnot [bool]) {
        Fail "Phone release manifest complete field is not a JSON boolean: $phoneComplete"
    }
    if ($null -ne $phoneComplete -and -not [bool]$phoneComplete) {
        Fail 'Phone release manifest is marked complete=false (partial -SkipAndroid build); a complete phone release is required.'
    }
    $phoneSourceDirty = Get-Field $phone 'source_dirty'
    if ($phoneSourceDirty -isnot [bool]) {
        Fail 'Phone release manifest has no JSON boolean source_dirty.'
    }
    if ([bool]$phoneSourceDirty) {
        Fail 'Phone release manifest has source_dirty=true; rebuild the phone release from a clean committed tree.'
    }
    $phoneArtifacts = @(Get-Field $phone 'artifacts')
    $phoneArtifactNames = @{}
    foreach ($artifact in $phoneArtifacts) {
        $name = [string](Get-Field $artifact 'name')
        if ($name -ne '') {
            $phoneArtifactNames[$name] = $true
        }
    }
    foreach ($name in @('device-service', 'device-mcp', 'control.apk', 'u2.jar', 'applist.dex', 'service.sh')) {
        if (-not $phoneArtifactNames.ContainsKey($name)) {
            Fail "Phone release manifest is missing required artifact '$name'; the phone release is incomplete."
        }
    }
    $phoneRelease = [string](Get-Field $phone 'release_version')
    $phoneContract = [string](Get-Field $phone 'contract_version')
    if ($phoneRelease -notmatch '^\d+\.\d+\.\d+$') {
        Fail "Phone release manifest release_version is invalid: '$phoneRelease'."
    }
    if ($phoneContract -notmatch '^[1-9][0-9]{0,2}$') {
        Fail "Phone release manifest contract_version is invalid: '$phoneContract'."
    }
    $hostRelease = [string](Get-Field $HostManifest 'release_version')
    $hostContract = [string](Get-Field $HostManifest 'contract_version')
    if ($phoneRelease -ne $hostRelease -or $phoneContract -ne $hostContract) {
        Fail ("Phone release manifest is not a companion of this host release: phone release=$phoneRelease " +
            "contract=$phoneContract; host release=$hostRelease contract=$hostContract.")
    }
    $phoneCommit = ([string](Get-Field $phone 'source_commit')).ToLowerInvariant()
    if ($phoneCommit -notmatch '^[0-9a-f]{7,40}$') {
        Fail "Phone release manifest source_commit is not a git commit id: $phoneCommit"
    }
    $hostCommit = [string](Get-Field $HostSource 'Commit')
    if (-not $hostCommit.StartsWith($phoneCommit, [System.StringComparison]::Ordinal)) {
        Fail ("Phone release manifest source_commit '$phoneCommit' does not match host-tools source_commit '" +
            $hostCommit + "'.")
    }
    $phoneCatalog = Get-Field $phone 'tool_catalog'
    $phoneCount = [int](Get-Field $phoneCatalog 'count')
    $phoneHash = ([string](Get-Field $phoneCatalog 'sha256')).ToLowerInvariant()
    $hostCatalog = Get-Field $HostManifest 'tool_catalog'
    if ($phoneCount -ne [int](Get-Field $hostCatalog 'count')) {
        Fail ("Phone release manifest tool_catalog.count $phoneCount does not match host-tools count " +
            [string](Get-Field $hostCatalog 'count') + '.')
    }
    if ($phoneHash -ne ([string](Get-Field $hostCatalog 'sha256')).ToLowerInvariant()) {
        Fail 'Phone release manifest tool_catalog.sha256 does not match the host-tools fingerprint.'
    }
    Write-Output ("phone release manifest: PASS companion release={0} contract={1} source_commit={2} source_dirty=false tool_catalog={3}/{4}" -f
        $phoneRelease, $phoneContract, $phoneCommit, $phoneCount, $phoneHash)
}

function Test-HostManifest {
    $source = Read-ReleaseSource
    & (Join-Path $PSScriptRoot 'generate-version.ps1') -Check
    if ($LASTEXITCODE -ne 0) {
        Fail 'Generated version files are stale; run scripts/generate-version.ps1 and commit the result.'
    }
    $catalog = Get-ToolCatalogFingerprint
    $gitState = Get-SourceState

    if (-not (Test-Path -LiteralPath $script:manifestPath -PathType Leaf)) {
        Fail "Host tools manifest is missing: $script:manifestPath"
    }
    try {
        $manifest = Get-Content -LiteralPath $script:manifestPath -Raw | ConvertFrom-Json
    } catch {
        Fail "Host tools manifest is not valid JSON: $script:manifestPath"
    }

    $format = [string](Get-Field $manifest 'format')
    if ($format -ne $script:manifestFormat) {
        Fail "Host tools manifest format is '$format', expected '$($script:manifestFormat)'."
    }
    $manifestVersion = Get-Field $manifest 'version'
    if ($null -eq $manifestVersion -or [int]$manifestVersion -ne $script:manifestVersion) {
        Fail "Host tools manifest version is '$manifestVersion', expected $($script:manifestVersion)."
    }
    $manifestRelease = [string](Get-Field $manifest 'release_version')
    if ($manifestRelease -ne $source.Release) {
        Fail "Host tools manifest release_version '$manifestRelease' does not match release/versions.json '$($source.Release)'."
    }
    $manifestContract = [string](Get-Field $manifest 'contract_version')
    if ($manifestContract -ne $source.Contract) {
        Fail "Host tools manifest contract_version '$manifestContract' does not match release/versions.json '$($source.Contract)'."
    }

    $target = Get-Field $manifest 'build_target'
    $goos = [string](Get-Field $target 'goos')
    $goarch = [string](Get-Field $target 'goarch')
    $cgo = Get-Field $target 'cgo_enabled'
    if ($goos -ne 'windows' -or $goarch -ne 'amd64' -or $null -eq $cgo -or [bool]$cgo) {
        Fail ("Host tools manifest build target is not windows/amd64 with CGO disabled: goos='$goos' goarch='$goarch' cgo='$cgo'.")
    }

    $manifestCommit = ([string](Get-Field $manifest 'source_commit')).ToLowerInvariant()
    if ($manifestCommit -notmatch '^[0-9a-f]{40}$') {
        Fail "Host tools manifest source_commit is not a 40-character commit id: $manifestCommit"
    }
    if ($manifestCommit -ne $gitState.Commit) {
        Fail ("Host tools manifest source_commit '$manifestCommit' does not match the current HEAD '$($gitState.Commit)'.")
    }
    $manifestDirty = Get-Field $manifest 'source_dirty'
    if ($manifestDirty -isnot [bool]) {
        Fail 'Host tools manifest has no JSON boolean source_dirty.'
    }
    if ([bool]$manifestDirty) {
        Fail ("Host tools manifest source_dirty=true (source_commit={0}); rebuild from a clean committed tree." -f
            $manifestCommit)
    }
    if ($gitState.Dirty) {
        Fail (("Current source tree is dirty (HEAD {0}); -VerifyOnly requires the clean committed tree that " +
            'produced the manifest. Commit or restore the changes instead of using a development tree for verification.') -f
            $gitState.Commit)
    }

    $manifestCatalog = Get-Field $manifest 'tool_catalog'
    $manifestCount = [int](Get-Field $manifestCatalog 'count')
    $manifestHash = ([string](Get-Field $manifestCatalog 'sha256')).ToLowerInvariant()
    if ($manifestCount -ne $catalog.Count) {
        Fail "Host tools manifest tool_catalog.count $manifestCount does not match the source catalog $($catalog.Count)."
    }
    if ($manifestHash -ne $catalog.SHA256) {
        Fail "Host tools manifest tool_catalog.sha256 $manifestHash does not match the source fingerprint $($catalog.SHA256)."
    }

    $artifactList = @(Get-Field $manifest 'artifacts')
    if ($artifactList.Count -ne $script:requiredArtifacts.Count) {
        Fail "Host tools manifest has $($artifactList.Count) artifacts; expected $($script:requiredArtifacts.Count)."
    }
    $seen = @{}
    $manifestDirectory = Split-Path -Parent $script:manifestPath
    $manifestDirectoryPrefix = $manifestDirectory.TrimEnd([System.IO.Path]::DirectorySeparatorChar) +
        [System.IO.Path]::DirectorySeparatorChar
    foreach ($artifact in $artifactList) {
        $name = [string](Get-Field $artifact 'name')
        $relativePath = [string](Get-Field $artifact 'path')
        if ($name -eq '') { Fail 'Host tools manifest contains an artifact with an empty name.' }
        if ($relativePath -eq '') { Fail "Host tools manifest artifact '$name' has an empty path." }
        if ($seen.ContainsKey($name)) { Fail "Host tools manifest contains duplicate artifact '$name'." }
        $seen[$name] = $true
        if ([System.IO.Path]::IsPathRooted($relativePath)) {
            Fail "Host tools manifest artifact '$name' path must be relative: $relativePath"
        }
        $artifactPath = [System.IO.Path]::GetFullPath((Join-Path $manifestDirectory $relativePath))
        if (-not $artifactPath.StartsWith($manifestDirectoryPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            Fail "Host tools manifest artifact '$name' path escapes the manifest directory: $relativePath"
        }
        if (-not (Test-Path -LiteralPath $artifactPath -PathType Leaf)) {
            Fail "Host tools manifest artifact '$name' is missing: $relativePath"
        }
        $expectedBytes = Get-Field $artifact 'bytes'
        if ($null -eq $expectedBytes) {
            Fail "Host tools manifest artifact '$name' has no byte size."
        }
        $actualBytes = (Get-Item -LiteralPath $artifactPath).Length
        if ([long]$expectedBytes -ne [long]$actualBytes) {
            Fail "Host tools manifest artifact '$name' size mismatch: manifest=$expectedBytes actual=$actualBytes."
        }
        $expectedHash = ([string](Get-Field $artifact 'sha256')).ToLowerInvariant()
        if ($expectedHash -notmatch '^[0-9a-f]{64}$') {
            Fail "Host tools manifest artifact '$name' has an invalid SHA-256 value."
        }
        $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $artifactPath).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash) {
            Fail "Host tools manifest artifact '$name' SHA-256 mismatch: manifest=$expectedHash actual=$actualHash."
        }
    }
    foreach ($name in $script:requiredArtifacts) {
        if (-not $seen.ContainsKey($name)) {
            Fail "Host tools manifest is missing required artifact '$name'."
        }
    }

    Test-PhoneManifestCompanion -HostManifest $manifest -HostSource $gitState

    Write-Output ("PASS host-tools manifest={0} release={1} contract={2} source_commit={3} source_dirty={4} tool_catalog={5}/{6} artifacts={7}" -f
        $script:manifestPath, $manifestRelease, $manifestContract, $manifestCommit,
        [bool]$manifestDirty, $manifestCount, $manifestHash, $artifactList.Count)
}

try {
    if ($VerifyOnly -and $AllowDirtySource) {
        Fail '-AllowDirtySource is only valid when building; -VerifyOnly always requires a clean committed tree.'
    }
    if ($VerifyOnly) {
        Test-HostManifest
    } else {
        Invoke-HostBuild
    }
    exit 0
} catch {
    Write-Output ('FAIL host tools: ' + $_.Exception.Message)
    exit 1
}
