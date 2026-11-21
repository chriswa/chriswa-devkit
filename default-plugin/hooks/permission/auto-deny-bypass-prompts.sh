#!/bin/sh
# PermissionRequest hook: in bypassPermissions mode, a shell command only
# reaches a permission prompt when one of Claude Code's safety checks flags it
# ("Dangerous rm operation not ruled out", critical-path removals, and the
# like). Deny those prompts on the spot so the session never stalls waiting
# for a human; the agent gets the message below and can rewrite the command.
# Every other mode passes through untouched (no output, exit 0).
payload=$(cat)
mode=$(printf '%s' "$payload" | jq -r '.permission_mode // empty' 2>/dev/null)
[ "$mode" = "bypassPermissions" ] || exit 0

cat <<'EOF'
{
  "hookSpecificOutput": {
    "hookEventName": "PermissionRequest",
    "decision": {
      "behavior": "deny",
      "message": "Auto-denied without asking the user: Claude Code's safety check flagged this command (for example a dangerous rm whose target it cannot resolve). Do not retry it as written. Rewrite it so every rm/rmdir target is a literal path inside the project (no $(...), backticks, eval, or variables in the target), or split it into simpler commands, or use a different approach."
    }
  }
}
EOF
