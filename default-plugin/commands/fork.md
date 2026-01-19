---
description: Fork the current Claude Code session into a new iTerm2 split pane.
---

Run the following command to fork this session:

```bash
osascript -l JavaScript "${CLAUDE_PLUGIN_ROOT}/scripts/iterm2-fork-session.js"
```

**Prerequisites:** The `capture-session-id.sh` SessionStart hook must be configured. See the plugin README for setup instructions.
