package prober

import (
	"context"
	"net"
	"net/http"
	"time"

	tls "github.com/refraction-networking/utls"
)

type HTTPClient struct {
	*http.Client
}

func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		Client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					conn, err := net.Dial(network, addr)
					if err != nil {
						return nil, err
					}

					host, _, err := net.SplitHostPort(addr)
					if err != nil {
						host = addr
					}

					uconn := tls.UClient(conn, &tls.Config{
						ServerName: host,
					}, tls.HelloChrome_Auto)
					err = uconn.Handshake()
					if err != nil {
						conn.Close()
						return nil, err
					}

					return uconn, nil
				},
				MaxIdleConns:        10,
				IdleConnTimeout:     30 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

func (c *HTTPClient) GetWithHeaders(url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return c.Do(req)
}
