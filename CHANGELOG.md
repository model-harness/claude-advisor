# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.2] - 2026-10-27

### Fixed
- `install.sh` works with macOS's system curl, which cannot require TLS 1.3; it now falls back to requiring TLS 1.2 or later there, with a warning.

## [0.1.1] - 2026-10-27

### Fixed
- The advisor no longer mistakes its own shortened copy of a long tool result for output the agent never saw.

### Added
- Each release run now installs the new release with the one-line installers on Linux, macOS and Windows, and fails if any install fails.

## [0.1.0] - 2026-10-26

### Added
- `bedrock-advisor` plugin with an `advisor` tool that sends the calling agent's own transcript to a stronger model on Bedrock and returns its guidance (ADR 0001).
- Automatic consultation when the same tool error happens twice in a row, with the advice attached to the tool result.
- `advisor-engine`, a Go binary that filters Claude Code transcripts and calls the Bedrock Converse API, with an `auto` backend that falls back to Claude Code's own model call on credential or access errors.
- `install.sh` and `install.ps1`, which install a checksum-verified engine release and the plugin from the `model-harness` marketplace.

[Unreleased]: https://github.com/model-harness/claude-advisor/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/model-harness/claude-advisor/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/model-harness/claude-advisor/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/model-harness/claude-advisor/releases/tag/v0.1.0
