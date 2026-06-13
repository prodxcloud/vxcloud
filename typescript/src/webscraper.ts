/**
 * WebScraper — the node's goroutine-driven web-research agent: a concurrent BFS
 * crawler, a multi-engine web search, and an LLM (or offline extractive)
 * deep-research loop. Mirrors `webscraper/webscraper.go` and Python vxsdk.
 *
 * Crawl and Search are NOT AI. Deep research uses the caller's LLM provider, or
 * runs fully offline (extractive) when `provider` is "none".
 *
 * Endpoints (all on the per-tenant node):
 *   POST /api/v2/tenant/agents/webscraper/scrape         — concurrent BFS crawl
 *   POST /api/v2/tenant/agents/webscraper/search         — multi-engine search
 *   POST /api/v2/tenant/agents/webscraper/deep-research  — deep search + report
 */

import type { Transport } from './transport.js';

/** A decoded JSON object response. */
export type Result = Record<string, unknown>;

/** Configures a concurrent BFS crawl. Provide `url` or `urls`. */
export interface ScrapeInput {
  url?: string;
  urls?: string[];
  /** BFS depth (cap applied server-side). */
  maxDepth?: number;
  /** Hard page budget. */
  maxPages?: number;
  /** Restrict crawl to the seed hosts. */
  sameHost?: boolean;
  /** Include extracted links per page. */
  includeLinks?: boolean;
  /** Parallel fetch workers. */
  concurrency?: number;
  /** Per-page text cap. */
  maxChars?: number;
}

/** Configures a multi-engine web search. */
export interface SearchInput {
  /** Max merged results. */
  limit?: number;
  /** Subset of ddg_lite|duckduckgo|ddg_api|bing|google (empty = reliable default). */
  engines?: string[];
}

/** Configures the deep-research loop. `provider: "none"` runs fully offline. */
export interface DeepResearchInput {
  provider?: string;
  model?: string;
  maxRounds?: number;
  breadth?: number;
  topK?: number;
  maxChars?: number;
}

export class WebScraper {
  constructor(private t: Transport) {}

  /** Run the concurrent BFS crawler; returns extracted pages (title, text,
   *  summary, headings, links, …). */
  async scrape(input: ScrapeInput): Promise<Result> {
    if (!input.url && (!input.urls || input.urls.length === 0)) {
      throw new Error('webscraper.scrape: provide url or urls');
    }
    const body = {
      url: input.url ?? '',
      urls: input.urls ?? [],
      max_depth: input.maxDepth ?? 0,
      max_pages: input.maxPages ?? 0,
      same_host: input.sameHost ?? false,
      include_links: input.includeLinks ?? false,
      concurrency: input.concurrency ?? 0,
      max_chars: input.maxChars ?? 0,
    };
    return (await this.t.postJSON<Result>('/api/v2/tenant/agents/webscraper/scrape', body)).body ?? {};
  }

  /** Fan a query across multiple engines, merge + dedupe + rank by cross-engine
   *  agreement. */
  async search(query: string, input: SearchInput = {}): Promise<Result> {
    if (!query) throw new Error('webscraper.search: query is required');
    const body = { query, limit: input.limit ?? 0, engines: input.engines ?? [] };
    return (await this.t.postJSON<Result>('/api/v2/tenant/agents/webscraper/search', body)).body ?? {};
  }

  /** Run the multi-round search→fetch→summarise→synthesise loop; returns a
   *  cited markdown report plus its sources. */
  async deepResearch(query: string, input: DeepResearchInput = {}): Promise<Result> {
    if (!query) throw new Error('webscraper.deepResearch: query is required');
    const body = {
      query,
      provider: input.provider ?? '',
      model: input.model ?? '',
      max_rounds: input.maxRounds ?? 0,
      breadth: input.breadth ?? 0,
      top_k: input.topK ?? 0,
      max_chars: input.maxChars ?? 0,
    };
    return (await this.t.postJSON<Result>('/api/v2/tenant/agents/webscraper/deep-research', body)).body ?? {};
  }
}
