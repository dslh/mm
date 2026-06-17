# mm — mon-marché shopping assistant

A CLI and MCP server for browsing the [mon-marché.fr](https://www.mon-marche.fr) catalog
and managing a shopping cart from your own customer login. mon-marché is a French online
grocery (fresh produce, Paris/Île-de-France delivery, operated by MMecom SAS).

**Scope ends at the cart.** Final review, checkout, and payment are always done in the
browser — there is no command or tool for them, and the client never handles payment
details or places orders.

## How it works

mon-marché has no public API; the site is an SPA that loads everything as JSON over XHR
from endpoints under `https://mon-marche.fr/api/`. `mm` is a thin typed client over that
private API: it replays the authenticated `session` cookie, paces requests to human
speed, and exposes catalog browse/search and cart read/add/remove as task-shaped
commands. Endpoint contracts were reverse-engineered from live browser traffic and are
documented in [`docs/api.md`](docs/api.md); the design rationale is in
[`docs/design.md`](docs/design.md).

Authentication: `mm auth login` prompts for your email and password (no echo), sends the
password only in the `POST /api/auth/signin` request, and persists just the resulting
`session` cookie to `.auth/state.json` (gitignored) — nothing else is stored, and the
password is never written to disk. A browser login captured with `playwright-cli
state-save` produces an equivalent file and remains a fallback.

## Build

```sh
go build -o bin/mm ./cmd/mm
```

Requires Go 1.26+.

## Usage

```
mm auth status                  session cookie expiry + live probe
mm auth login                   prompt for email + password, store the session cookie

mm search <query> [--all]       product search; --all follows pagination
mm browse [slug]                navigation tree (no arg) or category listing
mm product <slug>               single product detail

mm cart                         items + totals + distance to free shipping
mm cart add <item> [-n N]       increment (item is a query or id:<canonicalId>)
mm cart set <canonicalId> <n>   absolute quantity; 0 removes
mm cart apply [file|-]          batch of JSON lines → per-line report

mm orders [--limit N]           past order summaries
mm order <id>                   order detail
mm reorder <id> [--dry-run]     rebuild cart from a past order

mm slots                        selectable delivery windows
mm slots select <slotId>        set the cart's delivery window
```

Add `--json` to any command for structured output instead of compact human text.
Monetary amounts are integer euro cents (`424` = 4,24 €). Run `mm help` or
`mm help <cmd>` for full details.

## MCP server

`mm mcp` runs a stdio MCP server (official Go SDK) exposing the same operations as eleven
tools — `search`, `browse`, `get_product`, `get_cart`, `cart_apply`, `list_orders`,
`get_order`, `reorder`, `list_slots`, `select_slot`, `auth_status`. `cart_apply` is the
only cart-mutation tool; there is no checkout/payment tool.

Register it with a client over stdio:

```sh
claude mcp add mm -- /abs/path/to/bin/mm mcp
```

The server reads `.auth/state.json` from its working directory (or `$MM_STATE`).

## Layout

```
cmd/mm/          CLI entry + the `mm mcp` stdio server (mcp.go)
internal/api/    typed HTTP client: transport, pacing, session state, error taxonomy
internal/ops/    smart operations (resolve/clamp/batch/reorder) — shared by CLI and MCP
docs/            api.md (endpoint contracts) and design.md (architecture)
```

## Conventions & constraints

- Credentials and session tokens live outside git (`.env` and `.auth/` are gitignored)
  and are never printed or logged. Delivery PII is passed through as opaque bytes, never
  decoded, stored, or rendered (see `docs/design.md` → PII).
- All requests are rate-limited to human-like pacing — no aggressive polling, no bulk
  mirroring of the catalog. This keeps within mon-marché's terms of service (reviewed
  2026-06-11; personal-use automation is permitted).
- The private API is undocumented and may change without notice; the client fails loudly
  on unexpected responses rather than guessing.

## Status

Working end-to-end and verified live: CLI, MCP server, and delivery-slot selection.

## License

[MIT](LICENSE) © Douglas Lake-Hammond.

This license covers the code in this repository only. It grants no rights over
mon-marché's API, catalog data, or service, and is not an endorsement by or affiliation
with MMecom SAS. Use the client for your own account, within mon-marché's terms of
service, at human-paced request rates.
