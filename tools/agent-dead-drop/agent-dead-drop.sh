#!/usr/bin/env bash
# Pops a macOS dialog asking the user for a secret, saves it to a temp file that
# deletes itself after 10 minutes, and prints only the file's path on stdout so
# callers can capture it with F=$(agent-dead-drop "..."). Guidance for the agent
# goes to stderr. The secret itself is never printed.
#
# Everything runs inside main so bash parses the whole file before the dialog
# opens; editing or pulling this file during a run can't change what executes.
set -euo pipefail

TTL_SECONDS=600

usage() {
  cat <<'EOF'
Usage: F=$(agent-dead-drop "<message shown to the user>")

Asks the user to paste a secret into a dialog and saves it to
/tmp/secret_<uuid>.txt (mode 600, no trailing newline). Stdout is only that
path; guidance goes to stderr. Never cat or print the file; pass it to commands:
  gh secret set NAME --repo OWNER/REPO < "$F"
  op item create ... "credential=$(cat "$F")"

The file is deleted after 10 minutes. The dialog also closes after 10 minutes,
so run this with a Bash timeout of 600000 ms.

Exit codes: 0 saved, 1 user cancelled / left it empty / timed out, 2 error.
EOF
}

# The dialog is a small Swift app (dialog.swift), compiled on first use and
# whenever the source changes. Builds go to a temp name and are moved into place,
# so concurrent first runs don't see a half-written binary.
DIR="$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")"
DIALOG_SRC="$DIR/dialog.swift"
DIALOG_BIN="$DIR/.build/dead-drop-dialog"

build_dialog() {
  if [[ -x "$DIALOG_BIN" && ! "$DIALOG_SRC" -nt "$DIALOG_BIN" ]]; then
    return
  fi
  mkdir -p "$DIR/.build"
  local tmp="$DIALOG_BIN.$$"
  if ! swiftc -O -o "$tmp" "$DIALOG_SRC" >&2; then
    rm -f "$tmp"
    echo "agent-dead-drop: failed to build $DIALOG_SRC" >&2
    exit 2
  fi
  mv -f "$tmp" "$DIALOG_BIN"
}

main() {
  if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    usage
    exit 0
  fi
  if [[ $# -ne 1 || -z "$1" ]]; then
    usage >&2
    exit 2
  fi

  build_dialog
  local secret status=0
  secret="$("$DIALOG_BIN" "$1" "$TTL_SECONDS")" || status=$?
  if [[ $status -eq 1 ]]; then
    secret=""
  elif [[ $status -ne 0 ]]; then
    echo "agent-dead-drop: dialog failed (exit $status)" >&2
    exit 2
  fi

  if [[ -z "$secret" ]]; then
    echo "The user did not provide a secret (cancelled, left it empty, or the dialog timed out)." >&2
    exit 1
  fi

  local path
  path="/tmp/secret_$(uuidgen | tr '[:upper:]' '[:lower:]').txt"
  (umask 077 && set -o noclobber && printf '%s' "$secret" >"$path")
  unset secret

  # Detach fully (no inherited fds) so the caller isn't held open until the sleep ends.
  nohup bash -c 'sleep "$1"; rm -f "$2"' _ "$TTL_SECONDS" "$path" </dev/null >/dev/null 2>&1 &
  disown

  printf '%s\n' "$path"
  cat >&2 <<EOF
agent-dead-drop: secret saved to
  $path
Do not read, cat, or print it; use it only inside commands that move it to its
destination. It is deleted automatically in 10 minutes (around $(date -v+"${TTL_SECONDS}"S '+%-I:%M %p')), so store it in
1Password or wherever it is needed before then.
EOF
}

main "$@"
