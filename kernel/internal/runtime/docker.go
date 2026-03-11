package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"bytes"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/antimatter-studios/teamagentica/kernel/internal/certs"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// DockerRuntime manages plugin containers via the Docker API.
type DockerRuntime struct {
	client      *client.Client
	network     string
	certManager *certs.CertManager
	devMode     bool
	baseDomain  string
}

// NewDockerRuntime creates a Docker client from environment and ensures the
// network exists. Returns nil runtime (not an error) if Docker is unavailable,
// so the kernel can still start without Docker.
// The certManager parameter is optional; pass nil to disable mTLS cert injection.
func NewDockerRuntime(networkName string, certManager *certs.CertManager, devMode bool, baseDomain string) (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Printf("WARNING: docker client init failed: %v — plugin runtime disabled", err)
		return nil, nil
	}

	// Ping to verify Docker is reachable.
	ctx := context.Background()
	if _, err := cli.Ping(ctx); err != nil {
		log.Printf("WARNING: docker daemon unreachable: %v — plugin runtime disabled", err)
		return nil, nil
	}

	rt := &DockerRuntime{
		client:      cli,
		network:     networkName,
		certManager: certManager,
		devMode:     devMode,
		baseDomain:  baseDomain,
	}

	if err := rt.ensureNetwork(ctx); err != nil {
		return nil, fmt.Errorf("ensure docker network: %w", err)
	}

	log.Printf("docker runtime initialised (network=%s)", networkName)
	return rt, nil
}

// ensureNetwork creates the bridge network if it does not already exist.
func (d *DockerRuntime) ensureNetwork(ctx context.Context) error {
	networks, err := d.client.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return err
	}
	for _, n := range networks {
		if n.Name == d.network {
			return nil
		}
	}
	_, err = d.client.NetworkCreate(ctx, d.network, network.CreateOptions{
		Driver: "bridge",
	})
	return err
}

// projectRoot returns the host path to the project root, read fresh from
// the environment each time so it always reflects the current value.
func (d *DockerRuntime) projectRoot() string {
	return os.Getenv("TEAMAGENTICA_PROJECT_ROOT")
}

// PullImage pulls a Docker image by reference.
func (d *DockerRuntime) PullImage(ctx context.Context, imageRef string) error {
	reader, err := d.client.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", imageRef, err)
	}
	defer reader.Close()
	// Consume the pull output so the operation completes.
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

