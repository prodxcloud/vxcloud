// Package billing wraps the platform's multicloud billing endpoints.
//
// Endpoints:
//
//	POST /api/v2/tenant/billing/multicloud
//	POST /api/v2/tenant/billing/optimization (planned)
package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Client — construct via c.Billing().
type Client struct {
	T       *transport.Transport
	NodeURL string
}

// MulticloudInput requests a cost breakdown across all connected
// providers for a date range.
type MulticloudInput struct {
	StartDate string `json:"start_date"` // YYYY-MM-DD
	EndDate   string `json:"end_date"`
}

// MulticloudReport is the cost-breakdown response.
type MulticloudReport struct {
	TotalUSD  float64                `json:"total_usd"`
	Breakdown map[string]float64     `json:"breakdown"`
	Period    map[string]string      `json:"period,omitempty"`
	Raw       map[string]interface{} `json:"-"`
}

// Multicloud retrieves a cost breakdown across all connected providers.
func (c *Client) Multicloud(ctx context.Context, in MulticloudInput) (*MulticloudReport, error) {
	if in.StartDate == "" || in.EndDate == "" {
		return nil, errors.New("billing.Multicloud: StartDate and EndDate are required")
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/billing/multicloud")
	var raw map[string]interface{}
	if err := c.T.JSON(ctx, "billing.Multicloud", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("billing.Multicloud: %w", err)
	}
	r := &MulticloudReport{Raw: raw, Breakdown: map[string]float64{}}
	if v, ok := raw["total_usd"].(float64); ok {
		r.TotalUSD = v
	}
	if bd, ok := raw["breakdown"].(map[string]interface{}); ok {
		for k, v := range bd {
			if vv, ok := v.(float64); ok {
				r.Breakdown[k] = vv
			}
		}
	}
	if p, ok := raw["period"].(map[string]interface{}); ok {
		r.Period = map[string]string{}
		for k, v := range p {
			if s, ok := v.(string); ok {
				r.Period[k] = s
			}
		}
	}
	return r, nil
}

// OptimizationInput requests cost-optimization recommendations.
type OptimizationInput struct {
	Provider string `json:"provider,omitempty"` // empty = all providers
}

// Recommendation is one cost-saving opportunity.
type Recommendation struct {
	Action     string                 `json:"action"` // right-size | shutdown | migrate | …
	Resource   string                 `json:"resource"`
	SavingsUSD float64                `json:"savings_usd,omitempty"`
	Detail     string                 `json:"detail,omitempty"`
	Raw        map[string]interface{} `json:"-"`
}

// OptimizationReport aggregates potential savings + recommendations.
type OptimizationReport struct {
	PotentialSavingsUSD float64                `json:"potential_savings_usd"`
	Recommendations     []Recommendation       `json:"recommendations"`
	Raw                 map[string]interface{} `json:"-"`
}

// Optimization retrieves AI-powered cost-optimization recommendations.
func (c *Client) Optimization(ctx context.Context, in OptimizationInput) (*OptimizationReport, error) {
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/billing/optimization")
	var raw map[string]interface{}
	if err := c.T.JSON(ctx, "billing.Optimization", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("billing.Optimization: %w", err)
	}
	r := &OptimizationReport{Raw: raw}
	if v, ok := raw["potential_savings_usd"].(float64); ok {
		r.PotentialSavingsUSD = v
	}
	if recs, ok := raw["recommendations"].([]interface{}); ok {
		for _, item := range recs {
			if m, ok := item.(map[string]interface{}); ok {
				rec := Recommendation{Raw: m}
				if v, ok := m["action"].(string); ok {
					rec.Action = v
				}
				if v, ok := m["resource"].(string); ok {
					rec.Resource = v
				}
				if v, ok := m["savings_usd"].(float64); ok {
					rec.SavingsUSD = v
				}
				if v, ok := m["detail"].(string); ok {
					rec.Detail = v
				}
				r.Recommendations = append(r.Recommendations, rec)
			}
		}
	}
	return r, nil
}
