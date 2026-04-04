export class EventsAPI {
  private baseUrl: string;
  constructor(baseUrl: string) { this.baseUrl = baseUrl; }

  streamUrl(): string {
    return `${this.baseUrl}/api/route/infra-redis/events/stream`;
  }

  historyUrl(): string {
    return `${this.baseUrl}/api/route/infra-redis/events/history`;
  }

  /** @deprecated Use streamUrl() — kept for backward compat */
  legacyStreamUrl(): string {
    return `${this.baseUrl}/api/debug/events`;
  }
}
