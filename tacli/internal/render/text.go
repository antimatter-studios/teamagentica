package render

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	subtitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("251"))
	groupStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("45"))
	nameStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	verStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	detailStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	enabledStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	runningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	disabledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	warningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	systemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("251"))
)

// Styles exposes the shared style constants for commands that need custom layouts.
var Styles = struct {
	Title, Subtitle, Group, Name, Version, Detail, Enabled, Disabled lipgloss.Style
}{
	Title:    titleStyle,
	Subtitle: subtitleStyle,
	Group:    groupStyle,
	Name:     nameStyle,
	Version:  verStyle,
	Detail:   detailStyle,
	Enabled:  enabledStyle,
	Disabled: disabledStyle,
}

// column defines a table column: which field key to read, header label, and how to style it.
type column struct {
	key    string
	header string
	style  func(value string, fields Fields) string
}

// columns defines the render order and styling for each field.
var columns = []column{
	{"name", "NAME", func(v string, _ Fields) string { return nameStyle.Render(v) }},
	{"version", "VERSION", func(v string, _ Fields) string { return verStyle.Render(v) }},
	{"enabled", "ENABLED", func(v string, _ Fields) string {
		if v == "enabled" {
			return enabledStyle.Render(v)
		}
		return detailStyle.Render(v)
	}},
	{"status", "STATUS", func(v string, _ Fields) string {
		switch v {
		case "enabled":
			return enabledStyle.Render("● " + v)
		case "running":
			return runningStyle.Render("● " + v)
		case "installed":
			return detailStyle.Render("○ " + v)
		case "stopped":
			return detailStyle.Render("○ " + v)
		case "unhealthy":
			return warningStyle.Render("● " + v)
		case "error":
			return disabledStyle.Render("● " + v)
		default:
			return detailStyle.Render("○ " + v)
		}
	}},
	{"url", "URL", func(v string, _ Fields) string { return detailStyle.Render(v) }},
	{"image", "IMAGE", func(v string, _ Fields) string { return detailStyle.Render(v) }},
	{"id", "ID", func(v string, f Fields) string {
		s := detailStyle.Render(v)
		if f["system"] == "true" {
			s += " " + systemStyle.Render("(SYSTEM)")
		}
		return s
	}},
}

// bufferedGroup holds a group title and its rows.
type bufferedGroup struct {
	title string
	rows  []Fields
}

// TextRenderer outputs styled text to the terminal.
type TextRenderer struct {
	groups []bufferedGroup
}

// NewText creates a text renderer.
func NewText() *TextRenderer {
	return &TextRenderer{}
}

func (t *TextRenderer) Header(title, subtitle, version string) {
	fmt.Printf("%s %s\n", titleStyle.Render(title), verStyle.Render("v"+version))
	fmt.Println(subtitleStyle.Render(subtitle))
	fmt.Println()
}

func (t *TextRenderer) GroupStart(name string) {
	t.groups = append(t.groups, bufferedGroup{title: name})
}

func (t *TextRenderer) Item(fields Fields) {
	if len(t.groups) == 0 {
		t.groups = append(t.groups, bufferedGroup{})
	}
	g := &t.groups[len(t.groups)-1]
	g.rows = append(g.rows, fields)
}

func (t *TextRenderer) Flush() error {
	for i, g := range t.groups {
		if i > 0 {
			fmt.Println()
		}
		if g.title != "" {
			fmt.Println(groupStyle.Render(g.title))
		}
		if len(g.rows) == 0 {
			continue
		}

		// Determine which columns are present in this group.
		var activeCols []column
		for _, col := range columns {
			for _, row := range g.rows {
				if v := row[col.key]; v != "" {
					activeCols = append(activeCols, col)
					break
				}
			}
		}

		// Build headers.
		headers := make([]string, len(activeCols))
		for ci, col := range activeCols {
			headers[ci] = headerStyle.Render(col.header)
		}

		// Build rows with styled cell values.
		var rows [][]string
		for _, row := range g.rows {
			cells := make([]string, len(activeCols))
			for ci, col := range activeCols {
				v := row[col.key]
				if v == "" {
					cells[ci] = ""
				} else {
					cells[ci] = col.style(v, row)
				}
			}
			rows = append(rows, cells)
		}

		tbl := table.New().
			Headers(headers...).
			Rows(rows...).
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("238"))).
			StyleFunc(func(row, col int) lipgloss.Style {
				return lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
			})

		fmt.Println(tbl.Render())
	}
	return nil
}

// stripAnsi removes ANSI escape codes for raw width calculation.
func stripAnsi(s string) string {
	var out strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
