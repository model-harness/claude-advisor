# bedrock-advisor

A Claude Code plugin that gives agents an `advisor` tool on Amazon Bedrock. When an agent calls it, a stronger
model (Opus by default) reads that agent's own transcript and returns guidance: an assessment, next steps, risks,
and when to stop. When the same tool error happens twice in a row, the plugin consults the advisor on its own and
attaches the advice to the tool result.

The plugin needs the `advisor-engine` binary, which the installer puts next to the plugin's data:

```sh
curl -fsSL https://model-harness.github.io/claude-advisor/install.sh | bash
```

Run `/bedrock-advisor` in a session to check the setup. Configuration, requirements and design:
<https://github.com/model-harness/claude-advisor>.

MIT licensed. Not affiliated with Anthropic or AWS.
