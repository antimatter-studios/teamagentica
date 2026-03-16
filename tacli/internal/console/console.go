package console

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

const (
	tabPlugins = 0
	tabEvents  = 1
	tabLogs    = 2
	tabMarket  = 3
)

var tabNames = []string{"Plugins", "Events", "Logs", "Marketplace"}

// ── shared messages ───────────────────────────────────────────────────────────

type healthMsg struct {
	health *client.HealthResponse
	err    error
}

// pluginsMsg is handled by tabs via the default fan-out branch.
type pluginsMsg struct {
	plugins []client.PluginSummary
	err     error
}

type refreshTickMsg struct{}
type clockTickMsg time.Time
type sseStartedMsg struct{}
type sseEventMsg client.SSEEvent
type logTickMsg struct{}

// ── model ─────────────────────────────────────────────────────────────────────

// Model is the top-level bubbletea model for the multi-panel console.
type Model struct {
	c         *client.Client
	activeTab int
	health    *client.HealthResponse
	connErr   error
	now       time.Time
	width     int
	height    int
	quit      bool

	plugTab pluginsTab
	evtTab  eventsTab
	logTab  logsTab
	mktTab  marketplaceTab

	evtCh chan client.SSEEvent
}

// New creates the console model.
func New(c *client.Client) Model {
	evtCh := make(chan client.SSEEvent, 256)
	return Model{
		c:     c,
		now:   time.Now(),
		evtCh: evtCh,

		plugTab: newPluginsTab(c),
		evtTab:  newEventsTab(),
		logTab:  newLogsTab(c),
		mktTab:  newMarketplaceTab(c),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tea.SetWindowTitle("tacli console"),
		doFetchHealth(m.c),
		doFetchPlugins(m.c),
		scheduleRefresh(),
		scheduleClock(),
		scheduleLogPoll(),
		startSSEStream(m.c, m.evtCh),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			m.quit = true
			return m, tea.Quit

		case key.Matches(msg, key.NewBinding(key.WithKeys("1"))):
			m.activeTab = tabPlugins
		case key.Matches(msg, key.NewBinding(key.WithKeys("2"))):
			m.activeTab = tabEvents
		case key.Matches(msg, key.NewBinding(key.WithKeys("3"))):
			m.activeTab = tabLogs
		case key.Matches(msg, key.NewBinding(key.WithKeys("4"))):
			m.activeTab = tabMarket
		case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			m.activeTab = (m.activeTab + 1) % 4
		case key.Matches(msg, key.NewBinding(key.WithKeys("shift+tab"))):
			m.activeTab = (m.activeTab + 3) % 4

		default:
			// non-global keys → active tab only
			var cmd tea.Cmd
			switch m.activeTab {
			case tabPlugins:
				m.plugTab, cmd = m.plugTab.update(msg)
			case tabEvents:
				m.evtTab, cmd = m.evtTab.update(msg)
			case tabLogs:
				m.logTab, cmd = m.logTab.update(msg)
			case tabMarket:
				m.mktTab, cmd = m.mktTab.update(msg)
			}
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case healthMsg:
		m.health = msg.health
		m.connErr = msg.err

	case clockTickMsg:
		m.now = time.Time(msg)
		cmds = append(cmds, scheduleClock())

	case refreshTickMsg:
		cmds = append(cmds, doFetchHealth(m.c), doFetchPlugins(m.c), scheduleRefresh())

	case sseStartedMsg:
		cmds = append(cmds, waitForSSEEvent(m.evtCh))

	case sseEventMsg:
		m.evtTab = m.evtTab.addEvent(client.SSEEvent(msg))
		cmds = append(cmds, waitForSSEEvent(m.evtCh))

	default:
		// all other messages fan out to all tabs (async responses)
		var cmd tea.Cmd

		m.plugTab, cmd = m.plugTab.update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.evtTab, cmd = m.evtTab.update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.logTab, cmd = m.logTab.update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.mktTab, cmd = m.mktTab.update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.quit {
		return ""
	}
	if m.width == 0 {
		return "loading…"
	}
	if m.width < 60 || m.height < 12 {
		return "terminal too small (need ≥60×12)"
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		m.renderTabBar(),
		m.renderContent(),
		m.renderFooter(),
	)
}

// ── rendering ─────────────────────────────────────────────────────────────────

func (m Model) renderHeader() string {
	w := m.width

	var appLabel string
	if m.health != nil {
		appLabel = sBold.Render(m.health.App) + " " + sMuted.Render("v"+m.health.Version)
	} else {
		appLabel = sBold.Render("TeamAgentica")
	}

	var connLabel string
	if m.connErr != nil {
		connLabel = sErr.Render("✗ disconnected") + "  " + sMuted.Render(m.c.BaseURL)
	} else if m.health != nil {
		connLabel = sOK.Render("● connected") + "  " + sMuted.Render(m.c.BaseURL)
	} else {
		connLabel = sMuted.Render("◌ connecting…")
	}

	clockLabel := sMuted.Render(m.now.Format("15:04:05"))

	// spread across full width
	lw := lipgloss.Width(appLabel)
	mw := lipgloss.Width(connLabel)
	rw := lipgloss.Width(clockLabel)
	space := w - 2 - lw - mw - rw
	if space < 2 {
		space = 2
	}
	lpad := space / 2
	rpad := space - lpad

	line := " " + appLabel +
		strings.Repeat(" ", lpad) +
		connLabel +
		strings.Repeat(" ", rpad) +
		clockLabel

	return line + "\n" + " " + sMuted.Render(strings.Repeat("─", w-2))
}

func (m Model) renderTabBar() string {
	parts := make([]string, len(tabNames))
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == m.activeTab {
			parts[i] = sTabActive.Render(label)
		} else {
			parts[i] = sTabInactive.Render(label)
		}
	}
	return " " + strings.Join(parts, " ")
}

