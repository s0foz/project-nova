package model

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Modelfile is the parsed representation of a Modelfile (a Dockerfile-like
// description of how to assemble a Nova model). It mirrors Ollama's grammar.
type Modelfile struct {
	// Base model to inherit from (FROM directive).
	From string
	// Adapter files (ADAPTER directive) — LoRA / fine-tune adapters.
	Adapters []string
	// Template is the Go text/template used to render prompts.
	Template string
	// License text appended to the model (LICENSE directive).
	License string
	// System message injected into the chat (SYSTEM directive).
	System string
	// Params are inference parameters (PARAMETER directive).
	Params Parameters
	// Messages are explicit chat messages (MESSAGE directive).
	Messages []Message
	// Raw is the unparsed source text, kept for round-tripping.
	Raw string
}

// Parameters holds inference-time options. Floats are kept as pointers so we
// can distinguish "unset" from "zero".
type Parameters struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopK             *int     `json:"top_k,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	NumCtx           *int     `json:"num_ctx,omitempty"`
	NumBatch         *int     `json:"num_batch,omitempty"`
	NumThread        *int     `json:"num_thread,omitempty"`
	NumGpu           *int     `json:"num_gpu,omitempty"`
	NumPredict       *int     `json:"num_predict,omitempty"`
	RepeatPenalty    *float64 `json:"repeat_penalty,omitempty"`
	RepeatLastN      *int     `json:"repeat_last_n,omitempty"`
	Seed             *int     `json:"seed,omitempty"`
	Stop             []string `json:"stop,omitempty"`
	Mirostat         *int     `json:"mirostat,omitempty"`
	MirostatTau      *float64 `json:"mirostat_tau,omitempty"`
	MirostatEta      *float64 `json:"mirostat_eta,omitempty"`
	PenalizeNewline  *bool    `json:"penalize_newline,omitempty"`
	F16KV            *bool    `json:"f16_kv,omitempty"`
	LowVram          *bool    `json:"low_vram,omitempty"`
	UseMLock         *bool    `json:"use_mlock,omitempty"`
	UseMMap          *bool    `json:"use_mmap,omitempty"`
	VocabOnly        *bool    `json:"vocab_only,omitempty"`
	FlashAttention   *bool    `json:"flash_attention,omitempty"`
	NumKeep          *int     `json:"num_keep,omitempty"`
	TypicalP         *float64 `json:"typical_p,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
}

// Message is a single chat message seeded into the model context.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ParseModelfile reads a Modelfile from an io.Reader.
func ParseModelfile(r io.Reader) (*Modelfile, error) {
	mf := &Modelfile{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var raw strings.Builder
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw.WriteString(scanner.Text())
		raw.WriteByte('\n')

		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split into directive + argument on the first whitespace run.
		idx := strings.IndexAny(line, " \t")
		var directive, rest string
		if idx < 0 {
			directive = strings.ToUpper(line)
		} else {
			directive = strings.ToUpper(line[:idx])
			rest = strings.TrimSpace(line[idx:])
		}

		switch directive {
		case "FROM":
			mf.From = unquote(rest)
		case "ADAPTER":
			mf.Adapters = append(mf.Adapters, unquote(rest))
		case "TEMPLATE":
			mf.Template = unquote(rest)
		case "LICENSE":
			mf.License = unquote(rest)
		case "SYSTEM":
			mf.System = unquote(rest)
		case "PARAMETER":
			if err := applyParameter(mf, rest); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
		case "MESSAGE":
			if err := applyMessage(mf, rest); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
		case "TEMPLATE_EOF", "SYSTEM_EOF", "LICENSE_EOF":
			// multi-line heredoc terminators handled below
		default:
			// Tolerate unknown directives but record them in Raw for round-trip.
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	mf.Raw = raw.String()
	return mf, nil
}

// ParseModelfileFile is a convenience wrapper for opening a file by path.
func ParseModelfileFile(path string) (*Modelfile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseModelfile(f)
}

func applyParameter(mf *Modelfile, rest string) error {
	idx := strings.IndexAny(rest, " \t")
	if idx < 0 {
		return fmt.Errorf("PARAMETER requires a key and value: %q", rest)
	}
	key := strings.ToLower(strings.TrimSpace(rest[:idx]))
	val := strings.TrimSpace(rest[idx+1:])
	val = unquote(val)

	switch key {
	case "temperature":
		f, err := parseFloat(val)
		if err != nil {
			return err
		}
		mf.Params.Temperature = &f
	case "top_k":
		i, err := parseInt(val)
		if err != nil {
			return err
		}
		mf.Params.TopK = &i
	case "top_p":
		f, err := parseFloat(val)
		if err != nil {
			return err
		}
		mf.Params.TopP = &f
	case "num_ctx":
		i, err := parseInt(val)
		if err != nil {
			return err
		}
		mf.Params.NumCtx = &i
	case "num_batch":
		i, err := parseInt(val)
		if err != nil {
			return err
		}
		mf.Params.NumBatch = &i
	case "num_thread":
		i, err := parseInt(val)
		if err != nil {
			return err
		}
		mf.Params.NumThread = &i
	case "num_gpu":
		i, err := parseInt(val)
		if err != nil {
			return err
		}
		mf.Params.NumGpu = &i
	case "num_predict":
		i, err := parseInt(val)
		if err != nil {
			return err
		}
		mf.Params.NumPredict = &i
	case "repeat_penalty":
		f, err := parseFloat(val)
		if err != nil {
			return err
		}
		mf.Params.RepeatPenalty = &f
	case "seed":
		i, err := parseInt(val)
		if err != nil {
			return err
		}
		mf.Params.Seed = &i
	case "stop":
		mf.Params.Stop = append(mf.Params.Stop, val)
	case "mirostat":
		i, err := parseInt(val)
		if err != nil {
			return err
		}
		mf.Params.Mirostat = &i
	case "mirostat_tau":
		f, err := parseFloat(val)
		if err != nil {
			return err
		}
		mf.Params.MirostatTau = &f
	case "mirostat_eta":
		f, err := parseFloat(val)
		if err != nil {
			return err
		}
		mf.Params.MirostatEta = &f
	default:
		// Unknown parameter keys are tolerated (forward compatibility).
	}
	return nil
}

func applyMessage(mf *Modelfile, rest string) error {
	idx := strings.IndexAny(rest, " \t")
	if idx < 0 {
		return fmt.Errorf("MESSAGE requires a role and content: %q", rest)
	}
	role := strings.ToLower(strings.TrimSpace(rest[:idx]))
	content := strings.TrimSpace(rest[idx+1:])
	content = unquote(content)
	switch role {
	case "system", "user", "assistant", "tool":
	default:
		return fmt.Errorf("unknown message role %q", role)
	}
	mf.Messages = append(mf.Messages, Message{Role: role, Content: content})
	return nil
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func parseFloat(s string) (float64, error) {
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0, fmt.Errorf("invalid float %q: %w", s, err)
	}
	return f, nil
}

func parseInt(s string) (int, error) {
	var i int
	if _, err := fmt.Sscanf(s, "%d", &i); err != nil {
		return 0, fmt.Errorf("invalid int %q: %w", s, err)
	}
	return i, nil
}
