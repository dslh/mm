# mon-marché.fr private API

Reverse-engineered from the SPA's XHR traffic on 2026-06-11 via playwright-cli, under an
authenticated session. Undocumented and unstable — re-verify against fresh browser traffic
if responses stop matching.

Base URL: `https://www.mon-marche.fr/api`

## Authentication

Auth is carried entirely by a **`session` cookie** (opaque hex token, scoped to
`www.mon-marche.fr`, path `/`). There is no bearer token or `Authorization` header — XHRs
send only `content-type: application/json` plus the cookie. So any client just needs to
replay the `session` cookie from a logged-in browser.

Login itself is a manual browser step (Doug types credentials). We persist the session with
`playwright-cli state-save .auth/state.json` and restore with `state-load`. The cookie does
expire; on expiry, re-do the manual login and re-save.

Verified 2026-06-11 that the `session` cookie alone is sufficient (plain `curl` with just
`Cookie: session=<token>` returns 200 + JSON — no UA, referer, or other header needed).
This holds for **mutations too**: `PATCH /cart/product` from plain `curl` with only the
cookie + `content-type` succeeds (200, cart updated) — no `Origin`/`Referer` check and no
CSRF token. Verified 2026-06-11 with a full add→remove round-trip from outside the browser.

**Lifetime:** the `session` cookie is `httpOnly`, `Secure`, `SameSite=Lax`, with a **~60-day
expiry**, and it is a **sliding window** — each authenticated request rolls the expiry
forward to ~now + 60 days. Verified 2026-06-11: after ~56 min of activity the persisted
`expires` had advanced by ~3384 s (matching real elapsed time), while the **token value was
unchanged** (only the expiry attribute moves; the token does not rotate).

Implications:
- Re-login is needed only after **~60 days of total inactivity**. For any regularly-used
  client the "session expired" path is rare — a clear error on `401 E_01_0000` suffices, no
  prominent always-on affordance needed.
- Because the token value is stable, a persisted `.auth/state.json` stays usable; but its
  stored `expires` goes stale (pessimistic) as the live cookie rolls forward. The client
  should **re-save state after activity** (or capture `Set-Cookie` from responses) so any
  "warn ahead of expiry" logic reads an accurate date. The `expires` can be read from
  `.auth/state.json` without exposing the token.

### Auth-failure signature (how the client detects an expired session)

Probe an **account-scoped** endpoint — `GET /orders/past` is a good one (cheap, no side
effects). Expired/invalid session returns **HTTP 401** with this exact body:

```json
{ "statusCode": 401, "error": "Unauthorized", "message": "Unauthorized", "code": "E_01_0000", "owner": "Keplr" }
```

A valid session returns 200. So the client should: treat `401` / `code: E_01_0000` on a
known-good endpoint as **"session expired → prompt re-login"**, and treat other failures
(5xx, network, schema mismatch) as **"API may have changed → fail loudly"**, keeping the two
distinct.

Do **not** probe auth with `/cart` or `/cart/light`: those return `404` with
`code: E_08_0005` ("Le panier est introuvable") for *both* an expired session and a valid
session with no cart — ambiguous. Also note `/api/auth/me` appears as a React-Query cache
key carrying the user identity, but it is **not** a working GET endpoint (404); don't use it.

All prices are integers in **cents** (`itemPrice: 424` = 4,24 €). Weights are in the unit
named alongside them (usually kg).

## Read endpoints

