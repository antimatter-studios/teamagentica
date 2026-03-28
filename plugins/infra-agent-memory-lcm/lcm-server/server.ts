/**
 * LCM Server — HTTP wrapper around lossless-claw's core classes.
 *
 * Runs as a subprocess managed by supervisord alongside the Go sidecar.
 * The Go sidecar proxies MCP tool calls to this server on port 8092.
 * LLM summarization calls go back to the Go sidecar's /internal/llm/complete endpoint.
 */

import { DatabaseSync } from "node:sqlite";
import express from "express";

import { ConversationStore } from "@martian-engineering/lossless-claw/src/store/conversation-store.js";
import { SummaryStore } from "@martian-engineering/lossless-claw/src/store/summary-store.js";
import { CompactionEngine } from "@martian-engineering/lossless-claw/src/compaction.js";
import type { CompactionConfig } from "@martian-engineering/lossless-claw/src/compaction.js";
import { ContextAssembler } from "@martian-engineering/lossless-claw/src/assembler.js";
import { runLcmMigrations } from "@martian-engineering/lossless-claw/src/db/migration.js";

// ── Config ──────────────────────────────────────────────────────────────────

const LCM_PORT = parseInt(process.env.LCM_PORT || "8092", 10);
const LCM_DATABASE_PATH = process.env.LCM_DATABASE_PATH || "/data/lcm.db";
const SIDECAR_URL = process.env.SIDECAR_URL || "http://localhost:8091";

const CONTEXT_THRESHOLD = parseFloat(process.env.LCM_CONTEXT_THRESHOLD || "0.75");
const FRESH_TAIL_COUNT = parseInt(process.env.LCM_FRESH_TAIL_COUNT || "32", 10);

// ── Database ────────────────────────────────────────────────────────────────

console.log(`[lcm] Opening database at ${LCM_DATABASE_PATH}`);
const db = new DatabaseSync(LCM_DATABASE_PATH);
db.exec("PRAGMA journal_mode=WAL");
db.exec("PRAGMA foreign_keys=ON");

// Run LCM schema migrations
runLcmMigrations(db);
console.log("[lcm] Migrations complete");

// ── Stores & Engines ────────────────────────────────────────────────────────

const conversationStore = new ConversationStore(db);
const summaryStore = new SummaryStore(db);

const compactionConfig: CompactionConfig = {
  contextThreshold: CONTEXT_THRESHOLD,
  freshTailCount: FRESH_TAIL_COUNT,
  leafMinFanout: 8,
  condensedMinFanout: 4,
  condensedMinFanoutHard: 3,
  incrementalMaxDepth: 0,
  leafChunkTokens: 20_000,
  leafTargetTokens: 1200,
  condensedTargetTokens: 2000,
  maxRounds: 10,
};

const compactionEngine = new CompactionEngine(conversationStore, summaryStore, compactionConfig);
const assembler = new ContextAssembler(conversationStore, summaryStore);

// ── LLM Callback ────────────────────────────────────────────────────────────

/** Calls the Go sidecar's LLM proxy for summarization. */
async function summarize(
  text: string,
  aggressive?: boolean,
  options?: { isCondensed?: boolean; depth?: number },
): Promise<string> {
  const maxTokens = options?.isCondensed ? 2000 : 1200;
  const systemPrompt = aggressive
    ? "You are a context compaction engine. Aggressively compress the following into a concise summary preserving only the most critical information."
    : "You are a context compaction engine. Summarize the following conversation, preserving key facts, decisions, and context.";

  const res = await fetch(`${SIDECAR_URL}/internal/llm/complete`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      messages: [
        { role: "system", content: systemPrompt },
        { role: "user", content: text },
      ],
      max_tokens: maxTokens,
    }),
  });

  if (!res.ok) {
    const errText = await res.text();
    throw new Error(`LLM call failed (${res.status}): ${errText}`);
  }

  const data = (await res.json()) as { content?: string; choices?: Array<{ message?: { content?: string } }> };

  // Handle both direct content and OpenAI-style response formats
  if (data.content) return data.content;
  if (data.choices?.[0]?.message?.content) return data.choices[0].message.content;
  throw new Error("LLM response missing content");
}

// ── Express App ─────────────────────────────────────────────────────────────

const app = express();
app.use(express.json({ limit: "10mb" }));

