param(
    [string]$Chroot = '/data/local/chroots/debian13',
    [string]$AppPort = 18880,
    [string]$HostPort = 18881,
    [string]$AgentName = 'local-agent',
    [string]$Manifest = 'build/release/manifest.json',
    [int]$PhoneMcpPort = 18882,
    [int]$PhoneMcpHostPort = 18883,
    [string[]]$McpAllowedOrigins = @(),
    [switch]$SkipPhoneMcp,
    [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$stateDirectory = Join-Path $repository '.state'
New-Item -ItemType Directory -Path $stateDirectory -Force | Out-Null
$deployStatePath = Join-Path $stateDirectory 'deploy.json'

function Write-Utf8NoBom {
    param([string]$Path, [string]$Text)
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Text, $encoding)
}

function Quote-Argument {
    param([string]$Value)
    if ($Value -match '[\\s"]') {
        return '"' + ($Value -replace '"', '\\"') + '"'
    }
    return $Value
}

function Get-Field {
    param($Object, [string]$Name)
    if ($null -eq $Object) { return $null }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

function Invoke-Adb {
    param([string[]]$Arguments, [switch]$AllowFailure)
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = (Get-Command adb).Source
    $startInfo.Arguments = (($Arguments | ForEach-Object { Quote-Argument $_ }) -join ' ')
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.CreateNoWindow = $true
    $process = [System.Diagnostics.Process]::Start($startInfo)
    try {
        $stdout = $process.StandardOutput.ReadToEnd()
        $stderr = $process.StandardError.ReadToEnd()
        $process.WaitForExit()
        if (-not $AllowFailure -and $process.ExitCode -ne 0) {
            throw "adb $($Arguments -join ' ') failed: $stderr $stdout"
        }
        return ($stdout + $stderr).Trim()
    } finally {
        $process.Dispose()
    }
}

# Device shell scripts are sent over stdin. Windows argv quoting must not
# reinterpret the device shell's inner quotes.
function Invoke-DeviceScript {
    param([string]$Script)
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = (Get-Command adb).Source
    $startInfo.Arguments = 'shell sh'
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = [System.Diagnostics.Process]::Start($startInfo)
    try {
        $process.StandardInput.Write($Script.Replace("`r", '') + "`n")
        $process.StandardInput.Close()
        $stdout = $process.StandardOutput.ReadToEnd()
        $stderr = $process.StandardError.ReadToEnd()
        $process.WaitForExit()
        if ($process.ExitCode -ne 0) {
            throw "device script failed ($($process.ExitCode)): $stderr $stdout"
        }
        return $stdout.Trim()
    } finally {
        $process.Dispose()
    }
}

function Invoke-DeviceScriptAllowFailure {
    param([string]$Script)
    try { return Invoke-DeviceScript -Script $Script } catch { return '' }
}

# Read one SHA-256 from the device without printing file content. Returns the
# lowercase hex string, or '' when the file cannot be read.
function Get-DeviceFileSha256 {
    param([string]$Path)
    $output = Invoke-DeviceScriptAllowFailure -Script "su -c 'sha256sum $Path 2>/dev/null'"
    $match = [regex]::Match($output, '(?m)^([0-9a-fA-F]{64})\s')
    if (-not $match.Success) { return '' }
    return $match.Groups[1].Value.ToLowerInvariant()
}

# Fail the release when a deployed artifact does not match the verified
# manifest. This is the post-copy proof that a silent cp/mv failure cannot be
# reported as a successful deployment.
function Assert-DeviceFileSha256 {
    param([string]$Name, [string]$Path, [string]$Expected)
    $expectedHash = $Expected.ToLowerInvariant()
    if ($expectedHash -notmatch '^[0-9a-f]{64}$') {
        throw "Manifest has no valid SHA-256 for deployed artifact '$Name'."
    }
    $actualHash = Get-DeviceFileSha256 -Path $Path
    if ($actualHash -ne $expectedHash) {
        throw ("Deployed artifact hash mismatch for {0}: expected {1}, actual {2} ({3})." -f
            $Name, $expectedHash, $actualHash, $Path)
    }
    Write-Output ("deployed artifact: {0} sha256={1} (match)" -f $Name, $actualHash)
}

function Get-ManifestArtifactPath {
    param($ManifestObject, [string]$Root, [string]$Name)
    foreach ($artifact in @(Get-Field $ManifestObject 'artifacts')) {
        if ([string](Get-Field $artifact 'name') -eq $Name) {
            $relativePath = [string](Get-Field $artifact 'path')
            return [System.IO.Path]::GetFullPath((Join-Path $Root $relativePath))
        }
    }
    throw "Manifest has no artifact named '$Name'."
}

# Read the installed package identity back from the device. A successful
# `adb install -r` only proves the package manager accepted the APK; the
# versionName/versionCode must also match the signed release manifest.
function Get-InstalledAppIdentity {
    param([string]$Package)
    $dump = Invoke-Adb -Arguments @('shell', 'dumpsys', 'package', $Package)
    $packageMatch = [regex]::Match($dump, 'Package \[([^\]]+)\]')
    $versionCodeMatch = [regex]::Match($dump, '(?m)^\s*versionCode=(\d+)')
    $versionNameMatch = [regex]::Match($dump, '(?m)^\s*versionName=([^\r\n]+)')
    if (-not $packageMatch.Success -or -not $versionCodeMatch.Success -or -not $versionNameMatch.Success) {
        throw "Could not read the installed App identity from dumpsys package $Package."
    }
    return [pscustomobject]@{
        Package     = $packageMatch.Groups[1].Value
        VersionName = $versionNameMatch.Groups[1].Value.Trim()
        VersionCode = [int]$versionCodeMatch.Groups[1].Value
    }
}

# ---------------------------------------------------------------- release manifest
if ([System.IO.Path]::IsPathRooted($Manifest)) {
    $manifestPath = [System.IO.Path]::GetFullPath($Manifest)
} else {
    $manifestPath = [System.IO.Path]::GetFullPath((Join-Path $repository $Manifest))
}
$manifestDirectory = Split-Path -Parent $manifestPath

# ---------------------------------------------------------------- prerequisites
$devices = Invoke-Adb -Arguments @('devices')
if ($devices -notmatch '\tdevice') {
    throw 'No adb device is online.'
}

if (-not $SkipBuild) {
    if ([System.IO.Path]::GetFileName($manifestPath) -ne 'manifest.json') {
        throw "-Manifest must name manifest.json inside the release output when building (got '$manifestPath')."
    }
    & (Join-Path $PSScriptRoot 'build-release.ps1') -Output $manifestDirectory
    if ($LASTEXITCODE -ne 0) {
        throw 'Release build failed.'
    }
}

& (Join-Path $PSScriptRoot 'verify-manifest.ps1') -Manifest $manifestPath
if ($LASTEXITCODE -ne 0) {
    throw "Release manifest verification failed: $manifestPath"
}

if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    throw "Release manifest is missing: $manifestPath"
}
$manifestObject = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
$complete = Get-Field $manifestObject 'complete'
if ($null -ne $complete -and -not [bool]$complete) {
    throw 'Refusing an incomplete release manifest (complete=false). Build a full release first.'
}
$releaseVersion = [string](Get-Field $manifestObject 'release_version')
$contractVersion = [string](Get-Field $manifestObject 'contract_version')
if ($releaseVersion -eq '' -or $contractVersion -eq '') {
    throw 'Release manifest has no release_version/contract_version.'
}
$sourceCommit = [string](Get-Field $manifestObject 'source_commit')
$sourceDirty = [bool](Get-Field $manifestObject 'source_dirty')
$manifestAndroid = Get-Field $manifestObject 'android'
$expectedPackage = [string](Get-Field $manifestAndroid 'package')
$expectedVersionName = [string](Get-Field $manifestAndroid 'version_name')
$expectedVersionCode = [int](Get-Field $manifestAndroid 'version_code')
if ($expectedPackage -eq '' -or $expectedVersionName -eq '' -or $expectedVersionCode -lt 1) {
    throw 'Release manifest has an incomplete android identity.'
}

# The manifest hashes are the only accepted proof of what was deployed. Build
# the map once and reject a manifest that lacks a required digest.
$manifestHashes = @{}
foreach ($artifact in @(Get-Field $manifestObject 'artifacts')) {
    $artifactName = [string](Get-Field $artifact 'name')
    $artifactHash = [string](Get-Field $artifact 'sha256')
    if ($artifactName -ne '' -and $artifactHash -ne '') {
        $manifestHashes[$artifactName] = $artifactHash.ToLowerInvariant()
    }
}
foreach ($name in @('device-service', 'device-mcp', 'u2.jar', 'applist.dex', 'service.sh', 'control.apk')) {
    if ($manifestHashes[$name] -notmatch '^[0-9a-f]{64}$') {
        throw "Release manifest has no valid SHA-256 for required artifact '$name'."
    }
}

$serviceBinary = Get-ManifestArtifactPath $manifestObject $manifestDirectory 'device-service'
$mcpBinary = Get-ManifestArtifactPath $manifestObject $manifestDirectory 'device-mcp'
$u2Jar = Get-ManifestArtifactPath $manifestObject $manifestDirectory 'u2.jar'
$applistDex = Get-ManifestArtifactPath $manifestObject $manifestDirectory 'applist.dex'
$serviceScript = Get-ManifestArtifactPath $manifestObject $manifestDirectory 'service.sh'
$apkPath = Get-ManifestArtifactPath $manifestObject $manifestDirectory 'control.apk'
foreach ($required in @($serviceBinary, $mcpBinary, $u2Jar, $applistDex, $serviceScript, $apkPath)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Missing deployment artifact: $required"
    }
}

