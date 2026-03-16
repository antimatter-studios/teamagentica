package console

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

// ── messages ──────────────────────────────────────────────────────────────────

type pluginDetailMsg struct {
	forID  string
	detail *client.PluginDetail
	err    error
}

type pluginActionMsg struct {
	action string
	err    error
}

// ── tab ───────────────────────────────────────────────────────────────────────

type pluginsTab struct {
	c       *client.Client
	plugins []client.PluginSummary
	cursor  int
	detail  *client.PluginDetail
	status  string // transient feedback line
	confirm string // pending confirm action ("restart")
}

func newPluginsTab(c *client.Client) pluginsTab {
	return pluginsTab{c: c}
}

func (t pluginsTab) update(msg tea.Msg) (pluginsTab, tea.Cmd) {
	switch msg := msg.(type) {
	case pluginsMsg:
		if msg.err != nil {
			return t, nil
		}
		t.plugins = msg.plugins
		if t.cursor >= len(t.plugins) {
			t.cursor = max(0, len(t.plugins)-1)
		}
		// auto-fetch detail for current selection
		if len(t.plugins) > 0 {
			return t, doFetchDetail(t.c, t.plugins[t.cursor].ID)
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
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if t.cursor < len(t.plugins)-1 {
				t.cursor++
				t.detail = nil
				t.confirm = ""
				if len(t.plugins) > 0 {
					return t, doFetchDetail(t.c, t.plugins[t.cursor].ID)
				}
			}

		case "k", "up":
			if t.cursor > 0 {
				t.cursor--
				t.detail = nil
				t.confirm = ""
				if len(t.plugins) > 0 {
					return t, doFetchDetail(t.c, t.plugins[t.cursor].ID)
				}
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
				t.status = "Press R again to confirm restart, Esc to cancel"
			}

		case "esc":
			t.confirm = ""
			t.status = ""
		}
	}

	return t, nil
}

func (t pluginsTab) view(width, height int) string {
	leftW := width * 2 / 5
	if leftW < 30 {
		leftW = 30
	}
	rightW := width - leftW
	innerH := height - 2

	leftLines := t.renderList(leftW-2, innerH)
	rightLines := t.renderDetail(rightW-2, innerH)

	leftContent := buildContent(leftLines, innerH, leftW-2)
	rightContent := buildContent(rightLines, innerH, rightW-2)

	leftPanel := renderBox(leftContent, leftW-2, t.activeLeft())
	rightPanel := renderBox(rightContent, rightW-2, false)

	return strings.Join([]string{
		"",
		" " + leftPanel + rightPanel,
	}, "\n")
}

func (t pluginsTab) activeLeft() bool { return true }

func (t pluginsTab) renderList(w, h int) []string {
	lines := []string{
		sBold.Render(fmt.Sprintf(" Plugins (%d)", len(t.plugins))),
		sep(w),
	}

	if len(t.plugins) == 0 {
		lines = append(lines, sDim.Render(" no plugins"))
		return lines
	}

	for i, p := range t.plugins {
		icon := pluginIcon(p.Status, p.Enabled)
		badge := ""
		if p.System {
			badge = sDim.Render(" sys")
		}
		name := trunc(p.Name, w-16)
		line := fmt.Sprintf(" %s %-*s %s%s", icon, w-16, name, statusColor(p.Status, p.Enabled), badge)
		if i == t.cursor {
			line = sSel.Render(pad(line, w))
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
			lines = append(lines, sWarn.Render(trunc(" "+t.status, w)))
		} else {
			lines = append(lines, sMuted.Render(trunc(" "+t.status, w)))
		}
	}

	return lines
}

func (t pluginsTab) renderDetail(w, h int) []string {
	_ = h

	p := t.selected()
	if p == nil {
		return []string{sDim.Render(" select a plugin")}
	}

	lines := []string{
		sBold.Render(" " + p.Name),
		sep(w),
	}

	add := func(label, val string) {
		line := fmt.Sprintf("  %-14s %s", label, val)
		lines = append(lines, trunc(line, w))
	}

	add("Status", statusColor(p.Status, p.Enabled))
	add("Version", sMuted.Render(p.Version))

	if t.detail != nil {
		add("Image", sDim.Render(trunc(t.detail.Image, w-18)))
		if t.detail.Host != "" {
			add("Host", sDim.Render(t.detail.Host))
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
			lines = append(lines, "    "+sDim.Render(dep))
		}
	}

	if t.detail != nil && t.detail.CandidateImage != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+sWarn.Render("Candidate"))
		lines = append(lines, "    image   "+sDim.Render(trunc(t.detail.CandidateImage, w-12)))
		if t.detail.CandidateVersion != "" {
			lines = append(lines, "    version "+sDim.Render(t.detail.CandidateVersion))
		}
	}

	if t.detail == nil {
		lines = append(lines, "")
		lines = append(lines, sDim.Render("  loading…"))
	}

	return lines
}

func (t pluginsTab) helpLine() string {
	return "j/k: navigate  e: enable  d: disable  R: restart (confirm)"
}

func (t pluginsTab) selected() *client.PluginSummary {
	if len(t.plugins) == 0 || t.cursor >= len(t.plugins) {
		return nil
	}
	p := t.plugins[t.cursor]
	return &p
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
	_ = id
	return func() tea.Msg {
		err := fn()
		return pluginActionMsg{action: action, err: err}
	}
}

