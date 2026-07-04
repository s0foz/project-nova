# Project:Nova CLI Reference

This document is the authoritative reference for the `nova` command-line
interface. It mirrors `nova --help` and every subcommand's `--help` output.

> **Quick orientation.** `nova` is a single binary with subcommands. The most
> common commands are `serve` (start the API server), `run` (generate/chat),
> `pull` (download a model), and `list` (see installed models). The tray app
> is the same binary launched as `nova --tray` (or `nova-tray.exe` directly).

---

## Table of contents

- [Synopsis](#synopsis)
- [Global flags](#global-flags)
- [Commands](#commands)
  - [`nova serve`](#nova-serve)
  - [`nova run`](#nova-run)
  - [`nova pull`](#nova-pull)
  - [`nova push`](#nova-push)
  - [`nova list`](#nova-list)
  - [`nova rm`](#nova-rm)
  - [`nova cp`](#nova-cp)
  - [`nova create`](#nova-create)
  - [`nova show`](#nova-show)
  - [`nova ps`](#nova-ps)
  - [`nova version`](#nova-version)
  - [`nova help`](#nova-help)
- [Exit codes](#exit-codes)
- [Environment variables](#environment-variables)
- [Shell completion](#shell-completion)

---

## Synopsis

```
nova [global-flags] <command> [command-flags] [args]
```

Most commands accept a model name as the first positional argument. Model names
follow the [Model name format](API.md#model-name-format):
`[registry/][namespace/]model[:tag]`.

---

## Global flags

These flags apply to every subcommand (must appear before the command name):

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--help`, `-h` | bool | `false` | Show help for `nova` or a subcommand |
| `--version`, `-v` | bool | `false` | Print version and exit |
| `--tray` | bool | `false` | Launch the desktop tray app (Windows GUI subsystem; ignored on non-Windows) |
| `--config` | string | (none) | Path to a config file (TOML/YAML, future) |
| `--debug` | bool | `false` | Enable verbose debug logging (equivalent to `NOVA_DEBUG=1`) |

---

## Commands

### `nova serve`

Start the Nova HTTP API server. This is the daemon the CLI, tray app, and any
external client (curl, OpenAI SDK, etc.) talk to.

#### Synopsis

```
nova serve [flags]
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--host`, `-H` | string | `$NOVA_HOST` or `127.0.0.1:11434` | Listen address (`host:port`) |
| `--models`, `-m` | string | `$NOVA_MODELS` or `%USERPROFILE%\.nova\models` | Models directory |
| `--origins` | string | `$NOVA_ORIGINS` or `localhost,127.0.0.1,0.0.0.0` | Comma-separated allowed CORS origins |
| `--keep-alive` | duration | `5m` | Default model keep-alive duration |
| `--max-runners` | int | `0` (auto) | Maximum concurrent loaded models |
| `--max-vram` | size | `0` (auto) | Maximum VRAM (bytes; accepts `2GB`, `512MB`) |
| `--flash-attention` | bool | `false` | Enable flash attention |
| `--num-ctx` | int | `4096` | Default context window |
| `--debug` | bool | `false` | Verbose logging |
| `--tls-cert` | string | (none) | Path to TLS cert (enables HTTPS) |
| `--tls-key` | string | (none) | Path to TLS key |

#### Examples

```bash
# Start the server with defaults
nova serve

# Listen on all interfaces, port 8080
nova serve --host 0.0.0.0:8080

# Use a custom models directory
nova serve --models D:\MyModels

# HTTPS
nova serve --tls-cert cert.pem --tls-key key.pem
```

#### Sample output

```
==> Project:Nova 0.1.0 (commit abc1234, built 2024-07-02T12:00:00Z)
==> Listening on http://127.0.0.1:11434
==> Models dir: C:\Users\you\.nova\models
==> Press Ctrl+C to stop
```

---

### `nova run`

Generate a completion for a prompt, or open an interactive chat REPL. By
default talks to the local `nova serve` over HTTP; with `--model` and an
embedded stub runner it can also run fully offline.

#### Synopsis

```
nova run <model> [prompt]
nova run [flags] -m <model>           # interactive REPL
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--model`, `-m` | string | (required) | Model name |
| `--system`, `-s` | string | (from Modelfile) | Override the system prompt |
| `--template` | string | (from Modelfile) | Override the prompt template |
| `--temperature`, `-t` | float | (from Modelfile) | Sampling temperature |
| `--top-p` | float | (from Modelfile) | Nucleus sampling probability |
| `--top-k` | int | (from Modelfile) | Top-K sampling |
| `--num-ctx` | int | (from Modelfile) | Context window size |
| `--num-predict`, `-n` | int | (from Modelfile) | Max tokens to predict |
| `--seed` | int | `-1` | RNG seed |
| `--stop` | string (repeatable) | (from Modelfile) | Stop sequence(s) |
| `--format` | string | (none) | `json` to force JSON output |
| `--raw` | bool | `false` | Pass prompt through without applying template |
| `--image`, `-i` | string (repeatable) | (none) | Path to image file (multimodal) |
| `--verbose`, `-v` | bool | `false` | Print per-request stats (tokens, durations) |
| `--keep-alive` | duration | `5m` | Override model keep-alive |
| `--insecure` | bool | `false` | Skip TLS verification when talking to a remote Nova server |
| `--host` | string | `127.0.0.1:11434` | Remote Nova server to talk to |

If a single positional `prompt` argument is provided, Nova generates one
completion and exits. If no prompt is given, Nova enters an interactive REPL.

#### Examples

```bash
# One-shot completion
nova run llama3 "Why is the sky blue?"

# Interactive chat REPL
nova run -m llama3

# Force JSON output
nova run llama3 --format json "List 3 colors as JSON."

# Set a custom system prompt
nova run llama3 -s "You are a pirate." "Hello!"

# Multimodal
nova run llava -i photo.jpg "What's in this picture?"
```

#### Sample output (interactive REPL)

```
>>> Hello!
Hi there! How can I help you today?
>>> /bye
Goodbye!
```

REPL slash commands: `/bye` (quit), `/show system`, `/show template`,
`/save <name>`, `/load <name>`, `/clear`, `/set <key> <value>`.

---

### `nova pull`

Pull (download) a model from a registry. Streams progress to the terminal.

#### Synopsis

```
nova pull <model> [flags]
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--insecure` | bool | `false` | Allow insecure registry connections |
| `--quiet`, `-q` | bool | `false` | Suppress progress output |

#### Examples

```bash
nova pull llama3
nova pull llama3:8b
nova pull quantum/qwen2:7b
```

#### Sample output

```
pulling manifest...
pulling sha256:ac477b39ea8f...  100% |████████████████████| 4.7 GB   12.3 MB/s   6m18s
verifying sha256 digest
writing manifest
success
```

---

### `nova push`

Push a local model to a registry. Requires registry credentials (configured
via `~/.nova/auth.json` or `NOVA_REGISTRY_TOKEN`).

#### Synopsis

```
nova push <model> [flags]
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--insecure` | bool | `false` | Allow insecure registry connections |
| `--quiet`, `-q` | bool | `false` | Suppress progress output |

#### Examples

```bash
nova push myorg/mymodel:1.0
```

---

### `nova list`

List all models installed locally.

#### Synopsis

```
nova list [flags]
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--json`, `-j` | bool | `false` | Output as JSON (matches `GET /api/tags`) |

#### Examples

```bash
nova list
nova list --json
```

#### Sample output

```
NAME                        ID              SIZE      MODIFIED
llama3:latest               ac477b39ea8f    4.7 GB    2 minutes ago
qwen2:7b                     1f2c8d4e9a73    4.3 GB    1 hour ago
myassistant:latest          9c8d4e1f2a73    4.7 GB    2 days ago
```

---

### `nova rm`

Delete a model and its manifest. Blobs that are no longer referenced are left
in place (run `nova prune` to garbage-collect, future).

#### Synopsis

```
nova rm <model> [model...] [flags]
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--force`, `-f` | bool | `false` | Skip the confirmation prompt |
| `--keep-blobs` | bool | `false` | Don't delete the manifest's blobs (shared with other models) |

#### Examples

```bash
nova rm llama3:8b
rm -f llama3:8b myassistant           # remove several at once
```

#### Sample output

```
deleted 'llama3:8b'
```

---

### `nova cp`

Copy an existing model to a new name. The blob layers are shared
(copy-on-write at the manifest level — no data duplication).

#### Synopsis

```
nova cp <source> <destination>
```

#### Examples

```bash
nova cp llama3:8b myalias:latest
nova cp llama3:8b quantum/qwen2:copy
```

#### Sample output

```
copied 'llama3:8b' -> 'myalias:latest'
```

---

### `nova create`

Build a new model from a Modelfile.

#### Synopsis

```
nova create <name> [-f Modelfile] [flags]
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--file`, `-f` | string | `./Modelfile` | Path to a Modelfile |
| `--quantize`, `-q` | string | (none) | Quantization to apply (e.g. `q4_0`, `q8_0`) |

If `--file` is omitted, Nova reads `./Modelfile` from the current directory.

#### Examples

```bash
# From a Modelfile in the current directory
nova create myassistant

# Specify a path
nova create myassistant -f ./Modelfiles/myassistant.modelfile

# Quantize on build
nova create myassistant-q4 -f ./Modelfile --quantize q4_0
```

#### Sample output

```
reading modelfile...
pulling base model 'llama3:8b'... done
using existing layer sha256:ac477b39ea8f...
writing manifest...
success
```

---

### `nova show`

Show details about an installed model: its Modelfile, parameters, template,
system message, license, and metadata.

#### Synopsis

```
nova show <model> [flags]
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--modelfile` | bool | `false` | Print only the Modelfile |
| `--license` | bool | `false` | Print only the license text |
| `--parameters` | bool | `false` | Print only the parameters block |
| `--template` | bool | `false` | Print only the template |
| `--system` | bool | `false` | Print only the system message |
| `--info` | bool | `false` | Print only the metadata info block |
| `--json`, `-j` | bool | `false` | Output as JSON (matches `POST /api/show`) |

#### Examples

```bash
nova show llama3
nova show llama3 --modelfile
nova show llama3 --json
```

#### Sample output

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

System
  You are a helpful assistant.

License
  MIT
```

---

### `nova ps`

List models currently loaded into memory on the running server.

#### Synopsis

```
nova ps [flags]
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--json`, `-j` | bool | `false` | Output as JSON (matches `GET /api/ps`) |

#### Examples

```bash
nova ps
nova ps --json
```

#### Sample output

```
NAME              ID              SIZE      VRAM     EXPIRES          until
llama3:latest     ac477b39ea8f    4.7 GB    4.7 GB   4 minutes        2024-07-02 12:38:00
```

---

### `nova version`

Print the version, commit, and build date of the `nova` binary.

#### Synopsis

```
nova version
nova --version
```

#### Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--json`, `-j` | bool | `false` | Output as JSON |

#### Examples

```bash
nova version
nova --version
nova version --json
```

#### Sample output

```
nova version 0.1.0 (commit abc1234, built 2024-07-02T12:00:00Z)
```

JSON:

```json
{
  "version": "0.1.0",
  "commit": "abc1234",
  "build_date": "2024-07-02T12:00:00Z"
}
```

---

### `nova help`

Show help. With no argument, prints top-level help and the command list. With
a command name, prints that command's help.

#### Synopsis

```
nova help [command]
nova --help
nova <command> --help
```

---

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Generic error |
| `2` | Usage error (bad flag or missing argument) |
| `3` | Network error (could not reach `nova serve`) |
| `4` | Model not found |
| `130` | Interrupted by Ctrl+C |

---

## Environment variables

All environment variables override the equivalent CLI flag.

| Variable | Default | Description |
| --- | --- | --- |
| `NOVA_HOST` | `127.0.0.1:11434` | Listen address (`nova serve`) or remote server (`nova run`) |
| `NOVA_MODELS` | `%USERPROFILE%\.nova\models` | Models directory |
| `NOVA_ORIGINS` | `localhost,127.0.0.1,0.0.0.0` | Allowed CORS origins |
| `NOVA_DEBUG` | (unset) | `1` / `true` enables verbose logging |
| `NOVA_MAX_RUNNERS` | (auto) | Max concurrent loaded models |
| `NOVA_MAX_VRAM` | (auto) | Max VRAM (bytes) |
| `NOVA_FLASH_ATTENTION` | `0` | `1` enables flash attention |
| `NOVA_KEEP_ALIVE` | `5m` | Default model keep-alive duration |
| `NOVA_NUM_CTX` | `4096` | Default context window size |
| `NOVA_REGISTRY_TOKEN` | (unset) | Bearer token for registry auth |

---

## Shell completion

Nova can generate shell completion scripts (cobra-based):

```bash
# PowerShell (add to $PROFILE)
nova completion powershell >> $PROFILE

# Bash
nova completion bash > /etc/bash_completion.d/nova

# Zsh
nova completion zsh > "${fpath[1]}/_nova"

# Fish
nova completion fish > ~/.config/fish/completions/nova.fish
```

After sourcing, `nova <Tab>` will offer subcommands and `nova run <Tab>` will
offer installed model names where supported.
