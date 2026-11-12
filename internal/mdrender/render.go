package mdrender

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

var orderedListItem = regexp.MustCompile(`^([0-9]+)\.\s+(.*)$`)

// Render converts the small Markdown subset used by benchmark reports into safe HTML.
func Render(markdown []byte) []byte {
	text := strings.ReplaceAll(string(markdown), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	var body strings.Builder
	index := 0
	for index < len(lines) {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			index++
			continue
		}

		if marker, ok := fenceMarker(trimmed); ok {
			index++
			content := []string{}
			for index < len(lines) {
				if strings.HasPrefix(strings.TrimSpace(lines[index]), "```") {
					index++
					break
				}
				content = append(content, lines[index])
				index++
			}
			class := ""
			if marker != "" {
				class = fmt.Sprintf(` class="language-%s"`, sanitizeLanguage(marker))
			}
			body.WriteString("<pre><code" + class + ">" + html.EscapeString(strings.Join(content, "\n")) + "</code></pre>\n")
			continue
		}

		if level, content, ok := heading(line); ok {
			body.WriteString(fmt.Sprintf("<h%d>%s</h%d>\n", level, renderInline(content), level))
			index++
			continue
		}

		if strings.HasPrefix(trimmed, "|") && index+1 < len(lines) && isTableSeparator(strings.TrimSpace(lines[index+1])) {
			header, ok := splitTableRow(line)
			if ok {
				index += 2
				rows := [][]string{}
				for index < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[index]), "|") {
					row, rowOK := splitTableRow(lines[index])
					if rowOK {
						rows = append(rows, row)
					}
					index++
				}
				body.WriteString(renderTable(header, rows))
				continue
			}
		}

		if isHorizontalRule(trimmed) {
			body.WriteString("<hr>\n")
			index++
			continue
		}

		if kind, _, ok := listMarker(line); ok {
			tag := "ul"
			if kind == "ordered" {
				tag = "ol"
			}
			body.WriteString("<" + tag + ">\n")
			for index < len(lines) {
				currentKind, currentContent, currentOK := listMarker(lines[index])
				if !currentOK || currentKind != kind {
					break
				}
				body.WriteString("<li>" + renderInline(currentContent) + "</li>\n")
				index++
			}
			body.WriteString("</" + tag + ">\n")
			continue
		}

		if strings.HasPrefix(trimmed, "> ") {
			body.WriteString("<blockquote><p>" + renderInline(strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))) + "</p></blockquote>\n")
			index++
			continue
		}

		paragraph := []string{strings.TrimSpace(line)}
		index++
		for index < len(lines) {
			next := strings.TrimSpace(lines[index])
			if next == "" || startsBlock(next) {
				break
			}
			paragraph = append(paragraph, next)
			index++
		}
		body.WriteString("<p>" + renderInline(strings.Join(paragraph, " ")) + "</p>\n")
	}

	title := firstHeading(lines)
	document := fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root { color-scheme: light dark; --border: #d7dce2; --muted: #667085; --code: #f2f4f7; }
* { box-sizing: border-box; }
body { margin: 0; background: #f6f7f9; color: #1d2939; font: 16px/1.65 -apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans SC", sans-serif; }
main { width: min(1180px, calc(100%% - 32px)); margin: 32px auto; padding: 32px 40px; background: #fff; border: 1px solid var(--border); border-radius: 12px; }
h1, h2, h3, h4 { line-height: 1.3; color: #101828; }
h1 { margin-top: 0; font-size: 2rem; }
h2 { margin-top: 2rem; padding-bottom: .35rem; border-bottom: 1px solid var(--border); }
p { margin: .8rem 0; }
a { color: #175cd3; }
code { padding: .15rem .35rem; background: var(--code); border-radius: 5px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
pre { overflow-x: auto; padding: 16px; background: #101828; color: #e4e7ec; border-radius: 8px; }
pre code { padding: 0; background: transparent; color: inherit; }
table { display: block; width: 100%%; overflow-x: auto; border-collapse: collapse; margin: 1rem 0; white-space: nowrap; }
th, td { padding: 9px 12px; border: 1px solid var(--border); text-align: left; vertical-align: top; }
th { background: #f2f4f7; font-weight: 650; }
tr:nth-child(even) td { background: #fafbfc; }
ul, ol { padding-left: 1.5rem; }
blockquote { margin: 1rem 0; padding: .2rem 1rem; border-left: 4px solid #98a2b3; color: var(--muted); }
hr { border: 0; border-top: 1px solid var(--border); }
@media (prefers-color-scheme: dark) {
  :root { --border: #344054; --muted: #98a2b3; --code: #1d2939; }
  body { background: #101828; color: #e4e7ec; }
  main { background: #1d2939; border-color: var(--border); }
  h1, h2, h3, h4 { color: #f9fafb; }
  th { background: #344054; }
  tr:nth-child(even) td { background: #273444; }
}
</style>
</head>
<body><main class="markdown">%s</main></body>
</html>
`, html.EscapeString(title), body.String())
	return []byte(document)
}

func fenceMarker(line string) (string, bool) {
	if !strings.HasPrefix(line, "```") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "```")), true
}

func heading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(line[level+1:]), true
}

func isTableSeparator(line string) bool {
	cells, ok := splitTableRow(line)
	if !ok || len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		normalized := strings.TrimSpace(strings.ReplaceAll(cell, ":", ""))
		if normalized == "" || strings.Trim(normalized, "-") != "" {
			return false
		}
	}
	return true
}

func splitTableRow(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
		return nil, false
	}
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	trimmed = strings.ReplaceAll(trimmed, "\\|", "\x00")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, len(parts))
	for index, part := range parts {
		cells[index] = strings.TrimSpace(strings.ReplaceAll(part, "\x00", "|"))
	}
	return cells, true
}

func renderTable(header []string, rows [][]string) string {
	var builder strings.Builder
	builder.WriteString("<div class=\"table-wrap\"><table><thead><tr>")
	for _, cell := range header {
		builder.WriteString("<th>" + renderInline(cell) + "</th>")
	}
	builder.WriteString("</tr></thead><tbody>")
	for _, row := range rows {
		builder.WriteString("<tr>")
		for _, cell := range row {
			builder.WriteString("<td>" + renderInline(cell) + "</td>")
		}
		builder.WriteString("</tr>")
	}
	builder.WriteString("</tbody></table></div>\n")
	return builder.String()
}

func isHorizontalRule(line string) bool {
	return line == "---" || line == "***" || line == "___"
}

func listMarker(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return "unordered", strings.TrimSpace(trimmed[2:]), true
	}
	if match := orderedListItem.FindStringSubmatch(trimmed); match != nil {
		return "ordered", match[2], true
	}
	return "", "", false
}

func startsBlock(line string) bool {
	if line == "" || strings.HasPrefix(line, "|") || strings.HasPrefix(line, "> ") || strings.HasPrefix(line, "```") {
		return true
	}
	if _, _, ok := heading(line); ok {
		return true
	}
	if _, _, ok := listMarker(line); ok {
		return true
	}
	return isHorizontalRule(line)
}

func renderInline(input string) string {
	var builder strings.Builder
	index := 0
	for index < len(input) {
		switch {
		case input[index] == '`':
			end := strings.IndexByte(input[index+1:], '`')
			if end < 0 {
				builder.WriteString(html.EscapeString(string(input[index])))
				index++
				continue
			}
			end += index + 1
			builder.WriteString("<code>" + html.EscapeString(input[index+1:end]) + "</code>")
			index = end + 1
		case strings.HasPrefix(input[index:], "**"):
			end := strings.Index(input[index+2:], "**")
			if end < 0 {
				builder.WriteString(html.EscapeString("**"))
				index += 2
				continue
			}
			end += index + 2
			builder.WriteString("<strong>" + renderInline(input[index+2:end]) + "</strong>")
			index = end + 2
		case input[index] == '[':
			closing := strings.Index(input[index+1:], "](")
			if closing < 0 {
				builder.WriteString(html.EscapeString("["))
				index++
				continue
			}
			closing += index + 1
			end := strings.Index(input[closing+2:], ")")
			if end < 0 {
				builder.WriteString(html.EscapeString("["))
				index++
				continue
			}
			end += closing + 2
			label := renderInline(input[index+1 : closing])
			href := safeURL(input[closing+2 : end])
			builder.WriteString(`<a href="` + html.EscapeString(href) + `">` + label + "</a>")
			index = end + 1
		default:
			next := index + 1
			for next < len(input) && !strings.ContainsAny(string(input[next]), "`*[") {
				next++
			}
			builder.WriteString(html.EscapeString(input[index:next]))
			index = next
		}
	}
	return builder.String()
}

func safeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil {
		return "#"
	}
	switch parsed.Scheme {
	case "", "http", "https", "mailto":
		return raw
	default:
		return "#"
	}
}

func sanitizeLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "text"
	}
	var builder strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			builder.WriteRune(character)
		}
	}
	if builder.Len() == 0 {
		return "text"
	}
	return builder.String()
}

func firstHeading(lines []string) string {
	for _, line := range lines {
		if _, content, ok := heading(line); ok {
			return content
		}
	}
	return "Report"
}