// Health
app.get("/health", (_req, res) => {
  const convCount = db.prepare("SELECT COUNT(*) as count FROM conversations").get() as { count: number };
  const msgCount = db.prepare("SELECT COUNT(*) as count FROM messages").get() as { count: number };
  const sumCount = db.prepare("SELECT COUNT(*) as count FROM summaries").get() as { count: number };
  res.json({
    status: "healthy",
    engine: "lcm",
    version: "0.5.2",
    conversations: convCount.count,
    messages: msgCount.count,
    summaries: sumCount.count,
  });
});

// ── POST /ingest — Store messages into immutable store ──────────────────────

app.post("/ingest", async (req, res) => {
  try {
    const { session_id, messages } = req.body as {
      session_id: string;
      messages: Array<{ role: string; content: string }>;
    };

    if (!session_id || !messages?.length) {
      res.status(400).json({ error: "session_id and messages are required" });
      return;
    }

    // Get or create conversation for this session
    const conversation = await conversationStore.getOrCreateConversation(session_id, session_id);
    const conversationId = conversation.conversationId;

    // Get current max sequence number
    const maxSeq = await conversationStore.getMaxSeq(conversationId);
    let seq = maxSeq + 1;

    const stored: Array<{ message_id: number; seq: number; role: string }> = [];

    for (const msg of messages) {
      const tokenCount = Math.ceil(msg.content.length / 4); // ~4 chars per token
      const record = await conversationStore.createMessage({
        conversationId,
        seq,
        role: msg.role as "user" | "assistant" | "system" | "tool",
        content: msg.content,
        tokenCount,
      });
      stored.push({ message_id: record.messageId, seq, role: msg.role });
      seq++;
    }

    // Update context items for the new messages
    await summaryStore.appendContextMessages(
      conversationId,
      stored.map((m) => m.message_id),
    );

    res.json({
      conversation_id: conversationId,
      session_id,
      messages_stored: stored.length,
      messages: stored,
    });
  } catch (err: any) {
    console.error("[lcm] ingest error:", err);
    res.status(500).json({ error: err.message });
  }
});

// ── POST /assemble — Build active context (recent + summaries) ──────────────

app.post("/assemble", async (req, res) => {
  try {
    const { session_id, max_tokens = 100_000 } = req.body as {
      session_id: string;
      max_tokens?: number;
    };

    if (!session_id) {
      res.status(400).json({ error: "session_id is required" });
      return;
    }

    const conversation = await conversationStore.getConversationBySessionId(session_id);
    if (!conversation) {
      res.json({ messages: [], estimated_tokens: 0, stats: { raw: 0, summaries: 0, total: 0 } });
      return;
    }

    const result = await assembler.assemble({
      conversationId: conversation.conversationId,
      tokenBudget: max_tokens,
      freshTailCount: FRESH_TAIL_COUNT,
    });

    res.json({
      messages: result.messages,
      estimated_tokens: result.estimatedTokens,
      system_prompt_addition: result.systemPromptAddition || null,
      stats: {
        raw: result.stats.rawMessageCount,
        summaries: result.stats.summaryCount,
        total: result.stats.totalContextItems,
      },
    });
  } catch (err: any) {
    console.error("[lcm] assemble error:", err);
    res.status(500).json({ error: err.message });
  }
});

// ── POST /compact — Trigger DAG compaction ──────────────────────────────────

app.post("/compact", async (req, res) => {
  try {
    const { session_id, token_budget = 100_000, force = false } = req.body as {
      session_id: string;
      token_budget?: number;
      force?: boolean;
    };

    if (!session_id) {
      res.status(400).json({ error: "session_id is required" });
      return;
    }

    const conversation = await conversationStore.getConversationBySessionId(session_id);
    if (!conversation) {
      res.status(404).json({ error: "session not found" });
      return;
    }

    const result = await compactionEngine.compact({
      conversationId: conversation.conversationId,
      tokenBudget: token_budget,
      summarize,
      force,
    });

    res.json({
      compacted: result.actionTaken,
      tokens_before: result.tokensBefore,
      tokens_after: result.tokensAfter,
      condensed: result.condensed,
      summary_id: result.createdSummaryId || null,
      level: result.level || null,
    });
  } catch (err: any) {
    console.error("[lcm] compact error:", err);
    res.status(500).json({ error: err.message });
  }
});

// ── POST /grep — Full-text search across messages and summaries ─────────────

