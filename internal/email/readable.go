package email

import (
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// ReadableBody renders MIME HTML as text for the agent, without fetching links
// or resources. The original Message.Body remains untouched for the mail UI.
func (m Message) ReadableBody() string {
	if !m.BodyIsHTML {
		return m.Body
	}
	root, err := html.Parse(strings.NewReader(m.Body))
	if err != nil {
		return ""
	}
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			out.WriteString(strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) {
					return ' '
				}
				return r
			}, n.Data))
			return
		}
		if n.Type != html.ElementNode && n.Type != html.DocumentNode {
			return
		}
		tag := n.Data
		switch tag {
		case "head", "style", "script", "template", "svg", "noscript":
			return
		}
		attrs := map[string]string{}
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		if _, hidden := attrs["hidden"]; hidden {
			return
		}
		style := strings.ToLower(strings.Join(strings.Fields(attrs["style"]), ""))
		if strings.EqualFold(attrs["aria-hidden"], "true") || strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") {
			return
		}
		block := false
		switch tag {
		case "p", "div", "section", "article", "header", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "tr", "blockquote", "pre":
			block = true
		}
		if block || tag == "br" || tag == "hr" {
			out.WriteByte('\n')
		}
		if tag == "li" {
			out.WriteString("- ")
		}
		if tag == "img" && strings.TrimSpace(attrs["alt"]) != "" {
			out.WriteString(" " + attrs["alt"] + " ")
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if tag == "a" {
			href := strings.TrimSpace(attrs["href"])
			if u, err := url.Parse(href); err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "mailto") {
				out.WriteString(" (" + href + ")")
			}
		}
		if tag == "td" || tag == "th" {
			out.WriteByte(' ')
		}
		if block {
			out.WriteByte('\n')
		}
	}
	walk(root)
	var lines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
