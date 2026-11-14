#Requires -Version 5.1
<#
.SYNOPSIS
    Verifies the MCP adapter entries of the deployed device service.

.DESCRIPTION
    Reads .state/deploy.json, refreshes the private MCP configuration, builds
    the host binaries through scripts/build-host-tools.ps1, runs the MCP probe
    for the requested transports and prints the service health/status summary.
    An existing valid private host MCP token is reused; a token is created only
    when none is available. Tokens, node trees, image data, shell output and
    notification text are never printed.

    The service must already be deployed by scripts/deploy.ps1 and the control
    App must report can_start=true. This script never deploys, restarts or
    stops the service.

.PARAMETER HostPort
    Service host port. Defaults to host_port in .state/deploy.json.

.PARAMETER SkipBuild
    Do not rebuild. Run scripts/build-host-tools.ps1 -VerifyOnly instead of
    trusting that the existing executables merely exist.

.PARAMETER Transport
    All, Stdio, Http or PRecovery.

.NOTES
    Exit codes: 0 = PASS, 1 = FAIL, 2 = BLOCKED (not admissible, or the HTTP
    listener is already occupied).
#>
param(
    [int]$HostPort = 0,
    [switch]$SkipBuild,
    [ValidateSet('All', 'Stdio', 'Http', 'PRecovery')]
    [string]$Transport = 'All'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$stateDirectory = Join-Path $repository '.state'
$deployStatePath = Join-Path $stateDirectory 'deploy.json'
$versionsPath = Join-Path $repository 'release/versions.json'
if (-not (Test-Path -LiteralPath $versionsPath -PathType Leaf)) {
    throw "Version source is missing: $versionsPath"
}
$versions = Get-Content -LiteralPath $versionsPath -Raw | ConvertFrom-Json
$contractVersion = [string]$versions.contract_version
if ($contractVersion -notmatch '^[1-9][0-9]{0,2}$') {
    throw "contract_version must be a positive integer: $contractVersion"
}
$mcpConfigPath = Join-Path $stateDirectory 'mcp-config.json'
$hostManifestPath = Join-Path $repository 'build/host-tools-manifest.json'
$adapterBinary = Join-Path $repository 'build/device-mcp.exe'
$probeBinary = Join-Path $repository 'build/mcp-probe.exe'
$httpListen = '127.0.0.1:18882'
$httpPort = 18882
$allowedOrigins = @('http://127.0.0.1:3000')
$probeTimeoutSeconds = 180

function Write-Utf8NoBom {
    param([string]$Path, [string]$Text)
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Text, $encoding)
}

function Get-Field {
    param($Object, [string]$Name)
    if ($null -eq $Object) { return $null }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

function New-HexToken {
    $bytes = New-Object byte[] 32
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($bytes) } finally { $generator.Dispose() }
    return ([System.BitConverter]::ToString($bytes) -replace '-', '').ToLowerInvariant()
}

# Start one stdio adapter with an immediately closed stdin and read only its
# startup log. This proves the build-side tools count without a device call and
# without printing protocol stdout or any configuration value.
function Assert-AdapterStartupLog {
    param([string]$Binary, [string]$ConfigPath, [int]$ExpectedTools)
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = $Binary
    $startInfo.Arguments = '-transport stdio -config "' + ($ConfigPath -replace '"', '\"') + '" -result-mode both'
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.CreateNoWindow = $true
    $process = [System.Diagnostics.Process]::Start($startInfo)
    try {
        $process.StandardInput.Close()
        $exited = $process.WaitForExit(15000)
        if (-not $exited) {
            $process.Kill()
            $process.WaitForExit(5000)
            throw 'adapter_startup_timeout'
        }
        $stderr = $process.StandardError.ReadToEnd()
        $null = $process.StandardOutput.ReadToEnd()
    } finally {
        $process.Dispose()
    }
    if ($stderr -notmatch 'msg=mcp\.start\b') { throw 'adapter_startup_log_missing' }
    $match = [regex]::Match($stderr, 'tools=(\d+)')
    if (-not $match.Success) { throw 'adapter_startup_tools_missing' }
    $actual = [int]$match.Groups[1].Value
    if ($actual -ne $ExpectedTools) {
        throw ('adapter_startup_tools=' + $actual + ' expected=' + $ExpectedTools)
    }
    Write-Output ('adapter startup log: PASS tools=' + $actual)
}

