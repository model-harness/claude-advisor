import { expect, mock, test } from 'claude-code/testing'

const ROOT = 'C:/Users/u/.claude/projects'
const PROJECT = ROOT + '/C--work'
const SESSION = 'sess-1'
const AGENT = 'agent1'
const ENGINE = 'C:/Users/u/.claude/plugins/data/bedrock-advisor-model-harness/bin/advisor-engine.exe'
const TOOL = 'mcp__bedrock-advisor__advisor'
const USAGE = { input_tokens: 100, output_tokens: 20, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 }

type Engine = (argv: readonly string[], request: any) => any

// Stubs a Windows machine with one project holding the session and one
// subagent transcript, and an engine answered by `engine`.
function machine(on: any, engine: Engine, calls: any[] = [], opts: { engineInstalled?: boolean; lists?: number[]; mainTranscript?: boolean } = {}) {
  const installed = opts.engineInstalled !== false
  mock.env(on, {
    OS: 'Windows_NT',
    USERPROFILE: 'C:\\Users\\u',
    ANTHROPIC_DEFAULT_OPUS_MODEL: 'global.anthropic.claude-opus-5',
  })
  on('session.id', () => ({ value: SESSION }))
  on('fs.list', () => {
    if (opts.lists) opts.lists.push(1)
    return { value: [{ name: 'C--work', kind: 'dir', size: 0, isLink: false }] }
  })
  // e.path arrives absolute in the host's own form, so compare normalized.
  on('fs.exists', ($: any, e: any) => {
    const path = String(e.path).split('\\').join('/')
    const known = [PROJECT + '/' + SESSION + '/subagents/agent-' + AGENT + '.jsonl', PROJECT + '/' + SESSION + '/subagents/agent-other.jsonl']
    if (opts.mainTranscript !== false) known.push(PROJECT + '/' + SESSION + '.jsonl')
    if (installed) known.push(ENGINE)
    return { value: known.some(p => path.endsWith(p.slice(2))) }
  })
  on('process.run', ($: any, e: any) => {
    if (!installed) return { deny: 'spawn ENOENT' }
    const request = e.init && e.init.stdin ? JSON.parse(e.init.stdin) : null
    calls.push({ argv: e.argv, request })
    return { value: { exitCode: 0, stdout: JSON.stringify(engine(e.argv, request)), stderr: '' } }
  })
  on('ui.log', () => ({ value: undefined }))
}

test('a subagent advisor call reads that agent transcript and returns the advice', async ($, on) => {
  const calls: any[] = []
  machine(on, () => ({ protocol: 1, ok: true, text: 'Check the fixture first.', usage: { inputTokens: 10, outputTokens: 5 } }), calls)
  const out = await $.tool.call({ tool: TOOL, question: 'Am I on track?', agentId: AGENT })
  expect(out.result).toBe('Check the fixture first.')
  expect(calls.length).toBe(1)
  expect(calls[0].argv[1]).toBe('ask')
  expect(calls[0].request).toMatchObject({
    protocol: 1,
    mode: 'ask',
    model: 'global.anthropic.claude-opus-5',
    question: 'Am I on track?',
    projectsRoot: ROOT,
    transcriptPath: PROJECT + '/' + SESSION + '/subagents/agent-' + AGENT + '.jsonl',
  })
})

test('auto backend falls back to Claude Code model call when Bedrock refuses, and stays there', async ($, on) => {
  const calls: any[] = []
  machine(on, (argv, req) =>
    req.mode === 'ask'
      ? { protocol: 1, ok: false, error: { kind: 'access_denied', message: 'explicit deny' } }
      : { protocol: 1, ok: true, system: 'SYS', prompt: 'PROMPT' }, calls)
  const models: any[] = []
  on('model.complete', ($: any, e: any) => {
    models.push(e)
    return { value: { isAnswered: true, text: 'Fallback advice.', usage: USAGE } }
  })
  const first = await $.tool.call({ tool: TOOL, agentId: AGENT })
  expect(first.result).toBe('Fallback advice.')
  expect(models[0]).toMatchObject({ model: 'opus', system: 'SYS', prompt: 'PROMPT' })
  const second = await $.tool.call({ tool: TOOL, agentId: AGENT })
  expect(second.result).toBe('Fallback advice.')
  expect(calls.map(c => c.request.mode)).toEqual(['ask', 'prompt', 'prompt'])
})

