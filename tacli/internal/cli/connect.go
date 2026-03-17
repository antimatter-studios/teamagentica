package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
)

func init() {
	connectCmd := &cobra.Command{
		Use:   "connect <url> [--name <profile>]",
		Short: "Connect to a kernel and save as a profile",
		Args:  cobra.ExactArgs(1),
		RunE:  runConnect,
	}
	connectCmd.Flags().String("name", "default", "profile name")
	connectCmd.Flags().String("email", "", "login email")
	connectCmd.Flags().String("password", "", "login password")
	rootCmd.AddCommand(connectCmd)
}

func runConnect(cmd *cobra.Command, args []string) error {
	url := args[0]
	name, _ := cmd.Flags().GetString("name")
	email, _ := cmd.Flags().GetString("email")
	password, _ := cmd.Flags().GetString("password")

	// Test connectivity first.
	c := client.New(url, "")
	h, err := c.Health()
	if err != nil {
		return fmt.Errorf("cannot reach kernel at %s: %w", url, err)
	}
	fmt.Printf("Connected to %s v%s (%s)\n", h.App, h.Version, url)

	var token string
	if email != "" && password != "" {
		lr, err := c.Login(email, password)
		if err != nil {
			return fmt.Errorf("login failed: %w", err)
		}
		token = lr.Token
		fmt.Println("Authenticated successfully")
	}

	// Save profile — SetProfile merges with existing, preserving kernel config.
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.SetProfile(config.Profile{
		Name:  name,
		URL:   url,
		Token: token,
	})
	cfg.ActiveProfile = name

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Profile %q saved and set as active\n", name)
	return nil
}
