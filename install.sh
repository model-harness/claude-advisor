#!/usr/bin/env bash
# Install bedrock-advisor for Claude Code: the advisor-engine binary and the
# plugin that uses it, both from the same release.
#
#   curl -fsSL https://model-harness.github.io/claude-advisor/install.sh | bash
#
# The whole script is one function called on the last line, so a download cut
# short runs nothing.
set -euo pipefail

REPO="model-harness/claude-advisor"
PLUGIN="bedrock-advisor"
MARKETPLACE="model-harness"
PLUGIN_ID="$PLUGIN@$MARKETPLACE"
MIN_CLAUDE="2.1.293"
WORK=""

usage() {
  cat <<'EOF'
Install bedrock-advisor for Claude Code.

  install.sh [--version vX.Y.Z] [--from-source DIR] [--uninstall]

  --version vX.Y.Z    release to install (default: latest; env CLAUDE_ADVISOR_VERSION)
  --from-source DIR   build the engine from a checkout and install the plugin from it
  --uninstall         remove the plugin, its marketplace and the engine
EOF
}

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# version_ge A B: true when dotted version A >= B.
version_ge() {
  awk -v a="$1" -v b="$2" 'BEGIN {
    na = split(a, x, "."); nb = split(b, y, ".")
    for (i = 1; i <= (na > nb ? na : nb); i++) {
      if ((x[i] + 0) > (y[i] + 0)) exit 0
      if ((x[i] + 0) < (y[i] + 0)) exit 1
    }
    exit 0
  }'
}

# TLS 1.3 is required where curl can enforce it. macOS's system curl cannot
# set a TLS minimum ("curl: (4)"), so there it falls back to 1.2 or later,
# once, with a warning; downloads are still checked against checksums.txt.
TLS_FLAG="--tlsv1.3"

# probe_tls picks the TLS flag once, before any download.
probe_tls() {
  local rc=0
  curl -fsS --proto '=https' --tlsv1.3 -I -o /dev/null "https://github.com" 2>/dev/null || rc=$?
  if [ "$rc" = 4 ]; then
    warn "this curl cannot require TLS 1.3; requiring TLS 1.2 or later instead"
    TLS_FLAG="--tlsv1.2"
  fi
}

curl_https() { curl -fsSL --proto '=https' "$TLS_FLAG" "$@"; }

fetch() { curl_https -o "$1" "$2"; }

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# verify_attestation FILE TAG checks the binary's build provenance when the
# GitHub CLI can: that it was built by this repository's release workflow for
# exactly TAG, on a GitHub-hosted runner. A checksum from the same release
# can't prove that. Without a usable gh the check is skipped, with a warning.
verify_attestation() {
  local file="$1" tag="$2" help out
  if [ "${CLAUDE_ADVISOR_SKIP_ATTESTATION:-}" = 1 ]; then
    warn "build provenance not checked (CLAUDE_ADVISOR_SKIP_ATTESTATION=1)"
    return
  fi
  if ! command -v gh >/dev/null 2>&1; then
    warn "build provenance not checked: install the GitHub CLI (gh) to verify it"
    return
  fi
  help=$(gh attestation verify --help </dev/null 2>&1) || help=""
  # Each flag on its own: gh lists them alphabetically, not in this order.
  case "$help" in *--source-ref*) ;; *) help="" ;; esac
  case "$help" in *--signer-workflow*) ;; *) help="" ;; esac
  if [ -z "$help" ]; then
    warn "build provenance not checked: this gh is too old for 'gh attestation verify --source-ref' (update gh)"
    return
  fi
  if ! gh auth status --active --hostname github.com </dev/null >/dev/null 2>&1; then
    warn "build provenance not checked: gh is not signed in to github.com"
    return
  fi
  if ! out=$(gh attestation verify "$file" --hostname github.com --repo "$REPO" \
    --source-ref "refs/tags/$tag" --signer-workflow "$REPO/.github/workflows/release.yml" \
    --deny-self-hosted-runners </dev/null 2>&1); then
    printf '%s\n' "$out" >&2
    die "build provenance check failed for $(basename "$file") ($tag); not installing it"
  fi
  say "Build provenance verified"
}

marketplace_added() { claude plugin marketplace list --json </dev/null 2>/dev/null | grep -q "\"name\": *\"$MARKETPLACE\""; }
plugin_installed() { claude plugin list --json </dev/null 2>/dev/null | grep -q "\"id\": *\"$PLUGIN_ID\""; }

# saved_config prints the options the user set, except engine_path, as JSON.
saved_config() {
  if ! command -v jq >/dev/null 2>&1; then
    warn "jq is not installed, so your bedrock-advisor settings will not carry over; set them again with /config"
    echo '{}'
    return
  fi
  claude plugin configure "$PLUGIN_ID" --json </dev/null 2>/dev/null |
    jq -c '.inputs as $i | [(.configured // [])[] | select(. != "engine_path") | {key: ., value: $i[.]}] | from_entries' 2>/dev/null ||
    echo '{}'
}

resolve_latest() {
  local url tag
  url=$(curl_https -I -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
    die "could not reach https://github.com/$REPO/releases/latest"
  tag=${url##*/}
  case "$tag" in
    v[0-9]*.[0-9]*.[0-9]*) printf '%s\n' "$tag" ;;
    *) die "no release of $REPO found" ;;
  esac
}