| Method | Path | Returns |
|--------|------|---------|
| GET | `/cart` | Full cart — see schema below. **Contains customer PII** (name, email, phone, delivery address, access notes). |
| GET | `/cart/light` | Lighter cart payload. |
| GET | `/cart/recompute2` | Recomputed totals/fees. |
| GET | `/cart/recipes` | Recipes associated with cart items. |
| GET | `/cart/relatedArticles?context=CROSS_SELL\|UPSELL&nbRelatedArticles=24` | Cross/up-sell suggestions. |
| GET | `/orders/current` | `{ "items": [...], "count": N }` — in-progress order. |
| GET | `/orders/last` | `{ "past": { id, delivery, state, totalPrice, products[] }, ... }` |
| GET | `/orders/past` | All past orders: `{ count, items[] }`. Each item is a **summary** — `id`, `delivery`, `state`, `totalPrice`, `loyalty`, and `products[]` with only `{id, name, image}` (no quantities). |
| GET | `/orders/{id}` | **Full order detail** — products carry `canonicalId`, `name`, `quotation.count` (quantity), `itemPrice`, etc. **Contains customer PII.** This is the endpoint to use for reorder. |
| GET | `/account/bookmarks-lists` | Favorites lists: `[{ id, name, bookmarks: [{ bookmarkId, name, image, type }] }]` |
| GET | `/account/bookmarks` | Flat bookmarks. |
| GET | `/account/products/sku` | Account product SKUs (reorder history). |
| GET | `/account/recipes/ids` | Saved recipe ids. |
| POST | `/addresses/deliverySlots2` | Available delivery windows for a location — see "Delivery slots". |
| GET | `/navigation` | Category navigation tree — see "Browse by category". |
| GET | `/category/{slug}` | Category contents (subcategories and/or products) — see "Browse by category". |
| GET | `/articleDetailBySlug/{slug}` | **Single product detail** (keyed by slug, not id) — see "Single product". |
| GET | `/sitemap.xml` | Product/category sitemap (also in robots.txt). |

## Search

- `GET /search/suggestions?query=<q>` — autocomplete. Fires per keystroke after ~3 chars.
  ```json
  {
    "keywords": ["Tomate", "Soupe tomate", "Tomate cerise", ...],
    "categories": [{ "id": "...", "name": "Tomates", "parentId": "...", "slug": "tomates" }]
  }
  ```
- `GET /search/popular` — popular search terms (no query).
- `GET /search2?type=PRODUCT&text=<q>&modelVersion=ordinal_df` — **the main product search.**
  `type=RECIPE` returns recipes instead. It is **text search only** — there is no category
  filter (passing `category`/`categoryId`/etc. is ignored and yields 0 results; use
  `/category/{slug}` to browse instead). Response:
  ```json
  { "count": 87, "items": [ /* up to 50 product objects */ ], "algoVersion": "...", "next": "10du-0e8F" }
  ```
  **Pagination:** `count` is the total; each page returns up to 50 `items`. `next` is an
  opaque cursor token — fetch the next page by appending **`&next=<token>`** to the same
  query. Repeat until the response has **no `next`** (last page). Verified 2026-06-11:
  `tomate` → 87 total, page 1 = 50 items + `next`, page 2 (`&next=...`) = remaining 37 items,
  no `next`. (Only the param name `next` works; `cursor`/`after`/`from`/`page` are ignored.)

### Product object (search2 `items[]`)

```jsonc
{
  "type": "PRODUCT",
  "id": "WcfOfldPeT$oHRKg6HvOFfrXCQa",   // compound: "<canonicalId>$<variant/shop>"
  "canonicalId": "WcfOfldPeT",            // <-- use THIS id for cart mutations
  "sku": "FL3037",
  "name": "La Tomate côtelée rouge",
  "slug": "la-tomate-marmande-selection-variete-ancienne",
  "availableQuantity": 189,
  "origin": "France",
  "granularity": { "singular": "pièce", "plural": "pièces" },
  "packSize": 1,
  "itemPrice": 424,                        // cents; the effective per-item price
  "itemDefinition": { "type": "arbitraryQuantity", "weight": { "value": 1, "unit": "kg" } },
  "pricing": {
    "sellPrices": {
      "perGranularity": { "net": 424, "dutyFree": 402, "unit": "kg", "pieces": 1, "currency": "EUR" },
      "perWeightUnit":  { "net": 424, "dutyFree": 402, "unit": "kg", "value": 1,  "currency": "EUR", "main": true }
    }
  },
  "promo": {                               // present only when on offer
    "mechanism": "IMMEDIATE_DISCOUNT",
    "itemOriginalPrice": 499,
    "conditions": { "type": "PERCENT", "value": 15, "startDate": <ms>, "endDate": <ms> }
  },
  "images": [ /* cloudinary urls + format crops */ ],
  "shortDescription": "...", "attributes": [...], "labels": [...]
}
```

## Browse by category