# ---------------------------------------------------------------- tokens
$deployState = $null
if (Test-Path -LiteralPath $deployStatePath) {
    $deployState = Get-Content -LiteralPath $deployStatePath -Raw | ConvertFrom-Json
}
function New-Token {
    $bytes = New-Object byte[] 32
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $generator.GetBytes($bytes)
    $generator.Dispose()
    return ([System.BitConverter]::ToString($bytes) -replace '-', '').ToLowerInvariant()
}
if ($null -eq $deployState) {
    $deployState = [pscustomobject]@{
        agent_token          = (New-Token)
        control_token        = (New-Token)
        mcp_token            = (New-Token)
        app_port             = $AppPort
        host_port            = $HostPort
        phone_mcp_port       = $PhoneMcpPort
        phone_mcp_host_port  = $PhoneMcpHostPort
        agent_name           = $AgentName
        chroot               = $Chroot
        release_version      = $releaseVersion
    }
} else {
    $deployState.app_port = $AppPort
    $deployState.host_port = $HostPort
    $deployState.chroot = $Chroot
    $deployState.release_version = $releaseVersion
    foreach ($pair in @(
            @{ Name = 'phone_mcp_port'; Value = $PhoneMcpPort },
            @{ Name = 'phone_mcp_host_port'; Value = $PhoneMcpHostPort })) {
        $deployState | Add-Member -NotePropertyName $pair.Name -NotePropertyValue $pair.Value -Force
    }
}
$mcpToken = [string](Get-Field $deployState 'mcp_token')
if ($mcpToken -eq '') {
    $mcpToken = New-Token
    if ($null -eq $deployState.PSObject.Properties['mcp_token']) {
        $deployState | Add-Member -NotePropertyName mcp_token -NotePropertyValue $mcpToken
    } else {
        $deployState.mcp_token = $mcpToken
    }
}
while ($mcpToken -eq [string]$deployState.agent_token -or
        $mcpToken -eq [string]$deployState.control_token) {
    $mcpToken = New-Token
}
$deployState | Add-Member -NotePropertyName mcp_token -NotePropertyValue $mcpToken -Force
Write-Utf8NoBom -Path $deployStatePath -Text ($deployState | ConvertTo-Json -Depth 4)
& icacls $deployStatePath /inheritance:r /grant:r "$($env:USERNAME):(R,W)" | Out-Null

