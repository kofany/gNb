# gNb Panel — Plan B: SPA (React 19 frontend)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **PREREQUISITE:** Plan A (`2026-04-25-panel-A-backend.md`) must be complete and tagged `backend-v0`. Tasks below assume `gnb-panel/` repo exists with full backend, `dist/CONTRACT.md`, `src/shared/wire.ts`, and `bun run start` serves the working API.

**Goal:** Build the React 19 SPA on top of the existing panel-bun backend. Single bun process serves both: in dev, Vite (`5173`) proxies to bun-server (`3000`); in prod, bun-server serves `dist/client/` static files alongside `/api/*` and `/ws`.

**Architecture:** TanStack Router (file-based, type-safe) + TanStack Query (HTTP cache) + Zustand (client state for events feed, attach state, sidebar) + shadcn/ui (Radix + Tailwind) components copied into `src/client/components/ui/`. WS client maintains one persistent connection to `/ws`, multiplexes events into Zustand stores. All wire types imported from `@shared/wire` (no duplication).

**Tech Stack:** React 19.2.5 · Vite 8.0.10 · Tailwind 4.2.4 · shadcn 4.4.0 · TanStack Router 1.168.24 · TanStack Query 5.100.1 · Zustand 5.0.12 · cmdk · sonner · lucide-react · Vitest 4.1.5 + @testing-library/react 16.3.2 · Playwright 1.59.1.

**Spec:** [`docs/superpowers/specs/2026-04-25-panel-design.md`](../specs/2026-04-25-panel-design.md). Wire contract: `gnb-panel/docs/CONTRACT.md` + `gnb-panel/src/shared/wire.ts`.

**Cross-plan alignment:** Task 1 verifies the contract. Every component that talks to the backend imports types from `@shared/wire` — never defines its own request/response shape. If `wire.ts` ever changes, TS errors here surface immediately.

> **Note on task granularity:** UI tasks below give file paths, key tests, and behavioral specs in prose. Full component code is **not** inlined for every visual detail (component code is iterative). For tasks where logic is non-trivial (WS client, auth gate, command palette, attach view), full code is provided.

---

## Phase 0 — Contract sanity check + dev environment

### Task 1: Verify backend contract is in place

**Files:** none modified.

- [ ] **Step 1: Read both contract documents**

```bash
cat docs/CONTRACT.md
cat src/shared/wire.ts | head -200
```

- [ ] **Step 2: Boot backend, run contract verifier**

```bash
bun run scripts/contract-verify.ts
```

Expected: all rows `✓`. If any `✗`, **stop**: there is drift between Plan A and the spec — fix Plan A first.

- [ ] **Step 3: Smoke-test login + WS via bun's interactive shell**

```bash
bun --eval "
  const r = await fetch('http://127.0.0.1:3000/api/auth/login', {
    method:'POST',
    headers:{'content-type':'application/json','x-requested-with':'panel'},
    body: JSON.stringify({username:'admin', password:'<PASTE BOOTSTRAP PASSWORD>'})
  });
  console.log('login:', r.status, r.headers.get('set-cookie'));
"
```

(Boot panel-bun first via `bun run dev:server`; copy the printed bootstrap password.)

Confirm: cookie returned. Backend is ready.

- [ ] **Step 4: Commit nothing — checkpoint only**

This task is a gate. If it passes, continue.

---

### Task 2: Vite client root + index.html

**Files:**
- Create: `gnb-panel/src/client/index.html`
- Create: `gnb-panel/src/client/main.tsx`
- Create: `gnb-panel/src/client/styles/app.css`

- [ ] **Step 1: Re-verify Vite + Tailwind 4 versions live** (per project rule).

- [ ] **Step 2: Write `src/client/index.html`**

```html
<!doctype html>
<html lang="en" class="dark">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>gNb Panel</title>
  </head>
  <body class="bg-background text-foreground">
    <div id="root"></div>
    <script type="module" src="/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 3: Write `src/client/styles/app.css` (Tailwind 4 CSS-first config)**

```css
@import "tailwindcss";

