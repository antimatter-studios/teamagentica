package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/render"
)

// Version is set at build time via -ldflags.
var Version = "dev"

var (
	flagKernel  string
	flagProfile string
	flagConsole bool
	flagJSON    bool
)

var rootCmd = &cobra.Command{
	Use:           "tacli",
	Short:         "Team Agentica CLI — inspect and manage the platform",
	Long:          "tacli connects to Team Agentica for inspection, debugging, and management.",
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagConsole {
			return runConsole(cmd, args)
		}
		return cmd.Help()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagKernel, "kernel", "k", "", "kernel URL (e.g. http://localhost:8080)")
	rootCmd.PersistentFlags().StringVarP(&flagProfile, "profile", "p", "", "named profile from ~/.tacli/config.json")
	rootCmd.Flags().BoolVar(&flagConsole, "console", false, "launch interactive TUI dashboard")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output as JSON")
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// getRenderer returns a text or JSON renderer based on the --json flag.
func getRenderer() render.Renderer {
	if flagJSON {
		return render.NewJSON()
	}
	return render.NewText()
}

// resolveKernelURL figures out which kernel URL to use from flags, profile, or env.
func resolveKernelURL() (string, error) {
	if flagKernel != "" {
		return flagKernel, nil
	}
	if v := os.Getenv("TACLI_KERNEL"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no kernel specified — use --kernel, --profile, or TACLI_KERNEL env")
}