$installDirectory = "$Chroot/opt/mobile-use-vnext"
$stateOnDevice = "$Chroot/var/lib/mobile-use-vnext"

# ---------------------------------------------------------------- stop this repository's deployment

# Private App data is never removed: no uninstall, no pm clear and no deletion
# under /data/data/com.mobileuse.vnext. Only the process is stopped so the new
# APK can be installed over the existing one with adb install -r.
Write-Output 'Stopping the installed control App...'
$null = Invoke-Adb -Arguments @('shell', 'am', 'force-stop', 'com.mobileuse.vnext') -AllowFailure

# The Go service must be restarted for a new binary/config; a running instance
# keeps the old package/tokens until it is stopped.
Write-Output 'Stopping the current Go device service...'
$null = Invoke-DeviceScriptAllowFailure -Script @"
su -c 'chroot $Chroot /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; if [ -x /opt/mobile-use-vnext/service.sh ]; then /opt/mobile-use-vnext/service.sh stop || true; fi"'
"@

Write-Output 'Stopping the phone-side MCP adapter (if present)...'
$null = Invoke-DeviceScriptAllowFailure -Script @"
su -c 'pkill -f "^/opt/mobile-use-vnext/device-mcp " || true'
su -c 'pkill -f "^device-mcp -transport " || true'
su -c 'rm -f $stateOnDevice/device-mcp.pid'
"@
# A host-managed stdio adapter can be respawned by its supervisor between the
# stop and the install. The install below is atomic and the post-install hash
# checks prove the file; this bounded wait only reduces the chance of starting
# the new HTTP adapter behind an old listener.
for ($attempt = 0; $attempt -lt 10; $attempt++) {
    $remaining = Invoke-DeviceScriptAllowFailure -Script 'pgrep -x device-mcp'
    if ($remaining.Trim() -eq '') { break }
    Start-Sleep -Milliseconds 300
}
if ($remaining.Trim() -ne '') {
    Write-Warning ("phone-side MCP adapter still running after the stop attempt (pid(s): {0}); the install is atomic but the old process may keep the previous inode." -f ($remaining.Trim() -replace "\s+", ", "))
}

