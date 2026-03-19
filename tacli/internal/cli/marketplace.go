package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
	"github.com/antimatter-studios/teamagentica/tacli/internal/render"
)

func init() {
	cmd := &cobra.Command{
		Use:   "marketplace",
		Short: "Manage marketplace sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List configured marketplaces",
			RunE:  runMarketplaceList,
		},
		&cobra.Command{
			Use:   "add <url>",
			Short: "Add a marketplace by URL",
			Args:  cobra.ExactArgs(1),
			RunE:  runMarketplaceAdd,
		},
		&cobra.Command{
			Use:   "remove <name>",
			Short: "Remove a marketplace by name",
			Args:  cobra.ExactArgs(1),
			RunE:  runMarketplaceRemove,
		},
		&cobra.Command{
			Use:   "install <plugin-id>",
			Short: "Install a plugin by ID from the marketplace",
			Args:  cobra.ExactArgs(1),
			RunE:  runMarketplaceInstall,
		},
		&cobra.Command{
			Use:   "upgrade <plugin-id>",
			Short: "Upgrade an installed plugin's metadata from the marketplace",
			Args:  cobra.ExactArgs(1),
			RunE:  runMarketplaceUpgrade,
		},
		&cobra.Command{
			Use:   "plugins [marketplace-name]",
			Short: "List available plugins",
			Args:  cobra.MaximumNArgs(1),
			RunE:  runMarketplacePlugins,
		},
		&cobra.Command{
			Use:   "submit <path>",
			Short: "Submit plugin.yaml file(s) — accepts a single file or a directory of plugins",
			Args:  cobra.ExactArgs(1),
			RunE:  runMarketplaceSubmitCatalog,
		},
	)

	rootCmd.AddCommand(cmd)
}

func resolveMarketplaceClient(cmd *cobra.Command) (*client.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	kernelURL, token, err := resolveConnection(cfg)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("authentication required — run tacli connect with --email/--password first")
	}
	return client.New(kernelURL, token), nil
}

func runMarketplaceList(cmd *cobra.Command, args []string) error {
	c, err := resolveMarketplaceClient(cmd)
	if err != nil {
		return err
	}
	return marketplaceList(c)
}

func runMarketplaceAdd(cmd *cobra.Command, args []string) error {
	c, err := resolveMarketplaceClient(cmd)
	if err != nil {
		return err
	}
	return marketplaceAdd(c, args[0])
}

func runMarketplaceRemove(cmd *cobra.Command, args []string) error {
	c, err := resolveMarketplaceClient(cmd)
	if err != nil {
		return err
	}
	return marketplaceRemove(c, args[0])
}

func runMarketplaceInstall(cmd *cobra.Command, args []string) error {
	c, err := resolveMarketplaceClient(cmd)
	if err != nil {
		return err
	}
	return marketplaceInstall(c, args[0])
}

func runMarketplaceUpgrade(cmd *cobra.Command, args []string) error {
	c, err := resolveMarketplaceClient(cmd)
	if err != nil {
		return err
	}
	return marketplaceUpgrade(c, args[0])
}

func runMarketplacePlugins(cmd *cobra.Command, args []string) error {
	c, err := resolveMarketplaceClient(cmd)
	if err != nil {
		return err
	}
	var providerName string
	if len(args) > 0 {
		providerName = args[0]
	}
	return marketplacePlugins(c, providerName)
}

func runMarketplaceSubmitCatalog(cmd *cobra.Command, args []string) error {
	c, err := resolveMarketplaceClient(cmd)
	if err != nil {
		return err
	}
	return marketplaceSubmitCatalog(c, args[0])
}

func marketplaceAdd(c *client.Client, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid URL: %s", rawURL)
	}

	name := u.Host
	prov, err := c.AddProvider(name, rawURL)
	if err != nil {
		return err
	}

	fmt.Printf("Added marketplace %q (%s) [id=%d]\n", prov.Name, prov.URL, prov.ID)
	return nil
}

func marketplaceRemove(c *client.Client, name string) error {
	providers, err := c.ListProviders()
	if err != nil {
		return err
	}

	for _, p := range providers {
		if p.Name == name {
			if p.System {
				return fmt.Errorf("cannot remove %q — it is a system marketplace required for platform operation", name)
			}
			if err := c.DeleteProvider(strconv.FormatUint(uint64(p.ID), 10)); err != nil {
				return err
			}
			fmt.Printf("Removed marketplace %q (installed plugins unchanged)\n", name)
			return nil
		}
	}

	return fmt.Errorf("marketplace %q not found", name)
}

func marketplaceUpgrade(c *client.Client, pluginID string) error {
	fmt.Printf("Upgrading %s from marketplace...\n", pluginID)

	// Capture current version before upgrading so we can show the before/after.
	var oldVersion string
	if current, err := c.GetPlugin(pluginID); err == nil {
		oldVersion = current.Version
	}

	plugin, err := c.UpgradePlugin(pluginID)
	if err != nil {
		return fmt.Errorf("upgrade %s: %w", pluginID, err)
	}

	if oldVersion != "" && oldVersion != plugin.Version {
		fmt.Printf("Upgraded %s (%s → %s)\n", plugin.ID, oldVersion, plugin.Version)
	} else {
		fmt.Printf("Upgraded %s (version %s)\n", plugin.ID, plugin.Version)
	}
	return nil
}

