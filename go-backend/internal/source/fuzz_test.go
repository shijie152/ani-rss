package source

import "testing"

func FuzzParseMikanHTML(f *testing.F) {
	f.Add([]byte(`<div class="date-select"><div class="date-text">2026 秋</div><a data-year="2026" data-season="秋">秋</a></div><div class="sk-bangumi"><h3>星期一</h3><ul class="an-ul"><li><span data-src="/cover.jpg"></span><a href="/Home/Bangumi/42">Demo</a></li></ul></div>`))
	f.Add([]byte(`<ul class="an-ul"><li><a href="/Home/Search">not an anime</a></li></ul>`))
	f.Fuzz(func(t *testing.T, body []byte) {
		result := New(Options{}).parseMikanList("https://mikan.test/Home/Search", body)
		if result == nil {
			t.Fatal("Mikan parser returned nil")
		}
		if _, ok := result["weeks"]; !ok {
			t.Fatal("Mikan parser omitted weeks")
		}
	})
}