# ---------------------------------------------------------------- stage artifacts
$staging = '/data/local/tmp/mobile-use-vnext-deploy'
$null = Invoke-DeviceScript -Script "rm -rf $staging && mkdir -p $staging"
$configPath = Join-Path $stateDirectory 'config.json'
$config = [ordered]@{
    server           = [ordered]@{ host = '127.0.0.1'; port = [int]$AppPort }
    state_dir        = '/var/lib/mobile-use-vnext'
    adb              = [ordered]@{ path = 'adb'; serial = '127.0.0.1:5555'; u2_port = 9008; timeout_seconds = 8 }
    tokens           = [ordered]@{ agent = $deployState.agent_token; control = $deployState.control_token }
    agent_name       = $AgentName
    control_package  = 'com.mobileuse.vnext'
    u2_jar_path      = '/opt/mobile-use-vnext/u2.jar'
    applist_dex_path = '/opt/mobile-use-vnext/applist.dex'
    release_version  = $releaseVersion
}
Write-Utf8NoBom -Path $configPath -Text ($config | ConvertTo-Json -Depth 5)
& icacls $configPath /inheritance:r /grant:r "$($env:USERNAME):(R,W)" | Out-Null

if (-not $SkipPhoneMcp) {
    $phoneMcpConfigPath = Join-Path $stateDirectory 'phone-mcp-config.json'
    $phoneMcpConfig = [ordered]@{
        service   = [ordered]@{
            base_url    = "http://127.0.0.1:$AppPort"
            agent_token = $deployState.agent_token
        }
        transport = 'http'
        http      = [ordered]@{
            listen          = "127.0.0.1:$PhoneMcpPort"
            token           = $mcpToken
            allowed_origins = @($McpAllowedOrigins)
        }
    }
    Write-Utf8NoBom -Path $phoneMcpConfigPath -Text ($phoneMcpConfig | ConvertTo-Json -Depth 5)
    & icacls $phoneMcpConfigPath /inheritance:r /grant:r "$($env:USERNAME):(R,W)" | Out-Null
}

