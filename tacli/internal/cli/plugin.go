package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
	"github.com/antimatter-studios/teamagentica/tacli/internal/render"
)

func init() {
	cmd := &cobra.Command{
		Use:     "plugin",
		Aliases: []string{"plugins"},
		Short:   "Manage plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newPluginClient()
			if err != nil {
				return err
			}
			return runPluginList(c)
		},
	}

	// enable
	enableCmd := &cobra.Command{
		Use:   "enable [id]",
		Short: "Enable a plugin, or all plugins with --all",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runPluginEnable,
	}
	enableCmd.Flags().Bool("all", false, "enable all plugins")

	// disable
	disableCmd := &cobra.Command{
		Use:   "disable [id]",
		Short: "Disable a plugin, or all plugins with --all",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runPluginDisable,
	}
	disableCmd.Flags().Bool("all", false, "disable all plugins")
	disableCmd.Flags().BoolP("force", "f", false, "skip confirmation prompt")

	// restart
	restartCmd := &cobra.Command{
		Use:   "restart [id]",
		Short: "Restart a plugin, or all enabled plugins with --all",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runPluginRestart,
	}
	restartCmd.Flags().Bool("all", false, "restart all enabled plugins")

	// uninstall
	uninstallCmd := &cobra.Command{
		Use:   "uninstall [id]",
		Short: "Uninstall a plugin, or all plugins with --all",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runPluginUninstall,
	}
	uninstallCmd.Flags().Bool("all", false, "uninstall all plugins and remove all configuration")
	uninstallCmd.Flags().BoolP("force", "f", false, "skip confirmation prompt")

	// config
	configCmd := &cobra.Command{
		Use:   "config <id>",
		Short: "Show or update plugin configuration",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runPluginConfig,
	}
	configSetCmd := &cobra.Command{
		Use:   "set <id> KEY=VALUE [KEY=VALUE ...]",
		Short: "Set plugin configuration values",
		Args:  cobra.MinimumNArgs(2),
		RunE:  runPluginConfigSet,
	}
	configGetCmd := &cobra.Command{
		Use:   "get <id> [KEY ...]",
		Short: "Get plugin configuration values",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runPluginConfigGet,
	}
	configCmd.AddCommand(configSetCmd, configGetCmd)

	// schema
	schemaCmd := &cobra.Command{
		Use:   "schema <id>",
		Short: "Show plugin schema as JSON",
		Args:  cobra.ExactArgs(1),
		RunE:  runPluginSchema,
	}

	// list
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all installed plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newPluginClient()
			if err != nil {
				return err
			}
			return runPluginList(c)
		},
	}

	// candidate
	candidateCmd := &cobra.Command{
		Use:   "candidate <id>",
		Short: "Deploy a candidate container alongside the stable",
		Args:  cobra.ExactArgs(1),
		RunE:  runPluginCandidate,
	}
	candidateCmd.Flags().String("image", "", "image to deploy as candidate (default: stable image)")

	// promote
	promoteCmd := &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote the candidate to stable",
		Args:  cobra.ExactArgs(1),
		RunE:  runPluginPromote,
	}

	// rollback
	rollbackCmd := &cobra.Command{
		Use:   "rollback <id>",
		Short: "Discard candidate or restore previous stable",
		Args:  cobra.ExactArgs(1),
		RunE:  runPluginRollback,
	}

	cmd.AddCommand(listCmd, enableCmd, disableCmd, restartCmd, uninstallCmd, configCmd, schemaCmd, candidateCmd, promoteCmd, rollbackCmd)
	rootCmd.AddCommand(cmd)
}

func newPluginClient() (*client.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	kernelURL, token, err := resolveConnection(cfg)
	if err != nil {
		return nil, err
	}
	return client.New(kernelURL, token), nil
}

