# agent-dead-drop

Lets an agent ask you for a secret without the secret entering its transcript. The agent runs it with a message and you get a dialog with a wide, wrapping text field. You paste the value and press Return. The secret goes to `/tmp/secret_<uuid>.txt` (mode 600, no trailing newline), and the agent gets back only the path. The file deletes itself after 10 minutes.

`bin/agent-dead-drop` symlinks to `agent-dead-drop.sh`, and `shell/path.sh` puts the devkit `bin/` on your PATH. The dialog is a small AppKit app in `dialog.swift`. The script compiles it with `swiftc` into `.build/` on first use and whenever the source changes, so it needs the Xcode command line tools.

## Usage

```sh
F=$(agent-dead-drop "Paste the GitHub PAT you just generated")
gh secret set NAME --repo OWNER/REPO < "$F"
```

Stdout is only the path. Guidance for the agent, including when the file expires, goes to stderr.

The dialog closes after 10 minutes, so agents should call it with a 600000 ms Bash timeout. The default 2-minute timeout would kill it first.

Exit codes: `0` saved (path on stdout), `1` you cancelled, left it empty, or let it time out, `2` usage error or the dialog failed to build or open.
