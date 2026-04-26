import { useEffect, useRef, useState, useCallback } from "react";
import { useShallow } from "zustand/react/shallow";
import { ArrowLeft, Check, Package, Paperclip, Plus, Send, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";
import { useChatStore } from "../stores/chatStore";
import { apiClient } from "../api/client";
import type { Attachment } from "@teamagentica/api-client";
import ConfirmDialog from "./ConfirmDialog";
import ChatAttachmentPreview from "./ChatAttachmentPreview";

function ElapsedTimer({ startedAt }: { startedAt: number }) {
  const [elapsed, setElapsed] = useState(() => Math.floor((Date.now() - startedAt) / 1000));
  useEffect(() => {
    const id = setInterval(() => {
      setElapsed(Math.floor((Date.now() - startedAt) / 1000));
    }, 1000);
    return () => clearInterval(id);
  }, [startedAt]);
  const mins = Math.floor(elapsed / 60);
  const secs = elapsed % 60;
  const display = mins > 0 ? `${mins}m ${secs}s` : `${secs}s`;
  return <span className="ml-2 font-mono text-xs text-muted-foreground">{display}</span>;
}

async function uploadFile(file: File): Promise<{ file_id: string; filename: string }> {
  const formData = new FormData();
  formData.append("file", file);
  return apiClient.chat.uploadFile(formData);
}

async function fetchFileBlob(fileIdOrKey: string): Promise<string> {
  const blob = await apiClient.chat.fetchFileBlob(fileIdOrKey);
  return URL.createObjectURL(blob);
}

function AuthImage({ fileKey, alt, className }: { fileKey: string; alt: string; className?: string }) {
  const [src, setSrc] = useState<string>("");
  useEffect(() => {
    let revoke = "";
    fetchFileBlob(fileKey).then((url) => {
      revoke = url;
      setSrc(url);
    }).catch(() => {});
    return () => { if (revoke) URL.revokeObjectURL(revoke); };
  }, [fileKey]);
  if (!src) return null;
  return <img src={src} alt={alt} className={className} onClick={() => window.open(src, "_blank")} />;
}

interface ChatProps {
  activePage: string;
  subpath: string;
  onConversationChange: (subpath: string) => void;
}

export default function Chat({ activePage, subpath, onConversationChange }: ChatProps) {
  const { conversations, activeConversationId, messages, sending, loading, error, shelvedTasks, progressInfo, inFlightTasks } = useChatStore(
    useShallow((s) => ({
      conversations: s.conversations,
      activeConversationId: s.activeConversationId,
      messages: s.messages,
      sending: s.sending,
      loading: s.loading,
      error: s.error,
      shelvedTasks: s.shelvedTasks,
      progressInfo: s.progressInfo,
      inFlightTasks: s.inFlightTasks,
    }))
  );

  const activeProgressList: ProgressInfo[] = activeConversationId ? progressInfo[activeConversationId] ?? [] : [];
  const activeInFlightList = activeConversationId ? inFlightTasks[activeConversationId] ?? [] : [];
  const loadConversations = useChatStore((s) => s.loadConversations);
  const selectConversation = useChatStore((s) => s.selectConversation);
  const newConversation = useChatStore((s) => s.newConversation);
  const removeConversation = useChatStore((s) => s.removeConversation);
  const send = useChatStore((s) => s.send);
  const shelfTask = useChatStore((s) => s.shelfTask);
  const revealShelved = useChatStore((s) => s.revealShelved);

  const [input, setInput] = useState("");
  const [pendingFiles, setPendingFiles] = useState<{ file_id: string; filename: string }[]>([]);
  const [uploading, setUploading] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [expandedAttachment, setExpandedAttachment] = useState<Attachment | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<{ id: number; title: string } | null>(null);
  const expandedAttachmentId = useRef<string>("");
  const dragCounter = useRef(0);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const isNearBottomRef = useRef(true);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleMessagesScroll = useCallback(() => {
    const el = messagesContainerRef.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    isNearBottomRef.current = distanceFromBottom < 500;
  }, []);

  useEffect(() => { loadConversations(); }, [loadConversations]);

  useEffect(() => {
    if (activePage !== "chat") return;
    const urlConvId = subpath ? Number(subpath) : null;
    if (urlConvId && !Number.isNaN(urlConvId) && urlConvId !== activeConversationId) {
      selectConversation(urlConvId);
    } else if (!subpath && activeConversationId) {
      onConversationChange(String(activeConversationId));
    }
  }, [activePage, subpath]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (activePage !== "chat") return;
    const urlConvId = subpath ? Number(subpath) : null;
    if (activeConversationId && activeConversationId !== urlConvId) {
      onConversationChange(String(activeConversationId));
    } else if (!activeConversationId && subpath) {
      onConversationChange("");
    }
  }, [activeConversationId]); // eslint-disable-line react-hooks/exhaustive-deps

  const prevMessageCountRef = useRef(0);

  useEffect(() => {
    const el = messagesContainerRef.current;
    const msgCount = messages.length;
    const prevCount = prevMessageCountRef.current;
    prevMessageCountRef.current = msgCount;

    if (prevCount === 0 && msgCount > 0) {
      messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
      return;
    }
    if (msgCount <= prevCount) return;
    if (el && !isNearBottomRef.current) return;
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const handleSend = async () => {
    const content = input.trim();
    if (!content && pendingFiles.length === 0) return;
    setInput("");
    const attachmentIds = pendingFiles.map((f) => f.file_id);
    setPendingFiles([]);
    isNearBottomRef.current = true;
    await send(content || "(attached files)", attachmentIds.length > 0 ? attachmentIds : undefined);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleFileUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files;
    if (!files || files.length === 0) return;
    setUploading(true);
    try {
      for (const file of Array.from(files)) {
        const result = await uploadFile(file);
        setPendingFiles((prev) => [...prev, result]);
      }
    } catch (err) {
      console.error("Upload failed:", err);
    }
    setUploading(false);
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const uploadFiles = useCallback(async (files: FileList | File[]) => {
    const imageFiles = Array.from(files).filter((f) => f.type.startsWith("image/"));
    if (imageFiles.length === 0) return;
    setUploading(true);
    try {
      for (const file of imageFiles) {
        const result = await uploadFile(file);
        setPendingFiles((prev) => [...prev, result]);
      }
    } catch (err) {
      console.error("Upload failed:", err);
    }
    setUploading(false);
  }, []);

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounter.current++;
    if (e.dataTransfer.types.includes("Files")) setDragOver(true);
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounter.current--;
    if (dragCounter.current === 0) setDragOver(false);
  }, []);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      e.stopPropagation();
      dragCounter.current = 0;
      setDragOver(false);
      if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
        uploadFiles(e.dataTransfer.files);
      }
    },
    [uploadFiles]
  );

  const parseAttachments = (attachmentsJson?: string): Attachment[] => {
    if (!attachmentsJson) return [];
    try { return JSON.parse(attachmentsJson); } catch { return []; }
  };

  return (
    <div className="flex h-full w-full">
      {/* Sidebar */}
      <aside className="flex w-64 shrink-0 flex-col border-r">
        <div className="p-2">
          <Button
            variant="default"
            className="w-full justify-start gap-2"
            onClick={() => { newConversation(); onConversationChange(""); }}
          >
            <Plus className="h-4 w-4" /> New chat
          </Button>
        </div>
        <Separator />
        <div className="flex-1 overflow-y-auto p-2">
          {conversations.map((conv) => {
            const active = conv.id === activeConversationId;
            return (
              <div
                key={conv.id}
                className={cn(
                  "group mb-1 flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent",
                  active && "bg-accent text-accent-foreground"
                )}
                onClick={() => selectConversation(conv.id)}
              >
                {(conv.unread_count ?? 0) > 0 && (
                  <Badge variant="default" className="h-5 px-1.5 text-[10px]" title={`${conv.unread_count} unread`}>
                    {conv.unread_count}
                  </Badge>
                )}
                <span className="flex-1 truncate">{conv.title}</span>
                <Button
                  size="icon"
                  variant="ghost"
                  className="h-6 w-6 opacity-0 group-hover:opacity-100"
                  onClick={(e) => { e.stopPropagation(); setDeleteConfirm({ id: conv.id, title: conv.title }); }}
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
            );
          })}
          {conversations.length === 0 && (
            <div className="p-4 text-center text-xs text-muted-foreground">No conversations yet</div>
          )}
        </div>

        {messages.length > 0 && (() => {
          const participants = new Map<string, { label: string; count: number }>();
          for (const msg of messages) {
            if (msg.role === "progress") continue;
            const key = msg.role === "user" ? "_you" : (msg.agent_alias || "agent");
            const existing = participants.get(key);
            if (existing) existing.count++;
            else participants.set(key, {
              label: msg.role === "user" ? "You" : `@${msg.agent_alias || "agent"}`,
              count: 1,
            });
          }
          const totalMessages = messages.filter((m) => m.role !== "progress").length;
          return (
            <div className="border-t p-2">
              <div className="mb-2 flex items-center justify-between px-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                <span>Participants</span>
                <span>{totalMessages} msg{totalMessages !== 1 ? "s" : ""}</span>
              </div>
              <div className="flex flex-col gap-1">
                {Array.from(participants.values()).map((p) => (
                  <div key={p.label} className="flex items-center justify-between rounded px-2 py-1 text-xs">
                    <span>{p.label}</span>
                    <Badge variant="outline" className="text-[10px]">{p.count}</Badge>
                  </div>
                ))}
              </div>
            </div>
          );
        })()}
      </aside>

      {/* Main */}
      <div
        className="relative flex flex-1 flex-col"
        onDragEnter={handleDragEnter}
        onDragLeave={handleDragLeave}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        {dragOver && (
          <div className="pointer-events-none absolute inset-0 z-50 flex items-center justify-center bg-primary/10 backdrop-blur-sm">
            <div className="rounded-lg border-2 border-dashed border-primary bg-background px-6 py-4 text-lg font-semibold">
              Drop images here
            </div>
          </div>
        )}
        {expandedAttachment ? (
          <div className="flex h-full flex-col">
            <div className="flex items-center gap-3 border-b px-3 py-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setExpandedAttachment(null);
                  const id = expandedAttachmentId.current;
                  if (id) {
                    requestAnimationFrame(() => {
                      document.getElementById(id)?.scrollIntoView({ block: "center" });
                    });
                  }
                }}
              >
                <ArrowLeft className="mr-1 h-4 w-4" /> Back
              </Button>
              <span className="text-sm font-medium">{expandedAttachment.filename}</span>
            </div>
            <div className="flex flex-1 items-center justify-center overflow-hidden bg-muted/20 p-4">
              <ChatAttachmentPreview attachment={expandedAttachment} />
            </div>
          </div>
        ) : (
          <>
            <div
              ref={messagesContainerRef}
              onScroll={handleMessagesScroll}
              className="flex-1 overflow-y-auto p-4"
            >
              {loading && (
                <div className="p-8 text-center text-sm text-muted-foreground">Loading messages...</div>
              )}
              <div className="flex w-full flex-col gap-4">
                {messages.filter((m) => m.role !== "progress").map((msg) => (
                  <Card
                    key={msg.id}
                    className={cn(
                      "p-3",
                      msg.role === "user" ? "bg-primary/5 border-primary/20" : "bg-muted/30"
                    )}
                  >
                    <div className="mb-1 flex items-baseline justify-between gap-2 text-xs">
                      {msg.role === "user" ? (
                        <span className="font-semibold">You</span>
                      ) : (
                        <span className="flex items-baseline gap-2">
                          <span className="font-semibold text-primary">@{msg.agent_alias || "agent"}</span>
                          {msg.model && <span className="text-muted-foreground">{msg.model}</span>}
                        </span>
                      )}
                      <span className="text-muted-foreground">
                        {new Date(msg.created_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
                        {" · "}
                        {new Date(msg.created_at).toLocaleDateString([], { day: "numeric", month: "short", year: "numeric" })}
                      </span>
                    </div>
                    <div className="whitespace-pre-wrap text-sm">{msg.content}</div>
                    <div className="mt-2 flex flex-wrap gap-2">
                      {parseAttachments(msg.attachments).map((att, idx) => {
                        const attId = att.storage_key || att.file_id || "";
                        return (
                          <div
                            key={attId || `att-${idx}`}
                            id={`chat-att-${attId || idx}`}
                            className="cursor-pointer"
                            onClick={() => {
                              expandedAttachmentId.current = `chat-att-${attId || idx}`;
                              setExpandedAttachment(att);
                            }}
                          >
                            <ChatAttachmentPreview attachment={att} compact />
                          </div>
                        );
                      })}
                    </div>
                    {msg.role === "assistant" && (msg.duration_ms || msg.input_tokens || msg.cost_usd) ? (
                      <div className="mt-2 text-[10px] text-muted-foreground">
                        {[
                          msg.model || undefined,
                          msg.duration_ms ? `${(msg.duration_ms / 1000).toFixed(1)}s` : undefined,
                          msg.input_tokens
                            ? `${msg.input_tokens}+${msg.output_tokens} tokens${msg.cached_tokens ? ` (${msg.cached_tokens} cached)` : ""}`
                            : undefined,
                          msg.cost_usd ? `$${msg.cost_usd.toFixed(4)}` : undefined,
                        ].filter(Boolean).join(" · ")}
                      </div>
                    ) : null}
                  </Card>
                ))}
                {activeProgressList.map((progress) => {
                  const inFlight = activeInFlightList.find((t) => t.taskGroupId === progress.taskGroupId);
                  return (
                    <Card key={progress.taskGroupId} className="border-amber-500/40 bg-amber-500/5 p-3">
                      <div className="flex items-center gap-2 text-sm">
                        <span className="flex-1">
                          {progress.message}
                          <span className="ml-2 font-mono text-xs text-muted-foreground">[task = {progress.taskGroupId}]</span>
                          {inFlight && <ElapsedTimer startedAt={inFlight.startedAt} />}
                        </span>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-7 w-7"
                          onClick={() => shelfTask(progress.taskGroupId)}
                          title="Move to shelf"
                        >
                          <Package className="h-4 w-4" />
                        </Button>
                      </div>
                    </Card>
                  );
                })}
                <div ref={messagesEndRef} />
              </div>
            </div>

            {error && (
              <Alert variant="destructive" className="mx-4 mb-2">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}

            {shelvedTasks.length > 0 && (
              <div className="border-t bg-muted/20 px-4 py-2">
                <div className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">Shelf</div>
                <div className="flex flex-wrap gap-2">
                  {shelvedTasks.map((t) => (
                    <Card
                      key={t.taskGroupId}
                      className={cn(
                        "flex flex-row items-center gap-2 p-2 text-xs",
                        t.status === "completed" && "border-emerald-500/40",
                        t.status === "failed" && "border-destructive/40"
                      )}
                    >
                      <span
                        className={cn(
                          "h-2 w-2 rounded-full",
                          t.status === "completed" ? "bg-emerald-500" : t.status === "failed" ? "bg-destructive" : "bg-amber-500 animate-pulse"
                        )}
                      />
                      <span className="max-w-[12rem] truncate">{t.message}</span>
                      <ElapsedTimer startedAt={t.startedAt} />
                      {t.status === "completed" && (
                        <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => revealShelved(t.taskGroupId)} title="Insert result">
                          <Check className="h-3.5 w-3.5" />
                        </Button>
                      )}
                      {t.status === "failed" && (
                        <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => revealShelved(t.taskGroupId)} title="Dismiss">
                          <X className="h-3.5 w-3.5" />
                        </Button>
                      )}
                    </Card>
                  ))}
                </div>
              </div>
            )}

            {pendingFiles.length > 0 && (
              <div className="flex flex-wrap gap-2 border-t bg-muted/20 px-4 py-2">
                {pendingFiles.map((f) => (
                  <div key={f.file_id} className="relative">
                    <AuthImage
                      fileKey={f.file_id}
                      alt={f.filename}
                      className="h-16 w-16 rounded-md border object-cover"
                    />
                    <Button
                      size="icon"
                      variant="destructive"
                      className="absolute -right-1 -top-1 h-5 w-5"
                      onClick={() =>
                        setPendingFiles((prev) => prev.filter((p) => p.file_id !== f.file_id))
                      }
                    >
                      <X className="h-3 w-3" />
                    </Button>
                  </div>
                ))}
              </div>
            )}

            <div className="flex items-end gap-2 border-t p-3">
              <Textarea
                placeholder="Type a message..."
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                rows={Math.max(2, input.split("\n").length + 1)}
                className="min-h-0 flex-1 resize-none"
              />
              <input
                ref={fileInputRef}
                type="file"
                accept="image/png,image/jpeg,image/gif,image/webp"
                className="hidden"
                onChange={handleFileUpload}
                multiple
              />
              <Button
                variant="outline"
                size="icon"
                onClick={() => fileInputRef.current?.click()}
                disabled={uploading}
                title="Attach image"
              >
                <Paperclip className="h-4 w-4" />
              </Button>
              <Button
                onClick={handleSend}
                disabled={sending || (!input.trim() && pendingFiles.length === 0)}
              >
                <Send className="h-4 w-4" />
              </Button>
            </div>
          </>
        )}
      </div>
      {deleteConfirm && (
        <ConfirmDialog
          title="Delete conversation"
          confirmLabel="Delete"
          cancelLabel="Cancel"
          onConfirm={() => { removeConversation(deleteConfirm.id); setDeleteConfirm(null); }}
          onCancel={() => setDeleteConfirm(null)}
        >
          Delete <strong>"{deleteConfirm.title}"</strong>? This cannot be undone.
        </ConfirmDialog>
      )}
    </div>
  );
}