func resolvePluginClient(cmd *cobra.Command) (*client.Client, error) {
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

func runPluginEnable(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	all, _ := cmd.Flags().GetBool("all")
	if all {
		return runPluginBatch(c, true)
	}
	if len(args) == 0 {
		return fmt.Errorf("specify a plugin id or use --all")
	}
	pluginID := args[0]
	result, err := c.EnablePlugin(pluginID)
	if err != nil {
		return fmt.Errorf("enable %s: %w", pluginID, err)
	}
	fmt.Printf("Enabled %s\n", pluginID)
	for _, id := range result.Enabled {
		if id != pluginID {
			fmt.Printf("  + enabled dependency: %s\n", id)
		}
	}
	return nil
}

func runPluginDisable(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	all, _ := cmd.Flags().GetBool("all")
	force, _ := cmd.Flags().GetBool("force")
	if all {
		if !force {
			plugins, err := c.ListPlugins()
			if err != nil {
				return err
			}
			var enabled int
			for _, p := range plugins {
				if p.Enabled {
					enabled++
				}
			}
			if enabled > 0 {
				if err := requireConfirmation(
					fmt.Sprintf("This will disable all %d enabled plugin(s) — the cluster will be effectively offline.", enabled),
				); err != nil {
					return err
				}
			}
		}
		return runPluginBatch(c, false)
	}
	if len(args) == 0 {
		return fmt.Errorf("specify a plugin id or use --all")
	}
	pluginID := args[0]
	if err := c.DisablePlugin(pluginID); err != nil {
		return fmt.Errorf("disable %s: %w", pluginID, err)
	}
	fmt.Printf("Plugin %s disabled\n", pluginID)
	return nil
}

func runPluginRestart(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	all, _ := cmd.Flags().GetBool("all")
	if all {
		return runPluginRestartAll(c)
	}
	if len(args) == 0 {
		return fmt.Errorf("specify a plugin id or use --all")
	}
	pluginID := args[0]
	if err := c.RestartPlugin(pluginID); err != nil {
		return fmt.Errorf("restart %s: %w", pluginID, err)
	}
	fmt.Printf("Plugin %s restarted\n", pluginID)
	return nil
}

func runPluginUninstall(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	all, _ := cmd.Flags().GetBool("all")
	force, _ := cmd.Flags().GetBool("force")
	if all {
		return runUninstallAll(c, force)
	}
	if len(args) == 0 {
		return fmt.Errorf("specify a plugin id or use --all")
	}
	pluginID := args[0]
	if !force {
		if err := confirmDestructive(pluginID); err != nil {
			return err
		}
	}
	if err := c.UninstallPlugin(pluginID); err != nil {
		return fmt.Errorf("uninstall %s: %w", pluginID, err)
	}
	fmt.Printf("Plugin %s uninstalled (config removed)\n", pluginID)
	return nil
}

func runPluginConfig(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	pluginID := args[0]
	// remaining args after the id are treated as key filters
	return pluginConfigGet(c, pluginID, args[1:])
}

func runPluginConfigGet(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	return pluginConfigGet(c, args[0], args[1:])
}

func runPluginConfigSet(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	pluginID := args[0]
	pairs := args[1:]
	if len(pairs) == 0 {
		return fmt.Errorf("usage: tacli plugin config set %s KEY=VALUE [KEY=VALUE ...]", pluginID)
	}

	values := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		eq := strings.IndexByte(pair, '=')
		if eq < 1 {
			return fmt.Errorf("invalid config pair %q — expected KEY=VALUE", pair)
		}
		values[pair[:eq]] = pair[eq+1:]
	}

	if err := c.SetPluginConfig(pluginID, values); err != nil {
		return fmt.Errorf("set config %s: %w", pluginID, err)
	}

	for k, v := range values {
		upper := strings.ToUpper(k)
		if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "KEY") ||
			strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") {
			fmt.Printf("  %s = ***\n", k)
		} else {
			fmt.Printf("  %s = %s\n", k, v)
		}
	}
	fmt.Printf("Updated %d config value(s) for %s\n", len(values), pluginID)
	return nil
}

