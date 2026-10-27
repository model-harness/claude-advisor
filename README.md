# claude-advisor

An advisor for Claude Code sessions that run on Amazon Bedrock.

Claude Code's built-in [advisor tool](https://code.claude.com/docs/en/advisor) lets the working model consult a
stronger model at decision points. It is a server-side tool on Anthropic's API, so it is not available on Bedrock.
This project re-creates it locally:

- **`bedrock-advisor`**, a Claude Code plugin (a [mod](https://code.claude.com/docs/en/plugins/mods/overview)),
  registers an `advisor` tool. When an agent calls it, the plugin finds **that agent's own transcript** on disk:
  a subagent's lives at `~/.claude/projects/<project>/<session>/subagents/agent-<id>.jsonl`.
- **`advisor-engine`**, a small Go binary, filters the transcript down to the task, messages, tool calls and
  results, and asks the advisor model on Bedrock (Converse API) for guidance. The guidance comes back as the tool
  result.
- When the same tool error happens twice in a row, the plugin consults the advisor on its own and attaches the
  advice to the tool result.

It fits an orchestrator setup where Opus delegates to Sonnet or Haiku subagents: the subagents can escalate
mid-task to Opus, with their full context, instead of guessing or handing back.

## Install

macOS / Linux:

```sh
curl -fsSL https://model-harness.github.io/claude-advisor/install.sh | bash
```

Windows (PowerShell):

```powershell
irm https://model-harness.github.io/claude-advisor/install.ps1 | iex
```

The installer downloads the engine for your platform from the
[latest release](https://github.com/model-harness/claude-advisor/releases/latest), verifies its SHA-256 against
the release's `checksums.txt`, adds this repository as the `model-harness` plugin marketplace pinned to the same
release tag, installs `bedrock-advisor@model-harness`, and checks your AWS credentials. Start a new Claude Code
session afterwards and run `/bedrock-advisor` to see the setup. Re-run the installer to upgrade; it reinstalls
cleanly and keeps the options you set (on macOS and Linux this needs `jq`).

Each release binary also carries a GitHub build provenance attestation. When a GitHub CLI (`gh`) new enough
to pin the tag is installed and signed in to github.com, the installer checks that the binary was built by this
repository's release workflow for the tag you install, and refuses one that fails; otherwise it warns that it
skipped the check. To check by hand:

```sh
gh attestation verify advisor-engine_linux_amd64 --repo model-harness/claude-advisor \
  --source-ref refs/tags/v0.1.3 --signer-workflow model-harness/claude-advisor/.github/workflows/release.yml
```

Options: `--version v0.1.0` / `-Version v0.1.0` (or `CLAUDE_ADVISOR_VERSION`) pins a release;
`--from-source <checkout>` / `-FromSource <checkout>` builds the engine with Go instead of downloading it;
`--uninstall` / `-Uninstall` removes everything; `CLAUDE_ADVISOR_SKIP_ATTESTATION=1` skips the build provenance check
(with a warning).

### Requirements

- Claude Code v2.1.293 or later, configured for Bedrock.
- For the engine's direct Bedrock call: AWS credentials the AWS SDK default chain can find, `AWS_REGION`, and
  `bedrock:InvokeModel` on the advisor model. Without them, the default `auto` backend sends the advisor request
  through Claude Code's own model call, with the engine still building the prompt.

## Use

Claude calls the advisor when it judges it useful: before committing to an approach, when stuck, or before
declaring a multi-step task done. You can also ask for it in a prompt or a subagent brief ("consult the advisor
before you edit anything"). The command `/bedrock-advisor` shows the status; `/bedrock-advisor doctor` checks the
engine and Bedrock access; `/bedrock-advisor ask <question>` consults the advisor about the main session.

## Configure

Set options in `/config` inside a session, or from your shell:

```sh
echo '{"advisor_model":"sonnet"}' | claude plugin configure bedrock-advisor@model-harness --values-stdin
```

`claude plugin configure bedrock-advisor@model-harness` without `--values-stdin` shows the current values.

| Option | Default | Meaning |
| --- | --- | --- |
| `advisor_model` | `opus` | Alias resolved through `ANTHROPIC_DEFAULT_OPUS_MODEL` (and the Sonnet and Haiku equivalents), or a full Bedrock model or inference profile id |
| `backend` | `auto` | `auto`: engine calls Bedrock, falls back to Claude Code's model call on credential or access errors. `bedrock`: engine only. `session`: Claude Code's model call only |
| `auto_consult` | `true` | Consult automatically when the same tool error happens twice in a row (at most 5 times per session) |
| `max_tokens` | `2048` | Cap on one advisor reply |
| `max_context_chars` | `400000` | Characters of transcript sent to the advisor; the task and the newest entries are kept |
| `engine_path` | set by the installer | Path to `advisor-engine` |

Pick an advisor at least as strong as the model that calls it. The plugin does not enforce the built-in
advisor's pairing rules.

## Compared with the built-in advisor

| | Built-in advisor | claude-advisor |
| --- | --- | --- |
| Where it runs | Inside one API request, on Anthropic's servers | A normal tool call: one extra round trip |
| Providers | Anthropic API only | Bedrock, or any provider through the `session` backend |
| Context | The exact request, server-side | The calling agent's transcript, filtered |
| Forced consultations | None | On a repeated tool error |
| Advisor prompt | Anthropic's, not published | [Open source](engine/internal/advise/advise.go) |
| Billing | Advisor model rates on the Anthropic API | Advisor model rates on Bedrock |

## How it works

```
Opus (main) --Agent(model: sonnet)--> Sonnet subagent
                                        | calls mcp__bedrock-advisor__advisor
                                        v
                         plugin tool.call hook  (knows the caller's agentId)
                                        | finds <session>/subagents/agent-<id>.jsonl
                                        v
                         advisor-engine ask  (JSON on stdin)
                                        | filters transcript, builds prompt
                                        v
                         Bedrock Converse (advisor model)
                                        | guidance
                                        v
                         tool result returned to the subagent
```

The engine keeps user and assistant messages, tool calls and tool results, and the context hooks attached after tool results (including
its own earlier advice), and drops other attachments, request snapshots, meta messages and thinking blocks, which are most of a transcript's bytes. The engine opens only regular `.jsonl` files inside the Claude Code projects directory,
which it derives itself, through an `os.Root`, so neither `..` nor a symlink can lead it elsewhere. See
[ADR 0001](docs/adr/0001-mod-plus-local-engine.md) for why this is a plugin plus a local engine rather than a
gateway.

## Develop

```sh
cd engine && go test ./...                 # engine
cd plugin && claude plugin test            # plugin hooks, with stubs
claude plugin validate --strict ./plugin   # manifest and hooks module
./install.sh --from-source .               # install from this checkout
```

See [CONTRIBUTING.md](CONTRIBUTING.md). Not affiliated with Anthropic or AWS.

## License

[MIT](LICENSE)
