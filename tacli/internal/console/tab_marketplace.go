package console

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

// ── messages ──────────────────────────────────────────────────────────────────

type catalogMsg struct {
	plugins []client.CatalogPlugin
	err     error
}

type installMsg struct {
	pluginID string
	err      error
}

// ── tab ───────────────────────────────────────────────────────────────────────

type marketplaceTab struct {
	c            *client.Client
	catalog      []client.CatalogPlugin
	installedIDs map[string]bool
	cursor       int
	loading      bool
	status       string
	err          string
}

func newMarketplaceTab(c *client.Client) marketplaceTab {
	return marketplaceTab{
		c:            c,
		installedIDs: make(map[string]bool),
	}
}

func (t marketplaceTab) update(msg tea.Msg) (marketplaceTab, tea.Cmd) {
	switch msg := msg.(type) {
	case pluginsMsg:
		if msg.err == nil {
			t.installedIDs = make(map[string]bool, len(msg.plugins))
			for _, p := range msg.plugins {
				t.installedIDs[p.ID] = true
			}
			// trigger initial catalog load on first plugins arrival
			if !t.loading && len(t.catalog) == 0 {
				t.loading = true
				return t, doFetchCatalog(t.c)
			}
		}

	case catalogMsg:
		t.loading = false
		if msg.err != nil {
			t.err = msg.err.Error()
		} else {
			t.catalog = msg.plugins
			t.err = ""
		}
		if t.cursor >= len(t.catalog) {
			t.cursor = max(0, len(t.catalog)-1)
		}

	case installMsg:
		t.loading = false
		if msg.err != nil {
			t.status = "install failed: " + msg.err.Error()
		} else {
			t.status = msg.pluginID + " installed"
			t.installedIDs[msg.pluginID] = true
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if t.cursor < len(t.catalog)-1 {
				t.cursor++
			}
		case "k", "up":
			if t.cursor > 0 {
				t.cursor--
			}
		case "r":
			if !t.loading {
				t.loading = true
				t.status = ""
				t.err = ""
				return t, doFetchCatalog(t.c)
			}
		case "i":
			if p := t.selectedCatalog(); p != nil {
				if t.installedIDs[p.PluginID] {
					t.status = p.Name + " is already installed"
				} else {
					t.loading = true
					t.status = "installing " + p.Name + "…"
					return t, doInstallPlugin(t.c, p.PluginID)
				}
			}
		}
	}

	return t, nil
}

func (t marketplaceTab) view(width, height int) string {
	innerW := width - 4
	innerH := height - 2

	lines := []string{
		sBold.Render(" Marketplace"),
		sep(innerW),
	}

	if t.loading && len(t.catalog) == 0 {
		lines = append(lines, sDim.Render("  loading catalog…"))
	} else if t.err != "" {
		lines = append(lines, sErr.Render("  error: "+t.err))
	} else if len(t.catalog) == 0 {
		lines = append(lines, sDim.Render("  no plugins in catalog  (r to refresh)"))
	} else {
		nameW := innerW - 42
		if nameW < 16 {
			nameW = 16
		}
		for i, p := range t.catalog {
			installed := "   "
			if t.installedIDs[p.PluginID] {
				installed = sOK.Render("[✓]")
			}
			name := trunc(p.Name, nameW)
			desc := trunc(p.Description, 28)
			ver := trunc(p.Version, 8)
			line := fmt.Sprintf("  %s %-*s %-28s %s", installed, nameW, name, desc, sDim.Render(ver))
			if i == t.cursor {
				line = sSel.Render(pad(line, innerW))
			}
			lines = append(lines, line)
		}
	}

	// status line
	if t.status != "" {
		for len(lines) < innerH-1 {
			lines = append(lines, "")
		}
		lines = append(lines, sMuted.Render("  "+trunc(t.status, innerW-2)))
	}

	// loading indicator when refreshing existing catalog
	if t.loading && len(t.catalog) > 0 {
		hdr := " " + sBold.Render("Marketplace") + "  " + sDim.Render("refreshing…")
		lines[0] = hdr
	}

	content := buildContent(lines, innerH, innerW)
	return "\n " + renderBox(content, innerW, true)
}

func (t marketplaceTab) selectedCatalog() *client.CatalogPlugin {
	if len(t.catalog) == 0 || t.cursor >= len(t.catalog) {
		return nil
	}
	p := t.catalog[t.cursor]
	return &p
}

func (t marketplaceTab) helpLine() string {
	return "j/k: navigate  i: install  r: refresh"
}

// ── commands ──────────────────────────────────────────────────────────────────

func doFetchCatalog(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		result, err := c.BrowsePlugins()
		if err != nil {
			return catalogMsg{err: err}
		}
		return catalogMsg{plugins: result.Plugins}
	}
}

func doInstallPlugin(c *client.Client, pluginID string) tea.Cmd {
	return func() tea.Msg {
		_, err := c.InstallPlugin(pluginID)
		return installMsg{pluginID: pluginID, err: err}
	}
}

