# narxoz-canvas-bot

A Telegram bot that tracks **assignments** and **grades** in the Canvas LMS of
Narxoz University (`https://canvas.narxoz.kz`) and sends you notifications in
**Russian**: when new assignments appear, grades are posted, work is submitted,
and deadlines approach or pass.

- Language: **Go 1.27** (no CGO — uses `modernc.org/sqlite`).
- Telegram: `go-telegram-bot-api/v5`; rate limiting: `golang.org/x/time/rate`.
- All user-facing messages are Russian; code, comments, logs and this README are
  in English.
- Every deadline is shown and evaluated in **Asia/Almaty (UTC+5)**.

## Features

- **A chat that only carries notifications.** New assignments, grades,
  submissions, reminders and overdue alerts arrive as messages; nothing else is
  posted there. The **Open** button next to the message input (Telegram's menu
  button) leads to the Mini App.
- **Mini App (Telegram Web App)**: assignments / not submitted / overdue /
  grades / courses / settings with course mutes, reminder lead time, a manual
  Canvas refresh and the Canvas token dialog. Identity comes from Telegram's
  signed `initData` (HMAC-SHA256 with the bot token) — no passwords, and the app
  can be locked to the owner's `TELEGRAM_CHAT_ID`.
- **Canvas token from chat or app**: `CANVAS_API_TOKEN` is optional. Paste a
  token in `/setup` (the message carrying it is deleted right away) or in the
  app; it is verified against `/api/v1/users/self`, stored per chat (AES-256-GCM
  or base64 obfuscation, see `CANVAS_TOKEN_SECRET`) and, when Canvas cannot be
  reached, kept for background verification instead of asking again. It survives
  restarts; `/disconnect` removes it.
- **Data sync** every `POLL_INTERVAL`: fetch active courses and their
  assignments (with the embedded submission), diff against SQLite. Follows
  **all** pagination pages, retries with backoff on 429/5xx, per-request
  timeout, and throttles to the Canvas rate limit (~10 req/s).
- **Notifications**: 🆕 new assignment, ✅ grade posted, 📤 submission detected,
  ⏰ deadline at a configurable lead time (24h/12h/6h), 🔥 1 hour before,
  ⚠️ "deadline passed, not submitted" — sent **once**. A submitted assignment is
  simply "сдано" everywhere.
- **Reminder job** (separate schedule) applies only to **unsubmitted**
  assignments; submitting at any time stops future reminders.
- **Courses that disappear** from the API are marked inactive (never deleted),
  so a transient API glitch cannot trigger false "new assignment" alerts.
- **Commands remain** as an alternative to the app (`/status`, `/refresh`,
  `/setup`, `/mute`, …); when the app is configured they answer with compact
  summaries instead of interactive lists. Without `WEBAPP_URL` the bot keeps the
  full menu keyboard and inline lists, so nothing regresses for chat-only use.
- **Reliability**: JSON `slog` logging, one-off "token missing/expired" notice,
  panic recovery per update and per job, graceful SIGINT/SIGTERM shutdown.

## Project structure

```
.
├── cmd/bot/main.go          # entrypoint: config, wiring, graceful shutdown
├── internal/
│   ├── canvas/              # Canvas HTTP client (pagination, rate limit, retries,
│   │                        # swappable token, live probe test)
│   ├── canvasconn/          # Canvas connection state shared by bot and Mini App
│   ├── config/              # env configuration + validation
│   ├── model/               # shared domain types (Course, Assignment, Event)
│   ├── notify/              # Telegram delivery + Russian message formatting
│   ├── poller/              # scheduler + pure diff/reminder logic
│   ├── runtime/             # shared runtime stats (poller -> bot)
│   ├── secrets/             # sealing/encryption of chat-entered Canvas tokens
│   ├── store/               # SQLite repository (courses, assignments, settings,
│   │                        # subscribers, per-chat Canvas tokens)
│   ├── webapp/              # Mini App: HTTPS server, initData auth, JSON API
│   │   └── assets/          #      single-page interface (HTML/CSS/JS, embedded)
│   └── bot/                 # update loop, commands, /setup, notification texts
├── Dockerfile               # multi-stage, CGO disabled
├── docker-compose.yml       # bot + Caddy (HTTPS), DB and certificate volumes
├── Caddyfile                # automatic Let's Encrypt certificate for the app
├── .dockerignore            # keeps .env / *.db out of the image
├── .env.example             # all variables
├── Makefile                 # build / run / test / vet / fmt / docker
└── README.md
```

## Configuration