@theme {
  --color-background: oklch(0.135 0 0);          /* near-black */
  --color-foreground: oklch(0.95 0 0);
  --color-card: oklch(0.18 0 0);
  --color-border: oklch(0.28 0 0);
  --color-primary: oklch(0.7 0.18 250);          /* cool blue */
  --color-accent: oklch(0.78 0.16 75);           /* amber for outbound */
  --color-destructive: oklch(0.58 0.22 25);
  --font-sans: 'Inter Variable', system-ui, sans-serif;
  --font-mono: 'JetBrains Mono', ui-monospace, monospace;
}

html, body, #root { height: 100%; margin: 0; }
```

- [ ] **Step 4: Write `src/client/main.tsx` (skeleton router + query)**

```tsx
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { RouterProvider, createRouter } from '@tanstack/react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Toaster } from 'sonner';
import { routeTree } from './routeTree.gen';
import './styles/app.css';

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, staleTime: 5_000, refetchOnWindowFocus: false } },
});

const router = createRouter({ routeTree, defaultPreload: 'intent', context: { queryClient } });
declare module '@tanstack/react-router' { interface Register { router: typeof router } }

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster theme="dark" richColors position="bottom-right" />
    </QueryClientProvider>
  </StrictMode>,
);
```

- [ ] **Step 5: Run dev**

```bash
bun run dev:client
```

Expected: Vite serves on `5173`, but compilation will fail because `routeTree.gen` doesn't exist yet. That's expected — Task 3 fixes it.

- [ ] **Step 6: Commit (intentionally non-functional yet)**

```bash
git add src/client/index.html src/client/main.tsx src/client/styles/app.css
git commit -m "feat(client): vite root + main.tsx + tailwind4 CSS-first theme"
```

---

### Task 3: TanStack Router setup + first route + dev script

**Files:**
- Create: `gnb-panel/src/client/routes/__root.tsx`
- Create: `gnb-panel/src/client/routes/index.tsx`
- Create: `gnb-panel/src/client/routeTree.gen.ts` (auto-generated)
- Create: `gnb-panel/scripts/dev.ts` (concurrent server + vite)

- [ ] **Step 1: Add TanStack Router Vite plugin to `vite.config.ts`**

```ts
import { TanStackRouterVite } from '@tanstack/router-plugin/vite';
// add to plugins:
plugins: [TanStackRouterVite({ routesDirectory: 'src/client/routes', generatedRouteTree: 'src/client/routeTree.gen.ts' }), react(), tailwindcss()],
```

(Install `@tanstack/router-plugin` if not yet pulled; verify version.)

- [ ] **Step 2: `routes/__root.tsx`**

```tsx
import { createRootRoute, Outlet } from '@tanstack/react-router';
export const Route = createRootRoute({ component: () => <Outlet /> });
```

- [ ] **Step 3: `routes/index.tsx` (placeholder dashboard)**

```tsx
import { createFileRoute } from '@tanstack/react-router';
export const Route = createFileRoute('/')({
  component: () => <div className="p-6">Dashboard placeholder</div>,
});
```

- [ ] **Step 4: `scripts/dev.ts` — spawn vite + bun server concurrently**

```ts
import { spawn } from 'bun';
const server = spawn(['bun', '--watch', 'src/server/index.ts'], { stdio: ['inherit', 'inherit', 'inherit'] });
const vite = spawn(['bunx', 'vite'], { stdio: ['inherit', 'inherit', 'inherit'] });
process.on('SIGINT', () => { server.kill(); vite.kill(); });
await Promise.all([server.exited, vite.exited]);
```

- [ ] **Step 5: Verify dev**

```bash
bun run dev
```

Visit `http://localhost:5173` → see "Dashboard placeholder".

- [ ] **Step 6: Commit**

```bash
git add src/client/routes/ src/client/routeTree.gen.ts vite.config.ts scripts/dev.ts
git commit -m "feat(client): TanStack Router root + index + dev script"
```

---

### Task 4: Initialize shadcn/ui + first components (Button, Input, Card, Dialog, Toast wiring)

**Files:**
- Create: `gnb-panel/components.json`
- Auto-create: `gnb-panel/src/client/components/ui/{button,input,label,card,dialog,form,sheet,sonner}.tsx`

