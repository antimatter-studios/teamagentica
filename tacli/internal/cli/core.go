package cli

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockertypes "github.com/docker/docker/api/types"
	dockerclient "github.com/docker/docker/client"
	"github.com/spf13/cobra"
)

var coreCmd = &cobra.Command{
	Use:   "core",
	Short: "Manage the TeamAgentica core (kernel + UI containers)",
}

func init() {
	rootCmd.AddCommand(coreCmd)
}

// listManagedContainers returns all containers with teamagentica.managed=true.
// Pass all=true to include stopped containers.
func listManagedContainers(ctx context.Context, docker *dockerclient.Client, all bool) ([]dockertypes.Container, error) {
	f := filters.NewArgs()
	f.Add("label", "teamagentica.managed=true")
	return docker.ContainerList(ctx, container.ListOptions{All: all, Filters: f})
}

// newDockerClient creates a Docker client from environment, returning a helpful error.
func newDockerClient() (*dockerclient.Client, error) {
	d, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker not available: %w", err)
	}
	return d, nil
}
