package console

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

// refreshMsg triggers a data refresh.
type refreshMsg struct{}

// dataMsg carries fetched data.
type dataMsg struct {
	health  *client.HealthResponse
	plugins []client.PluginSummary
	err     error
}

// Model is the bubbletea model for the console dashboard.
type Model struct {
	client  *client.Client
	health  *client.HealthResponse
	plugins []client.PluginSummary
	err     error
	width   int
	height  int
	cursor  int
	quit    bool
}

// New creates a new console model.
func New(c *client.Client) Model {
	return Model{client: c}
}

// Init starts the first data fetch.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		fetchData(m.client),
		tea.SetWindowTitle("tacli console"),
	)
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			m.quit = true
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("r"))):
			return m, fetchData(m.client)
		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			if m.cursor < len(m.plugins)-1 {
				m.cursor++
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if m.cursor > 0 {
				m.cursor--
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case dataMsg:
		m.health = msg.health
		m.plugins = msg.plugins
		m.err = msg.err
		return m, scheduleRefresh()

	case refreshMsg:
		return m, fetchData(m.client)
	}

	return m, nil
}

// View renders the dashboard.
func (m Model) View() string {
	if m.quit {
		return ""
	}

	// Styles.
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		MarginBottom(1)

	ok := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selected := lipgloss.NewStyle().Background(lipgloss.Color("8")).Foreground(lipgloss.Color("15"))
	header := lipgloss.NewStyle().Bold(true).Underline(true).MarginTop(1).MarginBottom(1)

	var s string

	// Header.
	if m.health != nil {
		s += title.Render(fmt.Sprintf("┃ %s v%s", m.health.App, m.health.Version))
		s += "\n"
		s += ok.Render("  ● connected") + "  " + dim.Render(m.client.BaseURL)
		s += "\n"
	} else if m.err != nil {
		s += title.Render("┃ tacli console")
		s += "\n"
		s += errStyle.Render("  ✗ " + m.err.Error())
		s += "\n"
	} else {
		s += title.Render("┃ tacli console")
		s += "\n"
		s += dim.Render("  connecting...")
		s += "\n"
	}

	// Plugins.
	s += header.Render("  Plugins")
	s += "\n"

	if len(m.plugins) == 0 {
		s += dim.Render("  no plugins")
		s += "\n"
	} else {
		for i, p := range m.plugins {
			indicator := errStyle.Render("○")
			if p.Enabled {
				indicator = ok.Render("●")
			}

			line := fmt.Sprintf("  %s %-28s %s", indicator, p.Name, p.Status)
			if i == m.cursor {
				line = selected.Render(line)
			}
			s += line + "\n"
		}
	}

	// Footer.
	s += "\n"
	s += dim.Render("  r: refresh  q: quit  ↑/↓: navigate")
	s += "\n"

	return s
}

func fetchData(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		h, err := c.Health()
		if err != nil {
			return dataMsg{err: err}
		}

		plugins, err := c.ListPlugins()
		if err != nil {
			// Health OK but plugins failed (auth issue) — show what we can.
			return dataMsg{health: h, err: err}
		}

		return dataMsg{health: h, plugins: plugins}
	}
}

func scheduleRefresh() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return refreshMsg{}
	})
}
