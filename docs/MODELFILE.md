# Project:Nova Modelfile Reference

A **Modelfile** is a Dockerfile-like text document that describes how to
assemble a Project:Nova model. It is the same grammar Ollama uses, so existing
Modelfiles (and the Ollama model library) work without modification.

A Modelfile is consumed by:

- `nova create <name> <Modelfile>` — builds and saves the model locally
- `POST /api/create` — same operation via the HTTP API
- `nova show <model>` — re-emits the (normalised) Modelfile

This document is the authoritative reference for every directive, parameter
key, template variable, and example.

---

## Table of contents

- [Syntax overview](#syntax-overview)
- [Directives](#directives)
  - [`FROM`](#from)
  - [`ADAPTER`](#adapter)
  - [`PARAMETER`](#parameter)
  - [`TEMPLATE`](#template)
  - [`SYSTEM`](#system)
  - [`MESSAGE`](#message)
  - [`LICENSE`](#license)
- [Parameter keys](#parameter-keys)
- [Template syntax](#template-syntax)
- [Examples](#examples)
  - [Example 1 — Basic completion model](#example-1--basic-completion-model)
  - [Example 2 — Chat assistant with a system prompt](#example-2--chat-assistant-with-a-system-prompt)
  - [Example 3 — Fine-tuned model with a LoRA adapter](#example-3--fine-tuned-model-with-a-lora-adapter)
  - [Example 4 — Custom template with tool calls](#example-4--custom-template-with-tool-calls)

---

## Syntax overview

A Modelfile is a sequence of lines, each of one of these forms:

```
DIRECTIVE value
```

- Directives are case-insensitive (`FROM`, `from`, `From` are all valid).
- Lines beginning with `#` are comments.
- Blank lines are ignored.
- String values may be quoted with `"..."` or `'...'`; quotes are stripped.
- Multi-line values use a heredoc-style syntax (see [TEMPLATE](#template) and
  [SYSTEM](#system)).

Example:

```dockerfile
# Modelfile for my assistant
FROM llama3:8b
PARAMETER temperature 0.6
PARAMETER stop "<|im_end|>"
SYSTEM """You are a concise, friendly assistant."""
LICENSE "MIT"
```

---

## Directives

### `FROM`

Specifies the base model the new model inherits from. **Required.**

```
FROM <model-name>
```

The `<model-name>` follows the [Model name format](API.md#model-name-format):

| Value | Meaning |
| --- | --- |
| `llama3` | The `library/llama3:latest` model from the default registry |
| `llama3:8b` | The 8B-parameter tag of llama3 |
| `quantum/qwen2` | The `quantum/qwen2:latest` model |
| `./path/to/gguf` | A local GGUF file (path must exist) |
| `sha256:<hex>` | A specific blob digest |

Examples:

```dockerfile
FROM llama3:8b
FROM ./models/my-base.gguf
FROM sha256:ac477b39ea8f4d3b9c2e1f5a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c
```

---

### `ADAPTER`

Adds a fine-tune adapter (LoRA, etc.) on top of the base model. May appear
multiple times.

```
ADAPTER <path-or-digest>
```

| Value | Meaning |
| --- | --- |
| `./adapters/my-lora.bin` | Path to a local adapter file |
| `sha256:<hex>` | A blob digest in the local store |

Example:

```dockerfile
FROM llama3:8b
ADAPTER ./adapters/sarcasm.bin
```

---

### `PARAMETER`

Sets a single inference-time parameter. May appear multiple times for the same
key (e.g. `stop`); for scalar keys the last value wins.

```
PARAMETER <key> <value>
```

See [Parameter keys](#parameter-keys) for the full list.

Example:

```dockerfile
PARAMETER temperature 0.7
PARAMETER top_p 0.9
PARAMETER num_ctx 8192
PARAMETER stop "<|im_end|>"
PARAMETER stop "User:"
```

---

### `TEMPLATE`

Sets the Go text/template used to render the prompt sent to the runner. The
template receives the conversation state as variables.

Single-line form:

```
TEMPLATE "{{ .System }}\n\n{{ .Prompt }}"
```

Multi-line heredoc form (preferred for non-trivial templates):

```
TEMPLATE """{{ if .System }}<|im_start|>system
{{ .System }}<|im_end|>
{{ end }}{{ range .Messages }}<|im_start|>{{ .Role }}
{{ .Content }}<|im_end|>
{{ end }}<|im_start|>assistant
"""
```

See [Template syntax](#template-syntax) below for the full variable and
function reference.

---

### `SYSTEM`

Sets the default system message injected into the conversation. Equivalent to
a `MESSAGE system "..."` directive at the start of the conversation, but
stored separately so `nova show` reports it under its own field.

Single-line form:

```
SYSTEM You are a concise assistant.
```

Quoted form:

```
SYSTEM "You are a concise assistant."
```

Multi-line heredoc form:

```
SYSTEM """You are a concise assistant.
You always answer in three sentences or fewer.
You never apologise."""
```

---

### `MESSAGE`

Seeds the conversation with a pre-baked message. May appear multiple times.

```
MESSAGE <role> <content>
```

| Role | Description |
| --- | --- |
| `system` | A system message (alternative to `SYSTEM`) |
| `user` | A user turn |
| `assistant` | An assistant turn |
| `tool` | A tool result (must follow a tool call) |

Example:

```dockerfile
FROM llama3
MESSAGE user "What's 2+2?"
MESSAGE assistant "4."
```

Useful for one-shot / few-shot prompting baked into the model.

---

### `LICENSE`

Attaches license text to the model. The text is stored as a layer and
surfaced by `nova show` and `POST /api/show`.

```
LICENSE MIT
LICENSE """MIT License

Copyright (c) 2024 ...

Permission is hereby granted ..."""
```

---

## Parameter keys

All parameter keys are lowercase. Booleans accept `true`/`false` (and `1`/`0`).

### Sampling

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `temperature` | float | `0.8` | Sampling temperature. Higher = more random. |
| `top_k` | int | `40` | Top-K sampling: keep only the K most likely tokens at each step. |
| `top_p` | float | `0.9` | Nucleus sampling: keep tokens whose cumulative probability ≤ P. |
| `min_p` | float | `0.0` | Minimum token probability (relative to max) to keep. |
| `typical_p` | float | `1.0` | Typical-p sampling. `1.0` disables it. |
| `seed` | int | `-1` | RNG seed. `-1` = random per run. |
| `repeat_penalty` | float | `1.1` | Penalty applied to repeated tokens. `1.0` = no penalty. |
| `repeat_last_n` | int | `64` | How many previous tokens to consider for the repeat penalty. |
| `presence_penalty` | float | `0.0` | OpenAI-style presence penalty. |
| `frequency_penalty` | float | `0.0` | OpenAI-style frequency penalty. |
| `mirostat` | int | `0` | Mirostat mode. `0` = off, `1` = mirostat, `2` = mirostat 2. |
| `mirostat_tau` | float | `5.0` | Mirostat target entropy. |
| `mirostat_eta` | float | `0.1` | Mirostat learning rate. |
| `penalize_newline` | bool | `true` | Whether to penalise newline tokens. |

### Context & generation

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `num_ctx` | int | `4096` | Context window size in tokens. |
| `num_batch` | int | `512` | Prompt processing batch size. |
| `num_predict` | int | `-1` | Max tokens to predict. `-1` = unlimited, `0` = no generation. |
| `num_keep` | int | `0` | Tokens to keep from the prompt when truncating. |
| `stop` | []string | `[]` | Stop sequences. May be specified multiple times. |

### Performance / hardware

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `num_thread` | int | (auto) | Number of CPU threads. |
| `num_gpu` | int | (auto) | Number of model layers to offload to the GPU. `-1` = all. |
| `low_vram` | bool | `false` | Hint to use less VRAM (smaller batch). |
| `f16_kv` | bool | (auto) | Use 16-bit floats for the KV cache. |
| `flash_attention` | bool | `false` | Enable flash attention. |
| `use_mlock` | bool | `false` | Lock the model in RAM (no swap). |
| `use_mmap` | bool | `true` | Use `mmap` to load weights (faster startup). |
| `vocab_only` | bool | `false` | Load only the vocabulary (no weights) — for tokenisation. |

### Runtime

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `keep_alive` | duration | `5m` | How long a loaded model stays resident after its last use. `0` = unload immediately, `-1` = forever. |

---

## Template syntax

Nova templates use the Go `text/template` engine
(https://pkg.go.dev/text/template). They are rendered for every request before
the prompt is sent to the runner.

### Available variables

| Variable | Type | Description |
| --- | --- | --- |
| `.System` | string | The system message (from `SYSTEM` directive or per-request override) |
| `.Prompt` | string | The user prompt (for `/api/generate` and `nova run`) |
| `.Messages` | []Message | The conversation messages (for `/api/chat` and `nova run -m`) |
| `.Tools` | []Tool | The tools/functions available for the model to call |
| `.Suffix` | string | The suffix to insert after the completion (insert mode) |
| `.First` | bool | True if this is the first turn (no prior assistant reply) |
| `.Last` | bool | True if rendering the last (current) message |

Each entry in `.Messages` has:

| Field | Type | Description |
| --- | --- | --- |
| `.Role` | string | `system`, `user`, `assistant`, or `tool` |
| `.Content` | string | The message content |
| `.Images` | []string | Base64-encoded images attached to the message |
| `.ToolCalls` | []ToolCall | Tool calls the assistant requested |

Each entry in `.Tools` has:

| Field | Type | Description |
| --- | --- | --- |
| `.Type` | string | Always `"function"` |
| `.Function.Name` | string | Function name |
| `.Function.Description` | string | Human description |
| `.Function.Parameters` | object | JSON Schema for the function arguments |

### Built-in template functions

These mirror Ollama's helpers:

| Function | Description |
| --- | --- |
| `json <value>` | Marshals the value to JSON. |
| `now <format>` | Current UTC time formatted with `format` (Go time layout). |
| `ago <time>` | Humanised relative time since `time`. |
| `join <list> <sep>` | Joins a list of strings with `sep`. |
| `repeat <n> <s>` | Repeats string `s` `n` times. |
| `trim <s>` | Trims whitespace. |
| `lower <s>` / `upper <s>` | Lowercases / uppercases. |
| `first <list>` | First element of a list. |
| `last <list>` | Last element of a list. |

### Standard Go template actions

All standard Go template actions work: `{{ if }}...{{ else }}...{{ end }}`,
`{{ range }}...{{ end }}`, `{{ with }}...{{ end }}`, `{{ define }}`,
`{{ template }}`, `{{ block }}`.

### Default templates per model family

Nova ships default templates for common model families. The default is
roughly:

```go
{{ if .System }}{{ .System }}{{ end }}
{{ if .Prompt }}{{ .Prompt }}{{ end }}
{{ if .Suffix }}{{ .Suffix }}{{ end }}
```

For chat-style models (e.g. ChatML), you typically want something like:

```
{{ if .System }}<|im_start|>system
{{ .System }}<|im_end|>
{{ end }}{{ range .Messages }}<|im_start|>{{ .Role }}
{{ .Content }}<|im_end|>
{{ end }}<|im_start|>assistant
```

---

## Examples

### Example 1 — Basic completion model

```dockerfile
# Modelfile: code completion
FROM llama3:8b
PARAMETER temperature 0.2
PARAMETER num_predict 64
PARAMETER stop "```"
SYSTEM "You complete code snippets. Output only code, no commentary."
```

Build it:

```bash
nova create code-completer ./Modelfile
nova run code-completer "func main() {"
```

---

### Example 2 — Chat assistant with a system prompt

```dockerfile
# Modelfile: friendly assistant
FROM llama3:8b

# Inference parameters
PARAMETER temperature 0.6
PARAMETER top_p 0.85
PARAMETER num_ctx 8192
PARAMETER stop "<|im_end|>"
PARAMETER stop "User:"
PARAMETER repeat_penalty 1.15

# ChatML-style prompt template
TEMPLATE """{{ if .System }}<|im_start|>system
{{ .System }}<|im_end|>
{{ end }}{{ range .Messages }}<|im_start|>{{ .Role }}
{{ .Content }}<|im_end|>
{{ end }}<|im_start|>assistant
"""

# System message
SYSTEM """You are Nova, a concise, friendly coding assistant.
You always answer in three sentences or fewer.
You never apologise or hedge."""

# A couple of pre-baked turns to set the tone
MESSAGE user "What's Go?"
MESSAGE assistant "A statically typed, compiled language with fast builds and great concurrency primitives."

LICENSE "MIT"
```

Build and chat:

```bash
nova create mychat ./Modelfile
nova run -m mychat
```

---

### Example 3 — Fine-tuned model with a LoRA adapter

```dockerfile
# Modelfile: sarcasm-tuned llama3
FROM llama3:8b

# LoRA adapter trained on sarcastic responses
ADAPTER ./adapters/sarcasm-lora-v1.bin

PARAMETER temperature 1.0
PARAMETER top_p 0.95
PARAMETER repeat_penalty 1.05

SYSTEM "You respond to every question with sarcasm. Keep it short."

LICENSE """Sarcasm adapter (c) 2024 Acme Corp.
MIT-licensed base model, adapter released under CC-BY 4.0."""
```

Build:

```bash
nova create sarcastic-llama ./Modelfile
nova run sarcastic-llama "How are you today?"
```

---

### Example 4 — Custom template with tool calls

```dockerfile
# Modelfile: function-calling assistant (ChatML + tools)
FROM qwen2:7b

PARAMETER temperature 0.3
PARAMETER num_ctx 16384
PARAMETER stop "<|im_end|>"

# Custom template that:
#   1. Renders the system message
#   2. Lists available tools as JSON
#   3. Renders the message history
#   4. Cues the assistant to reply (or call a tool)
TEMPLATE """{{ if .System }}<|im_start|>system
{{ .System }}
Available tools:
{{ if .Tools }}{{ json .Tools }}{{ end }}<|im_end|>
{{ end }}{{ range .Messages }}{{ if eq .Role "tool" }}<|im_start|>tool
{{ .Content }}<|im_end|>
{{ else }}<|im_start|>{{ .Role }}
{{ .Content }}{{ if .ToolCalls }}
[TOOL_CALLS] {{ json .ToolCalls }}{{ end }}<|im_end|>
{{ end }}{{ end }}<|im_start|>assistant
"""

SYSTEM """You are a helpful assistant with access to the listed tools.
When you need information, call a tool by emitting a JSON object under [TOOL_CALLS].
Otherwise, answer the user directly and concisely."""

LICENSE "Apache-2.0"
```

Build:

```bash
nova create tool-assistant ./Modelfile
```

Call it via the API with tools:

```bash
curl http://127.0.0.1:11434/api/chat -d '{
  "model": "tool-assistant",
  "messages": [ { "role": "user", "content": "What is the weather in Berlin?" } ],
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "Get current weather for a city",
      "parameters": {
        "type": "object",
        "properties": { "city": { "type": "string" } },
        "required": ["city"]
      }
    }
  }],
  "stream": false
}'
```

---

## Tips & gotchas

- **Quote stop sequences that contain special characters**:
  `PARAMETER stop "<|im_end|>"` not `PARAMETER stop <|im_end|>`.
- **Templates are parsed at create time**, not run time — `nova create`
  will fail loudly if the template is malformed.
- **`SYSTEM` and the first `MESSAGE system` are equivalent**; pick one
  style per Modelfile for readability.
- **Adapters must match the base model architecture**. A llama3 adapter on a
  qwen2 base will fail to load.
- **`keep_alive` is a duration**, parsed by Go's `time.ParseDuration`:
  `5m`, `30s`, `1h`, `-1` (forever), `0` (unload immediately).
- **Bake as much as possible into the model** (system prompt, defaults) so
  client code stays tiny. Per-request `options` always override Modelfile
  parameters.
