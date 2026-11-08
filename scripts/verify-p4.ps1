#Requires -Version 5.1
<#
Tool-surface verification against the deployed device service.

    health/status -> apps -> diagnose -> notify -> start -> inspect -> keepalive
    -> open_app (create + reuse + unknown app) -> end -> cleanup verification
    -> optional shell (normal, output bound, timeout, STOP)

Requires the service deployed by scripts/deploy.ps1 and one online adb device.
The script never prints tokens, node trees, commands or shell output.

Exit codes: 0 = PASS/SKIP, 1 = FAIL, 2 = BLOCKED (can_start=false before work).
#>
param(
    [int]$HostPort = 0,
    [ValidateSet('All', 'Core', 'OpenApp', 'Shell', 'ShellRecovery')]
    [string]$Section = 'All',
    [int]$ShellTimeoutSeconds = 60
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$script:repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$script:stateDirectory = Join-Path $script:repository '.state'
$script:deployStatePath = Join-Path $script:stateDirectory 'deploy.json'
$script:versionsPath = Join-Path $script:repository 'release/versions.json'
if (-not (Test-Path -LiteralPath $script:versionsPath -PathType Leaf)) {
    throw "Version source is missing: $script:versionsPath"
}
$script:versions = Get-Content -LiteralPath $script:versionsPath -Raw | ConvertFrom-Json
$script:contractVersion = [string]$script:versions.contract_version
if ($script:contractVersion -notmatch '^[1-9][0-9]{0,2}$') {
    throw "contract_version must be a positive integer: $script:contractVersion"
}
$script:probePackage = 'com.mobileuse.probe'
$script:agentToken = ''
$script:baseUrl = ''
$script:hostPort = 0
$script:chroot = '/data/local/chroots/debian13'
$script:runId = ([guid]::NewGuid().ToString('N').Substring(0, 8))
$script:startSeq = 0
$script:activeStart = ''

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

function Assert-True {
    param([bool]$Condition, [string]$Label)
    if (-not $Condition) { Fail $Label }
}

function ConvertFrom-JsonSafe {
    param([string]$Text, [string]$Label)
    if ([string]::IsNullOrEmpty($Text)) { Fail "$Label was empty" }
    try { return ($Text | ConvertFrom-Json) } catch { Fail "$Label was not valid JSON" }
}

function Read-DeployState {
    if (-not (Test-Path -LiteralPath $script:deployStatePath -PathType Leaf)) {
        Fail 'missing .state/deploy.json (run scripts/deploy.ps1 first)'
    }
    $deploy = ConvertFrom-JsonSafe -Text (Get-Content -LiteralPath $script:deployStatePath -Raw) `
        -Label '.state/deploy.json'
    $token = Get-Field $deploy 'agent_token'
    if ($null -eq $token -or [string]$token -eq '') { Fail '.state/deploy.json has no agent_token' }
    $script:agentToken = [string]$token
    if ($HostPort -gt 0) {
        $script:hostPort = $HostPort
    } else {
        $port = Get-Field $deploy 'host_port'
        if ($null -eq $port) { Fail '.state/deploy.json has no host_port; pass -HostPort' }
        $script:hostPort = [int]$port
    }
    $chroot = Get-Field $deploy 'chroot'
    if ($null -ne $chroot -and [string]$chroot -ne '') { $script:chroot = [string]$chroot }
    $script:baseUrl = 'http://127.0.0.1:' + $script:hostPort
}

function Get-AgentHeaders {
    param([string]$Operation = '')
    $headers = @{
        Authorization           = 'Bearer ' + $script:agentToken
        'X-Mobile-Use-Contract' = $script:contractVersion
    }
    if ($Operation -ne '') { $headers['X-Mobile-Use-Operation'] = $Operation }
    return $headers
}

function Invoke-Http {
    param([string]$Url, [string]$Body = '{}', [hashtable]$Headers = @{}, [int]$TimeoutSeconds = 30)
    $request = [System.Net.HttpWebRequest]::Create($Url)
    $request.Method = 'POST'
    $request.Timeout = $TimeoutSeconds * 1000
    $request.ReadWriteTimeout = $TimeoutSeconds * 1000
    $request.Proxy = $null
    $request.KeepAlive = $false
    foreach ($name in $Headers.Keys) {
        $request.Headers.Add([string]$name, [string]$Headers[$name])
    }
    $payload = [System.Text.Encoding]::UTF8.GetBytes($Body)
    $request.ContentType = 'application/json; charset=utf-8'
    $request.ContentLength = $payload.Length
    $stream = $request.GetRequestStream()
    try { $stream.Write($payload, 0, $payload.Length) } finally { $stream.Dispose() }
    $response = $null
    try {
        $response = $request.GetResponse()
    } catch [System.Net.WebException] {
        $response = $_.Exception.Response
        if ($null -eq $response) { Fail ("cannot reach $Url (" + $_.Exception.Status + ")") }
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

function Invoke-Api {
    param([string]$Path, [string]$Body = '{}', [string]$Operation = '', [int]$TimeoutSeconds = 30)
    $response = Invoke-Http -Url ($script:baseUrl + $Path) -Body $Body `
        -Headers (Get-AgentHeaders -Operation $Operation) -TimeoutSeconds $TimeoutSeconds
    if ($response.Status -ne 200) {
        Fail "$Path returned HTTP $($response.Status): $($response.Text)"
    }
    return (ConvertFrom-JsonSafe -Text $response.Text -Label $Path)
}

function Assert-ApiError {
    param([string]$Path, [string]$Body, [string]$ExpectedCode, [int]$ExpectedStatus)
    $response = Invoke-Http -Url ($script:baseUrl + $Path) -Body $Body -Headers (Get-AgentHeaders)
    if ($response.Status -ne $ExpectedStatus) {
        Fail "$Path expected HTTP $ExpectedStatus, got $($response.Status)"
    }
    $parsed = ConvertFrom-JsonSafe -Text $response.Text -Label "$Path error"
    $code = [string](Get-Field (Get-Field $parsed 'error') 'code')
    if ($code -ne $ExpectedCode) { Fail "$Path expected error $ExpectedCode, got $code" }
}

function Get-Status {
    return (Invoke-Api -Path '/api/status' -Body '{}')
}

function Wait-Cleanup {
    param([int]$Seconds = 25)
    $deadline = (Get-Date).AddSeconds($Seconds)
    $last = $null
    while ((Get-Date) -lt $deadline) {
        $last = Get-Status
        $cleanup = Get-Field $last 'cleanup'
        $state = [string](Get-Field $cleanup 'state')
        if ($state -eq 'complete' -or $state -eq 'unconfirmed') { return $last }
        Start-Sleep -Milliseconds 300
    }
    return $last
}

function Assert-CleanComplete {
    param($Status)
    $cleanup = Get-Field $Status 'cleanup'
    Assert-True ([string](Get-Field $cleanup 'state') -eq 'complete') 'cleanup state is not complete'
    foreach ($field in @('execution', 'tasks', 'recents', 'display')) {
        $value = [string](Get-Field $cleanup $field)
        Assert-True ($value -eq 'confirmed' -or $value -eq 'not_applicable') "cleanup.$field=$value"
    }
    $canStart = Get-Field $Status 'can_start'
    Assert-True ($null -ne $canStart -and [bool]$canStart) 'can_start is not true after cleanup'
}

function Wait-CanStart {
    param([int]$Seconds = 12)
    $deadline = (Get-Date).AddSeconds($Seconds)
    $last = $null
    while ((Get-Date) -lt $deadline) {
        $last = Get-Status
        $canStart = Get-Field $last 'can_start'
        if ($null -ne $canStart -and [bool]$canStart) { return $last }
        Start-Sleep -Milliseconds 300
    }
    return $last
}

function Get-AdbPath {
    $command = Get-Command adb -ErrorAction SilentlyContinue
    if ($null -eq $command) { Fail 'adb is not on PATH' }
    return $command.Source
}

function Invoke-Adb {
    param([string[]]$Arguments)
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = Get-AdbPath
    $startInfo.Arguments = (($Arguments | ForEach-Object { if ($_ -match '\s') { '"' + $_ + '"' } else { $_ } }) -join ' ')
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.CreateNoWindow = $true
    $process = [System.Diagnostics.Process]::Start($startInfo)
    try {
        return (($process.StandardOutput.ReadToEnd() + $process.StandardError.ReadToEnd()).Trim())
    } finally {
        $process.WaitForExit()
        $process.Dispose()
    }
}

function Assert-NoAgentDisplay {
    $sf = Invoke-Adb -Arguments @('shell', 'dumpsys', 'SurfaceFlinger', '--display-id')
    Assert-True (-not ($sf -match 'MobileUseAgent')) 'SurfaceFlinger still has a MobileUseAgent display'
}

function Invoke-Stop {
    # Non-exported service: Android 16 requires the explicit component.
    $output = Invoke-Adb -Arguments @('shell', 'su', '-c',
        'am start-service -n com.mobileuse.vnext/.ControlService -a com.mobileuse.vnext.STOP')
    Assert-True ($output -match 'Starting service') 'STOP service action was not accepted'
}

function Invoke-DeviceScript {
    param([string]$Script)
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
        return ($stdout + $stderr).Trim()
    } finally {
        $process.Dispose()
    }
}

