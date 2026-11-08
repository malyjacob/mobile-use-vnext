param(
    [string]$Chroot = '/data/local/chroots/debian13',
    [string]$DeviceService = 'build/device-service',
    [string]$Apk = 'android-control/build/control.apk',
    [string]$AppPort = 18880,
    [string]$HostPort = 18881,
    [string]$AgentName = 'local-agent',
    [string]$ReleaseVersion = '0.2.0-m4',
    [switch]$SkipBuild,
    [switch]$SkipOldServiceStop
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
    if ($Value -match '[\s"]') {
        return '"' + ($Value -replace '"', '\"') + '"'
    }
    return $Value
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

# ---------------------------------------------------------------- prerequisites
$devices = Invoke-Adb -Arguments @('devices')
if ($devices -notmatch '\tdevice') {
    throw 'No adb device is online.'
}

if (-not $SkipBuild) {
    & (Join-Path $PSScriptRoot 'build.ps1') -Version $ReleaseVersion
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
    & (Join-Path $repository 'android-control/build.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Android build failed.' }
}

$serviceBinary = [System.IO.Path]::GetFullPath((Join-Path $repository $DeviceService))
$apkPath = [System.IO.Path]::GetFullPath((Join-Path $repository $Apk))
foreach ($required in @($serviceBinary, $apkPath,
        (Join-Path $repository 'third_party/uiautomator/u2.jar'),
        (Join-Path $repository 'third_party/android-applist/applist.dex'),
        (Join-Path $repository 'scripts/service.sh'))) {
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
        agent_token     = (New-Token)
        control_token   = (New-Token)
        app_port        = $AppPort
        host_port       = $HostPort
        agent_name      = $AgentName
        chroot          = $Chroot
        release_version = $ReleaseVersion
    }
} else {
    $deployState.app_port = $AppPort
    $deployState.host_port = $HostPort
    $deployState.chroot = $Chroot
    $deployState.release_version = $ReleaseVersion
}
Write-Utf8NoBom -Path $deployStatePath -Text ($deployState | ConvertTo-Json -Depth 4)
& icacls $deployStatePath /inheritance:r /grant:r "$($env:USERNAME):(R,W)" | Out-Null

# ---------------------------------------------------------------- stop old deployments
if (-not $SkipOldServiceStop) {
    Write-Output 'Stopping the old Python device service (if present)...'
    $null = Invoke-DeviceScriptAllowFailure -Script @"
su -c 'if [ -x /opt/mobile-use-platform/scripts/service.sh ]; then chroot $Chroot /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-platform/scripts/service.sh stop"; fi'
su -c 'pkill -f "mobile-use-service serve" || true'
"@
}

Write-Output 'Stopping the installed control App and removing the old signature...'
$null = Invoke-Adb -Arguments @('shell', 'am', 'force-stop', 'com.mobileuse.vnext') -AllowFailure
$null = Invoke-Adb -Arguments @('uninstall', 'com.mobileuse.vnext') -AllowFailure
$null = Invoke-Adb -Arguments @('shell', 'am', 'force-stop', 'com.mobileuse.control') -AllowFailure
$null = Invoke-Adb -Arguments @('uninstall', 'com.mobileuse.control') -AllowFailure

# The Go service must be restarted for a new binary/config; a running instance
# keeps the old package/tokens until it is stopped.
Write-Output 'Stopping the current Go device service...'
$null = Invoke-DeviceScriptAllowFailure -Script @"
su -c 'chroot $Chroot /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; if [ -x /opt/mobile-use-vnext/service.sh ]; then /opt/mobile-use-vnext/service.sh stop || true; fi"'
"@

# ---------------------------------------------------------------- stage artifacts
$staging = '/data/local/tmp/mobile-use-vnext-deploy'
$null = Invoke-DeviceScript -Script "rm -rf $staging && mkdir -p $staging"
$configPath = Join-Path $stateDirectory 'config.json'
$config = [ordered]@{
    server          = [ordered]@{ host = '127.0.0.1'; port = [int]$AppPort }
    state_dir       = '/var/lib/mobile-use-vnext'
    adb             = [ordered]@{ path = 'adb'; serial = '127.0.0.1:5555'; u2_port = 9008; timeout_seconds = 8 }
    tokens          = [ordered]@{ agent = $deployState.agent_token; control = $deployState.control_token }
    agent_name      = $AgentName
    control_package = 'com.mobileuse.vnext'
    u2_jar_path     = '/opt/mobile-use-vnext/u2.jar'
    applist_dex_path = '/opt/mobile-use-vnext/applist.dex'
    release_version = $ReleaseVersion
}
Write-Utf8NoBom -Path $configPath -Text ($config | ConvertTo-Json -Depth 5)
& icacls $configPath /inheritance:r /grant:r "$($env:USERNAME):(R,W)" | Out-Null

