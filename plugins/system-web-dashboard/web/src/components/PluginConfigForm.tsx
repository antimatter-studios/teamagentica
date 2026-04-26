import React, { useState, useRef } from "react";
import { ChevronDown, ChevronRight, Loader2, Plus, X, RefreshCw } from "lucide-react";
import type { Plugin } from "@teamagentica/api-client";
import {
  usePluginConfig,
  type ConfigField,
  type SelectOption,
} from "../hooks/usePluginConfig";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";

// Extract value and label from a select option (string or {label, value}).
function optValue(opt: SelectOption): string {
  return typeof opt === "string" ? opt : opt.value;
}
function optLabel(opt: SelectOption): string {
  return typeof opt === "string" ? opt : opt.label;
}

/** Format milliseconds as a human-readable duration. */
function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

interface DAGNode {
  id: string;
  alias: string;
  tool?: string;
  prompt?: string;
  state: string;
  duration_ms?: number;
  error?: string;
}

// ---- Tunnel list field (custom renderer for ConfigSchemaField.type === "tunnels") ----

type TunnelDriver = "ngrok" | "ssh-reverse";

interface TunnelEntry {
  name: string;
  driver: TunnelDriver;
  auto_start?: boolean;
  role?: string;
  target?: string;
  config?: Record<string, string>;
}

const TUNNEL_DRIVER_LABELS: Record<TunnelDriver, string> = {
  "ngrok": "ngrok",
  "ssh-reverse": "SSH reverse tunnel",
};

interface TunnelDriverField {
  key: string;
  label: string;
  placeholder?: string;
  secret?: boolean;
  multiline?: boolean;
  helpText?: string;
}

const NGROK_FIELDS: TunnelDriverField[] = [
  { key: "authtoken", label: "Auth Token", secret: true, helpText: "ngrok auth token from https://dashboard.ngrok.com" },
  { key: "domain", label: "Domain", placeholder: "my-app.ngrok-free.app", helpText: "Optional static/reserved ngrok domain" },
];

const SSH_REVERSE_FIELDS: TunnelDriverField[] = [
  { key: "host", label: "Bastion host", placeholder: "s1.example.com" },
  { key: "port", label: "Bastion SSH port", placeholder: "22" },
  { key: "user", label: "Bastion user", placeholder: "tunnel" },
  { key: "private_key", label: "Private key (PEM)", secret: true, multiline: true, helpText: "OpenSSH private key authorized by the bastion" },
  { key: "known_hosts", label: "Known hosts (optional)", multiline: true, helpText: "authorized_keys-format pinned host keys; empty = accept any (insecure)" },
  { key: "remote_bind_host", label: "Remote bind host", placeholder: "0.0.0.0" },
  { key: "remote_bind_port", label: "Remote bind port", placeholder: "0 (bastion-assigned)" },
];

function fieldsForDriver(driver: TunnelDriver): TunnelDriverField[] {
  return driver === "ngrok" ? NGROK_FIELDS : SSH_REVERSE_FIELDS;
}

function FieldShell({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("flex flex-col gap-1.5", className)}>{children}</div>;
}

function TunnelListField({
  label, helpText, required, value, onChange,
}: {
  label: string;
  helpText?: string;
  required?: boolean;
  value: string;
  onChange: (json: string) => void;
}) {
  let entries: TunnelEntry[] = [];
  try {
    const parsed = JSON.parse(value || "[]");
    if (Array.isArray(parsed)) entries = parsed;
  } catch { /* ignore — render as empty */ }

  const update = (next: TunnelEntry[]) => onChange(JSON.stringify(next));

  return (
    <FieldShell>
      <Label>
        {label}
        {required && <span className="text-destructive ml-1">*</span>}
      </Label>
      {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}
      <div className="flex flex-col gap-2 rounded-md border p-2">
        {entries.map((entry, i) => (
          <TunnelRow
            key={i}
            entry={entry}
            onChange={(updated) => {
              const next = [...entries];
              next[i] = updated;
              update(next);
            }}
            onRemove={() => update(entries.filter((_, j) => j !== i))}
          />
        ))}
        <Button
          variant="outline"
          size="sm"
          className="w-fit"
          onClick={() => update([
            ...entries,
            { name: `tunnel-${entries.length + 1}`, driver: "ngrok", auto_start: true, config: {} },
          ])}
        >
          <Plus className="h-4 w-4" />
          Add tunnel
        </Button>
      </div>
    </FieldShell>
  );
}

