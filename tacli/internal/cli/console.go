package cli

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
	"github.com/antimatter-studios/teamagentica/tacli/internal/console"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "console",
		Short: "Launch interactive TUI dashboard",
		RunE:  runConsole,
	})
}

func runConsole(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	url, token, err := resolveConnection(cfg)
	if err != nil {
		return err
	}

	c := client.New(url, token)

	p := tea.NewProgram(
		console.New(c),
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("console error: %w", err)
	}

	return nil
}
