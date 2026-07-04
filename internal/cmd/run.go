package cmd

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/client"
	"github.com/spf13/cobra"
)

// Flags for the `run` command.
var (
	runVerbose    bool
	runInsecure   bool
	runKeepalive  string
	runNowordwrap bool
	runFormat     string
	runStdin      bool
	runImages     []string
)

var runCmd = &cobra.Command{
	Use:   "run MODEL [PROMPT...]",
	Short: "Run a model",
	Long: `Run a model.

With a prompt argument (or stdin piped in), generate a one-shot completion.
Without a prompt and with a TTY on stdin, drop into an interactive chat REPL.

Supported slash commands inside the REPL:
  /bye, /exit, /quit     Exit the REPL
  /clear                 Clear conversation context
  /show info             Show model information
  /set system TEXT       Override the system prompt
  /set parameter KEY V   Set an inference parameter
  /?, /help              Show help`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		modelName := args[0]
		prompt := strings.Join(args[1:], " ")

		images, err := loadImages(runImages)
		if err != nil {
			return err
		}

		cli := client.New(rootHost)

		// Interactive REPL when no prompt and stdin is a terminal.
		if prompt == "" && !runStdin && isTerminal(os.Stdin) {
			return runREPL(cli, modelName, images)
		}

		// Otherwise consume stdin for the prompt if needed.
		if prompt == "" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			prompt = string(b)
		}
		return runGenerate(cli, modelName, prompt, images)
	},
}

func init() {
	runCmd.Flags().BoolVar(&runVerbose, "verbose", false, "Show timings and other debug info")
	runCmd.Flags().BoolVar(&runInsecure, "insecure", false, "Use an insecure registry")
	runCmd.Flags().StringVar(&runKeepalive, "keepalive", "", "Duration to keep model loaded (e.g. 10m, 0)")
	runCmd.Flags().BoolVar(&runNowordwrap, "nowordwrap", false, "Disable word wrapping in REPL output")
	runCmd.Flags().StringVar(&runFormat, "format", "", "Output format: json or raw")
	runCmd.Flags().BoolVar(&runStdin, "stdin", false, "Read prompt from stdin")
	runCmd.Flags().StringArrayVarP(&runImages, "image", "i", nil, "Path to image file (repeatable)")
	rootCmd.AddCommand(runCmd)
}

// runGenerate performs a one-shot Generate call and streams output to stdout.
func runGenerate(cli *client.Client, model, prompt string, images []string) error {
	ctx := context.Background()
	stream := true
	req := client.GenerateRequest{
		Model:  model,
		Prompt: prompt,
		Stream: &stream,
		Images: images,
	}
	switch runFormat {
	case "json":
		req.Format = "json"
	case "raw":
		req.Raw = true
	}
	if runKeepalive != "" {
		req.KeepAlive = runKeepalive
	}

	return cli.Generate(ctx, req, func(r client.GenerateResponse) error {
		if r.Done {
			if runVerbose {
				fmt.Fprintf(os.Stderr,
					"\n\ntotal duration: %s\nload duration: %s\nprompt eval count: %d\neval count: %d\n",
					dur(r.TotalDuration), dur(r.LoadDuration),
					r.PromptEvalCount, r.EvalCount)
			}
			return nil
		}
		fmt.Print(r.Response)
		return nil
	})
}