$stagedFiles = @($serviceBinary, $u2Jar, $applistDex, $serviceScript, $configPath)
if (-not $SkipPhoneMcp) {
    $stagedFiles += @($mcpBinary, $phoneMcpConfigPath)
}
foreach ($file in $stagedFiles) {
    $null = Invoke-Adb -Arguments @('push', $file, "$staging/")
}

# A host-managed stdio adapter can hold the old inode (or respawn) while the
# release is being staged, so cp onto the live path fails with ETXTBSY. A
# multi-command device shell used to mask that failure behind a later chmod.
# Install beside the target and rename, run the shell with set -eu, and verify
# every deployed artifact against the manifest before starting anything.
$null = Invoke-DeviceScript -Script @"
set -eu
su -c 'mkdir -p $installDirectory $stateOnDevice'
su -c 'cp $staging/device-service $installDirectory/device-service.new && chmod 755 $installDirectory/device-service.new && mv -f $installDirectory/device-service.new $installDirectory/device-service'
su -c 'cp $staging/u2.jar $installDirectory/u2.jar.new && chmod 600 $installDirectory/u2.jar.new && mv -f $installDirectory/u2.jar.new $installDirectory/u2.jar'
su -c 'cp $staging/applist.dex $installDirectory/applist.dex.new && chmod 600 $installDirectory/applist.dex.new && mv -f $installDirectory/applist.dex.new $installDirectory/applist.dex'
su -c 'cp $staging/service.sh $installDirectory/service.sh.new && chmod 755 $installDirectory/service.sh.new && mv -f $installDirectory/service.sh.new $installDirectory/service.sh'
su -c 'cp $staging/config.json $installDirectory/config.json.new && chmod 600 $installDirectory/config.json.new && mv -f $installDirectory/config.json.new $installDirectory/config.json'
su -c 'chmod 700 $stateOnDevice'
"@

if (-not $SkipPhoneMcp) {
    $null = Invoke-DeviceScript -Script @"
set -eu
su -c 'cp $staging/device-mcp $installDirectory/device-mcp.new && chmod 755 $installDirectory/device-mcp.new && mv -f $installDirectory/device-mcp.new $installDirectory/device-mcp'
su -c 'cp $staging/phone-mcp-config.json $installDirectory/mcp-config.json.new && chmod 600 $installDirectory/mcp-config.json.new && mv -f $installDirectory/mcp-config.json.new $installDirectory/mcp-config.json'
"@
}

# A successful mv only proves the directory entry changed. Compare what is
# actually on disk with the verified manifest before starting any service.
Assert-DeviceFileSha256 -Name 'device-service' -Path "$installDirectory/device-service" -Expected $manifestHashes['device-service']
Assert-DeviceFileSha256 -Name 'u2.jar' -Path "$installDirectory/u2.jar" -Expected $manifestHashes['u2.jar']
Assert-DeviceFileSha256 -Name 'applist.dex' -Path "$installDirectory/applist.dex" -Expected $manifestHashes['applist.dex']
Assert-DeviceFileSha256 -Name 'service.sh' -Path "$installDirectory/service.sh" -Expected $manifestHashes['service.sh']
$configHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $configPath).Hash.ToLowerInvariant()
Assert-DeviceFileSha256 -Name 'config.json' -Path "$installDirectory/config.json" -Expected $configHash
if (-not $SkipPhoneMcp) {
    Assert-DeviceFileSha256 -Name 'device-mcp' -Path "$installDirectory/device-mcp" -Expected $manifestHashes['device-mcp']
    $phoneMcpConfigHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $phoneMcpConfigPath).Hash.ToLowerInvariant()
    Assert-DeviceFileSha256 -Name 'mcp-config.json' -Path "$installDirectory/mcp-config.json" -Expected $phoneMcpConfigHash
}

# ---------------------------------------------------------------- install control App
Write-Output 'Installing the paired control App with adb install -r (private data preserved)...'
try {
    $installOutput = Invoke-Adb -Arguments @('install', '-r', $apkPath)
} catch {
    throw ("APK install failed. Stop the release and investigate the device/package state; " +
        "do not uninstall com.mobileuse.vnext because its private data must be preserved. " +
        $_.Exception.Message)
}
if ($installOutput -notmatch 'Success') {
    throw ("APK install failed: $installOutput. Stop the release and investigate; " +
        'do not uninstall com.mobileuse.vnext (private App data must be preserved).')
}

