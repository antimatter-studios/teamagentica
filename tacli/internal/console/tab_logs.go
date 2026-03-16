package console

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

// ── messages ──────────────────────────────────────────────────────────────────

type logDataMsg struct {
	pluginID string
	lines    []string
	err      error
}

// ── tab ───────────────────────────────────────────────────────────────────────

type logsTab struct {
	c          *client.Client
	plugins    []client.PluginSummary
	cursor     int    // which plugin in the selector
	lines      []string
	loadingFor string
	err        string
	offset     int // scroll from bottom
}

func newLogsTab(c *client.Client) logsTab {
	return logsTab{c: c}
}

func (t logsTab) update(msg tea.Msg) (logsTab, tea.Cmd) {
	switch msg := msg.(type) {
	case pluginsMsg:
		if msg.err != nil {
			return t, nil
		}
		prev := t.selectedID()
		t.plugins = msg.plugins
		if t.cursor >= len(t.plugins) {
			t.cursor = max(0, len(t.plugins)-1)
		}
		// if no plugin was selected before, start polling the first one
		if prev == "" && len(t.plugins) > 0 {
			id := t.plugins[t.cursor].ID
			t.loadingFor = id
			return t, doFetchLogs(t.c, id)
		}

	case logTickMsg:
		if id := t.selectedID(); id != "" {
			t.loadingFor = id
			return t, doFetchLogs(t.c, id)
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

	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if t.cursor < len(t.plugins)-1 {
				t.cursor++
				t.lines = nil
				t.err = ""
				t.offset = 0
				if id := t.selectedID(); id != "" {
					t.loadingFor = id
					return t, doFetchLogs(t.c, id)
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
					return t, doFetchLogs(t.c, id)
				}
			}

		case "r":
			if id := t.selectedID(); id != "" {
				t.loadingFor = id
				return t, doFetchLogs(t.c, id)
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

	if len(t.plugins) == 0 {
		selLines = append(selLines, sMuted.Render("  no plugins"))
	} else {
		p := t.plugins[t.cursor]
		icon := pluginIcon(p.Status, p.Enabled)
		nav := ""
		if t.cursor > 0 {
			nav += sMuted.Render("← ")
		} else {
			nav += "  "
		}
		if t.cursor < len(t.plugins)-1 {
			nav += sMuted.Render(" →")
		}
		selLines = append(selLines, "  "+icon+" "+sBold.Render(p.Name)+" "+nav)
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
	return "j/k: switch plugin  r: refresh  K/J: scroll  g: end"
}

func (t logsTab) selectedID() string {
	if len(t.plugins) == 0 || t.cursor >= len(t.plugins) {
		return ""
	}
	return t.plugins[t.cursor].ID
}

// ── command ───────────────────────────────────────────────────────────────────

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