func runPluginCandidate(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	id := args[0]
	image, _ := cmd.Flags().GetString("image")

	if image != "" {
		fmt.Printf("Deploying candidate %s for %s...\n", image, id)
	} else {
		fmt.Printf("Deploying candidate for %s (stable image)...\n", id)
	}

	if err := c.DeployCandidate(id, image); err != nil {
		return fmt.Errorf("deploy candidate %s: %w", id, err)
	}
	fmt.Printf("Candidate deployed. Use 'tacli plugin promote %s' when ready.\n", id)
	return nil
}

func runPluginPromote(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	id := args[0]
	fmt.Printf("Promoting candidate for %s...\n", id)
	if err := c.PromoteCandidate(id); err != nil {
		return fmt.Errorf("promote %s: %w", id, err)
	}
	fmt.Printf("Promoted. Use 'tacli plugin rollback %s' to restore previous if needed.\n", id)
	return nil
}

func runPluginRollback(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	id := args[0]
	fmt.Printf("Rolling back %s...\n", id)
	if err := c.RollbackCandidate(id); err != nil {
		return fmt.Errorf("rollback %s: %w", id, err)
	}
	fmt.Println("Rolled back.")
	return nil
}

func runPluginSchema(cmd *cobra.Command, args []string) error {
	c, err := resolvePluginClient(cmd)
	if err != nil {
		return err
	}
	raw, err := c.PluginSchema(args[0])
	if err != nil {
		return fmt.Errorf("schema %s: %w", args[0], err)
	}
	return printJSON(raw)
}

