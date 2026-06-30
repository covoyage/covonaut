package tools

import (
	"strings"
	"testing"
)

func TestParseBingRSSResults(t *testing.T) {
	rss := `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0">
  <channel>
    <item>
      <title>上海三岔港楔形绿地首开区进展</title>
      <link>https://example.com/project</link>
      <description><![CDATA[<b>浦东</b> 项目进展公告]]></description>
    </item>
  </channel>
</rss>`

	results, err := parseBingRSSResults(strings.NewReader(rss), 10)
	if err != nil {
		t.Fatalf("parse RSS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "上海三岔港楔形绿地首开区进展" {
		t.Fatalf("unexpected title: %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/project" {
		t.Fatalf("unexpected URL: %q", results[0].URL)
	}
	if results[0].Snippet != "浦东 项目进展公告" {
		t.Fatalf("unexpected snippet: %q", results[0].Snippet)
	}
}
