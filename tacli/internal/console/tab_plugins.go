package console

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

const restartMinDisplay = 2 * time.Second

// ── messages ──────────────────────────────────────────────────────────────────

type pluginDetailMsg struct {
	forID  string
	detail *client.PluginDetail
	err    error
}

type pluginActionMsg struct {
	action   string
	pluginID string
	err      error
}

// ── tab ───────────────────────────────────────────────────────────────────────

type pluginsTab struct {
	c          *client.Client
	plugins    []client.PluginSummary
	cursor     int
	detail     *client.PluginDetail
	status     string
	confirm    string
	cfgEdit    configEditor
	focusRight bool   // true = right pane (config) has focus
	cfgFor     string // plugin ID currently loaded in config editor
	restarting map[string]time.Time // plugin ID → restart initiated time
}

func newPluginsTab(c *client.Client) pluginsTab {
	return pluginsTab{c: c, cfgEdit: newConfigEditor(c)}
}

// InputActive returns true when the tab is capturing all keystrokes.
func (t pluginsTab) InputActive() bool {
	return t.cfgEdit.inputActive()
}

// loadConfigForSelected opens config editor for the currently selected plugin.
func (t *pluginsTab) loadConfigForSelected() tea.Cmd {
	p := t.selected()
	if p == nil {
		return nil
	}
	if t.cfgFor == p.ID {
		return nil // already loaded
	}
	t.cfgFor = p.ID
	return t.cfgEdit.open(p.ID)
}