test('a non-fallback engine error is reported to the agent', async ($, on) => {
  machine(on, () => ({ protocol: 1, ok: false, error: { kind: 'throttled', message: 'slow down' } }))
  const out = await $.tool.call({ tool: TOOL, agentId: AGENT })
  expect(out.result).toMatch(/^Advisor unavailable: throttled: slow down/)
})

test('a missing transcript is reported, not thrown', async ($, on) => {
  machine(on, () => ({ protocol: 1, ok: true, text: 'unused' }))
  const out = await $.tool.call({ tool: TOOL, agentId: 'nobody' })
  expect(out.result).toContain('no saved conversation for agent nobody')
})

test('an unsafe agent id never reaches the file system', async ($, on) => {
  const lists: number[] = []
  machine(on, () => ({ protocol: 1, ok: true, text: 'unused' }), [], { lists })
  const out = await $.tool.call({ tool: TOOL, agentId: '../../etc' })
  expect(out.result).toContain('no saved conversation for agent ../../etc')
  expect(lists.length).toBe(0)
})

test('a missing engine is reported as not installed', async ($, on) => {
  machine(on, () => ({}), [], { engineInstalled: false })
  const out = await $.tool.call({ tool: TOOL, agentId: AGENT })
  expect(out.result).toMatch(/advisor-engine is not installed at .*run the installer/)
})

test('a bare-string reply from an older model.complete is accepted', async ($, on) => {
  machine(on, (argv, req) =>
    req.mode === 'ask'
      ? { protocol: 1, ok: false, error: { kind: 'credentials', message: 'no creds' } }
      : { protocol: 1, ok: true, system: 'SYS', prompt: 'PROMPT' })
  on('model.complete', () => ({ value: 'String advice.' }))
  const out = await $.tool.call({ tool: TOOL, agentId: AGENT })
  expect(out.result).toBe('String advice.')
})

test('the same tool error twice consults the advisor once and attaches the advice', async ($, on) => {
  const calls: any[] = []
  machine(on, () => ({ protocol: 1, ok: true, text: 'Run go mod tidy first.' }), calls)
  on('tool.call', () => ({ result: 'missing go.sum entry', isError: true }))
  const first = await $.tool.call({ tool: 'Bash', command: 'go build', agentId: AGENT })
  expect(first.context).toBeUndefined()
  const second = await $.tool.call({ tool: 'Bash', command: 'go build', agentId: AGENT })
  expect(second.context?.[0]).toMatch(/Run go mod tidy first\./)
  expect(calls[0].request).toMatchObject({ trigger: 'repeated_error', detail: 'missing go.sum entry' })
  expect(calls[0].request.question).not.toContain('missing go.sum entry')
  const third = await $.tool.call({ tool: 'Bash', command: 'go build', agentId: AGENT })
  expect(third.context).toBeUndefined()
  expect(calls.length).toBe(1)
})

test('a successful call between errors resets the count', async ($, on) => {
  const calls: any[] = []
  machine(on, () => ({ protocol: 1, ok: true, text: 'advice' }), calls)
  let n = 0
  on('tool.call', () => (++n === 2 ? { result: 'ok' } : { result: 'boom', isError: true }))
  await $.tool.call({ tool: 'Bash', command: 'x', agentId: AGENT })
  await $.tool.call({ tool: 'Bash', command: 'x', agentId: AGENT })
  const third = await $.tool.call({ tool: 'Bash', command: 'x', agentId: AGENT })
  expect(third.context).toBeUndefined()
  expect(calls.length).toBe(0)
})