Browsing is server-rendered on the site (no XHR fires on a category page load), but the
underlying endpoints are these two, used together:

**`GET /navigation`** — the full menu tree. Shape:
```jsonc
{
  "families": [                              // 7 top-level families
    {
      "id": "nBHl1CNZ9VchkvXJ", "name": "Fruits & Légumes",
      "link": "https://www.mon-marche.fr/categorie/fruits",   // family landing (a category slug)
      "categories": [                        // e.g. "fruits", "legumes"
        { "id": "...", "name": "Fruits", "slug": "fruits",
          "children": [ { "id": "...", "name": "Fruits de saison", "slug": "fruits-de-saison" }, ... ] }
      ]
    }, ...
  ],
  "recipe": {...}, "home": {...}
}
```
Families: *À l'affiche, Fruits & Légumes, Boucherie & Poissonnerie, Fromagerie & Traiteur,
Épicerie & Boissons, Entretien Soins & Bébé, Promotions.* Families carry a `link` but no
`slug`; the browsable units are `categories[]` and their `children[]`, each with a `slug`.

**`GET /category/{slug}`** — contents of one category. Same shape for parent and leaf; which
field is populated tells you where you are:
- **Parent** (e.g. `/category/fruits`): `subcategories[]` populated (drill down by their
  `slug`), `items` empty.
- **Leaf** (e.g. `/category/tomates`): `items[]` populated with the listing, plus a `parent`
  ref back up.

`items[]` is a **mixed feed** — filter by `type`:
```
items[].type ∈ { "PRODUCT", "RECIPE", "CUSTOM_CONTENT" }   // CUSTOM_CONTENT = editorial banner
```
`type === "PRODUCT"` items use the same product shape as `/search2` (canonicalId, pricing,
itemPrice, …), so they feed straight into the cart mutation below. There's no visible
pagination on `/category/{slug}` (leaf returns the whole listing in one call).

Browse algorithm for the client: `/navigation` to enumerate slugs → `/category/{slug}`;
if `subcategories` is non-empty, recurse, else read `items` filtered to `PRODUCT`.

## Single product