# The package manager can accept an APK while a stale or wrong-signed package
# remains installed. Verify the installed identity against the manifest and
# fail the release without uninstalling anything on mismatch.
Write-Output 'Verifying the installed App identity against the release manifest...'
try {
    $installedApp = Get-InstalledAppIdentity -Package $expectedPackage
} catch {
    throw ("Installed App identity check failed. Stop the release and investigate; " +
        "do not uninstall com.mobileuse.vnext because its private data must be preserved. " +
        $_.Exception.Message)
}
Write-Output ("installed App: package={0} versionName={1} versionCode={2}" -f
    $installedApp.Package, $installedApp.VersionName, $installedApp.VersionCode)
if ($installedApp.Package -ne $expectedPackage -or
        $installedApp.VersionName -ne $expectedVersionName -or
        $installedApp.VersionCode -ne $expectedVersionCode) {
    throw ("Installed App identity does not match the release manifest: " +
        "installed package=$($installedApp.Package) versionName=$($installedApp.VersionName) versionCode=$($installedApp.VersionCode); " +
        "manifest package=$expectedPackage versionName=$expectedVersionName versionCode=$expectedVersionCode. " +
        'Stop the release and investigate; do not uninstall com.mobileuse.vnext (private App data must be preserved).')
}
$connection = [ordered]@{ base_url = "http://127.0.0.1:$AppPort"; token = $deployState.control_token }
$connectionPath = Join-Path $stateDirectory 'connection.json'
Write-Utf8NoBom -Path $connectionPath -Text ($connection | ConvertTo-Json)
$null = Invoke-Adb -Arguments @('push', $connectionPath, "$staging/connection.json")
$null = Invoke-DeviceScript -Script @"
set -eu
su -c 'mkdir -p /data/data/com.mobileuse.vnext/files'
su -c 'cp $staging/connection.json /data/data/com.mobileuse.vnext/files/connection.json.new && chmod 600 /data/data/com.mobileuse.vnext/files/connection.json.new && mv -f /data/data/com.mobileuse.vnext/files/connection.json.new /data/data/com.mobileuse.vnext/files/connection.json'
su -c 'chown -R `$(stat -c %u /data/data/com.mobileuse.vnext):`$(stat -c %g /data/data/com.mobileuse.vnext) /data/data/com.mobileuse.vnext/files'
su -c 'chmod 600 /data/data/com.mobileuse.vnext/files/connection.json'
"@
$connectionHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $connectionPath).Hash.ToLowerInvariant()
Assert-DeviceFileSha256 -Name 'connection.json' -Path '/data/data/com.mobileuse.vnext/files/connection.json' -Expected $connectionHash
$null = Invoke-Adb -Arguments @('shell', 'pm', 'grant', 'com.mobileuse.vnext', 'android.permission.POST_NOTIFICATIONS') -AllowFailure
$null = Invoke-Adb -Arguments @('shell', 'appops', 'set', 'com.mobileuse.vnext', 'SYSTEM_ALERT_WINDOW', 'allow') -AllowFailure

# ---------------------------------------------------------------- start service and App
Write-Output 'Starting the Go device service inside the chroot...'
$startOutput = Invoke-DeviceScript -Script @"
su -c 'chroot $Chroot /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh start"'
"@
Write-Output $startOutput

