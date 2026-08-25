package salesshift

// Social distribution and webmaster tooling.
//
// The fan-out itself runs in `vxsocial`, a Go service that opens one goroutine
// per network; this package talks to the FastAPI in front of it, which owns
// persistence. Every job returns `wall_ms` alongside `sequential_ms` so the
// parallelism is a measurement rather than a claim.
//
// Provider calls are SIMULATED unless the deployment holds social API
// credentials — every delivery carries `Simulated`, and callers should surface
// it rather than reporting a post as published when nothing left the building.
//
//	GET    /api/v1/salesshift/social/channels
//	GET    /api/v1/salesshift/social/posts
//	POST   /api/v1/salesshift/social/posts
//	DELETE /api/v1/salesshift/social/posts/{id}
//	POST   /api/v1/salesshift/social/posts/{id}/distribute
//	GET    /api/v1/salesshift/social/stats
//	POST   /api/v1/salesshift/social/webmaster/{inspect,robots,sitemap,generate}

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strconv"

	"github.com/prodxcloud/vxcloud/transport"
)

// SocialChannel is one destination, with that network's real limits.
type SocialChannel struct {
	Key              string `json:"key"`
	Name             string `json:"name"`
	Kind             string `json:"kind"` // social | blog | community | messaging
	MaxChars         int    `json:"max_chars"`
	MaxImages        int    `json:"max_images"`
	TypicalLatencyMs int    `json:"typical_latency_ms"`
}

// ListChannels returns the distribution catalogue. `available` is false when
// the vxsocial service is unreachable — drafts still save, publishing does not.
func (c *Client) ListChannels(ctx context.Context) (channels []SocialChannel, simulated, available bool, err error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/channels")
	var raw struct {
		Channels  []SocialChannel `json:"channels"`
		Simulated bool            `json:"simulated"`
		Available bool            `json:"available"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListChannels", "GET", url, nil, &raw); err != nil {
		return nil, false, false, fmt.Errorf("salesshift.ListChannels: %w", err)
	}
	return raw.Channels, raw.Simulated, raw.Available, nil
}

// SocialDelivery is one network's outcome for one post.
type SocialDelivery struct {
	ID          string `json:"id"`
	Channel     string `json:"channel"`
	ChannelName string `json:"channel_name"`
	Status      string `json:"status"` // published | rejected | failed
	RemoteID    string `json:"remote_id"`
	Permalink   string `json:"permalink"`
	Error       string `json:"error"`
	Attempts    int    `json:"attempts"`
	DurationMs  int    `json:"duration_ms"`
	Simulated   bool   `json:"simulated"`
	CreatedAt   string `json:"created_at"`
}

// SocialPost is a piece of content plus its distribution record.
type SocialPost struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Content       string           `json:"content"`
	LinkURL       string           `json:"link_url"`
	Images        int              `json:"images"`
	Hashtags      []string         `json:"hashtags"`
	Channels      []string         `json:"channels"`
	Status        string           `json:"status"` // draft|scheduled|distributing|distributed|failed
	ScheduledAt   string           `json:"scheduled_at"`
	DistributedAt string           `json:"distributed_at"`
	CreatedAt     string           `json:"created_at"`
	JobID         string           `json:"job_id"`
	WallMs        *int             `json:"wall_ms"`
	SequentialMs  *int             `json:"sequential_ms"`
	Speedup       *float64         `json:"speedup"`
	Deliveries    []SocialDelivery `json:"deliveries"`
	Published     int              `json:"published"`
	Rejected      int              `json:"rejected"`
	Failed        int              `json:"failed"`
}

// ListPosts returns the workspace's posts, newest first, with deliveries.
//
// limit 0 takes the route's default of 50 rows. The route declares
// ge=1,le=200, so a limit above 200 is REJECTED with 422, not clamped.
// There was no limit parameter at all, so every caller silently got 50 and had
// no way to ask for more. The status is escaped rather than concatenated.
func (c *Client) ListPosts(ctx context.Context, status string, limit int) ([]SocialPost, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/posts")
	v := neturl.Values{}
	if status != "" {
		v.Set("status", status)
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if len(v) > 0 {
		url += "?" + v.Encode()
	}
	var raw struct {
		Posts []SocialPost `json:"posts"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListPosts", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListPosts: %w", err)
	}
	return raw.Posts, nil
}

