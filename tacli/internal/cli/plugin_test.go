package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestExtractErrorMessage_JSONError(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "json error",
			input: `request failed (403): {"error":"system plugins cannot be disabled"}`,
			want:  "system plugins cannot be disabled",
		},
		{
			name:  "plain error",
			input: "connect: connection refused",
			want:  "connect: connection refused",
		},
		{
			name:  "json error with extra text",
			input: `request failed (400): {"error":"invalid plugin ID"}`,
			want:  "invalid plugin ID",
		},
		{
			name:  "malformed json",
			input: `request failed (400): {"error":"unclosed`,
			want:  `request failed (400): {"error":"unclosed`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractErrorMessage(errString(tt.input))
			if got != tt.want {
				t.Errorf("extractErrorMessage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// errString implements error interface for test purposes.
type errString string

func (e errString) Error() string { return string(e) }

func TestRootCommand_SubcommandExists(t *testing.T) {
	subcommands := map[string]bool{
		"plugin":      false,
		"plugins":     false,
		"marketplace": false,
		"connect":     false,
		"create":      false,
		"version":     false,
		"status":      false,
	}

	for _, cmd := range rootCmd.Commands() {
		if _, ok := subcommands[cmd.Name()]; ok {
			subcommands[cmd.Name()] = true
		}
	}

	for name, found := range subcommands {
		if !found {
			t.Errorf("expected subcommand %q not found", name)
		}
	}
}

func TestRootCommand_PluginVsPlugins(t *testing.T) {
	var pluginFound, pluginsFound bool
	for _, cmd := range rootCmd.Commands() {
		switch cmd.Name() {
		case "plugin":
			pluginFound = true
			// plugin command should accept --list
			if cmd.Flags().Lookup("list") == nil {
				t.Error("plugin command missing --list flag")
			}
		case "plugins":
			pluginsFound = true
			// plugins command accepts --list as a no-op alias
			if cmd.Flags().Lookup("list") == nil {
				t.Error("plugins command should accept --list flag")
			}
		}
	}
	if !pluginFound {
		t.Error("missing 'plugin' subcommand")
	}
	if !pluginsFound {
		t.Error("missing 'plugins' subcommand")
	}
}

func TestPluginCommand_Flags(t *testing.T) {
	var cmd *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "plugin" {
			cmd = c
			break
		}
	}
	if cmd == nil {
		t.Fatal("plugin command not found")
	}

	expectedFlags := []string{
		"list", "enable", "disable", "restart", "uninstall",
		"config", "schema", "enable-all", "disable-all",
		"uninstall-all", "force",
	}
	for _, flag := range expectedFlags {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("plugin command missing --%s flag", flag)
		}
	}
}

func TestMarketplaceCommand_Flags(t *testing.T) {
	var cmd *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "marketplace" {
			cmd = c
			break
		}
	}
	if cmd == nil {
		t.Fatal("marketplace command not found")
	}

	expectedFlags := []string{
		"add", "remove", "install", "list", "plugins",
	}
	for _, flag := range expectedFlags {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("marketplace command missing --%s flag", flag)
		}
	}
}
