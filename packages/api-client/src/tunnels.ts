import type { HttpTransport } from "./client.js";

const ROUTE = "/api/route/network-traffic-manager";

export interface TunnelStatus {
  state: string;             // stopped | starting | running | error
  url?: string;
  error?: string;
  public_key?: string;       // authorized_keys-format pubkey to install on the SSH host
  endpoint?: string;         // host:port to connect to from outside
}

export interface Tunnel {
  spec: {
    name: string;
    driver: string;
    auto_start?: boolean;
    role?: string;
    target: string;
    config?: Record<string, string>;
  };
  status: TunnelStatus;
}

export interface CreateTunnelRequest {
  name: string;
  driver: string;
  auto_start?: boolean;
  role?: string;
  target: string;
  config?: Record<string, string>;
}

export class TunnelsAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async list(): Promise<Tunnel[]> {
    const res = await this.http.get<{ tunnels: Tunnel[] }>(`${ROUTE}/tunnels`);
    return res.tunnels || [];
  }

  async get(name: string): Promise<Tunnel> {
    return this.http.get<Tunnel>(`${ROUTE}/tunnels/${encodeURIComponent(name)}`);
  }

  async create(req: CreateTunnelRequest): Promise<Tunnel> {
    return this.http.post<Tunnel>(`${ROUTE}/tunnels`, req);
  }

  async delete(name: string): Promise<void> {
    return this.http.delete(`${ROUTE}/tunnels/${encodeURIComponent(name)}`);
  }

  async start(name: string): Promise<TunnelStatus> {
    return this.http.post<TunnelStatus>(`${ROUTE}/tunnels/${encodeURIComponent(name)}/start`, {});
  }

  async stop(name: string): Promise<TunnelStatus> {
    return this.http.post<TunnelStatus>(`${ROUTE}/tunnels/${encodeURIComponent(name)}/stop`, {});
  }
}