test('/bedrock-advisor status reports engine, model and backend', async ($, on) => {
  machine(on, () => ({ version: '0.1.0', protocol: 1 }))
  const out = await $.command.run({ command: 'bedrock-advisor', args: '' })
  expect(out.text).toContain('engine: ' + ENGINE)
  expect(out.text).toContain('advisor model: opus -> global.anthropic.claude-opus-5')
  expect(out.text).toContain('backend: auto')
})

test('errors are tracked per agent', async ($, on) => {
  const calls: any[] = []
  machine(on, () => ({ protocol: 1, ok: true, text: 'advice' }), calls)
  on('tool.call', () => ({ result: 'boom', isError: true }))
  await $.tool.call({ tool: 'Bash', command: 'x', agentId: AGENT })
  const other = await $.tool.call({ tool: 'Bash', command: 'x', agentId: 'other' })
  expect(other.context).toBeUndefined()
  expect(calls.length).toBe(0)
})

test('automatic consultations stop at five per session', async ($, on) => {
  const calls: any[] = []
  machine(on, () => ({ protocol: 1, ok: true, text: 'advice' }), calls)
  let n = 0
  // Each error text repeats once, then the next one starts.
  on('tool.call', () => ({ result: 'error ' + Math.floor(n++ / 2), isError: true }))
  for (let i = 0; i < 14; i++) await $.tool.call({ tool: 'Bash', command: 'x', agentId: AGENT })
  expect(calls.length).toBe(5)
})

test('a failed automatic consultation does not use up the allowance', async ($, on) => {
  const calls: any[] = []
  machine(on, () => (calls.length === 1 ? { protocol: 1, ok: false, error: { kind: 'throttled', message: 'later' } } : { protocol: 1, ok: true, text: 'advice' }), calls)
  let n = 0
  on('tool.call', () => ({ result: 'error ' + Math.floor(n++ / 2), isError: true }))
  for (let i = 0; i < 14; i++) await $.tool.call({ tool: 'Bash', command: 'x', agentId: AGENT })
  expect(calls.length).toBe(6)
})

test('a refused tool call never counts as an error', async ($, on) => {
  const calls: any[] = []
  machine(on, () => ({ protocol: 1, ok: true, text: 'advice' }), calls)
  on('tool.call', () => ({ deny: 'The user declined.' }))
  for (let i = 0; i < 3; i++) await $.tool.call({ tool: 'Bash', command: 'rm -rf x', agentId: AGENT })
  expect(calls.length).toBe(0)
})

test('/bedrock-advisor ask reports the path the advice took', async ($, on) => {
  machine(on, (argv, req) =>
    req.mode === 'ask'
      ? { protocol: 1, ok: false, error: { kind: 'access_denied', message: 'denied' } }
      : { protocol: 1, ok: true, system: 'S', prompt: 'P' })
  on('model.complete', () => ({ value: { isAnswered: true, text: 'advice', usage: USAGE } }))
  const out = await $.command.run({ command: 'bedrock-advisor', args: 'ask is this ok' })
  expect(out.text).toContain('Advisor (opus via session (fallback: access_denied)):\nadvice')
})

test('/bedrock-advisor ask in a session with no saved conversation says so', async ($, on) => {
  const calls: any[] = []
  machine(on, () => ({ protocol: 1, ok: true, text: 'unused' }), calls, { mainTranscript: false })
  const out = await $.command.run({ command: 'bedrock-advisor', args: 'ask anything' })
  expect(out.text).toContain('no saved conversation for this session under ' + ROOT)
  expect(calls.length).toBe(0)
})

test('the main agent advisor call with no saved conversation says where it looked', async ($, on) => {
  const calls: any[] = []
  machine(on, () => ({ protocol: 1, ok: true, text: 'unused' }), calls, { mainTranscript: false })
  const out = await $.tool.call({ tool: TOOL, question: 'ok?' })
  expect(out.result).toContain('no saved conversation for this session under ' + ROOT)
  expect(calls.length).toBe(0)
})
