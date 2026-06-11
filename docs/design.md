# mm — design

CLI (and later MCP server) for the mon-marché shopping assistant. Companion to
`docs/api.md`, which is the source of truth for endpoint contracts. This document covers
what we build on top of them.

## Decisions

- **CLI first, MCP wrapper later.** A CLI is usable from Claude Code (and by Doug) with
  zero config, and gives a direct debugging surface while the private API's failure modes
  are still being learned. The MCP server becomes a thin wrapper once the core is proven.
- **Go.** Typed structs per response double as the API-drift detector ("fail loudly"),
  single static binary, and the official MCP Go SDK keeps the wrapper path open.
- **Design for agent tasks, not endpoints.** The commands map to jobs (find, build cart,
  review cart, reorder, check delivery windows), with the API's failure-recovery loops
  (clamping, stale-id fallback) baked into the library rather than left to the caller.

## Package layout

```
cmd/mm/          CLI entry: dispatch, flags, rendering (human + --json)
internal/api/    typed HTTP client: transport, pacing, session state, error taxonomy,
                 one method per endpoint, response structs
internal/ops/    smart operations composed from api: resolve/add/clamp, batch apply,
                 reorder — this is the layer the MCP server will also call
```

`internal/api` is deliberately dumb (one call = one request, typed in/out).
`internal/ops` owns multi-request logic and returns structured reports. The CLI only
parses args and renders.

## Command surface

```
mm auth status                  session cookie expiry (local) + live probe of /orders/past
mm auth login                   prints the playwright-cli login procedure (manual step)

mm search <query> [--all]       compact product results; --all follows `next` cursors
mm browse [slug]                no arg: navigation tree; arg: category listing
mm product <slug>               single product detail

mm cart                         items + totals + distance to free shipping
mm cart add <item> [-n N]       increment; <item> is a query, or `id:<canonicalId>`
mm cart set <canonicalId> <n>   absolute quantity; 0 removes
mm cart apply [file|-]          batch of JSON lines → per-line report

mm orders [--limit N]           past order summaries
mm order <id>                   order detail (canonicalId + quantities)
mm reorder <id> [--dry-run]     rebuild cart from a past order, with fallback report

mm slots                        selectable delivery windows (list only — see scope cuts)
```

Global flag `--json` switches output from compact human text to structured JSON.
Default output is compact text: agents parse it fine and it stays pleasant for Doug.

### Quantity semantics

The API's `PATCH /cart/product` sets **absolute** quantity. The CLI keeps verbs honest:

- `cart add` is **incremental**: read the cart, PATCH `current + n`.
- `cart set` is **absolute**: PATCH `n` directly; `0` removes.

### `cart add <item>` resolution

No id-vs-query heuristics — explicit prefix:

- `id:<canonicalId>` → direct PATCH. On stale-id/out-of-stock errors, report; no silent
  substitution (caller had an exact id, so an exact answer is owed).
- anything else → `/search2` query. Take the **first result**, add it, and report what
  was picked (name, id, unit price) plus the next 2 alternates so an agent can correct a
  wrong pick by id. Agents wanting full control search first, then add by id.

**Clamping:** on `E_ECOM_01_0003` (quantity > available), retry once clamped to
`availableQuantity` and report `clamped N→M`. The API rejects oversell outright, so
without this every over-ask costs the agent a round trip.

### `cart apply` — the batch workhorse

Input: JSON lines, each `{"query": "...", "n": 2}` or `{"id": "<canonicalId>", "n": 2}`
(`n` increments, `"set": k` for absolute). One cart read up front, then sequential
resolved adds under the client's pacing. Output: a per-line report, one of
`added | clamped | picked (via search) | out-of-stock | stale-id | error`, with totals
at the end. This turns "here's my shopping list" into a single invocation.

### `reorder <id>`

`/orders/{id}` → apply each line by `canonicalId`. On `E_ECOM_01_0012` (stale id) or
`E_ECOM_01_0004` (out of stock): **no auto-substitution** — report the failure plus the
top `/search2` hit for the line's name, and let the caller decide. `--dry-run` prints the
plan (lines, prices, availability) without mutating.

## Cross-cutting behavior (library-owned)

### Session state

`.auth/state.json` is Playwright storage-state format. The client extracts only the
`session` cookie for `www.mon-marche.fr` and ignores the rest (analytics). Because the
cookie is a sliding 60-day window whose token never rotates (api.md "Lifetime"), the
client captures `Set-Cookie` from responses and **rewrites the cookie's `expires` (and
value, defensively) back into `.auth/state.json`** after a run, so `mm auth status` reads
an accurate date without contacting the site. The token value is never printed or logged.

Login stays manual: `mm auth login` prints the playwright-cli procedure (headed browser,
Doug types credentials, `state-save .auth/state.json`).

### Error taxonomy (from api.md "Failure modes")

| Signal | Meaning | Behavior |
|---|---|---|
| 401 + `E_01_0000` | session expired | exit with "run `mm auth login`" |
| 404 + `E_08_0005` on cart reads | ambiguous (anonymous *or* empty) | probe `/orders/past`: 401 → auth error; 200 → treat as empty cart |
| 4xx + `E_ECOM_01_*` | product/quantity problem | structured per-item outcome (recoverable) |
| anything else (5xx, HTML, undecodable, missing required fields) | API drift | **fail loudly**: nonzero exit, raw status + body snippet, pointer to re-verify per api.md |

Decoding is tolerant of *new* fields but validates required ones (`canonicalId`,
`itemPrice`, …); a response that stops carrying them is treated as drift, not as empty.

### Rate pacing

All requests serialize through the client with a minimum interval (1s + 0–500ms jitter)
— human-paced per the ToS review. Batch operations (`apply`, `reorder`, `search --all`)
inherit this automatically.

### PII

Per api.md, `/cart` and `/orders/{id}` carry name/email/phone/address. The response
structs **do not include those fields** except `delivery.address.location` +
`postalCode`, which `mm slots` needs as the `deliverySlots2` request body; renderers
never print them. Money renders as `4,24 €` from integer cents; slot times render in
Europe/Paris local time.

## Scope cuts (v1)

- **No slot selection.** Only the attach-to-existing-order variant of
  `PATCH /cart/initialOrder` was captured (api.md "Selecting a window"); the clean
  contract is unverified. `mm slots` lists windows; Doug picks in the browser —
  consistent with checkout being manual anyway.
- **No checkout/payment anything**, per CLAUDE.md. Scope ends at the cart.
- **No favorites/recipes endpoints** yet (`/account/bookmarks*`, `/cart/recipes`) — easy
  adds later if wanted.

## MCP mapping (later)

Thin server over `internal/ops` via the official Go SDK; roughly one tool per command:
`search`, `browse`, `get_product`, `get_cart`, `cart_apply` (the workhorse — covers
add/set/remove/batch), `list_orders`, `get_order`, `reorder`, `list_slots`,
`auth_status`. Tool results reuse the `--json` output shapes.