func (t pluginsTab) update(msg tea.Msg) (pluginsTab, tea.Cmd) {
	switch msg := msg.(type) {
	case pluginsMsg:
		if msg.err != nil {
			t.status = "plugins error: " + msg.err.Error()
			return t, nil
		}
		t.plugins = sortPluginsByType(msg.plugins)
		if t.cursor >= len(t.plugins) {
			t.cursor = max(0, len(t.plugins)-1)
		}
		// clear restarting state for plugins that are running and past min display
		now := time.Now()
		for _, p := range t.plugins {
			started, ok := t.restarting[p.ID]
			if ok && p.Status == "running" && now.Sub(started) >= restartMinDisplay {
				delete(t.restarting, p.ID)
			}
		}
		if len(t.plugins) > 0 && t.detail == nil {
			// first load only — fetch detail + config for selected plugin
			cmds := []tea.Cmd{doFetchDetail(t.c, t.plugins[t.cursor].ID)}
			if cmd := t.loadConfigForSelected(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return t, tea.Batch(cmds...)
		}

	case pluginDetailMsg:
		if len(t.plugins) > 0 && msg.forID == t.plugins[t.cursor].ID {
			t.detail = msg.detail
			if msg.err != nil {
				t.status = "detail error: " + msg.err.Error()
			}
		}

	case pluginActionMsg:
		t.status = ""
		if msg.err != nil {
			t.status = msg.action + " failed: " + msg.err.Error()
		} else {
			t.status = msg.action + " sent"
			if msg.action == "restart" && msg.pluginID != "" {
				if t.restarting == nil {
					t.restarting = make(map[string]time.Time)
				}
				t.restarting[msg.pluginID] = time.Now()
			}
		}

	case configLoadedMsg, configSavedMsg, oauthDeviceCodeMsg, oauthPollMsg, oauthSubmitCodeMsg, oauthTickMsg:
		var cmd tea.Cmd
		t.cfgEdit, cmd = t.cfgEdit.update(msg)
		return t, cmd

	case tea.PasteMsg:
		if t.cfgEdit.inputActive() {
			var cmd tea.Cmd
			t.cfgEdit, cmd = t.cfgEdit.update(msg)
			return t, cmd
		}

	case tea.KeyPressMsg:
		if t.cfgEdit.inputActive() {
			var cmd tea.Cmd
			t.cfgEdit, cmd = t.cfgEdit.update(msg)
			return t, cmd
		}

		if t.focusRight {
			return t.updateRight(msg)
		}
		return t.updateLeft(msg)
	}

	return t, nil
}

func (t pluginsTab) updateLeft(msg tea.KeyPressMsg) (pluginsTab, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if t.cursor < len(t.plugins)-1 {
			t.cursor++
			t.detail = nil
			t.confirm = ""
			cmds := []tea.Cmd{doFetchDetail(t.c, t.plugins[t.cursor].ID)}
			t.cfgFor = ""
			if cmd := t.loadConfigForSelected(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return t, tea.Batch(cmds...)
		}

	case "k", "up":
		if t.cursor > 0 {
			t.cursor--
			t.detail = nil
			t.confirm = ""
			cmds := []tea.Cmd{doFetchDetail(t.c, t.plugins[t.cursor].ID)}
			t.cfgFor = ""
			if cmd := t.loadConfigForSelected(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return t, tea.Batch(cmds...)
		}

	case "tab", "l", "right":
		if t.cfgEdit.active() {
			t.focusRight = true
		}

	case "e":
		if p := t.selected(); p != nil {
			t.status = "enabling " + p.Name + "…"
			t.confirm = ""
			return t, doAction(t.c, p.ID, "enable", func() error {
				_, err := t.c.EnablePlugin(p.ID)
				return err
			})
		}

	case "d":
		if p := t.selected(); p != nil {
			t.status = "disabling " + p.Name + "…"
			t.confirm = ""
			return t, doAction(t.c, p.ID, "disable", func() error {
				return t.c.DisablePlugin(p.ID)
			})
		}

	case "R":
		if p := t.selected(); p != nil {
			if t.confirm == "restart" {
				t.confirm = ""
				t.status = "restarting " + p.Name + "…"
				return t, doAction(t.c, p.ID, "restart", func() error {
					return t.c.RestartPlugin(p.ID)
				})
			}
			t.confirm = "restart"
			t.status = "Press R again to restart selected, A to restart ALL enabled, Esc to cancel"
		}

	case "A":
		if t.confirm == "restart" {
			t.confirm = ""
			var enabled []client.PluginSummary
			for _, p := range t.plugins {
				if p.Enabled {
					enabled = append(enabled, p)
				}
			}
			if len(enabled) == 0 {
				t.status = "no enabled plugins to restart"
				return t, nil
			}
			t.status = fmt.Sprintf("restarting %d enabled plugins…", len(enabled))
			if t.restarting == nil {
				t.restarting = make(map[string]time.Time)
			}
			var cmds []tea.Cmd
			for _, p := range enabled {
				p := p
				t.restarting[p.ID] = time.Now()
				cmds = append(cmds, doAction(t.c, p.ID, "restart", func() error {
					return t.c.RestartPlugin(p.ID)
				}))
			}
			return t, tea.Batch(cmds...)
		}

	case "esc":
		t.confirm = ""
		t.status = ""
	}
	return t, nil
}

func (t pluginsTab) updateRight(msg tea.KeyPressMsg) (pluginsTab, tea.Cmd) {
	switch msg.String() {
	case "tab":
		t.focusRight = false
		return t, nil
	case "h", "left":
		// if on an alias row, left moves within the row first
		item := t.cfgEdit.selectedItem()
		if item != nil && item.aliasIdx >= 0 && item.aliasCol > 0 {
			item.prevCol()
			return t, nil
		}
		t.focusRight = false
		return t, nil
	}

	// delegate everything else to config editor
	var cmd tea.Cmd
	t.cfgEdit, cmd = t.cfgEdit.update(msg)
	// if config editor closed itself (esc with no changes), go back to left
	if !t.cfgEdit.active() {
		t.focusRight = false
		// reopen for current plugin
		t.cfgFor = ""
		if openCmd := t.loadConfigForSelected(); openCmd != nil {
			return t, openCmd
		}
	}
	return t, cmd
}

func (t pluginsTab) view(width, height int) string {
	leftW := width * 2 / 5
	if leftW < 30 {
		leftW = 30
	}
	rightW := width - leftW
	innerH := height - 2

	leftLines := t.renderList(leftW-2, innerH)

	var rightLines []string
	if t.cfgEdit.active() {
		rightLines = t.cfgEdit.render(rightW-2, innerH, t.selected(), t.detail, t.plugins)
	} else {
		rightLines = t.renderDetail(rightW-2, innerH)
	}

	leftContent := buildContent(leftLines, innerH, leftW-2)
	rightContent := buildContent(rightLines, innerH, rightW-2)

	leftPanel := renderBox(leftContent, leftW-2, !t.focusRight)
	rightPanel := renderBox(rightContent, rightW-2, t.focusRight)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
}

func (t pluginsTab) renderList(w, h int) []string {
	lines := []string{
		sBold.Render(fmt.Sprintf(" Plugins (%d)", len(t.plugins))),
		sep(w),
	}

	if len(t.plugins) == 0 {
		lines = append(lines, sMuted.Render(" no plugins"))
		return lines
	}

	// Pre-compute max widths for right-aligned columns across all plugins.
	maxStatusW := 0
	maxVerW := 0
	hasSys := false
	type rowMeta struct {
		displayStatus string
		statusStr     string
		statusW       int
		verStr        string
		verW          int
		sysStr        string
		sysW          int
	}
	metas := make([]rowMeta, len(t.plugins))
	for i, p := range t.plugins {
		ds := p.Status
		if _, ok := t.restarting[p.ID]; ok {
			ds = "restarting"
		}
		m := rowMeta{displayStatus: ds}
		m.statusStr = statusColor(ds, p.Enabled)
		m.statusW = lipgloss.Width(m.statusStr)
		if m.statusW > maxStatusW {
			maxStatusW = m.statusW
		}
		if p.Version != "" {
			m.verStr = sMuted.Render("[" + p.Version + "]")
			m.verW = len(p.Version) + 2
			if m.verW > maxVerW {
				maxVerW = m.verW
			}
		}
		if p.System {
			m.sysStr = sWarn.Render("(SYSTEM)")
			m.sysW = 8
			hasSys = true
		}
		metas[i] = m
	}

	// Column widths: " icon name  (SYSTEM) [version] status"
	sysColW := 0
	if hasSys {
		sysColW = 9 // "(SYSTEM) " with trailing space
	}
	verColW := 0
	if maxVerW > 0 {
		verColW = maxVerW + 1 // "[ver] " with trailing space
	}
	rightW := sysColW + verColW + maxStatusW
	nameW := w - 3 - rightW // 3 = leading space + icon + space
	if nameW < 8 {
		nameW = 8
	}

	lastType := ""
	for i, p := range t.plugins {
		// Insert group header when type changes.
		pType := pluginTypeFromID(p.ID)
		if pType != lastType {
			if lastType != "" {
				lines = append(lines, "") // blank separator between groups
			}
			lines = append(lines, sCyan.Render(" "+strings.ToUpper(pType)))
			lastType = pType
		}

		m := metas[i]
		icon := pluginIcon(m.displayStatus, p.Enabled)
		name := trunc(p.Name, nameW)

		// Build fixed-width right columns
		var right strings.Builder
		if hasSys {
			if m.sysW > 0 {
				right.WriteString(m.sysStr)
				right.WriteByte(' ')
			} else {
				right.WriteString(strings.Repeat(" ", sysColW))
			}
		}
		if maxVerW > 0 {
			if m.verW > 0 {
				right.WriteString(m.verStr)
				right.WriteString(strings.Repeat(" ", maxVerW-m.verW+1))
			} else {
				right.WriteString(strings.Repeat(" ", verColW))
			}
		}
		// Right-align status within its column
		right.WriteString(strings.Repeat(" ", maxStatusW-m.statusW))
		right.WriteString(m.statusStr)

		namePad := nameW - lipgloss.Width(name)
		if namePad < 0 {
			namePad = 0
		}

		line := fmt.Sprintf(" %s %s%s%s", icon, name, strings.Repeat(" ", namePad), right.String())
		if i == t.cursor {
			line = sSel.Render(pad(stripANSI(line), w))
		}
		lines = append(lines, line)
	}

	// status / confirm line at bottom
	if t.status != "" {
		avail := h - len(lines) - 1
		for avail > 0 {
			lines = append(lines, "")
			avail--
		}
		if t.confirm != "" {
			lines = append(lines, sWarn.Width(w).Render(" "+t.status))
		} else {
			lines = append(lines, sMuted.Width(w).Render(" "+t.status))
		}
	}

	return lines
}

func (t pluginsTab) renderDetail(w, h int) []string {
	_ = h

	p := t.selected()
	if p == nil {
		return []string{sMuted.Render(" select a plugin")}
	}

	lines := []string{
		sBold.Render(" " + p.Name),
		sep(w),
	}

	add := func(label, val string) {
		line := fmt.Sprintf("  %-14s %s", label, val)
		lines = append(lines, wrap(line, w)...)
	}

	add("Status", statusColor(p.Status, p.Enabled))
	add("Version", sMuted.Render(p.Version))

	if t.detail != nil {
		add("Image", t.detail.Image)
		if t.detail.Host != "" {
			add("Host", t.detail.Host)
		}
	}

	if t.detail != nil && len(t.detail.Capabilities) > 0 {
		lines = append(lines, "")
		lines = append(lines, "  "+sBold.Render("Capabilities"))
		for _, cap := range t.detail.Capabilities {
			lines = append(lines, "    "+sCyan.Render(cap))
		}
	}

	if t.detail != nil && len(t.detail.Dependencies) > 0 {
		lines = append(lines, "")
		lines = append(lines, "  "+sBold.Render("Dependencies"))
		for _, dep := range t.detail.Dependencies {
			if capSatisfied(dep, t.plugins) {
				lines = append(lines, "    "+sDepOk.Render(dep))
			} else {
				lines = append(lines, "    "+sDepMissing.Render(dep+" (not satisfied)"))
			}
		}
	}

	if t.detail == nil {
		lines = append(lines, "")
		lines = append(lines, sMuted.Render("  loading…"))
	}

	return lines
}

func (t pluginsTab) helpLine() string {
	if t.cfgEdit.inputActive() {
		if t.cfgEdit.selecting {
			return "j/k: navigate  Enter: select  Esc: cancel"
		}
		if t.cfgEdit.editing {
			return "type value  Enter: confirm  Esc: cancel"
		}
		if t.cfgEdit.oauthPoll {
			return "waiting for oauth…  Esc: cancel"
		}
	}
	if t.focusRight {
		help := "j/k: navigate  Enter: edit  Tab/←: plugins  s: save  Esc: discard"
		if len(t.cfgEdit.dirty) > 0 {
			help = fmt.Sprintf("j/k: navigate  Enter: edit  Tab/←: plugins  s: save (%d)  Esc: discard", len(t.cfgEdit.dirty))
		}
		return help
	}
	return "j/k: navigate  Tab/→: config  e: enable  d: disable  R: restart"
}

func (t pluginsTab) selected() *client.PluginSummary {
	if len(t.plugins) == 0 || t.cursor >= len(t.plugins) {
		return nil
	}
	p := t.plugins[t.cursor]
	return &p
}

// ── sorting helpers ──────────────────────────────────────────────────────────

// pluginTypeOrder defines the display order for plugin type groups.
var pluginTypeOrder = map[string]int{
	"agent": 0, "infra": 1, "messaging": 2, "network": 3,
	"storage": 4, "tool": 5, "user": 6,
}

func pluginTypeFromID(id string) string {
	if i := strings.Index(id, "-"); i > 0 {
		return id[:i]
	}
	return id
}

func sortPluginsByType(plugins []client.PluginSummary) []client.PluginSummary {
	sorted := make([]client.PluginSummary, len(plugins))
	copy(sorted, plugins)
	sort.SliceStable(sorted, func(i, j int) bool {
		ti, tj := pluginTypeFromID(sorted[i].ID), pluginTypeFromID(sorted[j].ID)
		oi, oki := pluginTypeOrder[ti]
		oj, okj := pluginTypeOrder[tj]
		if !oki {
			oi = 100
		}
		if !okj {
			oj = 100
		}
		if oi != oj {
			return oi < oj
		}
		if ti != tj {
			return ti < tj
		}
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

// ── commands ──────────────────────────────────────────────────────────────────

func doFetchDetail(c *client.Client, id string) tea.Cmd {
	return func() tea.Msg {
		d, err := c.GetPlugin(id)
		return pluginDetailMsg{forID: id, detail: d, err: err}
	}
}

func doAction(c *client.Client, id, action string, fn func() error) tea.Cmd {
	_ = c
	return func() tea.Msg {
		err := fn()
		return pluginActionMsg{action: action, pluginID: id, err: err}
	}
}