// NewPost is the payload for CreatePost. ScheduledAt (RFC3339) queues the post
// for the Celery beat worker instead of leaving it a draft.
type NewPost struct {
	Title       string   `json:"title,omitempty"`
	Content     string   `json:"content"`
	LinkURL     string   `json:"link_url,omitempty"`
	Images      int      `json:"images,omitempty"`
	Hashtags    []string `json:"hashtags,omitempty"`
	Channels    []string `json:"channels,omitempty"`
	ScheduledAt string   `json:"scheduled_at,omitempty"`
}

// CreatePost saves a post. It does not distribute — call DistributePost, or
// set ScheduledAt and let the worker do it.
func (c *Client) CreatePost(ctx context.Context, in NewPost) (*SocialPost, error) {
	if in.Content == "" {
		return nil, errors.New("salesshift.CreatePost: Content is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/posts")
	var raw struct {
		Post SocialPost `json:"post"`
	}
	if err := c.T.JSON(ctx, "salesshift.CreatePost", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.CreatePost: %w", err)
	}
	return &raw.Post, nil
}

// DeletePost removes a post and, by cascade, its deliveries.
func (c *Client) DeletePost(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("salesshift.DeletePost: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/posts/"+id)
	if err := c.T.JSON(ctx, "salesshift.DeletePost", "DELETE", url, nil, nil); err != nil {
		return fmt.Errorf("salesshift.DeletePost: %w", err)
	}
	return nil
}

// DistributeJob is the measured result of one fan-out.
type DistributeJob struct {
	JobID        string  `json:"job_id"`
	WallMs       int     `json:"wall_ms"`
	SequentialMs int     `json:"sequential_ms"`
	Speedup      float64 `json:"speedup"`
	Concurrency  int     `json:"concurrency"`
	Published    int     `json:"published"`
	Rejected     int     `json:"rejected"`
	Failed       int     `json:"failed"`
}

// DistributePost fans the post out across its channels, in parallel.
// concurrency 0 means "as parallel as there are channels".
func (c *Client) DistributePost(ctx context.Context, id string, concurrency int) (*SocialPost, *DistributeJob, error) {
	if id == "" {
		return nil, nil, errors.New("salesshift.DistributePost: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/posts/"+id+"/distribute")
	var raw struct {
		Post SocialPost    `json:"post"`
		Job  DistributeJob `json:"job"`
	}
	if err := c.T.JSON(ctx, "salesshift.DistributePost", "POST", url,
		map[string]any{"concurrency": concurrency}, &raw); err != nil {
		return nil, nil, fmt.Errorf("salesshift.DistributePost: %w", err)
	}
	return &raw.Post, &raw.Job, nil
}

// SocialStats aggregates delivery outcomes for the workspace.
type SocialStats struct {
	Posts    int `json:"posts"`
	ByStatus map[string]struct {
		Count int `json:"count"`
		AvgMs int `json:"avg_ms"`
	} `json:"by_status"`
	PerChannel []struct {
		Channel   string `json:"channel"`
		Published int    `json:"published"`
	} `json:"per_channel"`
	BestSpeedup *float64 `json:"best_speedup"`
}

// GetSocialStats returns distribution totals for the workspace.
func (c *Client) GetSocialStats(ctx context.Context) (*SocialStats, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/stats")
	var out SocialStats
	if err := c.T.JSON(ctx, "salesshift.GetSocialStats", "GET", url, nil, &out); err != nil {
		return nil, fmt.Errorf("salesshift.GetSocialStats: %w", err)
	}
	return &out, nil
}

/* ── webmaster ─────────────────────────────────────────────────────────── */

// SeoCheck is one pass/fail finding from a live page fetch.
type SeoCheck struct {
	Key    string `json:"key"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Level  string `json:"level"` // pass | warn | error
	Weight int    `json:"weight"`
}

// InspectResult is what a crawler would find on a page. Everything here is
// read from the live response — nothing is estimated.
type InspectResult struct {
	URL              string            `json:"url"`
	StatusCode       int               `json:"status_code"`
	Score            int               `json:"score"`
	Title            string            `json:"title"`
	Description      string            `json:"description"`
	Canonical        string            `json:"canonical"`
	RobotsMeta       string            `json:"robots_meta"`
	Viewport         string            `json:"viewport"`
	H1Count          int               `json:"h1_count"`
	Images           int               `json:"images"`
	ImagesWithoutAlt int               `json:"images_without_alt"`
	OG               map[string]string `json:"og"`
	Twitter          map[string]string `json:"twitter"`
	JSONLDBlocks     int               `json:"json_ld_blocks"`
	HTMLBytes        int               `json:"html_bytes"`
	Checks           []SeoCheck        `json:"checks"`
	FetchedAt        string            `json:"fetched_at"`
}

// InspectURL fetches a page and reports its crawlability.
func (c *Client) InspectURL(ctx context.Context, target string) (*InspectResult, error) {
	if target == "" {
		return nil, errors.New("salesshift.InspectURL: url is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/webmaster/inspect")
	var out InspectResult
	if err := c.T.JSON(ctx, "salesshift.InspectURL", "POST", url,
		map[string]any{"url": target}, &out); err != nil {
		return nil, fmt.Errorf("salesshift.InspectURL: %w", err)
	}
	return &out, nil
}

// RobotsResult summarises a live robots.txt.
type RobotsResult struct {
	URL        string   `json:"url"`
	Exists     bool     `json:"exists"`
	StatusCode int      `json:"status_code"`
	Sitemaps   []string `json:"sitemaps"`
	Rules      []struct {
		Agent string `json:"agent"`
		Rule  string `json:"rule"`
		Path  string `json:"path"`
	} `json:"rules"`
	RuleCount        int    `json:"rule_count"`
	BlocksEverything bool   `json:"blocks_everything"`
	Content          string `json:"content"`
	Error            string `json:"error"`
}

// CheckRobots reads the site's robots.txt and says what it permits.
func (c *Client) CheckRobots(ctx context.Context, target string) (*RobotsResult, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/webmaster/robots")
	var out RobotsResult
	if err := c.T.JSON(ctx, "salesshift.CheckRobots", "POST", url,
		map[string]any{"url": target}, &out); err != nil {
		return nil, fmt.Errorf("salesshift.CheckRobots: %w", err)
	}
	return &out, nil
}

// SitemapResult summarises a live sitemap.
type SitemapResult struct {
	URL        string   `json:"url"`
	Exists     bool     `json:"exists"`
	StatusCode int      `json:"status_code"`
	IsIndex    bool     `json:"is_index"`
	Count      int      `json:"count"`
	URLs       []string `json:"urls"`
	Bytes      int      `json:"bytes"`
	Error      string   `json:"error"`
}

// CheckSitemap fetches and summarises a sitemap (index or urlset).
func (c *Client) CheckSitemap(ctx context.Context, target string) (*SitemapResult, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/webmaster/sitemap")
	var out SitemapResult
	if err := c.T.JSON(ctx, "salesshift.CheckSitemap", "POST", url,
		map[string]any{"url": target}, &out); err != nil {
		return nil, fmt.Errorf("salesshift.CheckSitemap: %w", err)
	}
	return &out, nil
}

// GenerateWebmasterFiles produces a robots.txt and sitemap.xml for the given
// paths. Nothing is crawled to discover them — a sitemap should list the URLs
// the owner meant to publish.
func (c *Client) GenerateWebmasterFiles(ctx context.Context, domain string, paths, disallow []string) (robotsTxt, sitemapXML string, urlCount int, err error) {
	if domain == "" {
		return "", "", 0, errors.New("salesshift.GenerateWebmasterFiles: domain is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/social/webmaster/generate")
	body := map[string]any{"domain": domain, "paths": paths}
	if len(disallow) > 0 {
		body["disallow"] = disallow
	}
	var raw struct {
		RobotsTxt  string `json:"robots_txt"`
		SitemapXML string `json:"sitemap_xml"`
		URLCount   int    `json:"url_count"`
	}
	if err := c.T.JSON(ctx, "salesshift.GenerateWebmasterFiles", "POST", url, body, &raw); err != nil {
		return "", "", 0, fmt.Errorf("salesshift.GenerateWebmasterFiles: %w", err)
	}
	return raw.RobotsTxt, raw.SitemapXML, raw.URLCount, nil
}
