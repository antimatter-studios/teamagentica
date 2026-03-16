package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/spf13/cobra"
)

func init() {
	coreCmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the kernel and UI containers",
		RunE:  runCoreStop,
	})
}

func runCoreStop(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	docker, err := newDockerClient()
	if err != nil {
		return err
	}
	defer docker.Close()

	containers, err := listManagedContainers(ctx, docker, false)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	if len(containers) == 0 {
		fmt.Println("No running core containers found")
		return nil
	}

	// Stop UI before kernel.
	for _, role := range []string{"ui", "kernel"} {
		for _, c := range containers {
			if c.Labels["teamagentica.role"] == role {
				fmt.Printf("Stopping %s...", cName(c))
				timeout := 10
				if err := docker.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeout}); err != nil {
					fmt.Printf(" failed: %v\n", err)
				} else {
					fmt.Println(" stopped")
				}
			}
		}
	}
	return nil
}

// cName returns a clean display name for a container (strips leading /).
func cName(c dockertypes.Container) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID[:12]
}
