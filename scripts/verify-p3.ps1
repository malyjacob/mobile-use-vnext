#Requires -Version 5.1
<#
P3 writable-loop verification against the deployed device service.

Drives one complete cycle and asserts the observable contract at every step:

    health/status -> start -> probe launch -> observe -> tap -> observe
    -> text -> u2 readback -> task removal -> end -> cleanup verification

The service must already be deployed by scripts/deploy.ps1: the chroot
/data/local/chroots/debian13 runs /opt/mobile-use-vnext on 18880, the host has
`adb forward tcp:<host_port> tcp:<app_port>`, and .state/deploy.json holds
agent_token/host_port. The script never prints tokens, trees or service logs.

Exit codes: 0 = PASS, 1 = FAIL, 2 = BLOCKED (can_start=false, nothing started).
#>
param(
    [int]$HostPort = 0,
    [switch]$SkipBuildProbe,
    [int]$DisplayId = -1,
    [switch]$KeepSession
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# ---------------------------------------------------------------- fixed paths
$script:repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$script:stateDirectory = Join-Path $script:repository '.state'
$script:deployStatePath = Join-Path $script:stateDirectory 'deploy.json'
$script:versionsPath = Join-Path $script:repository 'release/versions.json'
$script:probeApkPath = Join-Path $script:repository 'android-control/build-probe/probe.apk'
$script:probePackage = 'com.mobileuse.probe'
$script:probeActivity = 'com.mobileuse.probe/.ProbeActivity'
if (-not (Test-Path -LiteralPath $script:versionsPath -PathType Leaf)) {
    throw "Version source is missing: $script:versionsPath"
}
$script:versions = Get-Content -LiteralPath $script:versionsPath -Raw | ConvertFrom-Json
$script:contractVersion = [string]$script:versions.contract_version
if ($script:contractVersion -notmatch '^[1-9][0-9]{0,2}$') {
    throw "contract_version must be a positive integer: $script:contractVersion"
}
$script:u2HostPort = 19008
$script:u2DevicePort = 9008
$script:textExpected = '你好，世界！'

# ---------------------------------------------------------------- run state
$script:adbPath = $null
$script:agentToken = ''
$script:baseUrl = ''
$script:apiHostPort = 0
$script:appPort = 0
$script:chroot = '/data/local/chroots/debian13'
$script:deviceStateDirectory = '/var/lib/mobile-use-vnext'
$script:requestedHostPort = 0
$script:requestedDisplayId = -1
$script:skipProbeBuild = [bool]$SkipBuildProbe
$script:keepScene = [bool]$KeepSession
$script:targetDisplayId = -1
$script:sessionOrigin = $null
$script:sessionEnded = $false
$script:u2Forwarded = $false

if ($PSBoundParameters.ContainsKey('HostPort')) {
    $script:requestedHostPort = $HostPort
}
if ($PSBoundParameters.ContainsKey('DisplayId')) {
    $script:requestedDisplayId = $DisplayId
}

# ---------------------------------------------------------------- primitives
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

function Assert-EqualText {
    param($Actual, [string]$Expected, [string]$Label)
    $actualText = '<null>'
    if ($null -ne $Actual) { $actualText = [string]$Actual }
    if ($actualText -ne $Expected) {
        Fail "$Label expected '$Expected' but was '$actualText'"
    }
}

function ConvertTo-JsonString {
    param([string]$Value)
    return (ConvertTo-Json -InputObject $Value -Compress)
}

function ConvertFrom-JsonSafe {
    param([string]$Text, [string]$Label)
    if ([string]::IsNullOrEmpty($Text)) { Fail "$Label was empty" }
    try {
        return ($Text | ConvertFrom-Json)
    } catch {
        Fail "$Label was not valid JSON"
    }
}

function Get-AdbPath {
    if ($null -eq $script:adbPath) {
        $command = Get-Command adb -ErrorAction SilentlyContinue
        if ($null -eq $command) { Fail 'adb is not on PATH' }
        $script:adbPath = $command.Source
    }
    return $script:adbPath
}

# Same Process plumbing as scripts/deploy.ps1: adb writes diagnostics to stderr
# and PowerShell must never turn that into an exception by itself.
function Invoke-AdbCapture {
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
        return [pscustomobject]@{
            ExitCode = $process.ExitCode
            Output   = ($stdout + $stderr).Trim()
        }
    } finally {
        $process.Dispose()
    }
}

function Invoke-Adb {
    param([string[]]$Arguments, [switch]$AllowFailure)
    $result = Invoke-AdbCapture -Arguments $Arguments
    if (-not $AllowFailure -and $result.ExitCode -ne 0) {
        throw ('adb ' + ($Arguments -join ' ') + ' exited with code ' + $result.ExitCode)
    }
    return $result.Output
}

# Device shell scripts are sent over stdin. Windows argv quoting must not
# reinterpret the device shell's inner quotes.
function Invoke-DeviceScript {
    param([string]$Script)
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = Get-AdbPath
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
            throw "device script failed ($($process.ExitCode))"
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

# ---------------------------------------------------------------- HTTP
# Byte-level UTF-8 on both directions: PowerShell 5.1 would otherwise decode a
# charset-less JSON body as Latin-1 and corrupt non-ASCII labels/text.
function Read-ResponseText {
    param([System.Net.WebResponse]$Response)
    $stream = $Response.GetResponseStream()
    $memory = New-Object System.IO.MemoryStream
    try {
        $buffer = New-Object byte[] 16384
        while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
            $memory.Write($buffer, 0, $read)
        }
        $payload = $memory.ToArray()
    } finally {
        $memory.Dispose()
        $stream.Dispose()
    }
    return [System.Text.Encoding]::UTF8.GetString($payload)
}

