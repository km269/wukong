package wecom

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"
)

// WeCom access_token endpoints.
const (
	wecomTokenURL = "https://qyapi.weixin.qq.com/cgi-bin/gettoken"
)

// tokenRefreshAhead is how early to refresh the token before expiry.
const tokenRefreshAhead = 5 * time.Minute

// WeComTokenManager caches and auto-refreshes the WeCom access_token.
//
// WeCom access_tokens are valid for 7200 seconds. The manager fetches
// a new token before the current one expires, ensuring continuous API
// access for streaming and proactive reply operations.
type WeComTokenManager struct {
	mu      sync.RWMutex
	corpID  string
	secret  string
	token   string
	expires time.Time
	client  *http.Client
}

// NewWeComTokenManager creates a token manager from channel config.
// If corpid or secret is empty, the manager operates in no-op mode
// (GetAccessToken returns an empty token).
func NewWeComTokenManager(
	cfg *config.WeComChannelConfig,
) *WeComTokenManager {
	return &WeComTokenManager{
		corpID: cfg.CorpID,
		secret: cfg.Secret,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetAccessToken returns a valid access_token. If the cached token is
// expired or about to expire, it fetches a new one automatically.
//
// Returns an empty string and no error when corpid or secret is not
// configured (the caller should handle gracefully).
func (tm *WeComTokenManager) GetAccessToken() (string, error) {
	if tm.corpID == "" || tm.secret == "" {
		return "", nil
	}

	tm.mu.RLock()
	hasToken := tm.token != ""
	valid := time.Now().Add(tokenRefreshAhead).Before(tm.expires)
	tm.mu.RUnlock()

	if hasToken && valid {
		tm.mu.RLock()
		tok := tm.token
		tm.mu.RUnlock()
		return tok, nil
	}

	return tm.refreshToken()
}

// refreshToken fetches a new access_token from the WeCom API.
func (tm *WeComTokenManager) refreshToken() (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Double-checked locking: another goroutine may have refreshed.
	if tm.token != "" &&
		time.Now().Add(tokenRefreshAhead).Before(tm.expires) {
		return tm.token, nil
	}

	url := fmt.Sprintf(
		"%s?corpid=%s&corpsecret=%s",
		wecomTokenURL, tm.corpID, tm.secret,
	)

	resp, err := tm.client.Get(url)
	if err != nil {
		return "", fmt.Errorf(
			"wecom: get token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf(
			"wecom: read token response: %w", err)
	}

	var tokenResp struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf(
			"wecom: parse token response: %w", err)
	}

	if tokenResp.ErrCode != 0 {
		return "", fmt.Errorf(
			"wecom: get token error: code=%d, msg=%s",
			tokenResp.ErrCode, tokenResp.ErrMsg)
	}

	tm.token = tokenResp.AccessToken
	tm.expires = time.Now().Add(
		time.Duration(tokenResp.ExpiresIn) * time.Second,
	)

	return tm.token, nil
}
