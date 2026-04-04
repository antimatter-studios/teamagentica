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
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime/runtimecfg"
)

// DockerRuntime manages plugin containers via the Docker API.
type DockerRuntime struct {
	client           *client.Client
	network          string
	dataDir          string // host-side path bind-mounted at /data in the kernel container
	certManager      *certs.CertManager
	rtCfg            *runtimecfg.Config
	baseDomain       string
	selfContainerID  string // Docker container ID of the kernel itself (resolved at startup)
}

// NewDockerRuntime creates a Docker client from environment and ensures the
// network exists. Returns nil runtime (not an error) if Docker is unavailable,
// so the kernel can still start without Docker.
// The certManager parameter is optional; pass nil to disable mTLS cert injection.
func NewDockerRuntime(networkName, dataDir string, certManager *certs.CertManager, rtCfg *runtimecfg.Config, baseDomain string) (*DockerRuntime, error) {
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
		dataDir:     dataDir,
		certManager: certManager,
		rtCfg:       rtCfg,
		baseDomain:  baseDomain,
	}

	if err := rt.ensureNetwork(ctx); err != nil {
		return nil, fmt.Errorf("ensure docker network: %w", err)
	}

	// Discover our own container ID so we can serve kernel logs.
	if id, err := rt.discoverSelfContainer(ctx); err != nil {
		log.Printf("WARNING: could not discover own container ID: %v — kernel logs unavailable", err)
	} else {
		rt.selfContainerID = id
		log.Printf("docker runtime: kernel container ID=%s", id[:12])
	}

	log.Printf("docker runtime initialised (network=%s)", networkName)
	return rt, nil
}

// SelfContainerID returns the Docker container ID of the kernel itself.
// Returns empty string if discovery failed.
func (d *DockerRuntime) SelfContainerID() string {
	return d.selfContainerID
}

// UIContainerID finds the web-dashboard container by well-known name.
// Checks for "teamagentica-web-dashboard-dev" (dev) and "teamagentica-web-dashboard" (prod).
func (d *DockerRuntime) UIContainerID(ctx context.Context) (string, error) {
	containers, err := d.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list containers: %w", err)
	}
	for _, c := range containers {
		for _, name := range c.Names {
			name = strings.TrimPrefix(name, "/")
			if name == "teamagentica-web-dashboard-dev" || name == "teamagentica-web-dashboard" {
				return c.ID, nil
			}
		}
	}
	return "", fmt.Errorf("web-dashboard container not found (teamagentica-web-dashboard / teamagentica-web-dashboard-dev)")
}

// discoverSelfContainer finds the kernel's own container by matching hostname.
func (d *DockerRuntime) discoverSelfContainer(ctx context.Context) (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("get hostname: %w", err)
	}

	containers, err := d.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list containers: %w", err)
	}

	for _, c := range containers {
		info, err := d.client.ContainerInspect(ctx, c.ID)
		if err != nil {
			continue
		}
		if info.Config != nil && info.Config.Hostname == hostname {
			return c.ID, nil
		}
	}

	return "", fmt.Errorf("no container found with hostname %q", hostname)
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

// pluginDir derives the plugin directory name from its Docker image tag.
// e.g. "teamagentica-agent-claude:dev" → "agent-claude"
func pluginDir(imageRef string) string {
	name := imageRef
	if i := strings.LastIndex(name, ":"); i >= 0 {
		name = name[:i]
	}
	return strings.TrimPrefix(name, "teamagentica-")
}

// templateVars builds the variable map for config template substitution.
func (d *DockerRuntime) templateVars(pluginID, pluginDirName string) map[string]string {
	return map[string]string{
		"DATA_DIR":   d.dataDir,
		"PLUGIN_ID":  pluginID,
		"PLUGIN_DIR": pluginDirName,
		"NETWORK":    d.network,
	}
}

// resolveMount converts a MountSpec into a Docker mount.Mount using resolved source/subpath strings.
func resolveMount(spec runtimecfg.MountSpec, source, target, subpath string) mount.Mount {
	m := mount.Mount{
		Target:   target,
		ReadOnly: spec.ReadOnly,
	}
	if spec.Type == "bind" {
		m.Type = mount.TypeBind
		m.Source = source
	} else {
		m.Type = mount.TypeVolume
		m.Source = source
		if subpath != "" {
			m.VolumeOptions = &mount.VolumeOptions{Subpath: subpath}
		}
	}
	return m
}

