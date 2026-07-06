# Project:Nova REST API Reference

The Project:Nova HTTP API server listens on `http://127.0.0.1:11434` by
default (override with `NOVA_HOST`). It exposes two surface areas:

1. **Nova native API** (Ollama-compatible) under `/api/*`.
2. **OpenAI-compatible API** under `/v1/*`.

Both surfaces are served from the same listener. All endpoints accept and
return JSON unless noted otherwise. Streaming endpoints use **newline-delimited
JSON** (NDJSON) — one JSON object per line.

---

## Table of contents

- [Conventions](#conventions)
- [Authentication](#authentication)
- [Model name format](#model-name-format)
- [Errors](#errors)
- [Nova native API](#nova-native-api)
  - [`POST /api/generate`](#post-apigenerate)
  - [`POST /api/chat`](#post-apichat)
  - [`POST /api/embeddings`](#post-apiembeddings)
  - [`POST /api/embed`](#post-apiembed)
  - [`GET /api/tags`](#get-apitags)
  - [`POST /api/show`](#post-apishow)
  - [`POST /api/pull`](#post-apipull)
  - [`POST /api/push`](#post-apipush)
  - [`POST /api/create`](#post-apicreate)
  - [`DELETE /api/delete`](#delete-apidelete)
  - [`POST /api/copy`](#post-apicopy)
  - [`GET /api/ps`](#get-apips)
  - [`GET /api/version`](#get-apiversion)
  - [`HEAD /api/blobs/{digest}`](#head-apiblobsdigest)
  - [`POST /api/blobs/{digest}`](#post-apiblobsdigest)
- [OpenAI-compatible API](#openai-compatible-api)
  - [`POST /v1/chat/completions`](#post-v1chatcompletions)
  - [`POST /v1/completions`](#post-v1completions)
  - [`POST /v1/embeddings`](#post-v1embeddings)
  - [`GET /v1/models`](#get-v1models)
  - [`GET /v1/models/{model}`](#get-v1modelsmodel)

---

## Conventions

| Concept | Value |
| --- | --- |
| Default base URL | `http://127.0.0.1:11434` |
| Content-Type | `application/json` (request bodies are JSON) |
| Streaming format | NDJSON — one JSON object per line |
| Date format | RFC 3339 UTC, e.g. `2024-07-02T12:34:56.789Z` |
| Duration fields | nanoseconds (integer) |
| Digests | `sha256:<hex>` |

All timestamps are UTC. All duration fields (`*_duration`) are integer
nanoseconds. Token counts are integers.

---

## Authentication

None by default. Nova binds to `127.0.0.1` and is intended for local use. To
expose the server on the network, set `NOVA_HOST=0.0.0.0:11434` and
`NOVA_ORIGINS=<your-origins>` and put a reverse proxy with auth in front.

The OpenAI-compatible endpoints accept any `Authorization: Bearer <token>`
header (the token is ignored) so existing OpenAI SDKs work without
modification.

---

## Model name format

Nova model names follow the Ollama convention:

```
[registry/][namespace/]model[:tag]
```

| Form | Resolves to |
| --- | --- |
| `llama3` | `registry.nova.ai/library/llama3:latest` |
| `llama3:8b` | `registry.nova.ai/library/llama3:8b` |
| `quantum/qwen2` | `registry.nova.ai/quantum/qwen2:latest` |
| `quantum/qwen2:7b` | `registry.nova.ai/quantum/qwen2:7b` |
| `myregistry.io/quantum/qwen2:7b` | `myregistry.io/quantum/qwen2:7b` |

When no registry is specified, `registry.nova.ai` is used. When no namespace is
specified, `library` is used. When no tag is specified, `latest` is used.

---

## Errors

Errors are returned as a JSON object:

```json
{ "error": "model not found" }
```

Common status codes:

| Code | Meaning | When |
| --- | --- | --- |
| `200 OK` | Success | Most endpoints |
| `201 Created` | Created | `POST /api/blobs/{digest}` when a new blob is accepted |
| `400 Bad Request` | Malformed request body | Bad JSON, missing required field |
| `404 Not Found` | Model or blob not found | `POST /api/show` for unknown model, `HEAD /api/blobs/{digest}` |
| `405 Method Not Allowed` | Wrong HTTP method | e.g. `GET /api/generate` |
| `409 Conflict` | State conflict | Blob already exists when `POST /api/blobs/{digest}` is used to create |
| `500 Internal Server Error` | Runner failure | The model failed to load or generate |

---

## Nova native API

### `POST /api/generate`

Generate a completion for a single prompt. Optionally streams tokens as they
are produced.

#### Request body

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `model` | string | yes | Model name (see [Model name format](#model-name-format)) |
| `prompt` | string | yes* | The prompt to complete. Required unless `images` alone |
| `suffix` | string | no | Text to append after the completion (insert mode) |
| `system` | string | no | Override the model's system message |
| `template` | string | no | Override the model's prompt template (Go text/template) |
| `context` | []int | no | Token context array returned from a previous call (for multi-turn) |
| `stream` | bool | no | Stream tokens as NDJSON (default `true`) |
| `raw` | bool | no | If `true`, pass `prompt` through without applying the template |
| `format` | string | no | `"json"` to force JSON output, or a JSON schema string |
| `images` | []string | no | Base64-encoded images (multimodal) |
| `keep_alive` | string | no | How long to keep the model loaded after this call (e.g. `"5m"`, `"0"` to unload, `"-1"` forever) |
| `options` | object | no | Per-request inference options (see [Options](#options-object)) |

##### Options object

All Modelfile `PARAMETER` keys are accepted, lowercased:

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `temperature` | float | 0.8 | Sampling temperature |
| `top_k` | int | 40 | Top-K sampling |
| `top_p` | float | 0.9 | Nucleus sampling probability |
| `num_ctx` | int | 4096 | Context window size |
| `num_batch` | int | 512 | Batch size for prompt processing |
| `num_thread` | int | (auto) | CPU threads |
| `num_gpu` | int | (auto) | GPU layers to offload |
| `num_predict` | int | -1 | Max tokens to predict (-1 = unlimited) |
| `repeat_penalty` | float | 1.1 | Repetition penalty |
| `repeat_last_n` | int | 64 | Window for repetition penalty |
| `seed` | int | -1 | RNG seed (-1 = random) |
| `stop` | []string | [] | Stop sequences |
| `mirostat` | int | 0 | Mirostat mode (0, 1, or 2) |
| `mirostat_tau` | float | 5.0 | Mirostat target entropy |
| `mirostat_eta` | float | 0.1 | Mirostat learning rate |
| `penalize_newline` | bool | true | Penalize newlines |
| `num_keep` | int | 0 | Tokens to keep from the prompt |
| `typical_p` | float | 1.0 | Typical-p sampling |
| `presence_penalty` | float | 0.0 | Presence penalty |
| `frequency_penalty` | float | 0.0 | Frequency penalty |
| `f16_kv` | bool | (auto) | Use 16-bit KV cache |
| `use_mlock` | bool | false | Lock model in RAM |
| `use_mmap` | bool | true | Use mmap to load weights |
| `flash_attention` | bool | false | Enable flash attention |

#### Streaming response (NDJSON, one object per token)

```json
{"model":"llama3","created_at":"2024-07-02T12:34:56.789Z","response":"Hello","done":false}
{"model":"llama3","created_at":"2024-07-02T12:34:56.790Z","response":", ","done":false}
{"model":"llama3","created_at":"2024-07-02T12:34:56.900Z","response":"","done":true,"done_reason":"stop","context":[1,2,3],"total_duration":123456789,"load_duration":100000,"prompt_eval_count":5,"prompt_eval_duration":500000,"eval_count":8,"eval_duration":110000000}
```

#### Non-streaming response (`"stream": false`)

```json
{
  "model": "llama3",
  "created_at": "2024-07-02T12:34:56.900Z",
  "response": "Hello, world!",
  "done": true,
  "done_reason": "stop",
  "context": [1, 2, 3, 4, 5],
  "total_duration": 123456789,
  "load_duration": 100000,
  "prompt_eval_count": 5,
  "prompt_eval_duration": 500000,
  "eval_count": 8,
  "eval_duration": 110000000
}
```

#### Example

```bash
curl http://127.0.0.1:11434/api/generate -d '{
  "model": "llama3",
  "prompt": "Why is the sky blue?",
  "stream": false
}'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 400 | Missing `model` or `prompt`; malformed JSON |
| 404 | Model not found |
| 500 | Runner error during generation |

---

### `POST /api/chat`

Generate a chat completion from a list of messages.

#### Request body

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `model` | string | yes | Model name |
| `messages` | []Message | yes | Conversation messages |
| `stream` | bool | no | Stream tokens (default `true`) |
| `format` | string | no | `"json"` or a JSON schema |
| `tools` | []Tool | no | Tool/function definitions the model may call |
| `keep_alive` | string | no | Model keep-alive duration |
| `options` | object | no | Per-request options (see above) |

##### Message object

| Field | Type | Description |
| --- | --- | --- |
| `role` | string | `system`, `user`, `assistant`, or `tool` |
| `content` | string | The message text |
| `images` | []string | Base64-encoded images (multimodal) |
| `tool_calls` | []ToolCall | Tool calls requested by the assistant |

##### Tool object

```json
{
  "type": "function",
  "function": {
    "name": "get_weather",
    "description": "Get the weather for a city",
    "parameters": { "type": "object", "properties": { "city": { "type": "string" } }, "required": ["city"] }
  }
}
```

#### Streaming response (NDJSON)

Each line is a partial message:

```json
{"model":"llama3","created_at":"2024-07-02T12:34:56.789Z","message":{"role":"assistant","content":"Hi"},"done":false}
{"model":"llama3","created_at":"2024-07-02T12:34:56.900Z","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","total_duration":123456789,"eval_count":8}
```

#### Non-streaming response

```json
{
  "model": "llama3",
  "created_at": "2024-07-02T12:34:56.900Z",
  "message": { "role": "assistant", "content": "Hi there!" },
  "done": true,
  "done_reason": "stop",
  "total_duration": 123456789,
  "load_duration": 100000,
  "prompt_eval_count": 12,
  "prompt_eval_duration": 500000,
  "eval_count": 4,
  "eval_duration": 110000000
}
```

#### Example

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

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 400 | Missing `model` or `messages` |
| 404 | Model not found |
| 500 | Runner error |

---

### `POST /api/embeddings`

> **Deprecated alias** of `/api/embed`. Kept for Ollama compatibility.
> Returns a single embedding vector for a single input.

#### Request body

```json
{ "model": "nomic-embed-text", "prompt": "Hello, world!" }
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `model` | string | yes | Embedding model name |
| `prompt` | string | yes | Text to embed |
| `options` | object | no | Per-request options |
| `keep_alive` | string | no | Keep-alive duration |

#### Response

```json
{
  "embedding": [0.123, -0.456, 0.789, /* ... */]
}
```

#### Example

```bash
curl http://127.0.0.1:11434/api/embeddings -d '{
  "model": "nomic-embed-text",
  "prompt": "Hello, world!"
}'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 400 | Missing `model` or `prompt` |
| 404 | Model not found |
| 500 | Runner error |

---

### `POST /api/embed`

Compute embedding vectors for one or more inputs. Supports batched input.

#### Request body

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `model` | string | yes | Embedding model name |
| `input` | string or []string | yes | One string or a list of strings |
| `truncate` | bool | no | Truncate inputs to fit context (default `true`) |
| `options` | object | no | Per-request options |
| `keep_alive` | string | no | Keep-alive duration |

#### Response

```json
{
  "model": "nomic-embed-text",
  "embeddings": [
    [0.123, -0.456, 0.789, /* ... */],
    [0.234, -0.567, 0.890, /* ... */]
  ],
  "total_duration": 123456789,
  "load_duration": 100000,
  "prompt_eval_count": 12
}
```

#### Example

```bash
curl http://127.0.0.1:11434/api/embed -d '{
  "model": "nomic-embed-text",
  "input": ["Hello", "world"]
}'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 400 | Missing `model` or `input` |
| 404 | Model not found |
| 500 | Runner error |

---

### `GET /api/tags`

List all models installed locally.

#### Request

No body. No query parameters.

#### Response

```json
{
  "models": [
    {
      "name": "llama3:latest",
      "model": "llama3:latest",
      "modified_at": "2024-07-02T12:00:00Z",
      "size": 4799994880,
      "digest": "sha256:ac477b39ea8f...",
      "details": {
        "format": "gguf",
        "family": "llama",
        "families": "llama",
        "parameter_size": "8.0B",
        "quantization_level": "Q4_0"
      }
    }
  ]
}
```

#### Example

```bash
curl http://127.0.0.1:11434/api/tags
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success (even if the list is empty) |

---

### `POST /api/show`

Show details about a model: its Modelfile, parameters, template, system
message, license, and details block.

#### Request body

```json
{ "model": "llama3", "name": "llama3" }
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `model` or `name` | string | yes | Model name (either field is accepted) |

#### Response

```json
{
  "license": "MIT",
  "modelfile": "# Modelfile for llama3\nFROM llama3\n...",
  "parameters": "num_keep 24\ntemperature 0.8\ntop_k 40\n...",
  "template": "{{ .System }}\n{{ .Prompt }}",
  "system": "You are a helpful assistant.",
  "details": {
    "format": "gguf",
    "family": "llama",
    "parameter_size": "8.0B",
    "quantization_level": "Q4_0"
  },
  "model_info": {
    "general.architecture": "llama",
    "llama.context_length": 8192
  },
  "modified_at": "2024-07-02T12:00:00Z"
}
```

#### Example

```bash
curl http://127.0.0.1:11434/api/show -d '{ "model": "llama3" }'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 404 | Model not found |

---

### `POST /api/pull`

Pull (download) a model from a registry. Streams progress as NDJSON.

#### Request body

```json
{ "name": "llama3:8b", "insecure": false, "stream": true }
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Model name to pull |
| `insecure` | bool | no | Allow insecure registry connections (default `false`) |
| `stream` | bool | no | Stream progress (default `true`) |

#### Streaming response (NDJSON)

```json
{"status":"pulling manifest"}
{"status":"pulling sha256:ac477b39ea8f...","digest":"sha256:ac477b39ea8f...","total":4799994880,"completed":1000000000}
{"status":"pulling sha256:ac477b39ea8f...","digest":"sha256:ac477b39ea8f...","total":4799994880,"completed":4799994880}
{"status":"verifying sha256 digest"}
{"status":"writing manifest"}
{"status":"success"}
```

#### Example

```bash
curl http://127.0.0.1:11434/api/pull -d '{ "name": "llama3" }'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success (or streaming started) |
| 400 | Missing `name` |
| 404 | Model not found in registry |

---

### `POST /api/push`

Push a local model to a registry. Streams progress as NDJSON.

#### Request body

```json
{ "name": "myorg/mymodel:1.0", "insecure": false, "stream": true }
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Model name to push |
| `insecure` | bool | no | Allow insecure registry connections |
| `stream` | bool | no | Stream progress (default `true`) |

#### Streaming response (NDJSON)

```json
{"status":"retrieving manifest"}
{"status":"pushing sha256:abc...","digest":"sha256:abc...","total":4799994880,"completed":1000000000}
{"status":"success"}
```

#### Example

```bash
curl http://127.0.0.1:11434/api/push -d '{ "name": "myorg/mymodel:1.0" }'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success (or streaming started) |
| 400 | Missing `name` |
| 401 | Registry authentication required (not yet implemented) |
| 404 | Local model not found |

---

### `POST /api/create`

Create a new model from a Modelfile.

#### Request body

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Destination model name |
| `modelfile` | string | no | Modelfile contents (string) |
| `path` | string | no | Path to a Modelfile on disk (alternative to `modelfile`) |
| `stream` | bool | no | Stream progress (default `true`) |
| `quantize` | string | no | Quantization to apply (e.g. `q4_0`) |

Either `modelfile` or `path` must be provided.

#### Streaming response (NDJSON)

```json
{"status":"reading modelfile"}
{"status":"pulling base model"}
{"status":"using existing layer sha256:abc..."}
{"status":"writing manifest"}
{"status":"success"}
```

#### Example

```bash
curl http://127.0.0.1:11434/api/create -d '{
  "name": "myassistant",
  "modelfile": "FROM llama3\nSYSTEM You are a concise assistant."
}'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success (or streaming started) |
| 400 | Missing `name`, both `modelfile` and `path`, or invalid Modelfile |
| 404 | Base model not found |

---

### `DELETE /api/delete`

Delete a model and its manifest. Blobs that are no longer referenced are left
in place (garbage-collected on a future `nova prune`).

#### Request body

```json
{ "name": "llama3:8b" }
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Model name to delete |

#### Response

Empty body, status `200`.

#### Example

```bash
curl -X DELETE http://127.0.0.1:11434/api/delete -d '{ "name": "llama3:8b" }'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 400 | Missing `name` |
| 404 | Model not found |

---

### `POST /api/copy`

Copy an existing model to a new name. The blob layers are shared (copy-on-write
at the manifest level — no data duplication).

#### Request body

```json
{ "source": "llama3:8b", "destination": "myalias:latest" }
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `source` | string | yes | Source model name |
| `destination` | string | yes | Destination model name |

#### Response

Empty body, status `200`.

#### Example

```bash
curl http://127.0.0.1:11434/api/copy -d '{
  "source": "llama3:8b",
  "destination": "myalias:latest"
}'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 400 | Missing `source` or `destination` |
| 404 | Source model not found |
| 409 | Destination already exists |

---

### `GET /api/ps`

List models currently loaded into memory.

#### Request

No body. No query parameters.

#### Response

```json
{
  "models": [
    {
      "name": "llama3:latest",
      "model": "llama3:latest",
      "size": 4799994880,
      "digest": "sha256:ac477b39ea8f...",
      "expires_at": "2024-07-02T12:40:00Z",
      "size_vram": 4799994880,
      "details": { "family": "llama", "parameter_size": "8.0B" }
    }
  ]
}
```

#### Example

```bash
curl http://127.0.0.1:11434/api/ps
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |

---

### `GET /api/version`

Return the running server's version.

#### Request

No body.

#### Response

```json
{ "version": "0.1.0" }
```

#### Example

```bash
curl http://127.0.0.1:11434/api/version
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Always (cheap call) |

---

### `HEAD /api/blobs/{digest}`

Check whether a content-addressed blob is present in the local store.

#### Path parameters

| Name | Format | Description |
| --- | --- | --- |
| `digest` | `sha256:<hex>` | The blob's digest |

#### Response

Empty body. Status `200` if the blob exists, `404` if not.

#### Example

```bash
curl -I http://127.0.0.1:11434/api/blobs/sha256:ac477b39ea8f...
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Blob present |
| 404 | Blob not found |

---

### `POST /api/blobs/{digest}`

Upload a blob to the local store, content-addressed by `digest`. The server
verifies the body's SHA-256 matches the digest in the URL.

#### Path parameters

| Name | Format | Description |
| --- | --- | --- |
| `digest` | `sha256:<hex>` | The expected digest of the uploaded body |

#### Request body

Raw binary (the blob contents). Content-Type is not required.

#### Response

Empty body, status `201` on creation or `200` if the blob already existed.

#### Example

```bash
curl -X POST --data-binary @file.bin \
  http://127.0.0.1:11434/api/blobs/sha256:$(sha256sum file.bin | cut -d' ' -f1)
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Blob already exists |
| 201 | Blob created |
| 400 | Malformed digest |
| 409 | Uploaded body's SHA-256 did not match `digest` |

---

## OpenAI-compatible API

These endpoints translate OpenAI requests into Nova's internal model API.
Point any OpenAI SDK at `http://localhost:11434/v1` with any API key.

### `POST /v1/chat/completions`

OpenAI-compatible chat completions. Supports streaming (SSE) and non-streaming
responses, function/tool calls, and the standard `n`, `temperature`,
`max_tokens`, `top_p`, `stop`, `presence_penalty`, `frequency_penalty`,
`seed`, `response_format` parameters.

#### Request body (subset of OpenAI's schema)

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `model` | string | yes | Nova model name |
| `messages` | []Message | yes | OpenAI-format messages |
| `stream` | bool | no | Use SSE streaming (default `false`) |
| `temperature` | float | no | Sampling temperature |
| `max_tokens` | int | no | Max tokens to generate |
| `top_p` | float | no | Nucleus sampling |
| `n` | int | no | Number of completions (defaults to 1; >1 supported) |
| `stop` | string or []string | no | Stop sequences |
| `presence_penalty` | float | no | Presence penalty |
| `frequency_penalty` | float | no | Frequency penalty |
| `seed` | int | no | RNG seed |
| `response_format` | object | no | `{ "type": "json_object" }` for JSON mode |
| `tools` | []Tool | no | Tool/function definitions |
| `tool_choice` | string or object | no | `"auto"`, `"none"`, or a specific tool |
| `user` | string | no | (ignored) |

#### Non-streaming response

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1719921296,
  "model": "llama3",
  "system_fingerprint": "nova_0.1.0",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! How can I help?"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 12,
    "completion_tokens": 8,
    "total_tokens": 20
  }
}
```

#### Streaming response (SSE — `text/event-stream`)

```
data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","created":1719921296,"model":"llama3","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","created":1719921296,"model":"llama3","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","created":1719921296,"model":"llama3","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":2,"total_tokens":14}}

data: [DONE]
```

#### Example (non-streaming)

```bash
curl http://127.0.0.1:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama3",
    "messages": [
      { "role": "system", "content": "You are concise." },
      { "role": "user",   "content": "Hello!" }
    ]
  }'
```

#### Example (Python SDK)

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:11434/v1", api_key="nova")
resp = client.chat.completions.create(
    model="llama3",
    messages=[{"role":"user","content":"Hello!"}],
)
print(resp.choices[0].message.content)
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 400 | Missing `model` or `messages`; malformed JSON |
| 404 | Model not found |
| 429 | Rate limited (future) |
| 500 | Runner error |

---

### `POST /v1/completions`

OpenAI-compatible legacy text completions.

#### Request body (subset)

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `model` | string | yes | Nova model name |
| `prompt` | string or []string | yes | Prompt(s) to complete |
| `stream` | bool | no | SSE streaming (default `false`) |
| `temperature` | float | no | Sampling temperature |
| `max_tokens` | int | no | Max tokens (default 16, OpenAI default) |
| `top_p` | float | no | Nucleus sampling |
| `n` | int | no | Number of completions |
| `stop` | string or []string | no | Stop sequences |
| `seed` | int | no | RNG seed |

#### Non-streaming response

```json
{
  "id": "cmpl-abc123",
  "object": "text_completion",
  "created": 1719921296,
  "model": "llama3",
  "system_fingerprint": "nova_0.1.0",
  "choices": [
    {
      "text": " world!",
      "index": 0,
      "finish_reason": "stop",
      "logprobs": null
    }
  ],
  "usage": {
    "prompt_tokens": 1,
    "completion_tokens": 2,
    "total_tokens": 3
  }
}
```

#### Example

```bash
curl http://127.0.0.1:11434/v1/completions \
  -H "Content-Type: application/json" \
  -d '{ "model": "llama3", "prompt": "Hello", "stream": false }'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 400 | Missing `model` or `prompt` |
| 404 | Model not found |
| 500 | Runner error |

---

### `POST /v1/embeddings`

OpenAI-compatible embeddings.

#### Request body

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `model` | string | yes | Embedding model name |
| `input` | string or []string or []int or [][]int | yes | One or more inputs |
| `encoding_format` | string | no | `"float"` (default) or `"base64"` |
| `dimensions` | int | no | Truncate embeddings to N dims (future) |

#### Response

```json
{
  "object": "list",
  "data": [
    { "object": "embedding", "index": 0, "embedding": [0.123, -0.456, /* ... */] },
    { "object": "embedding", "index": 1, "embedding": [0.234, -0.567, /* ... */] }
  ],
  "model": "nomic-embed-text",
  "usage": { "prompt_tokens": 4, "total_tokens": 4 }
}
```

#### Example

```bash
curl http://127.0.0.1:11434/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{ "model": "nomic-embed-text", "input": ["Hello", "world"] }'
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 400 | Missing `model` or `input` |
| 404 | Model not found |
| 500 | Runner error |

---

### `GET /v1/models`

OpenAI-compatible model list. Returns every model installed locally.

#### Request

No body.

#### Response

```json
{
  "object": "list",
  "data": [
    {
      "id": "llama3",
      "object": "model",
      "created": 1719921296,
      "owned_by": "library"
    },
    {
      "id": "qwen2:7b",
      "object": "model",
      "created": 1719921296,
      "owned_by": "quantum"
    }
  ]
}
```

#### Example

```bash
curl http://127.0.0.1:11434/v1/models
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |

---

### `GET /v1/models/{model}`

OpenAI-compatible single-model lookup. Returns metadata for one installed
model.

#### Path parameters

| Name | Description |
| --- | --- |
| `model` | Model name (URL-encoded if it contains `:` or `/`) |

#### Response

```json
{
  "id": "llama3",
  "object": "model",
  "created": 1719921296,
  "owned_by": "library"
}
```

#### Example

```bash
curl http://127.0.0.1:11434/v1/models/llama3
```

#### Status codes

| Code | When |
| --- | --- |
| 200 | Success |
| 404 | Model not found |

---

## Compatibility notes

- All `/v1/*` endpoints accept (and ignore) `Authorization` headers so OpenAI
  SDKs work as-is.
- SSE streaming follows the OpenAI convention: `data: <json>\n\n` lines,
  terminated by `data: [DONE]\n\n`.
- NDJSON streaming (on `/api/*` endpoints) is one JSON object per line with no
  blank-line separator.
- The `format: "json"` field on `/api/generate` and `/api/chat` requests a
  best-effort JSON object from the model. The OpenAI equivalent is
  `response_format: { "type": "json_object" }`.
- Tool/function calling is supported on both `/api/chat` and
  `/v1/chat/completions` when the underlying runner implements it.
