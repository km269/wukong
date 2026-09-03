package rodbackend

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
	"github.com/km269/wukong/pkg/logutil"
)

// cdpAuditEnabled reports whether CDP command auditing is on
// (WUKONG_CDP_AUDIT set to any non-empty value). The audit prints
// every protocol command with duration at debug level — the command
// *sequence* is what "driving-style" fingerprint audits need, so
// payloads are deliberately not logged (huge and may carry secrets).
func cdpAuditEnabled() bool {
	return os.Getenv("WUKONG_CDP_AUDIT") != ""
}

// auditClient wraps a CDP client and logs each Call. It exists to
// make the invisible visible: before tuning the command surface you
// must be able to see it. Typical workflow:
//
//	WUKONG_CDP_AUDIT=1 wukong ... 2>&1 | grep '\[cdp\]'
//
// then prune the domain enables / command bursts that show up.
type auditClient struct {
	inner rod.CDPClient
}

// Event passes the inner event channel through unchanged. Events are
// not audited — only the commands we send define our driving style;
// incoming events depend on the page.
func (a *auditClient) Event() <-chan *cdp.Event {
	return a.inner.Event()
}

// Call logs method name, duration and error around the inner call.
func (a *auditClient) Call(ctx context.Context, sessionID, method string, params interface{}) ([]byte, error) {
	start := time.Now()
	res, err := a.inner.Call(ctx, sessionID, method, params)
	logutil.Debug("[cdp]",
		slog.String("method", method),
		slog.String("session", sessionID),
		slog.Duration("took", time.Since(start)),
		slog.Bool("error", err != nil))
	return res, err
}

// connectCDP opens the CDP connection for controlURL and returns a rod
// client, wrapped for auditing when enabled. Connecting manually (and
// passing rod.New().Client) instead of rod.New().ControlURL() is the
// only injection point rod offers — Client and ControlURL are mutually
// exclusive by design.
func connectCDP(ctx context.Context, controlURL string) (rod.CDPClient, error) {
	inner, err := cdp.StartWithURL(ctx, controlURL, nil)
	if err != nil {
		return nil, err
	}
	if cdpAuditEnabled() {
		return &auditClient{inner: inner}, nil
	}
	return inner, nil
}
