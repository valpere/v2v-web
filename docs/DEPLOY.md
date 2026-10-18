# Deploy — Oracle host, behind Caddy

Target: the Always Free Micro that already runs `v2v-engine` and `v2v-bot`
(`ssh v2vdemo`). Public surface after this: **80/443 → Caddy → 127.0.0.1:8086
(v2v-web) → 127.0.0.1:8091 (engine web API)**. Nothing else is exposed.

## 0. Prerequisites
- A DNS **A record** `web.pere.org.ua → <server public IP>` (zone is hosted at the
  Cloudflare, DNS only). Check: `dig +short web.pere.org.ua`.
- Oracle console → VCN → Security List: **ingress** TCP 80 and 443 from 0.0.0.0/0.
- Host firewall (iptables rejects everything but 22 today). Insert **before** the
  final `REJECT` and persist:
  ```
  sudo iptables -I INPUT 5 -p tcp --dport 80  -m state --state NEW -j ACCEPT
  sudo iptables -I INPUT 5 -p tcp --dport 443 -m state --state NEW -j ACCEPT
  sudo netfilter-persistent save      # apt install iptables-persistent if missing
  ```

## 1. Engine: turn the web API on (server `~/v2v-demo/.env`)
```
WEB_ADDR=127.0.0.1:8091
WEB_TRUSTED_KEY=<openssl rand -hex 24>     # the same value goes to v2v-web as ENGINE_KEY
CHECKOUT=mock                              # plain web cannot pay with Stars
# WEB_PRINCIPAL_DAILY_TURNS=100            # default; messages per visitor per day
```
Then `./minions/srv.sh restart engine` (v2v-demo repo) and check the journal for
`web API on http://127.0.0.1:8091`.

## 2. v2v-web: unit, env, binary
`~/v2v-web/.env` on the server (chmod 600, **no quotes**):
```
ENGINE_URL=http://127.0.0.1:8091
ENGINE_KEY=<same as WEB_TRUSTED_KEY>
ADDR=127.0.0.1:8086
TRUST_PROXY=1
PUBLIC_ORIGIN=https://web.pere.org.ua
```
Install the unit (`deploy/v2v-web.service` → `/etc/systemd/system/`),
`sudo systemctl daemon-reload && sudo systemctl enable v2v-web`, then from the
v2v-demo repo: `./minions/srv.sh push-bin web` (builds for the server's arch,
uploads, restarts). The client is embedded in the binary.

## 3. Caddy
`sudo apt install caddy` (or the official repo), put `deploy/Caddyfile` in
`/etc/caddy/Caddyfile`, `sudo systemctl reload caddy`. The certificate is issued on
the first request to the name; watch `journalctl -u caddy`.

## 4. Verify
```
curl -sI https://web.pere.org.ua/            # 200, CSP + HSTS headers
curl -s  https://web.pere.org.ua/v1/topics   # 10 topics
```
Open the page, pick a topic, send a message. Cross-site POSTs must be `403`:
`curl -si -X POST https://web.pere.org.ua/v1/sessions -H 'Origin: https://evil.example' -H 'Content-Type: application/json' -d '{}'`.

## Rollback
`./minions/srv.sh rollback web`; or `sudo systemctl disable --now v2v-web caddy`
and drop the two iptables rules (`-D`). The engine's `WEB_ADDR` can stay: it is loopback.
