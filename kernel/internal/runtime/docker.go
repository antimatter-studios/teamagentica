package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"bytes"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
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
// e.g. "teamagentica-agent-anthropic:dev" → "agent-anthropic"
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

// selfHostedRegistry is the Docker-network address of the container registry plugin.
// Container names are deterministic: "teamagentica-plugin-{id}".
const selfHostedRegistry = "teamagentica-plugin-infra-container-registry:5000"

// PullImage pulls a Docker image if it doesn't exist locally.
// For teamagentica-* images, it tries the self-hosted container registry first,
// falling back to the local daemon / Docker Hub.
func (d *DockerRuntime) PullImage(ctx context.Context, imageRef string) error {
	// Check if image exists locally first.
	if _, _, err := d.client.ImageInspectWithRaw(ctx, imageRef); err == nil {
		return nil
	}

	// For teamagentica images, try pulling from the self-hosted registry first.
	if strings.HasPrefix(imageRef, "teamagentica-") {
		registryRef := selfHostedRegistry + "/" + imageRef
		reader, err := d.client.ImagePull(ctx, registryRef, image.PullOptions{})
		if err == nil {
			_, _ = io.Copy(io.Discard, reader)
			reader.Close()
			// Re-tag to the expected local name so ContainerCreate finds it.
			if err := d.client.ImageTag(ctx, registryRef, imageRef); err == nil {
				log.Printf("pulled %s from self-hosted registry", imageRef)
				return nil
			}
		}
		log.Printf("self-hosted registry pull failed for %s, falling back to default: %v", imageRef, err)
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

// StartPlugin creates and starts the plugin's pod of containers. Returns the
// api container's Docker ID (kernel uses it as the canonical ContainerID for
// back-compat with single-container code paths). All container IDs are also
// persisted on the plugin via SetContainerIDs.
//
// Multi-container plugins are described by plugin.GetEffectiveContainers().
// Legacy single-container plugins synthesize a single api container from the
// top-level Image / DockerLabels / ExtraPorts fields, so this path handles
// both shapes uniformly.
//
// TODO: K8s adapter — a teamagentica plugin maps 1:1 to a native pod, so the
// adapter should translate ContainerSpec[] into pod.spec.containers[] and the
// kernel-managed network membership into pod metadata only.
func (d *DockerRuntime) StartPlugin(ctx context.Context, plugin *models.Plugin, env map[string]string, diskPaths map[string]string) (string, error) {
	specs := plugin.GetEffectiveContainers()
	if len(specs) == 0 {
		return "", fmt.Errorf("plugin %s has no container specs", plugin.ID)
	}

	// Pre-compute the api container name so we know which one carries the env
	// vars (mTLS certs, AGENT_*, etc.) and whose ID gets returned.
	apiName := plugin.APIContainerName()

	// Stop+remove any stale containers for this plugin (crashed, orphaned, etc.)
	// before starting fresh ones. Iterate over all known names.
	for _, s := range specs {
		stale := podContainerName(plugin.ID, s.Name)
		d.client.ContainerRemove(ctx, stale, container.RemoveOptions{Force: true})
	}

	createdIDs := map[string]string{}
	var apiContainerID string

	// Start order: declaration order. Sidecars typically need the api or a
	// shared service to come up — we don't sequence by role because plugin
	// authors should declare them in dependency order.
	for _, spec := range specs {
		isAPI := spec.Name == apiName
		// Only the api container gets the kernel env (mTLS certs, AGENT_*, etc.).
		// Sidecars receive a minimal env with the api container's hostname so
		// they can call the api by name on the shared docker network.
		var cEnv map[string]string
		if isAPI {
			cEnv = env
		} else {
			cEnv = map[string]string{
				"PLUGIN_ID":          plugin.ID,
				"PLUGIN_API_HOST":    podContainerName(plugin.ID, apiName),
				"PLUGIN_CONTAINER":   spec.Name,
			}
			for k, v := range d.rtCfg.PluginEnv {
				cEnv[k] = v
			}
		}

		id, err := d.startContainerInPod(ctx, plugin, spec, isAPI, cEnv, diskPaths)
		if err != nil {
			// Roll back anything we already started.
			for _, started := range createdIDs {
				_ = d.client.ContainerRemove(ctx, started, container.RemoveOptions{Force: true})
			}
			return "", fmt.Errorf("start container %s/%s: %w", plugin.ID, spec.Name, err)
		}
		createdIDs[spec.Name] = id
		if isAPI {
			apiContainerID = id
		}
		log.Printf("plugin %s: started container %s (id=%s, role=%s)", plugin.ID, spec.Name, id[:12], spec.Role)
	}

	// Persist the per-container ID map. The api container ID is also returned
	// (the caller writes it into Plugin.ContainerID for back-compat).
	plugin.SetContainerIDs(createdIDs)

	return apiContainerID, nil
}

// startContainerInPod creates and starts a single container belonging to a
// plugin pod. The api container receives mTLS certs + agent env; sidecars
// receive only the minimal cEnv passed in.
func (d *DockerRuntime) startContainerInPod(ctx context.Context, plugin *models.Plugin, spec models.ContainerSpec, isAPI bool, env map[string]string, diskPaths map[string]string) (string, error) {
	containerName := podContainerName(plugin.ID, spec.Name)

	// Remove any stale container with the same name (crashed, orphaned, etc.).
	d.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	// Pick image: dev variant when DevMode is on AND the spec defines one.
	imageRef := spec.Image
	if plugin.DevMode && spec.DevImage != "" {
		imageRef = spec.DevImage
		log.Printf("plugin %s: container %s using dev image %s", plugin.ID, spec.Name, imageRef)
	}

	// --- env injection ---
	// Only the api container gets cert mounts and agent identity vars. Sidecars
	// get whatever env the caller built for them.
	var certMount *mount.Mount
	if isAPI && d.certManager != nil {
		_, _, _, err := d.certManager.GeneratePluginCert(plugin.ID)
		if err != nil {
			return "", fmt.Errorf("generate plugin cert: %w", err)
		}

		certDir := d.certManager.GetPluginCertDir(plugin.ID)
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

	if isAPI {
		sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
		env["AGENT_PLUGIN_ID"] = plugin.ID
		env["AGENT_PROJECT_ID"] = "default"
		env["AGENT_INSTANCE_ID"] = containerName
		env["AGENT_PRINCIPAL"] = "agent:default:" + containerName
		env["AGENT_TYPE"] = deriveAgentType(plugin.GetCapabilities())
		env["AGENT_SESSION_ID"] = sessionID

		for k, v := range d.rtCfg.PluginEnv {
			env[k] = v
		}
	}

	// Group containers under the kernel in Docker Desktop. Each container in
	// a pod still shares the same compose project so they cluster together.
	labels := map[string]string{
		"com.docker.compose.project": "teamagentica",
		"com.docker.compose.service": plugin.ID + "-" + spec.Name,
	}

	// Workspace-editor public subdomain wiring — only on api container.
	if isAPI && hasCapabilityPrefix(plugin.GetCapabilities(), "workspace:editor") && d.baseDomain != "" {
		subdomain := "code." + d.baseDomain
		proxyName := "teamagentica-" + plugin.ID
		pluginPort := env["INFRA_CODE_SERVER_PORT"]
		if pluginPort == "" {
			pluginPort = "8092"
		}
		labels["docker-proxy."+proxyName+".host"] = subdomain
		labels["docker-proxy."+proxyName+".port"] = pluginPort
		env["TEAMAGENTICA_PUBLIC_HOST"] = subdomain
		env["TEAMAGENTICA_BASE_DOMAIN"] = d.baseDomain
	}

	var envSlice []string
	for k, v := range env {
		envSlice = append(envSlice, k+"="+v)
	}

	// Container-spec docker labels (with ${ENV_VAR} substitution).
	for k, v := range spec.DockerLabels {
		resolvedKey := substituteEnvVars(k)
		resolvedVal := substituteEnvVars(v)
		if _, exists := labels[resolvedKey]; exists {
			log.Printf("plugin %s container %s: docker label %q conflicts with kernel-internal label, skipping plugin value", plugin.ID, spec.Name, resolvedKey)
			continue
		}
		labels[resolvedKey] = resolvedVal
	}

	cfg := &container.Config{
		Image:    imageRef,
		Hostname: containerName,
		Env:      envSlice,
		Labels:   labels,
	}

	// Container-spec extra ports.
	var portBindings nat.PortMap
	if len(spec.Ports) > 0 {
		cfg.ExposedPorts = nat.PortSet{}
		portBindings = nat.PortMap{}
		for _, ep := range spec.Ports {
			if ep.Internal <= 0 {
				log.Printf("plugin %s container %s: port entry has invalid internal port %d, skipping", plugin.ID, spec.Name, ep.Internal)
				continue
			}
			natPort, err := nat.NewPort("tcp", fmt.Sprintf("%d", ep.Internal))
			if err != nil {
				log.Printf("plugin %s container %s: invalid port %d: %v", plugin.ID, spec.Name, ep.Internal, err)
				continue
			}
			cfg.ExposedPorts[natPort] = struct{}{}
			binding := nat.PortBinding{HostIP: "0.0.0.0"}
			if ep.External > 0 {
				binding.HostPort = fmt.Sprintf("%d", ep.External)
			}
			portBindings[natPort] = []nat.PortBinding{binding}
		}
	}

	vars := d.templateVars(plugin.ID, pluginDir(imageRef))

	mounts := []mount.Mount{}
	// Data mount + cert + plugin runtime mounts only attach to the api container.
	// Sidecars get a minimal filesystem; they can mount via shared_disks if needed.
	if isAPI {
		dataSource := runtimecfg.Resolve(d.rtCfg.DataMount.Source, vars)
		ensureBindDir(d.rtCfg.DataMount.Type, dataSource, d.dataDir)
		dataMount := resolveMount(
			runtimecfg.MountSpec{Type: d.rtCfg.DataMount.Type},
			dataSource, "/data", "",
		)
		mounts = append(mounts, dataMount)
		if certMount != nil {
			mounts = append(mounts, *certMount)
		}

		for _, pm := range d.rtCfg.PluginMounts {
			src := runtimecfg.Resolve(pm.Source, vars)
			tgt := pm.Target
			sub := runtimecfg.Resolve(pm.Subpath, vars)
			mounts = append(mounts, resolveMount(pm, src, tgt, sub))
		}
		if len(d.rtCfg.PluginMounts) > 0 {
			log.Printf("mounting %d extra mount(s) for plugin %s api container", len(d.rtCfg.PluginMounts), plugin.ID)
		}

		for _, sd := range plugin.GetSharedDisks() {
			if sd.Name == "" || sd.Target == "" {
				continue
			}
			hostPath, ok := diskPaths[sd.Name]
			if !ok {
				return "", fmt.Errorf("shared disk %q declared by plugin %s could not be resolved — storage-disk may be unavailable", sd.Name, plugin.ID)
			}
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeBind,
				Source: hostPath,
				Target: sd.Target,
			})
			log.Printf("mounting shared disk %s into plugin %s at %s (host=%s)", sd.Name, plugin.ID, sd.Target, hostPath)
		}

		if hasCapabilityPrefix(plugin.GetCapabilities(), "build:docker") {
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeBind,
				Source: "/var/run/docker.sock",
				Target: "/var/run/docker.sock",
			})
			log.Printf("mounting docker.sock into plugin %s (build:docker capability)", plugin.ID)
		}
	}

	// Dev bind mounts: substitute {{TEAMAGENTICA_REPO}} and validate host paths
	// exist before letting Docker auto-create empty dirs as root.
	if plugin.DevMode && len(spec.DevBindMounts) > 0 {
		repo := os.Getenv("TEAMAGENTICA_REPO")
		if repo == "" {
			log.Printf("plugin %s container %s: dev_bind_mounts requested but TEAMAGENTICA_REPO is not set — skipping dev mounts", plugin.ID, spec.Name)
		} else {
			for _, bm := range spec.DevBindMounts {
				host := strings.ReplaceAll(bm.Host, "{{TEAMAGENTICA_REPO}}", repo)
				if _, err := os.Stat(host); err != nil {
					return "", fmt.Errorf("dev bind mount host path %q does not exist: %w (refusing to let docker create as root)", host, err)
				}
				mounts = append(mounts, mount.Mount{
					Type:     mount.TypeBind,
					Source:   host,
					Target:   bm.Container,
					ReadOnly: bm.ReadOnly,
				})
				log.Printf("plugin %s container %s: dev bind %s -> %s", plugin.ID, spec.Name, host, bm.Container)
			}
		}
	}

	hostCfg := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		Mounts:        mounts,
	}
	if len(portBindings) > 0 {
		hostCfg.PortBindings = portBindings
	}

	// All containers in a pod join the same kernel-managed bridge network so
	// they can resolve each other by container name (e.g. http://api:8080).
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

