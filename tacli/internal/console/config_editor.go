package console

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/antimatter-studios/teamagentica/tacli/internal/client"
)

// ── messages ──────────────────────────────────────────────────────────────────

// schemaSection is a readonly key-value section from the plugin schema (non-config).
type schemaSection struct {
	Name   string
	Fields []schemaSectionField
}

type schemaSectionField struct {
	Key   string
	Value string
}

type configLoadedMsg struct {
	pluginID      string
	items         []configField
	oauthAuthed   bool
	extraSections []schemaSection
	err           error
}

type configSavedMsg struct {
	err error
}

type oauthDeviceCodeMsg struct {
	url  string
	code string
	err  error
}

type oauthPollMsg struct {
	authenticated bool
	err           error
}

type oauthSubmitCodeMsg struct {
	authenticated bool
	err           error
}

type oauthTickMsg time.Time

// ── config field (merged schema + stored value) ──────────────────────────────

type configField struct {
	Key      string
	Label    string
	Value    string
	Type     string // "string", "select", "boolean", "oauth", "number", "text", "aliases", "bot_token"
	Secret   bool
	Required bool
	ReadOnly bool
	Default  string
	Options  []string
	HelpText string

	VisibleWhen *client.VisibleWhen
}

// aliasEntry is a single name→target pair stored as JSON in the aliases field.
type aliasEntry struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// botTokenEntry is a single alias→token pair stored as JSON in a bot_token field.
type botTokenEntry struct {
	Alias string `json:"alias"`
	Token string `json:"token"`
}

// visibleItem represents one row in the config editor's navigable list.
// Regular fields have aliasIdx == -1. Alias/bot_token sub-rows have aliasIdx >= 0.
type visibleItem struct {
	fieldIdx int
	aliasIdx int // -1 = normal field, >=0 = sub-row index (aliases or bot_token entries)
	aliasCol int // 0=name/alias, 1=target/token, 2=[X] delete — tracks focused cell within the row
	isAdd    bool // true = the [Add] button for aliases/bot_token
}

func (v *visibleItem) nextCol() {
	if v.aliasCol < 2 {
		v.aliasCol++
	}
}

func (v *visibleItem) prevCol() {
	if v.aliasCol > 0 {
		v.aliasCol--
	}
}

// ── editor ───────────────────────────────────────────────────────────────────

type configEditor struct {
	c        *client.Client
	pluginID string
	fields   []configField
	visible  []visibleItem
	cursor   int
	editing  bool
	input    textinput.Model
	dirty    map[string]string
	loading  bool
	saving   bool
	status   string
	scroll   int

	// inline select picker state
	selecting    bool   // true when a select dropdown is open
	selectOpts   []string
	selectCursor int
	selectField  string // key of the field being selected

	// readonly schema sections (non-config)
	extraSections []schemaSection

	// oauth state
	oauthURL        string
	oauthCode       string
	oauthPoll       bool
	oauthAuthed     bool
	oauthWaiting    bool      // waiting for user to paste auth code
	oauthSubmitting bool      // code submitted, waiting for response
	oauthDeadline   time.Time // countdown deadline (zero = no countdown)
}

func newConfigEditor(c *client.Client) configEditor {
	ti := textinput.New()
	ti.CharLimit = 1024
	return configEditor{
		c:     c,
		dirty: make(map[string]string),
		input: ti,
	}
}

func (e *configEditor) open(pluginID string) tea.Cmd {
	e.pluginID = pluginID
	e.fields = nil
	e.visible = nil
	e.cursor = 0
	e.editing = false
	e.input.Reset()
	e.input.Blur()
	e.dirty = make(map[string]string)
	e.loading = true
	e.saving = false
	e.status = ""
	e.scroll = 0
	e.oauthURL = ""
	e.oauthCode = ""
	e.oauthPoll = false
	e.oauthWaiting = false
	e.oauthSubmitting = false
	e.oauthDeadline = time.Time{}
	return doLoadConfig(e.c, pluginID)
}

func (e *configEditor) close() {
	e.pluginID = ""
	e.fields = nil
	e.visible = nil
}

func (e configEditor) active() bool  { return e.pluginID != "" }
func (e configEditor) inputActive() bool { return e.editing || e.selecting || e.oauthPoll || e.oauthSubmitting }

func (e configEditor) selectedItem() *visibleItem {
	if e.cursor < 0 || e.cursor >= len(e.visible) {
		return nil
	}
	return &e.visible[e.cursor]
}

func (e configEditor) selectedField() *configField {
	item := e.selectedItem()
	if item == nil {
		return nil
	}
	return &e.fields[item.fieldIdx]
}

