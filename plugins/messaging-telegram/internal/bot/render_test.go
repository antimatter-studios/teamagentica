package bot

import "testing"

func TestRenderMarkdown_Telegram(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bold", "**hi**", "<b>hi</b>"},
		{"italic-ast", "this is *italic* text", "this is <i>italic</i> text"},
		{"italic-und", "this is _italic_ text", "this is <i>italic</i> text"},
		{"inline-code", "use `foo()` to call", "use <code>foo()</code> to call"},
		{"link", "see [home](https://x.io)", `see <a href="https://x.io">home</a>`},
		{"header", "## Section A", "<b>Section A</b>"},
		{"escapes-raw-html", "<script>", "&lt;script&gt;"},
		{"list-dash", "- item one\n- item two", "• item one\n• item two"},
		{"list-star", "* item one\n* item two", "• item one\n• item two"},
		{"hr", "---", "──────────"},
		{"strike", "~~old~~", "<s>old</s>"},
		{
			"code-block-preserves-content",
			"```go\nx := 1 < 2\n```",
			"<pre><code>x := 1 &lt; 2\n</code></pre>",
		},
		{
			"mixed",
			"# Title\n- alpha\n- **beta** with `code`",
			"<b>Title</b>\n• alpha\n• <b>beta</b> with <code>code</code>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderMarkdownForTelegram(tc.in)
			if got != tc.want {
				t.Errorf("\n  in:   %q\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}
