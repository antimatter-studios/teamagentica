export class EventsAPI {
  private baseUrl: string;
  constructor(baseUrl: string) { this.baseUrl = baseUrl; }

  streamUrl(): string {
    return `${this.baseUrl}/api/debug/events`;
  }

  historyUrl(): string {
    return `${this.baseUrl}/api/debug/history`;
  }
}
