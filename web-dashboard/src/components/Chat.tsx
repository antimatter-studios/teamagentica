import { useEffect, useRef, useState, useCallback } from "react";
import { useShallow } from "zustand/react/shallow";
import { useChatStore } from "../stores/chatStore";
import { apiClient } from "../api/client";
import type { Attachment } from "@teamagentica/api-client";
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

export default function Chat() {
  const { conversations, activeConversationId, messages, sending, loading, error, sendStartedAt, activeTaskGroupId, shelvedTasks } = useChatStore(
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
    }))
  );
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
  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    loadConversations();
  }, [loadConversations]);

  // Progress updates now arrive via SSE → eventStore → chatStore subscription.
  // No polling needed.

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const handleSend = async () => {
    const content = input.trim();
    if (!content && pendingFiles.length === 0) return;
    setInput("");
    const attachmentIds = pendingFiles.map((f) => f.file_id);
    setPendingFiles([]);
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
        <button className="chat-new-btn" onClick={newConversation}>
          + NEW CHAT
        </button>
        <div className="chat-conv-list">
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
        <div className="chat-messages">
          {loading && (
            <div className="chat-loading">Loading messages...</div>
          )}
          {messages.map((msg) => (
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
        <div className="modal-overlay" onClick={() => setDeleteConfirm(null)}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: 400 }}>
            <div className="modal-header">
              <div className="modal-title">Delete conversation</div>
            </div>
            <p style={{ color: "var(--text-secondary)", margin: "12px 0 0" }}>
              Delete <strong>"{deleteConfirm.title}"</strong>? This cannot be undone.
            </p>
            <div className="modal-actions">
              <button className="modal-btn modal-btn--ghost" onClick={() => setDeleteConfirm(null)}>
                Cancel
              </button>
              <button
                className="modal-btn modal-btn--danger"
                onClick={() => {
                  removeConversation(deleteConfirm.id);
                  setDeleteConfirm(null);
                }}
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