// podContainerName builds the deterministic Docker container name for a
// container inside a plugin pod. Single-container plugins use container name
// "default" so the resulting name stays compatible with the legacy
// "teamagentica-plugin-<id>" convention via a suffix.
func podContainerName(pluginID, containerName string) string {
	if containerName == "" || containerName == "default" {
		// Backward compat: legacy single-container plugins kept this name.
		return "teamagentica-plugin-" + pluginID
	}
	return "teamagentica-plugin-" + pluginID + "-" + containerName
}

// StartCandidatePlugin starts a candidate container alongside the primary.
// Uses the same logic as StartPlugin but with a "-candidate" container name suffix
// so both containers can coexist on the same network.
func (d *DockerRuntime) StartCandidatePlugin(ctx context.Context, plugin *models.Plugin, env map[string]string, diskPaths map[string]string) (string, error) {
	// Temporarily override the ID to get a different container name,
	// then restore it. The container name becomes "teamagentica-plugin-{id}-candidate".
	origID := plugin.ID
	plugin.ID = origID + "-candidate"
	defer func() { plugin.ID = origID }()

	return d.StartPlugin(ctx, plugin, env, diskPaths)
}

// StopPluginPod stops every container in a plugin's pod (api + sidecars) in
// reverse declaration order so sidecars depending on the api shut down first.
// Containers that don't exist (already removed/crashed) are treated as success.
//
// TODO: K8s adapter — pod deletion is a single API call.
func (d *DockerRuntime) StopPluginPod(ctx context.Context, plugin *models.Plugin) error {
	specs := plugin.GetEffectiveContainers()
	if len(specs) == 0 {
		// Fall back to ContainerID for plugins with no spec (defensive).
		if plugin.ContainerID != "" {
			return d.StopPlugin(ctx, plugin.ContainerID)
		}
		return nil
	}
	var firstErr error
	// Reverse order: stop sidecars first, then the api container last.
	for i := len(specs) - 1; i >= 0; i-- {
		name := podContainerName(plugin.ID, specs[i].Name)
		if err := d.client.ContainerStop(ctx, name, container.StopOptions{}); err != nil {
			if !errdefs.IsNotFound(err) {
				log.Printf("warning: container stop %s: %v", name, err)
			}
		}
		if err := d.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil {
			if !errdefs.IsNotFound(err) {
				if firstErr == nil {
					firstErr = fmt.Errorf("remove container %s: %w", name, err)
				}
			}
		}
	}
	plugin.SetContainerIDs(nil)
	return firstErr
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
func (d *DockerRuntime) StartManagedContainer(ctx context.Context, mc *models.ManagedContainer, baseDomain string, diskMounts []ResolvedDiskMount) (string, error) {
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

	// Disk mounts: pre-resolved host paths from storage-disk API.
	var mounts []mount.Mount
	for _, dm := range diskMounts {
		m := mount.Mount{
			Type:     mount.TypeBind,
			Source:   dm.HostPath,
			Target:   dm.Target,
			ReadOnly: dm.ReadOnly,
		}
		mounts = append(mounts, m)
		log.Printf("managed container %s: mounting disk %s at %s", mc.ID, dm.HostPath, dm.Target)
	}

	// Template vars for plugin source resolution.
	vars := d.templateVars("", "")

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

	log.Printf("started managed container %s (image=%s, disks=%d, subdomain=%s, plugin_source=%s)", mc.ID, mc.Image, len(diskMounts), mc.Subdomain, mc.PluginSource)
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

// ResolveContainerID looks up the actual container ID for a plugin by its
// deterministic name. This reconciles stale container IDs after kernel restarts
// where containers were recreated with new Docker IDs.
func (d *DockerRuntime) ResolveContainerID(ctx context.Context, pluginID string) (string, bool, error) {
	containerName := "teamagentica-plugin-" + pluginID
	info, err := d.client.ContainerInspect(ctx, containerName)
	if err != nil {
		return "", false, err
	}
	return info.ID, info.State.Running, nil
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

// envVarRefRe matches ${VAR_NAME} references in label keys/values.
var envVarRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// substituteEnvVars replaces ${VAR} references in s with values from the
// kernel's process env. Missing vars are best-effort: replaced with the empty
// string and a warning is logged.
func substituteEnvVars(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return envVarRefRe.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		val, ok := os.LookupEnv(name)
		if !ok {
			log.Printf("docker labels: env var %q referenced but not set, substituting empty string", name)
			return ""
		}
		return val
	})
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

func deriveAgentType(caps []string) string {
	for _, c := range caps {
		if strings.HasPrefix(c, "agent:") {
			return strings.TrimPrefix(c, "agent:")
		}
	}
	for _, c := range caps {
		if strings.HasPrefix(c, "infra:") {
			return "infra"
		}
	}
	return "plugin"
}
