# chriswa-devkit

Personal development toolkit combining shell customization with Claude Code safety guardrails.

## What is this?

**chriswa-devkit** is a personal monorepo containing:

- **Shell Configuration**: Custom zsh setup with aliases, prompts, and utilities
- **Claude Code Hooks**: Safety guards for bash commands (git, cd, find)
- **Claude Code Tools**: Session search and custom statusline
- **Installation Utilities**: Automated setup scripts

## Installation

```bash
cd ~/chriswa-devkit
chriswa-devkit-install
```

This will:
1. Add `source ~/chriswa-devkit/shell/index.sh` to your `~/.zshrc`
2. Register Claude Code hooks in `~/.claude/settings.json`
3. Configure custom statusline in `~/.claude/settings.json`

## Project Structure

```
chriswa-devkit/
├── shell/              # Shell customization
│   ├── index.sh       # Main entry point
│   ├── aliases.sh     # Command shortcuts
│   ├── prompt.sh      # Custom prompt with git status
│   ├── path.sh        # PATH management
│   └── killport.sh    # Port cleanup utility
├── claude/            # Claude Code integration
│   ├── hooks/         # PreToolUse safety guards
│   ├── statusline/    # Custom status display
│   └── tools/         # Session search utility
├── bin/               # Executable wrappers
│   ├── chriswa-devkit-install
│   └── claude-session-search
└── install/           # Installation scripts
    ├── index.ts       # Main installer
    ├── shell.ts       # Shell config installer
    ├── hooks.ts       # Hooks installer
    └── statusline.ts  # Statusline installer
```

## Features

### Shell Utilities

- **Git-aware prompt**: Shows branch, status colors, and exit codes
- **Convenient aliases**: `g`, `gs`, `gc`, `gb` for git; `p` for pnpm
- **Port management**: `killport 3000` to free up ports
- **Sleep control**: macOS sleep management utilities

### Claude Code Safety Guards

- **Git Mutation Guard**: Requires approval for state-changing git commands
- **Git Add Guard**: Prevents standalone `git add` (enforces atomic `git add && git commit`)
- **CD Guard**: Blocks `cd` commands (enforces subshell pattern `(cd dir && cmd)`)
- **Find Guard**: Requires `node_modules` exclusion in find commands

### Claude Code Tools

- **Session Search**: Search through all Claude Code sessions with filters
- **Custom Statusline**: Shows directory, git branch, model, cost, tokens, and session ID

## Technology

- **Runtime**: Bun
- **Language**: TypeScript (ESLint, strict mode)
- **Validation**: Zod schemas
- **Target**: ES2022
