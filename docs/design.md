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

mm slots                        selectable delivery windows
mm slots select <slotId>        set the cart's delivery window (checkout stays manual)
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
structs **do not decode those fields** except `delivery.address.location` + `postalCode` +
`countryCode`, which `mm slots` needs as the `deliverySlots2` request body; renderers never
print them. Money renders as `4,24 €` from integer cents; slot times render in Europe/Paris
local time.

**Slot selection pass-through.** `PATCH /cart/delivery2` (slot selection) is the one flow
that must *send* PII back — it re-posts the cart's `delivery.{note,address}` alongside the
chosen slot. The rule is **opaque pass-through, never inspection**:

- `Cart.RawDelivery` (a `json.RawMessage`, tag `json:"-"`) captures the `delivery` object
  verbatim on `GET /cart`. The note/address values are never unmarshaled into Go strings,
  so they can't reach a log line or `fmt` call. `SetCartDelivery` sub-selects the
  `{note,address}` keys as raw bytes and posts them straight back.
- The raw bytes live **in memory only** — never written to disk (the session file holds
  just the cookie), never emitted by `--json` (the `json:"-"` tag + the `viewCart` shape
  both exclude it).
- `DriftError` snippets are **suppressed for PII endpoints** (`/cart`, `/orders/{id}`,
  `/cart/delivery2` — `reqOpts.sensitive`): on a decode/HTTP failure the error reports the
  body's size, not its content, so personal data never lands in stderr.

This loops the customer's own data back into the same system it came from, with no new
on-disk or logged copy — the agreed handling (2026-06-14).

## Scope cuts (v1)

- **No checkout/payment anything**, per CLAUDE.md. Scope ends at the cart: `mm slots
  select` sets the delivery window, but final review, checkout, and payment stay in the
  browser.
- **No favorites/recipes endpoints** yet (`/account/bookmarks*`, `/cart/recipes`) — easy
  adds later if wanted.

## MCP server

`mm mcp` runs a stdio server (official Go SDK, `github.com/modelcontextprotocol/go-sdk`)
exposing `internal/ops` to Claude — `cmd/mm/mcp.go`, a thin wrapper, no new logic. Eleven
tools, one per command:

| Tool | Wraps | Input |
|---|---|---|
| `search` | `Search`/`SearchAll` | `query`, `all?` |
| `browse` | `Navigation` (no slug) / `Category` | `slug?` |
| `get_product` | `ArticleBySlug` | `slug` |
| `get_cart` | `GetCart` | — |
| `cart_apply` | `Apply` (the workhorse — add/set/remove/batch) | `lines[]` of `{query\|id, n\|set}` |
| `list_orders` | `OrdersPast` | `limit?` |
| `get_order` | `Order` | `id` |
| `reorder` | `Reorder` | `id`, `dryRun?` |
| `list_slots` | `Slots` | — |
| `select_slot` | `SelectSlot` | `slotId` |
| `auth_status` | `ProbeAuth` + expiry | — |

`cart_apply` is the only cart-mutation tool (no separate add/set tools — the design's
"one workhorse" choice). Scope still ends at the cart: no checkout/payment tool exists.

Implementation notes:

- **Output = the `--json` shapes, byte-for-byte.** Handlers return the same view-shaped
  values the CLI marshals (`viewCart` strips the cart's delivery PII; `api.Order`/`api.Cart`
  structs already omit it). Tools register with `AddTool[In, any]`: the SDK infers and
  validates the *input* schema from the typed args but generates no output schema (skipped
  when `Out == any`), so the deliberately drift-tolerant response structs aren't subject to
  strict output validation. The returned value is mirrored into both `structuredContent`
  and a text block.
- **One shared client, fully serialized.** A single `api.Client` lives for the server's
  lifetime and every handler runs under one mutex, so all traffic stays human-paced (ToS)
  and the un-synchronized session state is never touched concurrently. Session expiry rolls
  forward in memory from `Set-Cookie` and is written back on shutdown.
- **Errors** map the library taxonomy to tool errors (`isError` + message) so the agent can
  self-correct: auth → "recreate `.auth/state.json`"; drift → "re-verify per docs/api.md".
  `auth_status` is the exception — an expired session returns `valid:false` rather than
  erroring, since reporting that is its job.