// startEditing configures the textinput and enters editing mode.
func (e *configEditor) startEditing(value string) {
	e.editing = true
	e.input.Reset()
	e.input.EchoMode = textinput.EchoNormal
	e.input.SetValue(value)
	e.input.CursorEnd()
	e.input.Focus()
}

func (e *configEditor) stopEditing() {
	e.editing = false
	e.input.Blur()
}

// ── aliases helpers ──────────────────────────────────────────────────────────

func (e configEditor) getAliases(f *configField) []aliasEntry {
	val := e.currentValue(f)
	var entries []aliasEntry
	if val != "" {
		_ = json.Unmarshal([]byte(val), &entries)
	}
	return entries
}

func (e *configEditor) setAliases(f *configField, entries []aliasEntry) {
	b, _ := json.Marshal(entries)
	if len(entries) == 0 {
		b = []byte("[]")
	}
	e.dirty[f.Key] = string(b)
	e.applyDirty(f.Key, string(b))
}

// ── bot_token helpers ────────────────────────────────────────────────────────

func (e configEditor) getBotTokens(f *configField) []botTokenEntry {
	val := e.currentValue(f)
	var entries []botTokenEntry
	if val != "" {
		_ = json.Unmarshal([]byte(val), &entries)
	}
	return entries
}

func (e *configEditor) setBotTokens(f *configField, entries []botTokenEntry) {
	b, _ := json.Marshal(entries)
	if len(entries) == 0 {
		b = []byte("[]")
	}
	e.dirty[f.Key] = string(b)
	e.applyDirty(f.Key, string(b))
}

// ── update ───────────────────────────────────────────────────────────────────

func (e configEditor) update(msg tea.Msg) (configEditor, tea.Cmd) {
	switch msg := msg.(type) {
	case configLoadedMsg:
		e.loading = false
		if msg.err != nil {
			e.status = "load failed: " + msg.err.Error()
		} else if msg.pluginID == e.pluginID {
			e.fields = msg.items
			e.oauthAuthed = msg.oauthAuthed
			e.extraSections = msg.extraSections
			e.recomputeVisible()
			e.cursor = 0
			e.scroll = 0
		}

	case configSavedMsg:
		e.saving = false
		if msg.err != nil {
			e.status = "save failed: " + msg.err.Error()
		} else {
			e.status = "config saved"
			e.dirty = make(map[string]string)
			return e, doLoadConfig(e.c, e.pluginID)
		}

	case oauthDeviceCodeMsg:
		e.loading = false
		if msg.err != nil {
			e.status = "oauth error: " + msg.err.Error()
		} else {
			e.oauthURL = msg.url
			e.oauthCode = msg.code
			e.status = ""
			if msg.code == "" {
				// Flow requires user to paste code back (e.g. Claude OAuth).
				e.oauthWaiting = true
				e.oauthDeadline = time.Now().Add(60 * time.Second)
				e.startEditing("")
				return e, doOAuthTick()
			}
			// Device-code flow (e.g. OpenAI) — poll for completion.
			e.oauthPoll = true
			return e, doOAuthPoll(e.c, e.pluginID)
		}

	case oauthTickMsg:
		if e.oauthWaiting && !e.oauthDeadline.IsZero() {
			if time.Now().After(e.oauthDeadline) {
				e.oauthWaiting = false
				e.oauthURL = ""
				e.oauthDeadline = time.Time{}
				e.stopEditing()
				e.status = "auth timed out"
				return e, nil
			}
			return e, doOAuthTick()
		}
		if !e.oauthDeadline.IsZero() {
			return e, doOAuthTick()
		}

	case oauthSubmitCodeMsg:
		e.oauthSubmitting = false
		e.oauthURL = ""
		e.oauthDeadline = time.Time{}
		e.input.Blur()
		if msg.err != nil {
			e.status = "auth error: " + msg.err.Error()
		} else if msg.authenticated {
			e.oauthAuthed = true
			e.status = "authenticated"
		} else {
			e.status = "auth failed — invalid code"
		}

	case oauthPollMsg:
		// Device-code flow polling (OpenAI etc.)
		if msg.err != nil {
			e.oauthPoll = false
			e.status = "oauth poll error: " + msg.err.Error()
		} else if msg.authenticated {
			e.oauthPoll = false
			e.oauthURL = ""
			e.oauthCode = ""
			e.oauthAuthed = true
			e.status = "authenticated"
		} else if e.oauthPoll {
			return e, doOAuthPollAfterDelay(e.c, e.pluginID)
		}

	case tea.PasteMsg:
		if e.editing || e.oauthWaiting {
			var cmd tea.Cmd
			e.input, cmd = e.input.Update(msg)
			return e, cmd
		}

	case tea.KeyPressMsg:
		if e.oauthSubmitting {
			return e, nil // ignore keys while submitting
		}
		if e.oauthPoll {
			if msg.String() == "esc" {
				e.oauthPoll = false
				e.oauthURL = ""
				e.oauthCode = ""
				e.status = "oauth cancelled"
			}
			return e, nil
		}
		if e.selecting {
			return e.updateSelecting(msg)
		}
		if e.editing {
			return e.updateEditing(msg)
		}
		return e.updateNavigating(msg)
	}

	return e, nil
}

