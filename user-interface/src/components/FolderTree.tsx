import { useCallback, useEffect, useState } from "react";
import { apiClient } from "../api/client";
import { folderName } from "@teamagentica/api-client";
import type { Plugin } from "@teamagentica/api-client";

interface FolderNode {
  path: string;
  name: string;
}

interface FolderTreeProps {
  provider: Plugin;
  isSelected: boolean;
  activePath: string;
  onSelectProvider: (p: Plugin) => void;
  onNavigate: (prefix: string) => void;
}

export default function FolderTree({
  provider,
  isSelected,
  activePath,
  onSelectProvider,
  onNavigate,
}: FolderTreeProps) {
  const [expanded, setExpanded] = useState(false);
  const [children, setChildren] = useState<FolderNode[] | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (isSelected && !expanded) setExpanded(true);
  }, [isSelected]);

  useEffect(() => {
    if (expanded && children === null && !loading) {
      setLoading(true);
      apiClient.files.browse(provider.id, "")
        .then((r) =>
          setChildren((r.folders || []).map((f) => ({ path: f, name: folderName(f) })))
        )
        .catch(() => setChildren([]))
        .finally(() => setLoading(false));
    }
  }, [expanded]);

  const handleRootClick = () => {
    if (!isSelected) onSelectProvider(provider);
    else onNavigate("");
    if (!expanded) setExpanded(true);
  };

  const isRootActive = isSelected && activePath === "";

  return (
    <div className="ftree-root">
      <button
        className={`ftree-row ftree-provider ${isRootActive ? "active" : ""}`}
        onClick={handleRootClick}
      >
        <span
          className="ftree-dot"
          style={{ color: provider.status === "running" ? "var(--success)" : "var(--text-muted)" }}
        >
          {"\u25CF"}
        </span>
        <span className="ftree-name">{provider.name || provider.id}</span>
      </button>
      {expanded && (
        <div className="ftree-lines">
          {loading && !children && (
            <div className="ftree-row ftree-leaf">
              <span className="ftree-prefix">{"└─ "}</span>
              <span className="ftree-name ftree-muted">Loading...</span>
            </div>
          )}
          {children && children.length === 0 && (
            <div className="ftree-row ftree-leaf">
              <span className="ftree-prefix">{"└─ "}</span>
              <span className="ftree-name ftree-muted">No folders</span>
            </div>
          )}
          {children &&
            children.map((node, i) => (
              <TreeNode
                key={node.path}
                node={node}
                providerId={provider.id}
                isProviderSelected={isSelected}
                activePath={activePath}
                onNavigate={onNavigate}
                isLast={i === children.length - 1}
                parentPrefix=""
              />
            ))}
        </div>
      )}
    </div>
  );
}

interface TreeNodeProps {
  node: FolderNode;
  providerId: string;
  isProviderSelected: boolean;
  activePath: string;
  onNavigate: (prefix: string) => void;
  isLast: boolean;
  parentPrefix: string;
}

function TreeNode({
  node,
  providerId,
  isProviderSelected,
  activePath,
  onNavigate,
  isLast,
  parentPrefix,
}: TreeNodeProps) {
  const [expanded, setExpanded] = useState(false);
  const [children, setChildren] = useState<FolderNode[] | null>(null);
  const [loading, setLoading] = useState(false);

  const isActive = isProviderSelected && activePath === node.path;
  const isAncestor =
    isProviderSelected && activePath.startsWith(node.path) && activePath !== node.path;

  // Auto-expand and load if this node is an ancestor of the active path
  useEffect(() => {
    if (isAncestor) {
      if (!expanded) setExpanded(true);
      if (children === null && !loading) {
        setLoading(true);
        apiClient.files.browse(providerId, node.path)
          .then((result) =>
            setChildren((result.folders || []).map((f) => ({ path: f, name: folderName(f) })))
          )
          .catch(() => setChildren([]))
          .finally(() => setLoading(false));
      }
    }
  }, [isAncestor, activePath]);

  const loadChildren = useCallback(async () => {
    if (loading) return;
    setLoading(true);
    try {
      const result = await apiClient.files.browse(providerId, node.path);
      setChildren((result.folders || []).map((f) => ({ path: f, name: folderName(f) })));
    } catch {
      setChildren([]);
    } finally {
      setLoading(false);
    }
  }, [providerId, node.path, loading]);

  useEffect(() => {
    if (expanded && children === null && !isAncestor) loadChildren();
  }, [expanded]);

  const handleClick = () => {
    onNavigate(node.path);
    if (!expanded) setExpanded(true);
  };

  const branch = isLast ? "└─ " : "├─ ";
  const childPrefix = parentPrefix + (isLast ? "   " : "│  ");

  return (
    <>
      <button
        className={`ftree-row ${isActive ? "active" : ""}`}
        onClick={handleClick}
      >
        <span className="ftree-prefix">{parentPrefix}{branch}</span>
        <span className="ftree-name">{node.name}</span>
      </button>
      {expanded && loading && (
        <div className="ftree-row ftree-leaf">
          <span className="ftree-prefix">{childPrefix}{"└─ "}</span>
          <span className="ftree-name ftree-muted">...</span>
        </div>
      )}
      {expanded && children && children.length > 0 &&
        children.map((child, i) => (
          <TreeNode
            key={child.path}
            node={child}
            providerId={providerId}
            isProviderSelected={isProviderSelected}
            activePath={activePath}
            onNavigate={onNavigate}
            isLast={i === children.length - 1}
            parentPrefix={childPrefix}
          />
        ))}
    </>
  );
}
