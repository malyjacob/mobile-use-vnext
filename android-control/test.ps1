param(
    [string]$JdkRoot = 'D:/Android/Jdk/17'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$javac = Join-Path $JdkRoot 'bin/javac.exe'
$java = Join-Path $JdkRoot 'bin/java.exe'
$gateSource = Join-Path $PSScriptRoot 'app/src/main/java/com/mobileuse/vnext/DisplayAttemptGate.java'
$messageListSource = Join-Path $PSScriptRoot 'app/src/main/java/com/mobileuse/vnext/MessageList.java'
$gateTest = Join-Path $PSScriptRoot 'tests/com/mobileuse/control/DisplayAttemptGateTest.java'
$messageListTest = Join-Path $PSScriptRoot 'tests/com/mobileuse/vnext/MessageListTest.java'
$testDirectory = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'build/test'))
$classesDirectory = Join-Path $testDirectory 'classes'

foreach ($required in @($javac, $java, $gateSource, $messageListSource, $gateTest, $messageListTest)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Required JVM test file is missing: $required"
    }
}

# Compile only the production gates/lists and their pure JVM tests. No Android SDK,
# device, or dependency download. Do not clean any path; all generated files stay in build/test.
New-Item -ItemType Directory -Path $classesDirectory -Force | Out-Null
& $javac "-J-Djava.io.tmpdir=$testDirectory" --release 8 -encoding UTF-8 `
    -d $classesDirectory $gateSource $messageListSource $gateTest $messageListTest
if ($LASTEXITCODE -ne 0) {
    throw "JVM test compilation failed with exit code $LASTEXITCODE"
}

& $java "-Djava.io.tmpdir=$testDirectory" -cp $classesDirectory com.mobileuse.vnext.DisplayAttemptGateTest
if ($LASTEXITCODE -ne 0) {
    throw "DisplayAttemptGateTest failed with exit code $LASTEXITCODE"
}

& $java "-Djava.io.tmpdir=$testDirectory" -cp $classesDirectory com.mobileuse.vnext.MessageListTest
if ($LASTEXITCODE -ne 0) {
    throw "MessageListTest failed with exit code $LASTEXITCODE"
}
