#!/bin/bash
# SessionEnd hook to output the session ID when Claude Code quits

# Read stdin to get session data
STDIN_DATA=$(cat)
SESSION_ID=$(echo "$STDIN_DATA" | jq -r '.session_id // empty')

# Write to a file so we can verify it ran
OUTPUT_FILE="/tmp/claude-session-end.log"
echo "$(date): Session ended - $SESSION_ID" >> "$OUTPUT_FILE"

# Output JSON with suppressOutput: false to display to user
if [ -n "$SESSION_ID" ]; then
  echo "{\"suppressOutput\": false, \"systemMessage\": \"Session ID: $SESSION_ID\"}"
fi

exit 0
