# Contributing

Issues and pull requests are welcome. Keep changes small and focused, and open an issue first for anything that
changes the plugin's options, the engine protocol, or the install flow.

## Layout

| Path | What |
| --- | --- |
| `plugin/` | The `bedrock-advisor` Claude Code plugin (mod): manifest, hooks module, tests |
| `engine/` | `advisor-engine`, the Go binary the plugin runs |
| `.claude-plugin/marketplace.json` | The `model-harness` marketplace that lists the plugin |
| `install.sh`, `install.ps1` | Installers, also published on GitHub Pages |
| `docs/` | GitHub Pages site and ADRs |

The plugin and the engine talk through JSON on stdin and stdout, defined in `engine/internal/protocol`. A
breaking change to it bumps `protocol.Version` and `PROTOCOL` in `plugin/hooks/register.js` together.

## Before you open a pull request

```sh
cd engine
test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go run github.com/securego/gosec/v2/cmd/gosec@v2.30.0 ./...
cd ../plugin && claude plugin test && claude plugin validate --strict .
cd .. && shellcheck install.sh
```

CI runs the same checks and installs from source on Linux, macOS and Windows. For a bug, add a failing test
first. Architectural decisions go in `docs/adr/` as a new numbered ADR; accepted ADRs are not edited.

## Releases

The maintainer bumps `version` in `plugin/.claude-plugin/plugin.json`, moves the `[Unreleased]` block in
`CHANGELOG.md` under the new version with the release date, and pushes a matching tag `vX.Y.Z`. The release
workflow refuses a tag that doesn't match `plugin.json`, then builds the engine for six platforms and publishes
them with `checksums.txt` and build provenance attestations. The installers pin the marketplace to the release
tag, so a change merged to `main` reaches users only through a release.

## CHANGELOG (required)

Every user-facing or operational change adds an entry to `CHANGELOG.md` under `[Unreleased]`, in the same pull
request. The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

- Use only these sections: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`. A dependency bump
  is `Changed`, or `Security` when it closes an advisory. A docs change is `Changed`.
- One bullet per change, one or two sentences, claim first. The evidence (measurements, alternatives, test
  output) belongs in the pull request or an ADR; reference it, as in `(#12, ADR 0002)`.
- Never date your own entry. The maintainer moves `[Unreleased]` under a dated version heading at release.
- A second sentence is right for what an operator must do: a setting to change, a reinstall, a restart.
- Never put credentials, tokens, account ids or internal URLs in an entry; release notes republish it.
- A refactor with no observable change needs no entry.
