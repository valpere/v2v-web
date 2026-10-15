# AGENTS.md — v2v-web

The **plain web frontend** of the v2v voice/text concierge: a browser client plus
a small backend-for-frontend (BFF) that serves it and forwards `/v1` to the
engine (`~/wrk/demo/v2v-demo/v2v-demo`, packages `internal/webapi`,
`internal/shop`). It holds **no conversation or pricing logic** — sessions,
topics, the grounded dialogue, catalog, cart and order amounts all live in the
engine. Sibling frontends of the same engine: v2v-bot (Telegram) and v2v-tma
(Telegram Mini App).

**Status: first skeleton (2026-10-15)** — static client (text mode), `/v1` proxy
with anonymous visitor principals. Not live. `docs/HANDOFF.md` lists what is
missing (first: the engine does not yet accept this server's principal scheme).

## Architecture

```
browser ──► v2v-web (static + /v1 proxy) ──► engine /v1 (WEB_ADDR) ──► LLM / STT / TTS
            cookie v2v_visitor ─► X-Principal: web:<id> + Bearer <ENGINE_KEY>
```

The API is the engine's end-user API: contract in `~/wrk/demo/v2v-tma/v2v-tma/docs/API.md`,
implementation notes in `v2v-demo/docs/web-api.md`. Browsers carry no Telegram
`initData`, so this server vouches for each visitor (see `internal/server`).

## Guardrails
1. **Text mode is the default and costs nothing.** Cloud voice only on an explicit
   user action; free browser speech APIs otherwise.
2. **Money is server-side only.** The client sends item ids and quantities, never a
   price or an amount; checkout carries no amounts. There is no Stars payment on
   the plain web (Stars need Telegram): checkout is `mock` here until another
   payment method is decided.
3. **A client never chooses its principal.** The BFF strips `X-Principal` and
   `Authorization` from the browser request and sets its own; the engine key
   (`ENGINE_KEY`) never reaches the browser.
4. **Model output is text, never HTML:** render replies with `textContent`.
5. Every `/v1` error is shown to the user in plain words; a `502` means the engine is down.

## Conventions
- Go 1.26+, standard library first, no frameworks. Frontend: vanilla ES6+, plain CSS,
  no bundler (`node --check` in `make check`).
- `make check` (gofmt, vet, `go test -race`, `node --check`, build) must pass before
  every commit. Test first for bug fixes; never rewrite tests together with the code
  they cover.
- No secrets in code or logs; config from env (`.env.example`): `ENGINE_URL`,
  `ENGINE_KEY`, `ADDR`, `WEB_DIR`. Never commit `.env*`.
- The repo is **public**: do not describe an open weakness in commits, issues or
  tracked docs before it is fixed.
- Work on a branch and open a PR (`<type>-<slug>`); one commit per logical change.
- `./tmp/` is gitignored scratch; scripts you reuse go to `./minions/` (Bash, not Python).
- Do not write to other repositories (v2v-demo, v2v-tma, v2v-bot) without explicit
  approval from Valentyn; write instructions into `./tmp/<project>-<topic>.md` instead.
- Communicate in Ukrainian; code, identifiers and comments in English; user-facing
  strings may be Ukrainian.

## Layout
- `cmd/v2v-web/` — wiring and graceful shutdown
- `internal/server/` — static files, `/healthz`, `/v1` reverse proxy, visitor cookie
- `web/` — the client (`index.html`, `assets/app.js`, `assets/app.css`)
- `docs/HANDOFF.md` — status and ordered backlog

## Verification
`make check`; then run the engine with `WEB_ADDR=127.0.0.1:8091 WEB_DEV_AUTH=1`,
`make run` here, open http://127.0.0.1:8086 and pick a topic.
