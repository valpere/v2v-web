# Handoff — v2v-web

State on 2026-10-15: **skeleton that builds and is tested** — static client in
text mode, `/v1` reverse proxy, anonymous visitor cookie, `make check`. Not live.

## 0. Read first
- Contract: `~/wrk/demo/v2v-tma/v2v-tma/docs/API.md` (`/v1` topics, sessions,
  messages, cart, checkout). Engine notes: `v2v-demo/docs/web-api.md`.
- This repo implements the *client* and a BFF. Do not add conversation or pricing
  logic: that is the engine's.

## 1. Backlog (in order)

1. **Engine: trusted-frontend auth (blocks production).** The engine's `/v1`
   accepts only `Authorization: tma <initData>` (or `WEB_DEV_AUTH` on loopback, one
   shared principal). Add a second scheme: `Authorization: Bearer <WEB_TRUSTED_KEY>`
   plus `X-Principal: <id>` — the key identifies a trusted frontend, the header
   names the end user it vouches for (like the channel API's key, per user).
   Constant-time compare, key >= 32 chars, refuse the scheme unless the listener
   is loopback/private, principal charset/length validated, namespaced so it can
   never collide with a Telegram id (`web:` prefix). Until then run the engine
   with `WEB_DEV_AUTH=1` and leave `ENGINE_KEY` empty (every browser shares one
   principal — dev only). *Engine work: write it up for v2v-demo or do it there.*
2. **Abuse limits at the BFF.** A public page spends LLM money: per-IP rate limit
   and a daily cap per visitor (the engine's per-chat limits already apply per
   session; sessions per principal are capped at 20). Decide the limits with Val.
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
Engine: `WEB_ADDR=127.0.0.1:8091 WEB_DEV_AUTH=1 CHECKOUT=mock` (+ the usual dialog
backend keys). Here: `.env` from `.env.example`, `make run`, open
http://127.0.0.1:8086.
