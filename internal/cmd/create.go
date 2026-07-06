package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/project-nova/nova/internal/client"
	"github.com/spf13/cobra"
)

var (
	createFile     string
	createQuantize string
)

var createCmd = &cobra.Command{
	Use:   "create NAME [-f Modelfile]",
	Short: "Create a model from a Modelfile",
	Long: `Create a model from a Modelfile.

Reads the Modelfile from the path given by -f (default "Modelfile"), or from
stdin when -f is "-". Streams build progress to stderr.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		var src io.Reader
		if createFile == "-" {
			src = os.Stdin
		} else {
			f, err := os.Open(createFile)
			if err != nil {
				return fmt.Errorf("open modelfile %s: %w", createFile, err)
			}
			defer f.Close()
			src = f
		}
		b, err := io.ReadAll(src)
		if err != nil {
			return fmt.Errorf("read modelfile: %w", err)
		}

		stream := true
		req := client.CreateRequest{
			Name:      name,
			Modelfile: string(b),
			Quantize:  createQuantize,
			Stream:    &stream,
		}

		cli := client.New(rootHost)
		pp := &progressPrinter{}
		err = cli.Create(context.Background(), req, func(p client.ProgressResponse) error {
			pp.update("creating", p)
			return nil
		})
		pp.finish()
		if err != nil {
			return err
		}
		fmt.Printf("Created %s\n", name)
		return nil
	},
}

func init() {
	createCmd.Flags().StringVarP(&createFile, "file", "f", "Modelfile", "Path to Modelfile (or - for stdin)")
	createCmd.Flags().StringVar(&createQuantize, "quantize", "", "Quantization level (e.g. q4_0)")
	rootCmd.AddCommand(createCmd)
}