Copy `.env.example` to `.env` and fill it in. Configuration is validated at
startup — the process exits with a clear error if a required value is missing
or malformed.

| Variable                | Required | Default                     | Description                                                |
|-------------------------|----------|-----------------------------|------------------------------------------------------------|
| `TELEGRAM_BOT_TOKEN`    | yes      | —                           | Bot token from @BotFather.                                 |
| `CANVAS_API_TOKEN`      | no       | *(unset)*                   | Canvas access token. Leave empty and connect Canvas with `/setup` in the chat. When set, it is authoritative for the background sync. |
| `CANVAS_TOKEN_SECRET`   | no       | *(unset)*                   | Key that encrypts chat-entered tokens at rest (AES-256-GCM). Empty = base64 obfuscation only. |
| `CANVAS_BASE_URL`       | no       | `https://canvas.narxoz.kz`  | Canvas instance URL.                                       |
| `TELEGRAM_CHAT_ID`      | no       | *(unset)*                   | Primary notification chat. If unset, every chat that pressed `/start` is notified. |
| `TELEGRAM_API_ENDPOINT` | no       | *(unset)*                   | Self-hosted Bot API endpoint; must contain two `%s` (token, method). |
| `POLL_INTERVAL`         | no       | `5m`                        | Sync frequency (bare numbers = minutes).                   |
| `REMINDER_INTERVAL`     | no       | `30m`                       | Reminder job frequency.                                    |
| `REMINDER_LEAD`         | no       | `24`                        | Early reminder lead in hours: `6`, `12` or `24`.           |
| `TZ`                    | no       | `Asia/Almaty`               | Timezone for displayed/compared deadlines.                 |
| `LOG_LEVEL`             | no       | `info`                      | `debug` / `info` / `warn` / `error`.                       |
| `DB_PATH`               | no       | `./bot.db`                  | SQLite file (Docker Compose sets `/data/bot.db`).          |
| `REQUEST_TIMEOUT`       | no       | `30s`                       | Per-request Canvas timeout.                                |
| `WEBAPP_DOMAIN`         | no       | *(unset)*                   | Mini App host, e.g. `narxoz-bot.duckdns.org`. Used by Caddy only. |
| `WEBAPP_URL`            | no       | *(unset)*                   | Public HTTPS address of the app, e.g. `https://narxoz-bot.duckdns.org`. Enables the **Open** menu button and the app itself. |
| `WEBAPP_ADDR`           | no       | `:8080`                     | Local listen address behind the reverse proxy.             |
| `WEBAPP_DEV_CHAT_ID`    | no       | *(unset)*                   | Development only: authenticate a local browser as this chat without Telegram. **Never set on a public deployment.** |

## 1) Connect Canvas inside the chat (recommended)

1. Start the bot and press **🔑 Canvas API** (or send `/setup`).
2. The bot explains how to get a token: Canvas → avatar → **Account** →
   **Settings** → **Approved Integrations** → **New Access Token** →
   **Generate Token**. Copy it immediately, Canvas shows it once.
3. Send the token as a single message. The bot:
   - answers "⏳ Проверяю токен в Canvas…";
   - calls `GET /api/v1/users/self` with that token and shows the Canvas user
     it authenticated as (or explains a 401/403);
   - **deletes your message** with the token;
   - stores the token per chat, sealed (`CANVAS_TOKEN_SECRET` ⇒ AES-256-GCM);
   - polls Canvas right away and shows your real assignments.
4. Missing deadlines/grades then arrive automatically. `/canvas` shows the
   connection state, `/disconnect` deletes the stored token, `/cancel` leaves
   the setup dialog, **🔄 Обновить** polls Canvas on demand.

Notes:

- With `CANVAS_API_TOKEN` set in `.env`, that token drives the background sync
  for all subscribers; chat tokens are still used for on-demand checks in the
  chat they were entered in.
- Without `CANVAS_API_TOKEN`, the **most recently connected** chat token drives
  the background sync (the store keeps one shared dataset — it is designed as a
  personal bot).
- The SQLite database holds the sealed token. Set `CANVAS_TOKEN_SECRET` on a
  shared/VDS machine; without it the token is only base64-obfuscated.

## 2) The Mini App (Telegram Web App)

### What it is

The app is served by the bot process itself (`internal/webapp`), behind the
Caddy reverse proxy that terminates HTTPS. Telegram shows it after a tap on the
**Open** button that the bot installs next to the message input
(`setChatMenuButton` → `web_app`). Inside, everything the chat used to do with
buttons now lives in one native-feeling interface.

### Design contract

Documented here so later changes stay coherent (this section is documentation,
not shipped to the browser):