// runREPL runs an interactive chat loop. SIGINT during generation aborts the
// in-flight request but stays in the REPL; SIGINT at the prompt exits.
func runREPL(cli *client.Client, model string, images []string) error {
	installSignalHandler()

	fmt.Printf(">>> Send a message (/? for help)\n")
	reader := bufio.NewReader(os.Stdin)

	var (
		messages []client.Message
		system   string
		params   = map[string]any{}
	)

	for {
		fmt.Print(">>> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Println()
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			if done, err := handleSlash(line, cli, model, &system, &params, &messages); err != nil {
				return err
			} else if done {
				return nil
			}
			continue
		}

		msg := client.Message{Role: "user", Content: line}
		if len(images) > 0 {
			msg.Images = images
		}
		messages = append(messages, msg)

		stream := true
		req := client.ChatRequest{
			Model:    model,
			Messages: withSystem(messages, system),
			Stream:   &stream,
			Options:  params,
		}
		if runKeepalive != "" {
			req.KeepAlive = runKeepalive
		}

		var assistant strings.Builder
		err = cli.Chat(context.Background(), req, func(r client.ChatResponse) error {
			if r.Done {
				return nil
			}
			fmt.Print(r.Message.Content)
			assistant.WriteString(r.Message.Content)
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
			continue
		}
		fmt.Println()
		messages = append(messages, client.Message{Role: "assistant", Content: assistant.String()})
	}
}

// withSystem prepends a system message if one is set.
func withSystem(msgs []client.Message, system string) []client.Message {
	if system == "" {
		return msgs
	}
	out := make([]client.Message, 0, len(msgs)+1)
	out = append(out, client.Message{Role: "system", Content: system})
	out = append(out, msgs...)
	return out
}

// handleSlash dispatches a slash command. Returns (done, error) — done=true
// signals the REPL should exit cleanly.
func handleSlash(line string, cli *client.Client, model string, system *string, params *map[string]any, messages *[]client.Message) (bool, error) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false, nil
	}
	switch parts[0] {
	case "/bye", "/exit", "/quit":
		return true, nil
	case "/clear":
		*messages = nil
		fmt.Println("Cleared session context")
	case "/show":
		if len(parts) < 2 {
			fmt.Println("usage: /show info")
			return false, nil
		}
		switch parts[1] {
		case "info":
			resp, err := cli.Show(context.Background(), client.ShowRequest{Name: model})
			if err != nil {
				return false, err
			}
			fmt.Printf("Model:        %s\n", model)
			fmt.Printf("Family:       %s\n", resp.Details.Family)
			fmt.Printf("Parameters:   %s\n", resp.Details.ParameterSize)
			fmt.Printf("Quantization: %s\n", resp.Details.QuantizationLevel)
			fmt.Printf("Format:       %s\n", resp.Details.Format)
		default:
			fmt.Printf("Unknown show target %q (try: info)\n", parts[1])
		}
	case "/set":
		if len(parts) < 3 {
			fmt.Println("usage: /set system TEXT  |  /set parameter KEY VALUE")
			return false, nil
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, parts[0]+" "+parts[1]))
		switch parts[1] {
		case "system":
			*system = rest
			fmt.Println("System message updated.")
		case "parameter":
			idx := strings.IndexAny(rest, " \t")
			if idx < 0 {
				fmt.Println("usage: /set parameter KEY VALUE")
				return false, nil
			}
			key := rest[:idx]
			val := strings.TrimSpace(rest[idx+1:])
			(*params)[key] = parseScalar(val)
			fmt.Printf("Set parameter %s = %s\n", key, val)
		default:
			fmt.Printf("Unknown set target %q (try: system, parameter)\n", parts[1])
		}
	case "/?", "/help":
		fmt.Println("Slash commands:")
		fmt.Println("  /bye, /exit, /quit        Exit the REPL")
		fmt.Println("  /clear                    Clear conversation context")
		fmt.Println("  /show info                Show model information")
		fmt.Println("  /set system TEXT          Override the system prompt")
		fmt.Println("  /set parameter KEY VALUE  Set an inference parameter")
		fmt.Println("  /?, /help                 Show this help")
	default:
		fmt.Printf("Unknown command %q (try /?)\n", parts[0])
	}
	return false, nil
}

// parseScalar interprets a string as int, float, bool, or string.
func parseScalar(s string) any {
	var i int
	if _, err := fmt.Sscanf(s, "%d", &i); err == nil {
		return i
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
		return f
	}
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}

// loadImages reads each image path and returns base64-encoded payloads.
func loadImages(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read image %s: %w", p, err)
		}
		out = append(out, base64.StdEncoding.EncodeToString(b))
	}
	return out, nil
}

// isTerminal reports whether f is a character device (a TTY).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// dur formats a nanosecond duration as a human-readable string.
func dur(nanos int64) string {
	if nanos <= 0 {
		return "0s"
	}
	if nanos < int64(time.Millisecond) {
		return fmt.Sprintf("%dns", nanos)
	}
	if nanos < int64(time.Second) {
		return fmt.Sprintf("%dms", nanos/int64(time.Millisecond))
	}
	return fmt.Sprintf("%.2fs", float64(nanos)/float64(time.Second))
}