if (-not $SkipPhoneMcp) {
    Write-Output 'Starting the phone-side MCP adapter...'
    $null = Invoke-DeviceScript -Script @"
su -c 'chroot $Chroot /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; umask 077; nohup setsid /opt/mobile-use-vnext/device-mcp -transport http -config /opt/mobile-use-vnext/mcp-config.json >> /var/lib/mobile-use-vnext/device-mcp.log 2>&1 < /dev/null &"'
"@
    $mcpReady = $false
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        $code = Invoke-DeviceScriptAllowFailure -Script "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$PhoneMcpPort/mcp || true"
        if ($code.Trim() -eq '401') {
            $mcpReady = $true
            break
        }
        Start-Sleep -Milliseconds 500
    }
    if (-not $mcpReady) {
        throw ("Phone-side MCP adapter did not answer 401 on 127.0.0.1:$PhoneMcpPort/mcp; " +
            "inspect /var/lib/mobile-use-vnext/device-mcp.log and do not report the release complete.")
    }
    # The 401 probe only proves that some listener answers. An adapter left
    # over from the previous release can still own the port, so require exactly
    # one HTTP adapter and prove its executable is the manifest binary.
    $httpPidOutput = Invoke-DeviceScriptAllowFailure -Script 'pgrep -f "^/opt/mobile-use-vnext/device-mcp -transport http "'
    $httpPids = @($httpPidOutput -split "\s+" | Where-Object { $_ -match '^\d+$' })
    if ($httpPids.Count -ne 1) {
        throw ("Expected exactly one phone-side HTTP MCP adapter, found {0}. Inspect /var/lib/mobile-use-vnext/device-mcp.log and do not report the release complete." -f $httpPids.Count)
    }
    $httpPid = [string]$httpPids[0]
    Assert-DeviceFileSha256 -Name 'device-mcp (running http)' -Path "/proc/$httpPid/exe" -Expected $manifestHashes['device-mcp']
    $null = Invoke-DeviceScript -Script "su -c 'echo $httpPid > $stateOnDevice/device-mcp.pid'"
    $null = Invoke-DeviceScript -Script "su -c 'chmod 600 $stateOnDevice/device-mcp.log $stateOnDevice/device-mcp.pid 2>/dev/null || true'"

    # The managed HTTP adapter is now proven. A host-managed stdio adapter can
    # have been respawned from the previous inode; it cannot be restarted by
    # this script, so report it instead of claiming the whole MCP surface was
    # refreshed.
    $staleAdapters = 0
    $allMcpPids = Invoke-DeviceScriptAllowFailure -Script 'pgrep -x device-mcp'
    foreach ($pidText in @($allMcpPids -split "\s+")) {
        if ($pidText -notmatch '^\d+$' -or $pidText -eq $httpPid) { continue }
        $runningHash = Get-DeviceFileSha256 -Path "/proc/$pidText/exe"
        if ($runningHash -ne '' -and $runningHash -ne $manifestHashes['device-mcp']) { $staleAdapters++ }
    }
    if ($staleAdapters -gt 0) {
        Write-Warning ("phone MCP stdio adapter: previous binary still running in {0} process(es); restart the host MCP process to load the deployed device-mcp." -f $staleAdapters)
    } else {
        Write-Output 'phone MCP stdio adapter: no stale binary detected (host restart still required to rediscover tools)'
    }
}

Write-Output 'Launching the control App service...'
# MIUI can suspend a newly installed package before its first Activity launch;
# the paired foreground service is started directly with root so the control
# channel does not depend on that Activity interception.
$appStart = Invoke-DeviceScriptAllowFailure -Script @"
su -c 'am start-foreground-service -n com.mobileuse.vnext/.ControlService -a com.mobileuse.vnext.START'
"@
Write-Output $appStart
$null = Invoke-Adb -Arguments @('shell', 'am', 'start', '-n', 'com.mobileuse.vnext/.MainActivity') -AllowFailure
# MIUI may suspend a fresh package between install and first component start.
# Retry the paired foreground service until its process exists.
for ($attempt = 0; $attempt -lt 8; $attempt++) {
    Start-Sleep -Seconds 2
    $processId = Invoke-Adb -Arguments @('shell', 'pidof', 'com.mobileuse.vnext') -AllowFailure
    if ($processId -match '^\d+') {
        break
    }
    $null = Invoke-DeviceScriptAllowFailure -Script @"
su -c 'pm unsuspend --user 0 com.mobileuse.vnext'
su -c 'am start-foreground-service -n com.mobileuse.vnext/.ControlService -a com.mobileuse.vnext.START'
"@
}

