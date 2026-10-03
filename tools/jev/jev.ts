#!/usr/bin/env bun
// jev: send a TypeSafe System One request, print the full response plus a
// `_cost_estimate` (USD), and log input-token usage (the only billed quantity) to usage.jsonl.

import { appendFileSync, existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { z } from "zod";

const ROOT = import.meta.dir;
const USAGE_LOG = join(ROOT, "usage.jsonl");
const README = join(ROOT, "README.md");
const ENDPOINT = "https://api.typesafe.ai/v1/systemone";
const DEFAULT_MODEL = "jev-latest";
// USD per million input tokens, keyed by the versioned model ID the API reports
// in `model`. Output tokens are free. Source: https://docs.typesafe.ai/models.md
// A model missing here gets a null cost and a warning, so add new versions as they ship.
const USD_PER_MTOK_BY_MODEL: Record<string, number> = {
  "jev-1.13.0": 0.042,
};
const MAX_ATTEMPTS = 4;

const HELP = `jev: ask Jev (TypeSafe System One) typed questions from the command line.

Usage:
  jev <request.json>     send the request in a file
  jev -                  read the request from stdin
  jev --mtd              print month-to-date spend in USD (local time)
  jev --help             show this help

Input: JSON with "state" and "questions" ("model" optional, defaults to ${DEFAULT_MODEL}):
  {
    "state": "Help! My payouts have been failing for 3 days.",
    "questions": {
      "is_urgent": { "type": "noul", "instructions": "Does this convey urgency?" }
    }
  }

Output: the full API response as JSON on stdout ("model", "answers", "usage"),
plus "_cost_estimate": the request's input-token cost in USD (null if the model's
price isn't in jev.ts). Errors go to stderr
with a non-zero exit code (2 = invalid input, 1 = API/other failure).

Question types: noul (yes/no probability), choice (one of a set), score (ordered levels).
Full input format, answer shapes, and best practices: ${README}
Live docs: https://docs.typesafe.ai/llms.txt

Requires TYPESAFE_API_KEY in the environment. Usage is logged to ${USAGE_LOG}.`;

// ---------- Schemas (https://docs.typesafe.ai/api.md) ----------

type Json = string | number | boolean | null | Json[] | { [k: string]: Json };
const Json: z.ZodType<Json> = z.lazy(() =>
  z.union([z.string(), z.number(), z.boolean(), z.null(), z.array(Json), z.record(z.string(), Json)]),
);
// Instructions and criteria descriptions may be a string, object, or array.
const Structured = z.union([z.string(), z.array(Json), z.record(z.string(), Json)]);

const Question = z.discriminatedUnion("type", [
  z.strictObject({
    type: z.literal("noul"),
    instructions: Structured,
    criteria: z.strictObject({ true: Structured.optional(), false: Structured.optional() }).optional(),
  }),
  z.strictObject({
    type: z.literal("choice"),
    instructions: Structured,
    criteria: z
      .record(z.string(), Structured.nullable())
      .refine((c) => Object.keys(c).length >= 2, "choice needs at least 2 options")
      .refine((c) => Object.keys(c).length <= 255, "choice allows at most 255 options"),
  }),
  z.strictObject({
    type: z.literal("score"),
    instructions: Structured,
    criteria: z.array(Structured).min(2, "score needs at least 2 levels").max(10, "score allows at most 10 levels"),
  }),
]);

const Request = z.strictObject({
  state: Structured,
  model: z.string().default(DEFAULT_MODEL),
  questions: z.record(z.string(), Question).refine((q) => Object.keys(q).length > 0, "at least one question is required"),
});

const Usage = z.object({ input_tokens: z.number().int(), output_tokens: z.number().int() });

const Answer = z.discriminatedUnion("type", [
  z.looseObject({ type: z.literal("noul"), noul: z.number() }),
  z.looseObject({
    type: z.literal("choice"),
    choice: z.string(),
    probabilities: z.record(z.string(), z.number()),
    confidence: z.number(),
  }),
  z.looseObject({
    type: z.literal("score"),
    score: z.number(),
    legend: z.record(z.string(), z.string()),
    probabilities: z.record(z.string(), z.number()),
    confidence: z.number(),
  }),
]);

const Response = z.looseObject({
  model: z.string(),
  answers: z.record(z.string(), Answer),
  usage: Usage,
  request_id: z.string().optional(),
});

const UsageLogEntry = z.object({
  ts: z.iso.datetime({ offset: true }),
  input_tokens: z.number().int(),
  output_tokens: z.number().int().optional(),
  model: z.string(),
  // Estimate at log time from USD_PER_MTOK_BY_MODEL; null if the model had no known price.
  cost_estimate: z.number().nullable(),
  request_id: z.string().optional(),
});
type UsageLogEntry = z.infer<typeof UsageLogEntry>;

// ---------- Helpers ----------

class CliError extends Error {
  constructor(message: string, readonly exitCode: number) {
    super(message);
  }
}

function formatZodError(prefix: string, err: z.ZodError): CliError {
  return new CliError(`${prefix}:\n${z.prettifyError(err)}`, 2);
}

async function readInput(arg: string): Promise<unknown> {
  const text = arg === "-" ? await Bun.stdin.text() : readInputFile(arg);
  try {
    return JSON.parse(text);
  } catch (e) {
    throw new CliError(`Input is not valid JSON: ${(e as Error).message}`, 2);
  }
}

function readInputFile(path: string): string {
  if (!existsSync(path)) throw new CliError(`No such file: ${path}`, 2);
  return readFileSync(path, "utf8");
}

async function post(body: z.infer<typeof Request>, apiKey: string): Promise<unknown> {
  for (let attempt = 1; ; attempt++) {
    const res = await fetch(ENDPOINT, {
      method: "POST",
      headers: { Authorization: `Bearer ${apiKey}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const retryable = res.status === 429 || res.status === 529;
    if (retryable && attempt < MAX_ATTEMPTS) {
      const retryAfter = Number(res.headers.get("retry-after"));
      const delayMs = retryAfter > 0 ? retryAfter * 1000 : 500 * 2 ** (attempt - 1);
      await Bun.sleep(delayMs);
      continue;
    }
    const text = await res.text();
    if (!res.ok) throw new CliError(`TypeSafe API returned ${res.status}: ${text}`, 1);
    try {
      return JSON.parse(text);
    } catch {
      throw new CliError(`TypeSafe API returned non-JSON body: ${text}`, 1);
    }
  }
}

function logUsage(entry: UsageLogEntry): void {
  appendFileSync(USAGE_LOG, JSON.stringify(entry) + "\n");
}

function readUsageLog(): UsageLogEntry[] {
  if (!existsSync(USAGE_LOG)) return [];
  return readFileSync(USAGE_LOG, "utf8")
    .split("\n")
    .filter((line) => line.trim() !== "")
    .map((line, i) => {
      const parsed = UsageLogEntry.safeParse(JSON.parse(line));
      if (!parsed.success) throw formatZodError(`${USAGE_LOG} line ${i + 1} is malformed`, parsed.error);
      return parsed.data;
    });
}

function costUsd(model: string, inputTokens: number): number | null {
  const perMtok = USD_PER_MTOK_BY_MODEL[model];
  return perMtok === undefined ? null : (inputTokens * perMtok) / 1_000_000;
}

function warnUnknownPrice(model: string): void {
  process.stderr.write(
    `jev: no price known for model "${model}"; add it to USD_PER_MTOK_BY_MODEL in ${join(ROOT, "jev.ts")}\n`,
  );
}

// Re-prices every entry from tokens and model with the current table, so fixing
// a price corrects past entries too. Entries with an unknown model are skipped
// with a warning.
function monthToDateUsd(now = new Date()): number {
  const monthStart = new Date(now.getFullYear(), now.getMonth(), 1);
  const unknownModels = new Set<string>();
  let total = 0;
  for (const e of readUsageLog()) {
    if (new Date(e.ts) < monthStart) continue;
    const cost = costUsd(e.model, e.input_tokens);
    if (cost === null) unknownModels.add(e.model);
    else total += cost;
  }
  unknownModels.forEach(warnUnknownPrice);
  return total;
}

// ---------- Main ----------

async function ask(inputArg: string): Promise<void> {
  const parsedRequest = Request.safeParse(await readInput(inputArg));
  if (!parsedRequest.success) throw formatZodError("Invalid request", parsedRequest.error);

  const apiKey = process.env.TYPESAFE_API_KEY;
  if (!apiKey) throw new CliError("TYPESAFE_API_KEY is not set", 1);

  const raw = await post(parsedRequest.data, apiKey);

  // Log billed usage before validating answers, so a schema drift in answers
  // never drops a charge from the log.
  const usage = Usage.safeParse((raw as { usage?: unknown })?.usage);
  let cost: number | null = null;
  if (usage.success) {
    const r = raw as { model?: unknown; request_id?: unknown };
    const model = typeof r.model === "string" ? r.model : "unknown";
    cost = costUsd(model, usage.data.input_tokens);
    if (cost === null) warnUnknownPrice(model);
    logUsage({
      ts: new Date().toISOString(),
      input_tokens: usage.data.input_tokens,
      output_tokens: usage.data.output_tokens,
      model,
      cost_estimate: cost,
      request_id: typeof r.request_id === "string" ? r.request_id : undefined,
    });
  }

  const response = Response.safeParse(raw);
  if (!response.success) {
    throw new CliError(
      `Unexpected response shape:\n${z.prettifyError(response.error)}\nRaw: ${JSON.stringify(raw)}`,
      1,
    );
  }
  process.stdout.write(JSON.stringify({ ...response.data, _cost_estimate: cost }, null, 2) + "\n");
}

async function main(argv: string[]): Promise<void> {
  const [arg, ...rest] = argv;
  if (arg === undefined || arg === "--help" || arg === "-h") {
    process.stdout.write(HELP + "\n");
    if (arg === undefined) process.exitCode = 2;
    return;
  }
  if (rest.length > 0) throw new CliError(`Unexpected extra arguments: ${rest.join(" ")}\n\n${HELP}`, 2);
  if (arg.toLowerCase() === "--mtd") {
    process.stdout.write(monthToDateUsd() + "\n");
    return;
  }
  if (arg.startsWith("--")) throw new CliError(`Unknown option: ${arg}\n\n${HELP}`, 2);
  await ask(arg);
}

main(Bun.argv.slice(2)).catch((e) => {
  if (e instanceof CliError) {
    process.stderr.write(e.message + "\n");
    process.exit(e.exitCode);
  }
  console.error(e);
  process.exit(1);
});
