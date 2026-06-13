// Package webscraper is the resource module for the node's goroutine-driven web
// research agent: a concurrent BFS crawler, a multi-engine web search, and an
// LLM (or non-AI extractive) deep-research loop.
//
// Endpoints (all on the per-tenant node, JSON bodies):
//
//	POST /api/v2/tenant/agents/webscraper/scrape         — concurrent BFS crawl
//	POST /api/v2/tenant/agents/webscraper/search         — multi-engine search
//	POST /api/v2/tenant/agents/webscraper/deep-research  — deep search + report
//
// Crawl and Search are NOT AI. Deep research uses the caller's LLM provider, or
// runs fully offline (extractive) when Provider is "none".
package webscraper

import (
	"context"
	"errors"

	"github.com/prodxcloud/vxcloud/transport"
)

// Result is a decoded JSON object response.
type Result = map[string]interface{}

// Client is the web-research facade. Acquire it with c.WebScraper().
type Client struct {
	T       *transport.Transport
	NodeURL string
}

func (c *Client) post(ctx context.Context, op, path string, body map[string]interface{}) (Result, error) {
	url := transport.JoinURL(c.NodeURL, path)
	var out Result
	if err := c.T.JSON(ctx, op, "POST", url, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ScrapeOpts configures a concurrent BFS crawl. Provide URL or URLs.
type ScrapeOpts struct {
	URL          string   // single seed
	URLs         []string // multiple seeds
	MaxDepth     int      // BFS depth (default/cap server-side)
	MaxPages     int      // hard page budget
	SameHost     bool     // restrict to seed hosts
	IncludeLinks bool     // include extracted links per page
	Concurrency  int      // parallel fetch workers
	MaxChars     int      // per-page text cap
}

// Scrape runs the concurrent BFS crawler and returns extracted pages (title,
// text, summary, headings, links, …).
func (c *Client) Scrape(ctx context.Context, opts ScrapeOpts) (Result, error) {
	if opts.URL == "" && len(opts.URLs) == 0 {
		return nil, errors.New("webscraper.Scrape: provide URL or URLs")
	}
	body := map[string]interface{}{
		"url":           opts.URL,
		"urls":          opts.URLs,
		"max_depth":     opts.MaxDepth,
		"max_pages":     opts.MaxPages,
		"same_host":     opts.SameHost,
		"include_links": opts.IncludeLinks,
		"concurrency":   opts.Concurrency,
		"max_chars":     opts.MaxChars,
	}
	return c.post(ctx, "webscraper.Scrape", "/api/v2/tenant/agents/webscraper/scrape", body)
}

// SearchOpts configures a multi-engine web search.
type SearchOpts struct {
	Limit   int      // max merged results
	Engines []string // subset of ddg_lite|duckduckgo|ddg_api|bing|google (empty = reliable default)
}

// Search fans the query across multiple engines, merges + dedupes + ranks by
// cross-engine agreement, and falls back to robust sources if the primary set
// is empty.
func (c *Client) Search(ctx context.Context, query string, opts SearchOpts) (Result, error) {
	if query == "" {
		return nil, errors.New("webscraper.Search: query is required")
	}
	body := map[string]interface{}{
		"query":   query,
		"limit":   opts.Limit,
		"engines": opts.Engines,
	}
	return c.post(ctx, "webscraper.Search", "/api/v2/tenant/agents/webscraper/search", body)
}

// DeepOpts configures the deep-research loop. Provider "none" runs fully offline
// (extractive, no LLM); empty uses the agent default (Ollama).
type DeepOpts struct {
	Provider  string
	Model     string
	MaxRounds int
	Breadth   int
	TopK      int
	MaxChars  int
}

// DeepResearch runs the multi-round search→fetch→summarise→synthesise loop and
// returns a cited markdown report plus its sources.
func (c *Client) DeepResearch(ctx context.Context, query string, opts DeepOpts) (Result, error) {
	if query == "" {
		return nil, errors.New("webscraper.DeepResearch: query is required")
	}
	body := map[string]interface{}{
		"query":      query,
		"provider":   opts.Provider,
		"model":      opts.Model,
		"max_rounds": opts.MaxRounds,
		"breadth":    opts.Breadth,
		"top_k":      opts.TopK,
		"max_chars":  opts.MaxChars,
	}
	return c.post(ctx, "webscraper.DeepResearch", "/api/v2/tenant/agents/webscraper/deep-research", body)
}
