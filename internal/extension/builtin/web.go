package builtin

import (
	"context"
	"fmt"

	"github.com/km269/wukong/internal/browser"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/cortex"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
)

type WebToolSet struct {
	tools       []tool.Tool
	inited      bool
	closed      bool
	browser     *browser.Controller
	cortexStore *cortex.CortexStore
	userID      string
	aggregate   *aggregateSearchTool // reference for late injection
}

func NewWebToolSet(cfg *config.WukongConfig) *WebToolSet {
	ts := &WebToolSet{}
	ts.tools = make([]tool.Tool, 0, 4)

	// Create browser controller for page fetching with anti-detection.
	if cfg != nil {
		ts.browser = browser.NewController(&cfg.Browser)
	}

	var enabledBackends []string
	var searxngURL string
	var searxngAPIKey string
	var tavilyAPIKey string
	var googleAPIKey string
	var googleCSEID string
	var bingAPIKey string

	if cfg != nil {
		searchCfg := cfg.Browser.Search

		// Build enabled backends from per-backend Enabled fields.
		if searchCfg.SearXNG.Enabled {
			enabledBackends = append(enabledBackends, "searxng")
		}
		if searchCfg.DuckDuckGo.Enabled {
			enabledBackends = append(enabledBackends, "duckduckgo")
		}
		if searchCfg.Tavily.Enabled {
			enabledBackends = append(enabledBackends, "tavily")
		}
		if searchCfg.Google.Enabled {
			enabledBackends = append(enabledBackends, "google")
		}
		if searchCfg.Bing.Enabled {
			enabledBackends = append(enabledBackends, "bing")
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

	// Default to DuckDuckGo when no backend is explicitly enabled.
	if len(enabledBackends) == 0 {
		enabledBackends = []string{"duckduckgo"}
	}

	if len(enabledBackends) == 1 {
		switch enabledBackends[0] {
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
			fmt.Printf("[wukong/web] aggregating %d search backends\n", len(enabledBackends))
		}
		aggTool, agg := NewAggregateSearchTool(
			enabledBackends, searxngURL, searxngAPIKey, tavilyAPIKey,
			googleAPIKey, googleCSEID, bingAPIKey,
			ts.browser, ts.cortexStore, ts.userID,
		)
		ts.tools = append(ts.tools, aggTool)
		ts.aggregate = agg
	}

	return ts
}

// SetCortexStore injects the CortexStore for internal index search.
// Called after Initialize when the store becomes available.
// Accepts any to match the manager's dynamic interface pattern.
func (ts *WebToolSet) SetCortexStore(cs any, userID string) {
	store, ok := cs.(*cortex.CortexStore)
	if !ok || store == nil {
		return
	}
	ts.cortexStore = store
	ts.userID = userID
	if ts.aggregate != nil {
		ts.aggregate.SetCortexStore(store, userID)
	}
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
	if ts.browser != nil {
		// Controller doesn't have a Close method, but the backend
		// (chromedp/rod) is cleaned up via its own context cancellation.
	}
	return nil
}
