export class EventsAPI {
  constructor(private baseUrl: string) {}

  streamUrl(): string {
    return `${this.baseUrl}/api/debug/events`;
  }

  historyUrl(): string {
    return `${this.baseUrl}/api/debug/history`;
  }
}