app.post("/grep", async (req, res) => {
  try {
    const { query, session_id, limit = 20 } = req.body as {
      query: string;
      session_id?: string;
      limit?: number;
    };

    if (!query) {
      res.status(400).json({ error: "query is required" });
      return;
    }

    let conversationId: number | undefined;
    if (session_id) {
      const conv = await conversationStore.getConversationBySessionId(session_id);
      if (conv) conversationId = conv.conversationId;
    }

    const results = await conversationStore.searchMessages({
      conversationId,
      query,
      mode: "full_text",
      limit,
    });

    res.json({ results });
  } catch (err: any) {
    console.error("[lcm] grep error:", err);
    res.status(500).json({ error: err.message });
  }
});

// ── POST /expand — Expand a summary to see source messages ──────────────────

app.post("/expand", async (req, res) => {
  try {
    const { summary_id } = req.body as { summary_id: string };

    if (!summary_id) {
      res.status(400).json({ error: "summary_id is required" });
      return;
    }

    // Get the summary record
    const summary = await summaryStore.getSummary(summary_id);
    if (!summary) {
      res.status(404).json({ error: "summary not found" });
      return;
    }

    // Get the source messages that were compressed into this summary
    const sourceMessageIds = await summaryStore.getSummaryMessages(summary_id);
    const messages: Array<{ message_id: number; role: string; content: string; seq: number }> = [];

    for (const msgId of sourceMessageIds) {
      const msg = await conversationStore.getMessageById(msgId);
      if (msg) {
        messages.push({
          message_id: msg.messageId,
          role: msg.role,
          content: msg.content,
          seq: msg.seq,
        });
      }
    }

    res.json({
      summary_id: summary.summaryId,
      kind: summary.kind,
      depth: summary.depth,
      content: summary.content,
      token_count: summary.tokenCount,
      source_messages: messages,
    });
  } catch (err: any) {
    console.error("[lcm] expand error:", err);
    res.status(500).json({ error: err.message });
  }
});

// ── GET /conversations — List all conversations ─────────────────────────────

app.get("/conversations", (_req, res) => {
  try {
    const rows = db
      .prepare(
        `SELECT c.conversation_id, c.session_id, c.title, c.created_at, c.updated_at,
                COUNT(m.message_id) as message_count,
                MAX(m.created_at) as last_message_at
         FROM conversations c
         LEFT JOIN messages m ON m.conversation_id = c.conversation_id
         GROUP BY c.conversation_id
         ORDER BY COALESCE(MAX(m.created_at), c.created_at) DESC
         LIMIT 200`,
      )
      .all() as Array<{
      conversation_id: number;
      session_id: string;
      title: string | null;
      created_at: string;
      updated_at: string;
      message_count: number;
      last_message_at: string | null;
    }>;

    res.json({
      conversations: rows.map((r) => ({
        id: r.conversation_id,
        session_id: r.session_id,
        title: r.title,
        message_count: r.message_count,
        last_message_at: r.last_message_at || r.created_at,
        created_at: r.created_at,
      })),
    });
  } catch (err: any) {
    console.error("[lcm] conversations list error:", err);
    res.status(500).json({ error: err.message });
  }
});

// ── GET /conversations/:id/messages — List messages for a conversation ──────

app.get("/conversations/:id/messages", (req, res) => {
  try {
    const conversationId = parseInt(req.params.id, 10);
    const limit = parseInt((req.query.limit as string) || "100", 10);
    const offset = parseInt((req.query.offset as string) || "0", 10);

    const rows = db
      .prepare(
        `SELECT message_id, seq, role, content, token_count, created_at
         FROM messages
         WHERE conversation_id = ?
         ORDER BY seq ASC
         LIMIT ? OFFSET ?`,
      )
      .all(conversationId, limit, offset) as Array<{
      message_id: number;
      seq: number;
      role: string;
      content: string;
      token_count: number;
      created_at: string;
    }>;

    const total = db
      .prepare("SELECT COUNT(*) as count FROM messages WHERE conversation_id = ?")
      .get(conversationId) as { count: number };

    res.json({
      messages: rows.map((r) => ({
        id: r.message_id,
        seq: r.seq,
        role: r.role,
        content: r.content,
        token_count: r.token_count,
        created_at: r.created_at,
      })),
      total: total.count,
    });
  } catch (err: any) {
    console.error("[lcm] conversation messages error:", err);
    res.status(500).json({ error: err.message });
  }
});

// ── Start ───────────────────────────────────────────────────────────────────

app.listen(LCM_PORT, () => {
  console.log(`[lcm] Server listening on port ${LCM_PORT}`);
});