func (e configEditor) updateNavigating(msg tea.KeyPressMsg) (configEditor, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if e.cursor < len(e.visible)-1 {
			e.cursor++
		}
	case "k", "up":
		if e.cursor > 0 {
			e.cursor--
		}
	case "l", "right":
		if item := e.selectedItem(); item != nil && item.aliasIdx >= 0 {
			item.nextCol()
		} else if f := e.selectedField(); f != nil && f.Type == "boolean" {
			if e.currentValue(f) == "true" {
				e.dirty[f.Key] = "false"
				e.applyDirty(f.Key, "false")
			}
		}
	case "h", "left":
		if item := e.selectedItem(); item != nil && item.aliasIdx >= 0 {
			item.prevCol()
		} else if f := e.selectedField(); f != nil && f.Type == "boolean" {
			if e.currentValue(f) != "true" {
				e.dirty[f.Key] = "true"
				e.applyDirty(f.Key, "true")
			}
		}
	case "enter":
		item := e.selectedItem()
		if item == nil {
			break
		}
		f := &e.fields[item.fieldIdx]

		// alias sub-row actions
		if f.Type == "aliases" {
			if item.isAdd {
				entries := e.getAliases(f)
				entries = append(entries, aliasEntry{})
				e.setAliases(f, entries)
				e.recomputeVisible()
				for i, v := range e.visible {
					if v.fieldIdx == item.fieldIdx && v.aliasIdx == len(entries)-1 && v.aliasCol == 0 {
						e.cursor = i
						break
					}
				}
				e.startEditing("")
				break
			}
			if item.aliasCol == 2 {
				entries := e.getAliases(f)
				if item.aliasIdx < len(entries) {
					entries = append(entries[:item.aliasIdx], entries[item.aliasIdx+1:]...)
					e.setAliases(f, entries)
					e.recomputeVisible()
					if e.cursor >= len(e.visible) {
						e.cursor = max(0, len(e.visible)-1)
					}
				}
				break
			}
			if item.aliasIdx >= 0 {
				entries := e.getAliases(f)
				if item.aliasIdx < len(entries) {
					val := entries[item.aliasIdx].Name
					if item.aliasCol == 1 {
						val = entries[item.aliasIdx].Target
					}
					e.startEditing(val)
				}
				break
			}
		}

		// bot_token sub-row actions
		if f.Type == "bot_token" {
			if item.isAdd {
				entries := e.getBotTokens(f)
				entries = append(entries, botTokenEntry{})
				e.setBotTokens(f, entries)
				e.recomputeVisible()
				for i, v := range e.visible {
					if v.fieldIdx == item.fieldIdx && v.aliasIdx == len(entries)-1 && v.aliasCol == 0 {
						e.cursor = i
						break
					}
				}
				e.startEditing("")
				break
			}
			if item.aliasCol == 2 {
				entries := e.getBotTokens(f)
				if item.aliasIdx < len(entries) {
					entries = append(entries[:item.aliasIdx], entries[item.aliasIdx+1:]...)
					e.setBotTokens(f, entries)
					e.recomputeVisible()
					if e.cursor >= len(e.visible) {
						e.cursor = max(0, len(e.visible)-1)
					}
				}
				break
			}
			if item.aliasIdx >= 0 {
				entries := e.getBotTokens(f)
				if item.aliasIdx < len(entries) {
					if item.aliasCol == 1 {
						// token — start empty for secret entry with masked input
						e.startEditing("")
						e.input.EchoMode = textinput.EchoPassword
					} else {
						e.startEditing(entries[item.aliasIdx].Alias)
					}
				}
				break
			}
		}

		// regular field actions
		if f.Type == "oauth" {
			e.loading = true
			e.status = "starting oauth…"
			return e, doOAuthDeviceCode(e.c, e.pluginID)
		}
		if f.Type == "select" && len(f.Options) > 0 {
			e.selecting = true
			e.selectOpts = f.Options
			e.selectField = f.Key
			e.selectCursor = 0
			current := e.currentValue(f)
			for i, o := range f.Options {
				if o == current {
					e.selectCursor = i
					break
				}
			}
			return e, nil
		}
		if f.Type == "boolean" {
			current := e.currentValue(f)
			if current == "true" {
				e.dirty[f.Key] = "false"
				e.applyDirty(f.Key, "false")
			} else {
				e.dirty[f.Key] = "true"
				e.applyDirty(f.Key, "true")
			}
			return e, nil
		}
		if f.Type == "aliases" || f.Type == "bot_token" {
			// label row — do nothing, user navigates to sub-rows
			break
		}
		if f.Secret {
			e.startEditing("")
		} else {
			e.startEditing(e.currentValue(f))
		}

	case "s":
		if len(e.dirty) > 0 && !e.saving {
			e.saving = true
			e.status = "saving…"
			return e, doSaveConfig(e.c, e.pluginID, e.dirty)
		}
		if len(e.dirty) == 0 {
			e.status = "no changes to save"
		}
	case "esc":
		if len(e.dirty) > 0 {
			e.dirty = make(map[string]string)
			e.status = "changes discarded"
			// reload clean state
			return e, doLoadConfig(e.c, e.pluginID)
		}
	}
	return e, nil
}

