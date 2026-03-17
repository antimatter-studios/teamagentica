import type { HttpTransport } from "./client.js";

const ROUTE = "/api/route/infra-workspace-manager";

export interface Environment {
  plugin_id: string;
  name: string;
  description: string;
  image: string;
  port: number;
  icon?: string;
}

export interface Workspace {
  id: string;
  name: string;
  environment: string;
  status: string;
  subdomain: string;
  url: string;
  volume_name: string;
}

export interface Volume {
  name: string;
  size_bytes: number;
  created_at: string;
  is_empty: boolean;
  has_workspace: boolean;
  tags: string[];
  git_remote: string;
  extensions: string[];
}

export class WorkspacesAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async listEnvironments(): Promise<Environment[]> {
    const res = await this.http.get<{ environments: Environment[] }>(
      `${ROUTE}/environments`
    );
    return res.environments || [];
  }

  async listWorkspaces(): Promise<Workspace[]> {
    const res = await this.http.get<{ workspaces: Workspace[] }>(
      `${ROUTE}/workspaces`
    );
    return res.workspaces || [];
  }

  async listVolumes(): Promise<Volume[]> {
    const res = await this.http.get<{ volumes: Volume[] }>(
      `${ROUTE}/volumes`
    );
    return res.volumes || [];
  }

  async createWorkspace(data: {
    name: string;
    environment_id: string;
    git_repo?: string;
    volume_name?: string;
  }): Promise<void> {
    return this.http.post(`${ROUTE}/workspaces`, data);
  }

  async deleteWorkspace(id: string): Promise<void> {
    return this.http.delete(`${ROUTE}/workspaces/${id}`);
  }

  async renameWorkspace(id: string, name: string): Promise<void> {
    return this.http.patch(`${ROUTE}/workspaces/${id}`, { name });
  }

  async startWorkspace(id: string): Promise<void> {
    return this.http.post(`${ROUTE}/workspaces/${id}/start`, {});
  }

  async deleteVolume(name: string): Promise<void> {
    return this.http.delete(`${ROUTE}/volumes/${name}`);
  }

  iframeSrc(workspaceId: string): string {
    return `${this.http.baseUrl}/ws/${workspaceId}/`;
  }

  portProxyUrl(workspaceId: string, port: number): string {
    return `${this.http.baseUrl}/ws/${workspaceId}/proxy/${port}/`;
  }

  async fetchPorts(workspaceId: string): Promise<number[]> {
    try {
      const res = await this.http.get<{ ports: number[] }>(
        `/ws/${workspaceId}/ports`
      );
      return (res.ports || []).sort((a: number, b: number) => a - b);
    } catch {
      return [];
    }
  }
}