function Invoke-Http {
    param(
        [string]$Url,
        [string]$Method = 'POST',
        [string]$Body = '{}',
        [hashtable]$Headers = @{},
        [int]$TimeoutSeconds = 30
    )
    $request = [System.Net.HttpWebRequest]::Create($Url)
    $request.Method = $Method
    $request.Timeout = $TimeoutSeconds * 1000
    $request.ReadWriteTimeout = $TimeoutSeconds * 1000
    $request.Proxy = $null
    $request.KeepAlive = $false
    foreach ($name in $Headers.Keys) {
        $request.Headers.Add([string]$name, [string]$Headers[$name])
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
            Fail ("cannot reach $Url (" + $_.Exception.Status + ")")
        }
    }
    try {
        $statusCode = [int]$response.StatusCode
        $text = Read-ResponseText -Response $response
    } finally {
        $response.Dispose()
    }
    return [pscustomobject]@{ StatusCode = $statusCode; Text = $text }
}

function Get-AgentHeaders {
    return @{
        Authorization           = 'Bearer ' + $script:agentToken
        'X-Mobile-Use-Contract' = $script:contractVersion
    }
}

# Error bodies carry {error:{code,message}}; both are controlled short strings.
function Get-ApiErrorDetail {
    param($Response)
    try {
        $envelope = ConvertFrom-JsonSafe -Text $Response.Text -Label 'error body'
        $errorObject = Get-Field $envelope 'error'
        $detail = ''
        $code = Get-Field $errorObject 'code'
        $message = Get-Field $errorObject 'message'
        if ($null -ne $code) { $detail += " code=$code" }
        if ($null -ne $message) { $detail += " message=$message" }
        return $detail
    } catch {
        return ''
    }
}

function Invoke-AgentApi {
    param([string]$Path, [string]$Body = '{}', [int]$TimeoutSeconds = 30)
    $response = Invoke-Http -Url ($script:baseUrl + $Path) -Method 'POST' -Body $Body `
        -Headers (Get-AgentHeaders) -TimeoutSeconds $TimeoutSeconds
    if ($response.StatusCode -ne 200) {
        Fail ("$Path returned HTTP $($response.StatusCode)" + (Get-ApiErrorDetail -Response $response))
    }
    return (ConvertFrom-JsonSafe -Text $response.Text -Label $Path)
}

# ---------------------------------------------------------------- deploy state
function Read-DeployState {
    if (-not (Test-Path -LiteralPath $script:deployStatePath -PathType Leaf)) {
        Fail 'missing .state/deploy.json (run scripts/deploy.ps1 first)'
    }
    $deploy = ConvertFrom-JsonSafe -Text (Get-Content -LiteralPath $script:deployStatePath -Raw) `
        -Label '.state/deploy.json'
    $token = Get-Field $deploy 'agent_token'
    if ($null -eq $token -or [string]$token -eq '') { Fail '.state/deploy.json has no agent_token' }
    $script:agentToken = [string]$token

    $statePort = Get-Field $deploy 'host_port'
    if ($script:requestedHostPort -gt 0) {
        $script:apiHostPort = $script:requestedHostPort
    } elseif ($null -ne $statePort) {
        $script:apiHostPort = [int]$statePort
    } else {
        Fail '.state/deploy.json has no host_port; pass -HostPort'
    }
    $appPort = Get-Field $deploy 'app_port'
    if ($null -ne $appPort) { $script:appPort = [int]$appPort }
    $chroot = Get-Field $deploy 'chroot'
    if ($null -ne $chroot -and [string]$chroot -ne '') { $script:chroot = [string]$chroot }
    $script:baseUrl = 'http://127.0.0.1:' + $script:apiHostPort
    return $deploy
}

# Optional: the staged config names the device state dir and u2 port.
function Read-DeviceConfig {
    $configPath = Join-Path $script:stateDirectory 'config.json'
    if (-not (Test-Path -LiteralPath $configPath -PathType Leaf)) { return }
    try {
        $config = ConvertFrom-JsonSafe -Text (Get-Content -LiteralPath $configPath -Raw) `
            -Label '.state/config.json'
    } catch {
        return
    }
    $stateDir = Get-Field $config 'state_dir'
    if ($null -ne $stateDir -and [string]$stateDir -ne '') {
        $script:deviceStateDirectory = [string]$stateDir
    }
    $u2Port = Get-Field (Get-Field $config 'adb') 'u2_port'
    if ($null -ne $u2Port) {
        $port = [int]$u2Port
        if ($port -gt 0) { $script:u2DevicePort = $port }
    }
}

# ---------------------------------------------------------------- display id
# The public start/observe responses carry no display id. The service logs
# "control.display_reported display_id=<n>" when the control App reports the
# virtual display; the log lives inside the chroot and is root-owned.
function Resolve-DisplayId {
    $logPath = $script:chroot.TrimEnd('/') + $script:deviceStateDirectory + '/device-service.log'
    $deviceScript = "su -c 'tail -n 4000 " + $logPath + " 2>/dev/null'"
    for ($attempt = 1; $attempt -le 10; $attempt++) {
        $log = Invoke-DeviceScriptAllowFailure -Script $deviceScript
        $found = -1
        foreach ($line in ($log -split "`n")) {
            $match = [regex]::Match($line, 'control\.display_reported\b.*\bdisplay_id=(\d+)')
            if ($match.Success) { $found = [int]$match.Groups[1].Value }
        }
        if ($found -ge 0) { return $found }
        Start-Sleep -Milliseconds 300
    }
    Fail ("cannot read display_id from the service log; pass -DisplayId (log: $logPath)")
}

