# mon-marche shopping assistant

Personal shopping assistant for [mon-marché.fr](https://www.mon-marche.fr) — a French
online grocery service (fresh produce, Paris/Île-de-France delivery, operated by MMecom SAS).

## Goal

Build a CLI and/or MCP server that lets Claude browse the mon-marché catalog and manage
the shopping cart under your own customer login. Scope ends at the cart: **final review,
checkout, and payment are always done by you in the browser.** Never place orders or
touch payment flows.

## How it works (intended architecture)

There is no public API. The website is an SPA that loads everything as JSON over XHR from
endpoints under `https://mon-marche.fr/api/`. The plan is to reverse-engineer the private
API and build a thin client on top of it: auth/session handling, product search & browse,
cart read/add/remove.

Endpoint discovery is done with `playwright-cli` (installed globally): drive the site in
a headed browser and read the XHR traffic via its `requests` / `request <n>` /
`response-body <n>` commands. Claude (the assistant) never handles the password. Login
itself is `POST /api/auth/signin` (see `docs/api.md`): `mm auth login` prompts you for
email + password without echo, sends the password only in the signin request, and
persists just the resulting `session` cookie to the credentials file. By default that
is `<user config dir>/mm/state.json` (`os.UserConfigDir()` — e.g.
`~/Library/Application Support/mm/state.json` on macOS, `%AppData%\mm\state.json` on
Windows), so the same session is found regardless of the working directory. A
working-directory-relative `.auth/state.json` (gitignored) is still honored when present
— it's what this repo uses for dev — and `$MM_STATE` overrides both. `mm auth status`
prints the resolved path. A browser login captured with `state-save` produces an
equivalent file and remains a fallback.

## Terms-of-service review (done 2026-06-11)

Checked the CGV ("Conditions Générales de Vente – Mentions Légales", version of
2023-08-29, published as a Google Doc embedded at https://www.mon-marche.fr/cgv) and
robots.txt. Conclusion: **nothing forbids personal-use automation** — no clause about
bots, scraping, scripts, automated access, or API use. robots.txt even explicitly allows
ClaudeBot/anthropic-ai. Constraints that do apply:

- Art. 4.2: account can be suspended for disrupting site operation or an "abnormally
  high" number of transactions → keep request rates human-paced, no aggressive polling.
- Art. 16 (IP) + French sui generis database right → query the catalog on demand;
  don't bulk-mirror or redistribute it.
- Art. 4.1: account actions are deemed the account holder's; password is confidential →
  credentials stay local (keychain or untracked `.env`), never committed.

## Conventions

- Credentials and session tokens live outside git (`.env` is gitignored).
- Rate-limit all API calls to human-like pacing.
- The private API is undocumented and may change without notice — fail loudly and
  re-verify endpoints against fresh browser traffic when responses stop matching
  expectations.

## Status

- [x] ToS review — cleared for personal-use automation
- [x] Map auth, search, and cart endpoints via playwright-cli network capture
- [x] Document endpoints (request/response shapes) in `docs/api.md`
- [x] Order history + reorder path mapped (`/orders/past` → `/orders/{id}`)
- [x] Decide CLI vs MCP server → **CLI first, MCP wrapper later** (rationale in `docs/design.md`)
- [x] Implement client — v1 CLI (`mm`) working end-to-end, verified live 2026-06-11
- [x] MCP server (`mm mcp`) — thin stdio wrapper over `internal/ops`, verified 2026-06-14
- [x] Delivery-slot selection (`PATCH /cart/delivery2`) — mapped + implemented
      (`mm slots select`, MCP `select_slot`), verified live 2026-06-14. Delivery PII is
      passed through as opaque bytes, never decoded/logged/stored (see `docs/design.md` PII).
- [x] Direct login (`POST /api/auth/signin`) — mapped + implemented as `mm auth login`
      (prompts email + password without echo, persists only the cookie), 2026-06-16.
      Removes the playwright-cli `state-save` step for first-time setup / re-login.

## Code

Go module `github.com/dslh/mm`. Layout per
`docs/design.md`: `internal/api` (typed client: pacing, session state, error taxonomy),
`internal/ops` (smart operations: resolve/clamp/batch/reorder), `cmd/mm` (CLI + the
`mm mcp` stdio server in `mcp.go`). Build: `go build -o bin/mm ./cmd/mm`. Run `mm help`
for the command surface.

Register the MCP server with a client over stdio, e.g.
`claude mcp add mm -- /abs/path/to/bin/mm mcp` (the server reads the same credentials file
as the CLI — per-user config dir by default, `./.auth/state.json` if present, or
`$MM_STATE`; `mm auth status` prints the resolved path). Tools mirror the CLI; cart
mutation goes through the single `cart_apply` tool. See `docs/design.md` "MCP server".

## Releasing

Releases are cut by pushing a SemVer tag (`vX.Y.Z`) to `main`: the
`.github/workflows/release.yaml` workflow runs GoReleaser (`.goreleaser.yaml`), which
cross-builds the binaries and publishes the Homebrew tap + Scoop bucket. The version,
commit, and date are injected into `cmd/mm` via `-ldflags` from the tag — a plain
`go build` reports `dev`/`none`. So: commit to `main`, `git tag vX.Y.Z`, push the tag.
Follow SemVer; tags live on `main` (don't branch for a release). Pushing a tag publishes
externally and is hard to undo — confirm before pushing.

## Notes on driving the API

- Auth is just the `session` cookie (see `docs/api.md`). Persisted to the credentials
  file (per-user config dir by default; `mm auth status` prints the path).
- Clicking product cards in playwright-cli is flaky: a price overlay intercepts pointer
  events on the add/remove buttons. The reliable path is to call the JSON API directly
  from the page context with `playwright-cli eval '() => fetch("/api/...", {...})'` — the
  browser attaches the session cookie automatically. This is also how the eventual client
  will work (replay the cookie), so the UI flakiness doesn't matter.
- Core mutation: `PATCH /api/cart/product` with `{product:{id:<canonicalId>,quantity:N}}`;
  `quantity:0` removes. Verified 2026-06-11.
