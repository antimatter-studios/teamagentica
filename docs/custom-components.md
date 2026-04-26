# Web Dashboard — Custom Components

The web dashboard is built on [shadcn/ui](https://ui.shadcn.com). Every UI primitive
(`Button`, `Card`, `Dialog`, `Input`, `Select`, `Table`, `Tabs`, `Sheet`, etc.) lives
in [`plugins/system-web-dashboard/web/src/components/ui/`](../plugins/system-web-dashboard/web/src/components/ui/) and is
the canonical version. **Always reach for a shadcn primitive first.**

This document lists the components that remain custom. Each entry explains why
the component cannot be replaced by a stock shadcn primitive. Anything not on
this list should be expressed as composition of shadcn primitives + Tailwind
utilities — do not introduce new bespoke styled components.

## Theming

Themes are defined in [`plugins/system-web-dashboard/web/src/index.css`](../plugins/system-web-dashboard/web/src/index.css)
using the canonical shadcn token set (`--background`, `--foreground`, `--card`,
`--primary`, `--secondary`, `--muted`, `--accent`, `--destructive`, `--border`,
`--input`, `--ring`, etc.) declared in HSL channel format. To apply a theme from
[ui.shadcn.com/themes](https://ui.shadcn.com/themes) or
[tweakcn.com](https://tweakcn.com), paste its CSS variable block into a new
`[data-theme="..."]` selector and add it to `THEMES` in
[`plugins/system-web-dashboard/web/src/hooks/useTheme.ts`](../plugins/system-web-dashboard/web/src/hooks/useTheme.ts).
No further wiring needed.

## Custom components — and why they stay

### Behavioral compositions over shadcn primitives

These wrap shadcn primitives to encode a behavior shadcn does not ship. They are
not stylistic duplication — deleting them would push the same logic into every
caller.

| Component | Why it stays |
|---|---|
| `ConfirmDialog.tsx` | Imperative `{open, title, message, onConfirm}` API over shadcn `AlertDialog`. Removes ~10 lines of boilerplate per caller (4 callers). |
| `TimeoutButton.tsx` | Wraps shadcn `Button` with an N-second post-click auto-disable timer to prevent double-submission. Pure behavior. |
| `SaveButton.tsx` | Thin configuration of `TimeoutButton` (5s, check icon, "Save" label) used by every save flow. |
| `ProgressBar.tsx` | shadcn `Progress` + absolute-positioned percentage label. The Progress primitive intentionally has no built-in label. |

### Heavy-library integrations

Each of these is the dashboard's contract with a third-party library that has no
shadcn equivalent. The visual shell (cards, buttons, inputs, modals) inside
each is shadcn — only the library-specific surface is custom.

| Component | Library / reason |
|---|---|
| `KoiBackground.tsx` | vanta + three.js animated 3D canvas |
| `FolderTree.tsx` | Virtualised filesystem tree (react-arborist / @headless-tree/react) |
| `KanbanBoard.tsx` | dnd-kit drag-and-drop board logic. The columns/cards/dialogs/forms inside are shadcn. |
| `CostDashboard.tsx` | recharts time-series + breakdown charts. KPI tiles, tables, and filters are shadcn. |
| `CodeEditor.tsx` | Iframe lifecycle + workspace mount + port polling for the embedded editor |
| `previews/MarkdownPreview.tsx` | react-markdown + remark-gfm |
| `previews/PdfPreview.tsx` | Native `<object>` PDF renderer |
| `previews/VideoPreview.tsx` | Native `<video>` element |
| `previews/ImagePreview.tsx` | Image rendering with blob URL lifecycle |
| `previews/TextPreview.tsx` | Plain-text rendering with line wrapping |
| `previews/DefaultPreview.tsx` | Fallback for unsupported file types |

### Live-stream / domain renderers

Stateful surfaces tied to live event streams or chat-specific behavior. shadcn
provides their building blocks (Card, ScrollArea, Badge) but the streaming/auto-
scroll/in-flight-tracking logic is domain-specific.

| Component | Why it stays |
|---|---|
| `Chat.tsx` | SSE message streaming, auto-scroll, in-flight task tracking, conversation switching |
| `MemoryExplorer.tsx` | Live memory event rendering |
| `DebugConsole.tsx` | Live event log rendering with type-color dispatch |
| `UploadQueue.tsx` | Upload progress + cancellation queue, dnd-kit file drop |
| `ChatAttachmentPreview.tsx` | Type-dispatching wrapper around the `previews/` set |
| `FileViewer.tsx` | Type-dispatching wrapper around the `previews/` set |

### Schema-driven forms

| Component | Why it stays |
|---|---|
| `PluginConfigForm.tsx` | Dynamic form rendering driven by a plugin's runtime JSON schema. Cannot be expressed as a static shadcn `Form` because field shapes are unknown until fetch. Includes `TunnelPicker`, model lists, bot-token sub-renderers — all schema-dispatched. |
| `TunnelPicker.tsx` | Network-tunnel selection (used inside `PluginConfigForm`). Holds tunnel-discovery state and validation that doesn't fit a generic Select. |

## Rules going forward

1. New UI ⇒ start from `@/components/ui/*`. Compose with Tailwind utilities and shadcn theme tokens (`bg-card`, `text-muted-foreground`, `border-input`, `bg-destructive`, etc.).
2. No bespoke CSS class names (`.foo-button`, `.bar-panel`). [`src/index.css`](../plugins/system-web-dashboard/web/src/index.css) is for theme tokens and base resets only.
3. No inline `style={{ color, padding, border, … }}` for static values. Inline `style` is allowed only for genuinely dynamic values (drag transforms, dynamic grid columns, data-driven colors).
4. To add a new "wrapper that does something specific," confirm it encodes a behavior — not just styling — and add it to this document.
5. To change the look-and-feel, swap the theme block in [`src/index.css`](../plugins/system-web-dashboard/web/src/index.css). Do not introduce per-component overrides.
