# 1. Advisor as a Claude Code mod plus a local engine

Date: 2026-10-26

## Status

Accepted

## Context

Claude Code's advisor tool (`advisor_20260301`) runs server-side on Anthropic's API, inside one `/v1/messages`
request, and is not available on Amazon Bedrock. We want the same outcome on Bedrock: an agent, typically a
Sonnet or Haiku subagent of an Opus orchestrator, consults a stronger model that sees its context.

Three shapes were considered:

1. **A gateway** at `ANTHROPIC_BASE_URL` that emulates the server tool in front of Bedrock. It is the only way to
   reuse the built-in `/advisor` command and UI, but every request and the AWS credentials pass through it, it
   must rewrite advisor blocks in every later request's history, and it depends on a feature flag Claude Code
   fetches from Anthropic.
2. **A skill or MCP server with a script.** A script cannot tell which agent called it, so it cannot find that
   agent's transcript.
3. **A mod plus a local engine.** A spike on Claude Code 2.1.310 showed that a mod's tool is visible to
   subagents, that its `tool.call` hook receives `agentId`, that `agentId` names the subagent's transcript file,
   and that the file already holds the agent's latest step when the hook fires.

## Decision

Build option 3. The mod registers the `advisor` tool, resolves the caller's transcript, and adds what only a hook
can do: automatic consultation on a repeated tool error. A Go binary, `advisor-engine`, filters the transcript
and calls Bedrock Converse with the AWS SDK default chain. The two speak a versioned JSON protocol over stdin and
stdout.

The default `auto` backend falls back to Claude Code's own model call (`$.model.complete`) when the engine's
Bedrock call is refused for credentials or access. In the spike environment the engine's IAM identity carried an
explicit deny on `bedrock:InvokeModel` while Claude Code's own requests succeeded, so a direct-only design would
not have worked there.

## Consequences

- Works on Bedrock and, through the `session` backend, on any provider Claude Code supports.
- Each consultation is a normal tool call: one extra round trip compared with the built-in advisor, and the
  advisor's input is rebuilt from the transcript file, not the exact request.
- The advisor prompt is ours, not Anthropic's, and the model was not trained on this tool, so call frequency
  depends on the tool description and on the repeated-error trigger.
- Users install a binary. Releases are checksummed and built in CI; the installers verify them.
- The mod depends on the mods API and the transcript layout of Claude Code 2.1.293 or later; either can change
  between releases, and CI pins the Claude Code version it tests against.
