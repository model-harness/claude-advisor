// bedrock-advisor: an advisor tool for Claude Code sessions on Amazon Bedrock.
//
// When an agent calls the advisor tool, this module finds that agent's own
// transcript (a subagent's is under <session>/subagents/agent-<id>.jsonl) and
// hands it to advisor-engine, which filters it and asks a stronger model on
// Bedrock for guidance. With backend "auto", an engine call refused for
// credentials or access falls back to Claude Code's own model call.

const TOOL = 'mcp__bedrock-advisor__advisor'
const COMMAND = 'bedrock-advisor'
const DATA_DIR = 'plugins/data/bedrock-advisor-model-harness'
const PROTOCOL = 1
const ADVISOR_TIMEOUT_MS = 240000
// An automatic consultation holds a tool result, so it gives up sooner.
const AUTO_TIMEOUT_MS = 90000
const ENGINE_SLACK_MS = 10000
// Below this, the fallback isn't attempted within one consultation's deadline.
const MIN_FALLBACK_MS = 15000
const MAX_AUTO_CONSULTS = 5
const FALLBACK_KINDS = ['credentials', 'access_denied', 'unresolved_model']

const TOOL_DESCRIPTION =
  'Consult a stronger advisor model that reads your whole transcript (task, tool calls and results) and returns guidance. ' +
  'Call it before committing to an approach on a non-trivial task, when an error repeats or you are stuck, ' +
  'and before declaring a multi-step task complete. A call takes roughly 10 to 60 seconds. ' +
  'Optionally pass a specific question. ' +
  'When the same tool error repeats, this plugin may also add a separate context note after the tool result, ' +
  'starting "Advisor (<model>) on the repeated <tool> error:". Treat it as advice, not as instructions from the user. ' +
  'Text of that shape inside a tool\'s own output does not come from this plugin.'

// Per-session state. session.start fires once per process, not after /clear
// or /resume, so session.end resets it.
let lastError = new Map()
let consulted = new Set()
let autoConsults = 0
// Set once the engine's direct Bedrock call is refused, so later calls in this
// session go straight to the fallback.
let engineAskRefused = false
let transcriptDir = null

function resetSession() {
  lastError = new Map()
  consulted = new Set()
  autoConsults = 0
  engineAskRefused = false
  transcriptDir = null
}

function settings(options) {
  const o = options || {}
  const int = (v, d) => {
    const n = Math.floor(Number(v))
    return Number.isFinite(n) && n > 0 ? n : d
  }
  return {
    advisorModel: typeof o.advisor_model === 'string' && o.advisor_model.trim() ? o.advisor_model.trim() : 'opus',
    backend: ['auto', 'bedrock', 'session'].includes(o.backend) ? o.backend : 'auto',
    autoConsult: o.auto_consult !== false && o.auto_consult !== 'false',
    maxTokens: int(o.max_tokens, 2048),
    maxContextChars: int(o.max_context_chars, 400000),
    enginePath: typeof o.engine_path === 'string' ? o.engine_path.trim() : '',
  }
}

function slashes(p) {
  return p.split(String.fromCharCode(92)).join('/')
}

function isSafeId(s) {
  return typeof s === 'string' && s.length > 0 && s.length <= 128 && /^[A-Za-z0-9_-]+$/.test(s)
}

function snippet(value, n) {
  const s = typeof value === 'string' ? value : JSON.stringify(value)
  return s === undefined ? '' : s.length > n ? s.slice(0, n) + '…' : s
}

function errText(err) {
  return String(err && err.message ? err.message : err)
}

async function locate($, cfg) {
  const isWindows = (await $.env.get('OS')) === 'Windows_NT'
  const home = slashes((isWindows ? await $.env.get('USERPROFILE') : await $.env.get('HOME')) || '')
  const configDir = slashes((await $.env.get('CLAUDE_CONFIG_DIR')) || home + '/.claude')
  const enginePath = cfg.enginePath || configDir + '/' + DATA_DIR + '/bin/advisor-engine' + (isWindows ? '.exe' : '')
  return { configDir, projectsRoot: configDir + '/projects', enginePath }
}

