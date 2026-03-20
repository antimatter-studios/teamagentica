package console

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

// ── messages ──────────────────────────────────────────────────────────────────

type catalogMsg struct {
	plugins []client.CatalogPlugin
	err     error
}

type installMsg struct {
	pluginID  string
	installed []client.PluginSummary
	err       error
}

type uninstallMsg struct {
	pluginID string
	err      error
}

type upgradeMsg struct {
	pluginID string
	plugin   *client.PluginSummary
	err      error
}

// ── tab ───────────────────────────────────────────────────────────────────────

type marketplaceTab struct {
	c                 *client.Client
	catalog           []client.CatalogPlugin
	installedVersions map[string]string // plugin_id → installed version
	cursor    int
	scroll    int // first visible catalog row index
	viewW     int // last known content width
	viewH     int // last known content height
	loading   bool
	status            string
	err               string

	// uninstall confirmation state
	confirming      bool   // true when awaiting confirmation code
	confirmPluginID string // plugin being confirmed
	confirmPluginNm string // display name
	confirmCode     string // expected hex code
	confirmInput    string // what the user has typed so far
}

func newMarketplaceTab(c *client.Client) marketplaceTab {
	return marketplaceTab{
		c:                 c,
		installedVersions: make(map[string]string),
	}
}

// InputActive returns true when the tab is capturing all keystrokes (confirmation mode).
func (t marketplaceTab) InputActive() bool {
	return t.confirming
}

func (t marketplaceTab) update(msg tea.Msg) (marketplaceTab, tea.Cmd) {
	switch msg := msg.(type) {
	case pluginsMsg:
		if msg.err == nil {
			t.installedVersions = make(map[string]string, len(msg.plugins))
			for _, p := range msg.plugins {
				t.installedVersions[p.ID] = p.Version
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
			t.catalog = sortCatalogByGroup(msg.plugins)
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
			// mark all installed plugins (including dependencies)
			for _, p := range msg.installed {
				t.installedVersions[p.ID] = p.Version
			}
			if len(msg.installed) > 1 {
				t.status = fmt.Sprintf("%s installed (+%d dependencies)", msg.pluginID, len(msg.installed)-1)
			} else {
				t.status = msg.pluginID + " installed"
			}
		}

	case uninstallMsg:
		t.loading = false
		if msg.err != nil {
			t.status = "uninstall failed: " + msg.err.Error()
		} else {
			t.status = msg.pluginID + " uninstalled"
			delete(t.installedVersions, msg.pluginID)
		}

	case upgradeMsg:
		t.loading = false
		if msg.err != nil {
			t.status = "upgrade failed: " + msg.err.Error()
		} else if msg.plugin != nil {
			t.installedVersions[msg.pluginID] = msg.plugin.Version
			t.status = msg.pluginID + " upgraded to " + msg.plugin.Version
		}

	case tea.KeyPressMsg:
		if t.confirming {
			return t.updateConfirm(msg)
		}
		switch msg.String() {
		case "j", "down":
			if t.cursor < len(t.catalog)-1 {
				t.cursor++
				t.adjustScroll(t.catalogViewRows())
			}
		case "k", "up":
			if t.cursor > 0 {
				t.cursor--
				t.adjustScroll(t.catalogViewRows())
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
				if t.isInstalled(p.PluginID) {
					t.status = p.Name + " is already installed"
				} else {
					t.loading = true
					t.status = "installing " + p.Name + "…"
					return t, doInstallPlugin(t.c, p.PluginID)
				}
			}
		case "u":
			if p := t.selectedCatalog(); p != nil {
				if !t.isInstalled(p.PluginID) {
					t.status = p.Name + " is not installed"
				} else if !t.hasUpgrade(p.PluginID, p.Version) {
					t.status = p.Name + " is already up to date"
				} else {
					t.loading = true
					t.status = "upgrading " + p.Name + "…"
					return t, doUpgradePlugin(t.c, p.PluginID)
				}
			}
		case "d":
			if p := t.selectedCatalog(); p != nil {
				if !t.isInstalled(p.PluginID) {
					t.status = p.Name + " is not installed"
				} else {
					t.startConfirm(p.PluginID, p.Name)
				}
			}
		}
	}

	return t, nil
}

// ── confirmation flow ─────────────────────────────────────────────────────────

func (t *marketplaceTab) startConfirm(pluginID, name string) {
	code := make([]byte, 4)
	_, _ = rand.Read(code)
	t.confirming = true
	t.confirmPluginID = pluginID
	t.confirmPluginNm = name
	t.confirmCode = hex.EncodeToString(code)
	t.confirmInput = ""
	t.status = ""
}

func (t *marketplaceTab) cancelConfirm() {
	t.confirming = false
	t.confirmPluginID = ""
	t.confirmPluginNm = ""
	t.confirmCode = ""
	t.confirmInput = ""
}

func (t marketplaceTab) updateConfirm(msg tea.KeyPressMsg) (marketplaceTab, tea.Cmd) {
	switch msg.String() {
	case "esc":
		t.cancelConfirm()
		t.status = "uninstall cancelled"
	case "enter":
		if t.confirmInput == t.confirmCode {
			pluginID := t.confirmPluginID
			name := t.confirmPluginNm
			t.cancelConfirm()
			t.loading = true
			t.status = "uninstalling " + name + "…"
			return t, doUninstallPlugin(t.c, pluginID)
		}
		t.cancelConfirm()
		t.status = "confirmation code did not match — uninstall cancelled"
	case "backspace":
		if len(t.confirmInput) > 0 {
			t.confirmInput = t.confirmInput[:len(t.confirmInput)-1]
		}
	default:
		// accept all runes, filter to hex digits (supports paste)
		for _, ch := range msg.String() {
			if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') {
				t.confirmInput += string(ch)
			} else if ch >= 'A' && ch <= 'F' {
				t.confirmInput += string(ch + 32) // lowercase
			}
		}
	}
	return t, nil
}

