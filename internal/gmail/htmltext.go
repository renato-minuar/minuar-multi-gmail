package gmail

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	skipTags  = map[string]bool{"script": true, "style": true, "head": true, "title": true, "noscript": true}
	blockTags = map[string]bool{
		"p": true, "div": true, "li": true, "tr": true, "h1": true, "h2": true, "h3": true, "h4": true,
		"h5": true, "h6": true, "blockquote": true, "pre": true, "table": true, "ul": true, "ol": true,
		"hr": true, "section": true, "article": true, "header": true, "footer": true, "address": true,
	}
	spaceRun   = regexp.MustCompile(`\s+`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// htmlToText renders HTML as plain text: skipped script/style/head,
// block elements and <br> become line breaks, entities decoded, runs of
// blank lines collapsed to one.
func htmlToText(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		tok := z.Token()
		name := strings.ToLower(tok.Data)
		switch tt {
		case html.StartTagToken:
			if skipTags[name] {
				skip++
			} else if name == "br" || blockTags[name] {
				b.WriteString("\n")
			}
		case html.SelfClosingTagToken:
			if name == "br" || blockTags[name] {
				b.WriteString("\n")
			}
		case html.EndTagToken:
			if skipTags[name] {
				if skip > 0 {
					skip--
				}
			} else if blockTags[name] {
				b.WriteString("\n")
			}
		case html.TextToken:
			if skip == 0 {
				b.WriteString(spaceRun.ReplaceAllString(tok.Data, " "))
			}
		}
	}
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	out := strings.Join(lines, "\n")
	out = blankLines.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}
