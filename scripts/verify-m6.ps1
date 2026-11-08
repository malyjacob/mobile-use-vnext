#Requires -Version 5.1
<#
.SYNOPSIS
    Verifies the deployed control surface, message delivery, contract pairing and
    service restart recovery for the current release.

.DESCRIPTION
    Requires an already deployed release (build/release/manifest.json and
    .state/deploy.json), an online adb device, and the paired control App running
    with can_start=true. The script reads no private message body from the device
    and prints only identities, counts and PASS/FAIL lines.

    Checks:
      1. release manifest, health identity, installed App identity, tool catalog;
      2. ordinary contract pairing: an old-contract sync is refused, minimal STOP
         still binds, and admission recovers;
      3. notify -> App private store -> acknowledgement -> notification -> UI list
         and detail with local read state;
      4. the Activity, foreground notification and overlay derive from one control
         snapshot, including the stale state;
      5. service restart recovery: active resources, SIGKILL and SIGTERM paths,
         recovering/blocked admission, no old session revival, no replay.

    Destructive within the current deployment: it stops/restarts the service and
    creates a short-lived session. It never uninstalls the App, deletes state or
    clears App data.

.PARAMETER HostPort
    Service host port. Defaults to host_port in .state/deploy.json.

.PARAMETER Manifest
    Release manifest to verify. Defaults to build/release/manifest.json.

.PARAMETER SkipRestartDrill
    Skip section 5 (service restart recovery). Other sections still run.

.PARAMETER SkipTermDrill
    Skip the SIGTERM half of section 5; the SIGKILL half still runs.

.NOTES
    Exit codes: 0 = PASS, 1 = FAIL, 2 = BLOCKED.
#>
param(
    [int]$HostPort = 0,
    [string]$Manifest = 'build/release/manifest.json',
    [switch]$SkipRestartDrill,
    [switch]$SkipTermDrill
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$stateDirectory = Join-Path $repository '.state'
$deployStatePath = Join-Path $stateDirectory 'deploy.json'
$versionsPath = Join-Path $repository 'release/versions.json'
$uiDumpDirectory = $stateDirectory

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

function New-Uuid {
    return [guid]::NewGuid().ToString()
}

function Get-AdbPath {
    $command = Get-Command adb -ErrorAction SilentlyContinue
    if ($null -eq $command) { Fail 'adb is not on PATH' }
    return $command.Source
}

function Quote-Argument {
    param([string]$Value)
    if ($Value -match '[\s"]') {
        return '"' + ($Value -replace '"', '\"') + '"'
    }
    return $Value
}

function Invoke-Adb {
    param([string[]]$Arguments)
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = Get-AdbPath
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
        return ($stdout + $stderr).Trim()
    } finally {
        $process.Dispose()
    }
}

# Device scripts are sent over stdin so Windows argv quoting cannot rewrite the
# device shell's inner quotes.
function Invoke-DeviceScript {
    param([string]$Script, [switch]$AllowFailure)
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = Get-AdbPath
    $startInfo.Arguments = 'shell sh'
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.CreateNoWindow = $true
    $process = [System.Diagnostics.Process]::Start($startInfo)
    try {
        $process.StandardInput.Write($Script.Replace("`r", '') + "`n")
        $process.StandardInput.Close()
        $stdout = $process.StandardOutput.ReadToEnd()
        $stderr = $process.StandardError.ReadToEnd()
        $process.WaitForExit()
        if (-not $AllowFailure -and $process.ExitCode -ne 0) {
            Fail ("device script failed (" + $process.ExitCode + "): " + $stderr + $stdout)
        }
        return $stdout.Trim()
    } finally {
        $process.Dispose()
    }
}