# ---------------------------------------------------------------- tree helpers
function Get-ObservationRefs {
    param($Tree)
    $result = [pscustomobject]@{
        Button   = $null
        Input    = $null
        Counter  = $null
        TapCount = $null
        Nodes    = 0
    }
    $root = Get-Field $Tree 'root'
    if ($null -eq $root) { Fail 'observation tree has no root' }
    $queue = New-Object System.Collections.Queue
    $queue.Enqueue($root)
    while ($queue.Count -gt 0) {
        $node = $queue.Dequeue()
        $result.Nodes = $result.Nodes + 1
        $role = [string](Get-Field $node 'role')
        $ref = Get-Field $node 'ref'
        $labelText = ''
        foreach ($label in @(Get-Field $node 'label')) {
            if ($null -ne $label) { $labelText += [string]$label + "`n" }
        }
        if ($null -eq $result.Button -and $role -eq 'button' -and $labelText -match 'TAP') {
            $result.Button = $ref
        }
        if ($null -eq $result.Input -and $role -eq 'input') {
            $result.Input = $ref
        }
        $counter = [regex]::Match($labelText, 'taps:\s*(\d+)')
        if ($counter.Success) {
            $result.Counter = $ref
            $result.TapCount = [int]$counter.Groups[1].Value
        }
        foreach ($child in @(Get-Field $node 'children')) {
            if ($null -ne $child) { $queue.Enqueue($child) }
        }
    }
    return $result
}

function Invoke-FullObservation {
    param([string]$Reference, [string]$Label)
    $body = '{"reference":' + (ConvertTo-JsonString $Reference) + ',"full":true}'
    $result = Invoke-AgentApi -Path '/api/observe' -Body $body -TimeoutSeconds 60
    $observation = Get-Field $result 'observation'
    Assert-EqualText (Get-Field $observation 'status') 'complete' "$Label observation.status"
    Assert-EqualText (Get-Field (Get-Field $observation 'tree') 'format') 'full' "$Label tree.format"
    $id = Get-Field $observation 'id'
    if ($null -eq $id) { Fail "$Label has no observation.id" }
    return $observation
}

function Invoke-Act {
    param([string]$ObservationId, [string]$OperationJson, [string]$Label)
    $body = '{"observation_id":' + (ConvertTo-JsonString $ObservationId) + ',"operation":' + $OperationJson + '}'
    $result = Invoke-AgentApi -Path '/api/act' -Body $body -TimeoutSeconds 60
    Assert-EqualText (Get-Field $result 'outcome') 'executed' "$Label outcome"
    return $result
}

function Get-ForegroundTaskId {
    param($Observation)
    $foreground = Get-Field $Observation 'foreground'
    $state = Get-Field $foreground 'state'
    if ([string]$state -ne 'present') { return $null }
    # Never remove a task that the observation names as another package (for
    # example an IME). A missing package is tolerated; a different one is not.
    $package = [string](Get-Field $foreground 'package')
    if ($package -ne '' -and $package -ne $script:probePackage) { return $null }
    $taskId = Get-Field $foreground 'task_id'
    if ($null -eq $taskId) { return $null }
    $parsed = 0
    if (-not [int]::TryParse([string]$taskId, [ref]$parsed)) { return $null }
    return $parsed
}

# ------------------------------------------------------- device task probes
# `am stack remove` only returns the binder result: the device can keep listing
# the task for a few hundred milliseconds, and the service reads `am stack list`
# when it computes cleanup.tasks. Ending the session before the task left the
# display therefore left cleanup at tasks=unconfirmed. Both dumps below are
# parsed with the shapes measured in internal/android/dumpsys.go. `dumpsys
# activity activities` can also contain `mLastFocusedRootTask=Task{...#id}`
# residue, so only task header lines inside the target display's own section
# count; wrapper lines are never read as a live task.

# Get-ActivityDisplayTasks reads one display section of `dumpsys activity
# activities`. Known is false when the display section is absent, so an
# unreadable or truncated dump is not mistaken for an empty display.
function Get-ActivityDisplayTasks {
    param([string]$Dump, [int]$DisplayId)
    $ids = New-Object System.Collections.Generic.List[int]
    $inTarget = $false
    $sawTarget = $false
    foreach ($line in ($Dump -split "`n")) {
        $display = [regex]::Match($line, '^\s*Display(?:\s+#(\d+)\b|:\s*mDisplayId=(\d+)\b)')
        if ($display.Success) {
            $value = $display.Groups[1].Value
            if ($value -eq '') { $value = $display.Groups[2].Value }
            $inTarget = ([int]$value -eq $DisplayId)
            if ($inTarget) { $sawTarget = $true }
            continue
        }
        if (-not $inTarget) { continue }
        $task = [regex]::Match($line, '^\s*\*\s*Task\{[^}\r\n]*#(\d+)\b')
        if ($task.Success) { [void]$ids.Add([int]$task.Groups[1].Value) }
    }
    return [pscustomobject]@{ Known = $sawTarget; TaskIds = @($ids) }
}

