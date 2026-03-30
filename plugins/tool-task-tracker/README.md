# tool-task-tracker

Kanban-style task boards for teams and agents. Full CRUD for boards, columns, epics, cards (tasks), and comments, with both a REST API and MCP tool endpoints for AI agent use.

## Capabilities

- `system:tasks`
- `tool:tasks`

## Configuration

No user-facing config fields.

## Data Model

- Board -> Column -> Card -> Comment hierarchy
- Board -> Epic (optional grouping for cards)
- Cards have auto-incrementing per-board numbers, card_type (task/bug), priority (low/medium/high/urgent), assignee (user ID or agent alias), labels (comma-separated), optional due date, optional epic
- Columns have a `position` float for ordering
- Soft deletes throughout; cascading deletes (board removes columns/epics/cards; column removes cards; card removes comments; epic unlinks cards)
- User names resolved via SDK user cache for enriched API responses

## REST API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/boards` | List all boards |
| POST | `/boards` | Create a board |
| GET | `/boards/:id` | Get a board |
| PUT | `/boards/:id` | Update a board |
| DELETE | `/boards/:id` | Delete a board |
| GET | `/boards/:id/columns` | List columns |
| POST | `/boards/:id/columns` | Create a column |
| PUT | `/boards/:id/columns/:cid` | Update a column |
| DELETE | `/boards/:id/columns/:cid` | Delete a column |
| GET | `/boards/:id/epics` | List epics |
| POST | `/boards/:id/epics` | Create an epic |
| PUT | `/boards/:id/epics/:eid` | Update an epic |
| DELETE | `/boards/:id/epics/:eid` | Delete an epic |
| GET | `/boards/:id/cards` | List cards |
| GET | `/boards/:id/cards/search?q=` | Search cards by title/description/labels |
| POST | `/boards/:id/cards` | Create a card |
| GET | `/boards/:id/cards/number/:num` | Get card by board-scoped number |
| PUT | `/boards/:id/cards/:cid` | Update a card |
| DELETE | `/boards/:id/cards/:cid` | Delete a card |
| GET | `/cards/:cid` | Get a single card by ID |
| GET | `/cards/:cid/comments` | List comments |
| POST | `/cards/:cid/comments` | Add a comment |
| DELETE | `/cards/:cid/comments/:cmid` | Delete a comment |

## MCP Tool Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /mcp` | Tool schema for agent discovery |
| `POST /mcp/list_boards` | List boards with columns |
| `POST /mcp/list_tasks` | List tasks grouped by status |
| `POST /mcp/list_tasks_by_status` | List tasks in a specific column |
| `POST /mcp/list_epics` | List epics on a board |
| `POST /mcp/create_epic` | Create an epic |
| `POST /mcp/update_epic` | Update an epic |
| `POST /mcp/delete_epic` | Delete an epic |
| `POST /mcp/create_task` | Create a task |
| `POST /mcp/set_task_state` | Move a task to another column |
| `POST /mcp/update_task` | Update task fields |
| `POST /mcp/search_tasks` | Search tasks by text query |
| `POST /mcp/add_comment` | Add a comment to a task |

Tools are auto-registered with the MCP server plugin when available.

## Storage

SQLite at `/data/tasks.db` via GORM. WAL mode. Card numbers are backfilled on startup for any cards with number=0.

## Port

Hardcoded to 8093.