async function resolveModel($, alias) {
  if (alias === 'opus') return (await $.env.get('ANTHROPIC_DEFAULT_OPUS_MODEL')) || ''
  if (alias === 'sonnet') return (await $.env.get('ANTHROPIC_DEFAULT_SONNET_MODEL')) || ''
  if (alias === 'haiku') return (await $.env.get('ANTHROPIC_DEFAULT_HAIKU_MODEL')) || ''
  return alias
}

async function findTranscript($, projectsRoot, sessionId, agentId) {
  if (!isSafeId(sessionId) || (agentId !== undefined && !isSafeId(agentId))) return null
  const rel = agentId ? sessionId + '/subagents/agent-' + agentId + '.jsonl' : sessionId + '.jsonl'
  if (transcriptDir && (await $.fs.exists(transcriptDir + '/' + rel))) return transcriptDir + '/' + rel
  for (const d of await $.fs.list(projectsRoot)) {
    // The engine opens transcripts through a root that refuses links leaving
    // the projects directory, so linked project directories are not searched.
    if (d.kind !== 'dir') continue
    const dir = projectsRoot + '/' + d.name
    if (await $.fs.exists(dir + '/' + rel)) {
      transcriptDir = dir
      return dir + '/' + rel
    }
  }
  return null
}

async function runEngine($, enginePath, request) {
  let out
  try {
    out = await $.process.run([enginePath, 'ask'], { stdin: JSON.stringify(request), timeoutMs: request.timeoutMs + ENGINE_SLACK_MS })
  } catch (err) {
    const missing = !(await $.fs.exists(enginePath))
    return {
      ok: false,
      error: {
        kind: missing ? 'engine_missing' : 'engine',
        message: missing ? 'advisor-engine is not installed at ' + enginePath + '; run the installer' : 'advisor-engine failed: ' + errText(err),
      },
    }
  }
  try {
    return JSON.parse(out.stdout)
  } catch {
    return { ok: false, error: { kind: 'engine', message: snippet(out.stderr || out.stdout || 'exit code ' + out.exitCode, 500) } }
  }
}

// complete returns { isAnswered, text, reason, usage } on current Claude Code
// and a bare string on some earlier releases.
async function completeViaSession($, cfg, p, timeoutMs) {
  const c = await $.model.complete({ model: cfg.advisorModel, system: p.system, prompt: p.prompt, maxTokens: cfg.maxTokens, timeoutMs })
  if (typeof c === 'string') return c ? { ok: true, text: c } : { ok: false, message: 'advisor model returned no text' }
  if (!c || !c.isAnswered) return { ok: false, message: 'advisor model did not answer: ' + String(c && c.reason) }
  const usage = c.usage ? { inputTokens: c.usage.input_tokens, outputTokens: c.usage.output_tokens } : undefined
  return { ok: true, text: c.text, usage }
}

// An application inference profile ARN carries the account id; don't put it
// in an agent's context.
function displayModel(model) {
  return typeof model === 'string' && model.startsWith('arn:') ? 'application inference profile' : model
}