# Get-StackListTasks reads `am stack list`. A RootTask header names its
# displayId and the indented `taskId=N: package/Class` leaves are its current
# tasks, matching the service's own parser. Known is false only when the
# listing has no RootTask header at all (empty or error output); a readable
# listing without the target display proves that display holds no task.
function Get-StackListTasks {
    param([string]$Listing, [int]$DisplayId)
    $ids = New-Object System.Collections.Generic.List[int]
    $known = $false
    $inTarget = $false
    foreach ($line in ($Listing -split "`n")) {
        $header = [regex]::Match($line, '^RootTask id=(\d+) .*displayId=(\d+)\b')
        if ($header.Success) {
            $known = $true
            $inTarget = ([int]$header.Groups[2].Value -eq $DisplayId)
            continue
        }
        if (-not $inTarget) { continue }
        $leaf = [regex]::Match($line, '^\s+taskId=(\d+): [A-Za-z0-9_$.]+/[A-Za-z0-9_$.]+\b')
        if ($leaf.Success) { [void]$ids.Add([int]$leaf.Groups[1].Value) }
    }
    return [pscustomobject]@{ Known = $known; TaskIds = @($ids) }
}

# Test-DeviceTaskPresent combines both device views. The task counts as gone
# only when at least one view was readable and neither view still lists it on
# the target display.
function Test-DeviceTaskPresent {
    param([string]$StackListing, [string]$ActivityDump, [int]$TaskId, [int]$DisplayId)
    $stack = Get-StackListTasks -Listing $StackListing -DisplayId $DisplayId
    $activity = Get-ActivityDisplayTasks -Dump $ActivityDump -DisplayId $DisplayId
    $present = (($stack.Known) -and ($stack.TaskIds -contains $TaskId)) -or
        (($activity.Known) -and ($activity.TaskIds -contains $TaskId))
    return [pscustomobject]@{
        Present = [bool]$present
        Known   = [bool]($stack.Known -or $activity.Known)
    }
}

