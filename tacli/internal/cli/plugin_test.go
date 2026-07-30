package cli

import (
	"strings"
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

// The tests below describe the command tree as the CLI actually builds it.
//
// They previously asserted a flag-based interface — `plugin --list`,
// `plugin --enable-all`, `marketplace --install` — and a `plugins` command
// alongside `plugin`. The CLI expresses all of that as subcommands instead
// (`plugin list`, `plugin enable --all`, `marketplace install-plugin`), with
// `plugins` as an alias of `plugin`, so every one of those assertions failed on a
// clean checkout. The operations they were checking for all exist; only the shape
// was wrong. Each test below keeps the original intent and asserts the real shape.

// mustFind resolves a command path the way cobra does when parsing argv, which is
// what makes it the right tool here: it follows aliases, so `plugins` resolves to
// the `plugin` command, and walking rootCmd.Commands() by name would miss that.
//
// Anything in the path that is not a command comes back in the remaining args
// rather than as an error, so that is what gets checked.
func mustFind(t *testing.T, path ...string) *cobra.Command {
	t.Helper()
	cmd, rest, err := rootCmd.Find(path)
	if err != nil {
		t.Fatalf("tacli %s: %v", strings.Join(path, " "), err)
	}
	if len(rest) > 0 {
		t.Fatalf("tacli %s: %q is not a command", strings.Join(path, " "), rest[0])
	}
	return cmd
}

func TestRootCommand_SubcommandExists(t *testing.T) {
	// One entry per top-level area the CLI exposes. `create` is deliberately
	// absent: it is `core create`, not a root command, and is covered below.
	for _, name := range []string{
		"plugin", "marketplace", "connect", "core",
		"console", "profile", "get-config", "version", "status",
	} {
		t.Run(name, func(t *testing.T) {
			if got := mustFind(t, name); got.Name() != name {
				t.Errorf("tacli %s resolved to %q", name, got.Name())
			}
		})
	}
}

func TestRootCommand_PluginVsPlugins(t *testing.T) {
	// `plugins` is an alias, not a second command, so both spellings must land on
	// the same *cobra.Command.
	plugin := mustFind(t, "plugin")
	plugins := mustFind(t, "plugins")

	if plugin != plugins {
		t.Errorf("tacli plugins resolved to %q, want the same command as tacli plugin", plugins.Name())
	}

	// Bare `tacli plugin` lists, which is what the old --list flag meant.
	if plugin.RunE == nil {
		t.Error("plugin command has no RunE, so bare `tacli plugin` does nothing")
	}
}

func TestPluginCommand_Subcommands(t *testing.T) {
	for _, name := range []string{
		"list", "enable", "disable", "restart", "uninstall",
		"config", "schema", "candidate", "promote", "rollback", "dev-mode",
	} {
		t.Run(name, func(t *testing.T) { mustFind(t, "plugin", name) })
	}

	// The bulk operations are an --all flag on the individual verbs, rather than
	// the separate enable-all/disable-all/uninstall-all the old test expected.
	for _, name := range []string{"enable", "disable", "restart", "uninstall"} {
		t.Run(name+" --all", func(t *testing.T) {
			if mustFind(t, "plugin", name).Flags().Lookup("all") == nil {
				t.Errorf("plugin %s missing --all flag", name)
			}
		})
	}

	// --force skips the confirmation prompt, and only the destructive verbs have it.
	for _, name := range []string{"disable", "uninstall"} {
		t.Run(name+" --force", func(t *testing.T) {
			if mustFind(t, "plugin", name).Flags().Lookup("force") == nil {
				t.Errorf("plugin %s missing --force flag", name)
			}
		})
	}

	// config nests further.
	for _, name := range []string{"set", "get"} {
		t.Run("config "+name, func(t *testing.T) { mustFind(t, "plugin", "config", name) })
	}
}

func TestMarketplaceCommand_Subcommands(t *testing.T) {
	// The old test looked for --add/--remove/--install/--list/--plugins. Each is a
	// subcommand, and named for what it acts on, since providers and plugins are
	// both added and removed here.
	for _, name := range []string{
		"list-providers", "add-provider", "remove-provider",
		"list-plugins", "submit-plugin", "remove-plugin",
		"install-plugin", "upgrade-plugin",
	} {
		t.Run(name, func(t *testing.T) { mustFind(t, "marketplace", name) })
	}
}

func TestCoreCommand_Subcommands(t *testing.T) {
	// `create` was expected at the root by the old test. It lives under core,
	// with the rest of the lifecycle verbs.
	for _, name := range []string{
		"create", "delete", "start", "stop", "restart", "logs", "config",
	} {
		t.Run(name, func(t *testing.T) { mustFind(t, "core", name) })
	}
}
