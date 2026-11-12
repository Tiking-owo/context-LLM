package mdrender

import (
	"strings"
	"testing"
)

func TestRenderSupportsReportMarkdown(t *testing.T) {
	input := []byte(`# 测试报告

段落包含 **粗体**、` + "`代码`" + ` 和 [链接](https://example.com/report)。

- 第一项
- 第二项

| 档位 | 结果 |
|---:|---|
| 200000 | 通过 |

` + "```sh" + `
echo hello
` + "```" + `
`)
	output := string(Render(input))
	for _, expected := range []string{"<h1>测试报告</h1>", "<strong>粗体</strong>", "<code>代码</code>", `href="https://example.com/report"`, "<ul>", "<table>", "<pre><code class=\"language-sh\">"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing %q in rendered HTML:\n%s", expected, output)
		}
	}
}

func TestRenderEscapesUnsafeHTML(t *testing.T) {
	output := string(Render([]byte("# Report\n\n<script>alert('x')</script>")))
	if strings.Contains(output, "<script>alert") {
		t.Fatalf("unsafe HTML was not escaped:\n%s", output)
	}
	if !strings.Contains(output, "&lt;script&gt;") {
		t.Fatalf("expected escaped script text:\n%s", output)
	}
}