- **Purpose:** operate — "what do I owe right now, what changed, is the
  connection healthy". Not a marketing or reading surface.
- **Inherited identity:** it runs inside Telegram, so it uses the client's own
  design language: `--tg-theme-*` colours (light/dark), system font stack,
  grouped list rows, a segmented control, a three-tab bar and a bottom sheet.
  Only status colours are our own, and every status also carries a text label,
  so colour is never the sole signal.
- **Composition:** sticky header (title, connection chip, last sync, refresh),
  then either the filter segment + assignments list (Задания), the course list
  with mute switches (Курсы), or grouped settings sections (Настройки). Row =
  name (2-line clamp) + course + right column with status label, due date and
  score; tapping a row opens a bottom sheet with the full record and a link to
  Canvas.
- **Typography/colour:** 22/15/13 px scale, tabular numerals for dates and
  scores; Telegram theme variables with tuned fallbacks for browsers (verified
  contrast: names 13.9–21:1, secondary text 6.9–7.6:1, status 5.1–8.2:1).
- **Responsive:** single column, `max-width: 720px`, safe-area insets, tab bar
  fixed at the bottom; the layout is verified at 390×844 and 1280×800.
- **Required states:** loading skeletons, empty states per filter, unreachable
  Canvas banner with retry, "Canvas not connected" call to action, token form
  (with "where to get it"), refresh cooldown feedback, optimistic mute/lead
  toggles with rollback, and the local "opened outside Telegram" dev notice.
- **Finish condition:** every state above reachable in a browser against a real
  database, contrast checked, and the token dialog working end to end.

### Security

- Every `/api/*` request must carry Telegram `initData`
  (`Authorization: tma <initData>`), verified with HMAC-SHA256 over the sorted
  field list, keyed by `HMAC_SHA256("WebAppData", bot_token)`; payloads older
  than 24 h are rejected.
- The Telegram user id is used as the chat id, so the app and the bot share the
  same Canvas token, mutes and settings.
- With `TELEGRAM_CHAT_ID` set, the API answers only that user (403 otherwise).
- The static page is embedded in the binary (`go:embed`); no third-party assets
  are loaded except Telegram's own SDK script, and the interface renders without
  it.

### Local development

```bash
# Serve the app against a real database and inspect it in a browser.
WEBAPP_VISUAL=1 WEBAPP_VISUAL_SEED=1 WEBAPP_VISUAL_DB=./bot.db \
  go test ./internal/webapp -run TestVisualApp -v -timeout 0
# then open http://127.0.0.1:8090  (dev auth: WEBAPP_DEV_CHAT_ID)
```

The harness is a test file (`internal/webapp/visual_test.go`), skipped unless
`WEBAPP_VISUAL=1`: it never runs in `go test ./...` and is not part of the
deployed binary.

## 3) Create the Telegram bot