func (e configEditor) updateSelecting(msg tea.KeyPressMsg) (configEditor, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if e.selectCursor < len(e.selectOpts)-1 {
			e.selectCursor++
		}
	case "k", "up":
		if e.selectCursor > 0 {
			e.selectCursor--
		}
	case "enter":
		val := e.selectOpts[e.selectCursor]
		e.dirty[e.selectField] = val
		e.applyDirty(e.selectField, val)
		e.selecting = false
		e.selectOpts = nil
		e.recomputeVisible()
	case "esc":
		e.selecting = false
		e.selectOpts = nil
	}
	return e, nil
}

func (e configEditor) updateEditing(msg tea.KeyPressMsg) (configEditor, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if e.oauthWaiting {
			e.oauthURL = ""
			e.oauthWaiting = false
			e.oauthDeadline = time.Time{}
			e.status = "oauth cancelled"
		}
		e.stopEditing()
		return e, nil
	case "enter":
		if e.oauthWaiting {
			code := e.input.Value()
			if code != "" {
				e.stopEditing()
				e.oauthWaiting = false
				e.oauthSubmitting = true
				e.oauthDeadline = time.Now().Add(60 * time.Second)
				e.status = "submitting code…"
				return e, tea.Batch(doOAuthSubmitCode(e.c, e.pluginID, code), doOAuthTick())
			}
			return e, nil
		}
		value := e.input.Value()
		item := e.selectedItem()
		if item != nil && item.aliasIdx >= 0 {
			f := &e.fields[item.fieldIdx]

			if f.Type == "bot_token" {
				// save bot_token cell
				entries := e.getBotTokens(f)
				if item.aliasIdx < len(entries) {
					if item.aliasCol == 0 {
						entries[item.aliasIdx].Alias = value
					} else {
						entries[item.aliasIdx].Token = value
					}
					e.setBotTokens(f, entries)
					e.recomputeVisible()
				}
				// auto-advance: alias → token (same row, switch column)
				if item.aliasCol == 0 {
					e.visible[e.cursor].aliasCol = 1
					e.input.Reset()
					e.input.EchoMode = textinput.EchoPassword
					e.input.SetValue("")
					e.input.CursorEnd()
					e.input.Focus()
					return e, nil // stay in editing mode
				}
			} else {
				// save alias cell
				entries := e.getAliases(f)
				if item.aliasIdx < len(entries) {
					if item.aliasCol == 0 {
						entries[item.aliasIdx].Name = value
					} else {
						entries[item.aliasIdx].Target = value
					}
					e.setAliases(f, entries)
					e.recomputeVisible()
				}
				// auto-advance: name → target (same row, switch column)
				if item.aliasCol == 0 {
					e.visible[e.cursor].aliasCol = 1
					entries = e.getAliases(f)
					val := ""
					if item.aliasIdx < len(entries) {
						val = entries[item.aliasIdx].Target
					}
					e.input.Reset()
					e.input.EchoMode = textinput.EchoNormal
					e.input.SetValue(val)
					e.input.CursorEnd()
					e.input.Focus()
					return e, nil // stay in editing mode
				}
			}
		} else if item != nil {
			f := &e.fields[item.fieldIdx]
			e.dirty[f.Key] = value
			e.applyDirty(f.Key, value)
		}
		e.stopEditing()
		return e, nil
	default:
		// delegate all other keys to the textinput
		var cmd tea.Cmd
		e.input, cmd = e.input.Update(msg)
		return e, cmd
	}
}

// ── visibility logic ─────────────────────────────────────────────────────────

