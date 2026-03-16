package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
)

func init() {
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all profiles",
		RunE:  runProfileList,
	}

	showCmd := &cobra.Command{
		Use:   "show [name]",
		Short: "Show full details of a profile (defaults to active)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runProfileShow,
	}

	profileCmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage tacli profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProfileList(cmd, args)
		},
	}
	profileCmd.AddCommand(listCmd)
	profileCmd.AddCommand(showCmd)
	rootCmd.AddCommand(profileCmd)
}

func runProfileList(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	path := config.ConfigPath()

	if len(cfg.Profiles) == 0 {
		fmt.Println("No profiles — run 'tacli core create' to create one")
		return nil
	}

	fmt.Printf("Config file: %s\n\n", path)
	for _, p := range cfg.Profiles {
		active := "  "
		if p.Name == cfg.ActiveProfile {
			active = "* "
		}
		fmt.Printf("%s%-20s  %s\n", active, p.Name, p.URL)
	}
	return nil
}

func runProfileShow(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	path := config.ConfigPath()

	var name string
	if len(args) > 0 {
		name = args[0]
	} else {
		name = cfg.ActiveProfile
	}

	if name == "" {
		return fmt.Errorf("no active profile and no name given")
	}

	var found *config.Profile
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			found = &cfg.Profiles[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("profile %q not found", name)
	}

	fmt.Printf("File:   %s\n", path)
	fmt.Printf("Name:   %s", found.Name)
	if found.Name == cfg.ActiveProfile {
		fmt.Print(" (active)")
	}
	fmt.Printf("\nURL:    %s\n", found.URL)
	if found.Token != "" {
		fmt.Printf("Token:  %s...%s\n", found.Token[:6], found.Token[len(found.Token)-4:])
	}

	if found.Kernel.Image != "" {
		fmt.Println("\nKernel:")
		out, _ := json.MarshalIndent(found.Kernel, "  ", "  ")
		os.Stdout.Write(out)
		fmt.Println()
	}
	return nil
}