function Wait-Health {
    param([int]$Seconds = 30)
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $response = Invoke-WebRequest -Uri ($script:baseUrl + '/health') -UseBasicParsing -TimeoutSec 3
            if ($response.StatusCode -eq 200) { return $true }
        } catch {
            Start-Sleep -Milliseconds 300
        }
    }
    return $false
}

function Restart-Service {
    $null = Invoke-DeviceScript -Script @"
su -c 'pkill -9 -f "device-service --config /opt/mobile-use-vnext/config.json" || true'
"@
    Start-Sleep -Milliseconds 800
    $null = Invoke-DeviceScript -Script @"
su -c 'chroot $($script:chroot) /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh start"'
"@
    Assert-True (Wait-Health -Seconds 30) 'service did not come back after restart'
}

# ---------------------------------------------------------------- core tools
function Test-CoreTools {
    Write-Host 'core: health/status/apps/diagnose/notify'
    $status = Get-Status
    $canStart = Get-Field $status 'can_start'
    if ($null -eq $canStart -or -not [bool]$canStart) {
        Fail ("service is not admissible: " + [string](Get-Field $status 'blocking_code'))
    }

    $apps = Invoke-Api -Path '/api/apps' -Body '{}'
    $view = Get-Field $apps 'apps'
    Assert-True ($null -ne $view) 'apps response has no apps object'
    Assert-True ($null -ne (Get-Field $view 'items')) 'apps.items missing'
    Assert-True ($null -ne (Get-Field $view 'total')) 'apps.total missing'
    Assert-True ([string](Get-Field $view 'captured_at') -ne '') 'apps.captured_at missing'
    Assert-True ($null -eq (Get-Field $apps 'outcome')) 'apps must not report an outcome'
    Assert-True ([int](Get-Field $view 'total') -ge 1) 'apps.total is zero'

    $query = 'com.android'
    $probeInstalled = ((Invoke-Adb -Arguments @('shell', 'pm', 'path', $script:probePackage)) -match 'package:')
    if ($probeInstalled) { $query = $script:probePackage }
    $filtered = Invoke-Api -Path '/api/apps' -Body ('{"query":"' + $query + '"}')
    $filteredView = Get-Field $filtered 'apps'
    Assert-True ([int](Get-Field $filteredView 'total') -ge 1) 'apps query matched nothing'
    if ($probeInstalled) {
        $names = @()
        foreach ($item in @(Get-Field $filteredView 'items')) {
            $names += [string](Get-Field $item 'package')
        }
        Assert-True ($names -contains $script:probePackage) 'apps query did not return the probe package'
    }
    Assert-ApiError -Path '/api/apps' -Body '{"query":"x","limit":1}' `
        -ExpectedCode 'invalid_request' -ExpectedStatus 422

    $diag = Invoke-Api -Path '/api/diagnose' -Body '{}'
    $diagnosis = Get-Field $diag 'diagnosis'
    Assert-True ($null -ne $diagnosis) 'diagnose has no diagnosis object'
    Assert-True ([string](Get-Field $diagnosis 'coverage') -ne '') 'diagnose.coverage missing'

    $notify = Invoke-Api -Path '/api/notify' -Body '{"text":"tool verify notify"}' `
        -Operation ('Pverify-notify-' + $script:runId)
    Assert-True ([string](Get-Field $notify 'outcome') -eq 'executed') 'notify outcome is not executed'
    Assert-True ($null -ne (Get-Field $notify 'saved') -and [bool](Get-Field $notify 'saved')) 'notify.saved is not true'
    Assert-True ([string](Get-Field $notify 'message_id') -ne '') 'notify.message_id missing'
    $notification = Get-Field $notify 'notification'
    $nstate = [string](Get-Field $notification 'state')
    Assert-True ($nstate -in @('pending', 'submitted', 'unavailable')) "notify.notification.state=$nstate"
    Write-Host 'core: PASS'
}

