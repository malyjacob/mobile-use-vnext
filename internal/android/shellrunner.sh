#!/system/bin/sh
# mobile-use-vnext managed shell runner, protocol version 1.
#
# Usage: shellrunner.sh <private-dir> <deadline-seconds>
#
# The service has already written <private-dir>/command. This script detaches
# from the adb shell, runs the command in its own session, keeps a bounded
# output prefix (64 KiB + one block), drains the rest without closing the
# producer pipe while the managed command runs, and writes atomic metadata.
#
# Files:
#   version   protocol version ("1")
#   command   user script (written by the service, never in this script)
#   cmdpid    session/process-group leader PID
#   start     start_v1<TAB>pid<TAB>pgid<TAB>start_ticks<TAB>boot_id
#   exit      natural exit code (decimal), written only before a stop request
#   done      done_v1<TAB>termination<TAB>exit_code<TAB>bytes<TAB>truncated
#   stop      service request to finalize (written before a kill)
#   reason    controlled reason: timeout / cancelled
#   output    retained output prefix (at most 69632 bytes)

dir="$1"
deadline="$2"
[ -n "$dir" ] || exit 2
case "$deadline" in ''|*[!0-9]*) deadline=30 ;; esac
umask 022
mkdir -p "$dir" || exit 2
printf '1\n' > "$dir/version"

out="$dir/output"
: > "$out" || exit 2

# Device-authoritative deadline supervision. It only fires when the service
# has not already finalized the run; the service's own wait is the backstop.
(
  waited=0
  while [ "$waited" -lt "$deadline" ]; do
    if [ -e "$dir/done" ] || [ -e "$dir/exit" ] || [ -e "$dir/stop" ]; then
      exit 0
    fi
    sleep 1 2>/dev/null || exit 0
    waited=$((waited+1))
  done
  if [ -e "$dir/done" ] || [ -e "$dir/exit" ] || [ -e "$dir/stop" ]; then
    exit 0
  fi
  printf 'timeout\n' > "$dir/reason"
  : > "$dir/stop"
  group=$(cat "$dir/cmdpid" 2>/dev/null)
  case "$group" in ''|*[!0-9]*) exit 0 ;; esac
  kill -TERM -"$group" 2>/dev/null
  sleep 2
  kill -KILL -"$group" 2>/dev/null
) &
watchdog=$!

# First stage: a detached session records its PID (also the process-group
# leader), runs the user script, then records the natural exit code.
# Second stage: keep at most 64 KiB + one 4 KiB block, then drain the rest.
MOBILE_USE_SHELL_DIR="$dir" setsid /system/bin/sh -c '
  dir="$MOBILE_USE_SHELL_DIR"
  echo $$ > "$dir/cmdpid"
  /system/bin/sh "$dir/command" 2>&1
  code=$?
  if [ ! -e "$dir/stop" ]; then
    printf "%s\n" "$code" > "$dir/.exit.tmp"
    mv "$dir/.exit.tmp" "$dir/exit"
  fi
' | setsid /system/bin/sh -c 'dd bs=4096 count=17 2>/dev/null; cat >/dev/null' > "$out" &
reader=$!

pid=""
tries=0
while [ ! -s "$dir/cmdpid" ] && [ "$tries" -lt 100 ]; do
  sleep 0.02 2>/dev/null || sleep 1
  tries=$((tries+1))
done
if [ -r "$dir/cmdpid" ]; then
  pid=$(cat "$dir/cmdpid" 2>/dev/null)
fi
ticks=""
if [ -n "$pid" ] && [ -r "/proc/$pid/stat" ]; then
  ticks=$(awk '{print $22}' "/proc/$pid/stat" 2>/dev/null)
fi
boot=""
if [ -r /proc/sys/kernel/random/boot_id ]; then
  boot=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null)
fi
tmp="$dir/.start.tmp"
printf 'start_v1\t%s\t%s\t%s\t%s\n' "$pid" "$pid" "$ticks" "$boot" > "$tmp" && mv "$tmp" "$dir/start"

# Wait for a natural exit, an explicit stop request, or the recorded process
# disappearing. Never wait forever on a pipe that a detached background child
# may keep open: that child is outside the managed chain.
while :; do
  if [ -r "$dir/exit" ] || [ -r "$dir/stop" ]; then
    break
  fi
  if [ -n "$pid" ] && [ ! -d "/proc/$pid" ]; then
    break
  fi
  sleep 0.05 2>/dev/null || sleep 1
done

# Stop the watchdog before finalizing.
kill "$watchdog" 2>/dev/null

# Give the drainer a short flush window, then stop only the drainer. The
# managed command has already ended or was killed by the service; this never
# closes the producer pipe while the managed command is still running.
grace=0
while [ "$grace" -lt 20 ] && kill -0 "$reader" 2>/dev/null; do
  sleep 0.02 2>/dev/null || sleep 1
  grace=$((grace+1))
done
if kill -0 "$reader" 2>/dev/null; then
  kill -TERM -"$reader" 2>/dev/null || kill -TERM "$reader" 2>/dev/null
fi

reason=""
if [ -r "$dir/reason" ]; then
  reason=$(cat "$dir/reason" 2>/dev/null)
fi
code=""
if [ -r "$dir/exit" ]; then
  code=$(cat "$dir/exit" 2>/dev/null)
fi
if [ -n "$code" ]; then
  term=exited
else
  case "$reason" in
    timeout) term=timeout ;;
    cancelled) term=cancelled ;;
    *) if [ -z "$pid" ]; then term=not_started; else term=unknown; fi ;;
  esac
fi
size=$(wc -c < "$out" 2>/dev/null | tr -d ' ')
truncated=0
if [ -n "$size" ] && [ "$size" -gt 65536 ]; then
  truncated=1
fi
tmp="$dir/.done.tmp"
printf 'done_v1\t%s\t%s\t%s\t%s\n' "$term" "$code" "$size" "$truncated" > "$tmp" && mv "$tmp" "$dir/done"
