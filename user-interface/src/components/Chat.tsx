import { useEffect, useRef, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { useChatStore } from "../stores/chatStore";
import { uploadFile, fetchFileBlob, downloadFile, type Attachment } from "../api/chat";

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
  const { agents, hasCoordinator, conversations, activeConversationId, messages, selectedAgent, sending, loading, error } = useChatStore(
    useShallow((s) => ({
      agents: s.agents,
      hasCoordinator: s.hasCoordinator,
      conversations: s.conversations,
      activeConversationId: s.activeConversationId,
      messages: s.messages,
      selectedAgent: s.selectedAgent,
      sending: s.sending,
      loading: s.loading,
      error: s.error,
    }))
  );
  const loadAgents = useChatStore((s) => s.loadAgents);
  const loadConversations = useChatStore((s) => s.loadConversations);
  const selectConversation = useChatStore((s) => s.selectConversation);
  const newConversation = useChatStore((s) => s.newConversation);
  const removeConversation = useChatStore((s) => s.removeConversation);
  const setSelectedAgent = useChatStore((s) => s.setSelectedAgent);
  const send = useChatStore((s) => s.send);

  const [input, setInput] = useState("");
  const [pendingFiles, setPendingFiles] = useState<
    { file_id: string; filename: string }[]
  >([]);
  const [uploading, setUploading] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    loadAgents();
    loadConversations();
  }, [loadAgents, loadConversations]);

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
              <span className="chat-conv-title">{conv.title}</span>
              <button
                className="chat-conv-delete"
                onClick={(e) => {
                  e.stopPropagation();
                  removeConversation(conv.id);
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
      <div className="chat-main">
        <div className="chat-messages">
          {loading && (
            <div className="chat-loading">Loading messages...</div>
          )}
          {messages.map((msg) => (
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
                <div key={attId || `att-${idx}`} className="chat-msg-attachment">
                  {att.type === "url" && att.url ? (
                    att.mime_type?.startsWith("video/") ? (
                      <video
                        src={att.url}
                        controls
                        className="chat-msg-video"
                        style={{ maxWidth: "100%", maxHeight: 400 }}
                      />
                    ) : (
                      <img
                        src={att.url}
                        alt={att.filename}
                        className="chat-msg-image"
                        onClick={() => window.open(att.url, "_blank")}
                      />
                    )
                  ) : att.mime_type?.startsWith("image/") ? (
                    <AuthImage fileKey={attId} alt={att.filename} className="chat-msg-image" />
                  ) : (
                    <a href="#" className="chat-msg-file-link" onClick={(e) => { e.preventDefault(); downloadFile(attId, att.filename); }}>
                      {att.filename}
                    </a>
                  )}
                </div>
                );
              })}
              {msg.role === "assistant" && msg.duration_ms ? (
                <div className="chat-msg-meta">
                  {msg.duration_ms}ms
                  {msg.input_tokens
                    ? ` | ${msg.input_tokens}+${msg.output_tokens} tokens`
                    : ""}
                </div>
              ) : null}
            </div>
          ))}
          <div ref={messagesEndRef} />
        </div>

        {error && <div className="chat-error">{error}</div>}

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
          <select
            className="chat-agent-select"
            value={selectedAgent}
            onChange={(e) => setSelectedAgent(e.target.value)}
          >
            {hasCoordinator && (
              <option value="auto">Auto</option>
            )}
            {agents.map((a) => (
              <option key={a.alias} value={a.alias}>
                @{a.alias}
              </option>
            ))}
          </select>
          <textarea
            className="chat-input"
            placeholder="Type a message..."
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            rows={1}
            disabled={sending}
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
      </div>
    </div>
  );
}