- [ ] **Step 1: Run shadcn init**

```bash
bunx shadcn@latest init
```

Choose: dark mode, Tailwind 4, `src/client/components/ui` location, `@/components` alias, `@/lib/utils` alias (mapped to `src/client/lib/utils.ts`).

- [ ] **Step 2: Add baseline components**

```bash
bunx shadcn@latest add button input label card dialog form sheet sonner table dropdown-menu tabs badge tooltip skeleton scroll-area separator alert
```

- [ ] **Step 3: Commit**

```bash
git add components.json src/client/components/ src/client/lib/utils.ts
git commit -m "chore(ui): scaffold shadcn/ui components (dark theme)"
```

---

## Phase 1 — Core client libs

### Task 5: API fetch wrapper with typed errors + cookie-aware

**Files:**
- Create: `gnb-panel/src/client/lib/api.ts`
- Create: `gnb-panel/src/client/lib/api.test.ts`

- [ ] **Step 1: Failing test (mocking `fetch`)**

```ts
import { describe, it, expect, vi } from 'vitest';
import { api, ApiError } from './api';

describe('api', () => {
  it('throws ApiError with code on non-2xx', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'login required' } }), { status: 401, headers: { 'content-type': 'application/json' } })));
    await expect(api('/api/auth/me')).rejects.toBeInstanceOf(ApiError);
    try { await api('/api/auth/me'); } catch (e) { expect((e as ApiError).code).toBe('unauthorized'); }
  });
  it('returns parsed JSON on 2xx', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ x: 1 }), { status: 200, headers: { 'content-type': 'application/json' } })));
    expect(await api('/api/x')).toEqual({ x: 1 });
  });
  it('returns null on 204', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    expect(await api('/api/x')).toBeNull();
  });
});
```

- [ ] **Step 2: Implementation**

```ts
import type { ErrorCode } from '@shared/wire';

export class ApiError extends Error {
  constructor(public readonly status: number, public readonly code: ErrorCode | string, message: string, public readonly details?: Record<string, unknown>) {
    super(message);
    this.name = 'ApiError';
  }
}

export interface ApiOpts {
  method?: string;
  body?: unknown;
  signal?: AbortSignal;
}

export async function api<T = unknown>(path: string, opts: ApiOpts = {}): Promise<T> {
  const init: RequestInit = {
    method: opts.method ?? 'GET',
    credentials: 'same-origin',
    headers: { 'x-requested-with': 'panel', ...(opts.body !== undefined ? { 'content-type': 'application/json' } : {}) },
    ...(opts.body !== undefined ? { body: JSON.stringify(opts.body) } : {}),
    ...(opts.signal ? { signal: opts.signal } : {}),
  };
  const res = await fetch(path, init);
  if (res.status === 204) return null as T;
  const ct = res.headers.get('content-type') ?? '';
  const isJson = ct.includes('application/json');
  const body = isJson ? await res.json() : await res.text();
  if (!res.ok) {
    if (isJson && body && typeof body === 'object' && 'error' in body) {
      const e = (body as { error: { code: string; message: string; details?: Record<string, unknown> } }).error;
      throw new ApiError(res.status, e.code, e.message, e.details);
    }
    throw new ApiError(res.status, 'internal', typeof body === 'string' ? body : 'request failed');
  }
  return body as T;
}
```

- [ ] **Step 3: Run, expect pass; commit.**

```bash
bun test src/client/lib/api.test.ts
git add src/client/lib/api.ts src/client/lib/api.test.ts
git commit -m "feat(client): api fetch wrapper with typed ApiError"
```

---

### Task 6: WS client (auto-reconnect + cursor replay)

**Files:**
- Create: `gnb-panel/src/client/lib/ws.ts`
- Create: `gnb-panel/src/client/lib/ws.test.ts`

This module owns the single `/ws` connection. Exposes:
- `connect()` — opens WS, sends initial `events.subscribe { node_ids: 'all', cursor }` if we have one, otherwise `{ node_ids: 'all' }`.
- `subscribe(handler)` — register a callback for every incoming event.
- `request(method, params)` — issues an envelope-style request, returns a Promise resolved by the matching response.
- `attachOpen / attachClose / attachSend` — convenience wrappers.
- Tracks `lastSeq` for cursor replay across reconnects.