// StartPlugin creates and starts a container for the given plugin.
func (d *DockerRuntime) StartPlugin(ctx context.Context, plugin *models.Plugin, env map[string]string) (string, error) {
	containerName := "teamagentica-plugin-" + plugin.ID
	volumeName := "teamagentica-data-" + plugin.ID

	// Remove any stale container with the same name (crashed, orphaned, etc.).
	d.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	// If cert manager is available, generate plugin certs and inject via env + mount.
	var certMount *mount.Mount
	if d.certManager != nil {
		_, _, _, err := d.certManager.GeneratePluginCert(plugin.ID)
		if err != nil {
			return "", fmt.Errorf("generate plugin cert: %w", err)
		}

		certDir := d.certManager.GetPluginCertDir(plugin.ID)

		env["TEAMAGENTICA_TLS_CERT"] = filepath.Join("/certs", plugin.ID+".crt")
		env["TEAMAGENTICA_TLS_KEY"] = filepath.Join("/certs", plugin.ID+".key")
		env["TEAMAGENTICA_TLS_CA"] = "/certs/ca.crt"
		env["TEAMAGENTICA_TLS_ENABLED"] = "true"

		certMount = &mount.Mount{
			Type:     mount.TypeBind,
			Source:   certDir,
			Target:   "/certs",
			ReadOnly: true,
		}
	}

	// In dev mode, share Go caches across plugin containers.
	if d.devMode {
		env["GOMODCACHE"] = "/go/pkg/mod"
		env["GOCACHE"] = "/root/.cache/go-build"
	}

	// Assign a public subdomain for plugins that need direct browser access
	// (e.g. code-server iframes that can't work behind a sub-path proxy).
	var labels map[string]string
	if hasCapabilityPrefix(plugin.GetCapabilities(), "workspace:editor") && d.baseDomain != "" {
		subdomain := "code." + d.baseDomain
		proxyName := "teamagentica-" + plugin.ID
		// Use the plugin's configured port from env, fall back to 8092.
		pluginPort := env["INFRA_CODE_SERVER_PORT"]
		if pluginPort == "" {
			pluginPort = "8092"
		}
		labels = map[string]string{
			"docker-proxy." + proxyName + ".host": subdomain,
			"docker-proxy." + proxyName + ".port": pluginPort,
		}
		env["TEAMAGENTICA_PUBLIC_HOST"] = subdomain
		env["TEAMAGENTICA_BASE_DOMAIN"] = d.baseDomain
	}

	// Build env slice (after all env mutations).
	var envSlice []string
	for k, v := range env {
		envSlice = append(envSlice, k+"="+v)
	}

	cfg := &container.Config{
		Image:    plugin.Image,
		Hostname: containerName,
		Env:      envSlice,
		Labels:   labels,
	}

	projectRoot := d.projectRoot()

	var dataMount mount.Mount
	if d.devMode && projectRoot != "" {
		// Dev mode: bind mount plugin data from host for persistence and visibility.
		hostPluginData := filepath.Join(projectRoot, "data", plugin.ID)

		// Create via our own /data mount (which maps to the same host directory).
		localPluginData := filepath.Join("/data", plugin.ID)
		if err := os.MkdirAll(localPluginData, 0o755); err != nil {
			return "", fmt.Errorf("create plugin data dir: %w", err)
		}

		dataMount = mount.Mount{
			Type:   mount.TypeBind,
			Source: hostPluginData,
			Target: "/data",
		}
	} else {
		// Production: named Docker volume.
		dataMount = mount.Mount{
			Type:   mount.TypeVolume,
			Source: volumeName,
			Target: "/data",
		}
	}

	mounts := []mount.Mount{dataMount}
	if certMount != nil {
		mounts = append(mounts, *certMount)
	}

	// Cross-mount the storage:volume plugin's data into workspace-aware plugins.
	if hasCapabilityPrefix(plugin.GetCapabilities(), "ai:chat") ||
		hasCapabilityPrefix(plugin.GetCapabilities(), "workspace:") {
		if d.devMode && projectRoot != "" {
			hostVolume := filepath.Join(projectRoot, "data", "storage-volume")
			localDir := filepath.Join("/data", "storage-volume")
			if err := os.MkdirAll(localDir, 0o755); err != nil {
				log.Printf("warning: create storage-volume dir: %v", err)
			}
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeBind,
				Source: hostVolume,
				Target: "/workspaces",
			})
		} else {
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeVolume,
				Source: "teamagentica-data-storage-volume",
				Target: "/workspaces",
			})
		}
		log.Printf("cross-mounting workspace volume into plugin %s at /workspaces", plugin.ID)
	}

	// In dev mode, mount plugin source code and shared SDK for hot reload.
	if d.devMode && projectRoot != "" {
		// Derive plugin dir name from image (e.g. "teamagentica-telegram:dev" -> "telegram").
		imageName := plugin.Image
		if i := strings.LastIndex(imageName, ":"); i >= 0 {
			imageName = imageName[:i]
		}
		pluginDir := strings.TrimPrefix(imageName, "teamagentica-")

		mounts = append(mounts,
			mount.Mount{
				Type:   mount.TypeBind,
				Source: filepath.Join(projectRoot, "plugins", pluginDir),
				Target: filepath.Join("/app/plugins", pluginDir),
			},
			mount.Mount{
				Type:   mount.TypeBind,
				Source: filepath.Join(projectRoot, "pkg", "pluginsdk"),
				Target: "/app/pkg/pluginsdk",
			},
		)
		log.Printf("dev mode: mounting source for plugin %s from %s", plugin.ID, filepath.Join(projectRoot, "plugins", pluginDir))

		// Named volumes for shared Go module + build caches.
		goModVol := d.network + "-gomodcache"
		goBuildVol := d.network + "-gobuildcache"
		mounts = append(mounts,
			mount.Mount{
				Type:   mount.TypeVolume,
				Source: goModVol,
				Target: "/go/pkg/mod",
			},
			mount.Mount{
				Type:   mount.TypeVolume,
				Source: goBuildVol,
				Target: "/root/.cache/go-build",
			},
		)
	}

	hostCfg := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		Mounts:        mounts,
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			d.network: {},
		},
	}

	resp, err := d.client.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", containerName, err)
	}

	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start container %s: %w", containerName, err)
	}

	return resp.ID, nil
}

// StopPlugin stops and removes a container but keeps its data volume.
// If the container no longer exists (already removed/crashed), this is
// treated as success — the container is already gone.
func (d *DockerRuntime) StopPlugin(ctx context.Context, containerID string) error {
	if err := d.client.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		if errdefs.IsNotFound(err) {
			log.Printf("container %s already removed — nothing to stop", containerID)
			return nil
		}
		log.Printf("warning: container stop %s: %v", containerID, err)
	}
	if err := d.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		if errdefs.IsNotFound(err) {
			log.Printf("container %s already removed — nothing to clean up", containerID)
			return nil
		}
		return fmt.Errorf("remove container %s: %w", containerID, err)
	}
	return nil
}

