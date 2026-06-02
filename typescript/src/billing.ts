/**
 * Billing — multicloud cost reporting + AI-driven optimization recs.
 *
 * Endpoints:
 *   POST /api/v2/tenant/billing/multicloud
 *   POST /api/v2/tenant/billing/optimization
 */

import type { Transport } from './transport.js';

export interface MulticloudReport {
  totalUsd: number;
  breakdown: Record<string, number>;
  period?: Record<string, string>;
  raw: Record<string, unknown>;
}

export interface OptimizationRecommendation {
  action: string;
  resource: string;
  savingsUsd?: number;
  detail?: string;
  raw: Record<string, unknown>;
}

export interface OptimizationReport {
  potentialSavingsUsd: number;
  recommendations: OptimizationRecommendation[];
  raw: Record<string, unknown>;
}

export class Billing {
  constructor(private t: Transport) {}

  /** Cost breakdown across all connected providers for a date range. */
  async multicloud(input: { startDate: string; endDate: string }): Promise<MulticloudReport> {
    if (!input.startDate || !input.endDate) {
      throw new Error('billing.multicloud: startDate and endDate are required');
    }
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v2/tenant/billing/multicloud', {
      start_date: input.startDate, end_date: input.endDate,
    });
    const r = res.body ?? {};
    const bdRaw = (r.breakdown as Record<string, unknown>) ?? {};
    const breakdown: Record<string, number> = {};
    for (const [k, v] of Object.entries(bdRaw)) {
      if (typeof v === 'number') breakdown[k] = v;
    }
    let period: Record<string, string> | undefined;
    if (typeof r.period === 'object' && r.period !== null) {
      period = {};
      for (const [k, v] of Object.entries(r.period as Record<string, unknown>)) {
        if (typeof v === 'string') period[k] = v;
      }
    }
    return {
      totalUsd: typeof r.total_usd === 'number' ? r.total_usd : 0,
      breakdown,
      period,
      raw: r,
    };
  }

  /** AI-powered cost optimization recommendations. */
  async optimization(provider?: string): Promise<OptimizationReport> {
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v2/tenant/billing/optimization', {
      provider: provider ?? '',
    });
    const r = res.body ?? {};
    const recs: OptimizationRecommendation[] = [];
    const arr = r.recommendations;
    if (Array.isArray(arr)) {
      for (const item of arr) {
        if (typeof item === 'object' && item !== null) {
          const m = item as Record<string, unknown>;
          recs.push({
            action: (m.action as string) ?? '',
            resource: (m.resource as string) ?? '',
            savingsUsd: typeof m.savings_usd === 'number' ? m.savings_usd : undefined,
            detail: m.detail as string | undefined,
            raw: m,
          });
        }
      }
    }
    return {
      potentialSavingsUsd: typeof r.potential_savings_usd === 'number' ? r.potential_savings_usd : 0,
      recommendations: recs,
      raw: r,
    };
  }
}
