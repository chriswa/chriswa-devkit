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

### 3. `self-terminate` plugin (optional, intentionally NOT installed)

The marketplace also offers a second plugin, `self-terminate`, holding a skill
that lets a session end itself by SIGTERMing its own `claude` process. It is
shipped **uninstalled on purpose**: on machines running spaceterm, the
`mcp__plugin_spaceterm-hooks_spaceterm__self_terminate` MCP tool does the same
job and is surface-aware (it stops at the pty daemon and archives the surface).
Having both live means you cannot tell which one fired.

Install it only on a machine with no spaceterm MCP tool:

```bash
/plugin install self-terminate@chriswa-devkit-marketplace
```

### 4. `mute-tui` plugin

A hooks module that discards Claude's plain-text replies and tells Claude each
one was not delivered, so it reaches you through tools instead. It does nothing
until a session is muted: `/mute-tui [how to reach you]`, the model's
`mcp__mute-tui__mute_text_output` tool, or `[[mute-tui: how to reach you]]` at
the start of a prompt (how a spawned session is muted from its first reply).
Only `/unmute-tui` unmutes. Replies that say nothing, such as "Idle." or "Ending
turn.", still reach you, so a muted model always has a way to end its turn.

```bash
/plugin install mute-tui@chriswa-devkit-marketplace
```

## Project Structure

```
chriswa-devkit/
├── .claude-plugin/           # Claude Code marketplace
│   └── marketplace.json
├── default-plugin/           # Claude Code plugin
│   ├── .claude-plugin/
│   │   └── plugin.json
│   ├── commands/             # Slash commands
│   │   └── hello.md
│   ├── hooks/                # Event handlers
│   │   ├── hooks.json
│   │   └── bash/
│   │       ├── cd-guard.ts
│   │       ├── find-guard.ts
│   │       └── git-guard.ts
│   └── scripts/              # Utility scripts
│       └── capture-session-id.sh
├── self-terminate-plugin/    # Optional plugin, NOT installed by default
│   ├── .claude-plugin/
│   │   └── plugin.json
│   └── skills/
│       └── self-terminate/   # SIGTERM your own claude process
├── mute-tui-plugin/          # Mutes Claude's plain-text replies on demand
│   ├── .claude-plugin/
│   │   └── plugin.json
│   ├── hooks/                # hooks.json + register.ts (the hooks module)
│   └── types/                # state contract
├── claude/                   # Claude Code utilities (not plugin)
│   └── statusline/
├── shell/                    # Shell customization
│   ├── index.sh
│   ├── aliases.sh
│   ├── prompt.sh
│   ├── path.sh
│   └── killport.sh
├── bin/                      # Executable wrappers
│   └── chriswa-devkit-install
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

- **Custom Statusline**: Shows directory, git branch, model, cost, tokens, and session ID
- **claude-print-daemon** (`tools/claude-print-daemon`, Go): keeps `claude -p` processes warm so a reply skips Claude Code's startup. Build it into `bin/` with `(cd tools/claude-print-daemon && go build -o ../../bin/claude-print-daemon .)`

## Technology

- **Runtime**: Bun
- **Language**: TypeScript (ESLint, strict mode)
- **Validation**: Zod schemas
- **Target**: ES2022