# Wait-DeviceTaskGone polls the device until the removed task is no longer on
# the target display, up to TimeoutMs at one probe per PollMs. It never waits
# forever and never throws: the caller reports a still-present task as a
# warning and lets the session end normally.
function Wait-DeviceTaskGone {
    param([int]$TaskId, [int]$DisplayId, [int]$TimeoutMs = 5000, [int]$PollMs = 250)
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    $lastKnown = $false
    while ($true) {
        $listing = ''
        $dump = ''
        try {
            $listing = Invoke-Adb -Arguments @('shell', 'am', 'stack', 'list') -AllowFailure
            $dump = Invoke-Adb -Arguments @('shell', 'dumpsys', 'activity', 'activities') -AllowFailure
        } catch {
            # A failed read is unknown evidence for this round. The timeout
            # still bounds the wait, and the caller only warns.
        }
        $probe = Test-DeviceTaskPresent -StackListing $listing -ActivityDump $dump `
            -TaskId $TaskId -DisplayId $DisplayId
        $lastKnown = [bool]$probe.Known
        if (-not $probe.Present -and $probe.Known) {
            return [pscustomobject]@{
                Removed   = $true
                Known     = $true
                ElapsedMs = [int]$stopwatch.ElapsedMilliseconds
                TimeoutMs = $TimeoutMs
            }
        }
        if ($stopwatch.ElapsedMilliseconds -ge $TimeoutMs) {
            return [pscustomobject]@{
                Removed   = $false
                Known     = $lastKnown
                ElapsedMs = [int]$stopwatch.ElapsedMilliseconds
                TimeoutMs = $TimeoutMs
            }
        }
        Start-Sleep -Milliseconds $PollMs
    }
}

# ---------------------------------------------------------------- u2 readback
function Get-HtmlAttribute {
    param([string]$Tag, [string]$Name)
    $match = [regex]::Match($Tag, '(?:^|\s)' + [regex]::Escape($Name) + '="([^"]*)"')
    if (-not $match.Success) { return '' }
    return [System.Net.WebUtility]::HtmlDecode($match.Groups[1].Value)
}

function Get-U2Nodes {
    param([string]$Xml)
    $nodes = New-Object System.Collections.ArrayList
    foreach ($match in [regex]::Matches($Xml, '<node\b[^>]*>')) {
        $tag = $match.Value
        [void]$nodes.Add([pscustomobject]@{
            Class     = (Get-HtmlAttribute $tag 'class')
            Text      = (Get-HtmlAttribute $tag 'text')
            DisplayId = (Get-HtmlAttribute $tag 'display-id')
        })
    }
    return $nodes
}

function Read-U2Hierarchy {
    $null = Invoke-Adb -Arguments @('forward', "tcp:$($script:u2HostPort)", "tcp:$($script:u2DevicePort)")
    $script:u2Forwarded = $true
    $body = '{"jsonrpc":"2.0","id":1,"method":"dumpWindowHierarchy","params":[true,50]}'
    $url = 'http://127.0.0.1:' + $script:u2HostPort + '/jsonrpc/0'
    $response = Invoke-Http -Url $url -Method 'POST' -Body $body -TimeoutSeconds 30
    if ($response.StatusCode -ne 200) {
        Fail "u2 JSON-RPC returned HTTP $($response.StatusCode)"
    }
    $envelope = ConvertFrom-JsonSafe -Text $response.Text -Label 'u2 JSON-RPC response'
    $errorObject = Get-Field $envelope 'error'
    if ($null -ne $errorObject) {
        Fail ('u2 JSON-RPC error code=' + (Get-Field $errorObject 'code'))
    }
    $xml = Get-Field $envelope 'result'
    if ($null -eq $xml -or -not ($xml -is [string]) -or [string]$xml -eq '') {
        Fail 'u2 JSON-RPC response has no hierarchy XML'
    }
    return [string]$xml
}

function Remove-U2Forward {
    if (-not $script:u2Forwarded) { return }
    $null = Invoke-Adb -Arguments @('forward', '--remove', "tcp:$($script:u2HostPort)") -AllowFailure
    $script:u2Forwarded = $false
}

# ---------------------------------------------------------------- probe app
function Install-ProbeApk {
    if (-not (Test-Path -LiteralPath $script:probeApkPath -PathType Leaf)) {
        if ($script:skipProbeBuild) {
            Fail "probe APK is missing: $($script:probeApkPath) (and -SkipBuildProbe was set)"
        }
        Write-Output 'probe: building APK'
        & (Join-Path $PSScriptRoot 'build-probe.ps1')
        if (-not (Test-Path -LiteralPath $script:probeApkPath -PathType Leaf)) {
            Fail "scripts/build-probe.ps1 did not produce $($script:probeApkPath)"
        }
    }
    $install = Invoke-AdbCapture -Arguments @('install', '-r', $script:probeApkPath)
    if ($install.ExitCode -eq 0 -and $install.Output -match 'Success') {
        Write-Output 'probe: APK installed'
        return
    }
    # An already installed package is acceptable; its presence is the gate.
    $packagePath = Invoke-Adb -Arguments @('shell', 'pm', 'path', $script:probePackage) -AllowFailure
    if ($packagePath -notmatch 'package:') {
        Fail 'probe APK install failed and the package is not on the device'
    }
    Write-Warning 'probe APK reinstall failed; using the installed package.'
}

# ---------------------------------------------------------------- reporting
function Write-Summary {
    param(
        [string]$Result,
        [string]$Reason,
        [System.Collections.IDictionary]$Data
    )
    try {
        $summary = [ordered]@{
            timestamp_utc = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
            result        = $Result
        }
        if (-not [string]::IsNullOrEmpty($Reason)) { $summary['failure_reason'] = $Reason }
        if ($null -ne $Data) {
            foreach ($key in $Data.Keys) { $summary[$key] = $Data[$key] }
        }
        $path = Join-Path $script:stateDirectory 'verify-p3-last.json'
        Write-Utf8NoBom -Path $path -Text ($summary | ConvertTo-Json -Depth 8)
        return $path
    } catch {
        return $null
    }
}

# Best-effort end for the failure path; never throws.
function Invoke-EndCleanup {
    if ($null -eq $script:sessionOrigin -or $script:sessionEnded) { return }
    try {
        $body = '{"reference":' + (ConvertTo-JsonString $script:sessionOrigin) + '}'
        $response = Invoke-Http -Url ($script:baseUrl + '/api/end') -Method 'POST' -Body $body `
            -Headers (Get-AgentHeaders) -TimeoutSeconds 30
        if ($response.StatusCode -eq 200) {
            $script:sessionEnded = $true
            Write-Output "cleanup: /api/end $($script:sessionOrigin) accepted"
        } else {
            Write-Output "cleanup: /api/end $($script:sessionOrigin) returned HTTP $($response.StatusCode)"
        }
    } catch {
        Write-Output "cleanup: /api/end $($script:sessionOrigin) failed"
    }
}

