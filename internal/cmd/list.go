package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/project-nova/nova/internal/client"
	"github.com/spf13/cobra"
)

var listJSON bool

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List installed models",
	Long:    `List the models installed locally under the Nova models directory.`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(rootHost)
		resp, err := cli.List(context.Background())
		if err != nil {
			return err
		}
		if listJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tID\tSIZE\tMODIFIED")
		for _, m := range resp.Models {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				m.Name, shortID(m.Digest), humanSize(m.Size),
				m.ModifiedAt.Local().Format(time.RFC3339))
		}
		return tw.Flush()
	},
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(listCmd)
}

// shortID trims a "sha256:..." digest to a short, git-style prefix.
func shortID(digest string) string {
	const prefix = "sha256:"
	digest = strings.TrimPrefix(digest, prefix)
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

// humanSize renders a byte count as a human-friendly size string.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// progressPrinter renders streaming pull/push/create progress to stderr.
// It reuses a single line via carriage return while a stage is in progress,
// and emits a newline when the stage label changes.
type progressPrinter struct {
	lastLabel  string
	lastStatus string
	hasLine    bool
}

// update renders one progress line. label is the verb ("pulling"/"pushing").
func (pp *progressPrinter) update(label string, p client.ProgressResponse) {
	if pp.hasLine && (p.Status != pp.lastStatus || label != pp.lastLabel) {
		fmt.Fprintln(os.Stderr)
		pp.hasLine = false
	}
	pp.lastLabel = label
	pp.lastStatus = p.Status
	if p.Total > 0 {
		fmt.Fprintf(os.Stderr, "\r%s %s: %s / %s",
			label, p.Status, humanSize(p.Completed), humanSize(p.Total))
	} else {
		fmt.Fprintf(os.Stderr, "\r%s %s", label, p.Status)
	}
	pp.hasLine = true
}

// finish flushes any in-flight progress line with a trailing newline.
func (pp *progressPrinter) finish() {
	if pp.hasLine {
		fmt.Fprintln(os.Stderr)
		pp.hasLine = false
	}
}
