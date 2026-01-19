#!/bin/bash
# SessionStart hook to capture the Claude Code session ID
# This makes CLAUDE_SESSION_ID available to subsequent bash commands in the session.
#
# Add to your .claude/settings.json:
# {
#   "hooks": {
#     "SessionStart": [{
#       "hooks": [{
#         "type": "command",
#         "command": "~/.claude-code/plugins/chriswa@chriswa-devkit-marketplace/scripts/capture-session-id.sh"
#       }]
#     }]
#   }
# }

LOG_FILE="/tmp/claude-session-hook.log"

exec 2>>"$LOG_FILE"

echo "=== $(date) ===" >> "$LOG_FILE"
echo "CLAUDE_ENV_FILE: $CLAUDE_ENV_FILE" >> "$LOG_FILE"

# Read stdin and save it
STDIN_DATA=$(cat)
echo "STDIN: $STDIN_DATA" >> "$LOG_FILE"

SESSION_ID=$(echo "$STDIN_DATA" | jq -r '.session_id' 2>> "$LOG_FILE")
CWD=$(echo "$STDIN_DATA" | jq -r '.cwd' 2>> "$LOG_FILE")
echo "SESSION_ID: $SESSION_ID" >> "$LOG_FILE"
echo "CWD: $CWD" >> "$LOG_FILE"

if [ -n "$CLAUDE_ENV_FILE" ] && [ -n "$SESSION_ID" ] && [ "$SESSION_ID" != "null" ]; then
  echo "export CLAUDE_SESSION_ID=$SESSION_ID" >> "$CLAUDE_ENV_FILE"
  if [ -n "$CWD" ] && [ "$CWD" != "null" ]; then
    echo "export CLAUDE_CWD=$CWD" >> "$CLAUDE_ENV_FILE"
  fi
  echo "Wrote to CLAUDE_ENV_FILE" >> "$LOG_FILE"
else
  echo "Did not write - missing CLAUDE_ENV_FILE or SESSION_ID" >> "$LOG_FILE"
fi

echo "Exiting with 0" >> "$LOG_FILE"
exit 0
