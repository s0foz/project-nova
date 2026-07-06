# Project:Nova — Contributor Guide

Welcome! This guide covers everything you need to contribute to Project:Nova:
the project layout, how to add a new API endpoint, how to swap the stub runner
for a real `llama.cpp`-backed runner, how to build and test locally, and how
to cut a release.

> **TL;DR.** Fork the repo, run `.\scripts\build-windows.ps1 -Tray`, run
> `nova serve` in one terminal and `nova run llama3 "hello"` in another. Pick
> an issue, send a PR. CI runs on Windows (`build-windows.yml`) and Ubuntu
> (`lint.yml`).

---

## Table of contents

- [Prerequisites](#prerequisites)
- [Project layout](#project-layout)
- [Build locally](#build-locally)
- [Run & test](#run--test)
- [Coding conventions](#coding-conventions)
- [Adding a new API endpoint](#adding-a-new-api-endpoint)
- [Adding a new CLI subcommand](#adding-a-new-cli-subcommand)
- [Swapping the stub runner for a real backend](#swapping-the-stub-runner-for-a-real-backend)
- [Testing](#testing)
- [Releasing](#releasing)
- [Debugging tips](#debugging-tips)

---

## Prerequisites

| Tool | Version | Why |
| --- | --- | --- |
| Go | 1.22+ | language |
| Git | any | versioning |
| Make | any | convenience targets (optional; PowerShell works too) |
| WiX Toolset v3 | 3.14+ | only for building the MSI |
| PowerShell | 5.1+ or 7+ | build & install scripts (Windows ships 5.1) |
| `golangci-lint` | v1.59+ | optional; CI runs it as non-blocking |

Optional, for model work:

| Tool | Why |
| --- | --- |
| A real LLM runner (llama.cpp, etc.) | To exercise `Runner` implementations beyond the stub |
| A GPU + CUDA / ROCm | For real inference performance |

---

## Project layout

```
project-nova/
├── cmd/
│   └── nova/                 # main package — single entry point
│       └── main.go           # dispatches to internal/cmd
├── internal/
│   ├── api/handlers/         # HTTP handlers (Ollama + OpenAI endpoints)
│   ├── cmd/                  # cobra CLI subcommands
│   ├── env/                  # env vars, paths, dir bootstrap
│   ├── llm/                  # Runner interface + StubRunner
│   ├── model/                # Manifest, Layer, Modelfile parser
│   ├── openai/               # OpenAI-compat request/response translation
│   ├── registry/             # on-disk model store (manifests + blobs)
│   ├── server/               # orchestrator: loaded models, keep-alive, sweep
│   ├── tray/                 # Windows system-tray lifecycle
│   └── version/              # build-time version metadata
├── scripts/                  # build/install/packaging PowerShell scripts
├── .github/workflows/        # CI workflows
├── docs/                     # this documentation
├── assets/                   # icons, splash images
├── go.mod
├── Makefile
├── LICENSE
└── README.md
```

All non-`main` packages live under `internal/` — Nova is not (yet) intended
to be imported as a library.

---

## Build locally

### PowerShell (recommended on Windows)

```powershell
# Console + tray binaries, version from git
.\scripts\build-windows.ps1 -Tray

# Specific version
.\scripts\build-windows.ps1 -Version 0.1.0 -Tray

# Windows-on-ARM
.\scripts\build-windows.ps1 -Arch arm64 -Tray
```

Outputs in `.\dist\`:

- `nova.exe` / `nova-arm64.exe` — CLI/console
- `nova-tray.exe` / `nova-tray-arm64.exe` — GUI subsystem (no console)

### Make (cross-platform)

```bash
make build-windows   # cross-compile nova.exe (console + GUI subsystem)
make package-zip     # produce dist/nova-windows-amd64-<ver>.zip
make package-msi     # invoke scripts/build-msi.ps1 (Windows only)
make test            # go test ./... -race -count=1
make vet             # go vet ./...
make fmt             # gofmt + goimports (if installed)
make lint            # golangci-lint if installed, else go vet
make clean           # rm -rf dist build wix standalone
```

### Plain `go build`

```bash
go build -trimpath -o nova.exe ./cmd/nova
```

Without ldflags you get the default `0.0.0-dev` version. To embed version
metadata:

```bash
VERSION=$(git describe --tags --always --dirty)
COMMIT=$(git rev-parse --short HEAD)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

go build -trimpath \
  -ldflags "-s -w \
    -X github.com/project-nova/nova/internal/version.Version=$VERSION \
    -X github.com/project-nova/nova/internal/version.Commit=$COMMIT \
    -X github.com/project-nova/nova/internal/version.BuildDate=$DATE" \
  -o nova.exe ./cmd/nova
```

---

## Run & test

Start the server in one terminal:

```powershell
.\dist\nova.exe serve
```

In another terminal, exercise the API:

```powershell
# Native API
curl http://127.0.0.1:11434/api/version
curl http://127.0.0.1:11434/api/tags

.\dist\nova.exe pull llama3
.\dist\nova.exe run llama3 "Hello, world!"
.\dist\nova.exe list
.\dist\nova.exe ps
.\dist\nova.exe show llama3

# OpenAI-compat
curl http://127.0.0.1:11434/v1/models
curl http://127.0.0.1:11434/v1/chat/completions `
     -H "Content-Type: application/json" `
     -d '{ "model": "llama3", "messages": [ { "role": "user", "content": "Hi" } ] }'
```

Run the test suite:

```bash
make test              # go test ./... -race -count=1
go test ./... -run TestRegistry -v
go test ./internal/llm -v
```

---

## Coding conventions

- `gofmt` (or `goimports`) — non-negotiable. CI fails on unformatted files.
- `go vet` clean — CI fails on vet issues.
- Exported identifiers must have doc comments starting with the identifier name.
- Errors are wrapped with `fmt.Errorf("...: %w", err)` where context helps.
- Package docs go in `doc.go` or at the top of the most relevant `.go` file.
- No `panic` in library code — return `error`.
- Test files live next to the source: `foo_test.go` for `foo.go`.
- Keep changes small and focused — one feature per PR.

---

## Adding a new API endpoint

To add, say, `POST /api/heartbeat`:

1. **Define the request/response types** in
   `internal/api/handlers/types.go` (or a new file in that package):

   ```go
   type HeartbeatRequest struct {
       Model string `json:"model"`
   }

   type HeartbeatResponse struct {
       OK bool `json:"ok"`
   }
   ```

2. **Add the handler** in `internal/api/handlers/heartbeat.go`:

   ```go
   package handlers

   import (
       "encoding/json"
       "net/http"
   )

   // Heartbeat handles POST /api/heartbeat.
   func (h *Handler) Heartbeat(w http.ResponseWriter, r *http.Request) {
       var req HeartbeatRequest
       if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
           writeError(w, http.StatusBadRequest, err)
           return
       }
       // ... do work, talk to h.Server ...
       writeJSON(w, http.StatusOK, HeartbeatResponse{OK: true})
   }
   ```

3. **Register the route** in `internal/api/handlers/router.go` (or wherever the
   mux is set up):

   ```go
   mux.HandleFunc("/api/heartbeat", h.heartbeatOnly)
   ```

   Where `heartbeatOnly` enforces `POST` and delegates to `h.Heartbeat`.

4. **Test it.** Add `heartbeat_test.go` exercising the happy path, a 400 on
   bad JSON, and a 404 (or 405) where appropriate. Use `httptest.NewServer`
   and drive it through the same client code the CLI uses.

5. **Document it** in [`docs/API.md`](API.md) with the request/response
   schemas, status codes, and a curl example.

6. **Optional: expose via CLI** if it makes sense as a subcommand — see the
   next section.

---

## Adding a new CLI subcommand

Nova uses `spf13/cobra`. To add `nova heartbeat`:

1. **Create the command** in `internal/cmd/heartbeat.go`:

   ```go
   package cmd

   import (
       "fmt"
       "github.com/spf13/cobra"
   )

   func newHeartbeatCmd() *cobra.Command {
       cmd := &cobra.Command{
           Use:   "heartbeat",
           Short: "Send a heartbeat to the running server",
           RunE: func(cmd *cobra.Command, args []string) error {
               client, err := newClient()
               if err != nil { return err }
               resp, err := client.Post("/api/heartbeat", nil)
               if err != nil { return err }
               fmt.Println(resp)
               return nil
           },
       }
       return cmd
   }
   ```

2. **Register it** in `internal/cmd/root.go`:

   ```go
   rootCmd.AddCommand(newHeartbeatCmd())
   ```

3. **Test it** by building and running `.\dist\nova.exe heartbeat --help`.

4. **Document it** in [`docs/CLI.md`](CLI.md).

---

## Swapping the stub runner for a real backend

This is the single biggest integration point. Everything in Nova talks to the
[`llm.Runner`](../internal/llm/llm.go) interface — implement that and you have
a real backend.

### The interface

```go
type Runner interface {
    Load(ctx context.Context, m *model.Manifest, opts Options) error
    Loaded() bool
    Generate(ctx context.Context, prompt string, images []string, opts Options, fn func(Token) error) error
    Chat(ctx context.Context, messages []Message, opts Options, fn func(Token) error) error
    Embed(ctx context.Context, inputs []string, opts Options) ([][]float32, error)
    Tokenize(ctx context.Context, text string) ([]int, error)
    Detokenize(ctx context.Context, tokens []int) (string, error)
    Count(ctx context.Context, text string) (int, error)
    Close() error
    Stats() Stats
}
```

The streaming `fn(Token)` callback is invoked once per generated token (or
once at the end if `opts.Stream == false`). Returning an error from `fn`
cancels generation.

### Where the factory is wired in

`internal/server/server.go` defines:

```go
type RunnerFactory func() llm.Runner
var DefaultFactory RunnerFactory = func() llm.Runner { return llm.NewStubRunner() }
```

`server.New(factory)` takes a factory. The CLI / server bootstrap passes
`nil` (uses `DefaultFactory`) or your own factory. To swap to a real runner:

1. Implement `llm.Runner` in a new package, e.g. `internal/llm/llamacpp/`.
   The implementation typically:
   - Spawns a `llama.cpp` `main` binary as a subprocess (the Ollama model)
     OR links llama.cpp via cgo.
   - Talks to the subprocess over a local socket / stdin-stdout.
   - Translates `Options` into llama.cpp CLI flags or RPC params.
   - Streams tokens back through the `fn` callback.
2. Build a factory that returns your runner:

   ```go
   func NewLlamaCppFactory(binPath string) server.RunnerFactory {
       return func() llm.Runner {
           return llamacpp.New(binPath)
       }
   }
   ```

3. Wire it in `cmd/nova/main.go` (or wherever the server is constructed):

   ```go
   factory := llamacpp.NewLlamaCppFactory(cfg.RunnerPath)
   srv := server.New(factory)
   ```

4. Add a config flag (`--runner=stub|llamacpp|...`) and a registry of
   factories keyed by name.

### Reference: what the stub does

`internal/llm/llm.go` ships `StubRunner`, which:

- `Load` marks the runner as loaded (no real work).
- `Generate` echoes the prompt word-by-word with a 5ms delay per token.
- `Chat` echoes the last user message.
- `Embed` returns a deterministic 64-dim pseudo-vector per input.
- `Tokenize` splits on whitespace.
- `Close` unloads.

Use StubRunner as a structural template — match its method signatures
exactly.

### Concurrency

The same `Runner` instance may be called from multiple goroutines (e.g. when
two clients hit `/api/generate` for the same model simultaneously). Protect
internal state with a mutex as `StubRunner` does.

### Keep-alive

The `Server` calls `Load` once, holds the runner in memory, and unloads it
(`Close()`) only after `Options.KeepAlive` elapses with no further activity.
Your `Load` should be idempotent — calling it on an already-loaded runner is a
no-op (or a reload if the manifest changed).

---

## Testing

- **Unit tests** go in `*_test.go` files next to the code. Use the standard
  `testing` package; `testify` is fine if you really need it but the stdlib is
  preferred.
- **Table-driven** tests are the norm. Example:

  ```go
  func TestParseModelfile(t *testing.T) {
      cases := []struct{
          name string
          in   string
          want string
      }{
          {"plain", "FROM llama3", "llama3"},
          {"tagged", "FROM llama3:8b", "llama3:8b"},
      }
      for _, c := range cases {
          t.Run(c.name, func(t *testing.T) {
              mf, err := model.ParseModelfile(strings.NewReader(c.in))
              if err != nil { t.Fatal(err) }
              if mf.From != c.want { t.Fatalf("got %q want %q", mf.From, c.want) }
          })
      }
  }
  ```

- **HTTP handler tests** should use `httptest.NewServer` and exercise the
  same surface area as the curl examples in [`docs/API.md`](API.md).
- **Runner tests** should use `StubRunner` for predictability.
- **Integration tests** (if any) should be tagged with `//go:build integration`
  and skipped in CI by default.

Run everything:

```bash
make test
go test ./... -race -count=1 -v
```

---

## Releasing

Releases are **automated via CI**. The workflow in
[`.github/workflows/build-windows.yml`](../.github/workflows/build-windows.yml)
publishes a GitHub Release on every `v*` tag.

### To cut a release

1. Make sure `main` is green (CI passing).
2. Update `docs/` and `README.md` if anything has changed.
3. Tag the release:

   ```bash
   git tag -a v0.1.0 -m "v0.1.0: first public release"
   git push origin v0.1.0
   ```

4. CI will:
   - Build `nova.exe`, `nova-tray.exe`, `nova-arm64.exe`, `nova-tray-arm64.exe`
   - Package `nova-<version>-windows-amd64.zip`
   - Create a GitHub Release with auto-generated release notes
   - Attach all artifacts
5. Verify the release on GitHub, then announce.

### Versioning

We follow [Semantic Versioning](https://semver.org/):

- **MAJOR**: incompatible API or Modelfile grammar changes
- **MINOR**: new endpoints, new CLI flags, new Modelfile directives
- **PATCH**: bug fixes

Pre-release tags are allowed: `v0.1.0-rc.1`, `v0.1.0-beta.3`. CI marks the
GitHub Release as a prerelease when the tag contains a dash.

### MSI

MSIs are **not** built by CI (WiX v3 is Windows-only and not preinstalled on
`windows-latest`). To produce an MSI for a release:

1. Download the release's `nova-<version>-windows-amd64.zip`.
2. Unzip into `dist/`.
3. Run:

   ```powershell
   .\scripts\build-msi.ps1 -Version <version>
   ```

4. Upload the resulting `dist/nova-<version>-amd64.msi` to the GitHub Release
   manually.

---

## Debugging tips

- Set `NOVA_DEBUG=1` for verbose logging (server side and CLI side).
- The model store lives at `%USERPROFILE%\.nova\models` by default. Inspect:
  - `manifests/<registry>/<namespace>/<model>/<tag>` — JSON manifests
  - `blobs/sha256/<digest>` — raw layer data
  - `logs/nova.log` — rotating server log
- If a model fails to load, `nova show <model>` will tell you which layer is
  missing.
- For HTTP debugging, run with `NOVA_DEBUG=1` and watch stderr — every request
  is logged with method, path, status, and duration.
- If `nova serve` won't start, check the port isn't in use:
  `netstat -ano | findstr :11434`.
- The stub runner streams with a 5ms-per-token delay so you can observe
  streaming behaviour clearly. To go faster, swap in your own runner.
