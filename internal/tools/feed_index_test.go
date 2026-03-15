package tools

import "testing"

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello world", "hello world"},
		{"simple tags", "<p>hello</p>", "hello"},
		{"nested tags", "<div><p>hello</p></div>", "hello"},
		{"attributes", `<a href="http://example.com">link</a>`, "link"},
		{"br and hr", "one<br>two<hr>three", "one two three"},
		{"amp entity", "one &amp; two", "one & two"},
		{"lt gt entities", "&lt;tag&gt;", "<tag>"},
		{"nbsp", "one&nbsp;two", "one two"},
		{"numeric entity", "&#39;quoted&#39;", "'quoted'"},
		{"hex entity", "&#x27;hex&#x27;", "'hex'"},
		{"collapsed whitespace", "one  \n\t  two", "one two"},
		{"empty", "", ""},
		{"script tag", "<script>alert('xss')</script>visible", "alert('xss') visible"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTMLTags(tt.input)
			if got != tt.want {
				t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
