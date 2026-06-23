package markdown

import (
	"fmt"
	"strings"
	"testing"
)

// sampleHTML builds a realistic article page: a chrome-heavy shell (head with
// meta and stylesheet links, a navigation bar, a sidebar of related links, a
// comment-widget footer) wrapped around an article of paras paragraphs with
// inline links, a couple of headings, a list, and a table. It is what a typical
// content page looks like to the converter: most bytes are boilerplate the
// extractor must shed before rendering.
func sampleHTML(paras int) []byte {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<title>A Long Form Article About Something</title>`)
	b.WriteString(`<meta name="description" content="An example article used for benchmarking.">`)
	b.WriteString(`<meta property="og:site_name" content="Example Times">`)
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&b, `<link rel="stylesheet" href="/assets/style-%d.css">`, i)
	}
	b.WriteString(`</head><body>`)
	b.WriteString(`<header><nav>`)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, `<a href="/section/%d">Section %d</a>`, i, i)
	}
	b.WriteString(`</nav></header>`)
	b.WriteString(`<aside class="sidebar"><h3>Related</h3><ul>`)
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&b, `<li><a href="/related/%d">Related story number %d about a topic</a></li>`, i, i)
	}
	b.WriteString(`</ul></aside>`)

	b.WriteString(`<article><h1>A Long Form Article About Something</h1>`)
	b.WriteString(`<p>By a Staff Writer, published recently for the benefit of readers everywhere.</p>`)
	for i := 0; i < paras; i++ {
		if i%5 == 0 {
			fmt.Fprintf(&b, `<h2>Heading for section %d</h2>`, i/5)
		}
		fmt.Fprintf(&b, `<p>Paragraph %d carries a fair amount of text with an inline `+
			`<a href="/cited/%d">citation link</a> and enough words that the extractor `+
			`treats the surrounding container as the real article body rather than as `+
			`navigation chrome to be discarded before conversion to Markdown.</p>`, i, i)
	}
	b.WriteString(`<ul><li>First bullet point of a short list</li>`)
	b.WriteString(`<li>Second bullet with a <a href="/x">link</a></li>`)
	b.WriteString(`<li>Third bullet to round it out</li></ul>`)
	b.WriteString(`<table><tr><th>Year</th><th>Value</th></tr>`)
	b.WriteString(`<tr><td>2024</td><td>10</td></tr><tr><td>2025</td><td>20</td></tr></table>`)
	b.WriteString(`</article>`)

	b.WriteString(`<footer><div class="comments">`)
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&b, `<div class="comment"><a href="/user/%d">user%d</a> said something brief.</div>`, i, i)
	}
	b.WriteString(`</div><p>Copyright boilerplate that should not survive extraction.</p></footer>`)
	b.WriteString(`</body></html>`)
	return []byte(b.String())
}

func BenchmarkConvert(b *testing.B) {
	for _, paras := range []int{10, 40, 120} {
		body := sampleHTML(paras)
		md, ok := Convert(body, "text/html; charset=utf-8", "https://example.com/article")
		if !ok {
			b.Fatalf("sample with %d paragraphs did not convert", paras)
		}
		b.Run(fmt.Sprintf("paras=%d", paras), func(b *testing.B) {
			b.ReportMetric(float64(len(body)), "html_bytes")
			b.ReportMetric(float64(len(md)), "md_bytes")
			b.ReportMetric(float64(len(md))/float64(len(body)), "md/html")
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, ok := Convert(body, "text/html; charset=utf-8", "https://example.com/article"); !ok {
					b.Fatal("convert failed")
				}
			}
		})
	}
}
