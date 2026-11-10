"""Verify recovery guards with Go overlays; never edit the actual source."""
import hashlib
import json
import os
import re
import subprocess
from pathlib import Path

root = Path.cwd().resolve()
source = root / 'internal/engine/ui_recovery.go'
original = source.read_bytes()
text = original.decode('utf-8')
scratch = root / '.state/ui-evidence-mutations'
scratch.mkdir(parents=True, exist_ok=True)
cases = [
    ('checkpoint_durability', 'checkpointReady := e.uiCheckpointReady',
     'checkpointReady := true', 'TestLegacyUICheckpointSaveFailureNeverClaimsRebootGuidance'),
    ('stable_device_identity', 'if deviceID != current.DeviceID {',
     'if false {', 'TestUIRecoveryUnsafeIdentityRetainsResponsibility'),
    ('boot_boundary', 'return bootID != current.BootID, true',
     'return true, true', 'TestV2UIRecoverySameBootRequiresReboot'),
]
environment = os.environ.copy()
environment.update(GOEXPERIMENT='jsonv2', CGO_ENABLED='0')
results = []
for name, before, after, test in cases:
    if text.count(before) != 1:
        raise RuntimeError('Mutation site must be reviewed against current source')
    mutant = scratch / (name + '.go')
    mutant.write_text(text.replace(before, after, 1), encoding='utf-8')
    overlay = scratch / (name + '.json')
    overlay.write_text(json.dumps({'Replace': {str(source): str(mutant)}}), encoding='utf-8')
    process = subprocess.run(['go', 'test', '-overlay', str(overlay), './internal/engine',
                              '-run', '^' + test + '$', '-count=1', '-v', '-timeout', '45s'],
                             cwd=root, env=environment, capture_output=True, text=True,
                             encoding='utf-8', errors='replace', timeout=75)
    failed = re.findall(r'^--- FAIL: ([^ (]+)', process.stdout, re.M)
    if process.returncode == 0 or test not in failed:
        raise RuntimeError('Mutation did not reach the expected failing behavior assertion')
    if source.read_bytes() != original:
        raise RuntimeError('Original source changed during mutation verification')
    result = {'guard': name, 'expected_behavior_failure': True, 'test': test}
    results.append(result)
    print(json.dumps(result), flush=True)
(scratch / 'results.json').write_text(json.dumps({
    'source_sha256': hashlib.sha256(original).hexdigest(), 'results': results,
}, indent=2), encoding='utf-8')