# ---------------------------------------------------------------- session/inspect/keepalive
function Start-VerifySession {
    $script:startSeq++
    $start = Invoke-Api -Path '/api/start' -Body '{}' `
        -Operation ('Pverify-start-' + $script:runId + '-' + [string]$script:startSeq)
    Assert-True ([string](Get-Field $start 'outcome') -eq 'executed') 'start outcome is not executed'
    $observation = Get-Field $start 'observation'
    Assert-True ($null -ne $observation) 'start returned no observation'
    $tree = Get-Field $observation 'tree'
    Assert-True ([string](Get-Field $tree 'format') -eq 'full') 'start observation is not full'
    $script:activeStart = [string](Get-Field $start 'operation_id')
    return $start
}

function Get-FirstNodeRef {
    param($Root)
    $children = Get-Field $Root 'children'
    if ($null -eq $children) { return $null }
    foreach ($child in @($children)) {
        $role = [string](Get-Field $child 'role')
        $ref = [string](Get-Field $child 'ref')
        if ($ref -ne '' -and $role -notin @('root', 'group') -and $null -ne (Get-Field $child 'bounds')) {
            return $ref
        }
        $found = Get-FirstNodeRef -Root $child
        if ($null -ne $found) { return $found }
    }
    return $null
}

function Test-SessionTools {
    Write-Host 'session: start/inspect/keepalive/diagnose'
    $start = Start-VerifySession
    $observation = Get-Field $start 'observation'
    $oid = [string](Get-Field $observation 'id')

    $inspect = Invoke-Api -Path '/api/inspect' -Body ('{"observation_id":"' + $oid + '"}')
    $inspection = Get-Field $inspect 'inspection'
    Assert-True ([string](Get-Field $inspection 'scope') -eq 'tree') 'inspect scope is not tree'
    Assert-True ($null -ne (Get-Field (Get-Field $inspection 'tree') 'root')) 'inspect tree root missing'

    $ref = Get-FirstNodeRef -Root (Get-Field (Get-Field $inspection 'tree') 'root')
    if ($null -ne $ref) {
        $sub = Invoke-Api -Path '/api/inspect' -Body ('{"observation_id":"' + $oid + '","node":"' + $ref + '"}')
        $subInspection = Get-Field $sub 'inspection'
        Assert-True ([string](Get-Field $subInspection 'scope') -eq 'subtree') 'inspect node scope is not subtree'
        Assert-True ([string](Get-Field (Get-Field $subInspection 'root') 'ref') -eq $ref) 'inspect subtree ref mismatch'
    }

    $keep = Invoke-Api -Path '/api/keepalive' -Body ('{"reference":"' + $oid + '"}') `
        -Operation ('Pverify-keep-' + $script:runId)
    Assert-True ([string](Get-Field $keep 'outcome') -eq 'executed') 'keepalive outcome is not executed'
    $session = Get-Field $keep 'session'
    Assert-True ([string](Get-Field $session 'expires_at') -ne '') 'keepalive session.expires_at missing'

    $diag = Invoke-Api -Path '/api/diagnose' -Body ('{"reference":"' + $oid + '"}')
    $diagnosis = Get-Field $diag 'diagnosis'
    Assert-True ([string](Get-Field $diagnosis 'coverage') -eq 'original_record') 'diagnose(O) coverage is not original_record'

    Write-Host 'session: PASS'
    return $start
}