func (t marketplaceTab) view(width, height int) string {
	innerW := width - 2
	innerH := height - 2

	lines := []string{
		sBold.Render(" Marketplace"),
		sep(innerW),
	}

	if t.loading && len(t.catalog) == 0 {
		lines = append(lines, sMuted.Render("  loading catalog…"))
	} else if t.err != "" {
		lines = append(lines, sErr.Render("  error: "+t.err))
	} else if len(t.catalog) == 0 {
		lines = append(lines, sMuted.Render("  no plugins in catalog  (r to refresh)"))
	} else {
		// table column widths — adapt to available space
		const colGap = 1
		const statusW = 3
		const versionW = 15
		const minNameW = 16
		const minGroupW = 10
		const minDescW = 20

		fixed := 2 + statusW + 4*colGap + versionW
		flexible := innerW - fixed
		nameW := flexible * 25 / 100
		groupW := flexible * 15 / 100
		descW := flexible - nameW - groupW
		if nameW < minNameW {
			nameW = minNameW
		}
		if groupW < minGroupW {
			groupW = minGroupW
		}
		if descW < minDescW {
			descW = minDescW
		}

		// header row
		hdr := fmt.Sprintf("  %s %s %s %s %s",
			sMuted.Render(pad("", statusW)),
			sMuted.Render(pad("NAME", nameW)),
			sMuted.Render(pad("GROUP", groupW)),
			sMuted.Render(pad("VERSION", versionW)),
			sMuted.Render(pad("DESCRIPTION", descW)),
		)
		lines = append(lines, hdr)

		// how many rows reserved for non-catalog content at bottom
		bottomReserved := 0
		if t.confirming {
			bottomReserved = 2
		} else if t.status != "" {
			bottomReserved = 1
		}

		// visible catalog row capacity
		headerLines := len(lines) // title + sep + column header
		visibleRows := innerH - headerLines - bottomReserved
		if visibleRows < 1 {
			visibleRows = 1
		}

		// render only the visible slice of the catalog
		end := t.scroll + visibleRows
		if end > len(t.catalog) {
			end = len(t.catalog)
		}

		for i := t.scroll; i < end; i++ {
			p := t.catalog[i]
			upgrade := t.hasUpgrade(p.PluginID, p.Version)
			status := "   "
			if t.isInstalled(p.PluginID) {
				if upgrade {
					status = sWarn.Render(" ↑ ")
				} else {
					status = sOK.Render(" ✓ ")
				}
			}
			versionStr := p.Version
			if upgrade {
				versionStr = t.installedVersions[p.PluginID] + " → " + p.Version
			}
			line := fmt.Sprintf("  %s %-*s %-*s %-*s %s",
				status,
				nameW, trunc(p.Name, nameW),
				groupW, trunc(p.Group, groupW),
				versionW, trunc(versionStr, versionW),
				trunc(p.Description, descW),
			)
			if upgrade {
				line = stripANSI(line)
				if i == t.cursor {
					line = sUpgSel.Render(pad(line, innerW))
				} else {
					line = sUpgrade.Render(pad(line, innerW))
				}
			} else if i == t.cursor {
				line = sSel.Render(pad(stripANSI(line), innerW))
			}
			lines = append(lines, line)
		}
	}

	// confirmation prompt or status line — always placed at the bottom of the visible area
	if t.confirming {
		for len(lines) < innerH-2 {
			lines = append(lines, "")
		}
		// trim to make room if needed
		if len(lines) > innerH-2 {
			lines = lines[:innerH-2]
		}
		warn := fmt.Sprintf("  ⚠ Uninstall %q will remove all its data.", t.confirmPluginNm)
		lines = append(lines, sWarn.Render(trunc(warn, innerW)))
		prompt := fmt.Sprintf("  Type %s to confirm (esc to cancel): %s█", t.confirmCode, t.confirmInput)
		lines = append(lines, trunc(prompt, innerW))
	} else if t.status != "" {
		for len(lines) < innerH-1 {
			lines = append(lines, "")
		}
		if len(lines) > innerH-1 {
			lines = lines[:innerH-1]
		}
		lines = append(lines, sMuted.Render("  "+trunc(t.status, innerW-2)))
	}

	// loading indicator when refreshing existing catalog
	if t.loading && len(t.catalog) > 0 {
		hdr := " " + sBold.Render("Marketplace") + "  " + sMuted.Render("refreshing…")
		lines[0] = hdr
	}

	content := buildContent(lines, innerH, innerW)
	return renderBox(content, innerW, true)
}

