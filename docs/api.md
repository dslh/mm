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
expire; when calls start returning 401/redirects, re-do the manual login and re-save.

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
| GET | `/navigation` | Category navigation tree. |
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
  `type=RECIPE` returns recipes instead. Response:
  ```json
  { "count": 50, "items": [ /* product objects */ ], "algoVersion": "...", "next": "<cursor?>" }
  ```
  `next` appears to be the pagination cursor (not yet exercised).

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
- `quantity` is the target count (in `granularity` units / pieces).
- `source` is optional analytics metadata; safe to omit or set a fixed value.
- Returns the **full cart object** (same schema as `GET /cart`).
- `quantity: 0` removes the product from the cart. Verified 2026-06-11 (PATCH returned
  200 and the product was absent from the returned cart).

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
items) and may no longer be available — when reordering, fall back to a name/SKU search
(`/search2`) if a direct add by `canonicalId` fails.

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

## Telemetry to ignore

The site is chatty: requests to `datadoghq.eu`, `sentry.io`, `amplitude.com`,
`launchdarkly.com`, `braze.eu`, `cnstrc.com`, `creativecdn.com`, `cdn.builder.io`, and
`/api/vitals` are analytics/feature-flags and irrelevant to the assistant.