foreach ($file in @($serviceBinary, (Join-Path $repository 'third_party/uiautomator/u2.jar'),
        (Join-Path $repository 'third_party/android-applist/applist.dex'),
        (Join-Path $repository 'scripts/service.sh'), $configPath)) {
    $null = Invoke-Adb -Arguments @('push', $file, "$staging/")
}

$installDirectory = "$Chroot/opt/mobile-use-vnext"
$stateOnDevice = "$Chroot/var/lib/mobile-use-vnext"
$null = Invoke-DeviceScript -Script @"
su -c 'mkdir -p $installDirectory $stateOnDevice'
su -c 'cp $staging/device-service $installDirectory/device-service'
su -c 'cp $staging/u2.jar $installDirectory/u2.jar'
su -c 'cp $staging/applist.dex $installDirectory/applist.dex'
su -c 'cp $staging/service.sh $installDirectory/service.sh'
su -c 'cp $staging/config.json $installDirectory/config.json'
su -c 'chmod 755 $installDirectory/device-service $installDirectory/service.sh'
su -c 'chmod 600 $installDirectory/config.json $installDirectory/u2.jar $installDirectory/applist.dex'
su -c 'chmod 700 $stateOnDevice'
"@

# ---------------------------------------------------------------- install control App
Write-Output 'Installing the paired control App...'
$installOutput = Invoke-Adb -Arguments @('install', '-r', $apkPath)
if ($installOutput -notmatch 'Success') {
    throw "APK install failed: $installOutput"
}
$connection = [ordered]@{ base_url = "http://127.0.0.1:$AppPort"; token = $deployState.control_token }
$connectionPath = Join-Path $stateDirectory 'connection.json'
Write-Utf8NoBom -Path $connectionPath -Text ($connection | ConvertTo-Json)
$null = Invoke-Adb -Arguments @('push', $connectionPath, "$staging/connection.json")
$null = Invoke-DeviceScript -Script @"
su -c 'mkdir -p /data/data/com.mobileuse.vnext/files'
su -c 'cp $staging/connection.json /data/data/com.mobileuse.vnext/files/connection.json'
su -c 'chown -R `$(stat -c %u /data/data/com.mobileuse.vnext):`$(stat -c %g /data/data/com.mobileuse.vnext) /data/data/com.mobileuse.vnext/files'
su -c 'chmod 600 /data/data/com.mobileuse.vnext/files/connection.json'
"@
$null = Invoke-Adb -Arguments @('shell', 'pm', 'grant', 'com.mobileuse.vnext', 'android.permission.POST_NOTIFICATIONS') -AllowFailure
$null = Invoke-Adb -Arguments @('shell', 'appops', 'set', 'com.mobileuse.vnext', 'SYSTEM_ALERT_WINDOW', 'allow') -AllowFailure

# ---------------------------------------------------------------- start service and App
Write-Output 'Starting the Go device service inside the chroot...'
$startOutput = Invoke-DeviceScript -Script @"
su -c 'chroot $Chroot /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh start"'
"@
Write-Output $startOutput

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
    Authorization = "Bearer $($deployState.agent_token)"
    'X-Mobile-Use-Contract' = '1'
}
$deadline = (Get-Date).AddSeconds(90)
$ready = $false
$lastStatus = $null
$health = $null
while ((Get-Date) -lt $deadline) {
    try {
        $health = Invoke-RestMethod -Uri "$baseUrl/health" -Method Get -TimeoutSec 3
        $lastStatus = Invoke-RestMethod -Uri "$baseUrl/api/status" -Method Post -Headers $headers `
            -ContentType 'application/json' -Body '{}' -TimeoutSec 5
        if ($lastStatus.can_start -eq $true) {
            $ready = $true
            break
        }
    } catch {
        Start-Sleep -Milliseconds 500
        continue
    }
    Start-Sleep -Milliseconds 500
}
if (-not $ready) {
    $diagnostic = ''
    if ($null -ne $lastStatus) { $diagnostic = $lastStatus | ConvertTo-Json -Depth 8 }
    throw "Service did not become start-ready. Last status: $diagnostic"
}

Write-Output 'Deployment ready.'
Write-Output "health: contract=$($health.contract_version) service=$($health.service_instance_id) release=$($health.release_version)"
Write-Output "status: can_start=$($lastStatus.can_start)"
Write-Output "host verification URL: $baseUrl"
Write-Output "tokens/config: $deployStatePath (private, gitignored)"
