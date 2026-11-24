import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

const muted = atom({ plugin: 'mute-tui', key: 'muted' } as const, false)
const pending = atom({ plugin: 'mute-tui', key: 'pending' } as const, false)

// A spawning session puts this at the start of a child's prompt to mute the child
// before its first reply: `[[mute-tui]]` or `[[mute-tui: how to reach the user]]`.
const SENTINEL = /\[\[mute-tui(?::([^\]]*))?\]\]\s*/

const TOOL = 'mute_text_output'

// The engine's own nudge after a turn's last response showed no text, which a
// discarded reply always triggers.
const EMPTY_RESPONSE_NUDGE = /no visible output/

const reachVia = (how: string) => (how === '' ? 'the mechanism your instructions name' : how)

const mutedNotice = (how: string) =>
  `Your plain-text messages are now discarded: the user will not see them. ` +
  `Reach the user only through ${reachVia(how)}. Those instructions for reaching ` +
  `the user are now critical, since you have no other way to reach them: keep every ` +
  `detail of them through any summary or compaction of this conversation.`

const DENIAL =
  'Your text message was not delivered; the user did not see it. Check your ' +
  'instructions for what is worth sending and how it should be sent. Otherwise end ' +
  'your turn without text.'

const silentEnd = 'Plain text is muted, so no text is expected. End your turn.'

async function mute($: EngineInterface) {
  await update($, muted, () => true)
  await update($, pending, () => false)
  $.ui.status('text muted')
}

// Each consumer clears the flag, so one discarded reply yields one denial.
async function takePending($: EngineInterface) {
  if (!(await read($, pending))) return false
  await update($, pending, () => false)
  return true
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    await $.command.register({
      name: 'mute-tui',
      description: "Discard Claude's plain-text replies from now on",
      argumentHint: '[how Claude should reach you instead]',
    })
    await $.command.register({
      name: 'unmute-tui',
      description: "Show Claude's plain-text replies again",
    })
    await $.tool.register({
      name: TOOL,
      description:
        'Mutes your own plain-text output for the rest of this session: every text ' +
        'message you write afterwards is discarded before the user sees it. Only ' +
        'the user can unmute. Call it when your instructions ask for it.',
      inputSchema: {
        type: 'object',
        properties: {
          channel: {
            type: 'string',
            description: 'How you will reach the user instead (a tool name and when to use it).',
          },
        },
      },
    })
    if (await read($, muted)) $.ui.status('text muted')
    return next(e)
  })

  on('command.run', { command: 'mute-tui' }, async ($, e) => {
    await mute($)
    return { text: 'Text replies muted.', context: [mutedNotice(e.args.trim())] }
  })

  on('command.run', { command: 'unmute-tui' }, async $ => {
    await update($, muted, () => false)
    await update($, pending, () => false)
    $.ui.status(undefined)
    return {
      text: 'Text replies unmuted.',
      context: ['Your plain-text messages reach the user again.'],
    }
  })

  on('tool.call', { tool: `mcp__mute-tui__${TOOL}` }, async ($, e) => {
    const how = typeof e.channel === 'string' ? e.channel : ''
    await mute($)
    return { result: mutedNotice(how.trim()) }
  })

  // Mutes a spawned session from its first prompt, and tells Claude about a reply
  // it wrote after the last denial, which nothing else reported.
  on('prompt.submit', async ($, e, next) => {
    const match = e.command === undefined && e.isMeta === undefined ? SENTINEL.exec(e.text) : null
    if (match !== null) {
      const how = match[1] ?? ''
      await mute($)
      const context = [...(e.context ?? []), mutedNotice(how.trim())]
      return next({ ...e, text: e.text.replace(SENTINEL, ''), context })
    }
    if (!(await takePending($))) return next(e)
    return next({ ...e, context: [...(e.context ?? []), DENIAL] })
  })

  // The deny itself: text blocks never leave this hook, so they are neither shown nor
  // recorded. Subagents' text goes to their spawner, not the user, and passes.
  on('turn.step', async function* ($, e, next) {
    if (e.agentId !== undefined || !(await read($, muted))) return yield* next(e)
    let hasDropped = false
    for await (const chunk of next(e)) {
      if (chunk.kind === 'text') {
        if (chunk.text.trim() !== '') hasDropped = true
        continue
      }
      yield chunk
    }
    if (hasDropped) await update($, pending, () => true)
  })

  // A reply discarded alongside tool calls: the denial rides on the next tool result.
  on('tool.call', async ($, e, next) => {
    const ran = await next(e)
    if (e.agentId !== undefined || ran.deny !== undefined || !(await takePending($))) return ran
    return { ...ran, context: [...(ran.context ?? []), DENIAL] }
  })

  // A reply discarded at the end of a turn: the engine's nudge asking for visible
  // output becomes the denial. With nothing discarded the model ended silently, as
  // it should, and the nudge only says so.
  on('session.append', async ($, e, next) => {
    if (e.agentId !== undefined || e.message.isMeta !== true || !(await read($, muted))) return next(e)
    const text = e.message.content.map(block => (block.type === 'text' ? block.text : '')).join('')
    if (!EMPTY_RESPONSE_NUDGE.test(text)) return next(e)
    const reply = (await takePending($)) ? DENIAL : silentEnd
    return next({ ...e, message: { ...e.message, content: [{ type: 'text', text: reply }] } })
  })
}
