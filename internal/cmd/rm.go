package cmd

import (
	"context"
	"fmt"

	"github.com/project-nova/nova/internal/client"
	"github.com/spf13/cobra"
)

var rmCmd = &cobra.Command{
	Use:   "rm MODEL [MODEL...]",
	Short: "Remove an installed model",
	Long:  `Remove one or more installed models from the local Nova models directory.`,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(rootHost)
		for _, name := range args {
			if err := cli.Delete(context.Background(), name); err != nil {
				return fmt.Errorf("delete %s: %w", name, err)
			}
			fmt.Printf("deleted '%s'\n", name)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(rmCmd)
}