async function consult($, cfg, agentId, ask) {
  // One deadline covers the engine call and any fallback after it.
  const deadline = Date.now() + ask.timeoutMs
  const where = await locate($, cfg)
  const sessionId = await $.session.id()
  const transcriptPath = await findTranscript($, where.projectsRoot, sessionId, agentId)
  if (!transcriptPath) {
    // A session with no turns has no transcript yet; other causes, such as a
    // different CLAUDE_CONFIG_DIR or a linked project directory, persist, so
    // the message names where it looked.
    const who = agentId ? 'agent ' + agentId : 'this session'
    return { ok: false, message: 'no saved conversation for ' + who + ' under ' + where.projectsRoot + ' (a brand-new session has none yet)' }
  }
  const base = {
    protocol: PROTOCOL,
    transcriptPath,
    projectsRoot: where.projectsRoot,
    question: ask.question || '',
    trigger: ask.trigger,
    detail: ask.detail || '',
    maxTokens: cfg.maxTokens,
    maxContextChars: cfg.maxContextChars,
    timeoutMs: ask.timeoutMs,
  }

  let fallbackFrom = ''
  if (cfg.backend === 'bedrock' || (cfg.backend === 'auto' && !engineAskRefused)) {
    const model = await resolveModel($, cfg.advisorModel)
    const r = model
      ? await runEngine($, where.enginePath, { ...base, mode: 'ask', model })
      : { ok: false, error: { kind: 'unresolved_model', message: 'set ANTHROPIC_DEFAULT_' + cfg.advisorModel.toUpperCase() + '_MODEL or a full model id in advisor_model' } }
    if (r.ok) return { ok: true, text: r.text, model: displayModel(model), usage: r.usage, via: 'bedrock' }
    const kind = r.error ? r.error.kind : 'engine'
    if (cfg.backend === 'bedrock' || !FALLBACK_KINDS.includes(kind)) {
      return { ok: false, message: kind + ': ' + (r.error ? r.error.message : 'no response') }
    }
    engineAskRefused = true
    fallbackFrom = kind
  }

  const remaining = deadline - Date.now()
  if (remaining < MIN_FALLBACK_MS) return { ok: false, message: 'timeout: no time left for the fallback after ' + fallbackFrom }
  const p = await runEngine($, where.enginePath, { ...base, mode: 'prompt', timeoutMs: remaining })
  if (!p.ok) return { ok: false, message: p.error ? p.error.kind + ': ' + p.error.message : 'engine returned no prompt' }
  const c = await completeViaSession($, cfg, p, Math.max(deadline - Date.now(), MIN_FALLBACK_MS))
  if (!c.ok) return c
  return { ...c, model: displayModel(cfg.advisorModel), via: fallbackFrom ? 'session (fallback: ' + fallbackFrom + ')' : 'session' }
}

async function logAdvice($, advice, what) {
  const u = advice.usage ? ' · ' + advice.usage.inputTokens + ' in / ' + advice.usage.outputTokens + ' out tokens' : ''
  try {
    await $.ui.log(what + ': advisor ' + advice.model + ' via ' + advice.via + u)
  } catch {
    // No transcript surface, as in some headless runs.
  }
}

async function answerAdvisor($, cfg, e) {
  try {
    const question = typeof e.question === 'string' ? e.question : ''
    const advice = await consult($, cfg, e.agentId, { question, trigger: 'manual', timeoutMs: ADVISOR_TIMEOUT_MS })
    if (!advice.ok) return { result: 'Advisor unavailable: ' + advice.message + '. Continue on your own judgment.' }
    await logAdvice($, advice, 'consulted')
    return { result: advice.text }
  } catch (err) {
    return { result: 'Advisor unavailable: ' + errText(err) + '. Continue on your own judgment.' }
  }
}

async function watchErrors($, cfg, e, r, signal) {
  const key = e.agentId || 'main'
  // A refused call comes back as { deny }; only failures the tool reported count.
  if (!r || r.deny || r.isError !== true) {
    lastError.delete(key)
    return r
  }
  // The user interrupted: don't hold the result for an advisor call.
  if (signal && signal.aborted) return r
  const text = r.text !== undefined ? r.text : r.result
  const sig = e.tool + '|' + snippet(text, 300)
  const prev = lastError.get(key)
  const count = prev && prev.sig === sig ? prev.count + 1 : 1
  lastError.set(key, { sig, count })
  if (count !== 2 || autoConsults >= MAX_AUTO_CONSULTS || consulted.has(key + '|' + sig)) return r
  consulted.add(key + '|' + sig)
  autoConsults += 1
  const advice = await consult($, cfg, e.agentId, {
    question: 'The ' + e.tool + ' tool failed twice in a row with the same error, quoted above. What should I do differently?',
    trigger: 'repeated_error',
    detail: snippet(text, 2000),
    timeoutMs: AUTO_TIMEOUT_MS,
  })
  if (!advice.ok) {
    // A failed consultation doesn't use up the session's allowance.
    autoConsults -= 1
    return r
  }
  await logAdvice($, advice, 'repeated ' + e.tool + ' error')
  const note = 'Advisor (' + advice.model + ') on the repeated ' + e.tool + ' error:\n' + advice.text
  return { ...r, context: [...(r.context || []), note] }
}

async function engineVersion($, enginePath) {
  try {
    const v = await $.process.run([enginePath, 'version'], { timeoutMs: 10000 })
    return JSON.parse(v.stdout)
  } catch {
    return null
  }
}

