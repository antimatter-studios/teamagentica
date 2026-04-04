import { useEffect, useRef, useState, useCallback } from "react";
import { useShallow } from "zustand/react/shallow";
import { useChatStore, type ProgressInfo } from "../stores/chatStore";
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
  return <span className="chat-msg-elapsed">{display}</span>;
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
  const { conversations, activeConversationId, messages, sending, loading, error, sendStartedAt, activeTaskGroupId, shelvedTasks, progressInfo } = useChatStore(
    useShallow((s) => ({
      conversations: s.conversations,
      activeConversationId: s.activeConversationId,
      messages: s.messages,
      sending: s.sending,
      loading: s.loading,
      error: s.error,
      sendStartedAt: s.sendStartedAt,
      activeTaskGroupId: s.activeTaskGroupId,
      shelvedTasks: s.shelvedTasks,
      progressInfo: s.progressInfo,
    }))
  );

  // SSE-driven progress for the active conversation (if any).
  const activeProgress: ProgressInfo | null = activeConversationId ? progressInfo[activeConversationId] ?? null : null;
  const loadConversations = useChatStore((s) => s.loadConversations);
  const selectConversation = useChatStore((s) => s.selectConversation);
  const newConversation = useChatStore((s) => s.newConversation);
  const removeConversation = useChatStore((s) => s.removeConversation);
  const send = useChatStore((s) => s.send);
  const shelfTask = useChatStore((s) => s.shelfTask);
  const revealShelved = useChatStore((s) => s.revealShelved);
  // refreshMessages is called by the SSE subscription in chatStore, not directly here.

  const [input, setInput] = useState("");
  const [pendingFiles, setPendingFiles] = useState<
    { file_id: string; filename: string }[]
  >([]);
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

  // Track whether user is scrolled near the bottom (within 500px)
  const handleMessagesScroll = useCallback(() => {
    const el = messagesContainerRef.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    isNearBottomRef.current = distanceFromBottom < 500;
  }, []);

  useEffect(() => {
    loadConversations();
  }, [loadConversations]);

  // Sync URL subpath → store: when navigating to /chat/{id}, select that conversation.
  useEffect(() => {
    if (activePage !== "chat") return;
    const urlConvId = subpath ? Number(subpath) : null;
    if (urlConvId && !Number.isNaN(urlConvId) && urlConvId !== activeConversationId) {
      selectConversation(urlConvId);
    } else if (!subpath && activeConversationId) {
      // URL cleared (e.g. navigated to /chat) — keep current selection but update URL.
      onConversationChange(String(activeConversationId));
    }
  }, [activePage, subpath]); // eslint-disable-line react-hooks/exhaustive-deps

  // Sync store → URL: when user clicks a conversation in sidebar, update the URL.
  useEffect(() => {
    if (activePage !== "chat") return;
    const urlConvId = subpath ? Number(subpath) : null;
    if (activeConversationId && activeConversationId !== urlConvId) {
      onConversationChange(String(activeConversationId));
    } else if (!activeConversationId && subpath) {
      onConversationChange("");
    }
  }, [activeConversationId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Track previous message count to detect new-message vs refresh scenarios
  const prevMessageCountRef = useRef(0);

  // Auto-scroll only when user is near bottom.
  // On scroll handler keeps isNearBottomRef updated; we also snapshot position
  // before render to catch cases where the scroll handler hasn't fired recently.
  useEffect(() => {
    const el = messagesContainerRef.current;
    const msgCount = messages.length;
    const prevCount = prevMessageCountRef.current;
    prevMessageCountRef.current = msgCount;

    // First load of a conversation — always scroll to bottom
    if (prevCount === 0 && msgCount > 0) {
      messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
      return;
    }

    // No new messages (refresh returned same count) — never scroll
    if (msgCount <= prevCount) return;

    // New messages appended — only scroll if user was near bottom
    if (el) {
      // Trust the onScroll handler's isNearBottomRef since it reflects
      // the position *before* the new messages were rendered.
      if (!isNearBottomRef.current) return;
    }

    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const handleSend = async () => {
    const content = input.trim();
    if (!content && pendingFiles.length === 0) return;
    setInput("");
    const attachmentIds = pendingFiles.map((f) => f.file_id);
    setPendingFiles([]);
    // User just sent a message — always auto-scroll to follow the response
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
    const imageFiles = Array.from(files).filter((f) =>
      f.type.startsWith("image/")
    );
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
    if (e.dataTransfer.types.includes("Files")) {
      setDragOver(true);
    }
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounter.current--;
    if (dragCounter.current === 0) {
      setDragOver(false);
    }
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
    try {
      return JSON.parse(attachmentsJson);
    } catch {
      return [];
    }
  };

  return (
    <div className="chat-container">
      {/* Sidebar */}
      <div className="chat-sidebar">
        <button className="chat-new-btn" onClick={() => { newConversation(); onConversationChange(""); }}>
          + NEW CHAT
        </button>
        <div className="chat-conv-list" style={{ flex: 1 }}>
          {conversations.map((conv) => (
            <div
              key={conv.id}
              className={`chat-conv-item ${
                conv.id === activeConversationId ? "active" : ""
              }`}
              onClick={() => selectConversation(conv.id)}
            >
              {(conv.unread_count ?? 0) > 0 && (
                <span className="chat-conv-unread" title={`${conv.unread_count} unread`}>
                  {conv.unread_count}
                </span>
              )}
              <span className="chat-conv-title">{conv.title}</span>
              <button
                className="chat-conv-delete"
                onClick={(e) => {
                  e.stopPropagation();
                  setDeleteConfirm({ id: conv.id, title: conv.title });
                }}
              >
                x
              </button>
            </div>
          ))}
          {conversations.length === 0 && (
            <div className="chat-conv-empty">No conversations yet</div>
          )}
        </div>

        {/* Participants panel */}
        {messages.length > 0 && (() => {
          const participants = new Map<string, { label: string; count: number }>();
          for (const msg of messages) {
            if (msg.role === "progress") continue;
            const key = msg.role === "user" ? "_you" : (msg.agent_alias || "agent");
            const existing = participants.get(key);
            if (existing) {
              existing.count++;
            } else {
              participants.set(key, {
                label: msg.role === "user" ? "You" : `@${msg.agent_alias || "agent"}`,
                count: 1,
              });
            }
          }
          const totalMessages = messages.filter((m) => m.role !== "progress").length;
          return (
            <div className="chat-participants">
              <div className="chat-participants-header">
                <span>PARTICIPANTS</span>
                <span className="chat-participants-total">{totalMessages} msg{totalMessages !== 1 ? "s" : ""}</span>
              </div>
              <div className="chat-participants-list">
                {Array.from(participants.values()).map((p) => (
                  <div key={p.label} className="chat-participant">
                    <span className="chat-participant-name">{p.label}</span>
                    <span className="chat-participant-count">{p.count}</span>
                  </div>
                ))}
              </div>
            </div>
          );
        })()}
      </div>

      {/* Main area */}
      <div
        className="chat-main"
        onDragEnter={handleDragEnter}
        onDragLeave={handleDragLeave}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        {dragOver && (
          <div className="chat-drop-overlay">
            <div className="chat-drop-label">Drop images here</div>
          </div>
        )}
        {expandedAttachment ? (
          <div className="chat-expanded-preview">
            <div className="chat-expanded-preview-header">
              <button className="file-viewer-back" onClick={() => {
                setExpandedAttachment(null);
                const id = expandedAttachmentId.current;
                if (id) {
                  requestAnimationFrame(() => {
                    document.getElementById(id)?.scrollIntoView({ block: "center" });
                  });
                }
              }}>
                &#x2190; Back
              </button>
              <span className="file-viewer-filename">{expandedAttachment.filename}</span>
            </div>
            <div className="chat-expanded-preview-body">
              <ChatAttachmentPreview attachment={expandedAttachment} />
            </div>
          </div>
        ) : (
        <>
        <div className="chat-messages" ref={messagesContainerRef} onScroll={handleMessagesScroll}>
          {loading && (
            <div className="chat-loading">Loading messages...</div>
          )}
          {messages.filter((m) => !(m.role === "progress" && activeProgress)).map((msg) => (
            msg.role === "progress" ? (
            <div key={msg.id} className="chat-msg chat-msg-progress">
              <div className="chat-msg-progress-content">
                {msg.content}
                {activeTaskGroupId && <span className="chat-msg-task-group"> [task = {activeTaskGroupId}]</span>}
                {sendStartedAt && <ElapsedTimer startedAt={sendStartedAt} />}
                <button className="chat-shelf-btn" onClick={shelfTask} title="Move to shelf">
                  {"\u{1F4E6}"}
                </button>
              </div>
            </div>
            ) : (
            <div
              key={msg.id}
              className={`chat-msg ${
                msg.role === "user" ? "chat-msg-user" : "chat-msg-assistant"
              }`}
            >
              <div className="chat-msg-header">
                {msg.role === "user" ? (
                  <span className="chat-msg-role">You</span>
                ) : (
                  <span className="chat-msg-role chat-msg-agent">
                    @{msg.agent_alias || "agent"}
                    {msg.model && (
                      <span className="chat-msg-model">{msg.model}</span>
                    )}
                  </span>
                )}
                <span className="chat-msg-time">
                  {new Date(msg.created_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
                  {" · "}
                  {new Date(msg.created_at).toLocaleDateString([], { day: "numeric", month: "short", year: "numeric" })}
                </span>
              </div>
              <div className="chat-msg-content">
                {msg.content}
              </div>
              {parseAttachments(msg.attachments).map((att, idx) => {
                const attId = att.storage_key || att.file_id || "";
                return (
                <div
                  key={attId || `att-${idx}`}
                  id={`chat-att-${attId || idx}`}
                  className="chat-msg-attachment"
                  onClick={() => {
                    expandedAttachmentId.current = `chat-att-${attId || idx}`;
                    setExpandedAttachment(att);
                  }}
                  style={{ cursor: "pointer" }}
                >
                  <ChatAttachmentPreview attachment={att} compact />
                </div>
                );
              })}
              {msg.role === "assistant" && (msg.duration_ms || msg.input_tokens || msg.cost_usd) ? (
                <div className="chat-msg-meta">
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
            </div>
            )
          ))}
          {activeProgress && (
            <div className="chat-msg chat-msg-progress">
              <div className="chat-msg-progress-content">
                {activeProgress.message}
                {activeProgress.taskGroupId && <span className="chat-msg-task-group"> [task = {activeProgress.taskGroupId}]</span>}
                {sendStartedAt && <ElapsedTimer startedAt={sendStartedAt} />}
                <button className="chat-shelf-btn" onClick={shelfTask} title="Move to shelf">
                  {"\u{1F4E6}"}
                </button>
              </div>
            </div>
          )}
          <div ref={messagesEndRef} />
        </div>

        {error && <div className="chat-error">{error}</div>}

        {/* Shelf — backgrounded tasks */}
        {shelvedTasks.length > 0 && (
          <div className="chat-shelf">
            <div className="chat-shelf-label">Shelf</div>
            <div className="chat-shelf-items">
              {shelvedTasks.map((t) => (
                <div key={t.taskGroupId} className={`chat-shelf-item chat-shelf-item-${t.status}`}>
                  <span className="chat-shelf-item-dot" />
                  <span className="chat-shelf-item-msg">{t.message}</span>
                  <ElapsedTimer startedAt={t.startedAt} />
                  {t.status === "completed" && (
                    <button
                      className="chat-shelf-reveal-btn"
                      onClick={() => revealShelved(t.taskGroupId)}
                      title="Insert result into conversation"
                    >
                      {"\u2713"}
                    </button>
                  )}
                  {t.status === "failed" && (
                    <button
                      className="chat-shelf-reveal-btn"
                      onClick={() => revealShelved(t.taskGroupId)}
                      title="Dismiss"
                    >
                      {"\u2717"}
                    </button>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Pending file previews */}
        {pendingFiles.length > 0 && (
          <div className="chat-pending-files">
            {pendingFiles.map((f) => (
              <div key={f.file_id} className="chat-pending-file">
                <AuthImage fileKey={f.file_id} alt={f.filename} className="chat-pending-thumb" />
                <button
                  className="chat-pending-remove"
                  onClick={() =>
                    setPendingFiles((prev) =>
                      prev.filter((p) => p.file_id !== f.file_id)
                    )
                  }
                >
                  x
                </button>
              </div>
            ))}
          </div>
        )}

        {/* Input bar */}
        <div className="chat-input-bar">
          <textarea
            className="chat-input"
            placeholder="Type a message..."
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            rows={Math.max(2, (input.split("\n").length) + 2)}
          />
          <input
            ref={fileInputRef}
            type="file"
            accept="image/png,image/jpeg,image/gif,image/webp"
            style={{ display: "none" }}
            onChange={handleFileUpload}
            multiple
          />
          <button
            className="chat-attach-btn"
            onClick={() => fileInputRef.current?.click()}
            disabled={uploading}
            title="Attach image"
          >
            {uploading ? "..." : "\u{1F4CE}"}
          </button>
          <button
            className="chat-send-btn"
            onClick={handleSend}
            disabled={sending || (!input.trim() && pendingFiles.length === 0)}
          >
            {sending ? "..." : "\u{2192}"}
          </button>
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