function TunnelRow({
  entry, onChange, onRemove,
}: {
  entry: TunnelEntry;
  onChange: (e: TunnelEntry) => void;
  onRemove: () => void;
}) {
  const [open, setOpen] = useState(false);
  const driver = (entry.driver || "ngrok") as TunnelDriver;
  const cfg = entry.config || {};

  const setField = (patch: Partial<TunnelEntry>) => onChange({ ...entry, ...patch });
  const setCfg = (key: string, val: string) => onChange({ ...entry, config: { ...cfg, [key]: val } });

  return (
    <div className="rounded-md border bg-muted/30">
      <div
        className="flex items-center gap-2 p-2 cursor-pointer hover:bg-accent rounded-md"
        onClick={() => setOpen(!open)}
      >
        {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
        <span className="flex-1 min-w-0 truncate font-medium">
          {entry.name || "(unnamed)"}
        </span>
        <span className="text-xs text-muted-foreground shrink-0">
          {TUNNEL_DRIVER_LABELS[driver] || driver}
        </span>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          onClick={(e) => { e.stopPropagation(); onRemove(); }}
          title="Remove tunnel"
        >
          <X className="h-4 w-4" />
        </Button>
      </div>
      {open && (
        <div className="flex flex-col gap-3 border-l-2 border-border ml-2 pl-4 py-3 pr-2">
          <TunnelField label="Name" value={entry.name} onChange={(v) => setField({ name: v })} placeholder="ingress" />
          <FieldShell>
            <Label>Driver</Label>
            <Select
              value={driver}
              onValueChange={(v) => setField({ driver: v as TunnelDriver, config: {} })}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(Object.keys(TUNNEL_DRIVER_LABELS) as TunnelDriver[]).map((d) => (
                  <SelectItem key={d} value={d}>{TUNNEL_DRIVER_LABELS[d]}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </FieldShell>
          <TunnelField label="Target" value={entry.target || ""} onChange={(v) => setField({ target: v })} placeholder="webhook | host:port" helpText='"webhook" auto-discovers the webhook plugin; otherwise host:port' />
          <TunnelField label="Role" value={entry.role || ""} onChange={(v) => setField({ role: v })} placeholder="(empty) | ingress" helpText='"ingress" broadcasts ingress:ready when running' />
          <FieldShell>
            <div className="flex items-center gap-2">
              <Checkbox
                checked={entry.auto_start ?? false}
                onCheckedChange={(c) => setField({ auto_start: c === true })}
                id={`autostart-${entry.name}`}
              />
              <Label htmlFor={`autostart-${entry.name}`}>Auto-start</Label>
            </div>
          </FieldShell>
          <Separator />
          <div className="text-xs font-semibold tracking-wide uppercase text-muted-foreground">
            {TUNNEL_DRIVER_LABELS[driver]} config
          </div>
          {fieldsForDriver(driver).map((f) => (
            <TunnelField
              key={f.key}
              label={f.label}
              value={cfg[f.key] || ""}
              onChange={(v) => setCfg(f.key, v)}
              placeholder={f.placeholder}
              secret={f.secret}
              multiline={f.multiline}
              helpText={f.helpText}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function TunnelField({
  label, value, onChange, placeholder, secret, multiline, helpText,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  secret?: boolean;
  multiline?: boolean;
  helpText?: string;
}) {
  return (
    <FieldShell>
      <Label className="flex items-center gap-2">
        {label}
        {secret && <Badge variant="destructive">SECRET</Badge>}
      </Label>
      {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}
      {multiline ? (
        <Textarea
          value={value}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value)}
          rows={4}
          className="font-mono text-xs"
        />
      ) : (
        <Input
          type={secret ? "password" : "text"}
          value={value}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </FieldShell>
  );
}

/** Collapsible raw JSON debug view. */
function DAGRawJson({ item }: { item: Record<string, unknown> }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="mt-2">
      <button
        type="button"
        className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
        onClick={() => setOpen(!open)}
      >
        {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
        Raw JSON
      </button>
      {open && (
        <pre className="mt-2 rounded-md bg-muted/50 p-2 text-xs font-mono whitespace-pre-wrap break-all">
          {JSON.stringify(item, null, 2)}
        </pre>
      )}
    </div>
  );
}

/** Expandable item row — click to reveal DAG node detail. */
function ExpandableItem({ item, idx }: { item: Record<string, unknown>; idx: number }) {
  const [open, setOpen] = useState(false);
  const state = String(item.state || "");
  const stateColorClass =
    state === "running" ? "text-amber-500"
    : state === "completed" ? "text-green-500"
    : state === "failed" ? "text-red-500"
    : "text-muted-foreground";
  const message = String(item.message || "");
  const nodes = (item.nodes || []) as DAGNode[];

  return (
    <div key={String(item.id || idx)} className="border-b last:border-b-0">
      <div
        className="flex items-center gap-2 p-2 cursor-pointer hover:bg-accent"
        onClick={() => setOpen(!open)}
      >
        <span className={cn("flex-1 min-w-0 truncate", stateColorClass, state === "running" && "font-semibold")}>
          {open ? <ChevronDown className="h-3 w-3 inline mr-1" /> : <ChevronRight className="h-3 w-3 inline mr-1" />}
          {String(item.time || "")} {message}
        </span>
        <span className="text-xs text-amber-500 shrink-0 text-right">
          {String(item.summary || "")}
        </span>
      </div>
      {open && (
        <div className="p-3 bg-muted/30">
          <div className="text-sm mb-2">{message}</div>
          {nodes.length > 0 && (
            <div className="rounded-md border overflow-hidden">
              <div className="grid grid-cols-4 gap-2 px-2 py-1 text-xs font-semibold tracking-wide text-muted-foreground bg-muted">
                <span>STATUS</span>
                <span>ALIAS</span>
                <span>TOOL</span>
                <span>DURATION</span>
              </div>
              {nodes.map((node, ni) => {
                const nc = node.state === "running" ? "text-amber-500"
                  : node.state === "completed" ? "text-green-500"
                  : node.state === "failed" ? "text-red-500"
                  : "text-muted-foreground";
                const icon = node.state === "running" ? "▶"
                  : node.state === "completed" ? "✓"
                  : node.state === "failed" ? "✗"
                  : "○";
                return (
                  <React.Fragment key={node.id || ni}>
                    <div className="grid grid-cols-4 gap-2 px-2 py-1 text-xs items-center">
                      <span className={nc}>{icon} {node.state}</span>
                      <span>@{node.alias}</span>
                      <span className="text-muted-foreground">{node.tool || "—"}</span>
                      <span>{node.duration_ms ? formatDuration(node.duration_ms) : "—"}</span>
                    </div>
                    {node.prompt && (
                      <div className="grid grid-cols-[auto_1fr] gap-2 px-2 py-1 text-xs items-start bg-muted/30">
                        <span className="text-muted-foreground">prompt:</span>
                        <span className="whitespace-pre-wrap break-words font-mono">
                          {node.prompt}
                        </span>
                      </div>
                    )}
                  </React.Fragment>
                );
              })}
            </div>
          )}
          {nodes.length === 0 && (
            <div className="text-xs text-muted-foreground">No steps recorded</div>
          )}
          <DAGRawJson item={item} />
        </div>
      )}
    </div>
  );
}

import type { SchemaSection } from "../hooks/usePluginConfig";

/** device_code: show URL + verification code, auto-poll for completion. */
function DeviceCodeFlow({ url, code, polling }: { url: string; code: string; polling: boolean }) {
  return (
    <div className="flex flex-col gap-3 rounded-md border bg-muted/30 p-4">
      <p className="text-sm">Open the link below and enter the code to sign in:</p>
      <a className="text-sm font-mono break-all underline text-primary" href={url} target="_blank" rel="noopener noreferrer">
        {url}
      </a>
      <div className="rounded-md border bg-background px-3 py-2 font-mono text-lg tracking-widest text-center">
        {code}
      </div>
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        {polling ? (
          <><Loader2 className="h-4 w-4 animate-spin" /> Waiting for login to complete...</>
        ) : (
          "Login flow expired. Click the button to try again."
        )}
      </p>
    </div>
  );
}

/** redirect_code: show URL, user copies code from provider back and pastes it here. */
function RedirectCodeFlow({ url, submitting, onSubmit }: { url: string; submitting?: boolean; onSubmit: (code: string) => void }) {
  const inputRef = useRef<HTMLInputElement>(null);
  return (
    <div className="flex flex-col gap-3 rounded-md border bg-muted/30 p-4">
      <p className="text-sm">Open the link below to sign in, then copy the code back here:</p>
      <a className="text-sm font-mono break-all underline text-primary" href={url} target="_blank" rel="noopener noreferrer">
        {url}
      </a>
      <div className="flex items-center gap-2">
        <Input
          ref={inputRef}
          type="text"
          placeholder="Paste authorization code"
          disabled={submitting}
          onKeyDown={(e) => {
            if (e.key === "Enter" && inputRef.current?.value) {
              onSubmit(inputRef.current.value);
            }
          }}
        />
        <Button
          disabled={submitting}
          onClick={() => {
            if (inputRef.current?.value) {
              onSubmit(inputRef.current.value);
            }
          }}
        >
          {submitting ? (<><Loader2 className="h-4 w-4 animate-spin" /> SUBMITTING...</>) : "SUBMIT CODE"}
        </Button>
      </div>
    </div>
  );
}

function ReadonlyTable({ items, columns }: { items: Record<string, unknown>[]; columns: string[] }) {
  return (
    <div className="rounded-md border overflow-hidden">
      <div
        className="grid gap-2 px-3 py-2 text-xs font-semibold tracking-wide text-muted-foreground bg-muted"
        style={{ gridTemplateColumns: `repeat(${columns.length}, 1fr)` }}
      >
        {columns.map((col) => (
          <span key={col}>{col.replace(/_/g, " ").toUpperCase()}</span>
        ))}
      </div>
      {items.map((item, idx) => (
        <div
          className="grid gap-2 px-3 py-2 text-sm border-t"
          key={String(item.id || idx)}
          style={{ gridTemplateColumns: `repeat(${columns.length}, 1fr)` }}
        >
          {columns.map((col) => (
            <span key={col}>{item[col] == null ? "—" : String(item[col])}</span>
          ))}
        </div>
      ))}
      {items.length === 0 && (
        <div className="px-3 py-4 text-center text-sm text-muted-foreground">No entries</div>
      )}
    </div>
  );
}

function ReadonlySection({ section, headerRight }: { section: SchemaSection; headerRight?: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between text-xs font-semibold tracking-wide text-muted-foreground">
        <span>{section.name.replace(/_/g, " ").toUpperCase()}</span>
        {headerRight}
      </div>
      <div className="flex flex-col gap-1 rounded-md border">
        {section.items && section.columns ? (
          <ReadonlyTable items={section.items} columns={section.columns} />
        ) : section.items ? (
          <>
            {section.items.map((item, idx) => (
              <ExpandableItem key={String(item.id || idx)} item={item} idx={idx} />
            ))}
            {section.items.length === 0 && (
              <div className="px-3 py-4 text-center text-sm text-muted-foreground">No entries</div>
            )}
          </>
        ) : (
          <>
            {section.fields.map((f) => (
              <div className="flex items-center justify-between gap-3 px-3 py-1.5 border-b last:border-b-0 text-sm" key={f.key}>
                <span className="text-muted-foreground">{f.key}</span>
                <span className="font-mono text-xs">{f.value}</span>
              </div>
            ))}
            {section.fields.length === 0 && (
              <div className="px-3 py-4 text-center text-sm text-muted-foreground">No entries</div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

interface Props {
  plugin: Plugin;
  onSaved: () => void;
}

export default function PluginConfigForm({ plugin, onSaved }: Props) {
  const {
    fields,
    loading,
    saving,
    error,
    saveSuccess,
    extraSections,
    refreshCountdown,
    triggerRefresh,
    dynamicOptions,
    oauthStates,
    updateField,
    handleSave,
    handleOAuthLogin,
    handleOAuthLogout,
    handleOAuthSubmitCode,
  } = usePluginConfig(plugin, onSaved);

  function renderField(field: ConfigField, index: number) {
    const schema = field.schema;
    const fieldType = schema?.type || "string";
    const label = schema?.label || field.key.toUpperCase();
    const helpText = schema?.help_text;

    if (fieldType === "oauth") {
      const state = oauthStates[field.key];
      const oauthMethod = schema?.oauth_method || "device_code";

      return (
        <div className="flex flex-col gap-2 rounded-md border p-4" key={field.key}>
          <div className="text-sm font-semibold tracking-wide">
            {label.toUpperCase()}
          </div>
          {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}

          {plugin.status !== "running" ? (
            <div className="text-sm text-muted-foreground">
              Plugin must be running to authenticate.
            </div>
          ) : !state || state.loading ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" /> Checking authentication...
            </div>
          ) : state.status?.authenticated ? (
            <div className="flex items-center gap-3 flex-wrap">
              <Badge variant="default">AUTHENTICATED</Badge>
              {state.status.detail && (
                <span className="text-sm text-muted-foreground">{state.status.detail}</span>
              )}
              <Button
                variant="outline"
                size="sm"
                onClick={() => handleOAuthLogout(field.key)}
              >
                LOGOUT
              </Button>
            </div>
          ) : state.deviceCode ? (
            oauthMethod === "redirect_code" ? (
              <RedirectCodeFlow
                url={state.deviceCode.url}
                submitting={state.submitting}
                onSubmit={(code) => handleOAuthSubmitCode(field.key, code)}
              />
            ) : (
              <DeviceCodeFlow
                url={state.deviceCode.url}
                code={state.deviceCode.code}
                polling={state.polling}
              />
            )
          ) : (
            <div>
              <Button onClick={() => handleOAuthLogin(field.key)}>
                {label.toUpperCase()}
              </Button>
            </div>
          )}
          {state?.error && (
            <Alert variant="destructive">
              <AlertDescription>{state.error}</AlertDescription>
            </Alert>
          )}
        </div>
      );
    }

    // Aliases have their own dedicated tab — skip rendering here.
    if (fieldType === "aliases") {
      return null;
    }

    if (fieldType === "model_list") {
      let entries: string[] = [];
      try {
        entries = JSON.parse(field.value || "[]");
      } catch {
        entries = [];
      }

      const updateEntries = (updated: string[]) => {
        updateField(index, JSON.stringify(updated));
      };

      return (
        <FieldShell key={field.key}>
          <Label>
            {label}
            {schema?.required && <span className="text-destructive ml-1">*</span>}
          </Label>
          {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}

          <div className="flex flex-col gap-2 rounded-md border p-2">
            {entries.map((entry, i) => (
              <div className="flex items-center gap-2" key={i}>
                <Input
                  type="text"
                  value={entry}
                  placeholder="model name (e.g. llama3.2:3b)"
                  onChange={(e) => {
                    const updated = [...entries];
                    updated[i] = e.target.value;
                    updateEntries(updated);
                  }}
                />
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => updateEntries(entries.filter((_, j) => j !== i))}
                  title="Remove model"
                >
                  <X className="h-4 w-4" />
                </Button>
              </div>
            ))}
            <Button
              variant="outline"
              size="sm"
              className="w-fit"
              onClick={() => updateEntries([...entries, ""])}
            >
              <Plus className="h-4 w-4" />
              Add model
            </Button>
          </div>
        </FieldShell>
      );
    }

    if (fieldType === "bot_token") {
      let entries: { alias: string; token: string }[] = [];
      try {
        entries = JSON.parse(field.value || "[]");
      } catch {
        entries = [];
      }

      const updateEntries = (updated: typeof entries) => {
        updateField(index, JSON.stringify(updated));
      };

      return (
        <FieldShell key={field.key}>
          <Label>
            {label}
            {schema?.required && <span className="text-destructive ml-1">*</span>}
          </Label>
          {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}

          <div className="flex flex-col gap-2 rounded-md border p-2">
            {entries.length > 0 && (
              <div className="grid grid-cols-[1fr_2fr_auto] gap-2 text-xs font-semibold tracking-wide text-muted-foreground px-2">
                <span>ALIAS</span>
                <span>TOKEN</span>
                <span className="w-10" />
              </div>
            )}
            {entries.map((entry, i) => (
              <div className="grid grid-cols-[1fr_2fr_auto] gap-2 items-center" key={i}>
                <Input
                  type="text"
                  value={entry.alias}
                  placeholder="alias"
                  onChange={(e) => {
                    const updated = [...entries];
                    updated[i] = { ...updated[i], alias: e.target.value };
                    updateEntries(updated);
                  }}
                />
                <Input
                  type="password"
                  value={entry.token}
                  placeholder="bot token"
                  onChange={(e) => {
                    const updated = [...entries];
                    updated[i] = { ...updated[i], token: e.target.value };
                    updateEntries(updated);
                  }}
                />
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => updateEntries(entries.filter((_, j) => j !== i))}
                  title="Remove bot"
                >
                  <X className="h-4 w-4" />
                </Button>
              </div>
            ))}
            <Button
              variant="outline"
              size="sm"
              className="w-fit"
              onClick={() => updateEntries([...entries, { alias: "", token: "" }])}
            >
              <Plus className="h-4 w-4" />
              Add bot
            </Button>
          </div>
        </FieldShell>
      );
    }

    if (fieldType === "tunnels") {
      return (
        <TunnelListField
          key={field.key}
          label={label}
          helpText={helpText}
          required={schema?.required}
          value={field.value}
          onChange={(json) => updateField(index, json)}
        />
      );
    }

    const isReadOnly = schema?.readonly ?? false;

    const labelEl = (
      <Label className="flex items-center gap-2 flex-wrap">
        <span>{label}</span>
        {schema?.required && <span className="text-destructive">*</span>}
        {isReadOnly && <Badge variant="secondary">READ-ONLY</Badge>}
        {field.is_secret && <Badge variant="destructive">SECRET</Badge>}
        {fieldType === "select" && schema?.dynamic && dynamicOptions[field.key]?.fallback && (
          <Badge variant="outline" className="border-amber-500 text-amber-600">STATIC FALLBACK</Badge>
        )}
      </Label>
    );

    if (fieldType === "select" && schema?.dynamic) {
      const dyn = dynamicOptions[field.key];
      const isLoading = dyn?.loading ?? false;
      const dynError = dyn?.error;
      const opts = dyn?.options ?? [];
      const optValues = opts.map(optValue);
      const allOpts = field.value && !optValues.includes(field.value)
        ? [field.value, ...opts]
        : opts;
      const isDisabled = isReadOnly || isLoading || !!dynError;
      return (
        <FieldShell key={field.key}>
          {labelEl}
          {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}
          <Select
            value={field.value || undefined}
            onValueChange={(v) => updateField(index, v)}
            disabled={isDisabled}
          >
            <SelectTrigger>
              <SelectValue placeholder={isLoading ? "Loading..." : dynError ? "-- Unavailable --" : "-- Select --"} />
            </SelectTrigger>
            <SelectContent>
              {allOpts.map((opt) => (
                <SelectItem key={optValue(opt)} value={optValue(opt)}>{optLabel(opt)}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          {isLoading && (
            <span className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="h-3 w-3 animate-spin" /> Fetching available options...
            </span>
          )}
          {dynError && (
            <span className="text-xs text-destructive">{dynError}</span>
          )}
        </FieldShell>
      );
    }

    if (fieldType === "select" && schema?.options) {
      return (
        <FieldShell key={field.key}>
          {labelEl}
          {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}
          <Select
            value={field.value || undefined}
            onValueChange={(v) => updateField(index, v)}
            disabled={isReadOnly}
          >
            <SelectTrigger>
              <SelectValue placeholder="-- Select --" />
            </SelectTrigger>
            <SelectContent>
              {schema.options.map((opt) => (
                <SelectItem key={optValue(opt)} value={optValue(opt)}>{optLabel(opt)}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </FieldShell>
      );
    }

    if (fieldType === "boolean") {
      const checked = field.value === "true" || field.value === "1";
      return (
        <FieldShell key={field.key}>
          {labelEl}
          {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}
          <div className="flex items-center gap-2">
            <Switch
              checked={checked}
              onCheckedChange={(c) => updateField(index, c ? "true" : "false")}
              disabled={isReadOnly}
            />
            <span className="text-sm text-muted-foreground">
              {checked ? "Enabled" : "Disabled"}
            </span>
          </div>
        </FieldShell>
      );
    }

    if (fieldType === "number") {
      return (
        <FieldShell key={field.key}>
          {labelEl}
          {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}
          <Input
            type="number"
            value={field.value}
            onChange={(e) => updateField(index, e.target.value)}
            placeholder={`Enter ${field.key}`}
            disabled={isReadOnly}
          />
        </FieldShell>
      );
    }

    return (
      <FieldShell key={field.key}>
        {labelEl}
        {helpText && <span className="text-xs text-muted-foreground">{helpText}</span>}
        <Input
          type="text"
          value={
            field.is_secret && field.hasStoredValue && !field.value
              ? "••••••••"
              : field.value
          }
          onChange={(e) => {
            if (isReadOnly) return;
            const val = e.target.value;
            if (field.is_secret && field.hasStoredValue && !field.value) {
              updateField(index, val.replace("••••••••", ""));
            } else {
              updateField(index, val);
            }
          }}
          placeholder={
            field.is_secret
              ? "Enter secret value"
              : `Enter ${field.key}`
          }
          readOnly={isReadOnly}
        />
      </FieldShell>
    );
  }

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        LOADING CONFIGURATION...
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {fields.filter((f) => f.key !== "PLUGIN_PORT" && f.key !== "PLUGIN_DATA_PATH").length === 0 && (
        <div className="rounded-md border bg-muted/30 p-6 text-center text-sm text-muted-foreground">
          No configuration options available for this plugin.
        </div>
      )}

      {fields.map((field, index) => {
        // Hide system-injected env vars that are not user-configurable.
        if (field.key === "PLUGIN_PORT" || field.key === "PLUGIN_DATA_PATH") return null;
        if (field.schema?.visible_when) {
          const dep = fields.find(
            (f) => f.key === field.schema!.visible_when!.field
          );
          if (dep && dep.value !== field.schema.visible_when.value) {
            return null;
          }
        }
        return renderField(field, index);
      })}

      {extraSections.length > 0 && extraSections.map((section, idx) => (
        <ReadonlySection
          key={section.name}
          section={section}
          headerRight={idx === 0 ? (
            <Button variant="outline" size="sm" onClick={triggerRefresh}>
              <RefreshCw className="h-3 w-3" />
              Refresh ({refreshCountdown}s)
            </Button>
          ) : undefined}
        />
      ))}

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {saveSuccess && (
        <Alert>
          <AlertDescription>Configuration saved. Changes are now active.</AlertDescription>
        </Alert>
      )}

      {fields.length > 0 && (
        <div>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? (
              <>
                <Loader2 className="h-4 w-4 animate-spin" />
                SAVING...
              </>
            ) : (
              "SAVE CONFIGURATION"
            )}
          </Button>
        </div>
      )}
    </div>
  );
}
