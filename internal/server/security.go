package server

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/km269/wukong/internal/config"
)

// The server security configuration types (ServerTLSConfig,
// ServerAuthConfig, ServerRateLimitConfig, ServerSecurityConfig) live
// in internal/config/types_server.go — the root WukongConfig embeds
// them, so keeping the definitions here made internal/config depend
// on internal/server. Type aliases keep every existing reference in
// this package (and in external call sites) compiling unchanged.

type ServerTLSConfig = config.ServerTLSConfig

type ServerAuthConfig = config.ServerAuthConfig

type ServerRateLimitConfig = config.ServerRateLimitConfig

type ServerSecurityConfig = config.ServerSecurityConfig

func BuildTLSConfig(cfg ServerTLSConfig) (*tls.Config, error) {
	if !cfg.IsEnabled() {
		return nil, nil
	}

	certPEM, err := os.ReadFile(cfg.CertFile)
	if err != nil {
		return nil, fmt.Errorf("read cert file: %w", err)
	}

	keyPEM, err := os.ReadFile(cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load x509 key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
	}

	if cfg.CACertFile != "" {
		caPEM, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("append CA cert to pool")
		}
		tlsConfig.ClientCAs = caCertPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsConfig, nil
}

func AuthMiddleware(cfg ServerAuthConfig) func(http.Handler) http.Handler {
	if cfg.Type == "" {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	switch cfg.Type {
	case "api_key":
		return apiKeyMiddleware(cfg.APIKey)
	case "jwt":
		return jwtMiddleware(cfg.JWTSecret)
	default:
		return func(next http.Handler) http.Handler {
			return next
		}
	}
}

func apiKeyMiddleware(expectedKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				apiKey = r.URL.Query().Get("api_key")
			}
			// Constant-time comparison prevents timing side-channel
			// attacks that could leak the expected key byte-by-byte.
			if subtle.ConstantTimeCompare([]byte(apiKey), []byte(expectedKey)) != 1 {
				http.Error(w, `{"error":"unauthorized","message":"invalid API key"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func jwtMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"error":"unauthorized","message":"missing or invalid Authorization header"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenStr == "" {
				http.Error(w, `{"error":"unauthorized","message":"empty token"}`, http.StatusUnauthorized)
				return
			}
			if err := validateJWTToken(tokenStr, secret); err != nil {
				http.Error(w, `{"error":"unauthorized","message":"invalid token"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func validateJWTToken(tokenStr, secret string) error {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return fmt.Errorf("parse JWT: %w", err)
	}
	if !token.Valid {
		return fmt.Errorf("invalid JWT token")
	}
	return nil
}

type TokenBucket struct {
	tokens   map[string]*bucketInfo
	rate     float64
	capacity float64
	window   time.Duration
	mu       sync.Mutex
}

type bucketInfo struct {
	tokens   float64
	lastTime time.Time
}

func NewTokenBucket(maxPerMinute int, window time.Duration) *TokenBucket {
	rate := float64(maxPerMinute) / window.Seconds()
	return &TokenBucket{
		tokens:   make(map[string]*bucketInfo),
		rate:     rate,
		capacity: float64(maxPerMinute),
		window:   window,
	}
}

func (tb *TokenBucket) Allow(key string) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	b, exists := tb.tokens[key]
	if !exists {
		b = &bucketInfo{tokens: tb.capacity, lastTime: now}
		tb.tokens[key] = b
	}

	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * tb.rate
	if b.tokens > tb.capacity {
		b.tokens = tb.capacity
	}
	b.lastTime = now

	if b.tokens >= 1.0 {
		b.tokens--
		return true
	}
	return false
}

func RateLimitMiddleware(cfg ServerRateLimitConfig) func(http.Handler) http.Handler {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	maxPerMinute := cfg.MaxPerMinute
	if maxPerMinute <= 0 {
		maxPerMinute = 100
	}

	window := cfg.Window
	if window <= 0 {
		window = time.Minute
	}

	bucket := NewTokenBucket(maxPerMinute, window)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.RemoteAddr
			if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
				key = strings.Split(forwarded, ",")[0]
			}
			if !bucket.Allow(key) {
				http.Error(w, `{"error":"rate_limit","message":"too many requests"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func ApplySecurity(h http.Handler, secCfg ServerSecurityConfig) (http.Handler, *tls.Config) {
	var tlsCfg *tls.Config

	if secCfg.TLS.IsEnabled() {
		var err error
		tlsCfg, err = BuildTLSConfig(secCfg.TLS)
		if err != nil {
			tlsCfg = nil
		}
	}

	h = RateLimitMiddleware(secCfg.RateLimit)(h)
	h = AuthMiddleware(secCfg.Auth)(h)

	return h, tlsCfg
}

// SecurityConfigApplier adapts a ServerSecurityConfig to the opaque
// securityApplier interface accepted by
// extension.NewMCPServerWithSecurity. (The ApplySecurity method used
// to be defined directly on ServerSecurityConfig, but that type moved
// into internal/config, and the method needs this package's
// ApplySecurity implementation — so it became this wrapper.)
type SecurityConfigApplier struct {
	Cfg ServerSecurityConfig
}

// ApplySecurity wraps the handler with TLS, authentication, and
// rate-limiting middleware derived from the config.
func (a SecurityConfigApplier) ApplySecurity(h http.Handler) (http.Handler, interface{}) {
	return ApplySecurity(h, a.Cfg)
}

func BuildHTTPServer(addr string, handler http.Handler, secCfg ServerSecurityConfig) (*http.Server, error) {
	securedHandler, tlsCfg := ApplySecurity(handler, secCfg)

	srv := &http.Server{
		Addr:         addr,
		Handler:      securedHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if tlsCfg != nil {
		srv.TLSConfig = tlsCfg
	}

	return srv, nil
}

func Serve(srv *http.Server) error {
	if srv.TLSConfig != nil {
		return srv.ListenAndServeTLS("", "")
	}
	return srv.ListenAndServe()
}
