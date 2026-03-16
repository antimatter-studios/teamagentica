package cli

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/spf13/cobra"
)

func init() {
	coreCmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start stopped kernel and UI containers",
		RunE:  runCoreStart,
	})
}

func runCoreStart(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	docker, err := newDockerClient()
	if err != nil {
		return err
	}
	defer docker.Close()

	// All=true to find stopped containers too.
	containers, err := listManagedContainers(ctx, docker, true)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	if len(containers) == 0 {
		fmt.Println("No core containers found — use 'tacli core create' to set one up")
		return nil
	}

	// Start kernel before UI.
	for _, role := range []string{"kernel", "ui"} {
		for _, c := range containers {
			if c.Labels["teamagentica.role"] != role {
				continue
			}
			if c.State == "running" {
				fmt.Printf("%s already running\n", cName(c))
				continue
			}
			fmt.Printf("Starting %s...", cName(c))
			if err := docker.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
				fmt.Printf(" failed: %v\n", err)
			} else {
				fmt.Println(" started")
			}
		}
	}
	return nil
}
