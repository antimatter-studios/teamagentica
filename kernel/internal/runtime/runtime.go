package runtime

import (
	"context"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// ResolvedDiskMount is a disk mount with its host path already resolved via storage-disk.
type ResolvedDiskMount struct {
	HostPath string
	Target   string
	ReadOnly bool
}

// ContainerRuntime is the interface for managing plugin and workspace containers.
// Implementations include DockerRuntime, with future adapters for Podman, k8s,
// macOS Virtualization.framework, etc.
type ContainerRuntime interface {
	PullImage(ctx context.Context, imageRef string) error
	StartPlugin(ctx context.Context, plugin *models.Plugin, env map[string]string, diskPaths map[string]string) (containerID string, err error)
	StartCandidatePlugin(ctx context.Context, plugin *models.Plugin, env map[string]string, diskPaths map[string]string) (containerID string, err error)
	StopPlugin(ctx context.Context, containerID string) error
	// StopPluginPod stops and removes ALL containers belonging to a plugin pod
	// (api + sidecars), iterating over plugin.GetEffectiveContainers() and using
	// the deterministic container name. Safe to call when only some containers
	// exist; missing containers are treated as success.
	StopPluginPod(ctx context.Context, plugin *models.Plugin) error
	StartManagedContainer(ctx context.Context, mc *models.ManagedContainer, baseDomain string, diskMounts []ResolvedDiskMount) (containerID string, err error)
	HealthCheck(ctx context.Context, containerID string) (running bool, err error)
	// ResolveContainerID looks up the actual Docker container ID for a plugin
	// by its deterministic name (teamagentica-plugin-{pluginID}). Returns the
	// container ID and running state, or an error if no container exists.
	ResolveContainerID(ctx context.Context, pluginID string) (containerID string, running bool, err error)
	ContainerLogs(ctx context.Context, containerID string, tail int) (string, error)
	SelfContainerID() string
	UIContainerID(ctx context.Context) (string, error)
}
