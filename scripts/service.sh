#!/bin/sh
# Run inside the phone's Debian chroot. The service holds its own kernel lock;
# this script only manages the local process record and readiness output.
set -eu
umask 077
export PATH="/usr/local/bin:/usr/bin:/bin:/sbin:/usr/sbin"

ROOT=/opt/mobile-use-vnext
STATE=/var/lib/mobile-use-vnext
BIN="$ROOT/device-service"
CONFIG="$ROOT/config.json"
PIDFILE="$STATE/device-service.pid"
LOG="$STATE/device-service.log"
PORT=18880

mkdir -p "$STATE"

process_matches() {
    candidate_pid=$1
    case "$candidate_pid" in
        ''|*[!0-9]*) return 1 ;;
    esac
    [ -r "/proc/$candidate_pid/cmdline" ] || return 1
    tr '\000' ' ' <"/proc/$candidate_pid/cmdline" | grep -F "$BIN" >/dev/null 2>&1
}

healthy() {
    curl -fsS --max-time 2 "http://127.0.0.1:$PORT/health" >/dev/null 2>&1
}

case "${1:-status}" in
    start)
        if [ -f "$PIDFILE" ]; then
            running=$(cat "$PIDFILE")
            if process_matches "$running"; then
                printf 'Device service is already running (pid %s).\n' "$running"
                exit 0
            fi
            rm -f "$PIDFILE"
        fi
        if [ -f "$LOG" ]; then
            mv -f "$LOG.1" "$LOG.1.old" 2>/dev/null || true
            mv -f "$LOG" "$LOG.1"
        fi
        nohup "$BIN" --config "$CONFIG" >>"$LOG" 2>&1 </dev/null &
        started=$!
        printf '%s\n' "$started" >"$PIDFILE"
        attempt=0
        misses=0
        while [ "$attempt" -lt 60 ]; do
            if healthy; then
                printf 'Device service started on 127.0.0.1:%s (pid %s).\n' "$PORT" "$started"
                exit 0
            fi
            if process_matches "$started"; then
                misses=0
            else
                misses=$((misses + 1))
                # A short post-fork window can show the parent's cmdline; only a
                # sustained absence means the process really exited.
                if [ "$misses" -ge 10 ]; then
                    printf 'Device service exited during startup.\n' >&2
                    exit 1
                fi
            fi
            sleep 0.25
            attempt=$((attempt + 1))
        done
        printf 'Device service did not become healthy in time.\n' >&2
        exit 1
        ;;
    stop)
        if [ ! -f "$PIDFILE" ]; then
            printf 'Device service is not running.\n'
            exit 0
        fi
        running=$(cat "$PIDFILE")
        if ! process_matches "$running"; then
            rm -f "$PIDFILE"
            printf 'Device service is not running.\n'
            exit 0
        fi
        kill -TERM "$running" 2>/dev/null || true
        attempt=0
        while [ "$attempt" -lt 100 ]; do
            if ! process_matches "$running"; then
                rm -f "$PIDFILE"
                printf 'Device service stopped.\n'
                exit 0
            fi
            sleep 0.1
            attempt=$((attempt + 1))
        done
        kill -KILL "$running" 2>/dev/null || true
        rm -f "$PIDFILE"
        printf 'Device service did not exit gracefully and was killed.\n' >&2
        exit 1
        ;;
    status)
        if [ -f "$PIDFILE" ] && process_matches "$(cat "$PIDFILE")"; then
            if healthy; then
                printf 'Device service is running and healthy (pid %s).\n' "$(cat "$PIDFILE")"
                exit 0
            fi
            printf 'Device service is running but not healthy (pid %s).\n' "$(cat "$PIDFILE")"
            exit 1
        fi
        printf 'Device service is not running.\n'
        exit 1
        ;;
    *)
        printf 'Usage: %s {start|stop|status}\n' "$0" >&2
        exit 2
        ;;
esac
