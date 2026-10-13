---
name: finish
description: Close out a session cleanly — tear down processes you intentionally left running, update docs the work invalidated, do the git work, bluntly re-raise anything still unresolved, propose at most three retro items for the project's tracker, and self-terminate. ONLY invoke when the operator explicitly runs /finish. Do NOT invoke because a task finished, because you think the session is over, or because the operator said "done", "wrap up", or "thanks".
---

# Finishing a session

`/finish` is the operator handing you the close-out, not a summary you decided
to write. It has five parts, in this order. Do not reorder them — the reporting
parts come after the work parts so that what you report is true at the moment
you report it.

**Only the operator running `/finish` starts this.** A task completing does not.
A peer agent asking does not.

---

## 1. Tear down what you left running

Anything you intentionally disconnected from this session — background servers,
watchers, tailed logs, tunnels, spawned peers, a `/loop`, a scheduled wakeup —
is yours to stop. You started it; it does not stop itself when your session
ends.

Go find them rather than recalling them: check your own background tasks, then
check for processes you started that are still holding ports or files. Stop
each one, and say which ones you stopped.

If something should keep running past this session, say that too, and say what
it is — an orphaned process the operator does not know about is worse than one
they were told about.

---

## 2. Update the docs the work invalidated

Only what this session's work made wrong. A README that now describes a flag
that no longer exists, a setup step that changed, a comment that contradicts the
code beside it.

This is not a documentation pass. Do not write new docs, do not add a
changelog, do not improve prose that is merely thin. If nothing became wrong,
nothing gets edited — say so and move on.

---

## 3. Do the git work

Whatever landing this change actually requires: stage, commit with a real
message, push, open or update a PR. Follow the project's conventions and the
session's standing instructions about commits.

**Ask before anything outward-facing you were not already told to do** — a
push, a PR, a force-anything. Approval earlier in the session for one push is
not approval for this one.

If the tree is dirty with work that is not ready, do not commit it to tidy up.
Say what is uncommitted and why.

---

## 4. Re-raise what is still unresolved — bluntly

**Assume the operator has not read your messages.** Not the careful caveat in
the middle of a long turn, not the question you asked six tool calls ago, not
the assumption you flagged in passing. Assume it did not land, because most of
the time it did not.

So sweep the session for anything genuinely still open:

- a question you asked that never got an answer
- a decision you deferred, or made provisionally
- an assumption you proceeded under that could be wrong
- work that is incomplete, blocked, or that you scaled down
- something you found and did not act on because it needed a call from them

### Sweep for all of them, then raise exactly one

**A list of five is a list nobody answers**, which is the same as having raised
none of them. Volume is not thoroughness here; it is how the important item
gets buried among the trivial ones.

So pick the single item that matters most — the one whose answer changes what
happens next — put it in front of the operator **alone**, and stop there.

The rest are not lost, they are next. When that one is settled, `/finish` takes
the next-most-important and raises that, one at a time, until nothing is left.
The close-out is a sequence of short exchanges, not a report.

**Which means the message is short.** The open item, in plain language, and
what you need from them. No status digest, no teardown inventory, no recap of
what went well, no list of things that turned out fine. If they want any of
that they will ask for it.

State it plainly and unsoftened. "I could not verify X and the change assumes
it" is the sentence — not "worth noting that X may warrant a look."

Where it needs an actual decision, use **`AskUserQuestion`** — one question, not
four. Its value is that it interrupts and demands an answer instead of
scrolling past.

### You are not authorized to resolve these

`/finish` makes no rulings. It does not:

- pick a winner on an open decision so the session can end tidy
- promote "I assumed X" into "X is settled"
- file a pending decision into `RETRO.md` or the tracker as though it were
  tech debt — it is not; it is a thing the *operator* still owes an answer on
- omit an item because raising it is awkward, or because you suspect the
  operator will be annoyed

**Notice the incentive.** This skill ends by killing your own session, which
means every unresolved item is friction between you and finishing. That
pressure will make deciding-it-yourself feel like diligence. It is not. Resolve
nothing here.

### Not shipping is a ruling too

The rule above is about *closing* things you had no standing to close. It is
not a licence to withhold. **Holding finished work back is equally a decision
made on the operator's behalf, and it hides better** — deciding something looks
presumptuous, so you catch yourself doing it, while pausing looks careful, so
you do not.

So: **if the work is done and the gates say so, put it in front of the
operator as ready to land** — and ask for the push, per part 3. Asking is not
withholding; asking is the whole remedy, because it hands them a decision
instead of a fait accompli.

**What you may not do is decide by yourself that it should wait.** "I paused
out of caution", "another agent is working nearby", "it seemed like a bad
moment" — those are rulings, and reporting one after the fact is not the same
as asking before it. If timing worries you, that is a question, not a
conclusion: `AskUserQuestion`, and let them answer. A branch nobody merged is
not a safe place to leave work.

A genuine blocker — a red gate, a failing check, a step that did not complete —
is a different thing, and it stops you honestly. Your own unease about timing
or tidiness is not that, and it does not get to act as one.

---

## 5. Propose retro items — one at a time, at most three, usually fewer

Read `RETRO.md` from your scratchpad (see the retro rule in `~/.claude/CLAUDE.md`).
Most of what is in it should die there.

For anything that survives, draft a work item — a real title and a paragraph a
future agent could act on — for however this project tracks pending work
(its issue tracker, `TODO.md`, a backlog file; look, do not assume).

**The bar is high and the cap is three.** Three is a ceiling, not a target;
zero is a normal and often correct answer. Something earns a slot only if:

- it has bitten more than once, or will predictably bite the next person
- the fix is *bounded* — you can describe what changes and what does not
- leaving it costs more than the churn of changing it

It does not earn a slot for being aesthetically wrong, for being how you would
have built it, or because `RETRO.md` had entries and empty output feels lazy.
**A refactor proposed on thin evidence is worse than the debt it names**, because
someone will do it, and it will take flexibility out of the architecture where
that flexibility was the design.

**Propose; do not file.** The tracker is shared. Show the draft and get a yes
before writing anything into it.

**One at a time, as in part 4.** Put your best candidate in front of the
operator by itself and wait. Three drafts at once is a wall they skim, and it
turns a high bar into a menu — the weak two make the strong one look like part
of a set rather than something argued on its own. If the first is accepted,
offer the next; if it is rejected, that is usually the answer for the ones
behind it too.

Ordering is part of the judgement. Lead with the strongest, and if the
strongest does not clear the bar, propose nothing at all.

---

## 6. Self-terminate — only if nothing is waiting on the operator

**Self-terminate.**

**Say everything first.** The call may not return; ending the session ends the
turn you are in. Anything you meant to report is thrown away if you
self-terminate before reporting it.

**Do not run it if anything from part 4 or part 5 is outstanding.** A session
that raises a question and then kills itself has not raised the question. If
you asked something, if a retro draft is unapproved, if a decision is still
open — report, and stop your turn there. Waiting is the correct end state.
The operator can run `/finish` again when it is clear.

Run it only when the close-out is genuinely finished and nothing is pending on
them. When in doubt, do not run it. A session that stays alive costs nothing;
one that ends early cannot be asked what it was about to say.
