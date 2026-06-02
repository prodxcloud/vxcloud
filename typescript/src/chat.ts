/**
 * Chat resource — multi-provider AI chat. Provider envelope normalizes
 * Anthropic / OpenAI / Google / OpenClaw / Deepseek / Qwen / Groq / etc.
 *
 * The platform handler at POST /api/v2/chat/send accepts a provider key
 * and forwards using credentials stored under /api/v2/setup/ai-*.
 *
 * Streaming via SSE is documented in BIG_PLAN.md M3 (followup); v0.1 is
 * one-shot only.
 */

import type { Transport } from './transport.js';

export type ChatProvider =
  | 'anthropic' | 'openai' | 'google' | 'openclaw'
  | 'deepseek' | 'qwen' | 'groq' | 'mistral'
  | 'perplexity' | 'huggingface' | 'ollama' | 'hermes'
  | 'cohere' | 'azure-openai' | 'gemini' | 'llama';

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant';
  content: string;
}

export interface ChatSendInput {
  provider: ChatProvider;
  model: string;
  messages: ChatMessage[];
  /** Convenience — prepended as a system message if set. */
  systemPrompt?: string;
  /** 0–1; default 0.7 server-side. */
  temperature?: number;
  /** 0 = provider default. */
  maxTokens?: number;
}

export interface ChatSendOutput {
  completion: string;
  provider?: string;
  model?: string;
  inputTokens?: number;
  outputTokens?: number;
  raw: Record<string, unknown>;
}

export class Chat {
  constructor(private t: Transport) {}

  async send(input: ChatSendInput): Promise<ChatSendOutput> {
    if (!input.messages?.length && !input.systemPrompt) {
      throw new Error('chat.send: messages or systemPrompt is required');
    }
    const messages = [...(input.messages ?? [])];
    if (input.systemPrompt) {
      messages.unshift({ role: 'system', content: input.systemPrompt });
    }
    const body: Record<string, unknown> = {
      provider: input.provider,
      model: input.model,
      messages,
    };
    if (input.temperature && input.temperature > 0) body.temperature = input.temperature;
    if (input.maxTokens && input.maxTokens > 0) body.max_tokens = input.maxTokens;

    const res = await this.t.postJSON<Record<string, unknown>>('/api/v2/chat/send', body);
    const r = res.body ?? {};
    return {
      completion: (r.completion as string) ?? '',
      provider: r.provider as string | undefined,
      model: r.model as string | undefined,
      inputTokens: typeof r.input_tokens === 'number' ? r.input_tokens : undefined,
      outputTokens: typeof r.output_tokens === 'number' ? r.output_tokens : undefined,
      raw: r,
    };
  }

  /** One-shot helper: ask a single question, get a string back. */
  async quick(provider: ChatProvider, model: string, question: string): Promise<string> {
    const out = await this.send({
      provider, model,
      messages: [{ role: 'user', content: question }],
    });
    return out.completion;
  }
}