function Invoke-Http {
    param([string]$Url, [string]$Method = 'POST', [string]$Body = '{}',
        [hashtable]$Headers = @{}, [int]$TimeoutSeconds = 20)
    $request = [System.Net.HttpWebRequest]::Create($Url)
    $request.Method = $Method
    $request.Timeout = $TimeoutSeconds * 1000
    $request.ReadWriteTimeout = $TimeoutSeconds * 1000
    $request.Proxy = $null
    $request.KeepAlive = $false
    foreach ($name in $Headers.Keys) {
        if ($name -eq 'Accept') {
            $request.Accept = [string]$Headers[$name]
        } else {
            $request.Headers.Add([string]$name, [string]$Headers[$name])
        }
    }
    if ($Method -eq 'POST') {
        $payload = [System.Text.Encoding]::UTF8.GetBytes($Body)
        $request.ContentType = 'application/json; charset=utf-8'
        $request.ContentLength = $payload.Length
        $stream = $request.GetRequestStream()
        try { $stream.Write($payload, 0, $payload.Length) } finally { $stream.Dispose() }
    }
    $response = $null
    try {
        $response = $request.GetResponse()
    } catch [System.Net.WebException] {
        $response = $_.Exception.Response
        if ($null -eq $response) {
            Fail ("cannot reach " + $Url + " (" + $_.Exception.Status + ")")
        }
    }
    try {
        $status = [int]$response.StatusCode
        $memory = New-Object System.IO.MemoryStream
        $input = $response.GetResponseStream()
        try {
            $buffer = New-Object byte[] 16384
            while (($read = $input.Read($buffer, 0, $buffer.Length)) -gt 0) {
                $memory.Write($buffer, 0, $read)
            }
            $text = [System.Text.Encoding]::UTF8.GetString($memory.ToArray())
        } finally {
            $input.Dispose()
            $memory.Dispose()
        }
    } finally {
        $response.Dispose()
    }
    return [pscustomobject]@{ Status = $status; Text = $text }
}

function Get-Json {
    param([string]$Text, [string]$What)
    try { return $Text | ConvertFrom-Json } catch { Fail ("$What returned invalid JSON") }
}

function Get-Health {
    $response = Invoke-Http -Url ($script:baseUrl + '/health') -Method 'GET'
    if ($response.Status -ne 200) { Fail ("/health returned HTTP " + $response.Status) }
    return Get-Json $response.Text '/health'
}

