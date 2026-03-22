package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
	"github.com/antimatter-studios/teamagentica/tacli/internal/render"
)

func init() {
	setCmd := &cobra.Command{
		Use:   "set key=value [key=value...]",
		Short: "Update kernel configuration in the active profile",
		Long: `Write kernel config values to the active profile.
Changes take effect on next 'tacli core restart'.

Supported keys: domain, name, dev_mode, port
Labels:         label.KEY=VALUE (e.g. label.docker-proxy.foo=bar)
Remove label:   label.KEY= (empty value removes it)

Example:
  tacli core config set label.traefik.enable=true`,
		Args: cobra.MinimumNArgs(1),
		RunE: runCoreConfigSet,
	}

	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage core configuration",
	}
	configCmd.AddCommand(setCmd)
	coreCmd.AddCommand(configCmd)
}

func runCoreConfigSet(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	profile := cfg.Active()
	if profile == nil {
		return fmt.Errorf("no active profile — run 'tacli core create' first")
	}

	for _, arg := range args {
		k, v, ok := strings.Cut(arg, "=")
		if !ok {
			return fmt.Errorf("invalid argument %q — expected key=value", arg)
		}

		if strings.HasPrefix(k, "label.") {
			labelKey := strings.TrimPrefix(k, "label.")
			if profile.Kernel.Labels == nil {
				profile.Kernel.Labels = make(map[string]string)
			}
			if v == "" {
				delete(profile.Kernel.Labels, labelKey)
			} else {
				profile.Kernel.Labels[labelKey] = v
			}
			continue
		}

		switch k {
		case "domain":
			profile.Kernel.Domain = v
		case "name":
			profile.Kernel.Name = v
		case "dev_mode":
			profile.Kernel.DevMode = v == "true"
		case "data_dir":
			abs, err := filepath.Abs(v)
			if err != nil {
				return fmt.Errorf("resolve data_dir: %w", err)
			}
			profile.Kernel.DataDir = abs
		case "port":
			p, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("port must be a number: %w", err)
			}
			profile.Kernel.Port = p
			profile.URL = fmt.Sprintf("http://localhost:%d", p)
		default:
			return fmt.Errorf("unknown config key %q — valid keys: domain, name, dev_mode, data_dir, port, label.KEY", k)
		}
	}

	cfg.SetProfile(*profile)
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save profile: %w", err)
	}

	printKernelConfig(profile.Kernel)
	fmt.Println(render.Styles.Detail.Render("→ run 'tacli core restart' to apply"))
	return nil
}

// printKernelConfig renders the full kernel state as a table.
func printKernelConfig(ks config.KernelState) {
	r := getRenderer()
	r.GroupStart("Kernel Config")
	r.Item(render.Fields{"name": "image", "status": ks.Image})
	r.Item(render.Fields{"name": "port", "status": strconv.Itoa(ks.Port)})
	r.Item(render.Fields{"name": "domain", "status": ks.Domain})
	r.Item(render.Fields{"name": "name", "status": ks.Name})
	r.Item(render.Fields{"name": "data_dir", "status": ks.DataDir})
	r.Item(render.Fields{"name": "network", "status": ks.NetworkName})
	r.Item(render.Fields{"name": "dev_mode", "status": strconv.FormatBool(ks.DevMode)})

	if len(ks.Labels) > 0 {
		keys := make([]string, 0, len(ks.Labels))
		for k := range ks.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			r.Item(render.Fields{"name": "label." + k, "status": ks.Labels[k]})
		}
	}

	_ = r.Flush()
}

// buildKernelEnv constructs the kernel container env slice from a KernelState.
func buildKernelEnv(ks config.KernelState, networkName string) []string {
	env := []string{
		"TEAMAGENTICA_KERNEL_HOST=0.0.0.0",
		"TEAMAGENTICA_KERNEL_PORT=8080",
		"TEAMAGENTICA_KERNEL_ADVERTISE_HOST=teamagentica-kernel",
		"TEAMAGENTICA_DB_PATH=/data/kernel/database.db",
		"TEAMAGENTICA_DATA_DIR=" + ks.DataDir,
		"TEAMAGENTICA_DOCKER_NETWORK=" + networkName,
		"TEAMAGENTICA_BASE_DOMAIN=" + ks.Domain,
	}
	if ks.Name != "" {
		env = append(env, "APP_NAME="+ks.Name)
	}
	if ks.JWTTTLHours > 0 {
		env = append(env, fmt.Sprintf("JWT_TTL_HOURS=%d", ks.JWTTTLHours))
	}
	return env
}
