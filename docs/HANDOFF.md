# Handoff — v2v-web

State on 2026-10-15: **skeleton that builds and is tested** — static client in
text mode, `/v1` reverse proxy, anonymous visitor cookie, `make check`. Not live.

## 0. Read first
- Contract: `~/wrk/demo/v2v-tma/v2v-tma/docs/API.md` (`/v1` topics, sessions,
  messages, cart, checkout). Engine notes: `v2v-demo/docs/web-api.md`.
- This repo implements the *client* and a BFF. Do not add conversation or pricing
  logic: that is the engine's.

## 1. Backlog (in order)

1. ~~Engine: trusted-frontend auth~~ **DONE 2026-10-15** (v2v-demo PR #12): engine `WEB_TRUSTED_KEY` (>= 32 chars, loopback `WEB_ADDR`, not with `WEB_DEV_AUTH`) + `Authorization: Bearer <key>` + `X-Principal: web:<id>` (`id` = 1–64 of `[A-Za-z0-9_-]`). Set the same value here as `ENGINE_KEY`. Verified locally: visitor cookie keeps one session pool, a visitor without the cookie gets 404 on another's session, a wrong key straight at the engine is 401. The ids this server mints are 128-bit random; the engine only checks shape, so never derive one from client input.
2. ~~Abuse limits at the BFF~~ **DONE 2026-10-15** (`internal/server/limits.go`, `internal/limit`): messages per visitor 6/min + 60/day, per IP 12/min + 120/day, whole site 150/day (Kyiv midnight, separate from the engine's 300/day shared with the bot); new sessions 10/h per visitor, 20/h per IP; 429 + Retry-After; counters in memory (lost on restart). Behind a proxy set `TRUST_PROXY=1`. Engine side: per-principal daily cap `WEB_PRINCIPAL_DAILY_TURNS` (v2v-demo PR #13). Limits are fixed-window (a burst can straddle a boundary: up to 2× for a moment) and in memory (reset on restart); cookie-less clients are bounded by the IP rules, IPv6 by /64. A request the engine rejects is refunded.
3. **Voice.** Text mode is free. Browser `SpeechSynthesis` for reading replies;
   cloud STT/TTS only on an explicit button once the engine has a web voice
   endpoint (not built: `POST .../voice`, `GET /v1/tts`).
4. **Voice-to-UI.** Render `ui_actions` (`HIGHLIGHT_ITEM`, `ADD_TO_CART`) —
   today only the returned `cart` is drawn; the catalog `items` are not listed yet.
   Add an items grid per topic with +/- (PUT `/cart/items/{id}`).
5. **Checkout.** Plain web cannot pay with Stars. `CHECKOUT=mock` gives an
   "order placed (demo)" state; a real method is a product decision.
6. **Deploy.** Same Oracle host as the engine (`ssh v2vdemo`): a third unit
   `v2v-web` behind a TLS reverse proxy; `srv.sh` in v2v-demo operates the
   engine+bot units only — extend it when this goes live.
7. **Polish.** Language switch (`lang` uk|en), accessibility pass, a proper
   empty/error state, copy.

## 2. Local run
Engine: `WEB_ADDR=127.0.0.1:8091 WEB_TRUSTED_KEY=<same as ENGINE_KEY> CHECKOUT=mock` (+ the usual dialog
backend keys). Here: `.env` from `.env.example`, `make run`, open
http://127.0.0.1:8086.
