import { useCallback, useEffect, useState } from "react";
import type { WorkspaceDisk, WorkspaceOptions, Workspace, Environment, Disk } from "@teamagentica/api-client";
import { apiClient } from "../api/client";
import type { Plugin } from "@teamagentica/api-client";
import { X } from "lucide-react";
import SaveButton from "./SaveButton";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import TunnelPicker from "./TunnelPicker";

interface Props {
  workspaceId: string;
  workspace: Workspace;
  environment?: Environment;
  onClose: () => void;
}

export default function WorkspaceSettings({ workspaceId, workspace: ws, environment: env, onClose }: Props) {
  const [restarting, setRestarting] = useState(false);
  const [options, setOptions] = useState<WorkspaceOptions | null>(null);
  const [optionsLoading, setOptionsLoading] = useState(true);
  const [optionsDirty, setOptionsDirty] = useState(false);

  const [agentPlugins, setAgentPlugins] = useState<Plugin[]>([]);
  const [sharedDisks, setSharedDisks] = useState<Disk[]>([]);

  useEffect(() => {
    setOptionsLoading(true);
    apiClient.workspaces.getWorkspaceOptions(workspaceId)
      .then((opts) => { setOptions(opts); setOptionsLoading(false); })
      .catch(() => setOptionsLoading(false));
  }, [workspaceId]);

  useEffect(() => {
    apiClient.plugins.list().then((plugins) => {
      setAgentPlugins(plugins.filter((p) => p.capabilities?.includes("agent:chat") && p.enabled));
    }).catch(() => {});
    apiClient.workspaces.listDisks("shared").then(setSharedDisks).catch(() => {});
  }, []);

  // Local form state.
  const [envOverrides, setEnvOverrides] = useState<Record<string, string>>({});
  const [disks, setDisks] = useState<WorkspaceDisk[]>([]);
  const [agentPlugin, setAgentPlugin] = useState("");
  const [agentModel, setAgentModel] = useState("");
  const [tunnelRefs, setTunnelRefs] = useState<string[]>([]);

  // Sync local state when options load.
  useEffect(() => {
    if (!options) return;
    try {
      const parsed = options.env_overrides ? JSON.parse(options.env_overrides) : {};
      setEnvOverrides(parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {});
    } catch { setEnvOverrides({}); }
    try {
      // Prefer unified disks field, fall back to deprecated extra_disks.
      const raw = options.disks || options.extra_disks;
      const parsed = raw ? JSON.parse(raw) : [];
      if (Array.isArray(parsed)) {
        // Ensure legacy extra_disks entries get a type.
        setDisks(parsed.map((d: WorkspaceDisk) => ({ ...d, type: d.type || "shared" })));
      } else {
        setDisks([]);
      }
    } catch { setDisks([]); }
    setAgentPlugin(options.agent_plugin || "");
    setAgentModel(options.agent_model || "");
    try {
      const parsed = options.tunnel_refs ? JSON.parse(options.tunnel_refs) : [];
      setTunnelRefs(Array.isArray(parsed) ? parsed.filter((s) => typeof s === "string") : []);
    } catch { setTunnelRefs([]); }
  }, [options]);

  // Env override helpers.
  const [newEnvKey, setNewEnvKey] = useState("");
  const [newEnvVal, setNewEnvVal] = useState("");

  const addEnvOverride = () => {
    if (!newEnvKey.trim()) return;
    setEnvOverrides((prev) => ({ ...prev, [newEnvKey.trim()]: newEnvVal }));
    setNewEnvKey("");
    setNewEnvVal("");
  };

  const removeEnvOverride = (key: string) => {
    setEnvOverrides((prev) => {
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  // Disk helpers.
  const [newDiskId, setNewDiskId] = useState("");
  const [newDiskTarget, setNewDiskTarget] = useState("");
  const [newDiskRO, setNewDiskRO] = useState(false);

  const addDisk = () => {
    if (!newDiskId || !newDiskTarget.trim()) return;
    const disk = sharedDisks.find((d) => d.id === newDiskId);
    setDisks((prev) => [
      ...prev,
      { disk_id: newDiskId, name: disk?.name || newDiskId, type: "shared", target: newDiskTarget.trim(), read_only: newDiskRO },
    ]);
    setNewDiskId("");
    setNewDiskTarget("");
    setNewDiskRO(false);
  };

  const removeDisk = (idx: number) => {
    setDisks((prev) => prev.filter((_, i) => i !== idx));
  };

  // Split disks by type for display.
  const workspaceDisks = disks.filter((d) => d.type === "workspace");
  const sharedMountedDisks = disks.filter((d) => d.type === "shared");

  // Disks already attached — filter from dropdown.
  const attachedDiskIds = new Set(disks.map((d) => d.disk_id));
  const availableDisks = sharedDisks.filter((d) => !attachedDiskIds.has(d.id));

  const handleSave = useCallback(() => {
    // Auto-add pending form entries before saving.
    let disksToSave = disks;
    if (newDiskId && newDiskTarget.trim()) {
      const disk = sharedDisks.find((d) => d.id === newDiskId);
      disksToSave = [...disksToSave, { disk_id: newDiskId, name: disk?.name || newDiskId, type: "shared", target: newDiskTarget.trim(), read_only: newDiskRO }];
      setDisks(disksToSave);
      setNewDiskId("");
      setNewDiskTarget("");
      setNewDiskRO(false);
    }
    let envToSave = envOverrides;
    if (newEnvKey.trim()) {
      envToSave = { ...envToSave, [newEnvKey.trim()]: newEnvVal };
      setEnvOverrides(envToSave);
      setNewEnvKey("");
      setNewEnvVal("");
    }

    apiClient.workspaces.updateWorkspaceOptions(workspaceId, {
      env_overrides: envToSave,
      disks: disksToSave,
      agent_plugin: agentPlugin,
      agent_model: agentModel,
      tunnel_refs: tunnelRefs,
    }).then((updated) => {
      setOptions(updated);
      setOptionsDirty(true);
    }).catch(() => {});
  }, [workspaceId, envOverrides, disks, agentPlugin, agentModel, tunnelRefs, newDiskId, newDiskTarget, newDiskRO, sharedDisks, newEnvKey, newEnvVal]);

  const handleRestart = useCallback(async () => {
    setRestarting(true);
    try {
      await apiClient.workspaces.restartWorkspace(workspaceId);
      setOptionsDirty(false);
    } catch { /* */ }
    setRestarting(false);
  }, [workspaceId]);

  const statusVariant: "default" | "secondary" | "destructive" | "outline" =
    ws.status === "running" ? "default"
      : ws.status === "stopped" ? "secondary"
      : ws.status === "error" ? "destructive"
      : "outline";

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="max-w-4xl max-h-[90vh] overflow-hidden flex flex-col p-0">
        <DialogHeader className="px-6 pt-6">
          <DialogTitle className="text-xl">{ws.name}</DialogTitle>
        </DialogHeader>

        <div className="flex items-center gap-2 px-6 pb-2">
          <Badge variant={statusVariant}>{ws.status}</Badge>
          {env && <Badge variant="outline">{env.name}</Badge>}
          <div className="flex-1" />
          <Button variant="outline" size="sm" onClick={handleRestart} disabled={restarting}>
            {restarting ? "Restarting..." : "Restart"}
          </Button>
        </div>

        <div className="flex-1 overflow-y-auto px-6 pb-6 space-y-4">
          {optionsLoading ? (
            <div className="py-8 text-center text-muted-foreground">Loading options...</div>
          ) : (
            <>
              <Card>
                <CardHeader>
                  <CardTitle className="text-base">Overview</CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <div className="flex flex-col gap-1">
                      <Label className="text-muted-foreground">Workspace ID</Label>
                      <span className="font-mono text-sm">{ws.id}</span>
                    </div>
                    <div className="flex flex-col gap-1">
                      <Label className="text-muted-foreground">Subdomain</Label>
                      <span className="font-mono text-sm">{ws.subdomain}</span>
                    </div>
                    <div className="flex flex-col gap-1">
                      <Label className="text-muted-foreground">Environment</Label>
                      <span className="text-sm">{env?.name || ws.environment}</span>
                    </div>
                    <div className="flex flex-col gap-1">
                      <Label className="text-muted-foreground">Status</Label>
                      <span className="text-sm">{ws.status}</span>
                    </div>
                    {ws.url && (
                      <div className="flex flex-col gap-1">
                        <Label className="text-muted-foreground">URL</Label>
                        <a
                          href={ws.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-sm text-primary underline-offset-4 hover:underline break-all"
                        >
                          {ws.url}
                        </a>
                      </div>
                    )}
                    {options?.sidecar_id && (
                      <div className="flex flex-col gap-1">
                        <Label className="text-muted-foreground">Agent Sidecar</Label>
                        <span className="font-mono text-sm">{options.sidecar_id}</span>
                      </div>
                    )}
                  </div>
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle className="text-base">Environment</CardTitle>
                </CardHeader>
                <CardContent className="space-y-3">
                  <p className="text-sm text-muted-foreground">
                    Override environment variables. Merged on top of defaults on restart.
                  </p>
                  <div className="space-y-2">
                    {Object.entries(envOverrides).filter(([key]) => key !== "").map(([key, val]) => (
                      <div key={`env-${key}`} className="flex items-center gap-2">
                        <span className="font-mono text-sm min-w-[8rem] truncate">{key}</span>
                        <span className="text-muted-foreground">=</span>
                        <Input
                          className="flex-1"
                          value={val}
                          onChange={(e) => setEnvOverrides((prev) => ({ ...prev, [key]: e.target.value }))}
                        />
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => removeEnvOverride(key)}
                          title="Remove"
                        >
                          <X className="h-4 w-4" />
                        </Button>
                      </div>
                    ))}
                  </div>
                  <div className="flex items-center gap-2">
                    <Input
                      placeholder="KEY"
                      value={newEnvKey}
                      onChange={(e) => setNewEnvKey(e.target.value)}
                      onKeyDown={(e) => e.key === "Enter" && addEnvOverride()}
                      className="max-w-[12rem]"
                    />
                    <span className="text-muted-foreground">=</span>
                    <Input
                      placeholder="value"
                      value={newEnvVal}
                      onChange={(e) => setNewEnvVal(e.target.value)}
                      onKeyDown={(e) => e.key === "Enter" && addEnvOverride()}
                      className="flex-1"
                    />
                    <Button variant="outline" size="sm" onClick={addEnvOverride}>Add</Button>
                  </div>
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle className="text-base">Disks</CardTitle>
                </CardHeader>
                <CardContent className="space-y-3">
                  <p className="text-sm text-muted-foreground">
                    Manage disks attached to this workspace. Changes take effect on restart.
                  </p>
                  <div className="space-y-2">
                    {workspaceDisks.map((d, i) => (
                      <div key={`wdisk-${i}-${d.disk_id}`} className="flex items-center gap-2 flex-wrap">
                        <Badge variant="secondary">workspace</Badge>
                        <span className="text-sm font-medium">{d.name}</span>
                        <span className="text-muted-foreground">&rarr;</span>
                        <span className="font-mono text-sm">{d.target}</span>
                      </div>
                    ))}
                    {sharedMountedDisks.map((d) => {
                      const globalIdx = disks.indexOf(d);
                      return (
                        <div key={`sdisk-${globalIdx}-${d.disk_id}`} className="flex items-center gap-2 flex-wrap">
                          <Badge variant="outline">shared</Badge>
                          <span className="text-sm font-medium">{d.name}</span>
                          <span className="text-muted-foreground">&rarr;</span>
                          <span className="font-mono text-sm flex-1">{d.target}</span>
                          <Badge
                            variant={d.read_only ? "destructive" : "secondary"}
                            className={cn(!d.read_only && "bg-muted text-muted-foreground")}
                          >
                            {d.read_only ? "RO" : "RW"}
                          </Badge>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => removeDisk(globalIdx)}
                            title="Remove"
                          >
                            <X className="h-4 w-4" />
                          </Button>
                        </div>
                      );
                    })}
                    {disks.length === 0 && (
                      <div className="text-sm text-muted-foreground italic">No disks attached.</div>
                    )}
                  </div>
                  {availableDisks.length > 0 && (
                    <div className="flex items-center gap-2 flex-wrap">
                      <Select value={newDiskId} onValueChange={setNewDiskId}>
                        <SelectTrigger className="w-[12rem]">
                          <SelectValue placeholder="Select disk..." />
                        </SelectTrigger>
                        <SelectContent>
                          {availableDisks.map((d) => (
                            <SelectItem key={`avail-${d.id}-${d.name}`} value={d.id}>{d.name}</SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <span className="text-muted-foreground">&rarr;</span>
                      <Input
                        placeholder="$HOME/.config/git"
                        value={newDiskTarget}
                        onChange={(e) => setNewDiskTarget(e.target.value)}
                        className="flex-1 min-w-[10rem]"
                      />
                      <label className="flex items-center gap-2 text-sm">
                        <Checkbox
                          checked={newDiskRO}
                          onCheckedChange={(checked) => setNewDiskRO(checked === true)}
                        />
                        RO
                      </label>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={addDisk}
                        disabled={!newDiskId || !newDiskTarget.trim()}
                      >
                        Add
                      </Button>
                    </div>
                  )}
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle className="text-base">Tunnels</CardTitle>
                </CardHeader>
                <CardContent>
                  <TunnelPicker value={tunnelRefs} onChange={setTunnelRefs} />
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle className="text-base">Agent</CardTitle>
                </CardHeader>
                <CardContent className="space-y-4">
                  <p className="text-sm text-muted-foreground">
                    Attach an agent sidecar to this workspace.
                    {agentPlugin && (
                      <> Chat with it using <strong className="text-foreground">@{ws.subdomain}-{agentPlugin}</strong>.</>
                    )}
                  </p>
                  <div className="flex flex-col gap-2">
                    <Label>Agent Plugin</Label>
                    <Select
                      value={agentPlugin || "__none__"}
                      onValueChange={(v) => setAgentPlugin(v === "__none__" ? "" : v)}
                    >
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__none__">None</SelectItem>
                        {agentPlugins.map((p) => (
                          <SelectItem key={`agent-${p.id}`} value={p.id}>{p.name || p.id}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  {agentPlugin && (
                    <div className="flex flex-col gap-2">
                      <Label>Model</Label>
                      <Select
                        value={agentModel || "__default__"}
                        onValueChange={(v) => setAgentModel(v === "__default__" ? "" : v)}
                      >
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="__default__">Default</SelectItem>
                          <SelectItem value="claude-opus-4-6">Claude Opus</SelectItem>
                          <SelectItem value="claude-sonnet-4-6">Claude Sonnet</SelectItem>
                          <SelectItem value="claude-haiku-4-5-20251001">Claude Haiku</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                  )}
                  {options?.sidecar_id && (
                    <div className="flex flex-col gap-1">
                      <Label className="text-muted-foreground">Active Sidecar</Label>
                      <span className="font-mono text-sm">{options.sidecar_id}</span>
                    </div>
                  )}
                </CardContent>
              </Card>

              <div className="flex items-center gap-3 pt-2">
                <SaveButton onClick={handleSave} />
                {optionsDirty && (
                  <span className="text-sm text-muted-foreground">Restart required to apply</span>
                )}
              </div>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
