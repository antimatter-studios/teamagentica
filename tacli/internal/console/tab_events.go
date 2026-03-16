package console

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

const maxEvents = 500

// debugEvent mirrors the kernel's events.DebugEvent shape.
type debugEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	PluginID  string    `json:"plugin_id"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	Duration  int64     `json:"duration_ms"`
	Detail    string    `json:"detail"`
}

// ── tab ───────────────────────────────────────────────────────────────────────

type eventsTab struct {
	events     []debugEvent
	autoScroll bool
	offset     int // scroll offset from bottom (0 = show newest)
}

func newEventsTab() eventsTab {
	return eventsTab{autoScroll: true}
}

func (t eventsTab) addEvent(e client.SSEEvent) eventsTab {
	if e.Channel != "audit" {
		return t
	}
	var evt debugEvent
	if err := json.Unmarshal(e.Data, &evt); err != nil {
		return t
	}
	t.events = append(t.events, evt)
	if len(t.events) > maxEvents {
		t.events = t.events[len(t.events)-maxEvents:]
	}
	if t.autoScroll {
		t.offset = 0
	}
	return t
}

func (t eventsTab) update(msg tea.Msg) (eventsTab, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "g":
			t.autoScroll = true
			t.offset = 0
		case "G":
			t.autoScroll = false
		case "j", "down":
			t.autoScroll = false
			if t.offset > 0 {
				t.offset--
			}
		case "k", "up":
			t.autoScroll = false
			t.offset++
		case "c":
			t.events = nil
			t.offset = 0
		}
	}
	return t, nil
}

func (t eventsTab) view(width, height int) string {
	innerW := width - 4 // 2 for border + 2 for margin
	innerH := height - 2

	// Collect visible lines
	lines := make([]string, 0, len(t.events))
	for _, e := range t.events {
		lines = append(lines, t.renderEvent(e, innerW))
	}

	// Apply scroll window
	start := 0
	if len(lines) > innerH {
		bottom := len(lines) - t.offset
		if bottom < innerH {
			bottom = innerH
		}
		if bottom > len(lines) {
			bottom = len(lines)
		}
		start = bottom - innerH
		if start < 0 {
			start = 0
		}
		lines = lines[start:bottom]
	}

	// Status line: auto-scroll indicator
	scrollNote := ""
	if !t.autoScroll {
		scrollNote = sWarn.Render(" [paused — g: jump to end]")
	} else if len(t.events) == 0 {
		scrollNote = sDim.Render(" waiting for events…")
	}

	// Build content
	contentLines := make([]string, 0, innerH)
	contentLines = append(contentLines, pad(sBold.Render(" Events")+scrollNote, innerW))
	contentLines = append(contentLines, sep(innerW))

	eventLines := innerH - 2
	for _, l := range lines {
		contentLines = append(contentLines, l)
	}
	// pad to fill
	for len(contentLines) < innerH {
		contentLines = append(contentLines, pad("", innerW))
	}
	if len(contentLines) > innerH {
		contentLines = contentLines[:innerH]
	}

	content := buildContent(contentLines, innerH, innerW)
	_ = eventLines
	box := renderBox(content, innerW, true)
	return "\n " + box
}

func (t eventsTab) renderEvent(e debugEvent, w int) string {
	ts := sDim.Render(e.Timestamp.Format("15:04:05"))

	var typeLabel string
	switch e.Type {
	case "register":
		typeLabel = sBlue.Render(fmt.Sprintf("%-12s", "register"))
	case "deregister":
		typeLabel = sWarn.Render(fmt.Sprintf("%-12s", "deregister"))
	case "heartbeat":
		typeLabel = sDim.Render(fmt.Sprintf("%-12s", "heartbeat"))
	case "install":
		typeLabel = sCyan.Render(fmt.Sprintf("%-12s", "install"))
	case "error":
		typeLabel = sErr.Render(fmt.Sprintf("%-12s", "error"))
	default:
		typeLabel = sMuted.Render(fmt.Sprintf("%-12s", e.Type))
	}

	plugin := sMuted.Render(fmt.Sprintf("%-24s", trunc(e.PluginID, 24)))

	var detail string
	if e.Type == "proxy" && e.Method != "" {
		var statusStr string
		switch {
		case e.Status >= 500:
			statusStr = sErr.Render(fmt.Sprintf("%d", e.Status))
		case e.Status >= 400:
			statusStr = sWarn.Render(fmt.Sprintf("%d", e.Status))
		default:
			statusStr = sOK.Render(fmt.Sprintf("%d", e.Status))
		}
		detail = fmt.Sprintf("%s %s %s %s",
			sDim.Render(e.Method),
			sMuted.Render(trunc(e.Path, 30)),
			statusStr,
			sDim.Render(fmt.Sprintf("%dms", e.Duration)),
		)
	} else if e.Detail != "" {
		detail = sDim.Render(trunc(e.Detail, w-60))
	}

	line := strings.Join([]string{" ", ts, " ", typeLabel, " ", plugin, " ", detail}, "")
	return trunc(line, w)
}

func (t eventsTab) helpLine() string {
	return "j/k: scroll  g: jump to end  G: pause scroll  c: clear"
}
