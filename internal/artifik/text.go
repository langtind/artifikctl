package artifik

import (
	"html"
	"regexp"
	"strings"
)

var (
	breaks = regexp.MustCompile(`(?i)<\s*(br\s*/?|/p|/li|/h[1-6]|/div|/tr)\s*>`)
	items  = regexp.MustCompile(`(?i)<\s*li[^>]*>`)
	tags   = regexp.MustCompile(`<[^>]*>`)
	blanks = regexp.MustCompile(`\n{3,}`)
)

// text turns the HTML Artifik writes descriptions in into plain text, with
// a line for each paragraph and "- " before each list item.
func text(s string) string {
	if !strings.Contains(s, "<") {
		return strings.TrimSpace(html.UnescapeString(s))
	}
	s = breaks.ReplaceAllString(s, "\n")
	s = items.ReplaceAllString(s, "- ")
	s = tags.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.TrimSpace(blanks.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
