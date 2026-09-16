<div align="center">

<img src="Android-APP/design/assets/zcode-app-icon.png" width="88" alt="ZCode Proxy Logo" />

# ZCode Proxy

**Connect your GLM coding subscription plans into any AI programming tool.**

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows%20%7C%20Android-lightgrey)](https://github.com/Yondaime-kun/zcode-proxy-go/releases)

A lightweight, high-performance local proxy written in **Go**. It transforms Zhipu Z.AI / BigModel coding plans (Personal / Start-Plan) into standard, industry-compliant **OpenAI**, **Anthropic**, and **Codex Responses** API endpoints.

Use your subscription quota directly in tools like **Claude Code**, **Codex CLI**, **Cursor**, **Roo Code**, **Cline**, **Aider**, and **SillyTavern**.

[Quick Start](#quick-start) · [Terminal Interface](#interactive-terminal-interface) · [CLI Reference](#cli-reference) · [Tool Integrations](#tool-integrations) · [Building from Source](#building-from-source) · [Acknowledgements](#acknowledgements)

</div>

---

## Features

- **High Performance & Low Footprint**: Standalone Go binary with zero external runtime dependencies. Uses approximately 15–20 MB of RAM with 0.0% idle CPU usage.
- **Triple API Compatibility**:
  - **OpenAI Chat Completions** (`/v1/chat/completions`): Complete SSE streaming and non-streaming compatibility.
  - **Anthropic Messages** (`/v1/messages`): Native Claude-compatible translation with full tool/thinking support.
  - **Codex Responses** (`/v1/responses`): Tailored for Codex CLI.
- **Autonomous Captcha Solver & Auto-Retry**: Detects and handles upstream Aliyun slider challenges (error 3007) automatically during long-running agent workflows.
- **Multi-Account Pool & Failover**: Manage multiple accounts (`acc1`, `acc2`, etc.). Automatically rotates or fails over when account quota limits (code 1605 or 429) are encountered, supporting `round-robin`, `fill-first`, and `random` routing.
- **Real-Time Session Metrics**: Live tracking of total requests, status codes, average response latency, input & output token counts (with boundary chunk detection & fallback estimation), and client IP breakdown persisted in `~/.zcode-proxy/stats.json`.
- **Security & Access Control**: Optional bearer token authentication (`auth.proxyApiKey`) and CIDR-based IP whitelist/blacklist filtering.
- **Automated Campaign Claiming**: Background scheduler periodically polls and claims promotional trial/weekend packages with per-account tagging and automatic quota balance refresh.
- **Terminal User Interface (TUI)**: 4-card interactive ANSI dashboard with full mouse click and scroll support, real-time log streaming, and single-key shortcuts.
- **Android Companion App**: Run or manage the proxy directly on Android devices.

---

## Quick Start

### 1. Download Pre-built Binary

Download the latest executable for your operating system and architecture from [GitHub Releases](https://github.com/Yondaime-kun/zcode-proxy-go/releases):

```bash
# Example: Linux (x86_64)
curl -L -o zcode-proxy https://github.com/Yondaime-kun/zcode-proxy-go/releases/latest/download/zcode-proxy-linux-amd64
chmod +x zcode-proxy
./zcode-proxy
```

*(On Windows, run `zcode-proxy.exe` in PowerShell or Windows Terminal).*

### 2. Launch the Dashboard

Running `./zcode-proxy` without arguments launches the interactive terminal dashboard:

```text
╭─ Settings & Login ────────────────────────────────────────────────── ● ready ─╮
│   Provider   ● zai    ○ bigmodel   (stop to switch)                          │
│   Plan       ○ coding-plan    ● start-plan   (stop to switch)                │
│   Auth      ● logged in · 272526ba… (5 accs, active: acc1)                   │
│   Quota     GLM-5.3: 3.00M (100%) · GLM-5.3-Flash: 4.98M (100%)              │
│    Logged In    Switch Acc    Claim    Logout                                │
╰──────────────────────────────────────────────────────────────────────────────╯

╭─ Proxy Server ───────────────────────────────────────────────────────────────╮
│   Status    ● running  http://0.0.0.0:8080                                   │
│   Config    zai · start-plan · 2 models · /v1/responses · claim:auto         │
│    Start      Stop                                                           │
╰──────────────────────────────────────────────────────────────────────────────╯

╭─ Session Metrics ───────────────────────────────────────────────── ● 0 active ─╮
│   Requests  128 total · 128 ok · 0 err · 1.4s avg                            │
│   Tokens    1.25M in · 42.8k out · 1.29M total                               │
│   Models    glm-5.3: 850k (60) · glm-5.3-flash: 440k (68)                    │
│   Clients   127.0.0.1 (128)                                                  │
╰──────────────────────────────────────────────────────────────────────────────╯

╭─ Logs (14) ────────────────────────────────────────────────────── following ─╮
│   [auth] credentials loaded from disk (5 accounts, active: acc1)             │
│   zcode-proxy (Go) listening on http://0.0.0.0:8080                          │
│   [claim] auto ON for 5 accounts: acc1, acc2, acc3, acc4, acc5 (poll 300s)  │
│   [http] 127.0.0.1 -> POST /v1/chat/completions (model: glm-5.3)             │
│   [http] 127.0.0.1 <- POST /v1/chat/completions (200, in=1.2k, out=185)      │
╰──────────────────────────────────────────────────────────────────────────────╯
  ↑↓ scroll · [s] start/stop · [l] login · [o] logout · [a] acc · [u] quota · [m] claim · [r] reset · [g] follow · [q] quit
```

- Press <kbd>l</kbd> to log in via browser OAuth (or <kbd>a</kbd> to switch accounts).
- Press <kbd>s</kbd> to start or stop the proxy server.
- Press <kbd>m</kbd> or click **`[ Claim ]`** to trigger manual promotional package claims.
- Press <kbd>u</kbd> to refresh live quota balances.

---

## Interactive Terminal Interface

The dashboard supports keyboard shortcuts and mouse clicks/scrolling:

| Key | Action |
|:---:|:---|
| <kbd>s</kbd> | Start / stop the proxy server |
| <kbd>l</kbd> | OAuth login for the selected provider (opens browser authorization) |
| <kbd>o</kbd> | Log out from the active account |
| <kbd>a</kbd> | Switch active account in the multi-account pool |
| <kbd>u</kbd> | Refresh live quota balances from upstream |
| <kbd>m</kbd> | Manually trigger package claim for the active account |
| <kbd>p</kbd> | Switch provider (`zai` ↔ `bigmodel`) when proxy is stopped |
| <kbd>t</kbd> | Switch plan tier (`coding-plan` ↔ `start-plan`) when proxy is stopped |
| <kbd>r</kbd> | Reset session metrics and clear stats history |
| <kbd>c</kbd> | Clear the log pane buffer |
| <kbd>g</kbd> | Jump to bottom of log pane (resume follow mode) |
| <kbd>↑</kbd> / <kbd>↓</kbd> | Scroll log pane line-by-line (or use mouse scroll wheel) |
| <kbd>PgUp</kbd> / <kbd>PgDn</kbd> | Scroll log pane by page |
| <kbd>q</kbd> | Exit the proxy dashboard |

---

## CLI Reference

`zcode-proxy` can be operated headlessly for systemd services, Docker containers, or remote servers:

```bash
# Run headless server (no interactive TUI)
./zcode-proxy serve
./zcode-proxy serve --config /path/to/config.yaml

# Manage accounts
./zcode-proxy auth login zai --name acc1          # OAuth login
./zcode-proxy auth login bigmodel --paste         # Headless login via URL paste
./zcode-proxy auth list                           # List all configured accounts
./zcode-proxy auth switch acc2                    # Change active account
./zcode-proxy auth logout [alias]                 # Remove account credentials

# Check live quota balances
./zcode-proxy quota
./zcode-proxy quota --account acc1

# Claim promotional packages
./zcode-proxy claim list                          # Preview claimable packages
./zcode-proxy claim now                           # Claim packages immediately
```

---

## Tool Integrations

By default, the proxy listens on **`http://127.0.0.1:8080`**. Configure your development tool to point its API base URL to this address.

> **Authentication**: If `auth.proxyApiKey` is set in `config.yaml` (or via `ZCODE_PROXY_API_KEY`), provide that key in your tool. If no key is set, any dummy value (such as `sk-1234`) will be accepted.

### Claude Code

```bash
# Linux / macOS
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN=sk-1234
export ANTHROPIC_MODEL=glm-4.7
claude
```

```powershell
# Windows PowerShell
$env:ANTHROPIC_BASE_URL = "http://127.0.0.1:8080"
$env:ANTHROPIC_AUTH_TOKEN = "sk-1234"
$env:ANTHROPIC_MODEL = "glm-4.7"
claude
```

### Codex CLI

In `~/.codex/config.toml`:

```toml
model_provider = "zcode"
model = "glm-5.3"

[model_providers.zcode]
name = "ZCode Proxy"
base_url = "http://127.0.0.1:8080/v1"
wire_api = "responses"
env_key = "ZCODE_API_KEY"   # Any string unless proxyApiKey is configured
```

### Cursor / Continue / Roo Code / Cline

In your tool's custom OpenAI/Anthropic provider settings:

| Setting | Value |
|:---|:---|
| **Base URL** | `http://127.0.0.1:8080/v1` |
| **API Key** | Configured `proxyApiKey` (or dummy string like `sk-1234`) |
| **Model** | `glm-5.3`, `glm-5.3-flash`, `glm-4.7`, `glm-4.6v`, etc. |

For direct Anthropic clients, set Base URL to `http://127.0.0.1:8080` (the proxy handles `/v1/messages`).

### Testing with cURL

```bash
curl http://127.0.0.1:8080/v1/chat/completions   -H "Content-Type: application/json"   -d '{
    "model": "glm-5.3-flash",
    "messages": [{"role": "user", "content": "Hello! Reply in one sentence."}],
    "stream": true
  }'
```

---

## Supported Models

The proxy exposes upstream GLM models via `/v1/models`:

| Model Name | Context Window | Max Output | Capabilities |
|:---|:---:|:---:|:---|
| `glm-5.3` | 1,000,000 tokens | 128,000 tokens | Flagship coding & reasoning model |
| `glm-5.3-flash` | 1,000,000 tokens | 128,000 tokens | High-speed, lightweight reasoning |
| `glm-5.2` | 1,000,000 tokens | 128,000 tokens | Long-context coding & analysis |
| `glm-5.1` | 200,000 tokens | 64,000 tokens | Balanced performance |
| `glm-5` / `glm-5-turbo` | 200,000 tokens | 64,000 tokens | Standard generation |
| `glm-5v-turbo` | 200,000 tokens | 131,000 tokens | Multimodal / Vision |
| `glm-4.7` | 200,000 tokens | 131,000 tokens | Advanced coding model |
| `glm-4.6` | 200,000 tokens | 131,000 tokens | Stable coding |
| `glm-4.6v` | 131,000 tokens | 32,000 tokens | Multimodal / Vision |
| `glm-4.5-air` | 131,000 tokens | 96,000 tokens | Lightweight tier |

---

## Configuration & Environment

Settings are read from `config.yaml` (automatically created on first run) and can be overridden with environment variables:

| Environment Variable | Default | Description |
|:---|:---:|:---|
| `ZCODE_PROXY_PORT` | `8080` | HTTP proxy listening port |
| `ZCODE_PROXY_HOST` | `0.0.0.0` | HTTP proxy binding host |
| `ZCODE_PROXY_API_KEY` | `""` | Optional access key required for client requests |
| `ZCODE_PROVIDER` | `zai` | Upstream provider (`zai` or `bigmodel`) |
| `ZCODE_PROXY_CONFIG` | `config.yaml` | Path to custom YAML configuration file |
| `ZCODE_PROXY_CREDENTIAL_SECRET` | Machine-derived | Master encryption secret for `credentials.json` |
| `ZCODE_STATS_PATH` | `~/.zcode-proxy/stats.json` | Custom storage path for session metrics |

### Example `config.yaml`

```yaml
server:
  host: "0.0.0.0"
  port: 8080

provider: "zai"        # "zai" or "bigmodel"
plan: "start-plan"     # "coding-plan" or "start-plan"

auth:
  proxyApiKey: ""      # Leave blank for open local access

security:
  ipFilter:
    enabled: false
    whitelist:
      - "127.0.0.1"
      - "192.168.1.0/24"
    blacklist: []

routing:
  mode: "round-robin"  # "round-robin", "fill-first", or "random"

claim:
  enabled: true
  auto: true
  pollIntervalMs: 300000  # Poll every 5 minutes
```

---

## Building from Source

### Building the Go Proxy (`zcode-proxy`)

**Prerequisites**: [Go 1.22+](https://go.dev/dl/) installed.

```bash
# Clone the repository
git clone https://github.com/Yondaime-kun/zcode-proxy-go.git
cd zcode-proxy-go/go

# Run unit tests
go test -v ./...

# Build the release executable for your current OS
CGO_ENABLED=0 go build -ldflags="-s -w" -o zcode-proxy ./cmd/zcode-proxy
```

#### Cross-Compilation Commands

You can compile standalone binaries for other operating systems and architectures from any machine:

```bash
# Linux (x86_64 / amd64)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o zcode-proxy-linux-amd64 ./cmd/zcode-proxy

# Linux (ARM64 / aarch64)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o zcode-proxy-linux-arm64 ./cmd/zcode-proxy

# Windows (x86_64)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o zcode-proxy.exe ./cmd/zcode-proxy

# macOS (Apple Silicon / arm64)
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o zcode-proxy-darwin-arm64 ./cmd/zcode-proxy

# macOS (Intel / amd64)
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o zcode-proxy-darwin-amd64 ./cmd/zcode-proxy
```

---

### Building the Captcha Solver (`zcode-captcha-solver`)

The proxy includes a native fallback slider trajectory solver. For environments requiring DOM-level simulation against upstream Aliyun slider challenges (code 3007), a standalone solver executable can be built using [Bun](https://bun.sh).

**Prerequisites**: [Bun 1.0+](https://bun.sh) installed.

```bash
# Navigate to repository root
cd zcode-proxy-go

# Install Node/Bun dependencies
bun install

# Build standalone binary for your current OS
bun build --compile scripts/solve-captcha.ts --outfile zcode-captcha-solver

# Cross-compiling for target platforms
bun build --compile --target bun-linux-x64 scripts/solve-captcha.ts --outfile zcode-captcha-solver-linux-x64
bun build --compile --target bun-windows-x64 scripts/solve-captcha.ts --outfile zcode-captcha-solver.exe
bun build --compile --target bun-darwin-arm64 scripts/solve-captcha.ts --outfile zcode-captcha-solver-darwin-arm64
```

> **Binary Placement**: Place the compiled `zcode-captcha-solver` binary in the same directory as `zcode-proxy`, in your current working directory, or in your system `PATH`. The proxy will automatically detect and execute it on demand when upstream captcha verification is required.

---

## Docker Deployment

Run the proxy in a Docker container:

```bash
docker run -d   --name zcode-proxy   -p 8080:8080   -v $(pwd)/config.yaml:/app/config.yaml:ro   -v $(pwd)/credentials.json:/root/.zcode-proxy/credentials.json:ro   -e ZCODE_PROXY_CREDENTIAL_SECRET="your-custom-encryption-secret"   ghcr.io/yondaime-kun/zcode-proxy-go:latest
```

Using Docker Compose:

```yaml
services:
  zcode-proxy:
    image: ghcr.io/yondaime-kun/zcode-proxy-go:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/app/config.yaml:ro
      - ~/.zcode-proxy/credentials.json:/root/.zcode-proxy/credentials.json:ro
    environment:
      - ZCODE_PROXY_CREDENTIAL_SECRET=your-custom-encryption-secret
    restart: unless-stopped
```

---

## Android App

The repository includes a native Android companion app under `Android-APP/`. Pre-built APKs are available in [GitHub Releases](https://github.com/Yondaime-kun/zcode-proxy-go/releases):

- **Self-Contained Engine**: Runs the local proxy on Android devices over Wi-Fi.
- **One-Tap Controls**: Start/stop the server, inspect live logs, switch providers and plans.
- **Light & Dark Theme**: Material 3 UI design.

| Home | Logs | Settings | Dark Theme |
|:---:|:---:|:---:|:---:|
| <img src="docs/images/android/home-light.png" width="200" alt="Home" /> | <img src="docs/images/android/logs.png" width="200" alt="Logs" /> | <img src="docs/images/android/settings.png" width="200" alt="Settings" /> | <img src="docs/images/android/home-dark.png" width="200" alt="Dark" /> |

---

## Acknowledgements

This project is an extended fork of [TriDefender/zcode-api](https://github.com/TriDefender/zcode-api) (originally implemented in TypeScript and Bun).

Our fork introduces the native **Go implementation** (`go/`), providing:
- Ultra-low memory usage (~15MB RAM) and zero idle CPU usage.
- Single-binary portability across Linux, Windows, macOS, and BSD without runtime dependencies.
- A pure ANSI terminal UI (TUI) with real-time log streaming and mouse support.
- Multi-account auto-claim scheduling and live quota balances.
- Persistent session metrics and CIDR IP access controls.

We express our gratitude to the original upstream project and its contributors for their foundational reverse engineering and protocol analysis.

---

## FAQ

<details>
<summary><b>Why did the program exit immediately with "Not logged in"?</b></summary>
Run <code>./zcode-proxy auth login zai</code> (or bigmodel) first, or launch the interactive TUI directly with <code>./zcode-proxy</code> and press <kbd>l</kbd> to log in.
</details>

<details>
<summary><b>How do I log in on a remote headless server without a desktop browser?</b></summary>
Use paste mode: <code>./zcode-proxy auth login bigmodel --paste</code>. Copy the displayed authorization URL into your local browser, log in, and paste the redirected callback URL back into the terminal.
</details>

<details>
<summary><b>My coding tool receives 401 Unauthorized errors?</b></summary>
If <code>auth.proxyApiKey</code> is specified in <code>config.yaml</code> (or via <code>ZCODE_PROXY_API_KEY</code>), your tool must provide the exact same key in its Authorization header. If no key is configured in the proxy, any arbitrary key will be accepted.
</details>

<details>
<summary><b>How does automatic captcha solving work?</b></summary>
When upstream emits an Aliyun sliding captcha verification challenge (HTTP 400 with code 3007), the proxy automatically calculates track trajectories, requests fresh verification tokens from the security endpoint, and retries the upstream request seamlessly up to the configured retry limit.
</details>

<details>
<summary><b>Is my data or API traffic sent to third parties?</b></summary>
No. All network communication travels strictly between your local client, the local proxy, and official Zhipu / BigModel servers. No telemetry, logs, or credentials are sent to any third-party infrastructure.
</details>

---

## License

Distributed under the **MIT License**. See [`LICENSE`](LICENSE) for details.
