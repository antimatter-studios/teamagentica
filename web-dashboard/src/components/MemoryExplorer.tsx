import { useEffect, useState, useCallback, useRef } from "react";
import { useShallow } from "zustand/react/shallow";
import { useMemoryStore } from "../stores/memoryStore";
import type { Memory, LCMConversation, LCMMessage } from "@teamagentica/api-client";

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleString(undefined, {
      month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
    });
  } catch {
    return iso;
  }
}

function timeAgo(iso: string): string {
  try {
    const diff = Date.now() - new Date(iso).getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 60) return `${mins}m ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return `${hrs}h ago`;
    const days = Math.floor(hrs / 24);
    return `${days}d ago`;
  } catch {
    return "";
  }
}

function ScoreBadge({ score }: { score: number }) {
  if (!score || score === 0) return null;
  const pct = Math.round(score * 100);
  const color = pct >= 80 ? "var(--success)" : pct >= 50 ? "var(--warning)" : "var(--error)";
  return (
    <span className="mem-score" style={{ color, borderColor: color }}>
      {pct}%
    </span>
  );
}

function CategoryTags({ categories }: { categories: string[] | null }) {
  if (!categories || categories.length === 0) return null;
  return (
    <span className="mem-categories">
      {categories.map((c) => (
        <span key={c} className="mem-cat-tag">{c}</span>
      ))}
    </span>
  );
}

function MemoryCard({
  mem,
  onDelete,
  isSearchResult,
}: {
  mem: Memory;
  onDelete: (id: string) => void;
  isSearchResult: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const [confirming, setConfirming] = useState(false);

  const handleDelete = () => {
    if (!confirming) {
      setConfirming(true);
      return;
    }
    onDelete(mem.id);
    setConfirming(false);
  };

  const textLen = mem.memory?.length ?? 0;
  const isShort = textLen < 30;
  const isVague = /^(the user|user |they |it |this |that )/i.test(mem.memory ?? "");

  return (
    <div className={`mem-card ${isShort ? "mem-card-short" : ""} ${isVague ? "mem-card-vague" : ""}`}>
      <div className="mem-card-header">
        <span className="mem-card-time" title={mem.created_at}>
          {formatTime(mem.created_at)}
          <span className="mem-card-ago">{timeAgo(mem.created_at)}</span>
        </span>
        <div className="mem-card-actions">
          {isSearchResult && <ScoreBadge score={mem.score} />}
          <CategoryTags categories={mem.categories} />
          <button
            className={`mem-delete-btn ${confirming ? "mem-delete-confirm" : ""}`}
            onClick={handleDelete}
            onBlur={() => setConfirming(false)}
            title={confirming ? "Click again to confirm" : "Delete memory"}
          >
            {confirming ? "Confirm?" : "\u00d7"}
          </button>
        </div>
      </div>
      <div
        className={`mem-card-text ${expanded ? "expanded" : ""}`}
        onClick={() => setExpanded(!expanded)}
      >
        {mem.memory}
      </div>
      <div className="mem-card-meta">
        {mem.user_id && mem.user_id !== "global" && (
          <span className="mem-meta-tag">user:{mem.user_id}</span>
        )}
        {mem.agent_id && (
          <span className="mem-meta-tag">agent:{mem.agent_id}</span>
        )}
        {mem.run_id && (
          <span className="mem-meta-tag">run:{mem.run_id.slice(0, 8)}</span>
        )}
        {isShort && <span className="mem-quality-flag mem-flag-short">SHORT</span>}
        {isVague && <span className="mem-quality-flag mem-flag-vague">VAGUE</span>}
        {mem.immutable && <span className="mem-meta-tag mem-meta-immutable">immutable</span>}
        <span className="mem-card-id">{mem.id.slice(0, 8)}</span>
      </div>
    </div>
  );
}

// ── LCM Conversation Browser ──

function ConversationCard({
  conv,
  active,
  onClick,
}: {
  conv: LCMConversation;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <div
      className={`plugin-sidebar-item${active ? " active" : ""}`}
      onClick={onClick}
    >
      <span className="plugin-status-dot status-running" />
      <span className="plugin-sidebar-name" title={conv.session_id}>
        {conv.title || conv.session_id.slice(0, 24)}
      </span>
      <span className="plugin-sidebar-status status-running">{conv.message_count}</span>
    </div>
  );
}

function LCMMessageBubble({ msg }: { msg: LCMMessage }) {
  const [expanded, setExpanded] = useState(false);
  const isLong = msg.content.length > 300;

  return (
    <div className={`lcm-msg lcm-msg-${msg.role}`}>
      <div className="lcm-msg-header">
        <span className={`lcm-msg-role lcm-role-${msg.role}`}>{msg.role}</span>
        <span className="lcm-msg-meta">
          {msg.token_count}t
          {msg.created_at && (
            <span className="lcm-msg-time">{formatTime(msg.created_at)}</span>
          )}
        </span>
      </div>
      <div
        className={`lcm-msg-content ${expanded || !isLong ? "expanded" : ""}`}
        onClick={() => isLong && setExpanded(!expanded)}
      >
        {msg.content}
      </div>
      {isLong && !expanded && (
        <button className="lcm-msg-expand" onClick={() => setExpanded(true)}>
          Show more...
        </button>
      )}
    </div>
  );
}

function ConversationView() {
  const {
    conversationMessages, conversationTotal, loadingMessages, loadMoreMessages,
    selectedConversationId, conversations,
  } = useMemoryStore(useShallow((s) => ({
    conversationMessages: s.conversationMessages,
    conversationTotal: s.conversationTotal,
    loadingMessages: s.loadingMessages,
    loadMoreMessages: s.loadMoreMessages,
    selectedConversationId: s.selectedConversationId,
    conversations: s.conversations,
  })));

  const conv = conversations.find((c) => c.id === selectedConversationId);
  const hasMore = conversationMessages.length < conversationTotal;

  if (!selectedConversationId) {
    return (
      <div className="plugin-detail-empty">
        <div className="plugin-empty-icon">[C]</div>
        <p>Select a conversation from the sidebar to browse messages.</p>
      </div>
    );
  }

  return (
    <div className="lcm-conversation">
      <div className="lcm-conv-header">
        <span className="lcm-conv-title">{conv?.title || conv?.session_id || "Conversation"}</span>
        <span className="lcm-conv-stats">
          {conversationTotal} messages
          {conv?.last_message_at && (
            <span className="lcm-conv-time"> &middot; {timeAgo(conv.last_message_at)}</span>
          )}
        </span>
      </div>

      {loadingMessages && conversationMessages.length === 0 ? (
        <div className="plugin-loading">
          <div className="spinner" />
          <p>Loading messages...</p>
        </div>
      ) : conversationMessages.length === 0 ? (
        <div className="plugin-detail-empty">
          <p>No messages in this conversation.</p>
        </div>
      ) : (
        <div className="lcm-messages">
          {conversationMessages.map((msg) => (
            <LCMMessageBubble key={msg.id} msg={msg} />
          ))}
          {hasMore && (
            <button
              className="lcm-load-more"
              onClick={loadMoreMessages}
              disabled={loadingMessages}
            >
              {loadingMessages ? "Loading..." : `Load more (${conversationTotal - conversationMessages.length} remaining)`}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// ── Main Component ──

type SidebarView = "all" | "user" | "agent" | "conversations";

export default function MemoryExplorer() {
  const {
    memories, entities, searchResults, loading, searching, error,
    // @ts-ignore — WIP: these will be wired up
    selectedUserId, selectedAgentId,
    fetch, fetchEntities, search, clearSearch, deleteMemory, setFilter,
    conversations, loadingConversations, selectedConversationId,
    fetchConversations, selectConversation,
  } = useMemoryStore(useShallow((s) => s));

  const [searchQuery, setSearchQuery] = useState("");
  const [sidebarView, setSidebarView] = useState<SidebarView>("all");
  const [selectedEntity, setSelectedEntity] = useState<string | null>(null);
  const searchTimer = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => {
    fetch();
    fetchEntities();
    fetchConversations();
  }, []);

  const handleSearch = useCallback((q: string) => {
    setSearchQuery(q);
    clearTimeout(searchTimer.current);
    if (!q.trim()) {
      clearSearch();
      return;
    }
    searchTimer.current = setTimeout(() => search(q), 400);
  }, [search, clearSearch]);

  const selectEntity = useCallback((type: SidebarView, id: string | null) => {
    setSidebarView(type);
    setSelectedEntity(id);
    if (type === "conversations") {
      // Don't change mem0 filters
      return;
    }
    // Clear conversation selection when switching to semantic views
    if (selectedConversationId !== null) {
      selectConversation(null);
    }
    if (type === "all") {
      setFilter("selectedUserId", "");
      setFilter("selectedAgentId", "");
    } else if (type === "user") {
      setFilter("selectedUserId", id ?? "");
      setFilter("selectedAgentId", "");
    } else if (type === "agent") {
      setFilter("selectedAgentId", id ?? "");
      setFilter("selectedUserId", "");
    }
  }, [setFilter, selectConversation, selectedConversationId]);

  const handleSelectConversation = useCallback((convId: number) => {
    setSidebarView("conversations");
    selectConversation(convId);
  }, [selectConversation]);

  const isSearch = searchResults !== null;
  const displayList = isSearch
    ? searchResults
    : [...memories].sort((a, b) => {
        const ta = new Date(a.created_at).getTime();
        const tb = new Date(b.created_at).getTime();
        const validA = a.created_at && !isNaN(ta);
        const validB = b.created_at && !isNaN(tb);
        if (validA && !validB) return -1;
        if (!validA && validB) return 1;
        if (!validA && !validB) return 0;
        return tb - ta;
      });

  // Compute quality stats
  const totalCount = memories.length;
  const shortCount = memories.filter((m) => (m.memory?.length ?? 0) < 30).length;
  const vagueCount = memories.filter((m) => /^(the user|user |they |it |this |that )/i.test(m.memory ?? "")).length;
  const withCategories = memories.filter((m) => m.categories && m.categories.length > 0).length;
  const qualityScore = totalCount > 0
    ? Math.round(((totalCount - shortCount - vagueCount) / totalCount) * 100)
    : 0;

  // Group entities for sidebar
  const userEntities = entities.filter((e) => e.type === "user");
  const agentEntities = entities.filter((e) => e.type === "agent");
  const runEntities = entities.filter((e) => e.type === "run");

  const showConversationView = sidebarView === "conversations";

  if (loading && memories.length === 0) {
    return (
      <div className="plugin-loading">
        <div className="spinner large" />
        <p>LOADING MEMORIES...</p>
      </div>
    );
  }

  return (
    <div className="plugin-layout">
      {/* ── Left sidebar ── */}
      <aside className="plugin-sidebar">
        <div className="plugin-sidebar-header">
          <span className="section-icon">[M]</span>
          MEMORY
          {totalCount > 0 && (
            <span className="section-count">{totalCount}</span>
          )}
        </div>

        {/* Quality stats in sidebar */}
        <div className="mem-sidebar-stats">
          <div className={`mem-sidebar-stat-row ${qualityScore >= 70 ? "mem-stat-good" : qualityScore >= 40 ? "mem-stat-warn" : "mem-stat-bad"}`}>
            <span className="mem-sidebar-stat-label">Quality</span>
            <span className="mem-sidebar-stat-value">{qualityScore}%</span>
          </div>
          <div className="mem-sidebar-stat-row">
            <span className="mem-sidebar-stat-label">Short</span>
            <span className="mem-sidebar-stat-value">{shortCount}</span>
          </div>
          <div className="mem-sidebar-stat-row">
            <span className="mem-sidebar-stat-label">Vague</span>
            <span className="mem-sidebar-stat-value">{vagueCount}</span>
          </div>
          <div className="mem-sidebar-stat-row">
            <span className="mem-sidebar-stat-label">Categorized</span>
            <span className="mem-sidebar-stat-value">{withCategories}</span>
          </div>
        </div>

        <nav className="plugin-sidebar-list">
          {/* Semantic memories */}
          <div className="plugin-sidebar-group">
            <div className="plugin-sidebar-group-header">
              <span className="plugin-sidebar-group-name">Semantic (Mem0)</span>
            </div>
            <div
              className={`plugin-sidebar-item${sidebarView === "all" ? " active" : ""}`}
              onClick={() => selectEntity("all", null)}
            >
              <span className="plugin-status-dot status-running" />
              <span className="plugin-sidebar-name">All Memories</span>
              <span className="plugin-sidebar-status status-running">{totalCount}</span>
            </div>
          </div>

          {/* Episodic conversations */}
          <div className="plugin-sidebar-group">
            <div className="plugin-sidebar-group-header">
              <span className="plugin-sidebar-group-name">Episodic (LCM)</span>
              {conversations.length > 0 && (
                <span className="marketplace-sidebar-count">{conversations.length}</span>
              )}
            </div>
            {loadingConversations ? (
              <div className="plugin-sidebar-item">
                <span className="spinner small" />
                <span className="plugin-sidebar-name">Loading...</span>
              </div>
            ) : conversations.length === 0 ? (
              <div className="plugin-sidebar-item">
                <span className="plugin-status-dot status-stopped" />
                <span className="plugin-sidebar-name" style={{ opacity: 0.5 }}>No conversations</span>
              </div>
            ) : (
              conversations.map((conv) => (
                <ConversationCard
                  key={conv.id}
                  conv={conv}
                  active={sidebarView === "conversations" && selectedConversationId === conv.id}
                  onClick={() => handleSelectConversation(conv.id)}
                />
              ))
            )}
          </div>

          {/* Users */}
          {userEntities.length > 0 && (
            <div className="plugin-sidebar-group">
              <div className="plugin-sidebar-group-header">
                <span className="plugin-sidebar-group-name">Users</span>
                <span className="marketplace-sidebar-count">{userEntities.length}</span>
              </div>
              {userEntities.map((e) => (
                <div
                  key={`user-${e.id}`}
                  className={`plugin-sidebar-item${sidebarView === "user" && selectedEntity === e.id ? " active" : ""}`}
                  onClick={() => selectEntity("user", e.id)}
                >
                  <span className="plugin-status-dot status-running" />
                  <span className="plugin-sidebar-name">{e.id}</span>
                </div>
              ))}
            </div>
          )}

          {/* Agents */}
          {agentEntities.length > 0 && (
            <div className="plugin-sidebar-group">
              <div className="plugin-sidebar-group-header">
                <span className="plugin-sidebar-group-name">Agents</span>
                <span className="marketplace-sidebar-count">{agentEntities.length}</span>
              </div>
              {agentEntities.map((e) => (
                <div
                  key={`agent-${e.id}`}
                  className={`plugin-sidebar-item${sidebarView === "agent" && selectedEntity === e.id ? " active" : ""}`}
                  onClick={() => selectEntity("agent", e.id)}
                >
                  <span className="plugin-status-dot status-running" />
                  <span className="plugin-sidebar-name">{e.id}</span>
                </div>
              ))}
            </div>
          )}

          {/* Runs */}
          {runEntities.length > 0 && (
            <div className="plugin-sidebar-group">
              <div className="plugin-sidebar-group-header">
                <span className="plugin-sidebar-group-name">Runs</span>
                <span className="marketplace-sidebar-count">{runEntities.length}</span>
              </div>
              {runEntities.map((e) => (
                <div
                  key={`run-${e.id}`}
                  className="plugin-sidebar-item"
                >
                  <span className="plugin-status-dot status-stopped" />
                  <span className="plugin-sidebar-name">{e.id.slice(0, 12)}</span>
                </div>
              ))}
            </div>
          )}
        </nav>
      </aside>

      {/* ── Right content ── */}
      <main className="plugin-detail mem-detail">
        {error && <div className="form-error">{error}</div>}

        {showConversationView ? (
          <ConversationView />
        ) : (
          <>
            <div className="mem-toolbar">
              <div className="mem-search-wrap">
                <input
                  className="mem-search-input"
                  type="text"
                  placeholder="Semantic search — test what the agent would retrieve..."
                  value={searchQuery}
                  onChange={(e) => handleSearch(e.target.value)}
                />
                {searching && <span className="mem-search-spinner" />}
                {isSearch && (
                  <button className="mem-search-clear" onClick={() => { setSearchQuery(""); clearSearch(); }}>
                    Clear
                  </button>
                )}
              </div>
            </div>

            {isSearch && (
              <div className="mem-search-info">
                Showing {displayList.length} results for &ldquo;{searchQuery}&rdquo;
                {displayList.length > 0 && (
                  <span className="mem-search-score-range">
                    {" "}&mdash; scores: {Math.round((displayList[displayList.length - 1]?.score ?? 0) * 100)}%
                    {" "}to {Math.round((displayList[0]?.score ?? 0) * 100)}%
                  </span>
                )}
              </div>
            )}

            {displayList.length === 0 ? (
              <div className="plugin-detail-empty">
                <div className="plugin-empty-icon">[~]</div>
                <p>{isSearch ? "No memories matched your search." : "No memories stored yet."}</p>
              </div>
            ) : (
              <div className="mem-list">
                {displayList.map((mem) => (
                  <MemoryCard
                    key={mem.id}
                    mem={mem}
                    onDelete={deleteMemory}
                    isSearchResult={isSearch}
                  />
                ))}
              </div>
            )}
          </>
        )}
      </main>
    </div>
  );
}