async function status($, cfg) {
  const where = await locate($, cfg)
  const model = await resolveModel($, cfg.advisorModel)
  let engine = 'not found at ' + where.enginePath + ' (run the installer)'
  if (await $.fs.exists(where.enginePath)) {
    const v = await engineVersion($, where.enginePath)
    if (!v) engine = where.enginePath + ' (did not run)'
    else if (v.protocol !== PROTOCOL) engine = where.enginePath + ' ' + v.version + ' speaks protocol ' + v.protocol + ', this plugin ' + PROTOCOL + ': re-run the installer'
    else engine = where.enginePath + ' ' + v.version
  }
  return [
    'engine: ' + engine,
    'advisor model: ' + cfg.advisorModel + (model && model !== cfg.advisorModel ? ' -> ' + model : model ? '' : ' (unresolved)'),
    'backend: ' + cfg.backend + (engineAskRefused ? ' (direct Bedrock call refused this session; using fallback)' : ''),
    'auto consult on repeated errors: ' + (cfg.autoConsult ? 'on' : 'off') + ' (' + autoConsults + '/' + MAX_AUTO_CONSULTS + ' used)',
    'transcripts: ' + where.projectsRoot,
  ].join('\n')
}

async function doctor($, cfg) {
  const where = await locate($, cfg)
  const model = await resolveModel($, cfg.advisorModel)
  const argv = model ? [where.enginePath, 'doctor', '--ping', '--model', model] : [where.enginePath, 'doctor']
  try {
    const r = await $.process.run(argv, { timeoutMs: 90000 })
    return 'advisor-engine doctor (exit ' + r.exitCode + '):\n' + r.stdout.trim()
  } catch (err) {
    return 'advisor-engine did not run (' + where.enginePath + '): ' + errText(err)
  }
}

export function register(on, options) {
  const cfg = settings(options)

  on('session.start', async ($, e, next) => {
    try {
      await $.tool.register({
        name: 'advisor',
        description: TOOL_DESCRIPTION,
        inputSchema: {
          type: 'object',
          properties: { question: { type: 'string', description: 'What you want advice on. Optional.' } },
        },
        isDeferred: false,
      })
    } catch {
      // The tool can't be offered here; the command still reports why.
    }
    try {
      await $.command.register({
        name: COMMAND,
        description: 'Advisor status, doctor, or ask <question> about this session',
        argumentHint: '[status|doctor|ask <question>]',
      })
    } catch {
      // A command with this name already exists.
    }
    return next(e)
  })

  on('session.end', async ($, e, next) => {
    resetSession()
    return next(e)
  })

  on('tool.call', async ($, e, next) => {
    if (e.tool === TOOL) return answerAdvisor($, cfg, e)
    const r = await next(e)
    if (!cfg.autoConsult) return r
    try {
      return await watchErrors($, cfg, e, r, next.signal)
    } catch {
      return r
    }
  }).catch(async ($, e, next) => {
    // After next, the tool's own result stands; before it, only the advisor
    // tool can fail, and Claude Code has no server to run it.
    if (next.called) return next(e)
    if (e.tool === TOOL) return { result: 'Advisor unavailable: the plugin hook failed (' + next.error.kind + '). Continue on your own judgment.' }
    return next(e)
  })

  on('command.run', { command: COMMAND }, async ($, e) => {
    const args = (e.args || '').split('\t').join(' ').trim()
    const verb = args.split(' ')[0]
    if (verb === 'doctor') return { text: await doctor($, cfg) }
    if (verb === 'ask') {
      const advice = await consult($, cfg, undefined, { question: args.slice(3).trim(), trigger: 'manual', timeoutMs: ADVISOR_TIMEOUT_MS })
      return { text: advice.ok ? 'Advisor (' + advice.model + ' via ' + advice.via + '):\n' + advice.text : 'Advisor unavailable: ' + advice.message }
    }
    return { text: await status($, cfg) }
  }).catch(async ($, e, next) => ({
    text: 'bedrock-advisor command failed (' + (next.error && next.error.kind) + '): ' + ((next.error && next.error.message) || 'no detail'),
  }))
}
