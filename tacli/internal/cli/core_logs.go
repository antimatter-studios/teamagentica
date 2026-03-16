package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/spf13/cobra"
)

var flagLogsUI bool

func init() {
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Tail logs from the kernel (or UI) container",
		RunE:  runCoreLogs,
	}
	cmd.Flags().BoolVar(&flagLogsUI, "ui", false, "show UI container logs instead of kernel")
	coreCmd.AddCommand(cmd)
}

func runCoreLogs(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	docker, err := newDockerClient()
	if err != nil {
		return err
	}
	defer docker.Close()

	role := "kernel"
	if flagLogsUI {
		role = "ui"
	}

	containers, err := listManagedContainers(ctx, docker, true)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	for _, c := range containers {
		if c.Labels["teamagentica.role"] != role {
			continue
		}
		out, err := docker.ContainerLogs(ctx, c.ID, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
			Tail:       "100",
		})
		if err != nil {
			return fmt.Errorf("logs: %w", err)
		}
		defer out.Close()
		_, err = io.Copy(os.Stdout, out)
		return err
	}

	return fmt.Errorf("no %s container found", role)
}
