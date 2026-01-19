# chriswa-devkit

Personal development toolkit combining shell customization with Claude Code safety guardrails.

## What is this?

**chriswa-devkit** is a personal monorepo containing:

- **Shell Configuration**: Custom zsh setup with aliases, prompts, and utilities
- **Claude Code Hooks**: Safety guards for bash commands (git, cd, find)
- **Claude Code Tools**: Session search and custom statusline
- **Installation Utilities**: Automated setup scripts

## Installation

### 1. Shell & Statusline Setup

```bash
cd ~/chriswa-devkit
chriswa-devkit-install
```

This will:
1. Add `source ~/chriswa-devkit/shell/index.sh` to your `~/.zshrc`
2. Configure custom statusline in `~/.claude/settings.json`

### 2. Claude Code Plugin (Hooks)

From within Claude Code, run:

```bash
/plugin marketplace add ~/chriswa-devkit
/plugin install chriswa-devkit@chriswa-devkit-marketplace
```

This installs the Bash command safety guards as a Claude Code plugin.

**Updating the plugin:** When changes are made, bump the version in `default-plugin/.claude-plugin/plugin.json`. Claude Code will detect the new version and update automatically.

## Project Structure

```
chriswa-devkit/
├── .claude-plugin/           # Claude Code marketplace
│   └── marketplace.json
├── default-plugin/           # Claude Code plugin
│   ├── .claude-plugin/
│   │   └── plugin.json
│   ├── commands/             # Slash commands
│   │   ├── fork.md
│   │   └── hello.md
│   ├── hooks/                # Event handlers
│   │   ├── hooks.json
│   │   └── bash/
│   │       ├── cd-guard.ts
│   │       ├── find-guard.ts
│   │       └── git-guard.ts
│   └── scripts/              # Utility scripts
│       ├── iterm2-fork-session.js
│       └── capture-session-id.sh
├── claude/                   # Claude Code utilities (not plugin)
│   ├── statusline/
│   └── tools/
├── shell/                    # Shell customization
│   ├── index.sh
│   ├── aliases.sh
│   ├── prompt.sh
│   ├── path.sh
│   └── killport.sh
├── bin/                      # Executable wrappers
│   ├── chriswa-devkit-install
│   └── claude-session-search
└── install/                  # Installation scripts
    ├── index.ts
    ├── shell.ts
    └── statusline.ts
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
