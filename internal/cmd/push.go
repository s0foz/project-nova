package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/project-nova/nova/internal/client"
	"github.com/spf13/cobra"
)

var pushCmd = &cobra.Command{
	Use:   "push NAME",
	Short: "Push a model to a registry",
	Long:  `Push a locally-installed model to a registry.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(rootHost)
		name := args[0]
		pp := &progressPrinter{}
		err := cli.Push(context.Background(), name, func(p client.ProgressResponse) error {
			pp.update("pushing", p)
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
	rootCmd.AddCommand(pushCmd)
}
