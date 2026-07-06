package cmd

import (
	"context"
	"fmt"

	"github.com/project-nova/nova/internal/client"
	"github.com/spf13/cobra"
)

var (
	showLicense    bool
	showModelfile  bool
	showParameters bool
	showTemplate   bool
	showSystem     bool
)

var showCmd = &cobra.Command{
	Use:   "show MODEL",
	Short: "Show information about a model",
	Long: `Show information about a model.

By default all available metadata is printed. Use one of the --license,
--modelfile, --parameters, --template or --system flags to print only that
field.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(rootHost)
		resp, err := cli.Show(context.Background(), client.ShowRequest{Name: args[0]})
		if err != nil {
			return err
		}

		anyFilter := showLicense || showModelfile || showParameters || showTemplate || showSystem
		if showLicense {
			fmt.Println(resp.License)
		}
		if showModelfile {
			fmt.Println(resp.Modelfile)
		}
		if showParameters {
			fmt.Println(resp.Parameters)
		}
		if showTemplate {
			fmt.Println(resp.Template)
		}
		if showSystem {
			fmt.Println(resp.System)
		}
		if anyFilter {
			return nil
		}

		// Default: print everything.
		fmt.Printf("Model:        %s\n", args[0])
		fmt.Printf("Family:       %s\n", resp.Details.Family)
		fmt.Printf("Parameters:   %s\n", resp.Details.ParameterSize)
		fmt.Printf("Quantization: %s\n", resp.Details.QuantizationLevel)
		fmt.Printf("Format:       %s\n", resp.Details.Format)
		fmt.Println()
		if resp.Modelfile != "" {
			fmt.Println("# Modelfile")
			fmt.Println(resp.Modelfile)
			fmt.Println()
		}
		if resp.Parameters != "" {
			fmt.Println("# Parameters")
			fmt.Println(resp.Parameters)
			fmt.Println()
		}
		if resp.Template != "" {
			fmt.Println("# Template")
			fmt.Println(resp.Template)
			fmt.Println()
		}
		if resp.System != "" {
			fmt.Println("# System")
			fmt.Println(resp.System)
			fmt.Println()
		}
		if resp.License != "" {
			fmt.Println("# License")
			fmt.Println(resp.License)
		}
		return nil
	},
}

func init() {
	showCmd.Flags().BoolVar(&showLicense, "license", false, "Show license only")
	showCmd.Flags().BoolVar(&showModelfile, "modelfile", false, "Show modelfile only")
	showCmd.Flags().BoolVar(&showParameters, "parameters", false, "Show parameters only")
	showCmd.Flags().BoolVar(&showTemplate, "template", false, "Show template only")
	showCmd.Flags().BoolVar(&showSystem, "system", false, "Show system message only")
	rootCmd.AddCommand(showCmd)
}
