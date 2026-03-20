# tool-task-tracker

Kanban-style task boards for teams and agents.

## Overview

A full CRUD kanban board system backed by SQLite. Supports boards, columns (statuses), cards (tasks), and comments. Exposes both a REST API for UI integration and MCP-style tool endpoints for AI agent use. Agents can list boards, create/move/update tasks, and add comments.

## Capabilities

- `system:tasks`
- `tool:tasks`

## Dependencies

None.

## Configuration

No user-facing config fields (no `config_schema` in plugin.yaml).

## API Endpoints

### REST API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/boards` | List all boards |
| POST | `/boards` | Create a board |
| GET | `/boards/:id` | Get a board |
| PUT | `/boards/:id` | Update a board |
| DELETE | `/boards/:id` | Delete a board (cascades to columns + cards) |
| GET | `/boards/:id/columns` | List columns for a board |
| POST | `/boards/:id/columns` | Create a column |
| PUT | `/boards/:id/columns/:cid` | Update a column |
| DELETE | `/boards/:id/columns/:cid` | Delete a column (cascades to cards) |
| GET | `/boards/:id/cards` | List cards for a board |
| POST | `/boards/:id/cards` | Create a card |
| PUT | `/boards/:id/cards/:cid` | Update a card |
| DELETE | `/boards/:id/cards/:cid` | Delete a card (cascades to comments) |
| GET | `/cards/:cid/comments` | List comments on a card |
| POST | `/cards/:cid/comments` | Add a comment |
| DELETE | `/cards/:cid/comments/:cmid` | Delete a comment |

### MCP Tool Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/tools` | Tool schema for agent discovery |
| POST | `/mcp/list_boards` | List boards with columns |
| POST | `/mcp/list_tasks` | List tasks grouped by status |
| POST | `/mcp/list_tasks_by_status` | List tasks in a specific column |
| POST | `/mcp/create_task` | Create a task |
| POST | `/mcp/set_task_state` | Move a task to another column |
| POST | `/mcp/update_task` | Update task fields |
| POST | `/mcp/add_comment` | Add a comment to a task |

## Events

None (no SDK event reporting).

## How It Works

- Data stored in SQLite at `/data/tasks.db` (GORM with auto-migration)
- Board -> Column -> Card -> Comment hierarchy
- Cards have priority (low/medium/high/urgent), assignee, labels (comma-separated), optional due date (unix ms)
- Columns have a `position` float for ordering
- Deletes cascade: deleting a board removes its columns and cards; deleting a column removes its cards; deleting a card removes its comments

## Gotchas / Notes

- SQLite uses `_journal_mode=DELETE` and `_synchronous=FULL` for durability
- The MCP endpoints accept JSON bodies (POST) while the REST API uses standard HTTP verbs
- No authentication or per-user isolation -- all boards are shared
- Port is hardcoded to 8093
