package cmd

import (
	"context"
	"fmt"

	"github.com/project-nova/nova/internal/client"
	"github.com/spf13/cobra"
)

var cpCmd = &cobra.Command{
	Use:   "cp SOURCE DEST",
	Short: "Copy a model",
	Long:  `Copy a model from one name to another within the local Nova models directory.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(rootHost)
		if err := cli.Copy(context.Background(), args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("copied '%s' to '%s'\n", args[0], args[1])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cpCmd)
}
