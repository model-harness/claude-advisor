# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-26

### Added
- `bedrock-advisor` plugin with an `advisor` tool that sends the calling agent's own transcript to a stronger model on Bedrock and returns its guidance (ADR 0001).
- Automatic consultation when the same tool error happens twice in a row, with the advice attached to the tool result.
- `advisor-engine`, a Go binary that filters Claude Code transcripts and calls the Bedrock Converse API, with an `auto` backend that falls back to Claude Code's own model call on credential or access errors.
- `install.sh` and `install.ps1`, which install a checksum-verified engine release and the plugin from the `model-harness` marketplace.

[Unreleased]: https://github.com/model-harness/claude-advisor/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/model-harness/claude-advisor/releases/tag/v0.1.0