**`GET /articleDetailBySlug/{slug}`** — full detail for one product, **keyed by `slug`** (the
product object's `slug`, e.g. from search/category results), not by id. Returns 200 with the
search product shape plus extras: `description` (long), `breadcrumbs`, `rating`,
`relatedArticles`, `seo`, `enabled`. There is no by-id detail endpoint (`/product/{id}`,
`/products/{id}` etc. all 404) — go via slug.

## Catalog scoping (what's global vs session-specific)

`/search2` and `/category/{slug}` are **public** — they return 200 without a session cookie.
Verified 2026-06-11 by comparing authenticated vs anonymous responses:

- **`canonicalId` and prices are global** — identical with and without the session (same id
  set, same `itemPrice`). → **Safe to cache product ids and prices across sessions** for
  reorder/shopping lists.
- **`availableQuantity` is session/zone-scoped** — anonymous requests return a flat `200`
  placeholder; the authenticated session returns real stock for the delivery zone (e.g. 171,
  106). The product set can also differ slightly (an item out of stock for the zone may drop
  out). → **Never trust a cached `availableQuantity`; always re-read it under the live
  session before relying on it**, and let the add-time `E_ECOM_*` errors be the source of
  truth for "can I actually buy this right now."

The compound `id` (`<canonicalId>$<...>`) appears consistent with this: the `canonicalId`
half is global, the suffix reflects zone/shop fulfilment. Cart mutations key off the global
`canonicalId` only.

## Cart mutation (add / set quantity / remove)

**`PATCH /api/cart/product`** — sets the **absolute** quantity for a product (not an
increment). Request body:

```json
{
  "product": {
    "id": "WcfOfldPeT",
    "quantity": 1,
    "source": { "type": "search results", "detail": { "term": "tomate" } }
  }
}
```

- `id` is the **`canonicalId`**, not the compound `id` with `$`.
- `quantity` is the target count (in `granularity` units / pieces). This is uniform across
  the catalog: only two `itemDefinition.type` values exist (surveyed 2026-06-11 across
  produce/cheese/meat/dairy searches) — `arbitraryQuantity` (priced per weight, e.g. loose
  tomatoes) and `pieceWeight` (fixed-weight pieces, e.g. a barquette). **Both are ordered as
  an integer count of granularity units**; there is no fractional/per-kg quantity mode.
- `source` is optional analytics metadata; safe to omit or set a fixed value.
- Returns the **full cart object** (same schema as `GET /cart`).
- `quantity: 0` removes the product from the cart. Verified 2026-06-11 (PATCH returned
  200 and the product was absent from the returned cart).

### Failure modes (verified 2026-06-11)

All product-level failures return a **4xx with a structured `E_ECOM_*` `code`** and a French
`message`. This is the key signal: a structured `E_ECOM_*` body means "the product/quantity
is the problem" (recoverable — search-fallback or adjust), whereas an unstructured failure
(5xx, HTML, or a 4xx with no `E_ECOM` code) means "auth or API changed" (fail loudly).

| Case | HTTP | `code` | `message` | Client action |
|------|------|--------|-----------|---------------|
| Unknown / stale `canonicalId` | 404 | `E_ECOM_01_0012` | "Le produit n'est plus disponible pour le créneau sélectionné" | Fall back to `/search2` by name |
| Product out of stock (`availableQuantity: 0`) | 400 | `E_ECOM_01_0004` | "Le produit n'est pas disponible pour le créneau sélectionné" | Skip / offer alternative |
| Quantity > available | 400 | `E_ECOM_01_0003` | "La quantité sélectionnée n'est pas disponible" | Retry clamped to `availableQuantity` |

Important: oversell is **rejected, not clamped or oversold** — the request fails and the
cart keeps its previous quantity (verified: a 999999 request on a ~171-stock item returned
400 and the cart line stayed at 1). The client must do the clamping itself.

(Error namespace seen so far: `E_01_*` = auth, `E_08_*` = cart-not-found, `E_ECOM_01_*`
= product/quantity. See the Authentication section for `E_01_0000`.)

### Cart bootstrap

An authenticated session **always has a cart**: `GET /cart` returns `200` with `products: []`
when empty (a fresh cart is auto-created after checkout — verified post-order). Adding the
first item is just a normal `PATCH /cart/product`; no separate "create cart" step is needed,
and the delivery `timeSlot`/`address` carry over from the previous order. The `404`
`E_08_0005` ("Le panier est introuvable") is the **anonymous / no-session** case, not a
normal state for a logged-in client. (A true logged-in 404 — e.g. a brand-new account that
never had a cart — wasn't reproducible here since the existing cart can't be deleted via the
API; treat it as unverified. The delivery-slot dependency for adding items is also untested,
because a slot was already set.)

### Reorder flow

`GET /orders/past` (list) → `GET /orders/{id}` (detail, has canonicalId + quantity) →
`PATCH /api/cart/product` per line to rebuild the cart. Order-detail `products[]` item:

```jsonc
{
  "canonicalId": "8ClKOT6rH", "id": "8ClKOT6rH", "name": "Le Café moulu 100% arabica",
  "quotation": { "count": 1, ... }, "quotation2": { "count": { "quantity": 1 } },
  "itemPrice": 499, "state": "...", "real": {...}, "pricing": {...}
}
```

Note: a product's exact `canonicalId` can change over time (seasonal produce, relisted
items) and may no longer be available — when a direct add by `canonicalId` fails with
`E_ECOM_01_0012` (stale id) or `E_ECOM_01_0004` (out of stock), fall back to a name/SKU
search (`/search2`). See "Failure modes" above for the full code list.

## Cart object schema (GET /cart, and PATCH response)

Top-level keys:
`id`, `customer`, `addresses`, `delivery`, `coupons`, `products[]`, `price`,
`minOrderAmountReached`, `replaceMissingProducts`, `replaceBioWithNonBio`, `loyalty`, `atcRank`.

> **PII:** `customer` (firstName/lastName/email/phone), `delivery.note`, and
> `delivery.address` carry personal data. A client should read only what it needs
> (typically `products[]` and `price`) and avoid logging the rest.

