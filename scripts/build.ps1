param(
    [string]$Output = 'build',
    [string]$Version = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$versionsPath = Join-Path $repository 'release/versions.json'
if (-not (Test-Path -LiteralPath $versionsPath -PathType Leaf)) {
    throw "Version source is missing: $versionsPath"
}
$versions = Get-Content -LiteralPath $versionsPath -Raw | ConvertFrom-Json
$releaseVersion = [string]$versions.release_version
if ($releaseVersion -notmatch '^\d+\.\d+\.\d+$') {
    throw "release_version must be MAJOR.MINOR.PATCH: $releaseVersion"
}
# The version is owned by release/versions.json. -Version remains accepted only
# as an assertion so older invocations cannot silently build a different label.
if ($Version -ne '' -and $Version -ne $releaseVersion) {
    throw "-Version $Version does not match release/versions.json ($releaseVersion)"
}

# Regenerate the committed Go/Java/XML version files before compiling. The Go
# build embeds ReleaseVersion from this same source through -ldflags.
& (Join-Path $PSScriptRoot 'generate-version.ps1')

$outputDirectory = if ([System.IO.Path]::IsPathRooted($Output)) {
    [System.IO.Path]::GetFullPath($Output)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $repository $Output))
}
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
        foreach ($target in @(
                @{ Name = 'device-service'; Package = './cmd/device-service' },
                @{ Name = 'device-mcp'; Package = './cmd/device-mcp' })) {
            $path = Join-Path $outputDirectory $target.Name
            & go build -trimpath -buildvcs=false `
                -ldflags "-s -w -X github.com/malyjacob/mobile-use-vnext/internal/protocol.ReleaseVersion=$releaseVersion" `
                -o $path $target.Package
            if ($LASTEXITCODE -ne 0) {
                throw "go build exited with code $LASTEXITCODE for $($target.Package)"
            }
            $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()
            Write-Output "$($target.Name): $path"
            Write-Output "  sha256: $hash"
        }
        Write-Output "release version: $releaseVersion"
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
