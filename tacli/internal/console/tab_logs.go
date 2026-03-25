package console

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

// ── messages ──────────────────────────────────────────────────────────────────

type logDataMsg struct {
	pluginID string
	lines    []string
	err      error
}

// kernelEntry is the sentinel ID used for the kernel in the log selector.
const kernelEntry = "__kernel__"

// uiEntry is the sentinel ID used for the web-dashboard container.
const uiEntry = "__ui__"

// ── tab ───────────────────────────────────────────────────────────────────────

type logSource struct {
	id   string
	name string
}

type logsTab struct {
	c          *client.Client
	plugins    []client.PluginSummary
	sources    []logSource // kernel + plugins
	cursor     int         // which source in the selector
	lines      []string
	loadingFor string
	err        string
	offset     int // scroll from bottom
}

func newLogsTab(c *client.Client) logsTab {
	return logsTab{c: c}
}

func (t logsTab) rebuildSources() []logSource {
	sources := []logSource{
		{id: kernelEntry, name: "Kernel"},
		{id: uiEntry, name: "Web Dashboard"},
	}
	for _, p := range t.plugins {
		sources = append(sources, logSource{id: p.ID, name: p.Name})
	}
	return sources
}

func (t logsTab) update(msg tea.Msg) (logsTab, tea.Cmd) {
	switch msg := msg.(type) {
	case pluginsMsg:
		if msg.err != nil {
			return t, nil
		}
		prev := t.selectedID()
		t.plugins = msg.plugins
		t.sources = t.rebuildSources()
		if t.cursor >= len(t.sources) {
			t.cursor = max(0, len(t.sources)-1)
		}
		// if no source was selected before, start polling the first one (kernel)
		if prev == "" && len(t.sources) > 0 {
			id := t.sources[t.cursor].id
			t.loadingFor = id
			return t, t.fetchLogsCmd(id)
		}

	case logTickMsg:
		if id := t.selectedID(); id != "" {
			t.loadingFor = id
			return t, t.fetchLogsCmd(id)
		}

	case logDataMsg:
		if msg.pluginID == t.selectedID() {
			t.loadingFor = ""
			t.err = ""
			if msg.err != nil {
				t.err = msg.err.Error()
			} else {
				t.lines = msg.lines
				t.offset = 0 // jump to end on new data
			}
		}

	case tea.KeyPressMsg:
		switch msg.String() {
		case "j", "down":
			if t.cursor < len(t.sources)-1 {
				t.cursor++
				t.lines = nil
				t.err = ""
				t.offset = 0
				if id := t.selectedID(); id != "" {
					t.loadingFor = id
					return t, t.fetchLogsCmd(id)
				}
			}

		case "k", "up":
			if t.cursor > 0 {
				t.cursor--
				t.lines = nil
				t.err = ""
				t.offset = 0
				if id := t.selectedID(); id != "" {
					t.loadingFor = id
					return t, t.fetchLogsCmd(id)
				}
			}

		case "r":
			if id := t.selectedID(); id != "" {
				t.loadingFor = id
				return t, t.fetchLogsCmd(id)
			}

		case "K", "pgup":
			t.offset += 10

		case "J", "pgdn":
			if t.offset >= 10 {
				t.offset -= 10
			} else {
				t.offset = 0
			}

		case "g":
			t.offset = 0
		}
	}

	return t, nil
}

func (t logsTab) view(width, height int) string {
	innerW := width - 2
	innerH := height - 2

	selectorH := 3
	logH := innerH - selectorH

	// ── selector ──────────────────────────────────────────────────────────────
	selLines := make([]string, 0, selectorH)
	selLines = append(selLines, sBold.Render(" Logs"))

	if len(t.sources) == 0 {
		selLines = append(selLines, sMuted.Render("  no sources"))
	} else {
		src := t.sources[t.cursor]
		icon := "⚙"
		if src.id == uiEntry {
			icon = "🌐"
		} else if src.id != kernelEntry {
			// find the plugin to get its status icon
			for _, p := range t.plugins {
				if p.ID == src.id {
					icon = pluginIcon(p.Status, p.Enabled)
					break
				}
			}
		}
		nav := ""
		if t.cursor > 0 {
			nav += sMuted.Render("← ")
		} else {
			nav += "  "
		}
		if t.cursor < len(t.sources)-1 {
			nav += sMuted.Render(" →")
		}
		selLines = append(selLines, "  "+icon+" "+sBold.Render(src.name)+" "+nav)
	}
	selLines = append(selLines, sep(innerW))

	// ── log content ───────────────────────────────────────────────────────────
	logLines := make([]string, 0, logH)

	if t.err != "" {
		logLines = append(logLines, sErr.Render("  error: "+t.err))
	} else if t.loadingFor != "" && len(t.lines) == 0 {
		logLines = append(logLines, sMuted.Render("  loading…"))
	} else if len(t.lines) == 0 {
		logLines = append(logLines, sMuted.Render("  no logs"))
	} else {
		// apply scroll window
		visible := t.lines
		if len(visible) > logH {
			bottom := len(visible) - t.offset
			if bottom < logH {
				bottom = logH
			}
			if bottom > len(visible) {
				bottom = len(visible)
			}
			start := bottom - logH
			if start < 0 {
				start = 0
			}
			visible = visible[start:bottom]
		}
		for _, l := range visible {
			logLines = append(logLines, "  "+trunc(l, innerW-2))
		}
	}

	// combine
	allLines := append(selLines, logLines...)
	content := buildContent(allLines, innerH, innerW)
	return renderBox(content, innerW, true)
}

func (t logsTab) helpLine() string {
	return "j/k: switch source  r: refresh  K/J: scroll  g: end"
}

func (t logsTab) selectedID() string {
	if len(t.sources) == 0 || t.cursor >= len(t.sources) {
		return ""
	}
	return t.sources[t.cursor].id
}

func (t logsTab) fetchLogsCmd(id string) tea.Cmd {
	switch id {
	case kernelEntry:
		return doFetchKernelLogs(t.c)
	case uiEntry:
		return doFetchUILogs(t.c)
	default:
		return doFetchLogs(t.c, id)
	}
}

// ── commands ──────────────────────────────────────────────────────────────────

func doFetchLogs(c *client.Client, pluginID string) tea.Cmd {
	return func() tea.Msg {
		raw, err := c.GetPluginLogs(pluginID, 300)
		if err != nil {
			return logDataMsg{pluginID: pluginID, err: err}
		}
		lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
		return logDataMsg{pluginID: pluginID, lines: lines}
	}
}

func doFetchKernelLogs(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		raw, err := c.GetKernelLogs(300)
		if err != nil {
			return logDataMsg{pluginID: kernelEntry, err: err}
		}
		lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
		return logDataMsg{pluginID: kernelEntry, lines: lines}
	}
}

func doFetchUILogs(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		raw, err := c.GetUILogs(300)
		if err != nil {
			return logDataMsg{pluginID: uiEntry, err: err}
		}
		lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
		return logDataMsg{pluginID: uiEntry, lines: lines}
	}
}