`products[]` item shape (differs slightly from search):
```jsonc
{
  "canonicalId": "WcfOfldPeT", "id": "WcfOfldPeT", "sku": "FL3037",
  "name": "...", "itemPrice": 424,
  "quotation":  { "editable": true, "count": 1, "weight": 1 },     // <-- quantity lives here
  "quotation2": { "count": { "quantity": 1, "freeQuantity": 0, "itemDefinition": {...} } },
  "pricing": {...}, "originalPricing": {...}, "promo": {...}, "availableQuantity": 188
}
```

`price` block (all cents):
```jsonc
{
  "quotation": {
    "net": 6919, "dutyFree": 6498, "vat": 421,
    "shipping": 399, "preparationFee": 99, "preauthorization": 7548,
    "discount": 0, "promoSavings": 75, "currency": "EUR"
  },
  "fees": [{ "code": "2G", "vatRate": 0.055, "vat": 334 }, { "code": "S", "vatRate": 0.2, "vat": 87 }]
}
```

Delivery thresholds (from `delivery.deliveryPrices`): free shipping at 80,00 €
(`minCartNetPrice: 8000`), 3,99 € from 60,00 €, else 5,99 €.

## Delivery slots

### List available windows — `POST /api/addresses/deliverySlots2`

Request body is the delivery target (read it from the cart's `delivery.address`):
```json
{ "location": { "lat": 48.8017, "lng": 2.2772 }, "postalCode": "92320", "countryCode": "FR" }
```
Response: `{ "deliveryZones": [ ... ] }`. Each zone:
```jsonc
{
  "id": "0YBfbouSg", "name": "Zone JAUNE", "type": "delivery",
  "shop": { "id": "muX8iUUIg", "name": "Thiais" },
  "minOrderAmount": 4000,                 // cents — zone minimum (distinct from free-shipping threshold)
  "deliveryPrices": [...], "messages": [...], "specialEvents": [...],
  "preferredTimeSlotSchedule": {...}, "suggestedTimeSlots": [...],
  "deliverySlots": [ /* the windows */ ]
}
```
A `deliverySlot` (59 returned in the sample, 56 selectable):
```jsonc
{
  "id": "Kh-DjTXIgR",
  "from": 1781269200000, "to": 1781276400000,   // epoch ms — the window
  "orderUntil": 1781236800000,                   // epoch ms — order-by deadline
  "deliveryMode": "onFleet",
  "isFull": false, "isExpired": false, "isExcluded": false,   // <-- selectable only if all three false
  "daysLimit": 5,
  "extraPrice": { "currency": "EUR", "dutyFree": 0 },          // e.g. peak slots add +3,00 €
  "deliveryPricesWithDeltas": [...], "rate": {...},
  "activeDeliveryPrice": {...}, "initialOrder": null
}
```
This endpoint is the answer to "show me available delivery windows." Filter to
`!isFull && !isExpired && !isExcluded`. Times are epoch-ms; `orderUntil` is the cutoff to
order for that window.

### Selecting a window — partially mapped (⚠ verify before relying on it)

Picking a slot fires **`PATCH /api/cart/initialOrder`**, but the capture on 2026-06-11 was
taken while the account had an **in-progress order** (a placed order still open for
additions — the cart shows the slot as "Commande en cours" and you can add items until
`orderUntil`). In that mode the cart *binds to the existing order* and the request body was
`{ "orderId": "<existingOrderId>" }` — **not** a `timeSlotId` — and a companion
`POST /api/orders/taskDeliveryInfos` with `{ "orderIds": [...] }` fetches delivery info for
those orders. So what we captured is the "attach to in-progress order" path, **not** the
clean "choose a fresh slot for a new cart" contract (which presumably sends a slot `id`).

That clean contract was deliberately **not** captured: changing the slot while a real order
is in progress would alter that order's delivery time (outward-facing, hard to reverse), so
it needs either a fresh-cart state or Doug's explicit OK to change-and-restore. **TODO:
re-capture slot selection from a clean cart.**

## Telemetry to ignore

The site is chatty: requests to `datadoghq.eu`, `sentry.io`, `amplitude.com`,
`launchdarkly.com`, `braze.eu`, `cnstrc.com`, `creativecdn.com`, `cdn.builder.io`, and
`/api/vitals` are analytics/feature-flags and irrelevant to the assistant.
