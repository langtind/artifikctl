package artifik

import (
	"encoding/json"
	"strings"
)

// Artifik writes requirements and specifications as "smart docs": ProseMirror
// documents in JSON, with Artifik's own "request" node where the supplier is
// asked to answer or upload something. reference/README.md has the node
// types seen.

type smartDoc struct {
	DocJSON *node `json:"docJSON"`
}

type node struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Content []node `json:"content"`
	Attrs   struct {
		Type   string `json:"type"`
		Config struct {
			Required bool `json:"required"`
		} `json:"config"`
	} `json:"attrs"`
}

// docText writes a smart doc as plain text: a line for each paragraph and
// heading, "- " before each list item, and a line that says what a request
// asks the supplier for.
func docText(doc *smartDoc) string {
	if doc == nil || doc.DocJSON == nil {
		return ""
	}
	var lines []string
	var block func(n node, prefix string)
	block = func(n node, prefix string) {
		switch n.Type {
		case "bulletList", "orderedList":
			for _, item := range n.Content {
				block(item, prefix)
			}
		case "listItem":
			first := true
			for _, c := range n.Content {
				p := prefix + "  "
				if first {
					p, first = prefix+"- ", false
				}
				block(c, p)
			}
		case "request":
			what := map[string]string{"files": "upload files", "text": "answer in text", "task": "a task"}[n.Attrs.Type]
			if what == "" {
				what = "an answer"
			}
			if n.Attrs.Config.Required {
				what += ", required"
			}
			line := strings.TrimSpace(inline(n.Content))
			lines = append(lines, strings.TrimSpace(prefix+"[Supplier to give: "+what+"] "+line))
		default:
			if hasBlocks(n) {
				for _, c := range n.Content {
					block(c, prefix)
				}
				return
			}
			if line := strings.TrimSpace(inline(n.Content) + n.Text); line != "" {
				lines = append(lines, prefix+line)
			}
		}
	}
	block(*doc.DocJSON, "")
	return strings.Join(lines, "\n")
}

// hasBlocks says whether a node holds paragraphs or lists rather than text.
func hasBlocks(n node) bool {
	for _, c := range n.Content {
		if c.Type != "text" && c.Type != "hardBreak" {
			return true
		}
	}
	return false
}

// inline joins the text of a paragraph, with a space for a line break.
func inline(content []node) string {
	var b strings.Builder
	for _, c := range content {
		switch c.Type {
		case "text":
			b.WriteString(c.Text)
		case "hardBreak":
			b.WriteString(" ")
		default:
			b.WriteString(inline(c.Content))
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// rawDoc decodes a smart doc that may be missing.
func rawDoc(raw json.RawMessage) *smartDoc {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var doc smartDoc
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	return &doc
}