func (e *configEditor) recomputeVisible() {
	e.visible = nil
	for i, f := range e.fields {
		if f.VisibleWhen != nil {
			depVal := e.fieldValue(f.VisibleWhen.Field)
			if depVal != f.VisibleWhen.Value {
				continue
			}
		}
		if f.Type == "aliases" {
			// label row
			e.visible = append(e.visible, visibleItem{fieldIdx: i, aliasIdx: -1})
			// one row per alias — left/right switches aliasCol within the row
			entries := e.getAliases(&e.fields[i])
			for ai := range entries {
				e.visible = append(e.visible, visibleItem{fieldIdx: i, aliasIdx: ai, aliasCol: 0})
			}
			// [Add] button
			e.visible = append(e.visible, visibleItem{fieldIdx: i, aliasIdx: -1, isAdd: true})
		} else if f.Type == "bot_token" {
			// label row
			e.visible = append(e.visible, visibleItem{fieldIdx: i, aliasIdx: -1})
			// one row per bot — left/right switches aliasCol within the row
			entries := e.getBotTokens(&e.fields[i])
			for bi := range entries {
				e.visible = append(e.visible, visibleItem{fieldIdx: i, aliasIdx: bi, aliasCol: 0})
			}
			// [Add] button
			e.visible = append(e.visible, visibleItem{fieldIdx: i, aliasIdx: -1, isAdd: true})
		} else {
			e.visible = append(e.visible, visibleItem{fieldIdx: i, aliasIdx: -1})
		}
	}
	if e.cursor >= len(e.visible) {
		e.cursor = max(0, len(e.visible)-1)
	}
}

func (e configEditor) fieldValue(key string) string {
	if v, ok := e.dirty[key]; ok {
		return v
	}
	for _, f := range e.fields {
		if f.Key == key {
			if f.Value != "" && f.Value != "********" {
				return f.Value
			}
			return f.Default
		}
	}
	return ""
}

func (e configEditor) currentValue(f *configField) string {
	if v, ok := e.dirty[f.Key]; ok {
		return v
	}
	if f.Value != "" && f.Value != "********" {
		return f.Value
	}
	return f.Default
}

func (e *configEditor) applyDirty(key, val string) {
	for i := range e.fields {
		if e.fields[i].Key == key {
			e.fields[i].Value = val
			break
		}
	}
}

// ── rendering ────────────────────────────────────────────────────────────────