1. Message [@BotFather](https://t.me/BotFather) → `/newbot`.
2. Pick a name and a username ending in `bot`.
3. Copy the token into `TELEGRAM_BOT_TOKEN`.
4. Optional: to learn your `TELEGRAM_CHAT_ID`, press **/start** on your bot and
   read the logs (the subscriber id is recorded), or use
   [@userinfobot](https://t.me/userinfobot).

> Leaving `TELEGRAM_CHAT_ID` empty makes the bot notify every chat that pressed
> **/start** — convenient for a personal deployment.

## 4) Run locally

```bash
# Requires Go 1.27+
cp .env.example .env          # then edit .env
set -a; . ./.env; set +a      # export vars (Linux/macOS) — on Windows: $env:X="..."
make run                      # or: go run ./cmd/bot
```

Other targets: `make build`, `make test`, `make vet`, `make fmt`, `make tidy`.

## 5) Run with Docker Compose (recommended)

```bash
cp .env.example .env          # fill in the tokens (CANVAS_API_TOKEN may stay empty)
docker compose up -d --build  # build + start bot and Caddy (detached)
docker compose logs -f bot    # follow logs (JSON)
```

Two services come up:

- **bot** — the bot, the poller and the Mini App (HTTP on `:8080`, not published
  to the host); the SQLite database lives on the `bot-data` volume at
  `/data/bot.db` and survives restarts;
- **caddy** — HTTPS termination and an automatic Let's Encrypt certificate for
  `WEBAPP_DOMAIN`, publishing ports 80 and 443.

With `WEBAPP_URL` empty the app is simply not advertised in Telegram (the chat
keeps its keyboard). To run only the bot without HTTPS:

```bash
docker compose up -d --build bot
```

Both containers use `restart: unless-stopped`, so they come back after a reboot.

## 6) Deploy to a VDS (step by step)

Any small Linux VDS (1 vCPU / 1 GB RAM / 10 GB disk is plenty) works. With the
Mini App enabled, ports **80 and 443** must be reachable from the internet (80 is
needed for the Let's Encrypt challenge), so pick a host where they are not
blocked.

**a. Prepare the server (once).**

```bash
ssh root@YOUR_VDS_IP
apt update && apt upgrade -y
apt install -y ca-certificates curl git ufw
# Docker Engine + compose plugin (official convenience script):
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
# Firewall: SSH in, HTTP+HTTPS in for the Mini App certificate, everything else out.
ufw allow OpenSSH && ufw allow 80/tcp && ufw allow 443/tcp && ufw --force enable
```

**b. Point a hostname at the server (DuckDNS).**

1. Open <https://www.duckdns.org>, sign in and create a subdomain, e.g.
   `narxoz-bot`.
2. Set its IP to your VDS address (DuckDNS → *current ip*).
3. Check from anywhere: `getent hosts narxoz-bot.duckdns.org` must return the
   VDS IP.

Caddy will request the certificate for that host automatically once the
container starts with `WEBAPP_DOMAIN=narxoz-bot.duckdns.org`.

**c. Get the code onto the server.**

Option 1 — via git (push the repo to GitHub/GitLab first):

```bash
git clone <your-repo-url> /opt/narxoz-canvas-bot && cd /opt/narxoz-canvas-bot
```

Option 2 — copy from your machine (run locally, not on the server):

```bash
scp -r . root@YOUR_VDS_IP:/opt/narxoz-canvas-bot
```

**c. Configure the secrets.**

```bash
cd /opt/narxoz-canvas-bot
cp .env.example .env
nano .env            # TELEGRAM_BOT_TOKEN=... CANVAS_TOKEN_SECRET=...
chmod 600 .env       # keep the tokens readable only by root
```

`CANVAS_API_TOKEN` can stay empty — connect Canvas from the Telegram chat with
`/setup` or from the Mini App. Set a random `CANVAS_TOKEN_SECRET` so the
chat-entered token is encrypted in the database
(`tr -dc A-Za-z0-9 </dev/urandom | head -c 32`).

For the Mini App also set (step **b** above created the hostname):

```bash
WEBAPP_DOMAIN=narxoz-bot.duckdns.org
WEBAPP_URL=https://narxoz-bot.duckdns.org
```

**d. Start it.**

```bash
docker compose up -d --build
docker compose ps
docker compose logs -f --tail 50
```

You should see `telegram bot is listening for updates`,
`canvas_token":"env"|"chat"|"none"` and — with the app enabled —
`chat menu button points at the mini app` plus Caddy logging that the
certificate was obtained. Then open Telegram, press **/start** and connect
Canvas with `/setup` or via the **Open** button.

**e. Make it survive reboots.** Docker's `restart: unless-stopped` (already in
`docker-compose.yml`) plus the enabled `docker` service bring the bot back
automatically. Verify:

```bash
systemctl is-enabled docker
docker compose ls
```

**f. Update / operate.**

```bash
git pull                                    # or re-scp the sources
docker compose up -d --build                # rebuild + restart
docker compose logs -f --tail 100           # logs
docker compose restart                      # plain restart
docker compose down                         # stop (volume keeps the DB)
```

**g. Back up the database** (settings, mute state, chat tokens):

```bash
docker run --rm -v tg_canvas_lms_go_bot-data:/data -v "$PWD:/backup" alpine \
  tar czf /backup/bot-data-$(date +%F).tar.gz -C /data .
```

To restore, put the archive back into the volume with the same command plus
`--strip-components`/`tar xzf` instead of `czf`.

## Commands

With the Mini App configured the chat stays a notification channel: the reply
keyboard is gone, the **Open** button next to the input opens the app, and these
commands answer with compact summaries (without the app they keep their
interactive lists and keyboard):

| Command          | Behaviour                                                    |
|------------------|--------------------------------------------------------------|
| `/start`, `/help`| Greeting / command reference.                                |
| `/status`        | Canvas state, last poll, counts, reminder lead.              |
| `/refresh`       | Poll Canvas right now.                                       |
| `/setup`         | Connect / replace the Canvas token inside the chat.          |
| `/canvas`        | Canvas connection details for this chat.                     |
| `/disconnect`    | Delete the stored Canvas token.                              |
| `/cancel`        | Leave the token dialog.                                     |
| `/assignments`, `/missing`, `/overdue`, `/grades`, `/course <text>` | Chat-generated summaries (full lists live in the app). |
| `/mute <course>`, `/unmute <course>` | Silence notifications for a course.        |

Status icons in chat replies: 🟢 >3 days, 🟡 <3 days, 🔴 <24h, ⛔ overdue,
✅ submitted, ⚪ no deadline.

## Troubleshooting

**`Conflict: terminated by other getUpdates request` in the logs.**
Two processes are long-polling the same `TELEGRAM_BOT_TOKEN`. Telegram allows
exactly one, so messages are split randomly between them (a `/setup` dialog can
be answered by one instance while the paste goes to the other). Keep a single
instance: `docker compose down` locally before starting the bot on the VDS (and
vice versa), and check for leftovers with `docker ps` / `ps aux | grep bot`.

**No "Open" button in the chat.** It appears only when `WEBAPP_URL` is set and
the bot managed to install it — look for `chat menu button points at the mini
app` in the logs; if the call failed (or `err` is logged), check that the URL is
HTTPS and reachable. Re-joining the chat or restarting the bot re-installs it.

**The app opens but shows "Откройте приложение из Telegram".** The request
carried no valid `initData`: open it through the bot's Open button, not by
pasting the URL into a browser. With `TELEGRAM_CHAT_ID` set, only that account
is allowed (other users get "приложение доступно только владельцу бота").

**Caddy cannot get a certificate.** Port 80 must be reachable from the internet
(the HTTP-01 challenge) and the DuckDNS record must point at the VDS:
`docker compose logs caddy | tail -30`, `getent hosts <your-domain>`.
Certificates are cached in the `caddy-data` volume, so restarts do not re-issue
them.

**"Canvas не ответил на проверку" / `context deadline exceeded`.**
The token is stored anyway (marked *ждёт проверки ⏳*) and verified in the
background, so it does not have to be pasted again. Check the raw error in the
app's settings or in `docker compose logs | grep canvas`. Typical causes:

- the host/container where the bot runs cannot reach `CANVAS_BASE_URL`
  (firewall, broken DNS, blocked outbound TLS) — verify from that host with
  `curl -sS -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer x' \
   https://canvas.narxoz.kz/api/v1/users/self` (expect `401` for a bogus token);
- a transient Canvas/network hiccup — press the refresh button in the app.

**"Canvas отклонил токен (401/403)".** The token itself is wrong (not issued on
`CANVAS_BASE_URL`, expired, or copied incompletely). Generate a new one and send
`/setup` again.

**The bot answers nothing.** Check `docker compose logs -f`: the process logs
its startup line and every poller error in JSON. Use `/status` or the app's
settings tab for the Canvas/connection state.

## How time is handled

Canvas returns ISO-8601 UTC timestamps. They are stored in UTC and converted to
`TZ` (`Asia/Almaty`, UTC+5) for display and reminder arithmetic. Asia/Almaty has
no DST, and the code falls back to a fixed `UTC+5` zone when the OS has no
`tzdata`, so the binary behaves identically on a slim Alpine container.

## Tests

```bash
make test    # go test ./... -count=1
```

Unit tests cover the pure diff/reminder logic (new assignment, new grade,
reminder fires only for unsubmitted, overdue fires exactly once, late submission
resets overdue), table-driven `/assignments` sorting/pagination, callback
routing, the Canvas client (pagination, retries, 401, token swapping), the
sealing of tokens and the SQLite token store.

The Mini App has its own tests: `initData` signing/validation (valid, tampered,
foreign token, stale, dev fallback) and the whole JSON API against a fake Canvas
and a temporary database — state/counts, every list view, mute and lead-time
changes, the token flow (rejected / unreachable / accepted), refresh cooldown,
owner restriction and the SPA fallback.

End-to-end: `internal/bot/flow_test.go` runs the complete `/setup` dialog
against a fake Canvas and a fake Telegram Bot API — it asserts that a wrong
token is rejected, the token message is deleted, the token is stored sealed,
the verified token reaches the client and that live Canvas data appears in the
chat.

Opt-in live checks (skipped by default):

```bash
# Real Canvas instance: confirms reachability and that an invalid token is a 401.
LIVE_CANVAS=1 go test ./internal/canvas -run TestLiveCanvasProbe -v
# or: LIVE_CANVAS=1 LIVE_CANVAS_URL=https://your-canvas.example go test ...

# Serve the Mini App against a real database for browser inspection.
WEBAPP_VISUAL=1 WEBAPP_VISUAL_SEED=1 WEBAPP_VISUAL_DB=./bot.db \
  go test ./internal/webapp -run TestVisualApp -v -timeout 0
```
