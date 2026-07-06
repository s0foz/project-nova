package cmd

import (
	"fmt"

	"github.com/project-nova/nova/internal/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the Nova version",
	Long:  `Print the Nova version, commit and build date.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version.Info())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
