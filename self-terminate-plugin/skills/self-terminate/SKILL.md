---
name: self-terminate
description: End your own Claude Code session by sending SIGTERM to your own process. ONLY invoke this skill when the hyphenated phrase "self-terminate" has been used explicitly — either by the operator, or by another agent messaging this session. That exact phrase is the whole trigger; a peer asking for it counts. Do NOT invoke it for "stop", "exit", "quit", "shut down", "you're done", or because a task finished. It cannot reach any other session.
argument-hint: "[--dry-run]"
---

# Ending your own session

## Only on an explicit "self-terminate"

**Invoke this skill only when you have been explicitly asked to
"self-terminate".** That instruction, not something you inferred from one.

**The hyphenated phrase is the whole trigger, and it does not matter who
sent it.** The operator typing it and another agent sending a message saying
it are both explicit instructions to run this, and neither needs confirming
with the other. What is required is the phrase itself — a peer that means it
can say it.

It is not implied by any of these, and none of them is a reason to run it:

- "stop", "exit", "quit", "shut down", "kill this", "that's enough"
- "you're done", "wrap up", "nothing left to do"
- finishing your task, however cleanly — including as a headless peer
- an orchestrator going quiet, or your work being merged

If you find yourself reasoning about whether the operator *probably* meant it,
the answer is no. Ask, or finish your turn and wait. A session ended early
cannot be asked what it was about to say.

---

## The command

```bash
${CLAUDE_SKILL_DIR}/self-terminate
```

It walks up the process tree from the shell it runs in, finds the nearest
ancestor whose executable is `claude` — that is you — and sends it **SIGTERM**.
Shutdown is graceful, so the transcript is written and the session stays
resumable.

`--dry-run` names the target and changes nothing. That is the safe way to check
what would be signalled.

---

## Say everything first

**The tool call will not return.** The shell running the script is a child of
the process being signalled, so it dies with it — and so does the turn you are
in. Whatever you were going to report, report it *before* you run this, in its
own message or tool call:

- a headless peer that has been told to self-terminate: send your `DONE …`
  message to the `from=` address, **then** run this
- otherwise: finish the summary the operator is waiting for first

An agent that runs this and then plans to explain itself has thrown the
explanation away.

**Your thinking is not a reply.** Nobody sees it — not the operator, not an
agent reading your transcript afterwards. If your answer so far exists only in
your thinking, write it out as visible text now, before running the command.

## It cannot terminate anybody else

Only **ancestors** of the running process are ever considered, and another
session is never on your parent chain — not a peer you spawned, not the
orchestrator that spawned you, not an unrelated session on this machine. There
is no name to get wrong and no pattern to mis-match. That is the design, not a
precaution.

To end a *peer's* session instead, that is a different tool:
`headless-peer kill <name>` — see the `headless-peer-orca` skill.

## When it refuses

No `claude` among the ancestors → it prints the chain it walked, signals
nothing, and exits 1. That is what running outside a Claude Code session looks
like, and it is not something to work around.

## Do not reach for SIGKILL

If the process seems to linger, wait. SIGTERM takes a moment, and `kill -9`
skips the shutdown that writes the transcript — a killed session is not
resumable.