func randomHex(n int) string {
	b := make([]byte, n/2+1)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func (e configEditor) render(w, h int, summary *client.PluginSummary, detail *client.PluginDetail, allPlugins []client.PluginSummary) []string {
	var lines []string

	// plugin info header
	if summary != nil {
		lines = append(lines, sBold.Render(" "+summary.Name))
		lines = append(lines, sep(w))
		lines = append(lines, fmt.Sprintf("  %-14s %s", "Status", statusColor(summary.Status, summary.Enabled)))
		lines = append(lines, fmt.Sprintf("  %-14s %s", "Version", sMuted.Render(summary.Version)))
		if detail != nil {
			lines = append(lines, wrap(fmt.Sprintf("  %-14s %s", "Image", sMuted.Render(detail.Image)), w)...)
			if len(detail.Capabilities) > 0 {
				lines = append(lines, wrap(fmt.Sprintf("  %-14s %s", "Capabilities", sCyan.Render(strings.Join(detail.Capabilities, ", "))), w)...)
			}
			if len(detail.Dependencies) > 0 {
				lines = append(lines, fmt.Sprintf("  %-14s %s", "Dependencies", ""))
				for _, dep := range detail.Dependencies {
					if capSatisfied(dep, allPlugins) {
						lines = append(lines, "    "+sDepOk.Render(dep))
					} else {
						lines = append(lines, "    "+sDepMissing.Render(dep+" (not satisfied)"))
					}
				}
			}
		}
		lines = append(lines, "")
	}

	lines = append(lines, sBold.Render(" Config"))
	lines = append(lines, sep(w))

	if e.loading && len(e.fields) == 0 {
		lines = append(lines, sMuted.Render("  loading…"))
		return lines
	}

	// oauth device-code prompt
	if (e.oauthPoll || e.oauthWaiting || e.oauthSubmitting) && e.oauthURL != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+sBold.Render("OAuth Login"))
		lines = append(lines, "")
		linkStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5599FF")).
			Underline(true).
			Hyperlink(e.oauthURL)
		lines = append(lines, "  "+linkStyle.Render("Click here to login"))
		if e.oauthCode != "" {
			lines = append(lines, "  Code: "+sBold.Render(e.oauthCode))
		}
		lines = append(lines, "")
		if e.oauthSubmitting {
			lines = append(lines, "  "+sMuted.Render("Submitting code… "+e.countdownStr()))
		} else if !e.oauthWaiting && !e.oauthDeadline.IsZero() {
			lines = append(lines, "  "+sMuted.Render("Verifying… "+e.countdownStr()))
		} else if e.oauthWaiting {
			lines = append(lines, wrap("  Open the URL, sign in, then paste the code below.", w)...)
			lines = append(lines, "")
			lines = append(lines, "  Code: "+e.input.View())
			lines = append(lines, "")
			lines = append(lines, "  "+sMuted.Render("enter to submit · esc to cancel · "+e.countdownStr()))
		} else if e.oauthCode != "" {
			lines = append(lines, wrap("  Open the URL in your browser and enter the code.", w)...)
			lines = append(lines, "  "+sMuted.Render("Waiting for authentication… (esc to cancel)"))
		} else {
			lines = append(lines, "  "+sMuted.Render("Waiting for authentication… (esc to cancel)"))
		}
		return lines
	}

	// scroll viewport
	headerLines := len(lines)
	bottomReserved := 1
	viewRows := h - headerLines - bottomReserved
	if viewRows < 1 {
		viewRows = 1
	}
	if e.cursor < e.scroll {
		e.scroll = e.cursor
	} else if e.cursor >= e.scroll+viewRows {
		e.scroll = e.cursor - viewRows + 1
	}

	// label width for regular fields
	labelW := 12
	for _, item := range e.visible {
		if item.aliasIdx >= 0 || item.isAdd {
			continue
		}
		f := e.fields[item.fieldIdx]
		label := f.Label
		if label == "" {
			label = f.Key
		}
		if len(label) > labelW {
			labelW = len(label)
		}
	}
	if labelW > w/3 {
		labelW = w / 3
	}

	end := e.scroll + viewRows
	if end > len(e.visible) {
		end = len(e.visible)
	}

	for vi := e.scroll; vi < end; vi++ {
		item := e.visible[vi]
		f := e.fields[item.fieldIdx]
		selected := vi == e.cursor

		if item.isAdd {
			// [Add] button — label varies by field type
			addLabel := "[Add alias]"
			if f.Type == "bot_token" {
				addLabel = "[+ Add bot]"
			}
			line := "      " + sMuted.Render(addLabel)
			if selected {
				line = sSel.Render(pad(stripANSI(line), w))
			}
			lines = append(lines, line)
			continue
		}

		if item.aliasIdx >= 0 && f.Type == "bot_token" {
			// bot_token row: render [alias] [token] [X] on one line
			entries := e.getBotTokens(&f)
			if item.aliasIdx >= len(entries) {
				continue
			}
			be := entries[item.aliasIdx]
			aliasW := (w - 14) * 35 / 100
			if aliasW < 8 {
				aliasW = 8
			}
			tokenW := w - aliasW - 14

			aliasVal := be.Alias
			tokenVal := "********"
			if be.Token == "" {
				tokenVal = ""
			}
			if e.editing && selected && item.aliasCol == 0 {
				aliasVal = e.input.View()
			}
			if e.editing && selected && item.aliasCol == 1 {
				tokenVal = e.input.View()
			}

			aliasStr := trunc(aliasVal, aliasW)
			tokenStr := trunc(tokenVal, tokenW)
			delStr := "[X]"

			if selected {
				switch item.aliasCol {
				case 0:
					aliasStr = sBold.Render(aliasStr)
				case 1:
					tokenStr = sBold.Render(tokenStr)
				case 2:
					delStr = sBold.Render(delStr)
				}
			}

			line := fmt.Sprintf("    %-*s  %-*s  %s",
				aliasW, aliasStr,
				tokenW, tokenStr,
				delStr,
			)

			if selected {
				line = sSel.Render(pad(stripANSI(line), w))
			}
			lines = append(lines, line)
			continue
		}

		if item.aliasIdx >= 0 {
			// alias row: render [name] [target] [X] on one line
			entries := e.getAliases(&f)
			if item.aliasIdx >= len(entries) {
				continue
			}
			ae := entries[item.aliasIdx]
			nameW := (w - 14) * 40 / 100
			if nameW < 8 {
				nameW = 8
			}
			targetW := w - nameW - 14

			nameVal := ae.Name
			targetVal := ae.Target
			if e.editing && selected && item.aliasCol == 0 {
				nameVal = e.input.View()
			}
			if e.editing && selected && item.aliasCol == 1 {
				targetVal = e.input.View()
			}

			// highlight focused cell with underline-style brackets
			nameStr := trunc(nameVal, nameW)
			targetStr := trunc(targetVal, targetW)
			delStr := "[X]"

			if selected {
				switch item.aliasCol {
				case 0:
					nameStr = sBold.Render(nameStr)
				case 1:
					targetStr = sBold.Render(targetStr)
				case 2:
					delStr = sBold.Render(delStr)
				}
			}

			line := fmt.Sprintf("    %-*s  %-*s  %s",
				nameW, nameStr,
				targetW, targetStr,
				delStr,
			)

			if selected {
				line = sSel.Render(pad(stripANSI(line), w))
			}
			lines = append(lines, line)
			continue
		}

		// regular field
		label := f.Label
		if label == "" {
			label = f.Key
		}

		var valStr string
		if e.editing && selected {
			valStr = e.input.View()
		} else {
			valStr = e.currentValue(&f)
			if f.Secret && f.Value != "" {
				if _, isDirty := e.dirty[f.Key]; !isDirty {
					valStr = "********"
				}
			}
			switch f.Type {
			case "select":
				if e.selecting && f.Key == e.selectField {
					valStr = sMuted.Render("▼ select…")
				} else {
					valStr = valStr + " " + sMuted.Render("▸")
				}
			case "boolean":
				if valStr == "true" {
					valStr = sOK.Render(" Enabled ") + "  " + sMuted.Render(" Disabled ")
				} else {
					valStr = sMuted.Render(" Enabled ") + "  " + sErr.Render(" Disabled ")
				}
			case "aliases":
				entries := e.getAliases(&f)
				valStr = fmt.Sprintf("%d aliases", len(entries))
			case "bot_token":
				entries := e.getBotTokens(&f)
				if len(entries) == 1 {
					valStr = "1 bot"
				} else {
					valStr = fmt.Sprintf("%d bots", len(entries))
				}
			case "oauth":
				if e.oauthAuthed {
					valStr = sOK.Render("✓ authenticated")
				} else {
					valStr = sMuted.Render("[press Enter to login]")
				}
			}
		}

		isDirty := false
		if _, ok := e.dirty[f.Key]; ok {
			isDirty = true
		}
		dirtyMark := "  "
		if isDirty {
			dirtyMark = sWarn.Render("* ")
		}
		reqMark := " "
		if f.Required {
			reqMark = sErr.Render("*")
		}

		line := fmt.Sprintf(" %s%s%-*s  %s",
			dirtyMark, reqMark,
			labelW, trunc(label, labelW),
			trunc(valStr, w-labelW-6),
		)

		if selected {
			line = sSel.Render(pad(stripANSI(line), w))
		}

		lines = append(lines, line)

		// render inline select picker below the field
		if e.selecting && f.Key == e.selectField {
			for oi, opt := range e.selectOpts {
				prefix := "    "
				if oi == e.selectCursor {
					lines = append(lines, sSel.Render(pad(prefix+"▸ "+opt, w)))
				} else {
					lines = append(lines, prefix+"  "+opt)
				}
			}
		}
	}

	// readonly schema sections (non-config)
	if len(e.extraSections) > 0 {
		for _, sec := range e.extraSections {
			lines = append(lines, "")
			lines = append(lines, sBold.Render(" "+titleCase(sec.Name)))
			lines = append(lines, sep(w))
			for _, f := range sec.Fields {
				line := fmt.Sprintf("  %-*s  %s", labelW, trunc(f.Key, labelW), sMuted.Render(f.Value))
				lines = append(lines, wrap(line, w)...)
			}
		}
	}

	// bottom help/status
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	if len(lines) > h-1 {
		lines = lines[:h-1]
	}
	if e.status != "" {
		lines = append(lines, sMuted.Width(w).Render("  "+e.status))
	} else {
		help := "j/k: navigate  Enter: edit  s: save  Esc: close"
		if len(e.dirty) > 0 {
			help = fmt.Sprintf("j/k: navigate  Enter: edit  s: save (%d changes)  Esc: discard", len(e.dirty))
		}
		lines = append(lines, sMuted.Render("  "+trunc(help, w-4)))
	}

	return lines
}

