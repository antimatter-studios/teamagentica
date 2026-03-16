package cli

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/spf13/cobra"
)

var flagDeleteForce bool

func init() {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Stop and remove the kernel and UI containers",
		Long: `Stop and remove all core containers and the teamagentica Docker network.

Data stored in your data directory is NOT deleted — only containers are removed.
Use --force to skip the confirmation prompt.`,
		RunE: runCoreDelete,
	}
	cmd.Flags().BoolVar(&flagDeleteForce, "force", false, "skip confirmation prompt")
	coreCmd.AddCommand(cmd)
}

func runCoreDelete(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	docker, err := newDockerClient()
	if err != nil {
		return err
	}
	defer docker.Close()

	containers, err := listManagedContainers(ctx, docker, true)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	if len(containers) == 0 {
		fmt.Println("No core containers found")
		return nil
	}

	if !flagDeleteForce {
		details := []string{"Containers to be removed:"}
		for _, c := range containers {
			details = append(details, "  - "+cName(c))
		}
		details = append(details, "", "Your data directory is NOT deleted. Plugins and their data remain intact.")
		if err := requireConfirmation("This will stop and remove the core kernel and UI containers.", details...); err != nil {
			return err
		}
	}

	timeout := 10
	// Stop and remove UI before kernel.
	for _, role := range []string{"ui", "kernel"} {
		for _, c := range containers {
			if c.Labels["teamagentica.role"] != role {
				continue
			}
			name := cName(c)
			fmt.Printf("Stopping %s...", name)
			_ = docker.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeout})
			fmt.Print(" removing...")
			if err := docker.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				fmt.Printf(" failed: %v\n", err)
			} else {
				fmt.Println(" done")
			}
		}
	}

	// Remove the network (ignore error if other containers are still using it).
	fmt.Print("Removing network teamagentica...")
	if err := docker.NetworkRemove(ctx, "teamagentica"); err != nil {
		fmt.Printf(" skipped (%v)\n", err)
	} else {
		fmt.Println(" done")
	}

	fmt.Println("\nCore removed. Run 'tacli core create' to start fresh.")
	return nil
}
