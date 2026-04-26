import { HttpTransport, type ClientConfig } from "./client.js";
import { AuthAPI } from "./auth.js";
import { PluginsAPI } from "./plugins.js";
import { CostsAPI } from "./costs.js";
import { AliasesAPI } from "./aliases.js";
import { EventsAPI } from "./events.js";
import { WorkspacesAPI } from "./workspaces.js";
import { TunnelsAPI } from "./tunnels.js";
import { MarketplaceAPI } from "./marketplace.js";
import { ChatAPI } from "./chat.js";
import { FilesAPI } from "./files.js";
import { TasksAPI } from "./tasks.js";
import { AgentRegistryAPI } from "./agents.js";
import { UsersAPI } from "./users.js";
import { SchedulerAPI } from "./scheduler.js";
import { AgentAPI } from "./agent-registry.js";
import { MemoryAPI } from "./memory.js";

export class TeamAgenticaClient {
  readonly http: HttpTransport;
  readonly auth: AuthAPI;
  readonly plugins: PluginsAPI;
  readonly costs: CostsAPI;
  readonly aliases: AliasesAPI;
  readonly events: EventsAPI;
  readonly workspaces: WorkspacesAPI;
  readonly tunnels: TunnelsAPI;
  readonly marketplace: MarketplaceAPI;
  readonly chat: ChatAPI;
  readonly files: FilesAPI;
  readonly tasks: TasksAPI;
  readonly agents: AgentRegistryAPI;
  readonly users: UsersAPI;
  readonly scheduler: SchedulerAPI;
  readonly agentRegistry: AgentAPI;
  readonly memory: MemoryAPI;

  constructor(config: ClientConfig) {
    this.http = new HttpTransport(config);
    this.auth = new AuthAPI(this.http);
    this.plugins = new PluginsAPI(this.http);
    this.costs = new CostsAPI(this.http);
    this.aliases = new AliasesAPI(this.http);
    this.events = new EventsAPI(config.baseUrl);
    this.workspaces = new WorkspacesAPI(this.http);
    this.tunnels = new TunnelsAPI(this.http);
    this.marketplace = new MarketplaceAPI(this.http);
    this.chat = new ChatAPI(this.http);
    this.files = new FilesAPI(this.http, (cap) => this.plugins.search(cap));
    this.tasks = new TasksAPI(this.http);
    this.agents = new AgentRegistryAPI(this.http);
    this.users = new UsersAPI(this.http);
    this.scheduler = new SchedulerAPI(this.http);
    this.agentRegistry = new AgentAPI(this.http);
    this.memory = new MemoryAPI(this.http);
  }

  get baseUrl(): string {
    return this.http.baseUrl;
  }

  setToken(token: string | null): void {
    this.http.setToken(token);
  }
}

// Re-export everything
export type { ClientConfig } from "./client.js";
export { HttpTransport } from "./client.js";

export type { User, AuthResponse } from "./auth.js";
export { AuthAPI } from "./auth.js";

export type {
  Plugin,
  PluginConfigEntry,
  ConfigSchemaField,
  OAuthStatus,
  OAuthDeviceCode,
  OAuthPollResult,
} from "./plugins.js";
export { PluginsAPI, parseConfigSchema, parseCapabilities } from "./plugins.js";

export type {
  ModelPrice,
  TokenUsageRecord,
  RequestUsageRecord,
  UsageRecord,
  CostExplorerRecord,
  CostExplorerUser,
  ExternalUserMapping,
} from "./costs.js";
export { CostsAPI, isTokenRecord } from "./costs.js";

export type { AliasInfo } from "./aliases.js";
export { AliasesAPI } from "./aliases.js";

export { EventsAPI } from "./events.js";

export type { Environment, Workspace, Disk, WorkspaceDisk, WorkspaceOptions, WorkspaceOptionsUpdate } from "./workspaces.js";
export { WorkspacesAPI } from "./workspaces.js";

export type { Tunnel, TunnelStatus, CreateTunnelRequest } from "./tunnels.js";
export { TunnelsAPI } from "./tunnels.js";

export type {
  MarketplacePlugin,
  MarketplaceGroup,
  MarketplaceProvider,
} from "./marketplace.js";
export { MarketplaceAPI } from "./marketplace.js";

export type { Agent, Conversation, ChatMessage, Attachment } from "./chat.js";
export { ChatAPI } from "./chat.js";

export type { StorageFile, BrowseResult } from "./files.js";
export { FilesAPI, formatBytes, filenameFromKey, folderName } from "./files.js";

export type { Board, Column, Epic, Card, Comment } from "./tasks.js";
export { TasksAPI } from "./tasks.js";

export type { RegistryAlias, AgentType, CreateAliasRequest, UpdateAliasRequest, PluginModels } from "./agents.js";
export { AgentRegistryAPI } from "./agents.js";

export type { UserDetails, ServiceToken, ExternalUserMapping as UserExternalMapping, AuditLogEntry } from "./users.js";
export { UsersAPI } from "./users.js";

export type { ScheduledEvent, EventLogEntry, CreateEventRequest, UpdateEventRequest } from "./scheduler.js";
export { SchedulerAPI } from "./scheduler.js";

export type { AgentEntry, CreateAgentEntryRequest, UpdateAgentEntryRequest } from "./agent-registry.js";
export { AgentAPI } from "./agent-registry.js";

export type { Memory, MemoryEntity, LCMConversation, LCMMessage } from "./memory.js";
export { MemoryAPI } from "./memory.js";
