package clone

import (
	"net/url"
	"regexp"
	"testing"
)

func BenchmarkPathPaginationRegex(b *testing.B) {
	pageRe := regexp.MustCompile(`^(.+)/page/(\d+)/?$`)
	paths := []string{
		"/biographies-list/page/2/",
		"/biographies-list/page/3/",
		"/articles/page/10",
		"/news/page/1/",
		"/blog/post/page/5/",
		"/normal-page",
		"/about",
		"/contact",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range paths {
			pageRe.FindStringSubmatch(p)
		}
	}
}

func BenchmarkPathPaginationRegex_LargeSet(b *testing.B) {
	pageRe := regexp.MustCompile(`^(.+)/page/(\d+)/?$`)
	paths := make([]string, 1000)
	for i := range paths {
		if i%3 == 0 {
			paths[i] = "/list/page/" + itoa(i%100+1) + "/"
		} else {
			paths[i] = "/normal/path/" + itoa(i) + ".html"
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range paths {
			pageRe.FindStringSubmatch(p)
		}
	}
}

func BenchmarkURLParse(b *testing.B) {
	urls := []string{
		"https://www.state.gov/biographies-list/page/2/",
		"https://www.state.gov/biographies-list/?page=3",
		"https://www.state.gov/department-press-briefings/?offset=25&limit=25",
		"https://www.state.gov/articles/?cursor=abc123&limit=20",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, raw := range urls {
			url.Parse(raw)
		}
	}
}

func BenchmarkURLParse_LargeSet(b *testing.B) {
	urls := make([]string, 1000)
	for i := range urls {
		urls[i] = "https://www.state.gov/list/page/" + itoa(i%100+1) + "/?sort=date"
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, raw := range urls {
			url.Parse(raw)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