# ---------------------------------------------------------------- main
function Invoke-Main {
    $failure = $null
    try {
        [System.Net.ServicePointManager]::Expect100Continue = $false
        if ($null -eq (Get-Command adb -ErrorAction SilentlyContinue)) { Fail 'adb is not on PATH' }

        # The expected text must be exactly U+4F60 U+597D U+FF0C U+4E16 U+754C
        # U+FF01. If this file were decoded with another code page the literal
        # would become mojibake and the exact-match assertion would compare
        # mojibake with mojibake, so the literal is verified before it is used.
        $expectedCodes = @(0x4F60, 0x597D, 0xFF0C, 0x4E16, 0x754C, 0xFF01)
        $literalCodes = @($script:textExpected.ToCharArray() | ForEach-Object { [int]$_ })
        if (($literalCodes -join ',') -ne ($expectedCodes -join ',')) {
            Fail 'the probe text literal was decoded with the wrong code page (save scripts/verify-p3.ps1 as UTF-8)'
        }

        $null = Read-DeployState
        Read-DeviceConfig
        Write-Output "target: $($script:baseUrl) (host_port=$($script:apiHostPort))"

        $devices = Invoke-Adb -Arguments @('devices')
        if ($devices -notmatch '\tdevice') {
            Fail 'no adb device is online (expected 127.0.0.1:5555)'
        }

        # ---- 2/3. health and readiness -------------------------------------
        $healthResponse = $null
        try {
            $healthResponse = Invoke-Http -Url ($script:baseUrl + '/health') -Method 'GET' -TimeoutSeconds 10
        } catch {
            $hint = ''
            if ($script:appPort -gt 0) {
                $hint = " (check 'adb forward tcp:$($script:apiHostPort) tcp:$($script:appPort)' as deploy.ps1 sets it)"
            }
            Fail ("GET /health failed: " + $_.Exception.Message + $hint)
        }
        if ($healthResponse.StatusCode -ne 200) {
            $hint = ''
            if ($script:appPort -gt 0) {
                $hint = " (check 'adb forward tcp:$($script:apiHostPort) tcp:$($script:appPort)' as deploy.ps1 sets it)"
            }
            Fail ("GET /health returned HTTP $($healthResponse.StatusCode)$hint")
        }
        $health = ConvertFrom-JsonSafe -Text $healthResponse.Text -Label '/health'
        Assert-EqualText (Get-Field $health 'status') 'ok' '/health status'
        Assert-EqualText (Get-Field $health 'contract_version') $script:contractVersion '/health contract_version'
        Write-Output ("health: contract=$($script:contractVersion) service=" + (Get-Field $health 'service_instance_id') +
            ' release=' + (Get-Field $health 'release_version'))

        $status = Invoke-AgentApi -Path '/api/status'
        if ((Get-Field $status 'can_start') -ne $true) {
            $blocking = Get-Field $status 'blocking_code'
            $blockingOperation = Get-Field $status 'blocking_operation_id'
            $line = "BLOCKED: can_start=false blocking_code=$blocking"
            if ($null -ne $blockingOperation) { $line += " blocking_operation_id=$blockingOperation" }
            Write-Output $line
            $null = Write-Summary -Result 'blocked' -Reason ("can_start=false blocking_code=" + [string]$blocking) -Data ([ordered]@{})
            exit 2
        }
        Write-Output 'status: can_start=true'

        # ---- 4. start -------------------------------------------------------
        $start = Invoke-AgentApi -Path '/api/start' -Body '{}' -TimeoutSeconds 90
        $startObservation = Get-Field $start 'observation'
        $startSession = Get-Field $start 'session'
        $script:sessionOrigin = Get-Field $startSession 'origin'
        if ($null -eq $script:sessionOrigin) {
            $script:sessionOrigin = Get-Field $startObservation 'origin'
        }
        Assert-EqualText (Get-Field $start 'outcome') 'executed' '/api/start outcome'
        Assert-EqualText (Get-Field $startObservation 'status') 'complete' '/api/start observation.status'
        Assert-EqualText (Get-Field (Get-Field $startObservation 'tree') 'format') 'full' '/api/start tree.format'
        $startObservationId = Get-Field $startObservation 'id'
        if ($null -eq $startObservationId) { Fail '/api/start has no observation.id' }
        if ($null -eq $script:sessionOrigin) { Fail '/api/start has no session.origin' }

        # The start response carries no display id; fall back to the service log.
        if ($script:requestedDisplayId -ge 0) {
            $script:targetDisplayId = $script:requestedDisplayId
        } else {
            $script:targetDisplayId = Resolve-DisplayId
        }
        if ($script:targetDisplayId -lt 0) { Fail 'display id is unresolved' }
        Write-Output "start: $($script:sessionOrigin) $startObservationId display=$($script:targetDisplayId)"

        # ---- 5/6. probe app -------------------------------------------------
        Install-ProbeApk
        $null = Invoke-Adb -Arguments @('shell', 'am', 'force-stop', $script:probePackage) -AllowFailure
        $launch = Invoke-AdbCapture -Arguments @(
            'shell', 'am', 'start', '--display', "$($script:targetDisplayId)", '-W', '-n', $script:probeActivity
        )
        if ($launch.ExitCode -ne 0) { Fail "adb am start exited with code $($launch.ExitCode)" }
        if ($launch.Output -match 'Error:|Exception') {
            $firstLine = ($launch.Output -split "`n")[0].Trim()
            Fail "adb am start reported: $firstLine"
        }
        if ($launch.Output -notmatch 'Status:\s*ok') {
            Write-Warning 'adb am start did not report Status: ok; continuing with the tree observation.'
        }
        Start-Sleep -Seconds 1
        Write-Output "probe: $($script:probePackage) started on display $($script:targetDisplayId)"

        # ---- 7. observe the probe ------------------------------------------
        $observeObservation = Invoke-FullObservation -Reference $startObservationId -Label 'observe'
        $observeObservationId = Get-Field $observeObservation 'id'
        $observeRefs = Get-ObservationRefs -Tree (Get-Field $observeObservation 'tree')
        if ($null -eq $observeRefs.Button) {
            Fail "observe tree has no button labelled TAP (nodes=$($observeRefs.Nodes))"
        }
        if ($null -eq $observeRefs.Input) {
            Fail "observe tree has no input node (nodes=$($observeRefs.Nodes))"
        }
        if ($null -eq $observeRefs.TapCount) {
            Fail "observe tree has no taps counter (nodes=$($observeRefs.Nodes))"
        }
        if ($observeRefs.TapCount -ne 0) {
            Fail "probe counter starts at taps: $($observeRefs.TapCount), expected taps: 0"
        }
        $foreground = Get-Field $observeObservation 'foreground'
        if ([string](Get-Field $foreground 'package') -ne $script:probePackage) {
            Write-Warning "foreground package is not $($script:probePackage); the tree still resolved the probe widgets."
        }
        Write-Output ("observe: $observeObservationId button=$($observeRefs.Button) input=$($observeRefs.Input) " +
            "counter=$($observeRefs.Counter) taps=0 nodes=$($observeRefs.Nodes)")

        # ---- 8. tap ---------------------------------------------------------
        $tapOperation = '{"type":"tap","target":{"ref":' + (ConvertTo-JsonString $observeRefs.Button) + '}}'
        $tapResult = Invoke-Act -ObservationId $observeObservationId -OperationJson $tapOperation -Label '/api/act tap'
        $tapObservation = Get-Field $tapResult 'observation'
        $tapObservationId = Get-Field $tapObservation 'id'
        if ($null -eq $tapObservationId) { Fail 'tap response has no observation.id' }
        $tapFormat = [string](Get-Field (Get-Field $tapObservation 'tree') 'format')
        if ($tapFormat -ne 'full') {
            Write-Output "tap: $tapObservationId (tree=$tapFormat, re-observing for a stable full tree)"
        } else {
            Write-Output "tap: $tapObservationId"
        }

        # ---- 9. observe after the tap --------------------------------------
        $postTapObservation = Invoke-FullObservation -Reference $tapObservationId -Label 'post-tap observe'
        $postTapObservationId = Get-Field $postTapObservation 'id'
        $postTapRefs = Get-ObservationRefs -Tree (Get-Field $postTapObservation 'tree')
        if ($null -eq $postTapRefs.TapCount) {
            Fail "post-tap tree has no taps counter (nodes=$($postTapRefs.Nodes))"
        }
        if ($postTapRefs.TapCount -ne 1) {
            Fail "post-tap counter is taps: $($postTapRefs.TapCount), expected taps: 1"
        }
        if ($null -eq $postTapRefs.Input) {
            Fail "post-tap tree has no input node (nodes=$($postTapRefs.Nodes))"
        }
        Write-Output "post-tap observe: $postTapObservationId taps=$($postTapRefs.TapCount)"

        # ---- 10. type text --------------------------------------------------
        $textOperation = '{"type":"text","target":{"ref":' + (ConvertTo-JsonString $postTapRefs.Input) +
            '},"text":' + (ConvertTo-JsonString $script:textExpected) + '}'
        $textResult = Invoke-Act -ObservationId $postTapObservationId -OperationJson $textOperation -Label '/api/act text'
        $textObservation = Get-Field $textResult 'observation'
        $textObservationId = Get-Field $textObservation 'id'
        if ($null -eq $textObservationId) { Fail 'text response has no observation.id' }
        Write-Output "text: $textObservationId target=$($postTapRefs.Input)"

        # ---- 11. independent u2 readback -----------------------------------
        Start-Sleep -Milliseconds 300
        $u2Xml = Read-U2Hierarchy
        $u2Nodes = @(Get-U2Nodes -Xml $u2Xml)
        if ($u2Nodes.Count -eq 0) { Fail 'u2 hierarchy dump contained no nodes' }
        $displayFilter = [string]$script:targetDisplayId
        $editTexts = @($u2Nodes | Where-Object { $_.Class -eq 'android.widget.EditText' -and $_.DisplayId -eq $displayFilter })
        $scope = "display $displayFilter"
        if ($editTexts.Count -eq 0) {
            # Older dumps may omit display-id; the exact-text check still gates.
            $editTexts = @($u2Nodes | Where-Object { $_.Class -eq 'android.widget.EditText' })
            $scope = 'any display (fallback)'
        }
        if ($editTexts.Count -eq 0) { Fail 'u2 hierarchy dump has no EditText node' }
        $exact = @($editTexts | Where-Object { $_.Text -eq $script:textExpected })
        if ($exact.Count -ne 1) {
            $lengths = ($editTexts | ForEach-Object { $_.Text.Length }) -join ','
            Fail ("u2 readback mismatch in ${scope}: editTexts=$($editTexts.Count) exact=$($exact.Count) " +
                "textLengths=$lengths expectedLength=$($script:textExpected.Length)")
        }
        $u2TapNodes = @($u2Nodes | Where-Object { $_.Text -eq 'taps: 1' -and $_.DisplayId -eq $displayFilter })
        if ($u2TapNodes.Count -lt 1) {
            $u2TapNodes = @($u2Nodes | Where-Object { $_.Text -eq 'taps: 1' })
        }
        if ($u2TapNodes.Count -lt 1) { Fail 'u2 hierarchy dump does not show taps: 1' }
        Write-Output ("u2: exact text match (length=$($script:textExpected.Length)) taps: 1 in $scope")

        # ---- 12. drop the probe task ---------------------------------------
        $taskId = Get-ForegroundTaskId -Observation $textObservation
        if ($null -eq $taskId) { $taskId = Get-ForegroundTaskId -Observation $postTapObservation }
        if ($null -eq $taskId) {
            Write-Warning 'no foreground.task_id in the recent observations; skipping task removal.'
        } else {
            $remove = Invoke-AdbCapture -Arguments @('shell', 'am', 'stack', 'remove', "$taskId")
            if ($remove.ExitCode -ne 0 -or $remove.Output -match 'Error|Exception') {
                Write-Warning "am stack remove $taskId failed (ignored)."
            } else {
                Write-Output "task: removed $taskId"
            }
            # The removal is asynchronous; end only after the task left the
            # target display, otherwise cleanup reads it as still present.
            $removal = Wait-DeviceTaskGone -TaskId $taskId -DisplayId $script:targetDisplayId
            if ($removal.Removed) {
                Write-Output ("task: $taskId left display $($script:targetDisplayId) after " +
                    "$($removal.ElapsedMs) ms")
            } elseif ($removal.Known) {
                Write-Warning ("task $taskId is still listed on display $($script:targetDisplayId) " +
                    "after $($removal.TimeoutMs) ms; continuing")
            } else {
                Write-Warning ("cannot read the device task list for display " +
                    "$($script:targetDisplayId) after $($removal.TimeoutMs) ms; continuing")
            }
        }

        # ---- 13. end and verify cleanup ------------------------------------
        $endBody = '{"reference":' + (ConvertTo-JsonString $script:sessionOrigin) + '}'
        $endResult = Invoke-AgentApi -Path '/api/end' -Body $endBody -TimeoutSeconds 60
        $script:sessionEnded = $true
        Assert-EqualText (Get-Field $endResult 'outcome') 'executed' '/api/end outcome'
        Write-Output "end: $($script:sessionOrigin)"

        $cleanupState = ''
        $cleanupTasks = ''
        $cleanupRecents = ''
        $cleanupDisplay = ''
        $cleanupExecution = ''
        $canStart = $false
        $cleanupVerified = $false
        $lastStatusError = ''
        for ($attempt = 1; $attempt -le 60; $attempt++) {
            try {
                $polled = Invoke-AgentApi -Path '/api/status'
            } catch {
                $lastStatusError = $_.Exception.Message
                Start-Sleep -Milliseconds 500
                continue
            }
            $cleanup = Get-Field $polled 'cleanup'
            $cleanupState = [string](Get-Field $cleanup 'state')
            $cleanupExecution = [string](Get-Field $cleanup 'execution')
            $cleanupTasks = [string](Get-Field $cleanup 'tasks')
            $cleanupRecents = [string](Get-Field $cleanup 'recents')
            $cleanupDisplay = [string](Get-Field $cleanup 'display')
            $canStart = (Get-Field $polled 'can_start') -eq $true
            $tasksSettled = ($cleanupTasks -eq 'confirmed' -or $cleanupTasks -eq 'not_applicable')
            if ($cleanupState -eq 'complete' -and $cleanupDisplay -eq 'confirmed' -and $tasksSettled -and $canStart) {
                $cleanupVerified = $true
                break
            }
            Start-Sleep -Milliseconds 500
        }
        if (-not $cleanupVerified) {
            $reason = "cleanup unconfirmed: state=$cleanupState display=$cleanupDisplay tasks=$cleanupTasks " +
                "can_start=$canStart"
            if ($lastStatusError -ne '') { $reason += " lastStatusError=$lastStatusError" }
            Fail $reason
        }
        $surface = Invoke-Adb -Arguments @('shell', 'dumpsys', 'SurfaceFlinger', '--display-id')
        if ($surface -notmatch 'Display ') {
            Fail 'dumpsys SurfaceFlinger --display-id produced no display records'
        }
        if ($surface -match 'MobileUseAgent') {
            Fail 'SurfaceFlinger still reports a MobileUseAgent virtual display'
        }
        Write-Output ("cleanup: state=$cleanupState display=$cleanupDisplay tasks=$cleanupTasks " +
            "recents=$cleanupRecents can_start=$canStart")
        Write-Output 'SurfaceFlinger: no MobileUseAgent display'

        # ---- 14. pass summary ----------------------------------------------
        $summaryData = [ordered]@{
            display_id     = $script:targetDisplayId
            session_origin = $script:sessionOrigin
            observations   = [ordered]@{
                start    = $startObservationId
                observe  = $observeObservationId
                tap      = $tapObservationId
                post_tap = $postTapObservationId
                text     = $textObservationId
            }
            tap_count_tree   = $postTapRefs.TapCount
            tap_count_u2     = $u2TapNodes.Count
            text_length      = $script:textExpected.Length
            u2_text_exact    = $true
            cleanup          = [ordered]@{
                state     = $cleanupState
                execution = $cleanupExecution
                tasks     = $cleanupTasks
                recents   = $cleanupRecents
                display   = $cleanupDisplay
            }
            can_start        = $canStart
            surfaceflinger   = 'clean'
        }
        $summaryPath = Write-Summary -Result 'pass' -Reason '' -Data $summaryData
        Write-Output 'PASS p3 writable loop'
        Write-Output "  session=$($script:sessionOrigin) display=$($script:targetDisplayId)"
        Write-Output ("  observations: start=$startObservationId observe=$observeObservationId " +
            "tap=$tapObservationId post_tap=$postTapObservationId text=$textObservationId")
        Write-Output "  tap counter: tree=$($postTapRefs.TapCount) u2=$($u2TapNodes.Count)"
        Write-Output "  text: exact u2 match, length=$($script:textExpected.Length)"
        Write-Output ("  cleanup: state=$cleanupState display=$cleanupDisplay tasks=$cleanupTasks " +
            "recents=$cleanupRecents can_start=$canStart")
        Write-Output '  SurfaceFlinger: no MobileUseAgent display'
        if ($null -ne $summaryPath) {
            Write-Output "  summary: $summaryPath"
        }
    } catch {
        $failure = $_.Exception.Message
    }

    if ($null -ne $failure) {
        Write-Output ("FAIL: " + $failure)
        if (-not $script:keepScene) {
            Invoke-EndCleanup
        } elseif ($null -ne $script:sessionOrigin) {
            Write-Output "session $($script:sessionOrigin) kept (-KeepSession)"
        } else {
            Write-Output 'no session was created'
        }
        Remove-U2Forward
        $null = Write-Summary -Result 'fail' -Reason $failure -Data ([ordered]@{})
        exit 1
    }
    Remove-U2Forward
}

if ($MyInvocation.InvocationName -ne '.') {
    Invoke-Main
}
