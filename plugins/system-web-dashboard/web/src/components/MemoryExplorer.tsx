import { useEffect, useState, useCallback, useRef } from "react";
import { useShallow } from "zustand/react/shallow";
import { Brain, Loader2, Search, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { cn } from "@/lib/utils";
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
  const variant: "default" | "secondary" | "destructive" =
    pct >= 80 ? "default" : pct >= 50 ? "secondary" : "destructive";
  return <Badge variant={variant}>{pct}%</Badge>;
}

function CategoryTags({ categories }: { categories: string[] | null }) {
  if (!categories || categories.length === 0) return null;
  return (
    <span className="flex flex-wrap gap-1">
      {categories.map((c) => (
        <Badge key={c} variant="outline" className="text-[10px]">{c}</Badge>
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
    <Card
      className={cn(
        "p-3",
        isShort && "border-amber-500/40",
        isVague && "border-destructive/40"
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="flex items-baseline gap-2 text-xs text-muted-foreground" title={mem.created_at}>
          {formatTime(mem.created_at)}
          <span className="text-[10px]">{timeAgo(mem.created_at)}</span>
        </span>
        <div className="flex items-center gap-2">
          {isSearchResult && <ScoreBadge score={mem.score} />}
          <CategoryTags categories={mem.categories} />
          <Button
            size="sm"
            variant={confirming ? "destructive" : "ghost"}
            onClick={handleDelete}
            onBlur={() => setConfirming(false)}
            title={confirming ? "Click again to confirm" : "Delete memory"}
            className="h-7"
          >
            {confirming ? "Confirm?" : <X className="h-3.5 w-3.5" />}
          </Button>
        </div>
      </div>
      <div
        className={cn(
          "mt-2 cursor-pointer text-sm",
          !expanded && "line-clamp-3"
        )}
        onClick={() => setExpanded(!expanded)}
      >
        {mem.memory}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-1 text-[10px]">
        {mem.user_id && mem.user_id !== "global" && (
          <Badge variant="outline">user:{mem.user_id}</Badge>
        )}
        {mem.agent_id && <Badge variant="outline">agent:{mem.agent_id}</Badge>}
        {mem.run_id && <Badge variant="outline">run:{mem.run_id.slice(0, 8)}</Badge>}
        {isShort && <Badge className="bg-amber-500 text-white">SHORT</Badge>}
        {isVague && <Badge variant="destructive">VAGUE</Badge>}
        {mem.immutable && <Badge variant="secondary">immutable</Badge>}
        <span className="ml-auto font-mono text-muted-foreground">{mem.id.slice(0, 8)}</span>
      </div>
    </Card>
  );
}

const sidebarItem =
  "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-accent hover:text-accent-foreground cursor-pointer";
const sidebarActive = "bg-accent text-accent-foreground";

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
    <div className={cn(sidebarItem, active && sidebarActive)} onClick={onClick}>
      <span className="h-2 w-2 shrink-0 rounded-full bg-emerald-500" />
      <span className="flex-1 truncate" title={conv.session_id}>
        {conv.title || conv.session_id.slice(0, 24)}
      </span>
      <Badge variant="secondary" className="text-[10px]">{conv.message_count}</Badge>
    </div>
  );
}

function LCMMessageBubble({ msg }: { msg: LCMMessage }) {
  const [expanded, setExpanded] = useState(false);
  const isLong = msg.content.length > 300;

  const roleClasses: Record<string, string> = {
    user: "bg-primary/10 border-primary/30",
    assistant: "bg-muted",
    system: "bg-amber-500/10 border-amber-500/30",
  };

  return (
    <Card className={cn("p-3", roleClasses[msg.role])}>
      <div className="flex items-center justify-between gap-2 text-xs">
        <Badge variant="outline" className="uppercase">{msg.role}</Badge>
        <span className="text-muted-foreground">
          {msg.token_count}t
          {msg.created_at && <span className="ml-2">{formatTime(msg.created_at)}</span>}
        </span>
      </div>
      <div
        className={cn(
          "mt-2 whitespace-pre-wrap text-sm",
          isLong && !expanded && "line-clamp-6 cursor-pointer"
        )}
        onClick={() => isLong && setExpanded(!expanded)}
      >
        {msg.content}
      </div>
      {isLong && !expanded && (
        <Button variant="link" size="sm" className="mt-1 h-auto p-0" onClick={() => setExpanded(true)}>
          Show more...
        </Button>
      )}
    </Card>
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
      <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center text-muted-foreground">
        <Brain className="h-8 w-8" />
        <p>Select a conversation from the sidebar to browse messages.</p>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b p-3">
        <span className="text-sm font-semibold">{conv?.title || conv?.session_id || "Conversation"}</span>
        <span className="text-xs text-muted-foreground">
          {conversationTotal} messages
          {conv?.last_message_at && <span> · {timeAgo(conv.last_message_at)}</span>}
        </span>
      </div>

      {loadingMessages && conversationMessages.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 text-muted-foreground">
          <Loader2 className="h-5 w-5 animate-spin" />
          <p className="text-sm">Loading messages...</p>
        </div>
      ) : conversationMessages.length === 0 ? (
        <div className="flex flex-1 items-center justify-center p-8 text-sm text-muted-foreground">
          No messages in this conversation.
        </div>
      ) : (
        <ScrollArea className="flex-1">
          <div className="flex flex-col gap-3 p-3">
            {conversationMessages.map((msg) => (
              <LCMMessageBubble key={msg.id} msg={msg} />
            ))}
            {hasMore && (
              <Button variant="outline" onClick={loadMoreMessages} disabled={loadingMessages}>
                {loadingMessages ? "Loading..." : `Load more (${conversationTotal - conversationMessages.length} remaining)`}
              </Button>
            )}
          </div>
        </ScrollArea>
      )}
    </div>
  );
}

type SidebarView = "all" | "user" | "agent" | "conversations";

export default function MemoryExplorer() {
  const {
    memories, memoryTotal, entities, searchResults, loading, loadingMore, searching, error,
    // @ts-ignore — WIP: these will be wired up
    selectedUserId, selectedAgentId,
    fetch, loadMore, fetchEntities, search, clearSearch, deleteMemory, setFilter,
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
    if (type === "conversations") return;
    if (selectedConversationId !== null) selectConversation(null);
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

  const totalCount = memoryTotal || memories.length;
  const hasMoreMemories = memories.length < totalCount;
  const shortCount = memories.filter((m) => (m.memory?.length ?? 0) < 30).length;
  const vagueCount = memories.filter((m) => /^(the user|user |they |it |this |that )/i.test(m.memory ?? "")).length;
  const withCategories = memories.filter((m) => m.categories && m.categories.length > 0).length;
  const qualityScore = totalCount > 0
    ? Math.round(((totalCount - shortCount - vagueCount) / totalCount) * 100)
    : 0;

  const userEntities = entities.filter((e) => e.type === "user");
  const agentEntities = entities.filter((e) => e.type === "agent");
  const runEntities = entities.filter((e) => e.type === "run");

  const showConversationView = sidebarView === "conversations";

  if (loading && memories.length === 0) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
        <Loader2 className="h-6 w-6 animate-spin" />
        <p className="text-sm">Loading memories...</p>
      </div>
    );
  }

  const qualityTone =
    qualityScore >= 70 ? "text-emerald-500" : qualityScore >= 40 ? "text-amber-500" : "text-destructive";

  return (
    <div className="flex h-full w-full">
      {/* Sidebar */}
      <aside className="flex w-72 shrink-0 flex-col border-r">
        <div className="flex items-center gap-2 border-b p-3">
          <Brain className="h-4 w-4" />
          <span className="text-sm font-semibold uppercase tracking-wide">Memory</span>
          {totalCount > 0 && (
            <Badge variant="secondary" className="ml-auto">
              {hasMoreMemories ? `${memories.length}/${totalCount}` : totalCount}
            </Badge>
          )}
        </div>

        {/* Quality stats */}
        <div className="grid grid-cols-2 gap-2 border-b p-3 text-xs">
          <div className="flex items-center justify-between rounded-md border px-2 py-1">
            <span className="text-muted-foreground">Quality</span>
            <span className={cn("font-semibold", qualityTone)}>{qualityScore}%</span>
          </div>
          <div className="flex items-center justify-between rounded-md border px-2 py-1">
            <span className="text-muted-foreground">Short</span>
            <span className="font-semibold">{shortCount}</span>
          </div>
          <div className="flex items-center justify-between rounded-md border px-2 py-1">
            <span className="text-muted-foreground">Vague</span>
            <span className="font-semibold">{vagueCount}</span>
          </div>
          <div className="flex items-center justify-between rounded-md border px-2 py-1">
            <span className="text-muted-foreground">Categorized</span>
            <span className="font-semibold">{withCategories}</span>
          </div>
        </div>

        <ScrollArea className="flex-1">
          <nav className="flex flex-col gap-3 p-2">
            {/* Semantic */}
            <div className="flex flex-col gap-1">
              <div className="px-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                Semantic (Mem0)
              </div>
              <div
                className={cn(sidebarItem, sidebarView === "all" && sidebarActive)}
                onClick={() => selectEntity("all", null)}
              >
                <span className="h-2 w-2 shrink-0 rounded-full bg-emerald-500" />
                <span className="flex-1 truncate">All Memories</span>
                <Badge variant="secondary" className="text-[10px]">
                  {hasMoreMemories ? `${memories.length}/${totalCount}` : totalCount}
                </Badge>
              </div>
            </div>

            {/* Episodic */}
            <div className="flex flex-col gap-1">
              <div className="flex items-center justify-between px-2">
                <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                  Episodic (LCM)
                </span>
                {conversations.length > 0 && (
                  <Badge variant="outline" className="text-[10px]">{conversations.length}</Badge>
                )}
              </div>
              {loadingConversations ? (
                <div className={cn(sidebarItem, "text-muted-foreground")}>
                  <Loader2 className="h-3 w-3 animate-spin" />
                  <span>Loading...</span>
                </div>
              ) : conversations.length === 0 ? (
                <div className={cn(sidebarItem, "text-muted-foreground opacity-60")}>
                  <span className="h-2 w-2 shrink-0 rounded-full bg-muted-foreground" />
                  <span>No conversations</span>
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
              <SidebarGroup label="Users" count={userEntities.length}>
                {userEntities.map((e) => (
                  <div
                    key={`user-${e.id}`}
                    className={cn(sidebarItem, sidebarView === "user" && selectedEntity === e.id && sidebarActive)}
                    onClick={() => selectEntity("user", e.id)}
                  >
                    <span className="h-2 w-2 shrink-0 rounded-full bg-emerald-500" />
                    <span className="flex-1 truncate">{e.id}</span>
                  </div>
                ))}
              </SidebarGroup>
            )}

            {/* Agents */}
            {agentEntities.length > 0 && (
              <SidebarGroup label="Agents" count={agentEntities.length}>
                {agentEntities.map((e) => (
                  <div
                    key={`agent-${e.id}`}
                    className={cn(sidebarItem, sidebarView === "agent" && selectedEntity === e.id && sidebarActive)}
                    onClick={() => selectEntity("agent", e.id)}
                  >
                    <span className="h-2 w-2 shrink-0 rounded-full bg-emerald-500" />
                    <span className="flex-1 truncate">{e.id}</span>
                  </div>
                ))}
              </SidebarGroup>
            )}

            {/* Runs */}
            {runEntities.length > 0 && (
              <SidebarGroup label="Runs" count={runEntities.length}>
                {runEntities.map((e) => (
                  <div key={`run-${e.id}`} className={sidebarItem}>
                    <span className="h-2 w-2 shrink-0 rounded-full bg-muted-foreground" />
                    <span className="flex-1 truncate">{e.id.slice(0, 12)}</span>
                  </div>
                ))}
              </SidebarGroup>
            )}
          </nav>
        </ScrollArea>
      </aside>

      {/* Main content */}
      <main className="flex flex-1 flex-col overflow-hidden">
        {error && (
          <Alert variant="destructive" className="m-3">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {showConversationView ? (
          <ConversationView />
        ) : (
          <>
            <div className="border-b p-3">
              <div className="relative">
                <Search className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input
                  className="pl-8 pr-20"
                  type="text"
                  placeholder="Semantic search — test what the agent would retrieve..."
                  value={searchQuery}
                  onChange={(e) => handleSearch(e.target.value)}
                />
                {searching && (
                  <Loader2 className="absolute right-16 top-2.5 h-4 w-4 animate-spin text-muted-foreground" />
                )}
                {isSearch && (
                  <Button
                    size="sm"
                    variant="ghost"
                    className="absolute right-1 top-1 h-7"
                    onClick={() => { setSearchQuery(""); clearSearch(); }}
                  >
                    Clear
                  </Button>
                )}
              </div>
            </div>

            {isSearch && (
              <div className="border-b bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
                Showing {displayList.length} results for &ldquo;{searchQuery}&rdquo;
                {displayList.length > 0 && (
                  <span>
                    {" "}— scores: {Math.round((displayList[displayList.length - 1]?.score ?? 0) * 100)}%
                    {" "}to {Math.round((displayList[0]?.score ?? 0) * 100)}%
                  </span>
                )}
              </div>
            )}

            <ScrollArea className="flex-1">
              {displayList.length === 0 ? (
                <div className="flex flex-col items-center justify-center gap-2 p-12 text-center text-muted-foreground">
                  <Brain className="h-8 w-8" />
                  <p>{isSearch ? "No memories matched your search." : "No memories stored yet."}</p>
                </div>
              ) : (
                <div className="flex flex-col gap-3 p-3">
                  {displayList.map((mem) => (
                    <MemoryCard
                      key={mem.id}
                      mem={mem}
                      onDelete={deleteMemory}
                      isSearchResult={isSearch}
                    />
                  ))}
                  {!isSearch && hasMoreMemories && (
                    <Button variant="outline" onClick={loadMore} disabled={loadingMore}>
                      {loadingMore ? "Loading..." : `Load more (${totalCount - memories.length} remaining)`}
                    </Button>
                  )}
                </div>
              )}
            </ScrollArea>
          </>
        )}
      </main>
    </div>
  );
}

function SidebarGroup({
  label,
  count,
  children,
}: {
  label: string;
  count: number;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between px-2">
        <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </span>
        <Badge variant="outline" className="text-[10px]">{count}</Badge>
      </div>
      {children}
    </div>
  );
}