try {
    if (-not (Test-Path -LiteralPath $deployStatePath -PathType Leaf)) {
        throw 'deploy_state_missing'
    }
    try {
        $deploy = Get-Content -LiteralPath $deployStatePath -Raw | ConvertFrom-Json
    } catch {
        throw 'deploy_state_invalid'
    }

    $agentToken = [string](Get-Field $deploy 'agent_token')
    $controlToken = [string](Get-Field $deploy 'control_token')
    if ($agentToken -eq '') { throw 'deploy_state_agent_token_missing' }

    if ($HostPort -gt 0) {
        $servicePort = $HostPort
    } else {
        $deployPort = Get-Field $deploy 'host_port'
        if ($null -eq $deployPort) { throw 'deploy_state_host_port_missing' }
        $servicePort = [int]$deployPort
    }
    if ($servicePort -lt 1 -or $servicePort -gt 65535) { throw 'host_port_invalid' }
    $baseUrl = 'http://127.0.0.1:' + $servicePort

    # Reuse the private host MCP token from the existing configuration when it
    # is valid. Verification must not rotate a token that the real host process
    # may still be using; a new token is created only when none is available.
    $existingConfig = $null
    if (Test-Path -LiteralPath $mcpConfigPath -PathType Leaf) {
        try {
            $existingConfig = Get-Content -LiteralPath $mcpConfigPath -Raw | ConvertFrom-Json
        } catch {
            $existingConfig = $null
        }
    }
    $mcpToken = ''
    $tokenReused = $false
    if ($null -ne $existingConfig) {
        $existingHttp = Get-Field $existingConfig 'http'
        $candidate = [string](Get-Field $existingHttp 'token')
        if ($candidate -match '^[0-9a-fA-F]{64}$' -and $candidate -ne $agentToken -and
                $candidate -ne $controlToken) {
            $mcpToken = $candidate
            $tokenReused = $true
        }
        $existingListen = [string](Get-Field $existingHttp 'listen')
        if ($existingListen -ne '') {
            $httpListen = $existingListen
            $listenMatch = [regex]::Match($existingListen, ':(\d+)$')
            if ($listenMatch.Success) { $httpPort = [int]$listenMatch.Groups[1].Value }
        }
        $existingOrigins = Get-Field $existingHttp 'allowed_origins'
        if ($null -ne $existingOrigins -and @($existingOrigins).Count -gt 0) {
            $allowedOrigins = @($existingOrigins | ForEach-Object { [string]$_ })
        }
    }
    if ($mcpToken -eq '') {
        $mcpToken = New-HexToken
        while ($mcpToken -eq $agentToken -or $mcpToken -eq $controlToken) {
            $mcpToken = New-HexToken
        }
    }

    $mcpConfig = [ordered]@{
        service = [ordered]@{
            base_url    = $baseUrl
            agent_token = $agentToken
        }
        transport = 'stdio'
        http      = [ordered]@{
            listen          = $httpListen
            token           = $mcpToken
            allowed_origins = @($allowedOrigins)
        }
    }
    Write-Utf8NoBom -Path $mcpConfigPath -Text ($mcpConfig | ConvertTo-Json -Depth 5)
    $null = & icacls $mcpConfigPath /inheritance:r /grant:r "$($env:USERNAME):(R,W)" 2>&1
    if ($LASTEXITCODE -ne 0) { throw 'mcp_config_acl_failed' }
    Write-Output ('config refreshed path=.state/mcp-config.json token=' +
        $(if ($tokenReused) { 'reused' } else { 'created' }) + ' permissions=owner-only')

    if ($SkipBuild) {
        & (Join-Path $PSScriptRoot 'build-host-tools.ps1') -VerifyOnly
        if ($LASTEXITCODE -ne 0) { throw 'host_tools_verify_failed' }
        Write-Output 'host tools verified (build skipped)'
    } else {
        & (Join-Path $PSScriptRoot 'build-host-tools.ps1')
        if ($LASTEXITCODE -ne 0) { throw 'host_tools_build_failed' }
        Write-Output 'host tools built path=build/device-mcp.exe,probe=build/mcp-probe.exe'
    }

    if (-not (Test-Path -LiteralPath $hostManifestPath -PathType Leaf)) {
        throw 'host_tools_manifest_missing'
    }
    $hostManifest = Get-Content -LiteralPath $hostManifestPath -Raw | ConvertFrom-Json
    $hostCatalog = Get-Field $hostManifest 'tool_catalog'
    $toolCount = [int](Get-Field $hostCatalog 'count')
    if ($toolCount -ne 12) { throw 'host_tools_catalog_count_not_12' }
    Write-Output ('host tools identity: PASS release=' + [string](Get-Field $hostManifest 'release_version') +
        ' contract=' + [string](Get-Field $hostManifest 'contract_version') +
        ' tool_catalog=' + [string]$toolCount)
    Assert-AdapterStartupLog -Binary $adapterBinary -ConfigPath $mcpConfigPath -ExpectedTools $toolCount

    $modes = @()
    switch ($Transport) {
        'All' { $modes = @('Stdio', 'Http', 'PRecovery') }
        'Stdio' { $modes = @('Stdio') }
        'Http' { $modes = @('Http') }
        'PRecovery' { $modes = @('PRecovery') }
    }

    if ($modes -contains 'Http') {
        $listener = $null
        $bound = $false
        try {
            $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $httpPort)
            $listener.Start()
            $bound = $true
        } catch {
            Write-Output ('BLOCKED http reason=listener_occupied listen=' + $httpListen)
            Write-Output 'RESULT BLOCKED'
            exit 2
        } finally {
            if ($bound -and $null -ne $listener) { $listener.Stop() }
        }
    }

    $probeTransportByMode = @{
        Stdio     = 'stdio'
        Http      = 'http'
        PRecovery = 'p-recovery'
    }

    foreach ($mode in $modes) {
        $probeTransport = $probeTransportByMode[$mode]
        Write-Output ('== probe ' + $probeTransport + ' ==')
        $probeArguments = @(
            '-config', $mcpConfigPath,
            '-binary', $adapterBinary,
            '-transport', $probeTransport,
            '-timeout', [string]$probeTimeoutSeconds
        )
        $previousErrorAction = $ErrorActionPreference
        $probeOutput = @()
        $probeExitCode = 1
        try {
            $ErrorActionPreference = 'Continue'
            $probeOutput = & $probeBinary @probeArguments 2>&1
            $probeExitCode = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $previousErrorAction
        }
        foreach ($line in $probeOutput) {
            Write-Output ([string]$line)
        }
        Write-Output ('probe ' + $probeTransport + ' exit=' + $probeExitCode)
        if ($probeExitCode -ne 0) {
            if ($probeExitCode -eq 2) {
                Write-Output 'RESULT BLOCKED'
                exit 2
            }
            Write-Output 'RESULT FAIL'
            exit 1
        }
    }

    $health = $null
    try {
        $health = Invoke-RestMethod -Method Get -Uri ($baseUrl + '/health') -TimeoutSec 10
    } catch {
        Write-Output 'service health=unreachable'
        Write-Output 'RESULT FAIL'
        exit 1
    }
    $healthStatus = [string](Get-Field $health 'status')
    $contract = [string](Get-Field $health 'contract_version')
    Write-Output ('service health=' + $healthStatus + ' contract_version=' + $contract)

    $status = $null
    try {
        $headers = @{
            Authorization           = 'Bearer ' + $agentToken
            'X-Mobile-Use-Contract' = $contractVersion
        }
        $status = Invoke-RestMethod -Method Post -Uri ($baseUrl + '/api/status') `
            -Headers $headers -ContentType 'application/json' -Body '{}' -TimeoutSec 15
    } catch {
        Write-Output 'service status=unreachable'
        Write-Output 'RESULT FAIL'
        exit 1
    }
    $canStart = [bool](Get-Field $status 'can_start')
    $blockingCode = [string](Get-Field $status 'blocking_code')
    if ($canStart) {
        Write-Output 'service can_start=true'
        Write-Output 'RESULT PASS'
        exit 0
    }
    Write-Output 'service can_start=false'
    if ($blockingCode -ne '') {
        Write-Output ('service blocking_code=' + $blockingCode)
    }
    Write-Output 'RESULT BLOCKED'
    exit 2
} catch {
    $reason = $_.Exception.Message
    if ($reason -eq '') { $reason = 'unexpected_error' }
    Write-Output ('FAIL run reason=' + $reason)
    Write-Output 'RESULT FAIL'
    exit 1
}
