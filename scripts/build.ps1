param(
    [string]$Output = 'build',
    [string]$Version = '0.1.0-p3'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$outputDirectory = [System.IO.Path]::GetFullPath((Join-Path $repository $Output))
New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null

$previous = @{
    GOEXPERIMENT = $env:GOEXPERIMENT
    CGO_ENABLED  = $env:CGO_ENABLED
    GOOS         = $env:GOOS
    GOARCH       = $env:GOARCH
}
try {
    $env:GOEXPERIMENT = 'jsonv2'
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'linux'
    $env:GOARCH = 'arm64'
    Push-Location $repository
    try {
        $target = Join-Path $outputDirectory 'device-service'
        & go build -trimpath -buildvcs=false `
            -ldflags "-s -w -X github.com/malyjacob/mobile-use-vnext/internal/protocol.ReleaseVersion=$Version" `
            -o $target ./cmd/device-service
        if ($LASTEXITCODE -ne 0) {
            throw "go build exited with code $LASTEXITCODE"
        }
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $target).Hash.ToLowerInvariant()
        Write-Output "device-service: $target"
        Write-Output "release version: $Version"
        Write-Output "sha256: $hash"
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
