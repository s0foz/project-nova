package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/project-nova/nova/internal/client"
	"github.com/spf13/cobra"
)

var psJSON bool

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running models",
	Long:  `List models currently loaded in memory by the Nova server.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(rootHost)
		resp, err := cli.Running(context.Background())
		if err != nil {
			return err
		}
		if psJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tID\tSIZE\tPROCESSOR\tUNTIL")
		for _, m := range resp.Models {
			until := "forever"
			if !m.ExpiresAt.IsZero() {
				d := time.Until(m.ExpiresAt).Round(time.Second)
				if d > 0 {
					until = d.String()
				} else {
					until = "expired"
				}
			}
			processor := "100% CPU"
			if m.SizeVRAM > 0 {
				processor = "100% GPU"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				m.Name, shortID(m.Digest), humanSize(m.Size), processor, until)
		}
		return tw.Flush()
	},
}

func init() {
	psCmd.Flags().BoolVar(&psJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(psCmd)
}