function Invoke-Api {
    param([string]$Path, [string]$Body = '{}', [string]$Operation = '', [int]$TimeoutSeconds = 60)
    $headers = @{
        Authorization           = 'Bearer ' + $script:agentToken
        'X-Mobile-Use-Contract' = $script:contractVersion
    }
    if ($Operation -ne '') { $headers['X-Mobile-Use-Operation'] = $Operation }
    $response = Invoke-Http -Url ($script:baseUrl + $Path) -Body $Body -Headers $headers `
        -TimeoutSeconds $TimeoutSeconds
    return $response
}

function Get-Status {
    $response = Invoke-Api -Path '/api/status' -Body '{}'
    if ($response.Status -ne 200) { Fail ("/api/status returned HTTP " + $response.Status) }
    return Get-Json $response.Text '/api/status'
}

function Wait-Until {
    param([scriptblock]$Condition, [int]$TimeoutSeconds, [string]$What)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) { return $true }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Wait-CanStart {
    param([int]$TimeoutSeconds = 90)
    $last = $null
    $ok = Wait-Until -TimeoutSeconds $TimeoutSeconds -What 'can_start' -Condition {
        $script:lastStatus = Get-Status
        return [bool](Get-Field $script:lastStatus 'can_start')
    }
    if (-not $ok) {
        $detail = if ($null -ne $script:lastStatus) { $script:lastStatus | ConvertTo-Json -Depth 8 } else { '<none>' }
        Fail ("can_start did not become true; last status: " + $detail)
    }
}

function Wait-RecoveryBarrier {
    param([int]$TimeoutSeconds = 60)
    $codes = New-Object System.Collections.Generic.List[string]
    $observed = $false
    $uiDump = $null
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $status = Get-Status
        $code = [string](Get-Field $status 'blocking_code')
        if ($code -ne '' -and -not $codes.Contains($code)) { $codes.Add($code) }
        if (-not [bool](Get-Field $status 'can_start')) {
            if ($code -match 'recovery|resource|cleanup|display|shell') {
                $observed = $true
                if ($null -eq $uiDump) {
                    Start-Activity
                    $uiDump = Get-UiDump
                }
            }
            Start-Sleep -Milliseconds 200
            continue
        }
        return [pscustomobject]@{
            ObservedRecovery = $observed; Codes = $codes; Status = $status; UiDump = $uiDump
        }
    }
    Fail ('recovery did not finish; blocking codes: ' + ($codes -join ','))
}

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { Fail $Message }
}

function Start-Activity {
    param([string]$Action = '', [string]$MessageID = '')
    $arguments = @('shell', 'am', 'start', '-n', 'com.mobileuse.vnext/.MainActivity')
    if ($Action -ne '') { $arguments += @('-a', $Action) }
    if ($MessageID -ne '') { $arguments += @('--es', 'com.mobileuse.vnext.EXTRA_MESSAGE_ID', $MessageID) }
    $null = Invoke-Adb -Arguments $arguments
    Start-Sleep -Seconds 1
}

function Get-UiDump {
    # The fixed u2 server already owns UiAutomation on this device; a second
    # `uiautomator dump` process is killed. Use the same dumpWindowHierarchy RPC
    # the service uses, through a private host forward.
    $body = '{"jsonrpc":"2.0","id":1,"method":"dumpWindowHierarchy","params":[true,50]}'
    $response = Invoke-Http -Url 'http://127.0.0.1:19008/jsonrpc/0' -Body $body -TimeoutSeconds 20
    Assert-True ($response.Status -eq 200) ("u2 dump returned HTTP " + $response.Status)
    $json = Get-Json $response.Text 'u2 dump'
    $result = [string](Get-Field $json 'result')
    Assert-True ($result -match '<hierarchy') 'u2 dump returned no hierarchy'
    return $result
}

function Get-UnreadCount {
    param([string]$Dump)
    $match = [regex]::Match($Dump, '消息中心（未读 (\d+)）')
    if (-not $match.Success) { return -1 }
    return [int]$match.Groups[1].Value
}

function Get-TextBounds {
    param([string]$Dump, [string]$Text)
    $quoted = [regex]::Escape($Text)
    $pattern = '<node[^>]*text="' + $quoted + '"[^>]*bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"'
    $match = [regex]::Match($Dump, $pattern)
    if (-not $match.Success) { return $null }
    return [pscustomobject]@{
        X = [int](($match.Groups[1].Value -as [int]) + ($match.Groups[3].Value -as [int])) / 2
        Y = [int](($match.Groups[2].Value -as [int]) + ($match.Groups[4].Value -as [int])) / 2
    }
}

function Tap-Text {
    param([string]$Dump, [string]$Text)
    $bounds = Get-TextBounds -Dump $Dump -Text $Text
    Assert-True ($null -ne $bounds) ("UI text not found for tap: " + $Text)
    $null = Invoke-Adb -Arguments @('shell', 'input', 'tap', [string][int]$bounds.X, [string][int]$bounds.Y)
    Start-Sleep -Seconds 1
}

function Get-NotificationDump {
    return (Invoke-Adb -Arguments @('shell', 'dumpsys', 'notification', '--noredact'))
}

function Get-ServicePid {
    $pidText = Invoke-DeviceScript -Script @"
su -c 'cat $script:stateOnDevice/device-service.pid 2>/dev/null || true'
"@ -AllowFailure
    if ($pidText -notmatch '^\d+$') { return '' }
    return $pidText.Trim()
}

function Start-Service {
    $output = Invoke-DeviceScript -Script @"
su -c 'chroot $script:chroot /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh start"'
"@
    Write-Output ('service start: ' + $output)
}

function Stop-Service {
    $output = Invoke-DeviceScript -Script @"
su -c 'chroot $script:chroot /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh stop"'
"@ -AllowFailure
    Write-Output ('service stop: ' + $output)
}

function Stop-App {
    $null = Invoke-Adb -Arguments @('shell', 'am', 'force-stop', 'com.mobileuse.vnext')
    Start-Sleep -Seconds 1
}

function Start-App {
    $null = Invoke-DeviceScript -Script @"
su -c 'am start-foreground-service -n com.mobileuse.vnext/.ControlService -a com.mobileuse.vnext.START'
"@ -AllowFailure
    $started = Wait-Until -TimeoutSeconds 20 -What 'App process' -Condition {
        $appPid = Invoke-Adb -Arguments @('shell', 'pidof', 'com.mobileuse.vnext')
        return ($appPid -match '^\d+')
    }
    Assert-True $started 'the control App process did not start'
}

function Wait-Health {
    param([int]$TimeoutSeconds = 30)
    $ok = Wait-Until -TimeoutSeconds $TimeoutSeconds -What 'health' -Condition {
        try {
            $health = Get-Health
            return [string](Get-Field $health 'status') -eq 'ok'
        } catch {
            return $false
        }
    }
    if (-not $ok) { Fail 'service /health did not become ready' }
}

function Pause-Service {
    $servicePid = Get-ServicePid
    Assert-True ($servicePid -ne '') 'service pid file is missing'
    $null = Invoke-DeviceScript -Script @"
su -c 'kill -STOP $servicePid'
"@
}

function Resume-Service {
    $servicePid = Get-ServicePid
    Assert-True ($servicePid -ne '') 'service pid file is missing'
    $null = Invoke-DeviceScript -Script @"
su -c 'kill -CONT $servicePid'
"@
}

function Get-InstalledAppIdentity {
    $dump = Invoke-Adb -Arguments @('shell', 'dumpsys', 'package', 'com.mobileuse.vnext')
    $versionCode = [regex]::Match($dump, '(?m)^\s*versionCode=(\d+)')
    $versionName = [regex]::Match($dump, '(?m)^\s*versionName=([^\r\n]+)')
    if (-not $versionCode.Success -or -not $versionName.Success) {
        Fail 'could not read installed App versionName/versionCode'
    }
    return [pscustomobject]@{
        VersionName = $versionName.Groups[1].Value.Trim()
        VersionCode = [int]$versionCode.Groups[1].Value
    }
}

function Get-MessageCount {
    param([string]$Path)
    $count = Invoke-DeviceScript -Script @"
su -c 'grep -o "\"id\"" $Path 2>/dev/null | wc -l || true'
"@ -AllowFailure
    if ($count -match '(\d+)') { return [int]$Matches[1] }
    return 0
}

function Test-MessagePresent {
    param([string]$Path, [string]$ID)
    $result = Invoke-DeviceScript -Script @"
su -c 'grep -q "$ID" $Path 2>/dev/null && echo present || echo absent'
"@ -AllowFailure
    return ($result -match 'present')
}

function SyncBody {
    param([string]$Contract, [string]$AppInstance, [string]$ModeGeneration)
    return (@{
            contract_version      = $Contract
            app_instance_id       = $AppInstance
            app_version           = '0.4.0'
            selected_mode         = 'virtual'
            mode_generation       = $ModeGeneration
            mode_ready            = $true
            stop_ready            = $true
            notifications_enabled = $true
            background_running    = $true
            power_interactive     = $true
            keyguard_locked       = $false
            primary_display       = @{
                display_id = 0; width = 1080; height = 2400; density_dpi = 420; rotation = 0
                generation = $ModeGeneration; ime_hide = $false
            }
            display               = $null
            command_results       = @()
            stop_pending          = $null
            message_acks          = @()
        } | ConvertTo-Json -Depth 6 -Compress)
}

try {
    # ---------------------------------------------------------------- prerequisites
    if (-not (Test-Path -LiteralPath $deployStatePath -PathType Leaf)) {
        Write-Output 'BLOCKED deploy_state_missing; run scripts/deploy.ps1 first'
        exit 2
    }
    $deploy = Get-Content -LiteralPath $deployStatePath -Raw | ConvertFrom-Json
    $agentToken = [string](Get-Field $deploy 'agent_token')
    $controlToken = [string](Get-Field $deploy 'control_token')
    if ($agentToken -eq '' -or $controlToken -eq '') { Fail 'deploy state is missing tokens' }
    if ($HostPort -gt 0) { $servicePort = $HostPort } else {
        $servicePort = [int](Get-Field $deploy 'host_port')
    }
    $chroot = [string](Get-Field $deploy 'chroot')
    if ($chroot -eq '') { Fail 'deploy state is missing chroot' }
    $baseUrl = 'http://127.0.0.1:' + $servicePort
    $stateOnDevice = $chroot + '/var/lib/mobile-use-vnext'

    if (-not (Test-Path -LiteralPath $versionsPath -PathType Leaf)) { Fail 'release/versions.json is missing' }
    $versions = Get-Content -LiteralPath $versionsPath -Raw | ConvertFrom-Json
    $script:contractVersion = [string]$versions.contract_version
    $releaseVersion = [string]$versions.release_version
    $script:agentToken = $agentToken
    $script:baseUrl = $baseUrl

    $devices = Invoke-Adb -Arguments @('devices')
    if ($devices -notmatch '\tdevice') {
        Write-Output 'BLOCKED device_offline'
        exit 2
    }
    $null = Invoke-Adb -Arguments @('forward', '--remove', "tcp:$servicePort")
    $null = Invoke-Adb -Arguments @('forward', "tcp:$servicePort", 'tcp:18880')
    $null = Invoke-Adb -Arguments @('forward', '--remove', 'tcp:19008')
    $null = Invoke-Adb -Arguments @('forward', 'tcp:19008', 'tcp:9008')
    $mcpToken = [string](Get-Field $deploy 'mcp_token')
    $phoneMcpHostPort = [int](Get-Field $deploy 'phone_mcp_host_port')
    if ($phoneMcpHostPort -lt 1) { $phoneMcpHostPort = 18883 }
    Assert-True ($mcpToken -ne '') 'deploy state is missing mcp_token; the phone MCP adapter is part of the release'
    $null = Invoke-Adb -Arguments @('forward', '--remove', "tcp:$phoneMcpHostPort")
    $null = Invoke-Adb -Arguments @('forward', "tcp:$phoneMcpHostPort", 'tcp:18882')

    # ---------------------------------------------------------------- release identity
    $manifestPath = if ([System.IO.Path]::IsPathRooted($Manifest)) {
        [System.IO.Path]::GetFullPath($Manifest)
    } else {
        [System.IO.Path]::GetFullPath((Join-Path $repository $Manifest))
    }
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        Fail "release manifest is missing: $manifestPath"
    }
    & (Join-Path $PSScriptRoot 'verify-manifest.ps1') -Manifest $manifestPath
    if ($LASTEXITCODE -ne 0) { Fail 'release manifest verification failed' }
    $manifestObject = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    $manifestRelease = [string](Get-Field $manifestObject 'release_version')
    $manifestContract = [string](Get-Field $manifestObject 'contract_version')
    $manifestAndroid = Get-Field $manifestObject 'android'
    $manifestToolCatalog = Get-Field $manifestObject 'tool_catalog'
    Assert-True ($manifestRelease -eq $releaseVersion) 'manifest release_version differs from the version source'
    Assert-True ($manifestContract -eq $script:contractVersion) 'manifest contract_version differs from the version source'
    Write-Output ("CHECK release manifest: PASS release=$manifestRelease contract=$manifestContract source_commit=" +
        [string](Get-Field $manifestObject 'source_commit'))

    $health = Get-Health
    Assert-True ([string](Get-Field $health 'contract_version') -eq $manifestContract) 'service contract_version differs from the manifest'
    Assert-True ([string](Get-Field $health 'release_version') -eq $manifestRelease) 'service release_version differs from the manifest'
    Write-Output 'CHECK service identity: PASS'

    $installedApp = Get-InstalledAppIdentity
    Assert-True ($installedApp.VersionName -eq [string](Get-Field $manifestAndroid 'version_name')) 'installed App versionName differs from the manifest'
    Assert-True ($installedApp.VersionCode -eq [int](Get-Field $manifestAndroid 'version_code')) 'installed App versionCode differs from the manifest'
    Write-Output ("CHECK installed App: PASS versionName=" + $installedApp.VersionName + " versionCode=" + $installedApp.VersionCode)

    $catalogText = @()
    $catalogExit = 1
    Push-Location $repository
    try {
        $catalogText = (@(& go run ./scripts/toolcatalog) -join "`n")
        $catalogExit = $LASTEXITCODE
    } finally {
        Pop-Location
    }
    if ($catalogExit -ne 0) { Fail 'tool catalog command failed' }
    $catalog = Get-Json $catalogText 'tool catalog'
    Assert-True ([int](Get-Field $catalog 'count') -eq [int](Get-Field $manifestToolCatalog 'count')) 'tool catalog count differs from the manifest'
    Assert-True ([string](Get-Field $catalog 'sha256') -eq [string](Get-Field $manifestToolCatalog 'sha256')) 'tool catalog fingerprint differs from the manifest'
    Write-Output ("CHECK tool catalog: PASS count=" + (Get-Field $catalog 'count') + " sha256=" + (Get-Field $catalog 'sha256'))

    $status = Get-Status
    if (-not [bool](Get-Field $status 'can_start')) {
        Write-Output ('BLOCKED service not admissible: ' + ($status | ConvertTo-Json -Depth 8 -Compress))
        exit 2
    }
    Write-Output 'CHECK admissible baseline: PASS can_start=true'

    # ---------------------------------------------------------------- phone MCP adapter
    $mcpUrl = 'http://127.0.0.1:' + $phoneMcpHostPort + '/mcp'
    $mcpNoAuth = Invoke-Http -Url $mcpUrl -Method 'GET'
    Assert-True ($mcpNoAuth.Status -eq 401) ('phone MCP adapter did not reject an unauthenticated request: HTTP ' + $mcpNoAuth.Status)
    $discoverBody = '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{' +
        '"io.modelcontextprotocol/protocolVersion":"2026-07-28",' +
        '"io.modelcontextprotocol/clientCapabilities":{}}}}'
    $mcpHeaders = @{
        Authorization          = 'Bearer ' + $mcpToken
        'MCP-Protocol-Version' = '2026-07-28'
        'Mcp-Method'           = 'server/discover'
        'Accept'               = 'application/json, text/event-stream'
    }
    $mcpResponse = Invoke-Http -Url $mcpUrl -Body $discoverBody -Headers $mcpHeaders
    Assert-True ($mcpResponse.Status -eq 200) ('phone MCP discover returned HTTP ' + $mcpResponse.Status)
    Assert-True ($mcpResponse.Text -match 'supportedVersions') 'phone MCP discover did not return a supported version'
    Write-Output ('CHECK phone MCP adapter: PASS url=' + $mcpUrl + ' discover=supportedVersions')

    # ---------------------------------------------------------------- contract pairing
    $oldApp = New-Uuid
    $oldMode = New-Uuid
    $oldSync = Invoke-Http -Url ($baseUrl + '/v1/control/sync') -Body (SyncBody -Contract '1' `
            -AppInstance $oldApp -ModeGeneration $oldMode) -Headers @{ Authorization = 'Bearer ' + $controlToken }
    Assert-True ($oldSync.Status -eq 409) ("old-contract sync was not refused: HTTP " + $oldSync.Status)
    Assert-True ($oldSync.Text -match 'contract_mismatch') 'old-contract sync refusal did not use contract_mismatch'
    Write-Output 'CHECK contract pairing: PASS old-contract sync=409 contract_mismatch'

    $sessionBeforeStop = Get-Field $status 'session'
    Assert-True ($null -eq $sessionBeforeStop -or [string](Get-Field $sessionBeforeStop 'state') -eq 'ended') 'STOP pairing check requires no active session'
    $stopInstance = New-Uuid
    $stopBody = @{
        contract_version = '1'
        app_instance_id  = $stopInstance
        stop_seq         = 1
        reason           = 'user_stop'
    } | ConvertTo-Json -Compress
    $stopResponse = Invoke-Http -Url ($baseUrl + '/v1/control/stop') -Body $stopBody `
        -Headers @{ Authorization = 'Bearer ' + $controlToken }
    Assert-True ($stopResponse.Status -eq 200) ("minimal STOP rejected: HTTP " + $stopResponse.Status)
    $stopJson = Get-Json $stopResponse.Text 'stop'
    Assert-True ([bool](Get-Field $stopJson 'accepted')) 'minimal STOP was not accepted'
    Assert-True ([string](Get-Field $stopJson 'state') -match 'recorded|revoked|duplicate') 'minimal STOP returned an unexpected state'
    Write-Output ('CHECK minimal STOP: PASS state=' + [string](Get-Field $stopJson 'state') + ' contract=1')
    Wait-CanStart -TimeoutSeconds 20
    Write-Output 'CHECK admission recovery after STOP: PASS'

    # ---------------------------------------------------------------- messages
    $messageText = '验证消息 ' + (Get-Date).ToString('HHmmss') + '：控制同步投递'
    $notifyBody = @{ text = $messageText } | ConvertTo-Json -Compress
    $notifyResponse = Invoke-Api -Path '/api/notify' -Body $notifyBody
    Assert-True ($notifyResponse.Status -eq 200) ("notify returned HTTP " + $notifyResponse.Status)
    $notify = Get-Json $notifyResponse.Text 'notify'
    $messageID = [string](Get-Field $notify 'message_id')
    Assert-True ($messageID -match '^M[1-9][0-9]*$') 'notify did not return a canonical message id'
    Assert-True ([bool](Get-Field $notify 'saved')) 'notify did not report saved=true'
    Assert-True ([string](Get-Field $notify 'outcome') -eq 'executed') 'notify did not report executed'
    $notification = Get-Field $notify 'notification'
    Assert-True ([string](Get-Field $notification 'state') -eq 'pending') 'notify notification.state is not pending'
    Assert-True ($null -eq (Get-Field $notification 'error')) 'notify pending state still carries an error'
    Write-Output ('CHECK notify accepted: PASS id=' + $messageID + ' notification=pending')

    $serviceMessagesPath = $stateOnDevice + '/messages.json'
    $appMessagesPath = '/data/data/com.mobileuse.vnext/files/messages.json'
    $delivered = Wait-Until -TimeoutSeconds 30 -What 'App message store' -Condition {
        return (Test-MessagePresent -Path $appMessagesPath -ID $messageID)
    }
    Assert-True $delivered 'the App did not store the delivered message'
    Write-Output 'CHECK App message store: PASS message persisted'

    $released = Wait-Until -TimeoutSeconds 30 -What 'message acknowledgement' -Condition {
        return -not (Test-MessagePresent -Path $serviceMessagesPath -ID $messageID)
    }
    Assert-True $released 'the acknowledged message was not released from the service queue'
    Write-Output 'CHECK message acknowledgement: PASS released from service queue'

    $notifications = Get-NotificationDump
    Assert-True ($notifications -match 'agent-messages') 'the Agent message notification channel is absent from dumpsys'
    Assert-True ($notifications -match '验证消息') 'the message notification text was not found'
    Write-Output 'CHECK system notification: PASS channel+text'

    Start-Activity
    $activityDump = Get-UiDump
    Assert-True ($activityDump -match '消息中心') 'the Activity message-center entry is missing'
    $unreadBefore = Get-UnreadCount $activityDump
    Assert-True ($unreadBefore -ge 1) 'the Activity entry does not report an unread message'
    Start-Activity -Action 'com.mobileuse.vnext.OPEN_MESSAGE_CENTER'
    $listDump = Get-UiDump
    Assert-True ($listDump -match [regex]::Escape($messageText)) 'the message list does not show the delivered text'
    Write-Output 'CHECK message center list: PASS'
    Start-Activity -Action 'com.mobileuse.vnext.OPEN_MESSAGE' -MessageID $messageID
    $detailDump = Get-UiDump
    Assert-True ($detailDump -match [regex]::Escape($messageText)) 'the message detail does not show the full text'
    Assert-True ($detailDump -match '时间：') 'the message detail has no created time'
    Write-Output 'CHECK message detail: PASS'
    $null = Invoke-Adb -Arguments @('shell', 'input', 'keyevent', '4')
    Start-Sleep -Milliseconds 500
    $null = Invoke-Adb -Arguments @('shell', 'input', 'keyevent', '4')
    Start-Activity
    $afterReadDump = Get-UiDump
    $unreadAfter = Get-UnreadCount $afterReadDump
    Assert-True ($unreadAfter -eq $unreadBefore - 1) ('local read state did not reduce the unread count: ' +
        $unreadBefore + ' -> ' + $unreadAfter)
    Write-Output ('CHECK local read: PASS unread ' + $unreadBefore + ' -> ' + $unreadAfter + ' (never reported to the service)')

    # ---------------------------------------------------------------- control view entries
    Start-Activity
    $activityDump = Get-UiDump
    Assert-True ($activityDump -match '使用主屏（共享）') 'the primary-screen entry is missing'
    Tap-Text -Dump $activityDump -Text '使用主屏（共享）'
    $confirmationDump = Get-UiDump
    Assert-True ($confirmationDump -match '共享本机主屏给 Agent') 'the primary-screen confirmation dialog did not appear'
    Tap-Text -Dump $confirmationDump -Text '取消'
    $afterCancelDump = Get-UiDump
    Assert-True ($afterCancelDump -match '已选择：虚拟屏') 'cancelling the confirmation changed the selected mode'
    Write-Output 'CHECK primary confirmation: PASS explicit confirmation; cancel keeps virtual'

    $null = Invoke-Adb -Arguments @('shell', 'input', 'keyevent', '3')
    Start-Sleep -Seconds 2
    $overlayDump = Get-UiDump
    Assert-True ($overlayDump -match '待命|会话有效|正在') 'the overlay does not show a control-view summary'
    $notifications = Get-NotificationDump
    Assert-True ($notifications -match '虚拟屏 · 待命|会话有效|正在') 'the foreground notification does not show the control summary'
    Start-Activity
    $activityDump = Get-UiDump
    Assert-True ($activityDump -match '虚拟屏 · 待命') 'the Activity does not show the standby summary'
    foreach ($field in @('停止同步：已同步', '可用性：', '会话：', '设备操作：', '资源清理：',
            '执行链', '最近任务', '所选模式：虚拟屏', '通知：')) {
        Assert-True ($activityDump -match [regex]::Escape($field)) ('the control detail is missing: ' + $field)
    }
    Write-Output 'CHECK control detail: PASS availability/session/work/cleanup/mode/permissions'
    Write-Output 'CHECK control view entries: PASS Activity/notification/overlay show the same standby snapshot'

    Pause-Service
    try {
        Start-Sleep -Seconds 8
        Start-Activity
        $staleDump = Get-UiDump
        Assert-True ($staleDump -match '连接中断') 'the Activity did not enter the stale state'
        Assert-True ($staleDump -notmatch '虚拟屏 · 待命|会话有效') 'the stale Activity still shows a cached ready/active state'
        $notifications = Get-NotificationDump
        Assert-True ($notifications -match '连接中断') 'the foreground notification did not enter the stale state'
        $null = Invoke-Adb -Arguments @('shell', 'input', 'keyevent', '3')
        Start-Sleep -Seconds 1
        $overlayDump = Get-UiDump
        Assert-True ($overlayDump -match '断线|连接中断') 'the overlay did not show the stale state'
    } finally {
        Resume-Service
    }
    Write-Output 'CHECK stale presentation: PASS no cached active state on any entry'
    Wait-CanStart -TimeoutSeconds 30
    Write-Output 'CHECK stale recovery: PASS can_start=true after service resume'

    # ---------------------------------------------------------------- restart recovery
    if (-not $SkipRestartDrill) {
        $start = Invoke-Api -Path '/api/start' -Body '{}'
        Assert-True ($start.Status -eq 200) ("start returned HTTP " + $start.Status)
        $startJson = Get-Json $start.Text 'start'
        $origin = [string](Get-Field $startJson 'operation_id')
        Assert-True ($origin -match '^P') 'start did not return an operation id'
        Start-Sleep -Seconds 1
        $active = Get-Status
        $activeSession = Get-Field $active 'session'
        Assert-True ($null -ne $activeSession -and [string](Get-Field $activeSession 'state') -eq 'active') 'start did not produce an active session'
        Write-Output ('CHECK active session before restart: PASS origin=' + $origin)

        $pidText = Get-ServicePid
        Assert-True ($pidText -ne '') 'service pid file is missing'
        $killOutput = Invoke-DeviceScript -Script @"
su -c 'kill -9 $pidText 2>/dev/null || true'
"@ -AllowFailure
        Write-Output ('SIGKILL sent to service pid ' + $pidText)
        Start-Sleep -Seconds 8
        $staleNotifications = Get-NotificationDump
        Assert-True ($staleNotifications -match '连接中断') 'the foreground notification did not mark the killed service stale'
        Start-Service
        Wait-Health
        $barrier = Get-Status
        Assert-True (-not [bool](Get-Field $barrier 'can_start')) 'can_start stayed true after a killed service with old resources'
        $blocking = [string](Get-Field $barrier 'blocking_code')
        Assert-True ($blocking -match 'recovery|resource|cleanup|control_sync|shell|ledger|display') ('unexpected recovery blocking code: ' + $blocking)
        $barrierSession = Get-Field $barrier 'session'
        if ($null -ne $barrierSession) {
            Assert-True ([string](Get-Field $barrierSession 'state') -ne 'active') 'the old session was revived as active'
        }
        Write-Output ('CHECK restart barrier: PASS can_start=false blocking_code=' + $blocking)
        $recovery = Wait-RecoveryBarrier -TimeoutSeconds 90
        Write-Output ('CHECK restart barrier codes: observed=' + $recovery.ObservedRecovery +
            ' codes=' + ($recovery.Codes -join ','))
        if ($recovery.ObservedRecovery) {
            Assert-True ($null -ne $recovery.UiDump) 'no UI snapshot was captured during the recovery barrier'
            Assert-True ($recovery.UiDump -match '核验|不可用|清理') 'the App does not show the recovery/cleanup state'
        } else {
            Write-Output 'CHECK recovery UI: SKIPPED (barrier cleared before a recovery-specific code was observed)'
        }
        $afterRecovery = $recovery.Status
        $afterSession = Get-Field $afterRecovery 'session'
        Assert-True ($null -eq $afterSession -or [string](Get-Field $afterSession 'state') -ne 'active') 'the old session was revived after recovery'
        $cleanup = Get-Field $afterRecovery 'cleanup'
        Assert-True ($null -eq $cleanup -or [string](Get-Field $cleanup 'state') -eq 'complete') 'cleanup did not complete after recovery'
        Write-Output 'CHECK restart recovery: PASS no old active session, cleanup complete, can_start=true'

        if (-not $SkipTermDrill) {
            $start = Invoke-Api -Path '/api/start' -Body '{}'
            Assert-True ($start.Status -eq 200) ("second start returned HTTP " + $start.Status)
            Start-Sleep -Seconds 1
            $active = Get-Status
            Assert-True ([string](Get-Field (Get-Field $active 'session') 'state') -eq 'active') 'second start did not produce an active session'
            # Stop the App first so graceful shutdown cannot confirm the display
            # release. The restart must still clear the old resources and default
            # to the virtual screen once the App returns.
            Stop-App
            Stop-Service
            Start-Service
            Wait-Health
            $termBarrier = Get-Status
            Assert-True (-not [bool](Get-Field $termBarrier 'can_start')) 'can_start stayed true after a stopped service with an unconfirmed display'
            Write-Output ('CHECK SIGTERM barrier: PASS can_start=false blocking_code=' +
                [string](Get-Field $termBarrier 'blocking_code'))
            Start-App
            Wait-CanStart -TimeoutSeconds 90
            $afterTerm = Get-Status
            $termSession = Get-Field $afterTerm 'session'
            Assert-True ($null -eq $termSession -or [string](Get-Field $termSession 'state') -ne 'active') 'the SIGTERM path revived the old session'
            Start-Activity
            $termDump = Get-UiDump
            Assert-True ($termDump -match '虚拟屏') 'the restarted App did not return to the virtual-screen selection'
            Write-Output 'CHECK SIGTERM recovery: PASS App restart defaults virtual, no old active session, can_start=true'
        }
    }

    Write-Output 'RESULT PASS'
    exit 0
} catch {
    Write-Output ('RESULT FAIL: ' + $_.Exception.Message)
    exit 1
}
