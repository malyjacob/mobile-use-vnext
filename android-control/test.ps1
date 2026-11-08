param(
    [string]$JdkRoot = 'D:/Android/Jdk/17'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$javac = Join-Path $JdkRoot 'bin/javac.exe'
$java = Join-Path $JdkRoot 'bin/java.exe'
$gateSource = Join-Path $PSScriptRoot 'app/src/main/java/com/mobileuse/control/DisplayAttemptGate.java'
$testSource = Join-Path $PSScriptRoot 'tests/com/mobileuse/control/DisplayAttemptGateTest.java'
$testDirectory = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'build/test'))
$classesDirectory = Join-Path $testDirectory 'classes'

foreach ($required in @($javac, $java, $gateSource, $testSource)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Required JVM test file is missing: $required"
    }
}

# Compile only the production gate and its pure JVM test. No Android SDK, device, or
# dependency download. Do not clean any path; all generated files stay in build/test.
New-Item -ItemType Directory -Path $classesDirectory -Force | Out-Null
& $javac "-J-Djava.io.tmpdir=$testDirectory" --release 8 -encoding UTF-8 `
    -d $classesDirectory $gateSource $testSource
if ($LASTEXITCODE -ne 0) {
    throw "JVM test compilation failed with exit code $LASTEXITCODE"
}

& $java "-Djava.io.tmpdir=$testDirectory" -cp $classesDirectory com.mobileuse.control.DisplayAttemptGateTest
if ($LASTEXITCODE -ne 0) {
    throw "JVM tests failed with exit code $LASTEXITCODE"
}
