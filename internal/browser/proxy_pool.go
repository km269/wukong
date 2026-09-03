package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	tls "github.com/refraction-networking/utls"
)

type ProxyEntry struct {
	URL         string
	Healthy     bool
	LastUsed    time.Time
	FailCount   int
	CfClearance string
	Latency     time.Duration
}

type SmartProxyPool struct {
	proxies           []*ProxyEntry
	currentIndex      int
	maxFailures       int
	healthCheckURL    string
	mu                sync.Mutex
	healthCheckTicker *time.Ticker
	ctx               context.Context
	cancel            context.CancelFunc
}

func NewSmartProxyPool(proxyURLs []string, healthCheckInterval time.Duration) *SmartProxyPool {
	ctx, cancel := context.WithCancel(context.Background())

	proxies := make([]*ProxyEntry, 0, len(proxyURLs))
	for _, u := range proxyURLs {
		proxies = append(proxies, &ProxyEntry{
			URL:     u,
			Healthy: true,
		})
	}

	pool := &SmartProxyPool{
		proxies:        proxies,
		currentIndex:   0,
		maxFailures:    3,
		healthCheckURL: "https://www.google.com/favicon.ico",
		ctx:            ctx,
		cancel:         cancel,
	}

	if healthCheckInterval > 0 && len(proxies) > 0 {
		pool.healthCheckTicker = time.NewTicker(healthCheckInterval)
		go pool.healthCheckLoop()
	}

	return pool
}

func (p *SmartProxyPool) healthCheckLoop() {
	for {
		select {
		case <-p.healthCheckTicker.C:
			p.checkAllProxies()
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *SmartProxyPool) checkAllProxies() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, proxy := range p.proxies {
		if p.checkProxyHealth(proxy) {
			proxy.Healthy = true
			proxy.FailCount = 0
		} else {
			proxy.FailCount++
			if proxy.FailCount >= p.maxFailures {
				proxy.Healthy = false
			}
		}
	}
}

func (p *SmartProxyPool) checkProxyHealth(proxy *ProxyEntry) bool {
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		return false
	}

	conn, err := net.DialTimeout("tcp", proxyURL.Host, 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (p *SmartProxyPool) GetProxy() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.proxies) == 0 {
		return ""
	}

	startIndex := p.currentIndex
	for i := 0; i < len(p.proxies); i++ {
		idx := (startIndex + i) % len(p.proxies)
		if p.proxies[idx].Healthy {
			p.currentIndex = (idx + 1) % len(p.proxies)
			p.proxies[idx].LastUsed = time.Now()
			return p.proxies[idx].URL
		}
	}

	p.currentIndex = (startIndex + 1) % len(p.proxies)
	return p.proxies[startIndex].URL
}

func (p *SmartProxyPool) GetProxyWithCfClearance() (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.proxies) == 0 {
		return "", ""
	}

	startIndex := p.currentIndex
	for i := 0; i < len(p.proxies); i++ {
		idx := (startIndex + i) % len(p.proxies)
		if p.proxies[idx].Healthy && p.proxies[idx].CfClearance != "" {
			p.currentIndex = (idx + 1) % len(p.proxies)
			p.proxies[idx].LastUsed = time.Now()
			return p.proxies[idx].URL, p.proxies[idx].CfClearance
		}
	}

	for i := 0; i < len(p.proxies); i++ {
		idx := (startIndex + i) % len(p.proxies)
		if p.proxies[idx].Healthy {
			p.currentIndex = (idx + 1) % len(p.proxies)
			p.proxies[idx].LastUsed = time.Now()
			return p.proxies[idx].URL, ""
		}
	}

	p.currentIndex = (startIndex + 1) % len(p.proxies)
	return p.proxies[startIndex].URL, ""
}

func (p *SmartProxyPool) SetCfClearance(proxyURL, cfClearance string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, proxy := range p.proxies {
		if proxy.URL == proxyURL {
			proxy.CfClearance = cfClearance
			break
		}
	}
}

func (p *SmartProxyPool) ReportFailure(proxyURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, proxy := range p.proxies {
		if proxy.URL == proxyURL {
			proxy.FailCount++
			if proxy.FailCount >= p.maxFailures {
				proxy.Healthy = false
				proxy.CfClearance = ""
			}
			break
		}
	}
}

func (p *SmartProxyPool) GetHealthyCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	for _, proxy := range p.proxies {
		if proxy.Healthy {
			count++
		}
	}
	return count
}

func (p *SmartProxyPool) RotatedIndex() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.currentIndex
}

func (p *SmartProxyPool) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.proxies)
}

func (p *SmartProxyPool) Close() {
	if p.healthCheckTicker != nil {
		p.healthCheckTicker.Stop()
	}
	p.cancel()
}

func (p *SmartProxyPool) CreateHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				proxyURL := p.GetProxy()
				if proxyURL == "" {
					return net.Dial(network, addr)
				}

				proxy, err := url.Parse(proxyURL)
				if err != nil {
					return nil, fmt.Errorf("invalid proxy URL: %w", err)
				}

				conn, err := net.Dial(network, proxy.Host)
				if err != nil {
					p.ReportFailure(proxyURL)
					return nil, err
				}

				if proxy.User != nil {
					if err := negotiateProxyAuth(conn, proxy); err != nil {
						conn.Close()
						p.ReportFailure(proxyURL)
						return nil, err
					}
				}

				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					host = addr
				}

				uconn := tls.UClient(conn, &tls.Config{
					ServerName: host,
				}, tls.HelloChrome_Auto)
				if err := uconn.Handshake(); err != nil {
					conn.Close()
					p.ReportFailure(proxyURL)
					return nil, err
				}

				return uconn, nil
			},
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

func negotiateProxyAuth(conn net.Conn, proxy *url.URL) error {
	auth := ""
	if proxy.User != nil {
		if password, set := proxy.User.Password(); set {
			auth = proxy.User.Username() + ":" + password
		} else {
			auth = proxy.User.Username()
		}
	}

	if auth != "" {
		req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n",
			proxy.Host, proxy.Host, base64.StdEncoding.EncodeToString([]byte(auth)))
		if _, err := conn.Write([]byte(req)); err != nil {
			return err
		}

		var resp [1024]byte
		n, err := conn.Read(resp[:])
		if err != nil {
			return err
		}

		if !strings.Contains(string(resp[:n]), "200") {
			return fmt.Errorf("proxy authentication failed")
		}
	}

	return nil
}

var _ net.Conn = (*tls.UConn)(nil)
