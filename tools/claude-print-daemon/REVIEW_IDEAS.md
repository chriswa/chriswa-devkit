# Review ideas for claude-print-daemon

These ideas come from a previous Claude session that had no part in building this project. It
looked at an earlier implementation (the code as of 2026-10-13: `proc.go`, `pool.go`, `compact.go`,
`main.go`, the README, and how Spaceterm's Receptionist uses the daemon) and wrote down what came
out of it. Nothing here has been implemented, and the daemon was not changed. Line references and
behaviour may have moved on since.

That session's earlier research is in `~/research/CLAUDE_CODE_BTW_VS_CACHE.md`: how the prompt
cache works, why forking and resuming Claude Code sessions miss the cache, and the `/btw`
`side_question` control request. Claims below marked **[binary]** come from reading strings in the
Claude Code 2.1.288 binary, not from documentation, and can change in any release.

## What the review found working

The Receptionist's long-lived session sends **only what changed**: each turn carries the new
message, plus Spaceterm's status only when the session doesn't already know it (`statusToTell`
compared against `toldStatus` in `spaceterm/src/server/receptionist/receptionist.ts`). History is
append-only, so nothing has to be edited, hidden or rewound.

Usage numbers from `~/.claude-print-daemon/usage.jsonl` (2,405 lines on 2026-10-13), averages per
turn:

| Tag / source | Turns | Avg cost (notional) | Avg cache read | Avg cache write | Avg uncached input | Avg output |
|---|---|---|---|---|---|---|
| receptionist / live | 1,750 | $0.147 | 22,480 | 1,132 | 11 | 218 |
| receptionist / resume | 33 | $0.038 | 6,282 | 8,994 | 2 | 103 |
| receptionist / spare | 68 | $0.009 | 1,166 | 3,686 | 729 | 89 |
| compact:receptionist / resume | 97 | $0.042 | 0* | 0* | 0* | 0* |

\* A `/compact` result's `usage` is all zeros; the real numbers are only in `modelUsage` /
`compaction.cache_read_tokens` / `input_tokens` (see `compact.go`).

Other tags: auto-stamp/spare 183 turns, receptionist-bench 167, summary-chat/spare 25,
receptionist-handover 18. Notional cost by tag: receptionist $260.19, compact:receptionist $4.11,
receptionist-bench $4.91, receptionist-test $3.30, receptionist-handover $2.40,
receptionist-fork $1.50, auto-stamp $1.25.

Live turns read about 95% of their context from the cache and write only the new part. The daemon
never forks: new sessions use `--session-id` and continuing ones use `--resume`, so the
session-ID-in-system-prompt cache bug
([#77306](https://github.com/anthropics/claude-code/issues/77306)) can't happen.

Also done well:
- Abort semantics: `prompt_in_session`, and "an abort is not a rewind".
- Refusing a changed system prompt with a 422 instead of quietly answering under the old one.
- Scrubbing inherited `CLAUDE_CODE_*` / `CLAUDECODE` session variables from the environment.
- Killing processes on purpose so `modelUsage` stays per-process (explained in `compact.go`).
- The test binary doubling as a fake `claude -p` (`CPD_FAKE_CLAUDE`), with a fake clock.

## Problems

1. **The 55-minute "compact while the cache is warm" rule doesn't save anything on current
   Claude Code builds.** Compaction runs `/compact` in a separately resumed process (live processes
   have `--disable-slash-commands`). The README notes that the compaction's API call reuses the
   cache only up to the end of the first user message; everything after is paid uncached. That
   matches the resume cache bug
   [#93490](https://github.com/anthropics/claude-code/issues/93490) (open): on resume, Claude Code
   re-sends the `role: system` message carrying SessionStart context and `# Environment` as a plain
   string instead of the original block array with `cache_control`, so the prefix stops matching
   there. So the timing (wait until `CompactAfter` = 55m, drop after `CompactLatest` = 59m "since
   the cache may be cold") rests on a premise this build defeats.
2. **Resumes are where the money leaks.** Receptionist resume turns write more than they read
   (8,994 vs 6,282 average), which is #93490 again. The causes are eviction past `MaxLive` (5),
   effort or thinking changes (process flags, so a new process), and every compaction (the live
   process is killed and replaced by a resumed one). `--priority` already protects the Receptionist
   from the cap.
3. **`--no-thinking` won't carry over to newer models.** It sets `MAX_THINKING_TOKENS=0`. That
   works for Haiku, but on Opus 5.5 thinking can't be disabled (`{type: "disabled"}` and
   `budget_tokens` both return 400), and Sonnet 5.5 uses `between_tools` instead. If a
   `--no-thinking` profile is ever pointed at those models, check what Claude Code actually does
   with the variable.

## Ideas

1. **Compact inside the live process.** `--disable-slash-commands` is the only reason compaction
   needs a resumed process. Drop that flag on live processes and have the daemon reject or escape
   caller prompts that begin with `/`. Then `/compact` runs where the cache really is warm, the
   live process needn't be replaced (one fewer resume), and the 55-minute rule means something
   again. First check whether the stream-json control protocol has a compact request that avoids
   slash commands altogether (not verified).
2. **Add `POST /v1/aside` using the `side_question` control request.** **[binary]**
   - Request frame on the claude process's stdin:
     `{"type":"control_request","request_id":"<id>","request":{"subtype":"side_question","question":"…","history":[{"question":"…","response":"…"}]}}`.
     The answer comes back in a `control_response` as `{response, synthetic}` (plus
     `refusalFallback` if relevant); progress arrives as `system`/`control_request_progress`
     (`status: "started" | "api_retry"`). `response: null` means no answer.
   - Inside Claude Code it reuses the session's last cache-safe request parameters, appends the
     question, and runs with `maxTurns: 1`, tools denied, `skipCacheWrite: true` and
     `skipTranscript: true`: it reads the session's cache, writes nothing and leaves no trace in
     history. `history` entries go after the cached prefix, so they're re-sent at full price.
   - It fits this daemon unusually well: processes already run with `--tools ""`, so the main
     limit of side questions (no tools) costs nothing.
   - The headless handler takes `isTurnInProgress` / `getLiveMessages`, so it may answer while a
     turn is in flight, and `/v1/aside` might not need the busy 409. Verify.
   - Unknown: whether a freshly resumed or spare process can answer before its first turn. The
     headless handler has a `buildFallbackParams` path, which suggests it can.
   - Uses for the Receptionist: cheap checks against its own context ("is this worth saying
     aloud?", "which agent do they mean?"), speculative drafts, or state too large or short-lived to
     send as a delta. (It already asks *other agents* side questions through Spaceterm's
     `side-questions.ts`; this would let it ask its own session.)
3. **Show cache efficiency in `status`.** Read / write / uncached tokens per tag and per source,
   i.e. the table above as one command. Today resume and compaction leaks only show up by tallying
   `usage.jsonl` by hand.
4. **Record why each resume happened** (evicted, idle-expired, profile change, after compaction) in
   `usage.jsonl`, so problem 2 can be acted on.
5. **Run a canary when the binary updates.** Cache behaviour depends on Claude Code version details
   (#93490, and [#89651](https://github.com/anthropics/claude-code/issues/89651), which regressed
   in 2.1.237 and was reportedly fixed around 2.1.248–2.1.250). The daemon already replaces spares
   after 15 minutes to pick up updates. On a new version it could run one live turn and one resume,
   and flag the result in `daemon.log` if the read ratio drops.

## Priority order

1. Compaction in the live process (idea 1): it fixes a design premise and removes resumes.
2. `/v1/aside` (idea 2).
3. Observability: cache efficiency in `status` (idea 3), resume reasons (idea 4), version canary
   (idea 5).

## Related issues

- [#77306](https://github.com/anthropics/claude-code/issues/77306): forks change the session ID in
  the system prompt's scratchpad path, so the whole conversation is rewritten.
- [#93490](https://github.com/anthropics/claude-code/issues/93490): resume re-serializes the
  session-start system message, so the first request after a resume misses past the static prefix.
- [#96101](https://github.com/anthropics/claude-code/issues/96101): an entry whose last breakpoint
  sits on a trailing `role: system` message is never reused (per a comment on #93490).
- [#89651](https://github.com/anthropics/claude-code/issues/89651): headless SessionStart hook
  context sent uncached (2.1.237 onward, reportedly fixed later).
