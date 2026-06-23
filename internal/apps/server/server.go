// Package server provides a local HTTP server for previewing HTML apps.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Server provides a local HTTP server for previewing apps.
type Server struct {
	mu       sync.RWMutex
	httpSrv  *http.Server
	running  bool
	port     int
	rootDir  string
	addr     string
	appName  string
}

// Config holds server configuration.
type Config struct {
	// Port is the port to listen on (0 = auto-select).
	Port int

	// RootDir is the root directory to serve.
	RootDir string

	// AppName is the name of the app being served.
	AppName string

	// ReadTimeout is the read timeout.
	ReadTimeout time.Duration

	// WriteTimeout is the write timeout.
	WriteTimeout time.Duration
}

// DefaultConfig returns default server configuration.
func DefaultConfig() Config {
	return Config{
		Port:         0, // auto-select
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
}

// NewServer creates a new preview server.
func NewServer(cfg Config) *Server {
	return &Server{
		port:    cfg.Port,
		rootDir: cfg.RootDir,
		appName: cfg.AppName,
	}
}

// Start starts the server.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}

	// 确定端口
	port := s.port
	if port == 0 {
		// 自动选择端口
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("listen: %w", err)
		}
		port = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
	}

	// 创建 HTTP 服务器
	s.addr = fmt.Sprintf("http://localhost:%d", port)
	mux := http.NewServeMux()

	// 注册处理程序
	mux.HandleFunc("/", s.handleRequest)

	s.httpSrv = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:     mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	s.running = true
	s.mu.Unlock()

	// 启动服务器
	go func() {
		<-ctx.Done()
		s.httpSrv.Shutdown(context.Background())
	}()

	if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}

// Start starts the server and waits for it to be ready.
func (s *Server) StartAndWait(ctx context.Context) (string, error) {
	// 启动服务器
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	// 等待服务器启动
	for i := 0; i < 50; i++ {
		s.mu.RLock()
		running := s.running
		addr := s.addr
		s.mu.RUnlock()

		if running && addr != "" {
			return addr, nil
		}

		select {
		case err := <-errCh:
			return "", err
		case <-time.After(100 * time.Millisecond):
		}
	}

	return "", fmt.Errorf("server failed to start")
}

// Stop stops the server.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	if s.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}

	s.running = false
	return nil
}

// IsRunning returns whether the server is running.
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Address returns the server address.
func (s *Server) Address() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Port returns the server port.
func (s *Server) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// handleRequest handles HTTP requests.
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// 处理根路径
	path := r.URL.Path
	if path == "/" {
		// 尝试提供 index.html
		indexPath := filepath.Join(s.rootDir, "index.html")
		if exists(indexPath) {
			http.ServeFile(w, r, indexPath)
			return
		}
		// 提供目录列表
		s.serveDirectoryList(w, r)
		return
	}

	// 清理路径
	path = filepath.Clean(path)

	// 构建完整文件路径
	filePath := filepath.Join(s.rootDir, path)

	// 安全检查：确保路径在根目录内
	absRoot, _ := filepath.Abs(s.rootDir)
	absFile, err := filepath.Abs(filePath)
	if err != nil || !hasPrefix(absFile, absRoot) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// 检查文件是否存在
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	if info.IsDir() {
		// 尝试提供 index.html
		indexPath := filepath.Join(filePath, "index.html")
		if exists(indexPath) {
			http.ServeFile(w, r, indexPath)
			return
		}
		// 提供目录列表
		s.serveDirectoryList(w, r)
		return
	}

	// 服务文件
	http.ServeFile(w, r, filePath)
}

// serveDirectoryList generates an HTML directory listing.
func (s *Server) serveDirectoryList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = ""
	}
	dirPath := filepath.Join(s.rootDir, path)

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!DOCTYPE html>\n<html>\n<head>\n")
	fmt.Fprintf(w, "<meta charset=\"utf-8\">\n")
	fmt.Fprintf(w, "<title>Index of %s</title>\n", path)
	fmt.Fprintf(w, "<style>\n")
	fmt.Fprintf(w, "body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; ")
	fmt.Fprintf(w, "max-width: 800px; margin: 40px auto; padding: 0 20px; }\n")
	fmt.Fprintf(w, "h1 { border-bottom: 1px solid #eee; padding-bottom: 10px; }\n")
	fmt.Fprintf(w, "ul { list-style: none; padding: 0; }\n")
	fmt.Fprintf(w, "li { padding: 8px 0; border-bottom: 1px solid #f5f5f5; }\n")
	fmt.Fprintf(w, "a { text-decoration: none; color: #0066cc; }\n")
	fmt.Fprintf(w, "a:hover { text-decoration: underline; }\n")
	fmt.Fprintf(w, ".folder { color: #666; }\n")
	fmt.Fprintf(w, "</style>\n")
	fmt.Fprintf(w, "</head>\n<body>\n")
	fmt.Fprintf(w, "<h1>Index of %s</h1>\n", path)
	fmt.Fprintf(w, "<ul>\n")

	// 父目录链接
	if path != "" {
		parentPath := filepath.Dir(path)
		if parentPath == "." {
			parentPath = "/"
		}
		fmt.Fprintf(w, "<li><a href=\"%s\">📁 ..</a></li>\n", parentPath)
	}

	// 子目录和文件
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			fmt.Fprintf(w, "<li><a href=\"%s/\">📁 %s/</a></li>\n", entryPath, entry.Name())
		} else {
			fmt.Fprintf(w, "<li><a href=\"%s\">📄 %s</a></li>\n", entryPath, entry.Name())
		}
	}

	fmt.Fprintf(w, "</ul>\n")
	fmt.Fprintf(w, "<p style=\"color:#666;margin-top:20px;\">Wukong Apps Preview Server</p>\n")
	fmt.Fprintf(w, "</body>\n</html>")
}

// hasPrefix checks if path has the given prefix.
func hasPrefix(path, prefix string) bool {
	rel, err := filepath.Rel(prefix, path)
	if err != nil {
		return false
	}
	return rel == "." || !filepath.IsAbs(rel)
}

// exists checks if a file exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}