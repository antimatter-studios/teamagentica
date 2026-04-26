import { useCallback, useEffect, useState } from "react";
import { apiClient } from "../api/client";
import { folderName } from "@teamagentica/api-client";
import type { Plugin } from "@teamagentica/api-client";
import { Circle, Folder, FolderOpen, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";

interface FolderNode {
  path: string;
  name: string;
}

interface FolderTreeProps {
  provider: Plugin;
  isSelected: boolean;
  activePath: string;
  trashActive: boolean;
  refreshVersion: number;
  onSelectProvider: (p: Plugin) => void;
  onNavigate: (prefix: string) => void;
  onTrashClick: () => void;
}

const rowBase =
  "flex w-full items-center gap-2 rounded-md px-2 py-1 text-sm text-left transition-colors hover:bg-accent hover:text-accent-foreground";
const rowActive = "bg-accent text-accent-foreground";

export default function FolderTree({
  provider,
  isSelected,
  activePath,
  trashActive,
  refreshVersion,
  onSelectProvider,
  onNavigate,
  onTrashClick,
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

  // Re-fetch root children when storage changes (e.g. new folder created).
  useEffect(() => {
    if (!isSelected || !expanded || refreshVersion === 0) return;
    apiClient.files.browse(provider.id, "")
      .then((r) =>
        setChildren((r.folders || []).map((f) => ({ path: f, name: folderName(f) })))
      )
      .catch(() => {});
  }, [refreshVersion]);

  const handleRootClick = () => {
    if (!isSelected) onSelectProvider(provider);
    else onNavigate("");
    if (!expanded) setExpanded(true);
  };

  const isRootActive = isSelected && activePath === "";
  const running = provider.status === "running";

  return (
    <div className="flex flex-col">
      <button
        className={cn(rowBase, "font-medium", isRootActive && rowActive)}
        onClick={handleRootClick}
      >
        <Circle
          className={cn(
            "h-2 w-2 shrink-0 fill-current",
            running ? "text-emerald-500" : "text-muted-foreground"
          )}
        />
        <span className="truncate">{provider.name || provider.id}</span>
      </button>
      {expanded && (
        <div className="ml-3 flex flex-col border-l pl-2">
          {loading && !children && (
            <div className={cn(rowBase, "text-muted-foreground")}>
              <span className="text-xs">Loading...</span>
            </div>
          )}
          {children && children.length === 0 && (
            <div className={cn(rowBase, "text-muted-foreground")}>
              <span className="text-xs">No folders</span>
            </div>
          )}
          {children &&
            children.map((node) => (
              <TreeNode
                key={node.path}
                node={node}
                providerId={provider.id}
                isProviderSelected={isSelected}
                activePath={activePath}
                onNavigate={onNavigate}
              />
            ))}
          {/* Trash virtual item — always last */}
          <button
            className={cn(rowBase, isSelected && trashActive && rowActive)}
            onClick={(e) => { e.stopPropagation(); onTrashClick(); }}
          >
            <Trash2 className="h-4 w-4 shrink-0" />
            <span>Trash</span>
          </button>
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
}

function TreeNode({
  node,
  providerId,
  isProviderSelected,
  activePath,
  onNavigate,
}: TreeNodeProps) {
  const [expanded, setExpanded] = useState(false);
  const [children, setChildren] = useState<FolderNode[] | null>(null);
  const [loading, setLoading] = useState(false);

  const isActive = isProviderSelected && activePath === node.path;
  const isAncestor =
    isProviderSelected && activePath.startsWith(node.path) && activePath !== node.path;

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

  return (
    <>
      <button
        className={cn(rowBase, isActive && rowActive)}
        onClick={handleClick}
      >
        {expanded ? (
          <FolderOpen className="h-4 w-4 shrink-0" />
        ) : (
          <Folder className="h-4 w-4 shrink-0" />
        )}
        <span className="truncate">{node.name}</span>
      </button>
      {expanded && (
        <div className="ml-3 flex flex-col border-l pl-2">
          {loading && (
            <div className={cn(rowBase, "text-muted-foreground")}>
              <span className="text-xs">...</span>
            </div>
          )}
          {children && children.length > 0 &&
            children.map((child) => (
              <TreeNode
                key={child.path}
                node={child}
                providerId={providerId}
                isProviderSelected={isProviderSelected}
                activePath={activePath}
                onNavigate={onNavigate}
              />
            ))}
        </div>
      )}
    </>
  );
}
