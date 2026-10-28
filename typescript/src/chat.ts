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

import type { Transport, SSEEvent } from './transport.js';

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

export interface Conversation {
  sessionId: string;
  title: string;
  firstQuery: string;
  agentType: string;
  messageCount: number;
  startedAt: string;
  lastMessageAt: string;
}

export interface ConversationHistoryEntry {
  id: string;
  sessionId: string;
  userId: string;
  agentType: string;
  query: string;
  response: string;
  timestamp: string;
  title: string;
  metadata: Record<string, unknown>;
  aiModel: string;
  intent: string;
}

export interface ConversationList {
  conversations: Conversation[];
  total: number;
  limit?: number;
  offset?: number;
}

export interface AgentStreamInput {
  query: string;
  agent?: string;
  model?: string;
  sessionId?: string;
  userId?: string;
  workspaceId?: string;
  context?: Record<string, unknown>;
  /** The caller's own transcript. Sent verbatim to workflow chat v3. */
  history?: Array<{ role: 'user' | 'assistant'; content: string }>;
}

export interface WorkflowChatInput extends AgentStreamInput {
  nodes?: unknown[];
  edges?: unknown[];
  action?: string;
  mode?: string;
  /** Running a workflow is always an explicit, separate confirmation. */
  confirmRun?: boolean;
}

export type AgentStreamEvent = SSEEvent;

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

  /** List the signed-in user's conversations, with search and paging. */
  async listConversations(opts: {
    limit?: number; offset?: number; search?: string; agentType?: string; sessionIds?: string[];
  } = {}): Promise<ConversationList> {
    const q = new URLSearchParams();
    if (opts.limit !== undefined) q.set('limit', String(opts.limit));
    if (opts.offset !== undefined) q.set('offset', String(opts.offset));
    if (opts.search) q.set('q', opts.search);
    if (opts.agentType) q.set('agent_type', opts.agentType);
    if (opts.sessionIds?.length) q.set('session_ids', opts.sessionIds.join(','));
    const suffix = q.toString() ? `?${q}` : '';
    const body = (await this.t.get<Record<string, unknown>>(`/api/v3/ai/agent/conversations${suffix}`)).body ?? {};
    const rows = Array.isArray(body.conversations) ? body.conversations as Record<string, unknown>[] : [];
    return {
      conversations: rows.map((r) => ({
        sessionId: String(r.session_id ?? ''), title: String(r.title ?? ''),
        firstQuery: String(r.first_query ?? ''), agentType: String(r.agent_type ?? ''),
        messageCount: Number(r.message_count ?? 0), startedAt: String(r.started_at ?? ''),
        lastMessageAt: String(r.last_message_at ?? ''),
      })),
      total: Number(body.total ?? 0), limit: Number(body.limit ?? opts.limit ?? 50),
      offset: Number(body.offset ?? opts.offset ?? 0),
    };
  }

  /** Read one conversation in chronological order. */
  async history(sessionId: string, opts: { agentType?: string; limit?: number } = {}): Promise<ConversationHistoryEntry[]> {
    if (!sessionId) throw new Error('chat.history: sessionId is required');
    const q = new URLSearchParams({ session_id: sessionId });
    if (opts.agentType) q.set('agent_type', opts.agentType);
    if (opts.limit !== undefined) q.set('limit', String(opts.limit));
    const body = (await this.t.get<Record<string, unknown>>(`/api/v3/ai/agent/history?${q}`)).body ?? {};
    const rows = Array.isArray(body.history) ? body.history as Record<string, unknown>[] : [];
    return rows.reverse().map((r) => ({
      id: String(r.id ?? ''), sessionId: String(r.session_id ?? ''), userId: String(r.user_id ?? ''),
      agentType: String(r.agent_type ?? ''), query: String(r.query ?? ''), response: String(r.response ?? ''),
      timestamp: String(r.timestamp ?? ''), title: String(r.title ?? ''),
      metadata: (r.metadata && typeof r.metadata === 'object' ? r.metadata : {}) as Record<string, unknown>,
      aiModel: String(r.ai_model ?? ''), intent: String(r.intent ?? ''),
    }));
  }

  async saveMessage(input: { sessionId: string; messageType: 'human' | 'ai'; content: string; agentType?: string }): Promise<Record<string, unknown>> {
    if (!input.sessionId || !input.content) throw new Error('chat.saveMessage: sessionId and content are required');
    return (await this.t.postJSON<Record<string, unknown>>('/api/v3/ai/agent/conversation/save', {
      session_id: input.sessionId, message_type: input.messageType, content: input.content, agent_type: input.agentType ?? '',
    })).body ?? {};
  }

  async renameConversation(sessionId: string, title: string): Promise<Record<string, unknown>> {
    if (!sessionId || !title.trim()) throw new Error('chat.renameConversation: sessionId and title are required');
    return (await this.t.postJSON<Record<string, unknown>>('/api/v3/ai/agent/conversation/rename', {
      session_id: sessionId, title,
    })).body ?? {};
  }

  async deleteConversation(sessionId: string): Promise<Record<string, unknown>> {
    if (!sessionId) throw new Error('chat.deleteConversation: sessionId is required');
    return (await this.t.postJSON<Record<string, unknown>>('/api/v3/ai/agent/conversation/delete', {
      session_id: sessionId,
    })).body ?? {};
  }

  /** Stream a FastAPI agent. Events include text_delta, thinking, confirm,
   * model_info, tool_call, tool_result, error and done. */
  streamAgent(input: AgentStreamInput): AsyncGenerator<AgentStreamEvent> {
    if (!input.query.trim()) throw new Error('chat.streamAgent: query is required');
    return this.t.postSSE('/api/v3/ai/agent/chat', {
      query: input.query, agent: input.agent ?? 'networking', model: input.model ?? '',
      session_id: input.sessionId ?? '', user_id: input.userId ?? '', workspace_id: input.workspaceId ?? '',
      context: input.context ?? {}, history: input.history ?? [], stream: true,
    });
  }

  /** Stream the node-native workflow agent. A run can only be requested when
   * the caller explicitly sets confirmRun=true; the server still performs its
   * own exact confirmation check. */
  streamWorkflow(input: WorkflowChatInput): AsyncGenerator<AgentStreamEvent> {
    if (!input.query.trim()) throw new Error('chat.streamWorkflow: query is required');
    if (input.confirmRun && !/^\s*(run it|yes,? run it|go ahead and run it)\s*$/i.test(input.query)) {
      throw new Error('chat.streamWorkflow: confirmRun requires an explicit run-it command');
    }
    return this.t.postSSE('/api/v2/workflow/agent/chat', {
      query: input.query, model: input.model ?? '', session_id: input.sessionId ?? '',
      user_id: input.userId ?? '', workflow_id: String(input.context?.workflow_id ?? ''),
      action: input.action ?? 'generate', mode: input.mode ?? 'build', surface: 'chat',
      history: input.history ?? [], nodes: input.nodes ?? [], edges: input.edges ?? [], stream: true,
    });
  }

  /** Execute a read-only or planning Mission Control chat command. */
  async missionCommand(command: string, opts: { conversationId?: string; timezone?: string } = {}): Promise<Record<string, unknown>> {
    if (!command.trim()) throw new Error('chat.missionCommand: command is required');
    return (await this.t.postJSON<Record<string, unknown>>('/api/v2/vxchrono/chat/command', {
      command, surface: 'chat', conversation_id: opts.conversationId ?? '', timezone: opts.timezone ?? '',
    })).body ?? {};
  }
}