- [ ] **Step 1: Failing test (uses fake WebSocket via vitest mock)**

Create a tiny in-memory WS pair (`MessageChannel` won't work; use a hand-rolled `EventTarget` mock). Test cases:
- `request` resolves with matching `id` response.
- `request` rejects with `ApiError`-like error envelope.
- On reconnect, sends `events.subscribe { cursor: lastSeq }`.

- [ ] **Step 2: Implementation**

```ts
import type { ServerMsg, ClientMsg, WsMethod, SubscribeResult, EventMsg } from '@shared/wire';

type EventHandler = (e: EventMsg) => void;

interface PendingReq { resolve: (r: unknown) => void; reject: (e: Error) => void; timeout: ReturnType<typeof setTimeout>; }

export class PanelWsClient extends EventTarget {
  private ws: WebSocket | null = null;
  private nextId = 0;
  private pendings = new Map<string, PendingReq>();
  private handlers = new Set<EventHandler>();
  private lastSeq = 0;
  private retry = 0;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;

  constructor(private readonly url: string) { super(); }

  start(): void { this.connect(); }
  stop(): void {
    this.stopped = true;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    if (this.ws && this.ws.readyState === WebSocket.OPEN) this.ws.close(1000, 'stop');
  }

  onEvent(h: EventHandler): () => void {
    this.handlers.add(h);
    return () => this.handlers.delete(h);
  }

  request<R = unknown>(method: WsMethod, params: unknown, timeoutMs = 10_000): Promise<R> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return Promise.reject(new Error('ws not open'));
    const id = `c${++this.nextId}`;
    const env: ClientMsg = { type: 'request', id, method, params };
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => { this.pendings.delete(id); reject(new Error(`timeout ${method}`)); }, timeoutMs);
      this.pendings.set(id, { resolve: resolve as (r: unknown) => void, reject, timeout });
      this.ws!.send(JSON.stringify(env));
    });
  }

  private connect(): void {
    if (this.stopped) return;
    const ws = new WebSocket(this.url);
    this.ws = ws;
    ws.addEventListener('open', () => {
      this.retry = 0;
      const params = this.lastSeq > 0 ? { node_ids: 'all', cursor: this.lastSeq } : { node_ids: 'all' };
      this.request<SubscribeResult>('events.subscribe', params).then((r) => {
        this.lastSeq = r.cursor;
        if (r.gap) this.dispatchEvent(new CustomEvent('gap'));
        this.dispatchEvent(new CustomEvent('open'));
      }).catch(() => ws.close());
    });
    ws.addEventListener('message', (e) => this.handleMsg(e.data));
    ws.addEventListener('close', () => { this.cleanup(); this.scheduleReconnect(); });
    ws.addEventListener('error', () => { /* close handler will fire */ });
  }

  private cleanup(): void {
    for (const [, p] of this.pendings) { clearTimeout(p.timeout); p.reject(new Error('connection closed')); }
    this.pendings.clear();
    this.ws = null;
  }

  private scheduleReconnect(): void {
    if (this.stopped) return;
    const sched = [1000, 2000, 4000, 8000, 16000, 30000];
    const delay = sched[Math.min(this.retry, sched.length - 1)]!;
    this.retry++;
    this.dispatchEvent(new CustomEvent('reconnecting', { detail: { delay } }));
    this.retryTimer = setTimeout(() => { this.retryTimer = null; this.connect(); }, delay);
  }

  private handleMsg(raw: unknown): void {
    let msg: ServerMsg;
    try { msg = JSON.parse(typeof raw === 'string' ? raw : new TextDecoder().decode(raw as ArrayBuffer)); } catch { return; }
    if (msg.type === 'response') {
      const p = this.pendings.get(msg.id);
      if (p) { clearTimeout(p.timeout); this.pendings.delete(msg.id); p.resolve((msg as { result: unknown }).result); }
    } else if (msg.type === 'error') {
      const p = this.pendings.get(msg.id);
      if (p) { clearTimeout(p.timeout); this.pendings.delete(msg.id); p.reject(new Error(`${msg.error.code}: ${msg.error.message}`)); }
    } else if (msg.type === 'event') {
      this.lastSeq = Math.max(this.lastSeq, msg.seq);
      for (const h of this.handlers) h(msg);
    }
  }
}
```

- [ ] **Step 3: Tests + commit.**

---

### Task 7: Zustand stores (auth, sidebar, events feed, attach state)

**Files:**
- Create: `gnb-panel/src/client/stores/auth.ts` — current admin, `setMe`, `logout`
- Create: `gnb-panel/src/client/stores/events.ts` — recent events ring (last 200), grouped by node_id
- Create: `gnb-panel/src/client/stores/attaches.ts` — open attaches `{ key → { events: ScrollbackEntry[], lastTarget: string | null } }`
- Create: `gnb-panel/src/client/stores/nodes.ts` — node statuses (mirrors panel.node_status events into UI)
- Create: `gnb-panel/src/client/stores/__tests__/events.test.ts`

Use `zustand` with `persist` middleware ONLY for sidebar collapse state (not for auth/events/attaches — too volatile).

For each store:
- Define interface with state + actions.
- Test: action mutates state, multiple subscribers see updates.

- [ ] **Step 1–4 each store:** failing test → impl → pass → commit.

Commit messages:
- `feat(stores): auth store (me + setMe + clear)`
- `feat(stores): nodes store (status mirror from panel.node_status)`
- `feat(stores): events store (rolling 200-event feed)`
- `feat(stores): attaches store (key→scrollback + lastTarget)`

---

### Task 8: WS provider hook + global wire-up

**Files:**
- Create: `gnb-panel/src/client/lib/ws-provider.tsx`

A React provider that owns the singleton `PanelWsClient`, dispatches incoming events into the appropriate Zustand stores:
- `node.*` lifecycle → `events` store (push, evict oldest beyond 200)
- `panel.node_status` → `nodes` store
- `panel.heartbeat` → `nodes` store (update connected count)
- `bot.attach.*` → `attaches` store (push to scrollback for matching key)

Export `useWs()` hook returning the client (for command palette + attach view to call `request`).

Mount the provider in `routeTree.gen.ts` root (via `__root.tsx`).

Commit: `feat(client): WS provider, dispatches into Zustand stores`.

---

## Phase 2 — Auth flow

### Task 9: Auth gate + bootstrap routing

**Files:**
- Modify: `gnb-panel/src/client/routes/__root.tsx`
- Create: `gnb-panel/src/client/routes/_authed.tsx` (layout route, requires login)
- Create: `gnb-panel/src/client/routes/login.tsx`

Behavior:
- On app boot, query `/api/auth/me`.
- If 401 → redirect to `/login`.
- If 200 + `must_change_password` → redirect to `/account/change-password` (force).
- Otherwise render the authed layout with sidebar.

Use TanStack Router `beforeLoad` + `redirect()`.

Tests: integration test using @testing-library/react that:
- Mocks `/api/auth/me` 401 → renders Login.
- Mocks 200 → renders sidebar.
- Mocks 200 with `must_change_password: true` → redirects to change-password route.

Commit: `feat(client): auth gate + login/dashboard routing`.

---

### Task 10: Login page (`routes/login.tsx`)

Form (react-hook-form + zod via @hookform/resolvers):
- Inputs: username, password.
- Submit → `api('/api/auth/login', { method:'POST', body })`.
- On success: invalidate `/api/auth/me`, route to `/`.
- On `429` rate_limited: show toast.
- On `401`: form-level error.

UI: shadcn `<Card>` centered, single column, autofocus username.

Tests:
- React Testing Library: render form, fill + submit, mock API, expect `useRouter().navigate('/')` called.

Commit: `feat(client): login page`.

---

### Task 11: Forced password change page (`routes/account/change-password.tsx`)

Form (current, new, confirm). Server validates `current`. On 204:
- Refresh `/api/auth/me` (must_change → false).
- Route to `/`.

Reuse same shadcn card layout as login.

Commit: `feat(client): forced password change`.

---

## Phase 3 — App shell

### Task 12: Sidebar with live node status

**Files:**
- Create: `gnb-panel/src/client/components/Sidebar.tsx`
- Create: `gnb-panel/src/client/routes/_authed.tsx` (uses Sidebar)

Sidebar items:
- Dashboard (link `/`)
- Nodes (collapsible group)
  - For each node from `nodes` store: name + status dot (green/yellow/red/grey for connected/connecting/error/disabled) + `connected/total` count.
- Settings (link `/settings`)
- Footer: "Ctrl-K Command Palette" hint + logged-in user + logout button.

Status updates: subscribe to `nodes` store (which is updated by WS provider).

Commit: `feat(client): sidebar with live node statuses`.

---

### Task 13: Theme + global styles polish

- Verify all shadcn components inherit `--color-background` etc. from app.css.
- Add `<html class="dark">` (already in index.html).
- Add a fixed top loading bar (using `nprogress`-like component or shadcn skeleton) that lights up on TanStack Query `fetch` activity.

Commit: `style(client): dark theme polish + global loading indicator`.

---

## Phase 4 — Dashboard

### Task 14: `routes/index.tsx` — dashboard tiles + recent events

**Files:**
- Modify: `gnb-panel/src/client/routes/index.tsx`
- Create: `gnb-panel/src/client/components/dashboard/NodeTile.tsx`
- Create: `gnb-panel/src/client/components/dashboard/RecentEvents.tsx`

Layout:
- Row of NodeTiles (one per node from `nodes` store): name, status pill, connected/total bots, `last_connected_at` ago.
- Below: RecentEvents — virtualized list of last 50 events from `events` store, color-coded per type, with node-name prefix.

Click on tile → route to `/nodes/:nodeId`.

Tests: render dashboard with mocked store containing 3 nodes + 5 events; assert tiles + event rows render.

Commit: `feat(client): dashboard with node tiles + recent events feed`.

---

## Phase 5 — Nodes management

### Task 15: `routes/nodes/index.tsx` — list + add modal + test

**Files:**
- Create: `gnb-panel/src/client/routes/nodes/index.tsx`
- Create: `gnb-panel/src/client/components/nodes/AddNodeDialog.tsx`

Behaviors:
- Table (shadcn Table): name, ws_url, status, bots, last_connected_at, actions menu.
- Action menu items: Edit (rename / re-token), Disable / Enable, Delete (with confirm dialog).
- Add button → AddNodeDialog: form (name, ws_url, token) with "Test connection" button that hits `/api/nodes/test` first, shows ✓/✗.
- On submit: `POST /api/nodes` → invalidate `['nodes']` query → close dialog → toast.

TanStack Query: `useQuery(['nodes'])` polling on a 5s interval as backup (live updates via WS handle most cases).

Commit: `feat(client): nodes management table + add/edit/delete dialogs + test connection`.

---

## Phase 6 — Node detail tabs

### Task 16: `routes/nodes/$nodeId/route.tsx` — tab container

Layout: page header with node name + status, then a tab strip (Bots / Channels / Nicks / Owners / Mass / Events) backed by URL routes.

Use TanStack Router nested routes; each tab is its own file under `routes/nodes/$nodeId/`.

Commit: `feat(client): node detail layout + tab navigation`.

---

### Task 17: Bots tab (`routes/nodes/$nodeId/bots.tsx`) + bot drawer

**Files:**
- `bots.tsx` — virtualized table (use `@tanstack/react-virtual`).
- `components/bots/BotDrawer.tsx` — shadcn `<Sheet>` from right; shows bot info + action buttons.
- `components/bots/BotActionsForm.tsx` — sub-forms for change_nick, raw, say, join, part, quit.

Live updates: subscribe to `events` store; on any `node.bot_*` event for this node, invalidate `['nodes', nodeId, 'bots']` query.

Commit: `feat(client): bots table with live updates + action drawer`.

---

### Task 18: Channels tab (`routes/nodes/$nodeId/channels.tsx`)

Aggregate `bot.list` results: each unique channel → list of bots joined.

Commit: `feat(client): channels aggregated view`.

---

### Task 19: Nicks tab (`routes/nodes/$nodeId/nicks.tsx`)

Table + add / remove. On add → `POST /api/nodes/:id/nicks`. On remove → `DELETE /api/nodes/:id/nicks/:nick`. Live updates via `nicks.changed` event in `events` store.

Commit: `feat(client): nicks tab with add/remove`.

---

### Task 20: Owners tab (`routes/nodes/$nodeId/owners.tsx`)

Same shape as nicks tab. Mask URL-encoding via `Buffer.from(mask).toString('base64url')`.

Commit: `feat(client): owners tab with add/remove`.

---

### Task 21: Mass actions tab (`routes/nodes/$nodeId/mass.tsx`)

Buttons + forms for the five mass actions. Each opens a confirm dialog with explicit "this hits ALL N bots on this node" warning.

Commit: `feat(client): mass actions panel with confirmation`.

---

### Task 22: Events tab (`routes/nodes/$nodeId/events.tsx`)

Filterable live feed: filter chips for connect/disconnect/nick/join/part/kick/ban + search box. Subscribes to `events` store filtered by `node_id`.

Commit: `feat(client): per-node events tab with filters`.

---

## Phase 7 — Attach mode

### Task 23: Attach view (`routes/nodes/$nodeId/bots/$botId/attach.tsx`)

**Files:**
- `attach.tsx`
- `components/attach/Scrollback.tsx`
- `components/attach/InputBar.tsx`

Behavior:
- On mount: call `useWs().request('attach.open', { node_id, bot_id })`. On unmount: `attach.close`.
- Subscribe to `attaches` store for this `(node_id, bot_id)` key.
- Scrollback: virtualized list of entries, color-coded:
  - `bot.attach.raw_out` — yellow (outbound)
  - `bot.attach.raw_in` — white (inbound)
  - `bot.attach.privmsg/notice/join/part/...` — light grey (parsed events)
  - errors — red
- InputBar: input + send button + parser preview (show what `parseAttachInput(line, lastTarget)` produces).
- On submit: `useWs().request('attach.send', { node_id, bot_id, line })`. Update `lastTarget` from parser result.
- Esc / Ctrl-D: navigate back.
- Header: "Attached to <nick> on <node-name>" + Detach button.

Re-attach on WS reconnect: WS provider tracks open attaches in `attaches` store; on `open` event, re-issue `attach.open` for each.

Tests:
- Render with mocked store; click input + type `/msg #x hi` + submit → expect `request('attach.send', ...)` called with correct line.
- Esc key handler navigates back.

Commit: `feat(client): attach mode (scrollback + input bar + parser preview)`.

---

## Phase 8 — Settings

### Task 24: Settings layout + tabs

**Files:**
- `routes/settings/route.tsx` — shell + tabs (Admins, Account, Audit, About).

Commit: `feat(client): settings shell`.

---

### Task 25: Admins tab (`routes/settings/admins.tsx`)

Table + add admin dialog + remove confirm. UI for "must change password on first login" indicator.

Commit: `feat(client): admins management`.

---

### Task 26: Account tab (`routes/settings/account.tsx`)

Change password form (same as forced change page but reachable from settings).

Commit: `feat(client): account settings (password change)`.

---

### Task 27: Audit log tab (`routes/settings/audit.tsx`)

Paginated table backed by `useInfiniteQuery` against `/api/audit?cursor=&limit=`. Filter chips: action, user, node. Each row: timestamp, user, action, node-name, bot_id (if any), expandable details.

Commit: `feat(client): audit log viewer with infinite scroll + filters`.

---

### Task 28: About tab

Show panel version (read from `package.json` at build time via Vite `__APP_VERSION__` define), backend version (from `/api/auth/me` extra field or new `/api/about` endpoint — decide during implementation), link to spec.

Commit: `feat(client): about page`.

---

## Phase 9 — UX polish

### Task 29: Command palette (Ctrl-K)

**Files:**
- `components/CommandPalette.tsx` (cmdk)
- Mount globally in `_authed.tsx`.

Items dynamically generated:
- Nodes (navigate)
- Bots from each connected node (navigate to attach or drawer)
- Top-level actions: Add Node, Logout, Change Password, Open Audit
- Quick mass-actions: "Mass say to <node>", "Mass join to <node>".

Fuzzy match via cmdk built-in. Keyboard handler: `cmd+k` / `ctrl+k`.

Tests: render, type "warsaw bot 5", expect filtered list; press Enter → navigate.

Commit: `feat(client): Ctrl-K command palette`.

---

### Task 30: Sonner toasts everywhere + global error boundary

- Wrap mutating queries in TanStack Query's `onError` to show `toast.error(err.message)`.
- Add ErrorBoundary at root.

Commit: `feat(client): consistent error toasts + boundary`.

---

## Phase 10 — Build, deploy, E2E

### Task 31: Production build wiring

**Files:**
- Modify: `gnb-panel/scripts/build.ts`
- Modify: `gnb-panel/src/server/index.ts` (serve `dist/client/`)

Behavior:
- `bun run build` runs `vite build` then `bun build src/server/index.ts --target=bun --outfile=dist/server.js`.
- In prod, `Bun.serve.fetch` checks: if path doesn't start with `/api` and isn't `/ws`, serve from `dist/client/` (with SPA fallback to `index.html` for any non-asset path).
- Send `cache-control: public, max-age=31536000, immutable` for hashed assets, `no-cache` for `index.html`.

Tests: `bun run build && bun run start &; sleep 1; curl http://localhost:3000/` returns the SPA HTML; `curl /api/auth/me` returns 401 JSON.

Commit: `feat(server): static SPA serve + cache headers; build pipeline`.

---

### Task 32: Playwright E2E suite

**Files:**
- `tests/e2e/auth.spec.ts` — bootstrap login + forced password change.
- `tests/e2e/nodes.spec.ts` — add node (using a real `gNb -dev`), see bots, run a bot.say, see toast.
- `tests/e2e/attach.spec.ts` — open attach, send `/msg #x hi`, see outbound entry in scrollback.

Each spec spins up panel-bun via `webServer` (already configured in playwright.config.ts) plus a fake-gNb test helper hosted on a local port (reuse `fake-gnb-server.ts` from Plan A).

Commit: `test(e2e): playwright auth + nodes + attach scenarios`.

---

### Task 33: README, deployment notes, systemd unit

**Files:**
- Update: `gnb-panel/README.md`
- Create: `gnb-panel/deploy/gnbpanel.service`

Contents:
- Quick start (dev + prod).
- Bootstrap admin flow (where the password is printed).
- Adding the first node.
- TLS options (cloudflared + native).
- Backup strategy (just back up `data/panel.db`).
- systemd unit example.

Commit: `docs: README + systemd deployment notes`.

---

### Task 34: Tag `panel-v0` + final smoke

```bash
bun run lint && bun run typecheck && bun test && bun run e2e
git tag -a panel-v0 -m "Plan B complete: full SPA on top of backend"
```

---

## Plan B — Self-review checklist

Walk this before declaring done:

**1. Spec coverage** — for each section in `2026-04-25-panel-design.md` that pertains to the SPA (§4, §5 client side, §7 browser-side reconnect, §10 client tests):
- §4.1–4.4 IA & UX → Tasks 12, 14, 16–28
- §5.1 REST consumption → Tasks 5, 14–28
- §5.2 WS consumption → Tasks 6, 8, 23
- §5.3 auth flow → Tasks 9–11
- §7 browser→panel-bun reconnect → Task 6
- §10 testing — UI / E2E → Tasks 7–11 (vitest), 32 (playwright)

**2. Contract drift** — `bun run typecheck` clean. Every API call uses `api<TypeFromWire>(…)`. No interface duplicates of wire types in `src/client/`.

**3. WS resilience** — disconnect Wifi (or kill backend), confirm SPA shows reconnecting indicator, then catches up via cursor replay; attaches re-open automatically.

**4. Audit visibility** — every action surfaced in `/settings/audit` matches the action that was triggered (login, add node, bot.say, attach.send, etc.).

**5. Lighthouse / a11y** — quick Lighthouse run against `bun run start`; aim for ≥ 90 on accessibility (Radix primitives + shadcn already help).

If any item fails, fix before tagging.