func marketplaceInstall(c *client.Client, pluginID string) error {
	fmt.Printf("Installing %s from marketplace...\n", pluginID)
	result, err := c.InstallPlugin(pluginID)
	if err != nil {
		return fmt.Errorf("install %s: %w", pluginID, err)
	}
	fmt.Printf("Installed %s (version %s)\n", result.Plugin.Name, result.Plugin.Version)
	for _, p := range result.Installed {
		if p.ID != pluginID {
			fmt.Printf("  + installed dependency: %s (version %s)\n", p.Name, p.Version)
		}
	}
	fmt.Println("Use 'tacli plugin enable' to enable it")
	return nil
}

// enrichWithInstalled adds status and system fields to a catalog row based on installed state.
func enrichWithInstalled(fields render.Fields, installed map[string]client.PluginSummary, pluginID string) {
	p, ok := installed[pluginID]
	if !ok {
		return
	}
	if !p.Enabled {
		fields["status"] = "installed"
	} else if p.Status == "running" {
		fields["status"] = "running"
	} else {
		fields["status"] = "enabled"
	}
	if p.System {
		fields["system"] = "true"
	}
}

func marketplacePlugins(c *client.Client, providerName string) error {
	if providerName != "" {
		return marketplacePluginsFromProvider(c, providerName)
	}

	// No provider specified — list all plugins from all providers.
	result, err := c.BrowsePlugins()
	if err != nil {
		return err
	}

	for _, e := range result.Errors {
		fmt.Printf("Warning: %s\n", e)
	}

	if len(result.Plugins) == 0 {
		if len(result.Errors) > 0 {
			return fmt.Errorf("no plugins returned — marketplace providers may be offline")
		}
		fmt.Println("No plugins available in any marketplace")
		return nil
	}

	installed := fetchInstalledMap(c)

	r := getRenderer()
	r.GroupStart("Available Plugins")
	for _, p := range result.Plugins {
		fields := render.Fields{
			"name":    p.Name,
			"id":      p.PluginID,
			"version": p.Version,
		}
		enrichWithInstalled(fields, installed, p.PluginID)
		r.Item(fields)
	}
	return r.Flush()
}

func marketplacePluginsFromProvider(c *client.Client, providerName string) error {
	plugins, err := c.ProviderPlugins(providerName)
	if err != nil {
		// If provider not found, list available names to help the user.
		if providers, lErr := c.ListProviders(); lErr == nil && len(providers) > 0 {
			fmt.Printf("Marketplace %q not found. Available marketplaces:\n", providerName)
			for _, p := range providers {
				fmt.Printf("  - %s\n", p.Name)
			}
			return nil
		}
		return fmt.Errorf("plugins from %s: %w", providerName, err)
	}
	if len(plugins) == 0 {
		fmt.Printf("No plugins found in marketplace %q\n", providerName)
		return nil
	}

	installed := fetchInstalledMap(c)

	r := getRenderer()
	r.GroupStart(fmt.Sprintf("Plugins from %s", providerName))
	for _, p := range plugins {
		fields := render.Fields{
			"name":    p.Name,
			"id":      p.PluginID,
			"version": p.Version,
		}
		enrichWithInstalled(fields, installed, p.PluginID)
		r.Item(fields)
	}
	return r.Flush()
}

// fetchInstalledMap returns a map of plugin ID → PluginSummary for all installed plugins.
func fetchInstalledMap(c *client.Client) map[string]client.PluginSummary {
	plugins, err := c.ListPlugins()
	if err != nil {
		return nil
	}
	m := make(map[string]client.PluginSummary, len(plugins))
	for _, p := range plugins {
		m[p.ID] = p
	}
	return m
}

func marketplaceSubmitCatalog(c *client.Client, path string) error {
	var matches []string

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	if !info.IsDir() {
		// Single file supplied directly.
		matches = []string{path}
	} else {
		matches, err = filepath.Glob(filepath.Join(path, "*", "plugin.yaml"))
		if err != nil {
			return fmt.Errorf("glob: %w", err)
		}
		if len(matches) == 0 {
			return fmt.Errorf("no plugin.yaml files found in %s", path)
		}
	}

	var submitted, errors int
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("  skip %s: %v\n", path, err)
			errors++
			continue
		}

		var manifest map[string]interface{}
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			fmt.Printf("  skip %s: bad yaml: %v\n", path, err)
			errors++
			continue
		}

		id, _ := manifest["id"].(string)
		version, _ := manifest["version"].(string)
		if id == "" || version == "" {
			fmt.Printf("  skip %s: missing id or version\n", path)
			errors++
			continue
		}

		body, _ := json.Marshal(manifest)
		if err := c.SubmitManifest(body); err != nil {
			fmt.Printf("  fail %s: %v\n", id, err)
			errors++
			continue
		}

		submitted++
		fmt.Printf("  submitted %s@%s\n", id, version)
	}

	fmt.Printf("\n%d manifest(s) submitted, %d error(s)\n", submitted, errors)
	if errors > 0 {
		return fmt.Errorf("%d manifest(s) failed", errors)
	}
	return nil
}

func marketplaceList(c *client.Client) error {
	providers, err := c.ListProviders()
	if err != nil {
		return err
	}

	if len(providers) == 0 {
		fmt.Println("No marketplaces configured")
		return nil
	}

	r := getRenderer()
	r.GroupStart("Marketplaces")
	for _, p := range providers {
		r.Item(render.Fields{
			"name":    p.Name,
			"url":     p.URL,
			"enabled": strconv.FormatBool(p.Enabled),
			"system":  strconv.FormatBool(p.System),
		})
	}
	return r.Flush()
}
