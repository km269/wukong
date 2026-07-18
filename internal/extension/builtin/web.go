package builtin

import (
	"context"
	"fmt"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
)

type WebToolSet struct {
	tools  []tool.Tool
	inited bool
	closed bool
}

func NewWebToolSet(cfg *config.WukongConfig) *WebToolSet {
	ts := &WebToolSet{}
	ts.tools = make([]tool.Tool, 0, 4)

	var enabledBackends []string
	var searxngURL string
	var searxngAPIKey string
	var tavilyAPIKey string
	var googleAPIKey string
	var googleCSEID string
	var bingAPIKey string

	if cfg != nil {
		searchCfg := cfg.Browser.Search

		if len(searchCfg.Backends) > 0 {
			enabledBackends = searchCfg.Backends
		}

		if searchCfg.SearXNG.URL != "" {
			searxngURL = searchCfg.SearXNG.URL
		} else {
			searxngURL = "http://localhost:8080/"
		}
		searxngAPIKey = searchCfg.SearXNG.APIKey
		tavilyAPIKey = searchCfg.Tavily.APIKey
		googleAPIKey = searchCfg.Google.APIKey
		googleCSEID = searchCfg.Google.CSEID
		bingAPIKey = searchCfg.Bing.APIKey
	}

	if len(enabledBackends) == 0 {
		enabledBackends = []string{"duckduckgo"}
	}

	validBackends := make([]string, 0, len(enabledBackends))
	for _, backend := range enabledBackends {
		switch backend {
		case "duckduckgo", "searxng", "tavily", "google", "bing":
			validBackends = append(validBackends, backend)
			if util.DebugEnabled {
				fmt.Printf("[wukong/web] enabled search backend: %s\n", backend)
			}
		default:
			if util.DebugEnabled {
				fmt.Printf("[wukong/web] warning: unknown search backend %q, skipping\n", backend)
			}
		}
	}

	if len(validBackends) == 0 {
		validBackends = append(validBackends, "duckduckgo")
		if util.DebugEnabled {
			fmt.Println("[wukong/web] no valid backends configured, using default: duckduckgo")
		}
	}

	if len(validBackends) == 1 {
		switch validBackends[0] {
		case "searxng":
			ts.tools = append(ts.tools, NewSearXNGTool(searxngURL, searxngAPIKey))
		case "tavily":
			if tavilyAPIKey == "" {
				if util.DebugEnabled {
					fmt.Println("[wukong/web] warning: tavily backend enabled but no API key configured, falling back to duckduckgo")
				}
				ts.tools = append(ts.tools, duckduckgo.NewTool())
			} else {
				ts.tools = append(ts.tools, NewTavilyTool(tavilyAPIKey))
			}
		case "google":
			if googleAPIKey == "" || googleCSEID == "" {
				if util.DebugEnabled {
					fmt.Println("[wukong/web] warning: google backend enabled but API key or CSE ID not configured, falling back to duckduckgo")
				}
				ts.tools = append(ts.tools, duckduckgo.NewTool())
			} else {
				ts.tools = append(ts.tools, NewGoogleTool(googleAPIKey, googleCSEID))
			}
		case "bing":
			if bingAPIKey == "" {
				if util.DebugEnabled {
					fmt.Println("[wukong/web] warning: bing backend enabled but API key not configured, falling back to duckduckgo")
				}
				ts.tools = append(ts.tools, duckduckgo.NewTool())
			} else {
				ts.tools = append(ts.tools, NewBingTool(bingAPIKey))
			}
		case "duckduckgo":
		default:
			ts.tools = append(ts.tools, duckduckgo.NewTool())
		}
	} else {
		if util.DebugEnabled {
			fmt.Printf("[wukong/web] aggregating %d search backends\n", len(validBackends))
		}
		ts.tools = append(ts.tools, NewAggregateSearchTool(validBackends, searxngURL, searxngAPIKey, tavilyAPIKey, googleAPIKey, googleCSEID, bingAPIKey))
	}

	return ts
}

func (ts *WebToolSet) Tools(_ context.Context) []tool.Tool {
	return ts.tools
}

func (ts *WebToolSet) Name() string {
	return "web"
}

func (ts *WebToolSet) Init(_ context.Context) error {
	ts.inited = true
	return nil
}

func (ts *WebToolSet) Close() error {
	ts.closed = true
	return nil
}