func (e configEditor) countdownStr() string {
	if e.oauthDeadline.IsZero() {
		return ""
	}
	remaining := time.Until(e.oauthDeadline)
	if remaining <= 0 {
		return "(0s)"
	}
	return fmt.Sprintf("(%ds)", int(remaining.Seconds())+1)
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func joinOpts(opts []string) string {
	if len(opts) == 0 {
		return ""
	}
	s := opts[0]
	for _, o := range opts[1:] {
		s += "|" + o
	}
	return s
}

// ── commands ──────────────────────────────────────────────────────────────────

func doLoadConfig(c *client.Client, pluginID string) tea.Cmd {
	return func() tea.Msg {
		items, err := c.PluginConfigItems(pluginID)
		if err != nil {
			return configLoadedMsg{pluginID: pluginID, err: err}
		}

		schema, _ := c.PluginConfigSchema(pluginID)

		// Fetch full schema to extract non-config sections as readonly displays.
		var extraSections []schemaSection
		if fullRaw, err := c.PluginSchema(pluginID); err == nil {
			var full map[string]json.RawMessage
			if json.Unmarshal(fullRaw, &full) == nil {
				for sectionName, raw := range full {
					if sectionName == "config" {
						continue
					}
					var kvMap map[string]interface{}
					if json.Unmarshal(raw, &kvMap) != nil {
						continue
					}
					var fields []schemaSectionField
					for k, v := range kvMap {
						var display string
						switch val := v.(type) {
						case string:
							display = val
						case nil:
							display = ""
						default:
							b, _ := json.Marshal(val)
							display = string(b)
						}
						fields = append(fields, schemaSectionField{Key: k, Value: display})
					}
					sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
					extraSections = append(extraSections, schemaSection{Name: sectionName, Fields: fields})
				}
				sort.Slice(extraSections, func(i, j int) bool { return extraSections[i].Name < extraSections[j].Name })
			}
		}

		var fields []configField

		if schema != nil {
			type entry struct {
				key   string
				field client.ConfigSchemaField
			}
			entries := make([]entry, 0, len(schema))
			for k, f := range schema {
				entries = append(entries, entry{k, f})
			}
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].field.Order != entries[j].field.Order {
					return entries[i].field.Order < entries[j].field.Order
				}
				return entries[i].key < entries[j].key
			})

			storedMap := make(map[string]client.ConfigItem, len(items))
			for _, item := range items {
				storedMap[item.Key] = item
			}

			seen := make(map[string]bool)
			for _, e := range entries {
				if e.field.ReadOnly {
					continue
				}
				seen[e.key] = true
				val := ""
				if item, ok := storedMap[e.key]; ok {
					val = item.Value
				}
				fields = append(fields, configField{
					Key:         e.key,
					Label:       e.field.Label,
					Value:       val,
					Type:        e.field.Type,
					Secret:      e.field.Secret,
					Required:    e.field.Required,
					Default:     e.field.Default,
					Options:     e.field.Options,
					HelpText:    e.field.HelpText,
					VisibleWhen: e.field.VisibleWhen,
				})
			}
			for _, item := range items {
				if seen[item.Key] {
					continue
				}
				fields = append(fields, configField{
					Key:    item.Key,
					Label:  item.Label,
					Value:  item.Value,
					Type:   "string",
					Secret: item.IsSecret,
				})
			}
		} else {
			for _, item := range items {
				fields = append(fields, configField{
					Key:    item.Key,
					Label:  item.Label,
					Value:  item.Value,
					Type:   "string",
					Secret: item.IsSecret,
				})
			}
		}

		hasOAuth := false
		for _, f := range fields {
			if f.Type == "oauth" {
				hasOAuth = true
				break
			}
		}
		var oauthAuthed bool
		if hasOAuth {
			oauthAuthed, _ = c.OAuthStatus(pluginID)
		}

		return configLoadedMsg{pluginID: pluginID, items: fields, oauthAuthed: oauthAuthed, extraSections: extraSections}
	}
}