func (m Model) renderContent() string {
	h := m.contentHeight()
	w := m.width
	switch m.activeTab {
	case tabPlugins:
		return m.plugTab.view(w, h)
	case tabEvents:
		return m.evtTab.view(w, h)
	case tabLogs:
		return m.logTab.view(w, h)
	case tabMarket:
		return m.mktTab.view(w, h)
	}
	return ""
}

func (m Model) renderFooter() string {
	w := m.width
	var tabHelp string
	switch m.activeTab {
	case tabPlugins:
		tabHelp = m.plugTab.helpLine()
	case tabEvents:
		tabHelp = m.evtTab.helpLine()
	case tabLogs:
		tabHelp = m.logTab.helpLine()
	case tabMarket:
		tabHelp = m.mktTab.helpLine()
	}
	global := "1-4/Tab: switch  q: quit"
	sep := "  │  "
	line := tabHelp + sep + global
	return sMuted.Render(strings.Repeat("─", w)) + "\n  " + line
}

// contentHeight is the lines available for tab content.
// header=2  tabbar=1  footer=2  → subtract 5
func (m Model) contentHeight() int {
	h := m.height - 5
	if h < 4 {
		h = 4
	}
	return h
}

// ── background commands ───────────────────────────────────────────────────────

func doFetchHealth(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		h, err := c.Health()
		return healthMsg{health: h, err: err}
	}
}

func doFetchPlugins(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		plugins, err := c.ListPlugins()
		return pluginsMsg{plugins: plugins, err: err}
	}
}

func scheduleRefresh() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

func scheduleClock() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return clockTickMsg(t) })
}

func scheduleLogPoll() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return logTickMsg{} })
}

func startSSEStream(c *client.Client, ch chan client.SSEEvent) tea.Cmd {
	return func() tea.Msg {
		go func() {
			for {
				_ = c.StreamEvents(context.Background(), ch)
				time.Sleep(3 * time.Second)
			}
		}()
		return sseStartedMsg{}
	}
}

func waitForSSEEvent(ch chan client.SSEEvent) tea.Cmd {
	return func() tea.Msg {
		return sseEventMsg(<-ch)
	}
}
