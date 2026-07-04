package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/project-nova/nova/internal/client"
	"github.com/spf13/cobra"
)

var pullCmd = &cobra.Command{
	Use:   "pull NAME",
	Short: "Pull a model from a registry",
	Long:  `Pull a model from a registry and install it locally.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(rootHost)
		name := args[0]
		pp := &progressPrinter{}
		err := cli.Pull(context.Background(), name, func(p client.ProgressResponse) error {
			pp.update("pulling", p)
			return nil
		})
		pp.finish()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "success\n")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pullCmd)
}
