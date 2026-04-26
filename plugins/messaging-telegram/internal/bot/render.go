package bot

import (
	"fmt"
	"regexp"
	"strings"
)

// Telegram supports a small set of HTML tags for parse_mode=HTML:
//   <b>, <i>, <u>, <s>, <code>, <pre>, <a href="...">.
// Lists, headers, blockquotes and horizontal rules are NOT rendered natively,
// so we degrade them to readable plain-text equivalents (e.g. "• ", bold lines).

var (
	tgCodeBlockRe = regexp.MustCompile("(?s)```([a-zA-Z0-9_+\\-]*)\\n?(.*?)```")
	tgInlineCodeRe = regexp.MustCompile("`([^`\n]+)`")
	tgBoldRe       = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	tgItalicAstRe  = regexp.MustCompile(`(^|[^*\w])\*([^*\n]+)\*([^*\w]|$)`)
	tgItalicUndRe  = regexp.MustCompile(`(^|[^_\w])_([^_\n]+)_([^_\w]|$)`)
	tgStrikeRe     = regexp.MustCompile(`~~([^~\n]+)~~`)
	tgLinkRe       = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	tgHeaderRe     = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*#*$`)
	tgListItemRe   = regexp.MustCompile(`(?m)^(\s*)[-*+]\s+`)
	tgHRRe         = regexp.MustCompile(`(?m)^\s*(?:-{3,}|\*{3,}|_{3,})\s*$`)
)

// renderMarkdownForTelegram converts standard Markdown into the HTML subset
// that Telegram accepts with parse_mode=HTML.
func renderMarkdownForTelegram(text string) string {
	type token struct{ html string }
	var tokens []token
	stash := func(html string) string {
		tokens = append(tokens, token{html: html})
		return fmt.Sprintf("\x00TGTOK%d\x00", len(tokens)-1)
	}

	// 1. Pull out code blocks first so their contents are not mangled.
	text = tgCodeBlockRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := tgCodeBlockRe.FindStringSubmatch(m)
		body := sub[2]
		return stash("<pre><code>" + escapeHTML(body) + "</code></pre>")
	})
	text = tgInlineCodeRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := tgInlineCodeRe.FindStringSubmatch(m)
		return stash("<code>" + escapeHTML(sub[1]) + "</code>")
	})

	// 2. Escape raw HTML in the remaining body so user/agent text cannot
	//    inject arbitrary tags.
	text = escapeHTML(text)

	// 3. Block-level transforms.
	text = tgHRRe.ReplaceAllString(text, "──────────")
	text = tgHeaderRe.ReplaceAllString(text, "<b>$1</b>")
	text = tgListItemRe.ReplaceAllString(text, "$1• ")

	// 4. Inline transforms — bold first so it doesn't get eaten by italic.
	text = tgBoldRe.ReplaceAllString(text, "<b>$1</b>")
	text = tgStrikeRe.ReplaceAllString(text, "<s>$1</s>")
	text = tgItalicAstRe.ReplaceAllString(text, "$1<i>$2</i>$3")
	text = tgItalicUndRe.ReplaceAllString(text, "$1<i>$2</i>$3")
	text = tgLinkRe.ReplaceAllString(text, `<a href="$2">$1</a>`)

	// 5. Restore protected code placeholders.
	for i, tok := range tokens {
		text = strings.Replace(text, fmt.Sprintf("\x00TGTOK%d\x00", i), tok.html, 1)
	}
	return text
}

func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
