package console

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
// When err contains "invalid or expired token" or "missing authorization",
// the console surfaces this as an auth error in the header.
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
	authErr   string // non-empty when API returns auth errors
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
		m.mktTab.setSize(msg.Width, m.contentHeight())
		return m, nil

	case tea.KeyPressMsg:
		// ctrl+c always quits, even when a tab is capturing input
		if key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))) {
			m.quit = true
			return m, tea.Quit
		}

		// If the active tab is capturing input, send all keys to it directly
		if m.activeTab == tabMarket && m.mktTab.InputActive() {
			var cmd tea.Cmd
			m.mktTab, cmd = m.mktTab.update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		if m.activeTab == tabPlugins && m.plugTab.InputActive() {
			var cmd tea.Cmd
			m.plugTab, cmd = m.plugTab.update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

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

	case tea.PasteMsg:
		var cmd tea.Cmd
		switch m.activeTab {
		case tabPlugins:
			m.plugTab, cmd = m.plugTab.update(msg)
		case tabMarket:
			m.mktTab, cmd = m.mktTab.update(msg)
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
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

	case pluginsMsg:
		// detect auth errors and surface them in the header
		if msg.err != nil {
			errStr := msg.err.Error()
			if strings.Contains(errStr, "token") || strings.Contains(errStr, "authorization") || strings.Contains(errStr, "401") {
				m.authErr = "session expired — run: tacli connect"
			}
		} else {
			m.authErr = ""
		}
		// fan out to all tabs
		var cmd tea.Cmd
		m.plugTab, cmd = m.plugTab.update(msg)
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

func (m Model) View() tea.View {
	v := tea.View{WindowTitle: "tacli console", AltScreen: true}
	if m.quit {
		return v
	}
	if m.width == 0 {
		v.SetContent("loading…")
		return v
	}
	if m.width < 60 || m.height < 12 {
		v.SetContent("terminal too small (need ≥60×12)")
		return v
	}

	v.SetContent(lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		m.renderTabBar(),
		m.renderContent(),
		m.renderFooter(),
	))
	return v
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
	} else if m.authErr != "" {
		connLabel = sErr.Render("✗ " + m.authErr)
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

	// full-screen auth error overlay
	if m.authErr != "" {
		return m.renderAuthError(w, h)
	}

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

func (m Model) renderAuthError(w, h int) string {
	innerW := w - 2
	innerH := h - 2

	lines := make([]string, 0, innerH)

	// center vertically
	topPad := innerH/2 - 3
	if topPad < 1 {
		topPad = 1
	}
	for i := 0; i < topPad; i++ {
		lines = append(lines, "")
	}

	// warning block
	msg1 := "SESSION EXPIRED"
	msg2 := "Your authentication token is invalid or expired."
	msg3 := "Run the following command to reconnect:"
	msg4 := "tacli connect " + m.c.BaseURL
	msg5 := "(press q to quit)"

	center := func(s string) string {
		vw := lipgloss.Width(s)
		pad := (innerW - vw) / 2
		if pad < 0 {
			pad = 0
		}
		return strings.Repeat(" ", pad) + s
	}

	lines = append(lines, center(sErr.Render(msg1)))
	lines = append(lines, "")
	lines = append(lines, center(msg2))
	lines = append(lines, "")
	lines = append(lines, center(msg3))
	lines = append(lines, "")
	lines = append(lines, center(sBold.Render(msg4)))
	lines = append(lines, "")
	lines = append(lines, center(sMuted.Render(msg5)))

	content := buildContent(lines, innerH, innerW)
	return renderBox(content, innerW, true)
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
