package bot

import "regexp"

// Discord supports a Markdown-like syntax natively (bold, italic, code, code
// blocks, strikethrough, headers, "-" bullet lists, "1." numbered lists,
// blockquotes). The notable gap is that asterisk-prefixed list lines
// ("* item") are NOT recognised as a bullet list — only "-" works. We
// normalise those so agent output with mixed list styles renders as a list.

var dcListAsteriskRe = regexp.MustCompile(`(?m)^(\s*)\*\s+`)

// renderMarkdownForDiscord lightly normalises agent-produced Markdown so it
// renders correctly in Discord. The input is mostly already Discord-compatible.
func renderMarkdownForDiscord(text string) string {
	return dcListAsteriskRe.ReplaceAllString(text, "$1- ")
}