# ---------------------------------------------------------------- open_app + cleanup
function Test-OpenApp {
    Write-Host 'open_app: create/reuse/unknown/cleanup'
    $probe = ((Invoke-Adb -Arguments @('shell', 'pm', 'path', $script:probePackage)) -match 'package:')
    if (-not $probe) { Write-Host 'open_app: SKIP (probe package not installed)'; return }

    $start = Start-VerifySession
    $oid = [string](Get-Field (Get-Field $start 'observation') 'id')

    $missing = Invoke-Http -Url ($script:baseUrl + '/api/open_app') `
        -Body ('{"observation_id":"' + $oid + '","app":"com.mobileuse.missing.package"}') `
        -Headers (Get-AgentHeaders -Operation ('Pverify-open-missing-' + $script:runId))
    Assert-True ($missing.Status -eq 409) "open_app missing app expected 409, got $($missing.Status)"
    $missingBody = ConvertFrom-JsonSafe -Text $missing.Text -Label 'open_app missing'
    Assert-True ([string](Get-Field (Get-Field $missingBody 'error') 'code') -eq 'app_not_found') 'open_app missing app code'

    $open = Invoke-Api -Path '/api/open_app' `
        -Body ('{"observation_id":"' + $oid + '","app":"' + $script:probePackage + '"}') `
        -Operation ('Pverify-open-1-' + $script:runId)
    Assert-True ([string](Get-Field $open 'outcome') -eq 'executed') 'open_app outcome is not executed'
    $app = Get-Field $open 'app'
    $task1 = [int](Get-Field $app 'task_id')
    $display = [int](Get-Field $app 'display_id')
    Assert-True ($task1 -gt 0) 'open_app task_id missing'
    Assert-True ($display -gt 0) 'open_app display_id is not a virtual display'
    Assert-True (-not [bool](Get-Field $app 'reused')) 'first open_app unexpectedly reused a task'
    $post = Get-Field $open 'observation'
    Assert-True ([string](Get-Field $post 'status') -eq 'complete') 'open_app post observation is not complete'
    Assert-True ([string](Get-Field (Get-Field $post 'tree') 'format') -eq 'full') 'open_app post observation is not full'
    $oid2 = [string](Get-Field $post 'id')

    $reuse = Invoke-Api -Path '/api/open_app' `
        -Body ('{"observation_id":"' + $oid2 + '","app":"' + $script:probePackage + '"}') `
        -Operation ('Pverify-open-2-' + $script:runId)
    $reuseApp = Get-Field $reuse 'app'
    Assert-True ([string](Get-Field $reuse 'outcome') -eq 'executed') 'reuse outcome is not executed'
    Assert-True ([bool](Get-Field $reuseApp 'reused')) 'second open_app did not reuse'
    Assert-True ([int](Get-Field $reuseApp 'task_id') -eq $task1) 'reused task id changed'

    $end = Invoke-Api -Path '/api/end' -Body ('{"reference":"' + [string](Get-Field $start 'operation_id') + '"}') `
        -Operation ('Pverify-end-' + $script:runId)
    Assert-True ([string](Get-Field $end 'outcome') -eq 'executed') 'end outcome is not executed'
    $final = Wait-Cleanup
    Assert-CleanComplete -Status $final
    $script:activeStart = ''
    Assert-NoAgentDisplay

    # User STOP with an owned window: revoke, remove the task, release display.
    $stopStart = Start-VerifySession
    $stopOid = [string](Get-Field (Get-Field $stopStart 'observation') 'id')
    $stopOpen = Invoke-Api -Path '/api/open_app' `
        -Body ('{"observation_id":"' + $stopOid + '","app":"' + $script:probePackage + '"}') `
        -Operation ('Pverify-stop-open-' + $script:runId)
    Assert-True ([string](Get-Field $stopOpen 'outcome') -eq 'executed') 'STOP scene open_app failed'
    Invoke-Stop
    $stopped = Wait-Cleanup -Seconds 30
    Assert-True ([string](Get-Field (Get-Field $stopped 'cleanup') 'state') -eq 'complete') 'STOP cleanup did not complete'
    $stopped = Wait-CanStart -Seconds 12
    Assert-True ([bool](Get-Field $stopped 'can_start')) 'STOP did not restore can_start'
    $script:activeStart = ''
    Assert-NoAgentDisplay
    Write-Host 'open_app: PASS'
}

# ---------------------------------------------------------------- shell
function Test-Shell {
    Write-Host 'shell: normal/output/exit/timeout'
    $probe = Invoke-Http -Url ($script:baseUrl + '/api/shell') `
        -Body '{"reference":"Pdoesnotexist","command":"true"}' -Headers (Get-AgentHeaders)
    if ($probe.Status -eq 404) {
        Write-Host 'shell: SKIP (route not deployed)'
        return
    }
    if ($probe.Status -ne 409) {
        Fail "shell route probe expected 409, got $($probe.Status)"
    }

    $start = Start-VerifySession
    $oid = [string](Get-Field (Get-Field $start 'observation') 'id')

    $normal = Invoke-Api -Path '/api/shell' `
        -Body ('{"reference":"' + $oid + '","command":"printf ''shell-ok\\n''"}') `
        -Operation ('Pverify-shell-normal-' + $script:runId) -TimeoutSeconds 60
    $normalShell = Get-Field $normal 'shell'
    Assert-True ([string](Get-Field $normal 'outcome') -eq 'executed') 'shell normal outcome'
    Assert-True ([string](Get-Field $normalShell 'termination') -eq 'exited') 'shell normal termination'
    Assert-True (([int](Get-Field $normalShell 'exit_code')) -eq 0) 'shell normal exit code'
    Assert-True (-not [bool](Get-Field $normalShell 'truncated')) 'shell normal truncated'
    Assert-True ([string](Get-Field $normalShell 'output') -like '*shell-ok*') 'shell normal output'
    $post = Get-Field $normal 'observation'
    Assert-True ([string](Get-Field $post 'status') -eq 'complete') 'shell normal post observation'
    Assert-True ([string](Get-Field (Get-Field $post 'tree') 'format') -eq 'full') 'shell normal post tree is not full'
    $oid = [string](Get-Field $post 'id')

    $big = Invoke-Api -Path '/api/shell' `
        -Body ('{"reference":"' + $oid + '","command":"i=0; while [ $i -lt 20000 ]; do echo 0123456789; i=$((i+1)); done"}') `
        -Operation ('Pverify-shell-big-' + $script:runId) -TimeoutSeconds 60
    $bigShell = Get-Field $big 'shell'
    $bigOutput = [string](Get-Field $bigShell 'output')
    Assert-True ([string](Get-Field $big 'outcome') -eq 'executed') 'shell big outcome'
    Assert-True ([string](Get-Field $bigShell 'termination') -eq 'exited') 'shell big termination'
    Assert-True ($bigOutput.Length -eq 65536) ("shell big output length=" + $bigOutput.Length)
    Assert-True ([bool](Get-Field $bigShell 'truncated')) 'shell big truncated'
    $post = Get-Field $big 'observation'
    Assert-True ([string](Get-Field $post 'status') -eq 'complete') 'shell big post observation'
    $oid = [string](Get-Field $post 'id')

    $self124 = Invoke-Api -Path '/api/shell' `
        -Body ('{"reference":"' + $oid + '","command":"exit 124"}') `
        -Operation ('Pverify-shell-124-' + $script:runId) -TimeoutSeconds 60
    $s124 = Get-Field $self124 'shell'
    Assert-True ([string](Get-Field $self124 'outcome') -eq 'executed') 'shell self124 outcome'
    Assert-True ([string](Get-Field $s124 'termination') -eq 'exited') 'shell self124 must not be timeout'
    Assert-True (([int](Get-Field $s124 'exit_code')) -eq 124) 'shell self124 exit code'
    $post = Get-Field $self124 'observation'
    Assert-True ([string](Get-Field $post 'status') -eq 'complete') 'shell self124 post observation'
    $oid = [string](Get-Field $post 'id')

    $timeout = Invoke-Api -Path '/api/shell' `
        -Body ('{"reference":"' + $oid + '","command":"sleep 40"}') `
        -Operation ('Pverify-shell-timeout-' + $script:runId) -TimeoutSeconds 75
    $timeoutShell = Get-Field $timeout 'shell'
    Assert-True ([string](Get-Field $timeout 'outcome') -eq 'executed') 'shell timeout outcome'
    Assert-True ([string](Get-Field $timeoutShell 'termination') -eq 'timeout') 'shell timeout termination'
    Assert-True ($null -eq (Get-Field $timeoutShell 'exit_code')) 'shell timeout exit code must be null'
    $post = Get-Field $timeout 'observation'
    Assert-True ([string](Get-Field $post 'status') -eq 'complete') 'shell timeout post observation'
    $oid = [string](Get-Field $post 'id')

    $end = Invoke-Api -Path '/api/end' -Body ('{"reference":"' + [string](Get-Field $start 'operation_id') + '"}') `
        -Operation ('Pverify-shell-end-' + $script:runId)
    Assert-True ([string](Get-Field $end 'outcome') -eq 'executed') 'shell end outcome'
    $final = Wait-Cleanup
    Assert-CleanComplete -Status $final
    $script:activeStart = ''
    Assert-NoAgentDisplay

    # STOP while a managed command is still running.
    Write-Host 'shell: STOP'
    $stopStart = Start-VerifySession
    $stopOid = [string](Get-Field (Get-Field $stopStart 'observation') 'id')
    $job = Start-Job -ScriptBlock {
        param($url, $token, $body, $op, $contract)
        $request = [System.Net.HttpWebRequest]::Create($url)
        $request.Method = 'POST'
        $request.Timeout = 60000
        $request.ReadWriteTimeout = 60000
        $request.Proxy = $null
        $request.Headers.Add('Authorization', 'Bearer ' + $token)
        $request.Headers.Add('X-Mobile-Use-Contract', $contract)
        $request.Headers.Add('X-Mobile-Use-Operation', $op)
        $payload = [System.Text.Encoding]::UTF8.GetBytes($body)
        $request.ContentType = 'application/json; charset=utf-8'
        $request.ContentLength = $payload.Length
        $stream = $request.GetRequestStream()
        try { $stream.Write($payload, 0, $payload.Length) } finally { $stream.Dispose() }
        try {
            $response = $request.GetResponse()
        } catch [System.Net.WebException] {
            $response = $_.Exception.Response
        }
        try {
            $status = [int]$response.StatusCode
            $reader = New-Object System.IO.StreamReader($response.GetResponseStream(), [System.Text.Encoding]::UTF8)
            try { $text = $reader.ReadToEnd() } finally { $reader.Dispose() }
        } finally { $response.Dispose() }
        return [pscustomobject]@{ Status = $status; Text = $text }
    } -ArgumentList @(
        ($script:baseUrl + '/api/shell'),
        $script:agentToken,
        ('{"reference":"' + $stopOid + '","command":"sleep 40"}'),
        ('Pverify-shell-stop-' + $script:runId),
        $script:contractVersion
    )
    Start-Sleep -Milliseconds 2500
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    Invoke-Stop
    $stopwatch.Stop()
    Assert-True ($stopwatch.ElapsedMilliseconds -lt 5000) ("STOP took " + $stopwatch.ElapsedMilliseconds + " ms")
    $null = Wait-Job -Job $job -Timeout 45
    $received = @(Receive-Job -Job $job)
    $shellStop = $received | Where-Object { $null -ne $_.Status } | Select-Object -First 1
    Remove-Job -Job $job -Force
    Assert-True ($null -ne $shellStop -and [int]$shellStop.Status -eq 200) 'shell STOP response status'
    $stopBody = ConvertFrom-JsonSafe -Text $shellStop.Text -Label 'shell STOP'
    $stopShell = Get-Field $stopBody 'shell'
    $stopOutcome = [string](Get-Field $stopBody 'outcome')
    Assert-True ($stopOutcome -in @('executed', 'uncertain')) "shell STOP outcome=$stopOutcome"
    Assert-True ($null -ne $stopShell) 'shell STOP must include a shell object'
    Assert-True ([string](Get-Field $stopShell 'termination') -in @('cancelled', 'exited', 'unknown')) 'shell STOP termination'
    $stopped = Wait-Cleanup -Seconds 30
    Assert-True ([string](Get-Field (Get-Field $stopped 'cleanup') 'state') -eq 'complete') 'shell STOP cleanup did not complete'
    $stopped = Wait-CanStart -Seconds 12
    Assert-True ([bool](Get-Field $stopped 'can_start')) 'shell STOP did not restore can_start'
    $script:activeStart = ''
    Assert-NoAgentDisplay
    Write-Host 'shell: PASS'
}

# ---------------------------------------------------------------- shell recovery
function Test-ShellRecovery {
    Write-Host 'shell recovery: SIGKILL + restart'
    $start = Start-VerifySession
    $oid = [string](Get-Field (Get-Field $start 'observation') 'id')
    $operation = 'Pverify-shell-recovery-' + $script:runId
    $job = Start-Job -ScriptBlock {
        param($url, $token, $body, $op, $contract)
        $request = [System.Net.HttpWebRequest]::Create($url)
        $request.Method = 'POST'
        $request.Timeout = 90000
        $request.ReadWriteTimeout = 90000
        $request.Proxy = $null
        $request.Headers.Add('Authorization', 'Bearer ' + $token)
        $request.Headers.Add('X-Mobile-Use-Contract', $contract)
        $request.Headers.Add('X-Mobile-Use-Operation', $op)
        $payload = [System.Text.Encoding]::UTF8.GetBytes($body)
        $request.ContentType = 'application/json; charset=utf-8'
        $request.ContentLength = $payload.Length
        $stream = $request.GetRequestStream()
        try { $stream.Write($payload, 0, $payload.Length) } finally { $stream.Dispose() }
        try {
            $response = $request.GetResponse()
            try { $reader = New-Object System.IO.StreamReader($response.GetResponseStream(), [System.Text.Encoding]::UTF8) } finally { }
            try { $text = $reader.ReadToEnd() } finally { $reader.Dispose() }
            $response.Dispose()
            return 'completed'
        } catch {
            return 'connection_lost'
        }
    } -ArgumentList @(
        ($script:baseUrl + '/api/shell'),
        $script:agentToken,
        ('{"reference":"' + $oid + '","command":"sleep 60"}'),
        $operation,
        $script:contractVersion
    )
    Start-Sleep -Milliseconds 3000
    $dirs = Invoke-DeviceScript -Script 'ls -d /data/local/tmp/mobile-use-shell/* 2>/dev/null || true'
    Assert-True ($dirs -match 'mobile-use-shell') 'shell runner directory was not created before restart'
    Restart-Service
    $script:activeStart = ''
    $sawBlocked = $false
    $ready = $null
    $deadline = (Get-Date).AddSeconds(90)
    while ((Get-Date) -lt $deadline) {
        $status = Get-Status
        $canStart = Get-Field $status 'can_start'
        if ($null -eq $canStart) { Start-Sleep -Milliseconds 500; continue }
        if (-not [bool]$canStart) {
            $sawBlocked = $true
        } else {
            $ready = $status
            break
        }
        Start-Sleep -Milliseconds 500
    }
    Assert-True ($sawBlocked) 'recovery never exposed a closed admission barrier'
    Assert-True ($null -ne $ready) 'recovery did not restore can_start'
    Assert-True ([string](Get-Field (Get-Field $ready 'cleanup') 'state') -eq 'complete') 'recovery cleanup is not complete'
    $leftover = Invoke-DeviceScript -Script 'ls -d /data/local/tmp/mobile-use-shell/* 2>/dev/null || true'
    Assert-True ($leftover -notmatch 'mobile-use-shell') 'recovery left a runner directory behind'
    $old = Invoke-Http -Url ($script:baseUrl + '/api/status') `
        -Body ('{"reference":"' + $operation + '"}') `
        -Headers (Get-AgentHeaders -Operation ('Pverify-shell-recovery-status-' + $script:runId))
    Assert-True ($old.Status -eq 200 -or $old.Status -eq 504) "old shell status HTTP $($old.Status)"
    $oldBody = ConvertFrom-JsonSafe -Text $old.Text -Label 'shell recovery status'
    Assert-True ([string](Get-Field $oldBody 'operation_state') -eq 'finished') 'old shell operation is not finished'
    Assert-True ([string](Get-Field $oldBody 'phase') -eq 'previous_instance') 'old shell operation phase is not previous_instance'
    $null = Wait-Job -Job $job -Timeout 10
    $null = Receive-Job -Job $job
    Remove-Job -Job $job -Force
    Assert-NoAgentDisplay
    Write-Host 'shell recovery: PASS'
}
try {
    Read-DeployState
    switch ($Section) {
        'Core' { Test-CoreTools }
        'OpenApp' { Test-OpenApp }
        'Shell' { Test-Shell }
        'ShellRecovery' { Test-ShellRecovery }
        'All' {
            Test-CoreTools
            Test-OpenApp
            Test-Shell
        }
    }
    Write-Host 'PASS verify-p4'
    exit 0
} catch {
    if ($script:activeStart -ne '') {
        try {
            $cleanupBody = '{"reference":"' + $script:activeStart + '"}'
            $null = Invoke-Http -Url ($script:baseUrl + '/api/end') -Body $cleanupBody `
                -Headers (Get-AgentHeaders -Operation ('Pverify-cleanup-' + $script:runId))
        } catch {
            # Best effort only; the original failure is reported below.
        }
    }
    Write-Host ('FAIL verify-p4: ' + $_.Exception.Message)
    exit 1
}