// StartManagedContainer creates and starts a container on behalf of a plugin.
// Unlike StartPlugin, managed containers have no mTLS, no SDK env vars, no
// dev-mode source mounts — just a single volume mounted at /workspace and
// docker-proxy labels for subdomain routing.
func (d *DockerRuntime) StartManagedContainer(ctx context.Context, mc *models.ManagedContainer, baseDomain string) (string, error) {
	containerName := "teamagentica-mc-" + mc.ID

	// Pull image if not available locally.
	if err := d.PullImage(ctx, mc.Image); err != nil {
		log.Printf("managed container: image pull for %s failed (may be local): %v", mc.Image, err)
	}

	// Remove any stale container with the same name.
	d.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	// Build env slice from the stored env map.
	env := mc.GetEnv()
	var envSlice []string
	for k, v := range env {
		envSlice = append(envSlice, k+"="+v)
	}
	// Every managed container gets the proxy base path as an env var.
	// The workspace image's entrypoint decides how to use it.
	envSlice = append(envSlice, "PROXY_BASE_PATH=/ws/"+mc.ID)

	// Docker-proxy labels for subdomain routing.
	var labels map[string]string
	if mc.Subdomain != "" && baseDomain != "" {
		proxyName := "teamagentica-mc-" + mc.ID
		labels = map[string]string{
			"docker-proxy." + proxyName + ".host": mc.Subdomain + "." + baseDomain,
			"docker-proxy." + proxyName + ".port": fmt.Sprintf("%d", mc.Port),
		}
	}

	cfg := &container.Config{
		Image:    mc.Image,
		Hostname: containerName,
		Env:      envSlice,
		Labels:   labels,
	}
	if cmd := mc.GetCmd(); len(cmd) > 0 {
		cfg.Cmd = cmd
	}
	if mc.DockerUser != "" {
		cfg.User = mc.DockerUser
	}

	// Single volume mount: storage-volume's volumes/{name} → /workspace.
	var mounts []mount.Mount
	if mc.VolumeName != "" {
		projectRoot := d.projectRoot()
		if d.devMode && projectRoot != "" {
			hostVolume := filepath.Join(projectRoot, "data", "storage-volume", "volumes", mc.VolumeName)
			if err := os.MkdirAll(hostVolume, 0o755); err != nil {
				return "", fmt.Errorf("create volume dir: %w", err)
			}
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeBind,
				Source: hostVolume,
				Target: "/workspace",
			})
		} else {
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeVolume,
				Source: "teamagentica-data-storage-volume",
				Target: "/workspace",
				VolumeOptions: &mount.VolumeOptions{
					Subpath: filepath.Join("volumes", mc.VolumeName),
				},
			})
		}
	}

	// Extra mounts: additional volumes requested by the plugin (e.g. shared
	// extension directories). Same convention as the primary volume — names
	// resolve relative to the storage-volume volumes dir.
	for _, em := range mc.GetExtraMounts() {
		if em.VolumeName == "" || em.Target == "" {
			continue
		}
		projectRoot := d.projectRoot()
		if d.devMode && projectRoot != "" {
			hostPath := filepath.Join(projectRoot, "data", "storage-volume", "volumes", em.VolumeName)
			if err := os.MkdirAll(hostPath, 0o755); err != nil {
				return "", fmt.Errorf("create extra mount dir %s: %w", em.VolumeName, err)
			}
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeBind,
				Source:   hostPath,
				Target:   em.Target,
				ReadOnly: em.ReadOnly,
			})
		} else {
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeVolume,
				Source: "teamagentica-data-storage-volume",
				Target: em.Target,
				VolumeOptions: &mount.VolumeOptions{
					Subpath: filepath.Join("volumes", em.VolumeName),
				},
				ReadOnly: em.ReadOnly,
			})
		}
	}

	hostCfg := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "no"},
		Mounts:        mounts,
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			d.network: {},
		},
	}

	resp, err := d.client.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("create managed container %s: %w", containerName, err)
	}

	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start managed container %s: %w", containerName, err)
	}

	log.Printf("started managed container %s (image=%s, volume=%s, subdomain=%s)", mc.ID, mc.Image, mc.VolumeName, mc.Subdomain)
	return resp.ID, nil
}

// HealthCheck returns true if the container is in the "running" state.
func (d *DockerRuntime) HealthCheck(ctx context.Context, containerID string) (bool, error) {
	info, err := d.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, err
	}
	return info.State.Running, nil
}

// ContainerLogs returns the last N lines of a container's logs.
func (d *DockerRuntime) ContainerLogs(ctx context.Context, containerID string, tail int) (string, error) {
	reader, err := d.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	})
	if err != nil {
		return "", fmt.Errorf("get logs for %s: %w", containerID, err)
	}
	defer reader.Close()

	// Docker multiplexes stdout/stderr with 8-byte frame headers.
	// Use stdcopy to properly demux instead of stripping per-line.
	var buf bytes.Buffer
	if _, err := stdcopy.StdCopy(&buf, &buf, reader); err != nil {
		// Fallback: if stdcopy fails (e.g. TTY container), read raw.
		raw, readErr := io.ReadAll(reader)
		if readErr != nil {
			return "", fmt.Errorf("read logs for %s: %w", containerID, err)
		}
		return string(raw), nil
	}

	return strings.TrimRight(buf.String(), "\n"), nil
}

// hasCapabilityPrefix checks if any capability starts with the given prefix.
func hasCapabilityPrefix(caps []string, prefix string) bool {
	for _, c := range caps {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}
