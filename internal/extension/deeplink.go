// Package extension provides deeplink parsing for one-click
// MCP extension installation.
package extension

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/km269/wukong/internal/config"
)

// parseDeeplink parses a deeplink URL into an ExtensionConfig.
// Supported formats:
//   wukong://extension?name=github&type=external&transport=stdio&command=npx&args=-y&args=@modelcontextprotocol/server-github
//   https://wukong.ai/extension?name=...
func parseDeeplink(rawURL string) (config.ExtensionConfig, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return config.ExtensionConfig{},
			fmt.Errorf("parse URL: %w", err)
	}

	q := u.Query()
	name := q.Get("name")
	if name == "" {
		return config.ExtensionConfig{},
			fmt.Errorf("deeplink missing required 'name' parameter")
	}

	extType := q.Get("type")
	if extType == "" {
		extType = "external"
	}

	transport := q.Get("transport")
	if transport == "" {
		transport = "stdio"
	}

	command := q.Get("command")
	args := q["args"]
	serverURL := q.Get("url")

	// Parse environment variables
	env := make(map[string]string)
	for key, vals := range q {
		if strings.HasPrefix(key, "env.") {
			envKey := strings.TrimPrefix(key, "env.")
			if len(vals) > 0 {
				env[envKey] = vals[0]
			}
		}
	}

	// Check if environment variables need expansion
	expandEnv := q.Get("expand_env") == "true"

	if transport == "stdio" && command == "" {
		return config.ExtensionConfig{},
			fmt.Errorf("stdio transport requires 'command' parameter")
	}

	if (transport == "sse" || transport == "streamable") && serverURL == "" {
		return config.ExtensionConfig{},
			fmt.Errorf("sse/streamable transport requires 'url' parameter")
	}

	ext := config.ExtensionConfig{
		Name:      name,
		Type:      extType,
		Transport: transport,
		Command:   command,
		Args:      args,
		URL:       serverURL,
		Env:       env,
		Enabled:   true,
		Deeplink:  rawURL,
	}

	// Expand environment variables in command/args if requested
	if expandEnv {
		ext.Command = expandEnvVar(ext.Command)
		for i := range ext.Args {
			ext.Args[i] = expandEnvVar(ext.Args[i])
		}
	}

	return ext, nil
}

// expandEnvVar expands environment variable references like ${VAR}.
func expandEnvVar(s string) string {
	// Simple ${VAR} expansion
	for {
		start := strings.Index(s, "${")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end == -1 {
			break
		}
		varName := s[start+2 : start+end]
		val := ""
		if v, ok := envLookup(varName); ok {
			val = v
		}
		s = s[:start] + val + s[start+end+1:]
	}
	return s
}

// envLookup looks up an environment variable.
// Uses os.LookupEnv for actual environment variable resolution.
var envLookup = os.LookupEnv
