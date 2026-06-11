# mon-marche shopping assistant

Personal shopping assistant for [mon-marché.fr](https://www.mon-marche.fr) — a French
online grocery service (fresh produce, Paris/Île-de-France delivery, operated by MMecom SAS).

## Goal

Build a CLI and/or MCP server that lets Claude browse the mon-marché catalog and manage
the shopping cart under Doug's own customer login. Scope ends at the cart: **final review,
checkout, and payment are always done by Doug in the browser.** Never place orders or
touch payment flows.

## How it works (intended architecture)

There is no public API. The website is an SPA that loads everything as JSON over XHR from
endpoints under `https://mon-marche.fr/api/`. The plan is to reverse-engineer the private
API and build a thin client on top of it: auth/session handling, product search & browse,
cart read/add/remove.

Endpoint discovery is done with `playwright-cli` (installed globally): drive the site in
a headed browser and read the XHR traffic via its `requests` / `request <n>` /
`response-body <n>` commands. Login is always typed by Doug himself in the headed
browser — Claude never handles the password. The authenticated session is persisted with
`state-save` to `.auth/state.json` (gitignored) and restored with `state-load`.

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
- Art. 4.1: account actions are deemed Doug's; password is confidential → credentials
  stay local (keychain or untracked `.env`), never committed.

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
- [ ] Decide CLI vs MCP server (or CLI first, MCP wrapper later)
- [ ] Implement client

## Notes on driving the API

- Auth is just the `session` cookie (see `docs/api.md`). Persisted to `.auth/state.json`.
- Clicking product cards in playwright-cli is flaky: a price overlay intercepts pointer
  events on the add/remove buttons. The reliable path is to call the JSON API directly
  from the page context with `playwright-cli eval '() => fetch("/api/...", {...})'` — the
  browser attaches the session cookie automatically. This is also how the eventual client
  will work (replay the cookie), so the UI flakiness doesn't matter.
- Core mutation: `PATCH /api/cart/product` with `{product:{id:<canonicalId>,quantity:N}}`;
  `quantity:0` removes. Verified 2026-06-11.
