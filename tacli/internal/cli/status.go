package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
	"github.com/antimatter-studios/teamagentica/tacli/internal/render"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show kernel status and connected plugins",
		RunE:  runStatus,
	})
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	url, token, err := resolveConnection(cfg)
	if err != nil {
		return err
	}

	c := client.New(url, token)
	r := getRenderer()

	// Health check.
	h, err := c.Health()
	if err != nil {
		return fmt.Errorf("kernel unreachable: %w", err)
	}

	r.GroupStart("Kernel")
	r.Item(render.Fields{"name": h.App, "version": h.Version, "url": url, "status": "connected", "enabled": "true"})

	// Plugins (requires auth).
	if token == "" {
		return r.Flush()
	}

	plugins, err := c.ListPlugins()
	if err != nil {
		return r.Flush()
	}

	if len(plugins) > 0 {
		r.GroupStart("Plugins")
		for _, p := range plugins {
			r.Item(render.Fields{
				"name":    p.Name,
				"status":  p.Status,
				"enabled": strconv.FormatBool(p.Enabled),
				"id":      p.ID,
			})
		}
	}

	return r.Flush()
}

// resolveConnection gets URL and token from flags or active profile.
func resolveConnection(cfg *config.Config) (string, string, error) {
	if flagKernel != "" {
		return flagKernel, "", nil
	}

	profileName := flagProfile
	if profileName == "" {
		profileName = cfg.ActiveProfile
	}

	if profileName != "" {
		for _, p := range cfg.Profiles {
			if p.Name == profileName {
				return p.URL, p.Token, nil
			}
		}
		return "", "", fmt.Errorf("profile %q not found", profileName)
	}

	url, err := resolveKernelURL()
	return url, "", err
}
