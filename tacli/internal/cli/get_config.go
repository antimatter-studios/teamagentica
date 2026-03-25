package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/antimatter-studios/teamagentica/tacli/internal/config"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "get-config",
		Short: "Print the config file path and contents",
		RunE:  runGetConfig,
	})
}

func runGetConfig(cmd *cobra.Command, args []string) error {
	p := config.ConfigPath()
	fmt.Println(p)

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("{}")
			return nil
		}
		return err
	}

	// Pretty-print with jq if available, otherwise raw output.
	if jq, err := exec.LookPath("jq"); err == nil {
		c := exec.Command(jq, ".")
		c.Stdin = bytes.NewReader(data)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}

	os.Stdout.Write(data)
	fmt.Println()
	return nil
}