// ensureBindDir creates the local directory for a bind mount if needed.
// hostSource is the host-side path Docker will bind mount. The kernel container
// has dataDir mounted at /data, so we translate hostSource to a kernel-local
// path by replacing the dataDir prefix with /data, then mkdir there.
func ensureBindDir(mountType, hostSource, dataDir string) {
	if mountType != "bind" || dataDir == "" {
		return
	}
	localPath := strings.Replace(hostSource, dataDir, "/data", 1)
	if localPath == hostSource {
		return // hostSource doesn't start with dataDir — nothing to do
	}
	if err := os.MkdirAll(localPath, 0o755); err != nil {
		log.Printf("warning: create dir %s: %v", localPath, err)
	}
}

// PullImage pulls a Docker image if it doesn't exist locally.
func (d *DockerRuntime) PullImage(ctx context.Context, imageRef string) error {
	// Check if image exists locally first.
	if _, _, err := d.client.ImageInspectWithRaw(ctx, imageRef); err == nil {
		return nil
	}

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
		// Translate container-internal path to host path for Docker bind mount.
		hostCertDir := strings.Replace(certDir, "/data", d.dataDir, 1)

		env["TEAMAGENTICA_TLS_CERT"] = filepath.Join("/certs", plugin.ID+".crt")
		env["TEAMAGENTICA_TLS_KEY"] = filepath.Join("/certs", plugin.ID+".key")
		env["TEAMAGENTICA_TLS_CA"] = "/certs/ca.crt"

		certMount = &mount.Mount{
			Type:     mount.TypeBind,
			Source:   hostCertDir,
			Target:   "/certs",
			ReadOnly: true,
		}
	}

	// Inject extra env vars from runtime config (e.g. Go cache paths in dev).
	for k, v := range d.rtCfg.PluginEnv {
		env[k] = v
	}

	// Group plugin containers with the kernel in Docker Desktop.
	labels := map[string]string{
		"com.docker.compose.project": "teamagentica",
		"com.docker.compose.service": plugin.ID,
	}

	// Assign a public subdomain for plugins that need direct browser access
	// (e.g. code-server iframes that can't work behind a sub-path proxy).
	if hasCapabilityPrefix(plugin.GetCapabilities(), "workspace:editor") && d.baseDomain != "" {
		subdomain := "code." + d.baseDomain
		proxyName := "teamagentica-" + plugin.ID
		// Use the plugin's configured port from env, fall back to 8092.
		pluginPort := env["INFRA_CODE_SERVER_PORT"]
		if pluginPort == "" {
			pluginPort = "8092"
		}
		labels["docker-proxy."+proxyName+".host"] = subdomain
		labels["docker-proxy."+proxyName+".port"] = pluginPort
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

	// Build template vars for config resolution.
	vars := d.templateVars(plugin.ID, pluginDir(plugin.Image))

	// Data mount — strategy (bind vs volume) comes from runtime config.
	// Every plugin gets its own /data directory for private storage.
	dataSource := runtimecfg.Resolve(d.rtCfg.DataMount.Source, vars)
	ensureBindDir(d.rtCfg.DataMount.Type, dataSource, d.dataDir)
	dataMount := resolveMount(
		runtimecfg.MountSpec{Type: d.rtCfg.DataMount.Type},
		dataSource, "/data", "",
	)

	mounts := []mount.Mount{dataMount}
	if certMount != nil {
		mounts = append(mounts, *certMount)
	}

	// Cross-mount the storage-disk shared filesystem into plugins that need it.
	if hasCapabilityPrefix(plugin.GetCapabilities(), "agent:chat") ||
		hasCapabilityPrefix(plugin.GetCapabilities(), "workspace:") ||
		hasCapabilityPrefix(plugin.GetCapabilities(), "storage:") {
		src := runtimecfg.Resolve(d.rtCfg.StorageCrossMount.Source, vars)
		sub := runtimecfg.Resolve(d.rtCfg.StorageCrossMount.Subpath, vars)
		ensureBindDir(d.rtCfg.StorageCrossMount.Type, src, d.dataDir)
		mounts = append(mounts, resolveMount(d.rtCfg.StorageCrossMount, src, "/storage-root", sub))
		log.Printf("cross-mounting storage-disk into plugin %s at /storage-root", plugin.ID)
	}

	// Extra mounts from runtime config (source code, SDK, Go caches in dev; empty in prod).
	for _, pm := range d.rtCfg.PluginMounts {
		src := runtimecfg.Resolve(pm.Source, vars)
		tgt := pm.Target
		sub := runtimecfg.Resolve(pm.Subpath, vars)
		mounts = append(mounts, resolveMount(pm, src, tgt, sub))
	}
	if len(d.rtCfg.PluginMounts) > 0 {
		log.Printf("mounting %d extra mount(s) for plugin %s", len(d.rtCfg.PluginMounts), plugin.ID)
	}

	// Plugins with build:docker capability get access to the Docker socket.
	if hasCapabilityPrefix(plugin.GetCapabilities(), "build:docker") {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: "/var/run/docker.sock",
			Target: "/var/run/docker.sock",
		})
		log.Printf("mounting docker.sock into plugin %s (build:docker capability)", plugin.ID)
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