// catalogViewRows returns how many catalog rows fit in the current viewport.
// Fixed lines: title(1) + sep(1) + column header(1) = 3; plus bottom reserved for status/confirm.
func (t marketplaceTab) catalogViewRows() int {
	innerH := t.viewH - 2 // renderBox border
	reserved := 3          // title + sep + column header
	if t.confirming {
		reserved += 2
	} else if t.status != "" {
		reserved += 1
	}
	rows := innerH - reserved
	if rows < 1 {
		rows = 1
	}
	return rows
}

// setSize updates the stored viewport dimensions.
func (t *marketplaceTab) setSize(w, h int) {
	t.viewW = w
	t.viewH = h
}

// adjustScroll ensures the cursor is visible within the given viewport height.
// viewH is the number of catalog rows that can be displayed.
func (t *marketplaceTab) adjustScroll(viewH int) {
	if viewH < 1 {
		viewH = 1
	}
	if t.cursor < t.scroll {
		t.scroll = t.cursor
	} else if t.cursor >= t.scroll+viewH {
		t.scroll = t.cursor - viewH + 1
	}
}

func (t marketplaceTab) isInstalled(pluginID string) bool {
	_, ok := t.installedVersions[pluginID]
	return ok
}

func (t marketplaceTab) hasUpgrade(pluginID, catalogVersion string) bool {
	installed, ok := t.installedVersions[pluginID]
	return ok && installed != "" && installed != catalogVersion
}

func (t marketplaceTab) selectedCatalog() *client.CatalogPlugin {
	if len(t.catalog) == 0 || t.cursor >= len(t.catalog) {
		return nil
	}
	p := t.catalog[t.cursor]
	return &p
}

func (t marketplaceTab) helpLine() string {
	if t.confirming {
		return "type code to confirm  esc: cancel"
	}
	return "j/k: navigate  i: install  u: upgrade  d: uninstall  r: refresh"
}

// ── sorting ──────────────────────────────────────────────────────────────────

func sortCatalogByGroup(plugins []client.CatalogPlugin) []client.CatalogPlugin {
	sorted := make([]client.CatalogPlugin, len(plugins))
	copy(sorted, plugins)
	sort.SliceStable(sorted, func(i, j int) bool {
		gi, gj := sorted[i].Group, sorted[j].Group
		oi, oki := pluginTypeOrder[gi]
		oj, okj := pluginTypeOrder[gj]
		if !oki {
			oi = 100
		}
		if !okj {
			oj = 100
		}
		if oi != oj {
			return oi < oj
		}
		if gi != gj {
			return gi < gj
		}
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
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
		result, err := c.InstallPlugin(pluginID)
		if err != nil {
			return installMsg{pluginID: pluginID, err: err}
		}
		return installMsg{pluginID: pluginID, installed: result.Installed}
	}
}

func doUninstallPlugin(c *client.Client, pluginID string) tea.Cmd {
	return func() tea.Msg {
		err := c.UninstallPlugin(pluginID)
		return uninstallMsg{pluginID: pluginID, err: err}
	}
}

func doUpgradePlugin(c *client.Client, pluginID string) tea.Cmd {
	return func() tea.Msg {
		plugin, err := c.UpgradePlugin(pluginID)
		return upgradeMsg{pluginID: pluginID, plugin: plugin, err: err}
	}
}
