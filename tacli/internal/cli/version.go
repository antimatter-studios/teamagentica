package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
	"github.com/antimatter-studios/teamagentica/tacli/internal/render"
)

// groupOrder defines display ordering for plugin groups.
var groupOrder = []string{
	"agent",
	"messaging",
	"infra",
	"storage",
	"network",
	"tool",
	"user",
}

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version [filter]",
		Short: "Show version info for plugins (substring match)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runVersion,
	})
}

func runVersion(cmd *cobra.Command, args []string) error {
	r := getRenderer()

	filter := ""
	if len(args) > 0 {
		filter = strings.ToLower(args[0])
	}

	// tacli header — always works, no config needed.
	if filter == "" || strings.Contains("tacli", filter) {
		r.Header("tacli", "Team Agentica CLI — inspect and manage the platform", Version)
	}

	// Everything below requires a kernel connection — degrade gracefully.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "\nCannot read config: %s\nRun 'tacli connect' to set up a profile.\n", config.ConfigPath())
		return r.Flush()
	}
	url, token, err := resolveConnection(cfg)
	if err != nil || token == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "\nNo kernel connection configured.\nConfig: %s\nRun 'tacli connect' to set up a profile.\n", config.ConfigPath())
		return r.Flush()
	}

	c := client.New(url, token)

	// Kernel version.
	if filter == "" || strings.Contains("kernel", filter) {
		h, err := c.Health()
		if err == nil {
			r.GroupStart("Kernel")
			r.Item(render.Fields{"name": h.App, "version": h.Version, "url": url})
		}
	}

	plugins, err := c.ListPlugins()
	if err != nil {
		return r.Flush()
	}

	// Group plugins by ID prefix.
	groups := map[string][]client.PluginSummary{}
	for _, p := range plugins {
		if filter != "" && !strings.Contains(strings.ToLower(p.ID), filter) && !strings.Contains(strings.ToLower(p.Name), filter) {
			continue
		}
		group := pluginGroup(p.ID)
		groups[group] = append(groups[group], p)
	}

	// Display in defined order, then any remaining groups alphabetically.
	printed := map[string]bool{}
	for _, g := range groupOrder {
		if ps, ok := groups[g]; ok {
			renderPluginGroup(r, g, ps)
			printed[g] = true
		}
	}

	var remaining []string
	for g := range groups {
		if !printed[g] {
			remaining = append(remaining, g)
		}
	}
	sort.Strings(remaining)
	for _, g := range remaining {
		renderPluginGroup(r, g, groups[g])
	}

	if len(groups) == 0 && filter != "" && !strings.Contains("kernel", filter) {
		return fmt.Errorf("no plugins matching %q", filter)
	}

	return r.Flush()
}

func renderPluginGroup(r render.Renderer, group string, plugins []client.PluginSummary) {
	label := strings.ToUpper(group[:1]) + group[1:]
	r.GroupStart(label)
	for _, p := range plugins {
		r.Item(render.Fields{
			"name":    p.Name,
			"version": p.Version,
			"image":   p.Image,
			"id":      p.ID,
		})
	}
}

// pluginGroup extracts the group from a plugin ID like "agent-claude" → "agent".
func pluginGroup(id string) string {
	if idx := strings.Index(id, "-"); idx > 0 {
		return id[:idx]
	}
	return "other"
}
