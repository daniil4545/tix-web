#!/bin/sh
# Fake tix for tests: canned output by command, every argument logged on its own line.
if [ -n "$FAKE_TIX_LOG" ]; then
  for a in "$@"; do printf '%s\n' "$a" >>"$FAKE_TIX_LOG"; done
  printf -- '--\n' >>"$FAKE_TIX_LOG"
fi
case "$1" in
status)
  printf 'idea         idea         1\nopen         open         1\n'
  ;;
projects)
  printf 'shop   /home/u/shop   1\nall    -    0   not in catalog\n'
  ;;
list)
  printf '[{"id":"T-1","title":"Buy milk","project":"shop","status":"open","path":"/home/u/tasks/T-1.md"}]\n'
  ;;
show)
  case "$2" in
  T-404) echo "err T-404: task not found" >&2; exit 4 ;;
  T-409) echo "err T-409: rule says no" >&2; exit 5 ;;
  T-410) echo "err T-410: changed since rev abc" >&2; exit 3 ;;
  T-500) echo "boom" >&2; exit 1 ;;
  T-hang) exec sleep 5 ;;
  esac
  printf '{"id":"%s","path":"/home/u/tasks/x.md","body":"## Abstract\\n\\nhi <script>alert(1)</script> [x](javascript:alert(1))","notes":[{"text":"<img src=x onerror=alert(1)> **done**"}],"comments":[]}\n' "$2"
  ;;
move)
  case "$2" in
  T-409) echo "err T-409: 0/1 criteria checked; run: tix check T-409 1" >&2; exit 5 ;;
  esac
  echo "ok $2 moved"
  ;;
*)
  echo "err unknown command $1" >&2
  exit 6
  ;;
esac
