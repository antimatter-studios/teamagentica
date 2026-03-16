package runtime

import (
	"context"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// ContainerRuntime is the interface for managing plugin and workspace containers.
// Implementations include DockerRuntime, with future adapters for Podman, k8s,
// macOS Virtualization.framework, etc.
type ContainerRuntime interface {
	PullImage(ctx context.Context, imageRef string) error
	StartPlugin(ctx context.Context, plugin *models.Plugin, env map[string]string) (containerID string, err error)
	StartCandidatePlugin(ctx context.Context, plugin *models.Plugin, env map[string]string) (containerID string, err error)
	StopPlugin(ctx context.Context, containerID string) error
	StartManagedContainer(ctx context.Context, mc *models.ManagedContainer, baseDomain string) (containerID string, err error)
	HealthCheck(ctx context.Context, containerID string) (running bool, err error)
	ContainerLogs(ctx context.Context, containerID string, tail int) (string, error)
}
