"""Controlled launch-attribution failure against the paired phone service.

Only the private ADB executable path changes during this check. The wrapper
forwards mutating calls once, and hides exactly five task-list reads after an
accepted launch. Finally it restores the original config and service process.
No API response bodies, UI trees, input, credentials or device identities are
written to logs or this repository.
"""
import json
import shlex
import shutil
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

root = Path.cwd().resolve()
state = json.loads((root / '.state/deploy.json').read_text(encoding='utf-8-sig'))
versions = json.loads((root / 'release/versions.json').read_text(encoding='utf-8-sig'))
original_config = (root / '.state/config.json').read_bytes()
adb = shutil.which('adb')
chroot = state['chroot']
if chroot != '/data/local/chroots/debian13':
    raise RuntimeError('This verification is bound to the documented phone chroot')
base = 'http://127.0.0.1:' + str(state['host_port'])
private_dir = '/var/lib/mobile-use-vnext/verification-ui-evidence'
wrapper_path = '/opt/mobile-use-vnext/verify-ui-attribution-adb'
config_path = chroot + '/opt/mobile-use-vnext/config.json'
headers = {'Authorization': 'Bearer ' + state['agent_token'],
           'X-Mobile-Use-Contract': str(versions['contract_version']),
           'Content-Type': 'application/json'}

def remote(script, timeout=45):
    result = subprocess.run([adb, 'shell', 'su -c ' + shlex.quote(script)],
                            capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError('bounded verification maintenance command failed')
    return result.stdout.decode('utf-8', errors='replace').strip()

def inside(script, timeout=45):
    return remote('chroot ' + chroot + ' /bin/sh -c ' + shlex.quote(
        'export PATH=/usr/local/bin:/usr/bin:/bin; ' + script), timeout)

def push_private(local, destination):
    temporary = '/data/local/tmp/mobile-use-ui-evidence-' + uuid.uuid4().hex
    result = subprocess.run([adb, 'push', str(local), temporary], capture_output=True, timeout=30)
    if result.returncode:
        raise RuntimeError('private verification file transfer failed')
    remote('cp ' + temporary + ' ' + destination + ' && chmod 600 ' + destination +
           ' && rm -f ' + temporary)

def api(tool, arguments):
    call_headers = dict(headers)
    if tool != 'status':
        call_headers['X-Mobile-Use-Operation'] = 'P' + uuid.uuid4().hex
    request = urllib.request.Request(base + '/api/' + tool,
        data=json.dumps(arguments).encode('utf-8'), headers=call_headers, method='POST')
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        return json.load(error)

def wait_ready():
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        try:
            result = api('status', {})
            if result.get('can_start') is True:
                return
        except (OSError, ValueError):
            pass
        time.sleep(.25)
    raise RuntimeError('service did not reach normal admission before verification')

wait_ready()
real_adb = inside('command -v adb')
if real_adb not in ('/usr/bin/adb', '/usr/local/bin/adb'):
    raise RuntimeError('Unexpected real ADB path; review before instrumenting')
wrapper = '''#!/bin/sh
phase=/var/lib/mobile-use-vnext/verification-ui-evidence/launch-phase
remaining=/var/lib/mobile-use-vnext/verification-ui-evidence/remaining-reads
launches=/var/lib/mobile-use-vnext/verification-ui-evidence/launch-count
command=
for arg in "$@"; do command=$arg; done
if [ "$command" = "am stack list" ] && [ -f "$remaining" ]; then
    count=$(cat "$remaining")
    case "$count" in 1|2|3|4|5) ;; *) exit 1 ;; esac
    count=$((count - 1))
    if [ "$count" -eq 0 ]; then rm -f "$remaining" "$phase";
    else printf '%s\n' "$count" > "$remaining"; fi
    exit 0
fi
REAL_ADB "$@"
result=$?
case "$command" in
    'am start -W --display '*)
        if [ "$result" -eq 0 ] && [ -f "$phase" ]; then
            count=0
            if [ -f "$launches" ]; then count=$(cat "$launches"); fi
            printf '%s\n' "$((count + 1))" > "$launches"
            printf '%s\n' 5 > "$remaining"
        fi ;;
esac
exit "$result"
'''.replace('REAL_ADB', shlex.quote(real_adb))
wrapper_file = root / '.state/verify-ui-attribution-adb'
wrapper_file.write_text(wrapper, encoding='utf-8', newline='\n')
instrumented = json.loads(original_config)
instrumented['adb']['path'] = wrapper_path
instrumented_file = root / '.state/verify-ui-instrumented-config.json'
instrumented_file.write_text(json.dumps(instrumented), encoding='utf-8')
restore_file = root / '.state/verify-ui-normal-config.json'
restore_file.write_bytes(original_config)
origin = None
config_changed = False
try:
    inside('mkdir -p ' + private_dir + ' && chmod 700 ' + private_dir)
    push_private(wrapper_file, chroot + wrapper_path)
    remote('chmod 700 ' + chroot + wrapper_path)
    inside('/opt/mobile-use-vnext/service.sh stop')
    config_changed = True
    push_private(instrumented_file, config_path)
    inside('/opt/mobile-use-vnext/service.sh start')
    wait_ready()
    started = api('start', {})
    if started.get('outcome') != 'executed' or not started.get('observation', {}).get('id'):
        raise RuntimeError('controlled virtual session was not established')
    if started.get('observation', {}).get('screen') is None:
        raise RuntimeError('controlled session has no screen evidence')
    origin = started['operation_id']
    current = api('status', {})
    if current.get('session', {}).get('mode') != 'virtual':
        raise RuntimeError('controlled verification requires virtual mode')
    inside('rm -f ' + private_dir + '/remaining-reads ' + private_dir +
           '/launch-count; : > ' + private_dir + '/launch-phase')
    opened = api('open_app', {'observation_id': started['observation']['id'], 'app': 'com.android.settings'})
    if opened.get('outcome') != 'uncertain' or opened.get('error', {}).get('code') != 'app_launch_unconfirmed':
        raise RuntimeError('controlled launch did not reach the intended attribution failure')
    observed = api('observe', {'reference': opened['operation_id'], 'full': True})
    foreground = observed.get('observation', {}).get('foreground', {})
    if foreground.get('state') != 'present' or foreground.get('package') != 'com.android.settings':
        raise RuntimeError('controlled launched app was not observable afterward')
    original = api('status', {'reference': opened['operation_id']})
    if original.get('outcome') != 'uncertain':
        raise RuntimeError('later observation rewrote the original result')
    if inside('cat ' + private_dir + '/launch-count') != '1':
        raise RuntimeError('controlled launch was not forwarded exactly once')
    ended = api('end', {'reference': origin})
    if ended.get('outcome') != 'executed':
        raise RuntimeError('controlled session could not be ended')
    origin = None
    wait_ready()
    print(json.dumps({'real_launch_attribution_failure': True, 'later_observation_ok': True,
                      'original_outcome': 'uncertain', 'launch_count': 1,
                      'admission_recovered_after_end': True}), flush=True)
finally:
    try:
        if origin is not None:
            api('end', {'reference': origin})
    finally:
        try:
            if config_changed:
                try:
                    inside('/opt/mobile-use-vnext/service.sh stop')
                finally:
                    push_private(restore_file, config_path)
                    inside('/opt/mobile-use-vnext/service.sh start')
                    wait_ready()
        finally:
            inside('rm -f ' + private_dir + '/launch-phase ' + private_dir +
                   '/remaining-reads ' + private_dir + '/launch-count ' + wrapper_path)
    print(json.dumps({'normal_private_config_restored': config_changed}), flush=True)
