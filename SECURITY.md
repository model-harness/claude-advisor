# Security

Report a vulnerability privately through
[GitHub security advisories](https://github.com/model-harness/claude-advisor/security/advisories/new), not in a
public issue.

## What the project does with your data

- The plugin sends the calling agent's transcript to the advisor model: its task, messages, tool calls and tool
  results, and the context that hooks attached after tool results, which can include text other plugins or your
  own hooks added, such as file contents. It is filtered and capped by `max_context_chars`. With the `bedrock` or `auto` backend that goes to Amazon
  Bedrock in your AWS account and region; with `session`, or on fallback, it goes wherever Claude Code sends its
  own requests. Nothing is sent anywhere else.
- `advisor-engine` uses the AWS SDK default credential chain, or a Bedrock API key in `AWS_BEARER_TOKEN_BEDROCK`.
  It never stores or prints credentials, and removes ARNs and account ids from the error messages it returns. It
  opens only regular `.jsonl` files inside the Claude Code projects directory, which it derives from
  `CLAUDE_CONFIG_DIR` or the home directory rather than trusting the caller, through an `os.Root`.
- Tool results in a transcript are untrusted. The engine quotes the transcript and any error text inside tags
  named with a random per-request marker, and the advisor's system prompt tells it to treat them as data and to
  flag injected instructions, but its advice reaches the agent as a tool result, so the agent's own
  permissions remain the boundary.

## Supply chain

Release binaries are built by GitHub Actions from the tagged commit, with build provenance attestations
(`gh attestation verify <file> --repo model-harness/claude-advisor`). The installers verify each download against
the release's `checksums.txt` and pin the plugin to the same release. `install.sh` requires TLS 1.3.
`install.ps1` uses the operating system's TLS settings on PowerShell 7 and turns on TLS 1.2 and, where
available, 1.3 on Windows PowerShell 5.1, because Windows 10 has no TLS 1.3 client. Workflow actions are pinned to
commit SHAs.
