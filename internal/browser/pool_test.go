package browser

import (
	"testing"
)

// TestOptions_DisableDownloads_Default 验证 Options.DisableDownloads 默认零值为 false.
func TestOptions_DisableDownloads_Default(t *testing.T) {
	var opts Options
	if opts.DisableDownloads {
		t.Error("DisableDownloads default should be false (zero value)")
	}
}

// TestRenderJob_Referer 验证 renderJob 结构体可以正确携带 referer 字段.
func TestRenderJob_Referer(t *testing.T) {
	job := renderJob{
		url:     "https://example.com/page",
		referer: "https://example.com/",
	}
	if job.referer != "https://example.com/" {
		t.Errorf("renderJob.referer = %q, want %q",
			job.referer, "https://example.com/")
	}
	if job.url != "https://example.com/page" {
		t.Errorf("renderJob.url = %q, want %q",
			job.url, "https://example.com/page")
	}
}

// TestRenderJob_EmptyReferer 验证空 referer 也被正确处理.
func TestRenderJob_EmptyReferer(t *testing.T) {
	job := renderJob{
		url: "https://example.com/seed",
	}
	if job.referer != "" {
		t.Errorf("renderJob.referer = %q, want empty", job.referer)
	}
}
