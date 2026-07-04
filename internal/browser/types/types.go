package types

import (
	"context"
	"time"
)

type RenderResult struct {
	HTML                string
	URL                 string
	Title               string
	ContentType         string
	CloudflareClearance string
}

type ErrNotHTML struct {
	URL         string
	ContentType string
}

func (e *ErrNotHTML) Error() string {
	return "url " + e.URL + " returned " + e.ContentType + ", not HTML"
}

type BrowserBackend interface {
	Render(ctx context.Context, url string) (*RenderResult, error)
	SetSettle(d time.Duration)
	StealthEnabled() bool
	EnableStealth() error
	Close()
}