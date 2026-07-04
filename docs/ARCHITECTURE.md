# Project:Nova — Architecture

This document describes the high-level architecture of Project:Nova: the
components, how they fit together, the runner abstraction, the keep-alive
model, and the Windows tray lifecycle. After reading this you should be able
to navigate any file in the codebase and know which layer it belongs to.

---

## Table of contents

- [At a glance](#at-a-glance)
- [ASCII component diagram](#ascii-component-diagram)
- [Layering](#layering)
- [The Runner abstraction](#the-runner-abstraction)
- [The Server orchestrator](#the-server-orchestrator)
- [The keep-alive model](#the-keep-alive-model)
- [Model storage: manifests & blobs](#model-storage-manifests--blobs)
- [HTTP API layer](#http-api-layer)
- [CLI layer](#cli-layer)
- [Windows tray lifecycle](#windows-tray-lifecycle)
- [Request lifecycle: a full walkthrough](#request-lifecycle-a-full-walkthrough)
- [Cross-cutting concerns](#cross-cutting-concerns)
- [Design decisions & trade-offs](#design-decisions--trade-offs)

---

## At a glance

Project:Nova is a single Go binary that can run as:

1. A **CLI** (`nova run`, `nova pull`, ...) that mostly talks to a running
   server over HTTP.
2. An **HTTP API server** (`nova serve`) — the daemon.
3. A **Windows desktop tray app** (`nova-tray.exe` or `nova --tray`) that
   embeds the server and provides a system-tray UI.

All three are the same code, dispatched from `cmd/nova/main.go`. The build
differences are only the linker subsystem flag (`-H=windowsgui` for the tray
build) and the entry arguments.

The architecture is layered:

```
        CLI / external clients / OpenAI SDKs
                 │
        ┌────────▼─────────┐
        │   HTTP API layer │  ← /api/* (Ollama) + /v1/* (OpenAI)
        │  internal/api    │
        └────────┬─────────┘
                 │
        ┌────────▼─────────┐
        │     Server       │  ← orchestrator: load / keep-alive / unload
        │  internal/server │
        └────────┬─────────┘
                 │
        ┌────────▼─────────┐
        │     Runner       │  ← llm.Runner interface (stub by default)
        │   internal/llm   │
        └────────┬─────────┘
                 │
        ┌────────▼─────────┐
        │   Model store    │  ← manifests + content-addressed blobs
        │ internal/registry│
        │  internal/model  │
        └──────────────────┘
```

---

## ASCII component diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          cmd/nova (single binary)                          │
│                                                                            │
│  ┌────────────┐   ┌──────────────┐   ┌──────────────────────────────────┐  │
│  │  CLI flag  │   │   Tray UI    │   │         HTTP API server          │  │
│  │  dispatch  │   │ internal/tray│   │        (net/http + mux)          │  │
│  │  (cobra)   │   │  systray lib │   │  ┌────────────┬────────────────┐ │  │
│  │internal/cmd│   │              │   │  │ /api/*     │ /v1/*          │ │  │
│  └─────┬──────┘   └──────┬───────┘   │  │ Ollama-compat│ OpenAI-compat│ │  │
│        │                 │           │  └─────┬──────┴───────┬───────┘ │  │
│        │ HTTP client     │ embeds    │        │              │         │  │
│        └─────────────────┘  server   │  ┌─────▼──────────────▼───────┐ │  │
│                                     │  │  Handlers (internal/api)    │ │  │
│                                     │  │  - generate / chat / embed  │ │  │
│                                     │  │  - tags / show / ps / ver   │ │  │
│                                     │  │  - pull / push / create     │ │  │
│                                     │  │  - delete / copy / blobs    │ │  │
│                                     │  │  - openai translation       │ │  │
│                                     │  └─────────────┬──────────────┘ │  │
│                                     └────────────────┼─────────────────┘  │
│                                                      │                    │
│                              ┌───────────────────────▼─────────────────┐  │
│                              │            Server orchestrator           │  │
│                              │             internal/server              │  │
│                              │   - map[name]*LoadedModel                │  │
│                              │   - keep-alive timers / sweep loop       │  │
│                              │   - RunnerFactory                        │  │
│                              └────────────────┬─────────────────────────┘  │
│                                               │                            │
│                              ┌────────────────▼─────────────────────────┐  │
│                              │           llm.Runner (interface)          │  │
│                              │             internal/llm                  │  │
│                              │   - Load / Generate / Chat / Embed        │  │
│                              │   - Tokenize / Detokenize / Count         │  │
│                              │   - Close / Stats                         │  │
│                              │   ┌────────────────┐  ┌─────────────────┐ │  │
│                              │   │  StubRunner    │  │  (your real     │ │  │
│                              │   │  (default)     │  │   llama.cpp     │ │  │
│                              │   │  echoes input  │  │   runner here)  │ │  │
│                              │   └────────────────┘  └─────────────────┘ │  │
│                              └────────────────┬─────────────────────────┘  │
│                                               │                            │
│         ┌─────────────────────────────────────▼───────────────────────┐    │
│         │              Model store  (on disk, per-user)              │    │
│         │                                                             │    │
│         │   internal/registry   internal/model       internal/env     │    │
│         │   - Parse / List     - Manifest / Layer   - ModelsDir()     │    │
│         │   - CreateManifest   - Modelfile parser   - BlobsDir()      │    │
│         │   - CreateBlob       - Layer media types  - ManifestsDir()  │    │
│         │   - CopyManifest     - Digest (sha256)    - Host()          │    │
│         │                                                             │    │
│         │   %USERPROFILE%\.nova\models\                               │    │
│         │     ├── manifests/<registry>/<ns>/<model>/<tag>  (JSON)     │    │
│         │     ├── blobs/sha256/<digest>                  (raw bytes)  │    │
│         │     ├── .tmp/  (sockets, pid files, scratch)               │    │
│         │     └── logs/  (rotating logs)                            │    │
│         └─────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Layering

| Layer | Package(s) | Responsibility | Knows about |
| --- | --- | --- | --- |
| Entry | `cmd/nova` | Parse args, dispatch to CLI/server/tray | everything below |
| CLI | `internal/cmd` | Subcommands, flags, IO formatting | HTTP client, env |
| Tray | `internal/tray` | Windows system-tray UI, embeds server | server, env |
| HTTP API | `internal/api/handlers` | Ollama + OpenAI endpoints, request/response translation | server, registry, model, llm |
| OpenAI translation | `internal/openai` | Translate OpenAI requests <-> Nova internal types | model, llm |
| Orchestrator | `internal/server` | Load models, keep-alive, sweep, concurrency | registry, model, llm |
| Runner abstraction | `internal/llm` | Runner interface + StubRunner | model |
| Model | `internal/model` | Manifest, Layer, Modelfile parser | (nothing — leaf) |
| Registry | `internal/registry` | On-disk model store (manifests + blobs) | model, env |
| Environment | `internal/env` | Paths, env vars, dir bootstrap | (nothing — leaf) |
| Version | `internal/version` | Build-time metadata | (nothing — leaf) |

The dependency direction is top-down. Lower layers never import higher ones.
`internal/model` and `internal/env` are leaf packages and may be imported by
anyone.

---

## The Runner abstraction

The `llm.Runner` interface is the seam between Nova's plumbing and the actual
inference backend. Everything above the runner (HTTP API, server, CLI, tray)
is backend-agnostic.

```go
type Runner interface {
    Load(ctx, *Manifest, Options) error
    Loaded() bool
    Generate(ctx, prompt, images, Options, fn func(Token) error) error
    Chat(ctx, []Message, Options, fn func(Token) error) error
    Embed(ctx, []string, Options) ([][]float32, error)
    Tokenize(ctx, text) ([]int, error)
    Detokenize(ctx, []int) (string, error)
    Count(ctx, text) (int, error)
    Close() error
    Stats() Stats
}
```

Key points:

- **One Runner instance per loaded model.** The Server owns a `map[name]*LoadedModel`,
  each holding its own Runner. Multiple concurrent requests for the same model
  share one Runner (the Runner must be goroutine-safe).
- **Streaming is via a callback.** The runner emits `Token` values through
  `fn`; returning an error from `fn` cancels generation. The HTTP handler is
  responsible for translating these into NDJSON (Ollama API) or SSE (OpenAI
  API) chunks.
- **`Load` is idempotent.** Calling `Load` on an already-loaded runner is a
  no-op (or a reload if the manifest changed).
- **`Close` unloads and frees resources.** Called by the Server when the
  keep-alive timer expires or on `Server.Stop()`.

The default implementation is `llm.StubRunner`, which echoes input — it lets
us exercise the entire stack in CI without a real backend. See
[`docs/DEVELOPMENT.md`](DEVELOPMENT.md#swapping-the-stub-runner-for-a-real-backend)
for how to write a real one.

---

## The Server orchestrator

`internal/server` is the brain of the daemon. It owns:

- A `map[string]*LoadedModel` of currently-resident models, keyed by canonical
  name.
- A `RunnerFactory` that produces a fresh `llm.Runner` for each new load.
- A background sweep goroutine that unloads expired models.
- A `sync.Mutex` protecting all of the above.

The Server exposes high-level operations that the HTTP layer calls:

```go
Load(ctx, name, opts) (*LoadedModel, error)    // load if not resident; refresh keep-alive
Get(name) *LoadedModel                         // peek without loading; refresh keep-alive
Unload(name) error                             // force-unload
List() []*LoadedModel                          // for /api/ps
Generate(ctx, name, prompt, images, opts, fn) error
Chat(ctx, name, messages, opts, fn) error
Embed(ctx, name, inputs, opts) ([][]float32, error)
Stop()                                          // unload everything, stop sweeper
```

`Generate`/`Chat`/`Embed` all go through `acquire`, which is `Get` (refresh
keep-alive) or `Load` (load if missing). This means **the first request for a
model pays the load cost; subsequent requests hit the warm runner** until the
keep-alive expires.

---

## The keep-alive model

Each `LoadedModel` has an `ExpiresAt` time. Every time a request touches a
loaded model, `ExpiresAt` is bumped to `now + Options.KeepAlive` (default
`5m`).

A background `sweepLoop` goroutine ticks every 30 seconds. On each tick, it
walks the loaded-models map, and for any model whose `ExpiresAt` has passed,
it:

1. Removes the entry from the map.
2. Calls `runner.Close()` to free resources.

This means idle models are unloaded automatically, freeing RAM/VRAM for other
models. The user can override the default keep-alive per-request via the
`keep_alive` field on `/api/generate`, `/api/chat`, etc.

Special values for `keep_alive`:

| Value | Meaning |
| --- | --- |
| `"0s"` / `0` | Unload immediately after this request (one-shot) |
| `"-1s"` / `-1` | Keep loaded forever (no auto-unload) |
| `"5m"` | Five minutes (default) |
| `"1h"` | One hour |

The keep-alive is enforced only by the sweeper; if the server is shut down
gracefully (`Server.Stop()`), all runners are closed immediately.

---

## Model storage: manifests & blobs

Nova stores models on disk in a layout that mirrors OCI/Docker's image layout,
simplified:

```
%USERPROFILE%\.nova\models\
├── manifests\
│   └── <registry>\<namespace>\<model>\<tag>     ← JSON manifest file
├── blobs\
│   └── sha256\<digest>                          ← raw content-addressed layer bytes
├── .tmp\                                        ← sockets, pid files, scratch
└── logs\                                        ← rotating logs
```

### Manifest

A manifest is a JSON document naming the layers that make up a model:

```json
{
  "schemaVersion": 2,
  "name": "llama3:latest",
  "registry": "registry.nova.ai",
  "created": "2024-07-02T12:00:00Z",
  "modified": "2024-07-02T12:00:00Z",
  "layers": [
    { "mediaType": "application/vnd.nova.model+binary",      "digest": "sha256:abc...", "size": 4799994880 },
    { "mediaType": "application/vnd.nova.model.template+json","digest": "sha256:def...", "size": 412 },
    { "mediaType": "application/vnd.nova.model.params+json", "digest": "sha256:ghi...", "size": 88 },
    { "mediaType": "application/vnd.nova.model.system+json", "digest": "sha256:jkl...", "size": 56 },
    { "mediaType": "application/vnd.nova.model.license+text","digest": "sha256:mno...", "size": 1064 }
  ]
}
```

Layer media types (defined in `internal/model/manifest.go`):

| Media type | What it is |
| --- | --- |
| `application/vnd.nova.model+binary` | The model weights (GGUF, etc.) |
| `application/vnd.nova.model.template+json` | The prompt template (Go text/template) |
| `application/vnd.nova.model.params+json` | Inference parameters |
| `application/vnd.nova.model.system+json` | The system prompt |
| `application/vnd.nova.model.messages+json` | Pre-baked chat messages |
| `application/vnd.nova.model.adapter+binary` | A LoRA / fine-tune adapter |
| `application/vnd.nova.model.license+text` | License text |

### Blobs

Blobs are stored under `blobs/sha256/<hex>`. Their filenames are their own
digest, so they're automatically deduplicated across models — copying a model
(`nova cp`) creates a new manifest that references the same blobs. Deleting a
model (`nova rm`) removes the manifest but leaves the blobs in place (a
future `nova prune` will garbage-collect unreferenced blobs).

### Naming

Model names parse into `(Registry, Namespace, Model, Tag)` (see
`internal/registry/registry.go`). The defaults are
`registry.nova.ai/library/<model>/latest`. The full path under `manifests/`
is `<registry>/<namespace>/<model>/<tag>` — exactly four segments, which makes
the `List()` walk a simple directory traversal.

---

## HTTP API layer

`internal/api/handlers` registers every route on a single `*http.ServeMux`:

| Route | Method | Handler |
| --- | --- | --- |
| `/api/generate` | POST | streams or returns a completion |
| `/api/chat` | POST | streams or returns a chat completion |
| `/api/embeddings` | POST | single embedding (deprecated alias) |
| `/api/embed` | POST | batched embeddings |
| `/api/tags` | GET | list installed models |
| `/api/show` | POST | show model details |
| `/api/pull` | POST | pull a model (streamed progress) |
| `/api/push` | POST | push a model (streamed progress) |
| `/api/create` | POST | create a model from a Modelfile |
| `/api/delete` | DELETE | delete a model |
| `/api/copy` | POST | copy a model |
| `/api/ps` | GET | list loaded models |
| `/api/version` | GET | server version |
| `/api/blobs/{digest}` | HEAD, POST | check / upload a blob |
| `/v1/chat/completions` | POST | OpenAI-compat chat |
| `/v1/completions` | POST | OpenAI-compat legacy completions |
| `/v1/embeddings` | POST | OpenAI-compat embeddings |
| `/v1/models` | GET | OpenAI-compat model list |

Handlers never touch runners directly — they go through the `Server`. Handlers
are responsible for:

- Decoding/encoding JSON.
- Translating between OpenAI/Ollama schemas and Nova's internal types
  (`internal/openai` package).
- Streaming: NDJSON for `/api/*`, SSE for `/v1/*`.
- Status codes & error envelopes.

See [`docs/API.md`](API.md) for the full per-endpoint reference.

---

## CLI layer

`internal/cmd` is built on `spf13/cobra`. Each subcommand is a separate file
(`serve.go`, `run.go`, `pull.go`, ...) that registers itself with `rootCmd`
in `root.go`.

Most subcommands talk to the running `nova serve` daemon over HTTP via a small
client helper. A few (`version`, `--tray`) work offline.

`nova run` is the most complex: in one-shot mode it sends a single
`/api/generate` request and streams the response to stdout; in REPL mode
(`nova run -m <model>` with no positional prompt) it loops, maintains
context, and supports slash commands.

`nova --tray` and `nova-tray.exe` invoke the tray package, which embeds a
server in-process and starts it (see below).

---

## Windows tray lifecycle

The tray app is the **same Go binary** as the CLI, built with
`-H=windowsgui` so Windows gives it the **GUI subsystem** — meaning
double-clicking it won't pop up a console window. Internally:

```
nova-tray.exe startup
   │
   ▼
internal/tray.New()
   │  - creates a Server (server.New(factory))
   │  - registers tray menu items (Start/Stop API, Open Web UI, Models, About, Quit)
   │  - installs SIGINT/SIGTERM handlers
   ▼
┌─────────────────┐         ┌────────────────────────────┐
│  Tray icon loop │◄───────►│  Background HTTP server    │
│  (systray.Run)  │         │  (server.New + net/http)   │
└────────┬────────┘         └─────────────┬──────────────┘
         │                                │
         │ "Start API" menu click         │ /api/* requests from clients
         ▼                                ▼
   server.Start() ──────────────► handlers ──► runners ──► blobs/manifests
         │
         │ "Quit" menu click / window close
         ▼
   server.Stop()  (unloads all models, closes runners)
   systray.Quit()
```

Key points:

- The tray app **embeds** the server — it does not need a separate `nova serve`
  process. One process, one icon.
- The "Start/Stop API" menu item toggles the embedded HTTP listener. When
  stopped, the tray icon's colour/state changes.
- The "Open Web UI" item launches `http://localhost:11434` in the default
  browser.
- The "Models" submenu lists installed models and offers quick pull/list/delete.
- On Quit, the tray app calls `server.Stop()` (which unloads all models
  gracefully) and then exits.

Because the tray binary is built from the same source as the CLI, you can
also launch the tray from the console binary with `nova --tray` — useful for
debugging (the console window stays open and you can read logs).

---

## Request lifecycle: a full walkthrough

What happens when a client calls `POST /api/generate` with
`{ "model": "llama3", "prompt": "Hello!" }`?

1. **`net/http`** accepts the connection and dispatches to the route handler
   registered on the mux.
2. The **`generate` handler** in `internal/api/handlers`:
   - Decodes the JSON body into a `GenerateRequest`.
   - Resolves the model name via `registry.Parse`.
   - Calls `server.Generate(ctx, name, prompt, images, opts, fn)`.
3. The **`Server.Generate`** method:
   - Calls `acquire(ctx, name, opts)` → tries `Get(name)` (refresh keep-alive
     if present) or `Load(ctx, name, opts)` (reads manifest, instantiates a
     runner via the factory, calls `runner.Load`).
   - Calls `runner.Generate(ctx, prompt, images, opts, fn)`.
4. The **`Runner.Generate`** implementation:
   - For the **StubRunner**: splits the prompt into words, sleeps 5ms per
     token, calls `fn(Token{Content: word})` for each, then a final
     `fn(Token{Done: true, ...})`.
   - For a **real runner** (e.g. llama.cpp): streams tokens from the
     underlying engine, translating them to `Token` values.
5. The handler's **`fn` callback**:
   - If `stream: true` (default), writes one JSON object per token to the
     `http.ResponseWriter` and flushes.
   - If `stream: false`, accumulates into a buffer and writes one final
     response.
6. When `runner.Generate` returns, the handler writes any trailing metadata
   (token counts, durations) and closes the response.

The whole path is concurrency-safe. Multiple clients hitting the same model
share one `Runner` (which must be goroutine-safe). The Server's mutex
protects the loaded-model map but is **not** held during generation — each
request gets its own copy of the runner pointer and the runner handles its
own concurrency.

---

## Cross-cutting concerns

### Configuration (`internal/env`)

All runtime config — host, models dir, CORS origins, debug flag, keep-alive,
max runners, VRAM limits, flash attention, context size — flows through
`internal/env`. The package exposes typed accessors (`Host()`, `ModelsDir()`,
`Debug()`, `AllowedOrigins()`, etc.) and a `EnsureDirs()` helper that creates
the on-disk directory tree on first run.

### Logging

Nova uses the stdlib `log` package. With `NOVA_DEBUG=1`, verbose logs go to
stderr (or to `%USERPROFILE%\.nova\models\logs\nova.log` when running as the
tray app). The server logs every request, every model load/unload, and every
sweep tick at debug level.

### Version metadata (`internal/version`)

Build-time version/commit/date are injected via `-ldflags` into
`internal/version.Version`, `.Commit`, `.BuildDate`. Both the CLI (`nova
version`) and the API (`GET /api/version`) read from this package. The CI
workflow computes the values from `git describe`.

### Error envelopes

All HTTP errors use the same JSON shape:

```json
{ "error": "human-readable message" }
```

OpenAI-compat endpoints additionally set the appropriate HTTP status code and
may emit OpenAI-shaped error bodies (future work).

---

## Design decisions & trade-offs

| Decision | Why |
| --- | --- |
| **Single binary, three modes** (CLI / server / tray) | Smallest possible attack surface, simplest install, no service management required. |
| **Stub runner by default** | Lets us ship and exercise the full surface in CI without bundling multi-GB weights or depending on llama.cpp. |
| **`llm.Runner` as the only backend seam** | One interface to swap; everything above is backend-agnostic. |
| **Content-addressed blob store** | Free deduplication across models; copy is cheap; mirrors OCI so registries are familiar. |
| **Ollama + OpenAI dual API surface** | Drop-in replacement for Ollama clients AND any OpenAI SDK. Maximises compatibility. |
| **Per-user install (`%LOCALAPPDATA%`)** | No admin rights needed; no UAC prompt; trivial uninstall. |
| **GUI subsystem via `-H=windowsgui`** | Same source, different subsystem flag — no separate tray codebase. |
| **NDJSON for `/api/*`, SSE for `/v1/*`** | Matches each ecosystem's convention exactly so existing clients work. |
| **Keep-alive with background sweep** | Frees VRAM automatically when models go idle; small CPU cost (30s tick). |
| **No external service / daemon manager** | The tray app embeds the server. One process, one icon. Users don't need to learn a service manager. |

### Known limitations (current scope)

- The default runner is the stub — no real inference yet. Swap in a runner per
  [`docs/DEVELOPMENT.md`](DEVELOPMENT.md).
- Registry push/pull hit `registry.nova.ai` by default — that host may not
  exist yet; point `NOVA_REGISTRY` at a real registry (future).
- No built-in web UI yet — the tray app's "Open Web UI" item points at
  `http://localhost:11434` which currently returns the version JSON.
- Garbage collection of unreferenced blobs is manual (a future `nova prune`).
