package console

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	clrOK     = lipgloss.Color("10")  // bright green
	clrErr    = lipgloss.Color("9")   // bright red
	clrWarn   = lipgloss.Color("11")  // bright yellow
	clrBlue   = lipgloss.Color("12")  // bright blue
	clrCyan   = lipgloss.Color("14")  // bright cyan
	clrDim    = lipgloss.Color("8")   // dark grey
	clrMuted  = lipgloss.Color("244") // medium grey
	clrFg     = lipgloss.Color("252") // near-white
	clrBorder = lipgloss.Color("238") // panel border
	clrSelBg  = lipgloss.Color("235") // selection background

	sOK    = lipgloss.NewStyle().Foreground(clrOK)
	sErr   = lipgloss.NewStyle().Foreground(clrErr)
	sWarn  = lipgloss.NewStyle().Foreground(clrWarn)
	sBlue  = lipgloss.NewStyle().Foreground(clrBlue)
	sCyan  = lipgloss.NewStyle().Foreground(clrCyan)
	sDim   = lipgloss.NewStyle().Foreground(clrDim)
	sMuted = lipgloss.NewStyle().Foreground(clrMuted)
	sBold  = lipgloss.NewStyle().Bold(true)
	sSel   = lipgloss.NewStyle().Background(clrSelBg).Foreground(clrFg)

	sBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrBorder)

	sBorderActive = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(clrBlue)
)

// pluginIcon returns a colored status indicator.
func pluginIcon(status string, enabled bool) string {
	if !enabled {
		return sDim.Render("○")
	}
	switch status {
	case "running":
		return sOK.Render("●")
	case "starting":
		return sWarn.Render("◉")
	default:
		return sErr.Render("✗")
	}
}

// statusColor returns a styled status string.
func statusColor(status string, enabled bool) string {
	if !enabled {
		return sDim.Render("disabled")
	}
	switch status {
	case "running":
		return sOK.Render(status)
	case "starting":
		return sWarn.Render(status)
	default:
		return sErr.Render(status)
	}
}

// pad pads/truncates s to exactly w visual columns.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}

// trunc truncates s to at most n visual columns, adding "…" if needed.
func trunc(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	// strip to n-1 bytes then add ellipsis (safe for ASCII paths/IDs)
	r := []rune(s)
	if len(r) > n-1 {
		r = r[:n-1]
	}
	return string(r) + "…"
}

// buildContent builds exactly h lines, each padded to w visual columns.
func buildContent(lines []string, h, w int) string {
	result := make([]string, h)
	for i := range h {
		if i < len(lines) {
			result[i] = pad(lines[i], w)
		} else {
			result[i] = pad("", w)
		}
	}
	return strings.Join(result, "\n")
}

// renderBox draws content in a rounded border.
// w is the INNER width; total rendered width = w+2.
// Content should have exactly h lines.
func renderBox(content string, w int, active bool) string {
	style := sBorder
	if active {
		style = sBorderActive
	}
	return style.Width(w).Render(content)
}

// sep returns a dim horizontal separator of n chars.
func sep(n int) string {
	if n <= 0 {
		return ""
	}
	return sDim.Render(strings.Repeat("─", n))
}