main() {
  local version="${CLAUDE_ADVISOR_VERSION:-latest}" from_source="" uninstall=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --version) version="${2:?--version needs a tag}"; shift 2 ;;
      --from-source) from_source="${2:?--from-source needs a directory}"; shift 2 ;;
      --uninstall) uninstall=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) usage >&2; die "unknown option: $1" ;;
    esac
  done

  command -v claude >/dev/null 2>&1 || die "Claude Code is not on PATH. Install it first: https://code.claude.com/docs"
  local config_dir="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
  local bin_dir="$config_dir/plugins/data/$PLUGIN-$MARKETPLACE/bin"
  local engine="$bin_dir/advisor-engine"

  if [ "$uninstall" = 1 ]; then
    say "Removing $PLUGIN_ID"
    if plugin_installed; then claude plugin uninstall "$PLUGIN_ID" </dev/null || true; fi
    if marketplace_added; then claude plugin marketplace remove "$MARKETPLACE" </dev/null || true; fi
    rm -f "$engine"
    say "Done"
    return
  fi

  local have
  have=$(claude --version </dev/null 2>/dev/null | awk '{print $1}')
  [ -n "$have" ] || die "could not read the Claude Code version"
  version_ge "$have" "$MIN_CLAUDE" || die "Claude Code $have is too old; $MIN_CLAUDE or later is required (run: claude update)"

  local os arch
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS $(uname -s); on Windows use install.ps1" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "unsupported CPU $(uname -m)" ;;
  esac

  WORK=$(mktemp -d)
  trap 'rm -rf "$WORK"' EXIT

  # Build or download the engine before touching the current install.
  local source
  if [ -n "$from_source" ]; then
    from_source=$(cd "$from_source" && pwd)
    [ -f "$from_source/engine/go.mod" ] || die "$from_source is not a claude-advisor checkout"
    command -v go >/dev/null 2>&1 || die "--from-source needs Go on PATH"
    say "Building advisor-engine from $from_source"
    (cd "$from_source/engine" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=source" -o "$WORK/advisor-engine" ./cmd/advisor-engine)
    source="$from_source"
  else
    probe_tls
    [ "$version" = latest ] && version=$(resolve_latest)
    local asset="advisor-engine_${os}_${arch}" base="https://github.com/$REPO/releases/download/$version"
    say "Downloading $asset ($version)"
    fetch "$WORK/$asset" "$base/$asset" || die "download failed: $base/$asset"
    fetch "$WORK/checksums.txt" "$base/checksums.txt" || die "download failed: $base/checksums.txt"
    local expected actual
    expected=$(awk -v f="$asset" 'NF >= 2 && ($2 == f || $2 == "*" f) {print $1; exit}' "$WORK/checksums.txt")
    [ -n "$expected" ] || die "$asset is not listed in checksums.txt"
    actual=$(sha256 "$WORK/$asset")
    [ "$expected" = "$actual" ] || die "checksum mismatch for $asset (expected $expected, got $actual)"
    say "Checksum verified"
    verify_attestation "$WORK/$asset" "$version"
    mv "$WORK/$asset" "$WORK/advisor-engine"
    # Pin the plugin to the same release as the engine.
    source="$REPO#$version"
  fi
  chmod 755 "$WORK/advisor-engine"

  # Reinstall from a clean slate, so a re-run, an upgrade, or a switch between
  # a release and a checkout all end with the plugin and engine from one source.
  local saved='{}'
  if plugin_installed; then saved=$(saved_config); fi
  if marketplace_added; then
    say "Removing the previous install"
    claude plugin marketplace remove "$MARKETPLACE" </dev/null
  fi

  mkdir -p "$bin_dir"
  mv -f "$WORK/advisor-engine" "$engine"
  say "Engine installed: $engine ($("$engine" version))"

  # The saved options go on the install command line, so no step needs stdin.
  local -a config=(--config "engine_path=$engine")
  if [ "$saved" != '{}' ]; then
    while IFS= read -r kv; do
      [ -n "$kv" ] && config+=(--config "$kv")
    done < <(printf '%s' "$saved" | jq -r 'to_entries[] | "\(.key)=\(.value)"')
  fi

  say "Adding marketplace $source"
  if ! claude plugin marketplace add "$source" </dev/null ||
    ! { say "Installing $PLUGIN_ID"; claude plugin install "$PLUGIN_ID" "${config[@]}" </dev/null; }; then
    if [ "$saved" != '{}' ]; then
      warn "your previous settings were: $saved"
      warn "after a successful re-run, restore them with: echo '$saved' | claude plugin configure $PLUGIN_ID --values-stdin"
    fi
    die "the plugin install failed; fix the error above and re-run the installer"
  fi
  [ "$saved" != '{}' ] && say "Kept your settings"

  say "Checking AWS credentials"
  if ! "$engine" doctor; then
    echo "The engine cannot call Bedrock directly with these credentials. With the default"
    echo "backend 'auto', the advisor falls back to Claude Code's own model call."
  fi
  say "Done. Start a new Claude Code session and run /bedrock-advisor to check the setup."
}

main "$@"