func pluginConfigGet(c *client.Client, pluginID string, keys []string) error {
	raw, err := c.PluginConfig(pluginID)
	if err != nil {
		return fmt.Errorf("config %s: %w", pluginID, err)
	}

	var wrapper struct {
		Config []struct {
			Key      string `json:"key"`
			Value    string `json:"value"`
			IsSecret bool   `json:"is_secret"`
			Default  string `json:"default,omitempty"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return printJSON(raw)
	}

	filter := make(map[string]bool, len(keys))
	for _, k := range keys {
		filter[k] = true
	}

	for _, item := range wrapper.Config {
		if len(filter) > 0 && !filter[item.Key] {
			continue
		}
		val := item.Value
		if val == "" {
			val = item.Default
		}
		fmt.Printf("%s=%s\n", item.Key, val)
	}
	return nil
}

// pluginTypeFromID extracts the type prefix from a plugin ID (e.g. "agent-claude" → "agent").
func pluginTypeFromID(id string) string {
	if i := strings.Index(id, "-"); i > 0 {
		return id[:i]
	}
	return id
}

// pluginTypeOrder defines the display order for plugin type groups.
var pluginTypeOrder = map[string]int{
	"agent": 0, "infra": 1, "messaging": 2, "network": 3,
	"storage": 4, "tool": 5, "user": 6, "system": 7,
}

func sortPluginsByType(plugins []client.PluginSummary) {
	sort.SliceStable(plugins, func(i, j int) bool {
		ti, tj := pluginTypeFromID(plugins[i].ID), pluginTypeFromID(plugins[j].ID)
		oi, oki := pluginTypeOrder[ti]
		oj, okj := pluginTypeOrder[tj]
		if !oki {
			oi = 100
		}
		if !okj {
			oj = 100
		}
		if oi != oj {
			return oi < oj
		}
		if ti != tj {
			return ti < tj
		}
		return plugins[i].Name < plugins[j].Name
	})
}

func runPluginList(c *client.Client) error {
	plugins, err := c.ListPlugins()
	if err != nil {
		return err
	}
	if len(plugins) == 0 {
		fmt.Println("No plugins installed")
		return nil
	}

	sortPluginsByType(plugins)

	r := getRenderer()
	r.GroupStart("Plugins")
	for _, p := range plugins {
		enabled := "disabled"
		if p.Enabled {
			enabled = "enabled"
		}
		r.Item(render.Fields{
			"group":   pluginTypeFromID(p.ID),
			"id":      p.ID,
			"name":    p.Name,
			"enabled": enabled,
			"status":  p.Status,
			"version": p.Version,
		})
	}
	return r.Flush()
}

func runPluginBatch(c *client.Client, enable bool) error {
	plugins, err := c.ListPlugins()
	if err != nil {
		return err
	}

	action := "Enabling"
	if !enable {
		action = "Disabling"
	}

	okStyle := render.Styles.Enabled
	failStyle := render.Styles.Disabled

	var failed int
	for _, p := range plugins {
		fmt.Printf("%s %s ... ", action, p.Name)
		var err error
		if enable {
			_, err = c.EnablePlugin(p.ID)
		} else {
			err = c.DisablePlugin(p.ID)
		}
		if err != nil {
			fmt.Printf("%s - %s\n", failStyle.Render("[FAILED]"), extractErrorMessage(err))
			failed++
			continue
		}
		fmt.Println(okStyle.Render("[OK]"))
	}

	if failed > 0 {
		return fmt.Errorf("%d plugin(s) failed", failed)
	}
	return nil
}

// extractErrorMessage tries to pull a clean message from API error responses.
// API errors look like: request failed (403): {"error":"system plugins cannot be disabled"}
func extractErrorMessage(err error) string {
	msg := err.Error()
	if idx := strings.Index(msg, `{"error":"`); idx >= 0 {
		inner := msg[idx+10:]
		if end := strings.Index(inner, `"}`); end >= 0 {
			return inner[:end]
		}
	}
	return msg
}

func runPluginRestartAll(c *client.Client) error {
	plugins, err := c.ListPlugins()
	if err != nil {
		return err
	}

	okStyle := render.Styles.Enabled
	failStyle := render.Styles.Disabled

	var failed int
	for _, p := range plugins {
		if !p.Enabled {
			continue
		}
		fmt.Printf("Restarting %s ... ", p.Name)
		if err := c.RestartPlugin(p.ID); err != nil {
			fmt.Printf("%s - %s\n", failStyle.Render("[FAILED]"), extractErrorMessage(err))
			failed++
			continue
		}
		fmt.Println(okStyle.Render("[OK]"))
	}

	if failed > 0 {
		return fmt.Errorf("%d plugin(s) failed to restart", failed)
	}
	return nil
}

func runUninstallAll(c *client.Client, force bool) error {
	plugins, err := c.ListPlugins()
	if err != nil {
		return err
	}
	if len(plugins) == 0 {
		fmt.Println("No plugins installed")
		return nil
	}

	if !force {
		details := []string{
			fmt.Sprintf("This will uninstall ALL %d plugin(s) and permanently delete their", len(plugins)),
			"configurations, data, and credentials. Your cluster will be OFFLINE.",
			"",
			"Plugins to be destroyed:",
		}
		for _, p := range plugins {
			details = append(details, fmt.Sprintf("  - %s (%s)", p.Name, p.ID))
		}
		if err := requireConfirmation("DESTRUCTIVE OPERATION", details...); err != nil {
			return err
		}
	}

	okStyle := render.Styles.Enabled
	failStyle := render.Styles.Disabled

	var failed int
	for _, p := range plugins {
		fmt.Printf("Uninstalling %s ... ", p.Name)
		if err := c.UninstallPlugin(p.ID); err != nil {
			fmt.Printf("%s - %s\n", failStyle.Render("[FAILED]"), extractErrorMessage(err))
			failed++
			continue
		}
		fmt.Println(okStyle.Render("[OK]"))
	}

	if failed > 0 {
		return fmt.Errorf("%d plugin(s) failed to uninstall", failed)
	}
	return nil
}

func confirmDestructive(pluginID string) error {
	return requireConfirmation(
		fmt.Sprintf("This will uninstall %q and remove all its configuration.", pluginID),
	)
}

func printJSON(data json.RawMessage) error {
	pretty, err := json.MarshalIndent(json.RawMessage(data), "", "  ")
	if err != nil {
		_, err = os.Stdout.Write(data)
		fmt.Println()
		return err
	}
	_, err = os.Stdout.Write(pretty)
	fmt.Println()
	return err
}
