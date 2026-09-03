package browser

import (
	"testing"

	"github.com/km269/wukong/internal/browser/renderkit"
)

// TestOptions_DisableDownloads_Default 验证 Options.DisableDownloads 默认零值为 false.
func TestOptions_DisableDownloads_Default(t *testing.T) {
	var opts Options
	if opts.DisableDownloads {
		t.Error("DisableDownloads default should be false (zero value)")
	}
}

// TestRenderJob_Referer 验证共享 RenderJob（renderkit）可以正确携带 Referer 字段.
func TestRenderJob_Referer(t *testing.T) {
	job := renderkit.RenderJob{
		URL:     "https://example.com/page",
		Referer: "https://example.com/",
	}
	if job.Referer != "https://example.com/" {
		t.Errorf("RenderJob.Referer = %q, want %q",
			job.Referer, "https://example.com/")
	}
	if job.URL != "https://example.com/page" {
		t.Errorf("RenderJob.URL = %q, want %q",
			job.URL, "https://example.com/page")
	}
}

// TestRenderJob_EmptyReferer 验证空 referer 也被正确处理.
func TestRenderJob_EmptyReferer(t *testing.T) {
	job := renderkit.RenderJob{
		URL: "https://example.com/seed",
	}
	if job.Referer != "" {
		t.Errorf("RenderJob.Referer = %q, want empty", job.Referer)
	}
}
