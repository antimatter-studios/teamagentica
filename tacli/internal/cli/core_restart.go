package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
)

func init() {
	coreCmd.AddCommand(&cobra.Command{
		Use:   "restart",
		Short: "Recreate the kernel container from the active profile",
		RunE:  runCoreRestart,
	})
}

func runCoreRestart(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	profile := cfg.Active()
	if profile == nil || profile.Kernel.Image == "" {
		return fmt.Errorf("no kernel state in profile — run 'tacli core create' first")
	}
	ks := profile.Kernel

	ctx := context.Background()
	docker, err := newDockerClient()
	if err != nil {
		return err
	}
	defer docker.Close()

	networkName := ks.NetworkName
	if networkName == "" {
		networkName = "teamagentica"
	}

	// Stop and remove existing kernel container.
	fmt.Print("Stopping kernel...")
	timeout := 10
	_ = docker.ContainerStop(ctx, "teamagentica-kernel", container.StopOptions{Timeout: &timeout})
	fmt.Print(" removing...")
	_ = docker.ContainerRemove(ctx, "teamagentica-kernel", container.RemoveOptions{Force: true})
	fmt.Println(" done")

	// Recreate from profile.
	hostPort := strconv.Itoa(ks.Port)
	fmt.Printf("Starting kernel on port %s...", hostPort)

	labels := map[string]string{
		"teamagentica.managed": "true",
		"teamagentica.role":    "kernel",
	}
	for k, v := range ks.Labels {
		labels[k] = v
	}

	resp, err := docker.ContainerCreate(ctx,
		&container.Config{
			Image:        ks.Image,
			Hostname:     "teamagentica-kernel",
			Env:          buildKernelEnv(ks, networkName),
			ExposedPorts: nat.PortSet{"8080/tcp": struct{}{}, "8081/tcp": struct{}{}},
			Labels:       labels,
		},
		&container.HostConfig{
			PortBindings: nat.PortMap{
				"8080/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: hostPort}},
			},
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"},
				{Type: mount.TypeBind, Source: ks.DataDir, Target: "/data"},
			},
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{networkName: {}},
		},
		nil, "teamagentica-kernel",
	)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	if err := docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	fmt.Println(" started")
	return nil
}