// StartCandidatePlugin starts a candidate container alongside the primary.
// Uses the same logic as StartPlugin but with a "-candidate" container name suffix
// so both containers can coexist on the same network.
func (d *DockerRuntime) StartCandidatePlugin(ctx context.Context, plugin *models.Plugin, env map[string]string) (string, error) {
	// Temporarily override the ID to get a different container name,
	// then restore it. The container name becomes "teamagentica-plugin-{id}-candidate".
	origID := plugin.ID
	plugin.ID = origID + "-candidate"
	defer func() { plugin.ID = origID }()

	return d.StartPlugin(ctx, plugin, env)
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
// Unlike StartPlugin, managed containers have no mTLS or plugin SDK env vars —
// just volume mounts and docker-proxy labels for subdomain routing.
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

	// Group managed containers with the kernel in Docker Desktop.
	labels := map[string]string{
		"com.docker.compose.project": "teamagentica",
		"com.docker.compose.service": "mc-" + mc.ID,
	}
	// Docker-proxy labels for subdomain routing.
	if mc.Subdomain != "" && baseDomain != "" {
		proxyName := "teamagentica-mc-" + mc.ID
		labels["docker-proxy."+proxyName+".host"] = mc.Subdomain + "." + baseDomain
		labels["docker-proxy."+proxyName+".port"] = fmt.Sprintf("%d", mc.Port)
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

	// Template vars for managed container config resolution.
	vars := d.templateVars("", "")

	// Primary volume mount: storage-disk's volumes/{name} → /workspace.
	var mounts []mount.Mount
	if mc.VolumeName != "" {
		vars["VOLUME_NAME"] = mc.VolumeName
		src := runtimecfg.Resolve(d.rtCfg.ManagedVolumeMount.Source, vars)
		sub := runtimecfg.Resolve(d.rtCfg.ManagedVolumeMount.Subpath, vars)
		ensureBindDir(d.rtCfg.ManagedVolumeMount.Type, src, d.dataDir)
		mounts = append(mounts, resolveMount(d.rtCfg.ManagedVolumeMount, src, "/workspace", sub))
	}

	// Extra mounts: additional volumes requested by the plugin.
	for _, em := range mc.GetExtraMounts() {
		if em.VolumeName == "" || em.Target == "" {
			continue
		}
		vars["VOLUME_NAME"] = em.VolumeName
		src := runtimecfg.Resolve(d.rtCfg.ManagedExtraMount.Source, vars)
		sub := runtimecfg.Resolve(d.rtCfg.ManagedExtraMount.Subpath, vars)
		ensureBindDir(d.rtCfg.ManagedExtraMount.Type, src, d.dataDir)
		m := resolveMount(d.rtCfg.ManagedExtraMount, src, em.Target, sub)
		m.ReadOnly = em.ReadOnly
		mounts = append(mounts, m)
	}

	// Plugin source mounting for managed containers.
	if mc.PluginSource != "" && d.rtCfg.ManagedPluginSource.Enabled {
		vars["PLUGIN_SOURCE"] = mc.PluginSource
		src := runtimecfg.Resolve(d.rtCfg.ManagedPluginSource.Source, vars)
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: src,
			Target: "/plugin-source",
		})
		log.Printf("managed container %s: mounting plugin source %s at /plugin-source", mc.ID, mc.PluginSource)

		for _, em := range d.rtCfg.ManagedPluginSource.ExtraMounts {
			esrc := runtimecfg.Resolve(em.Source, vars)
			mounts = append(mounts, resolveMount(em, esrc, em.Target, ""))
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

	log.Printf("started managed container %s (image=%s, volume=%s, subdomain=%s, plugin_source=%s)", mc.ID, mc.Image, mc.VolumeName, mc.Subdomain, mc.PluginSource)
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
