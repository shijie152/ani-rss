package rss

import "testing"

func FuzzParseRSS(f *testing.F) {
	f.Add([]byte(`<rss><channel><item><title>[Group] Demo 01</title><link>magnet:?xt=urn:btih:ABC</link><pubDate>Mon, 01 Jan 2026 00:00:00 GMT</pubDate></item></channel></rss>`))
	f.Add([]byte(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><entry><title>Demo</title><id>demo</id></entry></feed>`))
	f.Fuzz(func(t *testing.T, body []byte) {
		resources, err := Parse(body, "Group", "https://example.test/feed.xml")
		if err == nil && resources == nil {
			t.Fatal("successful RSS parse returned nil resources")
		}
	})
}
