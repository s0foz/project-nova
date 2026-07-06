// Package llmcpp implements the llm.Runner interface by spawning llama.cpp's
// `llama-server` binary as a subprocess and proxying HTTP requests to its
// OpenAI-compatible API.
//
// # Binary discovery
//
// FindServer searches for the llama-server binary in this order:
//
//  1. NOVA_LLAMA_SERVER_PATH environment variable (if set and the file exists).
//  2. Alongside the current executable (./llama-server or ./llama-server.exe).
//  3. In a `bin` subdirectory alongside the current executable
//     (./bin/llama-server or ./bin/llama-server.exe).
//  4. The system PATH (exec.LookPath for "llama-server" and "llama-server.exe").
//
// Set NOVA_LLAMA_SERVER_PATH to the absolute path of a llama-server binary to
// override discovery. If no binary is found, Available() returns false and the
// Nova server falls back to the in-process stub runner (see package llm).
//
// # Extra args
//
// NOVA_LLAMA_SERVER_ARGS, if set, is split on whitespace (strings.Fields) and
// appended verbatim to the llama-server command line. Use it to pass flags Nova
// does not know about (e.g. "--rope-scaling 1 --rope-freq-scale 0.5") or to
// override Nova's defaults.
//
// # Lifecycle
//
// Load spawns the subprocess, allocates a free TCP port on 127.0.0.1, redirects
// the child's stdout/stderr to <models>/logs/llama-server-<port>.log, and polls
// GET /health every 200ms until it returns 200 OK (or 60s elapse). Generate,
// Chat, Embed, Tokenize, and Detokenize proxy to the corresponding llama-server
// endpoints. Close kills the subprocess and closes the log file. The runner
// uses Process.Kill (not a Unix-only signal) so it is portable to Windows.
//
// # Fallback
//
// Without llama-server installed, Nova falls back to the stub runner in package
// llm (which echoes its input). To get real AI text, build or install
// llama.cpp's llama-server and either put it on PATH or set
// NOVA_LLAMA_SERVER_PATH.
package llmcpp
