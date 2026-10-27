<#
.SYNOPSIS
Install bedrock-advisor for Claude Code: the advisor-engine binary and the plugin that uses it, both from the same release.

.DESCRIPTION
  irm https://model-harness.github.io/claude-advisor/install.ps1 | iex

Piped through iex, set $env:CLAUDE_ADVISOR_VERSION to pick a release.

.PARAMETER Version
Release tag to install. Default: latest.

.PARAMETER FromSource
Build the engine from a claude-advisor checkout and install the plugin from it.

.PARAMETER Uninstall
Remove the plugin, its marketplace and the engine.
#>
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingWriteHost', '', Justification = 'Installer output for a person; Write-Information is hidden under irm | iex')]
[CmdletBinding()]
param(
    [string]$Version = $(if ($env:CLAUDE_ADVISOR_VERSION) { $env:CLAUDE_ADVISOR_VERSION } else { 'latest' }),
    [string]$FromSource = '',
    [switch]$Uninstall
)

# The body runs in a child scope, so under irm | iex none of its variables,
# functions or preferences leak into the caller's session.
& {
    param([string]$Version, [string]$FromSource, [bool]$Uninstall)

    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    # Exit codes are checked by hand; a caller's opt-in must not turn the
    # expected non-zero exit of doctor into an error.
    $PSNativeCommandUseErrorActionPreference = $false
    Set-StrictMode -Version Latest

    $Repo = 'model-harness/claude-advisor'
    $Plugin = 'bedrock-advisor'
    $Marketplace = 'model-harness'
    $PluginId = "$Plugin@$Marketplace"
    $MinClaude = [version]'2.1.293'

    function Say([string]$Message) { Write-Host "==> $Message" }
    function Die([string]$Message) { throw "error: $Message" }

    function Invoke-Claude {
        param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
        & claude @Arguments
        if ($LASTEXITCODE -ne 0) { Die "claude $($Arguments -join ' ') failed with exit code $LASTEXITCODE" }
    }

    # Native stderr must not abort these probes, on Windows PowerShell 5.1 too.
    function Get-ClaudeJson {
        param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
        $ErrorActionPreference = 'Continue'
        $out = & claude @Arguments 2>$null
        if ($LASTEXITCODE -ne 0 -or -not $out) { return $null }
        try { return ($out | Out-String | ConvertFrom-Json) } catch { return $null }
    }

    # Checks the binary's build provenance when the GitHub CLI can: that it was
    # built by this repository's release workflow for exactly $Tag, on a
    # GitHub-hosted runner. A checksum from the same release can't prove that.
    # Without a usable gh the check is skipped, with a warning.
    function Test-Attestation([string]$Path, [string]$Tag) {
        if ($env:CLAUDE_ADVISOR_SKIP_ATTESTATION -eq '1') { Write-Warning 'Build provenance not checked (CLAUDE_ADVISOR_SKIP_ATTESTATION=1)'; return }
        if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { Write-Warning 'Build provenance not checked: install the GitHub CLI (gh) to verify it'; return }
        $ErrorActionPreference = 'Continue'
        $help = (& gh attestation verify --help 2>&1 | Out-String)
        if ($LASTEXITCODE -ne 0 -or $help -notmatch '--source-ref' -or $help -notmatch '--signer-workflow') {
            Write-Warning "Build provenance not checked: this gh is too old for 'gh attestation verify --source-ref' (update gh)"; return
        }
        & gh auth status --active --hostname github.com *> $null
        if ($LASTEXITCODE -ne 0) { Write-Warning 'Build provenance not checked: gh is not signed in to github.com'; return }
        # Each line as a string, so Windows PowerShell 5.1 doesn't wrap gh's stderr in error records.
        $out = (& gh attestation verify $Path --hostname github.com --repo $Repo --source-ref "refs/tags/$Tag" --signer-workflow "$Repo/.github/workflows/release.yml" --deny-self-hosted-runners 2>&1 | ForEach-Object { "$_" } | Out-String)
        if ($LASTEXITCODE -ne 0) {
            Write-Host $out.Trim()
            Die "build provenance check failed for $(Split-Path -Leaf $Path) ($Tag); not installing it"
        }
        Say 'Build provenance verified'
    }

    function Test-Marketplace { [bool]((Get-ClaudeJson plugin marketplace list --json) | Where-Object { $_.name -eq $Marketplace }) }
    function Test-Plugin { [bool]((Get-ClaudeJson plugin list --json) | Where-Object { $_.id -eq $PluginId }) }

    # The options the user set, except engine_path.
    function Get-SavedConfig {
        $c = Get-ClaudeJson plugin configure $PluginId --json
        $saved = @{}
        if ($c -and $c.PSObject.Properties['configured']) {
            foreach ($key in $c.configured) {
                if ($key -ne 'engine_path') { $saved[$key] = [string]$c.inputs.$key }
            }
        }
        return $saved
    }

    function Resolve-Latest {
        $request = [System.Net.HttpWebRequest]::Create("https://github.com/$Repo/releases/latest")
        $request.Method = 'HEAD'
        $request.AllowAutoRedirect = $false
        $response = $request.GetResponse()
        try { $location = $response.Headers['Location'] } finally { $response.Close() }
        $tag = if ($location) { ($location -split '/')[-1] } else { '' }
        if ($tag -notmatch '^v\d+\.\d+\.\d+') { Die "no release of $Repo found" }
        return $tag
    }

    if (-not (Get-Command claude -ErrorAction SilentlyContinue)) {
        Die 'Claude Code is not on PATH. Install it first: https://code.claude.com/docs'
    }

    $configDir = if ($env:CLAUDE_CONFIG_DIR) { $env:CLAUDE_CONFIG_DIR } else { Join-Path $HOME '.claude' }
    $binDir = Join-Path $configDir "plugins/data/$Plugin-$Marketplace/bin"
    $engine = Join-Path $binDir 'advisor-engine.exe'

    if ($Uninstall) {
        Say "Removing $PluginId"
        if (Test-Plugin) { & claude plugin uninstall $PluginId }
        if (Test-Marketplace) { & claude plugin marketplace remove $Marketplace }
        Remove-Item -LiteralPath $engine -Force -ErrorAction SilentlyContinue
        Say 'Done'
        return
    }

    $haveText = ((& claude --version) -split ' ')[0]
    $have = $null
    if (-not [version]::TryParse($haveText, [ref]$have)) { Die "could not read the Claude Code version from '$haveText'" }
    if ($have -lt $MinClaude) { Die "Claude Code $have is too old; $MinClaude or later is required (run: claude update)" }

    $arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
        'X64' { 'amd64' }
        'Arm64' { 'arm64' }
        default { Die "unsupported CPU $_" }
    }

    # Windows PowerShell 5.1 may default to TLS 1.0; GitHub needs 1.2 or later.
    # Restored afterwards, since the setting is process-wide.
    $savedProtocol = [Net.ServicePointManager]::SecurityProtocol
    if ($PSVersionTable.PSVersion.Major -lt 6) {
        $tls = [Net.SecurityProtocolType]::Tls12
        if ([enum]::GetNames([Net.SecurityProtocolType]) -contains 'Tls13') { $tls = $tls -bor [Net.SecurityProtocolType]::Tls13 }
        [Net.ServicePointManager]::SecurityProtocol = $tls
    }

    $work = Join-Path ([System.IO.Path]::GetTempPath()) ('claude-advisor-' + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $work | Out-Null
    try {
        # Build or download the engine before touching the current install.
        $built = Join-Path $work 'advisor-engine.exe'
        if ($FromSource) {
            $FromSource = (Resolve-Path -LiteralPath $FromSource).Path
            if (-not (Test-Path -LiteralPath (Join-Path $FromSource 'engine/go.mod'))) { Die "$FromSource is not a claude-advisor checkout" }
            if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Die '-FromSource needs Go on PATH' }
            Say "Building advisor-engine from $FromSource"
            $savedCgo = $env:CGO_ENABLED
            Push-Location (Join-Path $FromSource 'engine')
            try {
                $env:CGO_ENABLED = '0'
                & go build -trimpath -ldflags '-s -w -X main.version=source' -o $built ./cmd/advisor-engine
                if ($LASTEXITCODE -ne 0) { Die 'go build failed' }
            } finally {
                Pop-Location
                $env:CGO_ENABLED = $savedCgo
            }
            $source = $FromSource
        } else {
            if ($Version -eq 'latest') { $Version = Resolve-Latest }
            $asset = "advisor-engine_windows_$arch.exe"
            $base = "https://github.com/$Repo/releases/download/$Version"
            Say "Downloading $asset ($Version)"
            Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $work $asset)
            Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile (Join-Path $work 'checksums.txt')
            $expected = $null
            foreach ($line in Get-Content -LiteralPath (Join-Path $work 'checksums.txt')) {
                $fields = @($line.Trim() -split '\s+')
                if ($fields.Count -ge 2 -and ($fields[1] -eq $asset -or $fields[1] -eq "*$asset")) { $expected = $fields[0].ToLowerInvariant(); break }
            }
            if (-not $expected) { Die "$asset is not listed in checksums.txt" }
            $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $work $asset)).Hash.ToLowerInvariant()
            if ($expected -ne $actual) { Die "checksum mismatch for $asset (expected $expected, got $actual)" }
            Say 'Checksum verified'
            Test-Attestation (Join-Path $work $asset) $Version
            Move-Item -LiteralPath (Join-Path $work $asset) -Destination $built
            # Pin the plugin to the same release as the engine.
            $source = "$Repo#$Version"
        }

        # Reinstall from a clean slate, so a re-run, an upgrade, or a switch
        # between a release and a checkout all end with one source.
        $saved = @{}
        if (Test-Plugin) { $saved = Get-SavedConfig }
        if (Test-Marketplace) {
            Say 'Removing the previous install'
            Invoke-Claude plugin marketplace remove $Marketplace
        }

        New-Item -ItemType Directory -Force -Path $binDir | Out-Null
        Move-Item -LiteralPath $built -Destination $engine -Force
        Say "Engine installed: $engine ($(& $engine version))"
    } finally {
        Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
        [Net.ServicePointManager]::SecurityProtocol = $savedProtocol
    }

    # The saved options go on the install command line: Windows PowerShell 5.1
    # mangles JSON piped to a native command's stdin.
    $installArgs = @('plugin', 'install', $PluginId, '--config', "engine_path=$($engine -replace '\\', '/')")
    foreach ($key in $saved.Keys) { $installArgs += @('--config', "$key=$($saved[$key])") }
    try {
        Say "Adding marketplace $source"
        Invoke-Claude plugin marketplace add $source
        Say "Installing $PluginId"
        Invoke-Claude @installArgs
    } catch {
        if ($saved.Count -gt 0) {
            Write-Warning "Your previous settings were: $(($saved.GetEnumerator() | ForEach-Object { "$($_.Key)=$($_.Value)" }) -join ', '); set them again with /config"
        }
        Die "the plugin install failed ($($_.Exception.Message)); fix the error and re-run the installer"
    }
    if ($saved.Count -gt 0) { Say 'Kept your settings' }

    Say 'Checking AWS credentials'
    & $engine doctor
    if ($LASTEXITCODE -ne 0) {
        Write-Host "The engine cannot call Bedrock directly with these credentials. With the default"
        Write-Host "backend 'auto', the advisor falls back to Claude Code's own model call."
    }
    Say 'Done. Start a new Claude Code session and run /bedrock-advisor to check the setup.'
} $Version $FromSource $Uninstall.IsPresent
