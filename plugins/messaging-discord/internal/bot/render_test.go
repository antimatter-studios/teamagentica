package bot

import "testing"

func TestRenderMarkdown_Discord(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"asterisk-list", "* one\n* two", "- one\n- two"},
		{"indented-asterisk-list", "  * nested", "  - nested"},
		{"bold-untouched", "**bold** text", "**bold** text"},
		{"italic-untouched", "this *italic* word", "this *italic* word"},
		{"dash-list-untouched", "- one\n- two", "- one\n- two"},
		{"numbered-list-untouched", "1. one\n2. two", "1. one\n2. two"},
		{"code-block-untouched", "```go\nfmt.Println()\n```", "```go\nfmt.Println()\n```"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderMarkdownForDiscord(tc.in)
			if got != tc.want {
				t.Errorf("\n  in:   %q\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}
