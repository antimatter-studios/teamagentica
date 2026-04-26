import { useEffect, useState, useCallback } from "react";
import { X, Plus, Copy } from "lucide-react";
import type { Tunnel } from "@teamagentica/api-client";
import { apiClient } from "../api/client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface Props {
  value: string[];
  onChange: (next: string[]) => void;
}

const DRIVERS = [
  { id: "ssh-reverse", label: "SSH reverse tunnel", description: "Workspace dials out to an SSH server, exposing a port on that server." },
];

export default function TunnelPicker({ value, onChange }: Props) {
  const [tunnels, setTunnels] = useState<Tunnel[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [pickValue, setPickValue] = useState("");

  const refresh = useCallback(() => {
    setLoading(true);
    try {
      apiClient.tunnels.list()
        .then((t) => { setTunnels(t); setLoading(false); })
        .catch(() => setLoading(false));
    } catch {
      setLoading(false);
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  // Poll while any attached tunnel is transient or errored — public_key /
  // endpoint only become available after the driver starts.
  useEffect(() => {
    const transient = value.some((n) => {
      const t = tunnels.find((x) => x.spec.name === n);
      return !t || t.status.state === "starting" || t.status.state === "error" || !t.status.endpoint;
    });
    if (!transient) return;
    const id = setInterval(refresh, 5000);
    return () => clearInterval(id);
  }, [value, tunnels, refresh]);

  const byName = new Map(tunnels.map((t) => [t.spec.name, t]));
  const available = tunnels.filter((t) => !value.includes(t.spec.name));

  const attach = (name: string) => {
    if (!name || value.includes(name)) return;
    onChange([...value, name]);
    setPickValue("");
  };
  const detach = (name: string) => {
    onChange(value.filter((n) => n !== name));
  };

  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        Attach tunnels managed by network-traffic-manager. Detaching a tunnel here does not delete it.
      </p>

      <div className="space-y-2">
        {value.length === 0 && (
          <div className="text-sm text-muted-foreground italic">No tunnels attached.</div>
        )}
        {value.map((name) => {
          const t = byName.get(name);
          return (
            <div key={`tref-${name}`} className="border rounded-md p-2 space-y-2">
              <div className="flex items-center gap-2 flex-wrap">
                <Badge variant="outline">{t?.spec.driver || "?"}</Badge>
                <span className="font-mono text-sm">{name}</span>
                {t?.spec.target && (
                  <>
                    <span className="text-muted-foreground">&rarr;</span>
                    <span className="font-mono text-sm">{t.spec.target}</span>
                  </>
                )}
                {t && (
                  <Badge variant={t.status.state === "running" ? "default" : "secondary"}>
                    {t.status.state}
                  </Badge>
                )}
                {!t && <Badge variant="destructive">missing</Badge>}
                <div className="flex-1" />
                <Button variant="ghost" size="icon" onClick={() => detach(name)} title="Detach">
                  <X className="h-4 w-4" />
                </Button>
              </div>
              {t?.status.endpoint && (
                <CopyRow label="Endpoint" value={t.status.endpoint} mono />
              )}
              {t?.status.public_key && (
                <CopyRow
                  label="Public key"
                  value={t.status.public_key}
                  mono
                  hint={`Install in ~/.ssh/authorized_keys on the SSH host so the tunnel can connect.`}
                />
              )}
              {t?.status.error && (
                <p className="text-xs text-destructive">{t.status.error}</p>
              )}
            </div>
          );
        })}
      </div>

      <div className="flex items-center gap-2 flex-wrap">
        <Select value={pickValue} onValueChange={(v) => attach(v)}>
          <SelectTrigger className="w-[16rem]">
            <SelectValue placeholder={loading ? "Loading..." : available.length === 0 ? "No more tunnels" : "Attach existing tunnel..."} />
          </SelectTrigger>
          <SelectContent>
            {available.map((t) => (
              <SelectItem key={`tunnel-${t.spec.name}`} value={t.spec.name}>
                {t.spec.name} <span className="text-muted-foreground">({t.spec.driver})</span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button variant="outline" size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="h-4 w-4 mr-1" /> Create new
        </Button>
      </div>

      {showCreate && (
        <CreateTunnelDialog
          existingNames={new Set(tunnels.map((t) => t.spec.name))}
          onClose={() => setShowCreate(false)}
          onCreated={(name) => {
            setShowCreate(false);
            refresh();
            attach(name);
          }}
        />
      )}
    </div>
  );
}

function CopyRow({ label, value, mono, hint }: { label: string; value: string; mono?: boolean; hint?: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch { /* */ }
  };
  return (
    <div className="space-y-1">
      <div className="flex items-start gap-2">
        <Label className="text-xs text-muted-foreground min-w-[5rem] pt-1">{label}</Label>
        <div className={`flex-1 ${mono ? "font-mono" : ""} text-xs break-all`}>{value}</div>
        <Button variant="ghost" size="icon" onClick={copy} title="Copy">
          <Copy className="h-3 w-3" />
        </Button>
      </div>
      {(hint || copied) && (
        <p className="text-xs text-muted-foreground pl-[5.5rem]">{copied ? "Copied!" : hint}</p>
      )}
    </div>
  );
}

function CreateTunnelDialog({
  existingNames,
  onClose,
  onCreated,
}: {
  existingNames: Set<string>;
  onClose: () => void;
  onCreated: (name: string) => void;
}) {
  const [name, setName] = useState("");
  const [driver, setDriver] = useState("ssh-reverse");
  const [target, setTarget] = useState("");
  // ssh-reverse fields.
  const [host, setHost] = useState("");
  const [user, setUser] = useState("");
  const [port, setPort] = useState("22");
  const [remoteBindPort, setRemoteBindPort] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [knownHosts, setKnownHosts] = useState("");

  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const driverInfo = DRIVERS.find((d) => d.id === driver);

  const submit = async () => {
    setErr(null);
    if (!name.trim() || !target.trim()) {
      setErr("name and target are required");
      return;
    }
    if (existingNames.has(name.trim())) {
      setErr(`a tunnel named "${name.trim()}" already exists`);
      return;
    }
    let config: Record<string, string> = {};
    if (driver === "ssh-reverse") {
      if (!host.trim() || !user.trim() || !remoteBindPort.trim()) {
        setErr("host, user, and remote_bind_port are required");
        return;
      }
      config = {
        host: host.trim(),
        user: user.trim(),
        port: port.trim() || "22",
        remote_bind_port: remoteBindPort.trim(),
      };
      if (privateKey.trim()) config.private_key = privateKey;
      if (knownHosts.trim()) config.known_hosts = knownHosts;
    }
    setSubmitting(true);
    try {
      await apiClient.tunnels.create({
        name: name.trim(),
        driver,
        target: target.trim(),
        auto_start: true,
        config,
      });
      onCreated(name.trim());
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : "failed to create tunnel");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Create tunnel</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="flex flex-col gap-1">
            <Label>Name</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-mac-ssh" />
          </div>
          <div className="flex flex-col gap-1">
            <Label>Driver</Label>
            <Select value={driver} onValueChange={setDriver}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                {DRIVERS.map((d) => (
                  <SelectItem key={`drv-${d.id}`} value={d.id}>{d.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            {driverInfo && <p className="text-xs text-muted-foreground">{driverInfo.description}</p>}
          </div>
          <div className="flex flex-col gap-1">
            <Label>Target (workspace-internal host:port to forward)</Label>
            <Input value={target} onChange={(e) => setTarget(e.target.value)} placeholder="localhost:22" />
          </div>

          {driver === "ssh-reverse" && (
            <>
              <div className="grid grid-cols-3 gap-2">
                <div className="col-span-2 flex flex-col gap-1">
                  <Label>SSH host</Label>
                  <Input value={host} onChange={(e) => setHost(e.target.value)} placeholder="s1.antimatter-studios.com" />
                </div>
                <div className="flex flex-col gap-1">
                  <Label>Port</Label>
                  <Input value={port} onChange={(e) => setPort(e.target.value)} />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-2">
                <div className="flex flex-col gap-1">
                  <Label>SSH user</Label>
                  <Input value={user} onChange={(e) => setUser(e.target.value)} placeholder="tunnel" />
                </div>
                <div className="flex flex-col gap-1">
                  <Label>Remote bind port</Label>
                  <Input value={remoteBindPort} onChange={(e) => setRemoteBindPort(e.target.value)} placeholder="10022" />
                </div>
              </div>
              <div className="flex flex-col gap-1">
                <Label>Private key (PEM, optional)</Label>
                <textarea
                  className="w-full font-mono text-xs rounded-md border bg-background p-2 min-h-[5rem]"
                  value={privateKey}
                  onChange={(e) => setPrivateKey(e.target.value)}
                  placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                />
              </div>
              <div className="flex flex-col gap-1">
                <Label>Known hosts (optional)</Label>
                <textarea
                  className="w-full font-mono text-xs rounded-md border bg-background p-2 min-h-[3rem]"
                  value={knownHosts}
                  onChange={(e) => setKnownHosts(e.target.value)}
                  placeholder="s1.antimatter-studios.com ssh-ed25519 AAAAC3..."
                />
              </div>
              <p className="text-xs text-muted-foreground">
                After creation, install the tunnel's public key in <span className="font-mono">~/.ssh/authorized_keys</span> on the SSH host so the tunnel can connect.
              </p>
            </>
          )}

          {err && <p className="text-sm text-destructive">{err}</p>}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={submitting}>Cancel</Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting ? "Creating..." : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
