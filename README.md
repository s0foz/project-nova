# Project:Nova

> A Windows-first, Ollama-compatible local LLM runner — rebranded, reimagined, and built in Go.

[![Build Windows](https://github.com/s0foz/project-nova/actions/workflows/build-windows.yml/badge.svg?branch=main)](https://github.com/s0foz/project-nova/actions/workflows/build-windows.yml)
[![Lint](https://github.com/s0foz/project-nova/actions/workflows/lint.yml/badge.svg?branch=main)](https://github.com/s0foz/project-nova/actions/workflows/lint.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.22-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Platform](https://img.shields.io/badge/Platform-Windows-0078D6?logo=windows&logoColor=white)](#)
[![API: Ollama-compat](https://img.shields.io/badge/API-Ollama%20compatible-000000)](docs/API.md)

---

## What is Project:Nova?

**Project:Nova** is a single-binary, Windows-first desktop application that lets
you run large language models locally. It exposes an **Ollama-compatible** HTTP
API on `127.0.0.1:11434` plus an **OpenAI-compatible** API on `/v1/*`, ships
with a CLI (`nova.exe`) and a desktop tray app (`nova-tray.exe`), and uses the
same Modelfile grammar and content-addressed blob store as Ollama — so existing
Ollama tooling, model libraries, and client SDKs work without modification.

> **Relationship to Ollama.** Nova is an independent **reimplementation** of the
> Ollama user-facing surface in Go. It is **not affiliated with** or endorsed by
> the Ollama project. The goal is to provide a clean, hackable, Windows-native
> reference implementation that you can fully understand, audit, and extend.

The default runner is a deterministic **stub** that echoes input — perfect for
exercising the CLI, API, and tray end-to-end without a real model backend.
Swapping in a real `llama.cpp`-backed runner is a single interface
implementation (see [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md)).

---

## Features

- 🖥️ **Windows-first** — native `.exe`, optional GUI-subsystem tray app, MSI
  installer, per-user install (no admin required).
- 🔌 **Ollama-compatible REST API** on `127.0.0.1:11434` — `/api/generate`,
  `/api/chat`, `/api/tags`, `/api/pull`, `/api/push`, `/api/create`, `/api/show`,
  `/api/ps`, `/api/version`, `/api/delete`, `/api/copy`, `/api/embeddings`,
  `/api/embed`, `/api/blobs/{digest}`. Drop-in replacement for any Ollama
  client.
- 🤖 **OpenAI-compatible API** on `/v1/*` — `/v1/chat/completions`,
  `/v1/completions`, `/v1/embeddings`, `/v1/models`. Point any OpenAI SDK at
  `http://localhost:11434/v1` and go.
- 💻 **CLI** — `nova serve`, `nova run`, `nova pull`, `nova list`, `nova rm`,
  `nova cp`, `nova create`, `nova show`, `nova ps`, `nova version`. Mirrors
  `ollama` ergonomics. See [`docs/CLI.md`](docs/CLI.md).
- 🧩 **Modelfiles** — Dockerfile-like grammar (`FROM`, `PARAMETER`, `TEMPLATE`,
  `SYSTEM`, `MESSAGE`, `ADAPTER`, `LICENSE`). See
  [`docs/MODELFILE.md`](docs/MODELFILE.md).
- 📚 **Model library** — pull, push, copy, delete, and inspect models with
  content-addressed blob storage under `%USERPROFILE%\.nova\models`.
- ⚙️ **Configurable** — env vars for host, models dir, CORS origins, debug,
  keep-alive, max runners, VRAM limits, flash-attention, context size.
- 🪟 **Tray app** — `nova-tray.exe` runs in the system tray with no console
  window; double-clickable from the Start Menu.
- 🧪 **Stub runner** — exercise the entire surface in CI without a GPU or a
  real model. Swap in a real runner by implementing one interface.

---

## Quick start (Windows)

### Option A — One-line install

```powershell
irm https://raw.githubusercontent.com/s0foz/project-nova/main/scripts/install.ps1 | iex
```

This installs the latest release from `s0foz/project-nova` to
`%LOCALAPPDATA%\ProjectNova`, adds it to PATH, and creates a Start Menu
shortcut. If you forked the repo, pass `-Owner <your-github-username>` to
point the installer at your own releases.

### Option B — Download from Releases

1. Go to **[Releases](https://github.com/s0foz/project-nova/releases/latest)**.
2. Download one of:
   - `nova.exe` — CLI / server (console subsystem)
   - `nova-tray.exe` — desktop tray app (GUI subsystem, no console)
   - `nova-<version>-windows-amd64.zip` — both in one archive
3. Put them on your PATH (or use the install script).
4. Run `nova --version` to verify.

### Option C — Build from source

Requires **Go 1.22+** (https://go.dev/dl/) and Git.

```powershell
git clone https://github.com/s0foz/project-nova.git
cd project-nova

# Fastest path: build via PowerShell helper
.\scripts\build-windows.ps1 -Tray

# Or via Make (if you have make on Windows, e.g. via Git Bash / WSL)
make build-windows
```

Outputs land in `.\dist\`:

| File | Subsystem | Arch | Use |
| --- | --- | --- | --- |
| `nova.exe` | console | amd64 | CLI and `nova serve` |
| `nova-tray.exe` | GUI | amd64 | Desktop tray app |
| `nova-arm64.exe` | console | arm64 | CLI on Windows-on-ARM |
| `nova-tray-arm64.exe` | GUI | arm64 | Tray app on Windows-on-ARM |

See **[Build from source](#build-from-source)** below for details.

---

## Your first model

Nova's stub runner doesn't need real weights — you can exercise the API surface
right away:

```powershell
# Start the API server (foreground)
nova serve

# In another terminal:
nova pull llama3            # registers the model name (stub-friendly)
nova run llama3 "Hello!"    # stream a completion
nova list                   # list installed models
```

Sample output from `nova run llama3 "Hello!"`:

```
Pulling manifest...
Pulling sha256:ac477...   100% |████████████████████| (size here once a real runner is used)
Success
[Nova stub] Echo: Hello!
```

---

## Build from source

### Prerequisites

| Tool | Version | Notes |
| --- | --- | --- |
| Go | 1.22+ | `go version` |
| Git | any | for `git describe` versioning |
| (optional) WiX Toolset v3 | 3.14+ | only for building the MSI |
| (optional) `make` | any | only if you prefer Make targets |

### Build (PowerShell)

```powershell
# Console + tray binaries, version from git
.\scripts\build-windows.ps1 -Tray

# Specify a version
.\scripts\build-windows.ps1 -Version 0.1.0 -Tray

# Build for Windows-on-ARM
.\scripts\build-windows.ps1 -Arch arm64 -Tray
```

### Build (Make)

```bash
make build-windows        # cross-compile nova.exe (amd64, GUI subsystem)
make build-tray           # alias for build-windows
make package-zip          # produce dist/nova-windows-amd64-<ver>.zip
make package-msi          # invoke scripts/build-msi.ps1 (Windows only)
make test                 # go test ./... -race -count=1
make vet                  # go vet ./...
make fmt                  # gofmt + goimports
make clean                # rm -rf dist build wix standalone
```

### Package an MSI

```powershell
# 1. Build the binaries first
.\scripts\build-windows.ps1 -Tray

# 2. Build the MSI (requires WiX Toolset v3 on PATH)
.\scripts\build-msi.ps1 -Version 0.1.0

# Output: dist\nova-0.1.0-amd64.msi
```

Install the MSI silently:

```powershell
msiexec /i .\dist\nova-0.1.0-amd64.msi /quiet /norestart
```

---

## Usage — CLI

> Full reference: [`docs/CLI.md`](docs/CLI.md)

```powershell
nova --version                 # print version
nova serve                     # start the API server on 127.0.0.1:11434
nova --tray                    # start the desktop tray app (or run nova-tray.exe)
nova pull llama3               # pull a model from the library
nova run llama3 "Hello!"       # run a one-off completion (streams)
nova run -m llama3             # interactive chat REPL
nova list                      # list installed models
nova rm llama3:8b              # delete a model
nova cp llama3:8b myalias      # copy a model to a new name
nova create mymodel ./Modelfile  # build a model from a Modelfile
nova show llama3               # show model info (Modelfile, parameters, license)
nova ps                        # list currently loaded models
```

### Sample output — `nova list`

```
NAME                        ID              SIZE      MODIFIED
llama3:latest               ac477b39ea8f    4.7 GB    2 minutes ago
qwen2:7b                     1f2c8d4e9a73    4.3 GB    1 hour ago
```

### Sample output — `nova show llama3`

```
Model
  architecture        llama
  parameters          8.0B
  quantization        Q4_0
  context length      8192
  embedding length    4096

Parameters
  num_keep            24
  temperature         0.8
  top_k               40
  top_p               0.9
  ...

System
  You are a helpful assistant.

License
  MIT
```

---

## Usage — REST API

> Full reference: [`docs/API.md`](docs/API.md). The server listens on
> `http://127.0.0.1:11434` by default.

### Generate (Ollama API)

```bash
curl http://127.0.0.1:11434/api/generate -d '{
  "model": "llama3",
  "prompt": "Why is the sky blue?",
  "stream": false
}'
```

Response (non-streaming):

```json
{
  "model": "llama3",
  "created_at": "2024-07-02T12:34:56.789Z",
  "response": "[Nova stub] Echo: Why is the sky blue?",
  "done": true,
  "done_reason": "stop",
  "context": [1, 2, 3],
  "total_duration": 12345678,
  "load_duration": 100000,
  "prompt_eval_count": 5,
  "prompt_eval_duration": 500000,
  "eval_count": 8,
  "eval_duration": 11000000
}
```

For streaming, set `"stream": true` and read newline-delimited JSON objects.

### Chat (Ollama API)

```bash
curl http://127.0.0.1:11434/api/chat -d '{
  "model": "llama3",
  "messages": [
    { "role": "system", "content": "You are a helpful assistant." },
    { "role": "user",   "content": "Write a haiku about Go." }
  ],
  "stream": false
}'
```

### List installed models

```bash
curl http://127.0.0.1:11434/api/tags
```

```json
{
  "models": [
    {
      "name": "llama3:latest",
      "model": "llama3:latest",
      "modified_at": "2024-07-02T12:00:00Z",
      "size": 4799994880,
      "digest": "sha256:ac477b39ea8f...",
      "details": { "format": "gguf", "family": "llama", "parameter_size": "8.0B" }
    }
  ]
}
```

### OpenAI-compatible chat completions

```bash
curl http://127.0.0.1:11434/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "llama3",
  "messages": [ { "role": "user", "content": "Hello!" } ]
}'
```

Point any OpenAI SDK at Nova:

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:11434/v1", api_key="nova")
print(client.chat.completions.create(
    model="llama3",
    messages=[{"role":"user","content":"Hello!"}]
).choices[0].message.content)
```

---

## Configuration

Nova is configured primarily via environment variables (mirrors Ollama's
convention, rebranded):

| Variable | Default | Description |
| --- | --- | --- |
| `NOVA_HOST` | `127.0.0.1:11434` | Host:port the API server binds to |
| `NOVA_MODELS` | `%USERPROFILE%\.nova\models` | Root directory for manifests, blobs, logs |
| `NOVA_ORIGINS` | `localhost,127.0.0.1,0.0.0.0` | Comma-separated allowed CORS origins |
| `NOVA_DEBUG` | (unset) | `1` or `true` enables verbose logging |
| `NOVA_MAX_RUNNERS` | (unset) | Max number of concurrently loaded models |
| `NOVA_MAX_VRAM` | (unset) | Max VRAM (bytes) the runner may use |
| `NOVA_FLASH_ATTENTION` | (unset) | `1` enables flash attention |
| `NOVA_KEEP_ALIVE` | `5m` | Default model keep-alive duration |
| `NOVA_NUM_CTX` | `4096` | Default context window size |

Set them in PowerShell:

```powershell
$env:NOVA_HOST = "0.0.0.0:11434"   # listen on all interfaces
$env:NOVA_DEBUG = "1"
nova serve
```

Or persistently for the user:

```powershell
[Environment]::SetEnvironmentVariable('NOVA_HOST', '0.0.0.0:11434', 'User')
```

---

## Directory layout

```
project-nova/
├── cmd/
│   └── nova/                 # main package — entry point
├── internal/
│   ├── api/handlers/         # HTTP handlers (Ollama + OpenAI endpoints)
│   ├── cmd/                  # CLI subcommands (cobra-based)
│   ├── env/                  # env vars, paths, dir bootstrap
│   ├── llm/                  # Runner interface + StubRunner
│   ├── model/                # Manifest, Layer, Modelfile parser
│   ├── openai/               # OpenAI-compat translation layer
│   ├── registry/             # on-disk model store (manifests + blobs)
│   ├── server/               # orchestrator: loaded models, keep-alive, sweep
│   ├── tray/                 # Windows system-tray lifecycle
│   └── version/              # build-time version metadata
├── scripts/
│   ├── build-windows.ps1     # local Windows build (mirrors CI)
│   ├── build-msi.ps1         # WiX MSI packaging
│   ├── install.ps1           # one-line user install (irm | iex)
│   └── uninstall.ps1         # idempotent uninstall
├── .github/workflows/
│   ├── build-windows.yml     # Windows build + release pipeline
│   └── lint.yml              # go vet + gofmt + golangci-lint
├── docs/
│   ├── API.md                # full REST API reference
│   ├── MODELFILE.md          # Modelfile directive reference
│   ├── CLI.md                # CLI command reference
│   ├── DEVELOPMENT.md        # contributor guide
│   └── ARCHITECTURE.md       # component diagram + layering
├── assets/                   # icons, splash, etc.
├── go.mod
├── Makefile
├── LICENSE
└── README.md
```

The user's local model store (created on first run) looks like:

```
%USERPROFILE%\.nova\
└── models\
    ├── blobs\sha256\<digest>      # content-addressed layer data
    ├── manifests\<registry>\<namespace>\<model>\<tag>   # JSON manifests
    ├── .tmp\                       # sockets, pid files, scratch
    └── logs\                       # rotating logs
```

---

## Windows desktop tray

`nova-tray.exe` is the **same Go binary** as `nova.exe`, built with the
`-H=windowsgui` linker flag so Windows gives it the **GUI subsystem** — meaning
double-clicking it won't pop up a console window. It runs in the system tray
and provides a menu for:

- **Start / Stop API server** — toggles `nova serve` in-process.
- **Open Web UI** — opens `http://localhost:11434` in your default browser.
- **Models** — quick access to pull / list / delete.
- **About** — version + commit + build date.
- **Quit** — clean shutdown of all loaded models.

Launch the tray app:

- Start Menu → **Project:Nova** → **Nova Tray**, or
- Double-click `%LOCALAPPDATA%\ProjectNova\nova-tray.exe`, or
- `Start-Process nova-tray.exe` from PowerShell.

> Tip: build the tray app with `.\scripts\build-windows.ps1 -Tray` locally, or
> download `nova-tray.exe` from the latest GitHub Release.

---

## Documentation

| Doc | What's inside |
| --- | --- |
| [`docs/API.md`](docs/API.md) | Full REST API reference — every endpoint, every schema, every status code |
| [`docs/MODELFILE.md`](docs/MODELFILE.md) | Modelfile directive reference, all parameter keys, template syntax, full examples |
| [`docs/CLI.md`](docs/CLI.md) | Every `nova` subcommand with synopsis, flags, and examples |
| [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) | Contributor guide — layout, adding endpoints, swapping the runner, releasing |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | High-level architecture diagram and layering explanation |

---

## Contributing

Contributions are welcome! Please read [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md)
first. Quick checklist:

1. Fork & clone the repo.
2. Create a feature branch: `git checkout -b feat/my-thing`.
3. Make your change. Add tests where reasonable.
4. Run `make vet fmt test` locally.
5. Open a PR — the CI workflows (`build-windows.yml`, `lint.yml`) will run.
6. Sign your commits if you can; small focused PRs review faster.

Please **do not** commit generated binaries (`dist/`, `*.exe`, `*.msi`) — they
are `.gitignore`d. Tag releases as `vMAJOR.MINOR.PATCH` and the CI will
publish a GitHub Release automatically.

---

## License

MIT — see [`LICENSE`](LICENSE).

Project:Nova is an independent reimplementation and is **not affiliated with,
endorsed by, or sponsored by** the Ollama project or its maintainers. "Ollama"
is a trademark of its respective owners; we're just compatible.

---

<p align="center">
  Made with <span style="color:#e74c3c">♥</span> for Windows power users who want a hackable local LLM runner.
</p>
