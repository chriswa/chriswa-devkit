# jev

CLI wrapper around TypeSafe's System One API (model: Jev). It sends a request, prints the full response plus a `_cost_estimate` in USD, and logs billed input tokens to `usage.jsonl`.

## Install

Requires [Bun](https://bun.sh).

```sh
cd ~/chriswa-devkit/tools/jev && bun install
```

`bin/jev` symlinks to `tools/jev/jev.ts`, and `shell/path.sh` puts the devkit `bin/` on your PATH. Then add the API key to `~/.zshrc` (it's in 1Password as "typesafe.ai API Key - chriswa") and open a new shell:

```sh
export TYPESAFE_API_KEY=...
```

`jev.ts` runs directly through its `#!/usr/bin/env bun` line. It finds its files relative to its real location, so `usage.jsonl` is always written next to `jev.ts`. The usage log is specific to each machine and is not committed.

## Usage

```sh
jev request.json         # ask
echo '{...}' | jev -     # ask, request on stdin
jev --mtd                # month-to-date spend in USD (local time)
jev --help
```

Needs `TYPESAFE_API_KEY` in the environment. Exit codes: `0` success, `2` invalid input (Zod message on stderr), `1` API or other failure. Requests that get 429/529 are retried with backoff.

Live docs are the source of truth: https://docs.typesafe.ai/llms.txt. Start with [API reference](https://docs.typesafe.ai/api.md), [Primitives](https://docs.typesafe.ai/primitives.md), [How to build](https://docs.typesafe.ai/concepts/how-to-build-with-system-one.md), and [Confidence](https://docs.typesafe.ai/confidence.md).

## Input format

```json
{
  "state": "text, or a JSON object/array with the context to judge",
  "model": "jev-latest",
  "questions": {
    "<your_id>": { "type": "noul | choice | score", "instructions": "...", "criteria": ... }
  }
}
```

- `model` is optional (default `jev-latest`). Pin a versioned ID such as `jev-1.13.0` if you have tuned thresholds.
- Question IDs are only for your code. They are **not** sent to the model, so the full meaning must be in `instructions`/`criteria`.
- `instructions` and every criteria description can be a string, object, or array. Point at parts of `state` or of a structured instruction with backticked paths, e.g. ``"Is `ticket.messages[0].text` a refund request?"``.
- Limits: 64k tokens per request; 32k for `state` plus the longest question.

## Question types

| Type | Use for | `criteria` | Answer |
|---|---|---|---|
| `noul` | Does a condition hold? | optional `{ "true": "...", "false": "..." }` | `{ "type": "noul", "noul": 0.95 }` (P(yes)) |
| `choice` | One option from a set | `{ "option": "description" \| null, ... }`, 2–255 options | `{ "type": "choice", "choice": "billing", "probabilities": {...}, "confidence": 0.81 }` |
| `score` | Position on an ordered scale | `["level 0", "level 1", ...]`, 2–10 levels | `{ "type": "score", "score": 1.05, "legend": {"0": "..."}, "probabilities": {"0": 0.0, ...}, "confidence": 0.92 }` |

## Example

```json
{
  "state": "Help! My payouts have been failing for 3 days.",
  "questions": {
    "is_urgent": { "type": "noul", "instructions": "Does this convey urgency?" },
    "department": {
      "type": "choice",
      "instructions": "Which team should handle this?",
      "criteria": { "billing": "Payments, invoicing, refunds", "technical": "Bugs, outages, integrations", "none": "Doesn't fit any team" }
    },
    "frustration": {
      "type": "score",
      "instructions": "How frustrated is the customer?",
      "criteria": ["Calm", "Frustrated", "Very angry"]
    }
  }
}
```

## Best practices

- **Batch questions over the same state into one request.** They run in parallel, and `state` is billed once instead of once per call. Speculative questions are fine; ignore the answers you don't need.
- **One narrow judgment per question.** Split independent dimensions into separate questions and combine them in code.
- **Give a no-match option** in a `choice` when nothing may fit.
- **Score levels should describe concrete situations** that stand on their own, not just "low/medium/high".
- **Keep rules, math, and lookups in code.** Ask Jev only for the semantic judgment.
- **Use probabilities and confidence to gate actions**, with thresholds checked against your own data. A `noul` near 0.5 means yes and no are equally likely, not "medium".
- Jev returns judgments, not generated text. For extraction, find candidates in code and use a `choice` to select one.

## Usage log

`usage.jsonl` gets one line per successful request:

```json
{"ts":"2026-09-25T22:37:51.665Z","input_tokens":359,"output_tokens":57,"model":"jev-1.13.0","cost_estimate":0.000015078}
```

- `ts`: UTC ISO-8601 timestamp.
- `input_tokens`, `output_tokens`, `model`: copied from the API response. `model` is the versioned ID that answered, even when the request used an alias like `jev-latest`.
- `cost_estimate`: USD, computed when the line was written as `input_tokens` × the model's price. Only input tokens are billed. It's `null` if the model wasn't in the price table.
- `request_id`: included only when the API returns one.

Prices live in `USD_PER_MTOK_BY_MODEL` in `jev.ts`, keyed by versioned model ID (source: https://docs.typesafe.ai/models.md). A response from a model not in the table prints a warning to stderr, so add the new price when that happens. `--mtd` re-prices every entry since the first of the current local month from `input_tokens` and `model` using the current table, so a corrected price also fixes past totals. The stored `cost_estimate` is not recomputed.
