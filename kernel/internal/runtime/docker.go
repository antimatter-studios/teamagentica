package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"roboslop/kernel/internal/certs"
	"roboslop/kernel/internal/models"
)

// DockerRuntime manages plugin containers via the Docker API.
type DockerRuntime struct {
	client      *client.Client
	network     string
	certManager *certs.CertManager
}

// NewDockerRuntime creates a Docker client from environment and ensures the
// network exists. Returns nil runtime (not an error) if Docker is unavailable,
// so the kernel can still start without Docker.
// The certManager parameter is optional; pass nil to disable mTLS cert injection.
func NewDockerRuntime(networkName string, certManager *certs.CertManager) (*DockerRuntime, error) {
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
	containerName := "roboslop-plugin-" + plugin.ID
	volumeName := "roboslop-data-" + plugin.ID

	// If cert manager is available, generate plugin certs and inject via env + mount.
	var certMount *mount.Mount
	if d.certManager != nil {
		_, _, _, err := d.certManager.GeneratePluginCert(plugin.ID)
		if err != nil {
			return "", fmt.Errorf("generate plugin cert: %w", err)
		}

		certDir := d.certManager.GetPluginCertDir(plugin.ID)

		env["ROBOSLOP_TLS_CERT"] = "/certs/" + plugin.ID + ".crt"
		env["ROBOSLOP_TLS_KEY"] = "/certs/" + plugin.ID + ".key"
		env["ROBOSLOP_TLS_CA"] = "/certs/ca.crt"
		env["ROBOSLOP_TLS_ENABLED"] = "true"

		certMount = &mount.Mount{
			Type:     mount.TypeBind,
			Source:   certDir,
			Target:   "/certs",
			ReadOnly: true,
		}
	}

	// Build env slice.
	var envSlice []string
	for k, v := range env {
		envSlice = append(envSlice, k+"="+v)
	}

	cfg := &container.Config{
		Image: plugin.Image,
		Env:   envSlice,
	}

	mounts := []mount.Mount{
		{
			Type:   mount.TypeVolume,
			Source: volumeName,
			Target: "/data",
		},
	}
	if certMount != nil {
		mounts = append(mounts, *certMount)
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
func (d *DockerRuntime) StopPlugin(ctx context.Context, containerID string) error {
	if err := d.client.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		log.Printf("warning: container stop %s: %v", containerID, err)
	}
	if err := d.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove container %s: %w", containerID, err)
	}
	return nil
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

	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}

	// Docker log stream includes an 8-byte header per frame; strip them for
	// readability when possible.
	return stripDockerLogHeaders(string(data)), nil
}

// stripDockerLogHeaders removes the 8-byte Docker log frame headers.
func stripDockerLogHeaders(raw string) string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if len(line) > 8 {
			lines = append(lines, line[8:])
		} else if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