func doLoadConfigAfterDelay(c *client.Client, pluginID string, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return doLoadConfig(c, pluginID)()
	})
}

func doSaveConfig(c *client.Client, pluginID string, dirty map[string]string) tea.Cmd {
	values := make(map[string]string, len(dirty))
	for k, v := range dirty {
		values[k] = v
	}
	return func() tea.Msg {
		err := c.SetPluginConfig(pluginID, values)
		return configSavedMsg{err: err}
	}
}

func doOAuthDeviceCode(c *client.Client, pluginID string) tea.Cmd {
	return func() tea.Msg {
		result, err := c.OAuthDeviceCode(pluginID)
		if err != nil {
			return oauthDeviceCodeMsg{err: err}
		}
		return oauthDeviceCodeMsg{url: result.URL, code: result.Code}
	}
}

func doOAuthTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return oauthTickMsg(t)
	})
}

func doOAuthSubmitCode(c *client.Client, pluginID, code string) tea.Cmd {
	return func() tea.Msg {
		authed, err := c.OAuthSubmitCode(pluginID, code)
		return oauthSubmitCodeMsg{authenticated: authed, err: err}
	}
}

func doOAuthPoll(c *client.Client, pluginID string) tea.Cmd {
	return func() tea.Msg {
		authenticated, err := c.OAuthPoll(pluginID)
		return oauthPollMsg{authenticated: authenticated, err: err}
	}
}

func doOAuthPollAfterDelay(c *client.Client, pluginID string) tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		authenticated, err := c.OAuthPoll(pluginID)
		return oauthPollMsg{authenticated: authenticated, err: err}
	})
}
