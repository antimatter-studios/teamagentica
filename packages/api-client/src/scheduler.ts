import type { HttpTransport } from "./client.js";

const ROUTE = "/api/route/infra-cron-scheduler";

export interface ScheduledEvent {
  id: string;
  name: string;
  text: string;
  type: "once" | "repeat";
  schedule: string;
  schedule_type: "cron" | "interval";
  trigger_type: "timer" | "event";
  event_pattern: string;
  action_type: string;
  action_config: string;
  enabled: boolean;
  fired_count: number;
  next_fire: number;
  created_at: number;
  updated_at: number;
}

export interface EventLogEntry {
  id: string;
  job_id: string;
  job_name: string;
  text: string;
  result: string;
  fired_at: number;
}

export interface CreateEventRequest {
  name: string;
  text?: string;
  type: "once" | "repeat";
  trigger_type?: "timer" | "event";
  schedule?: string;
  event_pattern?: string;
  action_type?: string;
  action_config?: string;
}

export interface UpdateEventRequest {
  name?: string;
  text?: string;
  schedule?: string;
  event_pattern?: string;
  enabled?: boolean;
  action_type?: string;
  action_config?: string;
}

export class SchedulerAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async listEvents(): Promise<{ events: ScheduledEvent[]; count: number }> {
    return this.http.get(`${ROUTE}/events`);
  }

  async getEvent(id: string): Promise<ScheduledEvent> {
    return this.http.get(`${ROUTE}/events/${id}`);
  }

  async createEvent(req: CreateEventRequest): Promise<ScheduledEvent> {
    return this.http.post(`${ROUTE}/events`, req);
  }

  async updateEvent(id: string, req: UpdateEventRequest): Promise<ScheduledEvent> {
    return this.http.put(`${ROUTE}/events/${id}`, req);
  }

  async deleteEvent(id: string): Promise<void> {
    return this.http.delete(`${ROUTE}/events/${id}`);
  }

  async getLogs(limit = 50): Promise<{ entries: EventLogEntry[]; count: number }> {
    return this.http.get(`${ROUTE}/log?limit=${limit}`);
  }
}
