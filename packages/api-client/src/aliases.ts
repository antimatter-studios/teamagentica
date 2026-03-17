import type { HttpTransport } from "./client.js";

export interface AliasInfo {
  name: string;
  target: string;
  plugin_id: string;
  capabilities: string[];
}

export class AliasesAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async list(): Promise<AliasInfo[]> {
    const res = await this.http.get<{ aliases: AliasInfo[] }>("/api/aliases");
    return res.aliases || [];
  }
}
