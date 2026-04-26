import { useEffect, useState, type ReactNode } from "react";
import { useUserStore } from "../stores/userStore";
import type { UserDetails, ServiceToken } from "@teamagentica/api-client";
import { Plus, ScrollText } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Checkbox } from "@/components/ui/checkbox";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Separator } from "@/components/ui/separator";

type View = "users" | "tokens" | "audit" | "new-user" | "new-token" | "edit-user" | "user-detail";

export default function Users() {
  const {
    users, tokens, auditLogs, auditTotal, loading, error: storeError,
    fetch, fetchAudit,
    updateUser, banUser, deleteUser, createUser,
    createToken, revokeToken,
  } = useUserStore();

  const [view, setView] = useState<View>("users");
  const [error, setError] = useState("");

  // --- Selected items ---
  const [selectedUser, setSelectedUser] = useState<UserDetails | null>(null);
  const [selectedToken, setSelectedToken] = useState<ServiceToken | null>(null);

  // --- Edit user ---
  const [editDisplayName, setEditDisplayName] = useState("");
  const [editRole, setEditRole] = useState("");

  // --- New user ---
  const [newEmail, setNewEmail] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [newDisplayName, setNewDisplayName] = useState("");

  // --- New service token ---
  const [tokenName, setTokenName] = useState("");
  const [tokenCaps, setTokenCaps] = useState<string[]>(["plugins:search"]);
  const [tokenDays, setTokenDays] = useState(365);
  const [createdToken, setCreatedToken] = useState("");

  // --- Ban modal ---
  const [banTarget, setBanTarget] = useState<UserDetails | null>(null);
  const [banReason, setBanReason] = useState("");

  useEffect(() => { fetch(); }, [fetch]);

  // Keep selectedUser in sync with store data
  useEffect(() => {
    if (selectedUser) {
      const fresh = users.find((u) => u.id === selectedUser.id);
      if (fresh) setSelectedUser(fresh);
    }
  }, [users, selectedUser?.id]);

  // --- User actions ---
  const handleEditUser = (u: UserDetails) => {
    setSelectedUser(u);
    setEditDisplayName(u.display_name);
    setEditRole(u.role);
    setView("edit-user");
  };

  const handleSaveUser = async () => {
    if (!selectedUser) return;
    try {
      setError("");
      await updateUser(Number(selectedUser.id), {
        display_name: editDisplayName,
        role: editRole,
      });
      setView("user-detail");
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleBanUser = async () => {
    if (!banTarget) return;
    try {
      setError("");
      await banUser(Number(banTarget.id), !banTarget.banned, banReason);
      setBanTarget(null);
      setBanReason("");
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleDeleteUser = async (u: UserDetails) => {
    if (!confirm(`Delete user ${u.email}? This cannot be undone.`)) return;
    try {
      setError("");
      await deleteUser(Number(u.id));
      if (selectedUser?.id === u.id) {
        setSelectedUser(null);
        setView("users");
      }
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleCreateUser = async () => {
    try {
      setError("");
      await createUser(newEmail, newPassword, newDisplayName);
      setNewEmail("");
      setNewPassword("");
      setNewDisplayName("");
      setView("users");
    } catch (e: any) {
      setError(e.message);
    }
  };

  // --- Token actions ---
  const handleCreateToken = async () => {
    try {
      setError("");
      const token = await createToken(tokenName, tokenCaps, tokenDays);
      setCreatedToken(token);
      setTokenName("");
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleRevokeToken = async (id: number) => {
    if (!confirm("Revoke this service token?")) return;
    try {
      setError("");
      await revokeToken(id);
    } catch (e: any) {
      setError(e.message);
    }
  };

  const toggleCap = (cap: string) => {
    setTokenCaps((prev) =>
      prev.includes(cap) ? prev.filter((c) => c !== cap) : [...prev, cap]
    );
  };

  const selectUserDetail = (u: UserDetails) => {
    setSelectedUser(u);
    setView("user-detail");
  };

  const displayError = error || storeError || "";

  if (loading) {
    return (
      <div className="p-6">
        <div className="text-muted-foreground">Loading user data…</div>
      </div>
    );
  }

  const roleVariant = (role: string) =>
    role === "admin" ? "default" : "secondary";

  return (
    <div className="flex h-full">
      {/* ===== LEFT SIDEBAR ===== */}
      <aside className="flex w-72 flex-col border-r bg-card">
        <div className="flex-1 overflow-y-auto p-3 space-y-6">
          {/* Users group */}
          <div className="space-y-2">
            <div className="flex items-center justify-between px-2">
              <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Users</span>
              <Badge variant="secondary">{users.length}</Badge>
            </div>
            <Button
              variant="ghost"
              size="sm"
              className="w-full justify-start"
              onClick={() => { setView("new-user"); setError(""); }}
            >
              <Plus className="mr-2 h-4 w-4" /> Add User
            </Button>
            <div className="space-y-1">
              {users.map((u) => {
                const active = view === "user-detail" && selectedUser?.id === u.id;
                return (
                  <button
                    key={u.id}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-accent",
                      active && "bg-accent text-accent-foreground"
                    )}
                    onClick={() => selectUserDetail(u)}
                  >
                    <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
                      {(u.display_name || u.email).charAt(0).toUpperCase()}
                    </span>
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate font-medium">{u.display_name || u.email.split("@")[0]}</span>
                      <span className="truncate text-xs text-muted-foreground">{u.email}</span>
                    </span>
                    <span
                      className={cn(
                        "h-2 w-2 shrink-0 rounded-full",
                        u.banned ? "bg-destructive" : "bg-green-500"
                      )}
                    />
                  </button>
                );
              })}
            </div>
          </div>

          {/* Service Accounts group */}
          <div className="space-y-2">
            <div className="flex items-center justify-between px-2">
              <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Service Accounts</span>
              <Badge variant="secondary">{tokens.length}</Badge>
            </div>
            <Button
              variant="ghost"
              size="sm"
              className="w-full justify-start"
              onClick={() => { setView("new-token"); setCreatedToken(""); setError(""); }}
            >
              <Plus className="mr-2 h-4 w-4" /> Add Service Account
            </Button>
            <div className="space-y-1">
              {tokens.map((t) => {
                const active = selectedToken?.id === t.id && view === "tokens";
                return (
                  <button
                    key={t.id}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-accent",
                      active && "bg-accent text-accent-foreground"
                    )}
                    onClick={() => { setSelectedToken(t); setView("tokens"); }}
                  >
                    <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-secondary text-xs font-semibold text-secondary-foreground">
                      S
                    </span>
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate font-medium">{t.name}</span>
                      <span className="truncate text-xs text-muted-foreground">
                        {t.revoked ? "Revoked" : `Expires ${new Date(t.expires_at).toLocaleDateString()}`}
                      </span>
                    </span>
                    <span
                      className={cn(
                        "h-2 w-2 shrink-0 rounded-full",
                        t.revoked ? "bg-destructive" : "bg-green-500"
                      )}
                    />
                  </button>
                );
              })}
            </div>
          </div>
        </div>

        <Separator />

        {/* Audit log at bottom */}
        <div className="p-3">
          <Button
            variant={view === "audit" ? "secondary" : "ghost"}
            size="sm"
            className="w-full justify-start"
            onClick={() => { setView("audit"); fetchAudit(); }}
          >
            <ScrollText className="mr-2 h-4 w-4" />
            Audit Log
            {auditTotal > 0 && (
              <Badge variant="secondary" className="ml-auto">{auditTotal}</Badge>
            )}
          </Button>
        </div>
      </aside>

      {/* ===== MAIN CONTENT ===== */}
      <main className="flex-1 overflow-y-auto p-6 space-y-4">
        {displayError && (
          <Alert variant="destructive">
            <AlertDescription>{displayError}</AlertDescription>
          </Alert>
        )}

        {/* --- Users list (default) --- */}
        {view === "users" && (
          <Card>
            <CardHeader>
              <CardTitle>Users</CardTitle>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>ID</TableHead>
                    <TableHead>Email</TableHead>
                    <TableHead>Display Name</TableHead>
                    <TableHead>Role</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Created</TableHead>
                    <TableHead>Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.map((u) => (
                    <TableRow
                      key={u.id}
                      className="cursor-pointer"
                      onClick={() => selectUserDetail(u)}
                    >
                      <TableCell>{u.id}</TableCell>
                      <TableCell>{u.email}</TableCell>
                      <TableCell>{u.display_name || "—"}</TableCell>
                      <TableCell>
                        <Badge variant={roleVariant(u.role)}>{u.role.toUpperCase()}</Badge>
                      </TableCell>
                      <TableCell>
                        {u.banned ? (
                          <Badge variant="destructive">BANNED</Badge>
                        ) : (
                          <Badge variant="outline" className="border-green-600 text-green-600">Active</Badge>
                        )}
                      </TableCell>
                      <TableCell>{new Date(u.created_at).toLocaleDateString()}</TableCell>
                      <TableCell onClick={(e) => e.stopPropagation()}>
                        <div className="flex gap-1">
                          <Button size="sm" variant="ghost" onClick={() => handleEditUser(u)}>Edit</Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => { setBanTarget(u); setBanReason(u.ban_reason || ""); }}
                          >
                            {u.banned ? "Unban" : "Ban"}
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            className="text-destructive hover:text-destructive"
                            onClick={() => handleDeleteUser(u)}
                          >
                            Delete
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        )}

        {/* --- User detail --- */}
        {view === "user-detail" && selectedUser && (
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0">
              <CardTitle>{selectedUser.display_name || selectedUser.email}</CardTitle>
              <div className="flex gap-2">
                <Button size="sm" variant="outline" onClick={() => handleEditUser(selectedUser)}>Edit</Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => { setBanTarget(selectedUser); setBanReason(selectedUser.ban_reason || ""); }}
                >
                  {selectedUser.banned ? "Unban" : "Ban"}
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  onClick={() => handleDeleteUser(selectedUser)}
                >
                  Delete
                </Button>
              </div>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <DetailField label="Email" value={selectedUser.email} />
                <DetailField label="Display Name" value={selectedUser.display_name || "—"} />
                <DetailField
                  label="Role"
                  value={<Badge variant={roleVariant(selectedUser.role)}>{selectedUser.role.toUpperCase()}</Badge>}
                />
                <DetailField
                  label="Status"
                  value={
                    selectedUser.banned ? (
                      <Badge variant="destructive">
                        BANNED{selectedUser.ban_reason ? ` — ${selectedUser.ban_reason}` : ""}
                      </Badge>
                    ) : (
                      <Badge variant="outline" className="border-green-600 text-green-600">Active</Badge>
                    )
                  }
                />
                <DetailField label="Created" value={new Date(selectedUser.created_at).toLocaleString()} />
                <DetailField label="Updated" value={new Date(selectedUser.updated_at).toLocaleString()} />
              </div>
            </CardContent>
          </Card>
        )}

        {/* --- Edit user form --- */}
        {view === "edit-user" && selectedUser && (
          <Card>
            <CardHeader>
              <CardTitle>Edit User</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="space-y-4 max-w-md">
                <div className="space-y-2">
                  <Label>Email</Label>
                  <div className="text-sm text-muted-foreground">{selectedUser.email}</div>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="edit-display-name">Display Name</Label>
                  <Input
                    id="edit-display-name"
                    value={editDisplayName}
                    onChange={(e) => setEditDisplayName(e.target.value)}
                    placeholder="Enter display name"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Role</Label>
                  <Select value={editRole} onValueChange={setEditRole}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="admin">Admin</SelectItem>
                      <SelectItem value="user">User</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex justify-end gap-2 pt-2">
                  <Button variant="outline" onClick={() => setView("user-detail")}>Cancel</Button>
                  <Button onClick={handleSaveUser}>Save Changes</Button>
                </div>
              </div>
            </CardContent>
          </Card>
        )}

        {/* --- New user form --- */}
        {view === "new-user" && (
          <Card>
            <CardHeader>
              <CardTitle>Create User</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="space-y-4 max-w-md">
                <div className="space-y-2">
                  <Label htmlFor="new-email">Email</Label>
                  <Input
                    id="new-email"
                    type="email"
                    value={newEmail}
                    onChange={(e) => setNewEmail(e.target.value)}
                    placeholder="user@example.com"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="new-display-name">Display Name</Label>
                  <Input
                    id="new-display-name"
                    value={newDisplayName}
                    onChange={(e) => setNewDisplayName(e.target.value)}
                    placeholder="John Doe"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="new-password">Password</Label>
                  <Input
                    id="new-password"
                    type="password"
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    placeholder="Minimum 8 characters"
                  />
                  {newPassword.length > 0 && newPassword.length < 8 && (
                    <p className="text-xs text-destructive">Password must be at least 8 characters</p>
                  )}
                </div>
                <div className="flex justify-end gap-2 pt-2">
                  <Button variant="outline" onClick={() => setView("users")}>Cancel</Button>
                  <Button
                    onClick={handleCreateUser}
                    disabled={!newEmail || newPassword.length < 8}
                  >
                    Create User
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>
        )}

        {/* --- Token detail --- */}
        {view === "tokens" && selectedToken && (() => {
          let caps: string[] = [];
          try { caps = JSON.parse(selectedToken.capabilities); } catch { /* */ }
          return (
            <Card>
              <CardHeader className="flex flex-row items-center justify-between space-y-0">
                <CardTitle>{selectedToken.name}</CardTitle>
                <div className="flex gap-2">
                  {!selectedToken.revoked && (
                    <Button
                      size="sm"
                      variant="destructive"
                      onClick={() => handleRevokeToken(selectedToken.id)}
                    >
                      Revoke
                    </Button>
                  )}
                </div>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <DetailField
                    label="Status"
                    value={
                      selectedToken.revoked ? (
                        <Badge variant="destructive">REVOKED</Badge>
                      ) : (
                        <Badge variant="outline" className="border-green-600 text-green-600">Active</Badge>
                      )
                    }
                  />
                  <DetailField
                    label="Expires"
                    value={new Date(selectedToken.expires_at).toLocaleString()}
                  />
                  <div className="space-y-2 sm:col-span-2">
                    <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Capabilities</span>
                    <div className="flex flex-wrap gap-2">
                      {caps.map((c) => (
                        <Badge key={c} variant="secondary">{c}</Badge>
                      ))}
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>
          );
        })()}

        {/* --- New service token form --- */}
        {view === "new-token" && (
          <Card>
            <CardHeader>
              <CardTitle>Create Service Account</CardTitle>
            </CardHeader>
            <CardContent>
              {createdToken ? (
                <div className="space-y-4 max-w-lg">
                  <Alert>
                    <AlertDescription>
                      <div className="mb-2 font-medium">Token created — copy now, it won't be shown again:</div>
                      <code className="block break-all rounded bg-muted px-2 py-1 font-mono text-xs">
                        {createdToken}
                      </code>
                    </AlertDescription>
                  </Alert>
                  <div className="flex justify-end">
                    <Button
                      variant="outline"
                      onClick={() => { setCreatedToken(""); setView("users"); }}
                    >
                      Done
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="space-y-4 max-w-md">
                  <div className="space-y-2">
                    <Label htmlFor="token-name">Token Name</Label>
                    <Input
                      id="token-name"
                      value={tokenName}
                      onChange={(e) => setTokenName(e.target.value)}
                      placeholder="e.g. CI Pipeline, Monitoring Bot"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="token-days">Expiry (days)</Label>
                    <Input
                      id="token-days"
                      type="number"
                      value={tokenDays}
                      onChange={(e) => setTokenDays(Number(e.target.value))}
                      min={1}
                    />
                  </div>
                  <div className="space-y-2">
                    <Label>Capabilities</Label>
                    <div className="grid grid-cols-2 gap-2">
                      {["plugins:search", "plugins:manage", "users:read", "system:admin"].map((cap) => (
                        <label key={cap} className="flex cursor-pointer items-center gap-2 text-sm">
                          <Checkbox
                            checked={tokenCaps.includes(cap)}
                            onCheckedChange={() => toggleCap(cap)}
                          />
                          <span>{cap}</span>
                        </label>
                      ))}
                    </div>
                  </div>
                  <div className="flex justify-end gap-2 pt-2">
                    <Button variant="outline" onClick={() => setView("users")}>Cancel</Button>
                    <Button
                      onClick={handleCreateToken}
                      disabled={!tokenName || tokenCaps.length === 0}
                    >
                      Create Token
                    </Button>
                  </div>
                </div>
              )}
            </CardContent>
          </Card>
        )}

        {/* --- Audit log --- */}
        {view === "audit" && (
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0">
              <CardTitle>Audit Log</CardTitle>
              <Button size="sm" variant="outline" onClick={fetchAudit}>Refresh</Button>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Time</TableHead>
                    <TableHead>Action</TableHead>
                    <TableHead>Actor</TableHead>
                    <TableHead>Resource</TableHead>
                    <TableHead>IP</TableHead>
                    <TableHead>Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {auditLogs.map((log) => (
                    <TableRow key={log.id}>
                      <TableCell className="whitespace-nowrap text-xs">
                        {new Date(log.timestamp).toLocaleString()}
                      </TableCell>
                      <TableCell>
                        <Badge variant="secondary">{log.action}</Badge>
                      </TableCell>
                      <TableCell className="text-xs">{log.actor_type}:{log.actor_id}</TableCell>
                      <TableCell className="text-xs">{log.resource || "—"}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">{log.ip || "—"}</TableCell>
                      <TableCell>
                        {log.success ? (
                          <Badge variant="outline" className="border-green-600 text-green-600">OK</Badge>
                        ) : (
                          <Badge variant="destructive">FAIL</Badge>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                  {auditLogs.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={6} className="text-center text-muted-foreground">
                        No audit logs
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        )}
      </main>

      {/* ===== BAN DIALOG ===== */}
      <Dialog open={!!banTarget} onOpenChange={(open) => { if (!open) setBanTarget(null); }}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>
              {banTarget?.banned ? "Unban" : "Ban"} User: {banTarget?.email}
            </DialogTitle>
          </DialogHeader>
          {banTarget && !banTarget.banned && (
            <div className="space-y-2">
              <Label htmlFor="ban-reason">Reason</Label>
              <Input
                id="ban-reason"
                value={banReason}
                onChange={(e) => setBanReason(e.target.value)}
                placeholder="Optional ban reason"
              />
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setBanTarget(null)}>Cancel</Button>
            <Button
              variant={banTarget?.banned ? "default" : "destructive"}
              onClick={handleBanUser}
            >
              {banTarget?.banned ? "Unban" : "Ban User"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function DetailField({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="space-y-1">
      <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{label}</span>
      <div className="text-sm">{value}</div>
    </div>
  );
}