# ---------------------------------------------------------------- readiness
$null = Invoke-Adb -Arguments @('forward', '--remove', "tcp:$HostPort") -AllowFailure
$null = Invoke-Adb -Arguments @('forward', "tcp:$HostPort", "tcp:$AppPort")
$baseUrl = "http://127.0.0.1:$HostPort"
$headers = @{
    Authorization           = "Bearer $($deployState.agent_token)"
    'X-Mobile-Use-Contract' = $contractVersion
}
$deadline = (Get-Date).AddSeconds(90)
$ready = $false
$lastStatus = $null
$health = $null
$versionMismatch = ''
while ((Get-Date) -lt $deadline) {
    try {
        $health = Invoke-RestMethod -Uri "$baseUrl/health" -Method Get -TimeoutSec 3
        $lastStatus = Invoke-RestMethod -Uri "$baseUrl/api/status" -Method Post -Headers $headers `
            -ContentType 'application/json' -Body '{}' -TimeoutSec 5
    } catch {
        Start-Sleep -Milliseconds 500
        continue
    }
    $healthRelease = [string](Get-Field $health 'release_version')
    $healthContract = [string](Get-Field $health 'contract_version')
    if ($healthRelease -ne $releaseVersion -or $healthContract -ne $contractVersion) {
        $versionMismatch = "health release=$healthRelease contract=$healthContract; manifest release=$releaseVersion contract=$contractVersion"
        break
    }
    $recoveryView = Get-Field $lastStatus 'recovery'
    if ([bool](Get-Field $lastStatus 'can_start')) {
        $ready = $true
        break
    }
    Start-Sleep -Milliseconds 500
}
if ($versionMismatch -ne '') {
    throw "Deployed service does not match the verified manifest: $versionMismatch"
}
if (-not $ready) {
    $diagnostic = ''
    if ($null -ne $lastStatus) {
        $cleanupView = Get-Field $lastStatus 'cleanup'
        $diagnostic = 'blocking_code={0} cleanup_state={1} execution={2}' -f
            (Get-Field $lastStatus 'blocking_code'), (Get-Field $cleanupView 'state'),
            (Get-Field $cleanupView 'execution')
    }
    throw "Service did not become start-ready. Last status: $diagnostic"
}

$healthRelease = [string](Get-Field $health 'release_version')
$healthContract = [string](Get-Field $health 'contract_version')
$healthStatus = [string](Get-Field $health 'status')
$toolCatalog = Get-Field $manifestObject 'tool_catalog'
$catalogCount = [int](Get-Field $toolCatalog 'count')
$catalogFingerprint = [string](Get-Field $toolCatalog 'sha256')
$null = Invoke-Adb -Arguments @('forward', '--remove', "tcp:$PhoneMcpHostPort") -AllowFailure
if (-not $SkipPhoneMcp) {
    $null = Invoke-Adb -Arguments @('forward', "tcp:$PhoneMcpHostPort", "tcp:$PhoneMcpPort")
}
Write-Output 'Deployment ready.'
Write-Output ("version match: PASS (health release=$healthRelease contract=$healthContract)")
Write-Output 'App pairing: PASS (can_start=true implies a fresh paired control sync)'
Write-Output ("recovery barrier: PASS (health status=$healthStatus; can_start=true requires availability=ready)")
Write-Output 'STOP availability: PASS (stop_ready is part of can_start)'
Write-Output ("manifest: source_commit={0} source_dirty={1} tool_catalog_count={2} tool_catalog_sha256={3}" -f
    $sourceCommit, $sourceDirty, $catalogCount, $catalogFingerprint)
if ($SkipPhoneMcp) {
    Write-Warning 'Phone-side MCP adapter skipped: the deployment does not include phone-hosted MCP use.'
} else {
    Write-Output ("phone MCP HTTP adapter: PASS listen=127.0.0.1:$PhoneMcpPort url=http://127.0.0.1:$PhoneMcpHostPort/mcp sha256={0}" -f $manifestHashes['device-mcp'])
    Write-Output "phone MCP token: mcp_token in $deployStatePath (private, not printed)"
}
Write-Output "host verification URL: $baseUrl"
Write-Output "tokens/config: $deployStatePath (private, gitignored)"
Write-Output 'MCP client tools/list: NOT VERIFIED by deploy (restart the real client; verify-m6.ps1 covers server/discover)'
