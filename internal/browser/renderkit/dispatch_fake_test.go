package renderkit

import (
	"errors"

	"github.com/km269/wukong/internal/browser/types"
)

var errFake = errors.New("fake render error")

type fakeRender struct{ url string }

func (f fakeRender) result() *types.RenderResult {
	return &types.RenderResult{URL: f.url, HTML: "<html></html>"}
}
