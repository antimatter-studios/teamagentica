package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
)

var (
	flagCreateName     string
	flagCreatePort     int
	flagCreateImage    string
	flagCreateEmail    string
	flagCreatePassword string
	flagCreateConfig   string
)

func init() {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new TeamAgentica instance",
		Long: `Create a new TeamAgentica instance by starting a kernel container.

Without --config, creates a bare kernel with admin user.
With --config, reads a .taconfig YAML file and sets up the full stack:
kernel, UI, marketplace providers, plugins, and plugin configuration.

Flags override config file values when both are provided.`,
		RunE: runCreate,
	}
	cmd.Flags().StringVar(&flagCreateName, "name", "default", "profile name for this instance")
	cmd.Flags().IntVar(&flagCreatePort, "port", 0, "host port to expose the kernel on")
	cmd.Flags().StringVar(&flagCreateImage, "image", "", "kernel Docker image")
	cmd.Flags().StringVar(&flagCreateEmail, "email", "", "admin email")
	cmd.Flags().StringVar(&flagCreatePassword, "password", "", "admin password")
	cmd.Flags().StringVar(&flagCreateConfig, "config", "", "path or URL to .taconfig YAML file")
	coreCmd.AddCommand(cmd)
}

func runCreate(cmd *cobra.Command, args []string) error {
	// Load config file if provided, then merge flags on top.
	tc := resolveCreateConfig(cmd)

	// Validate required fields.
	if tc.Admin.Email == "" {
		return fmt.Errorf("admin email required (use --email or set admin.email in config)")
	}
	if tc.Admin.Password == "" {
		return fmt.Errorf("admin password required (use --password or set admin.password in config)")
	}

	ctx := context.Background()

	// 1. Connect to Docker.
	docker, err := newDockerClient()
	if err != nil {
		return err
	}
	defer docker.Close()

	if _, err := docker.Ping(ctx); err != nil {
		return fmt.Errorf("docker daemon unreachable: %w", err)
	}

	containerName := "teamagentica-kernel"
	networkName := "teamagentica"
	kernelURL := fmt.Sprintf("http://localhost:%d", tc.Kernel.Port)

	// 2. Check nothing already running with that name.
	if _, err := docker.ContainerInspect(ctx, containerName); err == nil {
		return fmt.Errorf("container %q already exists — use 'tacli connect %s' or remove it first", containerName, kernelURL)
	}

	// 3. Create Docker network.
	fmt.Printf("Creating network %s...\n", networkName)
	if err := ensureNetwork(ctx, docker, networkName); err != nil {
		return fmt.Errorf("create network: %w", err)
	}

	// 4. Resolve data directory (absolute path for Docker bind mount).
	dataDir, err := filepath.Abs(tc.Kernel.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// 5. Build kernel environment.
	env := []string{
		"TEAMAGENTICA_KERNEL_HOST=0.0.0.0",
		"TEAMAGENTICA_KERNEL_PORT=8080",
		"TEAMAGENTICA_KERNEL_ADVERTISE_HOST=" + containerName,
		"TEAMAGENTICA_DB_PATH=/data/kernel/database.db",
		"TEAMAGENTICA_DATA_DIR=" + dataDir,
		"TEAMAGENTICA_DOCKER_NETWORK=" + networkName,
		"TEAMAGENTICA_BASE_DOMAIN=" + tc.Kernel.Domain,
	}
	if tc.Kernel.Name != "" {
		env = append(env, "APP_NAME="+tc.Kernel.Name)
	}
	if tc.Kernel.JWTTTLHours > 0 {
		env = append(env, fmt.Sprintf("JWT_TTL_HOURS=%d", tc.Kernel.JWTTTLHours))
	}


	// 6. Build labels.
	labels := map[string]string{
		"teamagentica.managed":        "true",
		"teamagentica.role":           "kernel",
		"com.docker.compose.project":  "teamagentica",
		"com.docker.compose.service":  "kernel",
	}
	for k, v := range tc.Kernel.Labels {
		labels[k] = v
	}

	// 7. Start kernel container.
	fmt.Printf("Starting kernel (%s) on port %d...\n", tc.Kernel.Image, tc.Kernel.Port)
	hostPort := fmt.Sprintf("%d", tc.Kernel.Port)

	containerCfg := &container.Config{
		Image:        tc.Kernel.Image,
		Hostname:     containerName,
		Env:          env,
		ExposedPorts: nat.PortSet{"8080/tcp": struct{}{}, "8081/tcp": struct{}{}},
		Labels:       labels,
	}

	mounts := []mount.Mount{
		{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"},
		{Type: mount.TypeBind, Source: dataDir, Target: "/data"},
	}

	hostCfg := &container.HostConfig{
		PortBindings: nat.PortMap{
			"8080/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: hostPort}},
		},
		Mounts:        mounts,
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {},
		},
	}

	resp, err := docker.ContainerCreate(ctx, containerCfg, hostCfg, netCfg, nil, containerName)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	if err := docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	// 8. Wait for health check.
	fmt.Print("Waiting for kernel to start")
	c := client.New(kernelURL, "")
	if err := waitForHealth(c, 60*time.Second); err != nil {
		return fmt.Errorf("kernel failed to start: %w", err)
	}
	fmt.Println(" ready!")

	// 9. Register admin user.
	fmt.Printf("Creating admin user (%s)...\n", tc.Admin.Email)
	lr, err := c.Register(tc.Admin.Email, tc.Admin.Password)
	if err != nil {
		// Registration closed means users already exist — try logging in instead.
		fmt.Printf("Registration closed (existing database), logging in as %s...\n", tc.Admin.Email)
		lr, err = c.Login(tc.Admin.Email, tc.Admin.Password)
		if err != nil {
			return fmt.Errorf("login failed (kernel has existing data but credentials don't match): %w", err)
		}
	}
	c.Token = lr.Token

	// 10. Save profile with kernel state so future config changes can recreate the container.
	tacliCfg, err := config.Load()
	if err != nil {
		return err
	}
	tacliCfg.SetProfile(config.Profile{
		Name:  flagCreateName,
		URL:   kernelURL,
		Token: lr.Token,
		Kernel: config.KernelState{
			Image:       tc.Kernel.Image,
			Port:        tc.Kernel.Port,
			Domain:      tc.Kernel.Domain,
			DataDir:     dataDir,
			Name:        tc.Kernel.Name,
			DevMode:     tc.Kernel.DevMode,
			NetworkName: networkName,
			Labels:      tc.Kernel.Labels,
			JWTTTLHours: tc.Kernel.JWTTTLHours,
		},
	})
	tacliCfg.ActiveProfile = flagCreateName
	if err := config.Save(tacliCfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("\nKernel running at %s\n", kernelURL)
	fmt.Printf("Profile %q saved and set as active\n", flagCreateName)

	// 11. Add marketplace providers.
	for _, providerURL := range tc.Marketplace {
		fmt.Printf("Adding marketplace provider: %s\n", providerURL)
		if _, err := c.AddProvider("", providerURL); err != nil {
			fmt.Printf("  Warning: %v\n", err)
		}
	}

	// 13. Install, enable, and configure plugins.
	if len(tc.Plugins) > 0 {
		if err := setupPlugins(c, tc.Plugins); err != nil {
			fmt.Printf("Warning: plugin setup incomplete: %v\n", err)
		}
	}

	fmt.Println("\nDone!")
	return nil
}

// resolveCreateConfig builds a TAConfig by loading the config file (if any)
// and overriding with CLI flags.
func resolveCreateConfig(cmd *cobra.Command) *config.TAConfig {
	var tc *config.TAConfig

	if flagCreateConfig != "" {
		loaded, err := config.LoadTAConfig(flagCreateConfig)
		if err != nil {
			fmt.Printf("Warning: could not load config %s: %v\n", flagCreateConfig, err)
			tc = &config.TAConfig{}
		} else {
			tc = loaded
		}
	} else {
		tc = &config.TAConfig{}
	}

	// Apply defaults for fields not set by config file.
	if tc.Kernel.Image == "" {
		tc.Kernel.Image = "teamagentica-kernel:latest"
	}
	if tc.Kernel.Port == 0 {
		tc.Kernel.Port = 9741
	}
	if tc.Kernel.Domain == "" {
		tc.Kernel.Domain = "localhost"
	}
	if tc.Kernel.DataDir == "" {
		tc.Kernel.DataDir = "./data"
	}

	// Flags override config file values.
	if cmd.Flags().Changed("port") {
		tc.Kernel.Port = flagCreatePort
	}
	if cmd.Flags().Changed("image") {
		tc.Kernel.Image = flagCreateImage
	}
	if cmd.Flags().Changed("email") {
		tc.Admin.Email = flagCreateEmail
	}
	if cmd.Flags().Changed("password") {
		tc.Admin.Password = flagCreatePassword
	}

	return tc
}

// setupPlugins installs, enables, and configures plugins from the config.
func setupPlugins(c *client.Client, plugins map[string]config.PluginConfig) error {
	// Sort for deterministic order.
	names := make([]string, 0, len(plugins))
	for name := range plugins {
		names = append(names, name)
	}
	sort.Strings(names)

	// Install all plugins first.
	fmt.Println("\nInstalling plugins...")
	for _, name := range names {
		fmt.Printf("  Installing %s...", name)
		if _, err := c.InstallPlugin(name); err != nil {
			fmt.Printf(" failed: %v\n", err)
			continue
		}
		fmt.Println(" ok")
	}

	// Configure plugins (before enabling so config is ready at startup).
	for _, name := range names {
		pcfg := plugins[name]
		if len(pcfg.Config) == 0 {
			continue
		}
		fmt.Printf("  Configuring %s...", name)
		if err := c.SetPluginConfig(name, pcfg.Config); err != nil {
			fmt.Printf(" failed: %v\n", err)
			continue
		}
		fmt.Println(" ok")
	}

	// Enable plugins that have enable: true.
	fmt.Println("Enabling plugins...")
	for _, name := range names {
		pcfg := plugins[name]
		if !pcfg.Enable {
			continue
		}
		fmt.Printf("  Enabling %s...", name)
		if _, err := c.EnablePlugin(name); err != nil {
			fmt.Printf(" failed: %v\n", err)
			continue
		}
		fmt.Println(" ok")
	}

	return nil
}

func ensureNetwork(ctx context.Context, docker *dockerclient.Client, name string) error {
	networks, err := docker.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return err
	}
	for _, n := range networks {
		if n.Name == name {
			return nil
		}
	}
	_, err = docker.NetworkCreate(ctx, name, network.CreateOptions{Driver: "bridge"})
	return err
}

func waitForHealth(c *client.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(c.BaseURL + "/api/health")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		fmt.Print(".")
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out after %s", timeout)
}
