# Pinned uiautomator server patch

This directory contains the local binary patch applied to the UIAutomator server JAR shipped
by the project's pinned Python dependency, `uiautomator2==3.5.0`. The upstream source reference
is `openatx/android-uiautomator-server` tag `2.4.0`, commit
`a98441be6551ee97c5c80d09ae895d86ec1c1553`.

The wheel's JAR has manifest `versionName="untagged-204-3ba4fc1"`, and its checked-in bytes are
the build input used below. The upstream tag is a source reference, not a claim that rebuilding
that tag produces this exact wheel asset.

## Artifacts and checksums

The only checked-in server binary is the Python package resource at:

```text
service/src/mobile_use/android/assets/u2.jar
```

- Original `uiautomator2==3.5.0` JAR SHA-256:
  `EA100004A76E821F4BFF4F9D674165562752AF101943D2A65959A5F2EE7FB892`
- Patched resource JAR SHA-256:
  `28361193DE3D13303543C13BDC7268C22EDECABFA4E68E6FBDFC35FFFE1406A3`
- Upstream license: MIT, preserved in `LICENSE`.

Deployment should verify the patched hash from the package resource and push that file to the
fixed remote path `/data/local/tmp/u2.jar`. Replacing a running server JAR is insufficient; the
old `app_process` must be stopped before the patched server starts.

## Patch contents

`patches/0001-preserve-non-bmp-xml.patch` documents the sanitizer change from UTF-16 code units
to Unicode code points. Valid non-BMP characters such as `🌸` and `🚀` remain intact. XML-forbidden
code points and unpaired surrogates are still replaced with `.`. At the upper boundary, only
U+FFFE and U+FFFF are forbidden; code points above U+FFFF continue to the append path.

Because the fixed wheel JAR contains the older code-unit implementation, the reproducible build
applies the equivalent method replacement directly to `classes8.dex` smali. The patch header is
the upstream source location; it is not a guarantee that the upstream tag needs another source
change.

The same `classes8.dex` receives two display-bound text RPCs:

```text
setTextOnDisplay(
  int displayId,
  String packageName,
  String resourceId,
  String className,
  String description,
  String bounds,
  int width,
  int height,
  String text
) -> Map {
  candidateCount: int,
  value: String|null,
  issued: boolean,
  accepted: boolean
}

getTextOnDisplay(
  int displayId,
  String packageName,
  String resourceId,
  String className,
  String description,
  String bounds,
  int width,
  int height
) -> Map {
  candidateCount: int,
  value: String|null
}
```

`bounds` is Android `Rect.toShortString()` format, for example `[0,100][1080,220]`. The caller
must pass the same display ID and structural identity to both calls.

`DisplayBoundTextService` uses only
`UiAutomation.getWindowsOnAllDisplays().get(displayId)`. A candidate must be focused, editable,
visible to the user, enabled, non-password, and match package, resource ID, class, description,
and visible bounds exactly. The action is skipped unless exactly one candidate matches.

Once the unique target's `performAction` is called, `issued` is `true` even when
`performAction` returns `false`; that framework boolean is returned separately as `accepted`.
The read side is independent and read-only. It never invokes `setTextOnDisplay`; it refreshes
the uniquely matched node and returns its live accessibility text. `value` is `null` when that
refresh fails, which means unknown rather than empty.

An empty Android text field exposes its hint through `getText()` and reports
`isShowingHintText()`. The helper therefore returns an empty string for a refreshed node that is
showing its hint, so clearing a field can be confirmed instead of reading back the hint. It does
not compare the text with the hint: a real value that happens to equal the hint keeps its text
because the hint flag is false.

## Rebuilding

From the repository root on Windows:

```powershell
pwsh -File .\scripts\prepare-u2-server.ps1
```

The script:

- verifies the original and final JAR hashes;
- downloads and hash-verifies smali/baksmali 2.5.2 when absent;
- compiles the Java helper stubs against the selected Android SDK;
- applies the fixed smali replacements;
- assembles DEX inputs in sorted order with one smali worker for byte stability;
- checks that every `classes*.dex` in the result is readable;
- checks the active sanitizer's U+FFFE/U+FFFF branch targets and inclusive upper bound;
- parses `AndroidManifest.xml` with `aapt2`;
- checks the display, enabled, password, issued/accepted, and read-only invariants; and
- builds `vendor/uiautomator-server/.build/u2-xml-sanitizer-test.dex`, a separate reflection
  probe for execution against the production JAR on a device; and
- writes only the package resource JAR.

The probe loads `AccessibilityNodeInfoDumper.stripInvalidXMLChars` from the patched `u2.jar` at
runtime and checks that `🌸小爱✨测试🚀` survives, while U+FFFE, U+FFFF, and unpaired high and low
surrogates become `.`. It does not copy or simulate the sanitizer implementation.

The build was verified with Temurin JDK 17.0.20.1 and Android SDK Build Tools 36.0.0. It requires
`ANDROID_HOME` or `ANDROID_SDK_ROOT`, plus a platform `android.jar`, `d8`, and `aapt2`. No ADB
connection or device operation is used by the script. Temporary downloads and build products are
kept under the gitignored `.build/` directory.

## Integration

The project-local Python integration belongs in `mobile_use.android.uiautomator`, using a
`u2.Device` subclass whose inherited `_setup_jar` is overridden to push
`service/src/mobile_use/android/assets/u2.jar` to the fixed remote path. Do not replace or
monkey-patch the JAR inside the virtual environment. The Python side should reject a resource
whose SHA-256 is not the patched hash above before changing the device.
