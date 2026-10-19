# narxoz-canvas-bot

> **Status: frozen.** This project is not maintained any more. It worked, but
> the deployment could not be kept alive: the bot asks the Canvas API for the
> status of assignments and grades around the clock, and the university
> instance eventually blocked the IP address of the server it ran on. The code
> is kept as it is and still runs, but there are no updates. Read
> [Why the project is frozen](#why-the-project-is-frozen) and
> [Proxy (CANVAS_PROXY)](#proxy-canvas_proxy) before running it again.

A Telegram bot that tracks **assignments** and **grades** in the Canvas LMS of
Narxoz University (`https://canvas.narxoz.kz`) and sends you notifications in
**Russian**: when new assignments appear, grades are posted, work is submitted,
and deadlines approach or pass.

**Languages:** [English](README.md) | [Русский](README.ru.md)

- Language: **Go 1.27+** (no CGO, uses `modernc.org/sqlite`).
- Telegram: `go-telegram-bot-api/v5`; rate limiting: `golang.org/x/time/rate`.
- All user-facing messages are Russian; code, comments, logs and this README are
  in English.
- Every deadline is shown and evaluated in **Asia/Almaty (UTC+5)**.

## Why the project is frozen

To know the status of every assignment, the bot has to ask Canvas
continuously:

- every `POLL_INTERVAL` (5 minutes by default) it requests
  `GET /api/v1/courses` and then `GET /api/v1/courses/:id/assignments` for
  **each** active course, following all pagination pages;
- the token is checked with `GET /api/v1/users/self` when it is saved or when
  a refresh is requested;
- the reminder job does **not** call Canvas: it works on the local snapshot in
  SQLite, so it adds no traffic.

For a student with 8 to 10 courses that is about 10 to 12 requests every five
minutes, roughly 3,000 requests a day, always from the same IP address. The
built-in rate limiter allows about 10 requests per second (the Canvas limit),
so it does not slow this pattern down at all. For the university instance the
traffic looks exactly like scraping, and the source IP was eventually blocked:
the requests hang until the timeout and the bot reports
"Canvas не ответил на проверку".

Two practical conclusions:

1. The project is frozen, not broken. The code does its job, but a deployment
   from a blocked address cannot be kept running.
2. Before running it again, either raise `POLL_INTERVAL` (for example `1h` or
   `6h`, which cuts the traffic by 12 to 72 times) or put the bot behind a
   proxy with an address that is not blocked. See
   [Proxy (CANVAS_PROXY)](#proxy-canvas_proxy).

The data is not lost: the SQLite database with the settings, the mute state and
the sealed tokens stays in the `bot-data` volume, so the bot continues from
where it stopped.

## Proxy (CANVAS_PROXY)

`CANVAS_PROXY` routes the **Canvas traffic only** through an HTTP(S) or SOCKS5
proxy. Telegram keeps using the direct connection, and nothing else in the
project changes.

```dotenv
# .env
CANVAS_PROXY=socks5://user:pass@proxy-host:1080
```

**What it gives**

- **A different source IP for Canvas.** The university blocks the address of
  the data center, not the address of the proxy. A proxy in Kazakhstan (or a
  residential one) keeps the bot working while the server itself stays
  blocked.
- **A way out of a throttled or filtered route.** Some providers block
  `canvas.narxoz.kz` entirely: the connection hangs until the timeout. The
  proxy provides a route that works.
- **A single point of control.** If the proxy address changes, only `.env`
  changes, and the bot restarts with a new route.

**What it does not give**

- It does not reduce the number of requests. The polling pattern stays the
  same, so raise `POLL_INTERVAL` as well.
- It does not affect Telegram: the Bot API is always called directly, so the
  bot keeps working even if the proxy is down.
- It does not hide the token from a proxy that logs traffic. Use a proxy you
  trust.

**How it behaves**

- An invalid proxy URL is logged as an error and the client falls back to a
  direct request, so a typo never breaks the bot completely.
- The proxy is only used for `CANVAS_BASE_URL`; the environment variables
  `HTTPS_PROXY` / `HTTP_PROXY` are honoured for other requests by the Go
  standard library.
- Failures that point at the proxy (wrong address, DNS, refused connection)
  are reported to the chat instead of the generic Canvas error.


## Features

- **A chat that only carries notifications.** New assignments, grades,
  submissions, reminders and overdue alerts arrive as messages; nothing else is
  posted there. The **Open** button next to the message input (Telegram's menu
  button) leads to the Mini App.
- **Mini App (Telegram Web App)**: assignments / not submitted / overdue /
  grades / courses / settings with course mutes, reminder lead time, a manual
  Canvas refresh and the Canvas token dialog. Identity comes from Telegram's
  signed `initData` (HMAC-SHA256 with the bot token), so there are no
  passwords, and the app can be locked to the owner's `TELEGRAM_CHAT_ID`.
- **Canvas token from chat or app**: `CANVAS_API_TOKEN` is optional. Paste a
  token in `/setup` (the message carrying it is deleted right away) or in the
  app; it is verified against `/api/v1/users/self`, stored per chat (AES-256-GCM
  or base64 obfuscation, see `CANVAS_TOKEN_SECRET`) and, when Canvas cannot be
  reached, kept for background verification instead of asking again. It survives
  restarts; `/disconnect` removes it.
- **Data sync** every `POLL_INTERVAL`: fetch active courses and their
  assignments (with the embedded submission), diff against SQLite. Follows
  **all** pagination pages, retries with backoff on 429/5xx, per-request
  timeout, and throttles to the Canvas rate limit (about 10 req/s).
- **Notifications**: 🆕 new assignment, ✅ grade posted, 📤 submission detected,
  ⏰ deadline at a configurable lead time (24h/12h/6h), 🔥 1 hour before,
  ⚠️ "deadline passed, not submitted", sent **once**. A submitted assignment is
  simply "сдано" everywhere.
- **Reminder job** (separate schedule) applies only to **unsubmitted**
  assignments; submitting at any time stops future reminders.
- **Courses that disappear** from the API are marked inactive (never deleted),
  so a transient API glitch cannot trigger false "new assignment" alerts. A
  course whose fetch fails keeps its assignments and their deadlines visible:
  one broken course cannot empty the dataset.
- **Commands remain** as an alternative to the app (`/status`, `/refresh`,
  `/setup`, `/mute`, ...); when the app is configured they answer with compact
  summaries instead of interactive lists. Without `WEBAPP_URL` the bot keeps the
  full menu keyboard and inline lists, so nothing regresses for chat-only use.
- **Only private chats are notified.** A chat is subscribed when it presses
  **/start** in a private chat; groups are never subscribed, so personal
  assignments and grades cannot leak into a group. The token dialog works in a
  private chat only, because the bot deletes the message with the token.
- **Reliability**: JSON `slog` logging, one-off "token missing/expired" notice,
  panic recovery per update and per job, graceful SIGINT/SIGTERM shutdown.
- **The bot never freezes**: a manual refresh and the first sync after `/setup`
  run in the background, and the polls are serialised, so the bot keeps
  answering while Canvas is being walked.

## Requirements

| What            | Version / note                                                       |
|-----------------|----------------------------------------------------------------------|
| Go              | 1.27 or newer (only for a local build; the Docker image ships it)     |
| Docker + Compose| v2 or newer (recommended way to run)                                  |
| Canvas token    | from Canvas: avatar → Account → Settings → Approved Integrations      |
| Telegram token  | from [@BotFather](https://t.me/BotFather)                             |
| Public HTTPS    | only for the Mini App (the chat works without it)                    |

You do **not** need a public server to use the bot. The Mini App needs a
reachable HTTPS address, everything else works from a home machine.

## Installation

### 1. Create the Telegram bot

1. Message [@BotFather](https://t.me/BotFather) and send `/newbot`.
2. Pick a name and a username ending in `bot`.
3. Copy the token: this is `TELEGRAM_BOT_TOKEN`.
4. Optional: to learn your `TELEGRAM_CHAT_ID`, press **/start** on your bot and
   read the logs (the subscriber id is recorded), or ask
   [@userinfobot](https://t.me/userinfobot).

> Leaving `TELEGRAM_CHAT_ID` empty makes the bot notify every chat that pressed
> **/start**, which is convenient for a personal deployment.

### 2. Get the code

```bash
git clone <your-repo-url> narxoz-canvas-bot
cd narxoz-canvas-bot
```

### 3. Configure

```bash
cp .env.example .env        # Windows: copy .env.example .env
$EDITOR .env                # at minimum set TELEGRAM_BOT_TOKEN
```

`TELEGRAM_BOT_TOKEN` is the only required value. `CANVAS_API_TOKEN` may stay
empty: the bot then asks for the token inside the chat with `/setup` and
remembers it in the database. See the [Configuration](#configuration) table
for every variable.

### 4. Run it

Docker Compose (recommended):

```bash
docker compose up -d --build
docker compose logs -f bot
```

Local Go build:

```bash
set -a; . ./.env; set +a      # export vars (Linux/macOS); on Windows: $env:X="..."
make run                      # or: go run ./cmd/bot
```

### 5. Connect Canvas

Press **/start**, then send `/setup` and paste the Canvas token. The bot
verifies it, deletes your message and starts the sync. Details in
[Connect Canvas inside the chat](#connect-canvas-inside-the-chat).

## Configuration

Configuration is read from the environment (a `.env` file works too) and
validated at startup: the process exits with a clear error if a required value
is missing or malformed.

| Variable                | Required | Default                     | Description                                                |
|-------------------------|----------|-----------------------------|------------------------------------------------------------|
| `TELEGRAM_BOT_TOKEN`    | yes      | -                           | Bot token from @BotFather.                                 |
| `CANVAS_API_TOKEN`      | no       | *(unset)*                   | Canvas access token. Leave empty and connect Canvas with `/setup` in the chat. When set, it is authoritative for the background sync. |
| `CANVAS_TOKEN_SECRET`   | no       | *(unset)*                   | Key that encrypts chat-entered tokens at rest (AES-256-GCM). Empty means base64 obfuscation only. |
| `CANVAS_BASE_URL`       | no       | `https://canvas.narxoz.kz`  | Canvas instance URL.                                       |
| `CANVAS_PROXY`          | no       | *(unset)*                   | Proxy for Canvas traffic only (`http://user:pass@host:port`, `socks5://host:1080`). For hosts that cannot reach Canvas directly. |
| `TELEGRAM_CHAT_ID`      | no       | *(unset)*                   | Primary notification chat. If unset, every chat that pressed `/start` is notified. |
| `TELEGRAM_API_ENDPOINT` | no       | *(unset)*                   | Self-hosted Bot API endpoint; must contain two `%s` (token, method). |
| `POLL_INTERVAL`         | no       | `5m`                        | Sync frequency (bare numbers mean minutes).                |
| `REMINDER_INTERVAL`     | no       | `30m`                       | Reminder job frequency.                                    |
| `REMINDER_LEAD`         | no       | `24`                        | Early reminder lead in hours: `6`, `12` or `24`.           |
| `TZ`                    | no       | `Asia/Almaty`               | Timezone for displayed/compared deadlines.                 |
| `LOG_LEVEL`             | no       | `info`                      | `debug` / `info` / `warn` / `error`.                       |
| `DB_PATH`               | no       | `./bot.db`                  | SQLite file. Docker Compose sets `/data/bot.db`.           |
| `REQUEST_TIMEOUT`       | no       | `30s`                       | Per-request Canvas timeout.                                |
| `WEBAPP_DOMAIN`         | no       | *(unset)*                   | Mini App host, e.g. `narxoz-bot.duckdns.org`. Used by Caddy only. |
| `WEBAPP_URL`            | no       | *(unset)*                   | Public HTTPS address of the app, e.g. `https://narxoz-bot.duckdns.org`. Enables the **Open** menu button and the app itself. |
| `WEBAPP_ADDR`           | no       | `:8080`                     | Local listen address behind the reverse proxy.             |
| `WEBAPP_DEV_CHAT_ID`    | no       | *(unset)*                   | Development only: authenticate a local browser as this chat without Telegram. **Never set on a public deployment.** |

## Connect Canvas inside the chat

1. Start the bot and press **🔑 Canvas API** (or send `/setup`).
2. The bot explains how to get a token: Canvas → avatar → **Account** →
   **Settings** → **Approved Integrations** → **New Access Token** →
   **Generate Token**. Copy it immediately, Canvas shows it once.
3. Send the token as a single message. The bot:
   - answers "⏳ Проверяю токен в Canvas…";
   - calls `GET /api/v1/users/self` with that token and shows the Canvas user
     it authenticated as (or explains a 401/403);
   - **deletes your message** with the token;
   - stores the token per chat, sealed (`CANVAS_TOKEN_SECRET` means
     AES-256-GCM);
   - polls Canvas right away and shows your real assignments.
4. Missing deadlines and grades then arrive automatically. `/canvas` shows the
   connection state, `/disconnect` deletes the stored token, `/cancel` leaves
   the setup dialog, **🔄 Обновить** polls Canvas on demand.

Notes:

- With `CANVAS_API_TOKEN` set in `.env`, that token drives the background sync
  for all subscribers; chat tokens are still used for on-demand checks in the
  chat they were entered in.
- Without `CANVAS_API_TOKEN`, the **first** chat token that connects drives the
  background sync (the store keeps one shared dataset, it is designed as a
  personal bot). A token entered in the Mini App never takes that sync over.
- The SQLite database holds the sealed token. Set `CANVAS_TOKEN_SECRET` on a
  shared/VDS machine; without it the token is only base64-obfuscated, and the
  bot says so in the log at startup.
- The token dialog works in a **private chat only**: the bot deletes the
  message with the token, and that must not happen in a group.
- The first sync after `/setup` runs in the background, so the bot keeps
  answering while Canvas is being walked. The list arrives in the chat a bit
  later.

## The Mini App (Telegram Web App)

### What it is

The app is served by the bot process itself (`internal/webapp`), behind the
Caddy reverse proxy that terminates HTTPS. Telegram shows it after a tap on the
**Open** button that the bot installs next to the message input
(`setChatMenuButton` → `web_app`). Inside, everything the chat used to do with
buttons now lives in one native-feeling interface.

The HTTP server always starts, even without `WEBAPP_URL`, so that `/healthz`
answers and the reverse proxy has a target. Only the **Open** button in the
chat depends on `WEBAPP_URL`.

### Design contract

Documented here so later changes stay coherent (this section is documentation,
not shipped to the browser):

- **Purpose:** operate, meaning "what do I owe right now, what changed, is the
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
  contrast: names 13.9 to 21:1, secondary text 6.9 to 7.6:1, status 5.1 to
  8.2:1).
- **Responsive:** single column, `max-width: 720px`, safe-area insets, tab bar
  fixed at the bottom; the layout is verified at 390x844 and 1280x800.
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
- The endpoints check the HTTP method: the ones that change something answer
  `405` to a `GET`, so a link preview or a crawler cannot trigger a Canvas
  sync.
- The Telegram user id is used as the chat id, so the app and the bot share the
  same Canvas token, mutes and settings.
- With `TELEGRAM_CHAT_ID` set, the API answers only that user (403 otherwise).
  Without it, the app is open to every user of the bot, so set it when the bot
  is not strictly personal.
- The connectivity check reports only the caller's own Canvas account, and a
  token entered in the app never takes over the background sync.
- The app sends a strict `Content-Security-Policy` (its own scripts plus the
  Telegram SDK only), `X-Frame-Options` and `X-Content-Type-Options`, also
  when it runs without the Caddy proxy.
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

## Run with Docker Compose

```bash
cp .env.example .env          # fill in the tokens (CANVAS_API_TOKEN may stay empty)
docker compose up -d --build  # build + start bot and Caddy (detached)
docker compose logs -f bot    # follow logs (JSON)
```

Two services come up:

- **bot**: the bot, the poller and the Mini App (HTTP on `:8080`, not published
  to the host). The SQLite database lives on the `bot-data` volume at
  `/data/bot.db` and survives restarts. The container reports its state through
  a healthcheck that polls `/healthz`.
- **caddy**: HTTPS termination and an automatic Let's Encrypt certificate for
  `WEBAPP_DOMAIN`, publishing ports 80 and 443.

The bot process runs as an unprivileged user (uid 10001). A small entrypoint
prepares the data volume and then drops the privileges, so an installation that
was created by an older (root) image keeps working after an update.

Without `WEBAPP_DOMAIN` the app is simply not advertised in Telegram (the chat
keeps its keyboard) and Caddy serves a local placeholder site on `localhost`.
To run only the bot and skip Caddy completely:

```bash
docker compose up -d --build bot
```

Both containers use `restart: unless-stopped`, so they come back after a reboot.

## Deploy to a VDS (step by step)

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

Caddy requests the certificate for that host automatically once the container
starts with `WEBAPP_DOMAIN=narxoz-bot.duckdns.org`.

**c. Get the code onto the server.**

Option 1, via git (push the repo to GitHub/GitLab first):

```bash
git clone <your-repo-url> /opt/narxoz-canvas-bot && cd /opt/narxoz-canvas-bot
```

Option 2, copy from your machine (run locally, not on the server):

```bash
scp -r . root@YOUR_VDS_IP:/opt/narxoz-canvas-bot
```

**d. Configure the secrets.**

```bash
cd /opt/narxoz-canvas-bot
cp .env.example .env
nano .env            # TELEGRAM_BOT_TOKEN=... CANVAS_TOKEN_SECRET=...
chmod 600 .env       # keep the tokens readable only by root
```

`CANVAS_API_TOKEN` can stay empty: connect Canvas from the Telegram chat with
`/setup` or from the Mini App. Set a random `CANVAS_TOKEN_SECRET` so the
chat-entered token is encrypted in the database
(`tr -dc A-Za-z0-9 </dev/urandom | head -c 32`).

For the Mini App also set (step **b** above created the hostname):

```bash
WEBAPP_DOMAIN=narxoz-bot.duckdns.org
WEBAPP_URL=https://narxoz-bot.duckdns.org
```

**e. Start it.**

```bash
docker compose up -d --build
docker compose ps
docker compose logs -f --tail 50
```

You should see `telegram bot is listening for updates`,
`canvas_token":"env"|"chat"|"none"` and, with the app enabled,
`chat menu button points at the mini app` plus Caddy logging that the
certificate was obtained. Then open Telegram, press **/start** and connect
Canvas with `/setup` or via the **Open** button.

**f. Make it survive reboots.** Docker's `restart: unless-stopped` (already in
`docker-compose.yml`) plus the enabled `docker` service bring the bot back
automatically. Verify:

```bash
systemctl is-enabled docker
docker compose ls
```

**g. Update / operate.**

```bash
git pull                                    # or re-scp the sources
docker compose up -d --build                # rebuild + restart
docker compose logs -f --tail 100           # logs
docker compose restart                      # plain restart
docker compose down                         # stop (the volume keeps the DB)
```

**h. Back up the database** (settings, mute state, chat tokens):

```bash
docker compose exec -T bot tar czf - -C /data . > "bot-data-$(date +%F).tar.gz"
```

The command streams the archive to the current directory and does not depend on
the name of the Docker volume. To restore, stop the bot first so it does not
write to the database while the archive is unpacked:

```bash
docker compose stop bot
docker compose run --rm --no-deps -T bot sh -c "tar xzf - -C /data" < bot-data-2026-01-01.tar.gz
docker compose up -d bot
```

## Commands

With the Mini App configured the chat stays a notification channel: the reply
keyboard is gone, the **Open** button next to the input opens the app, and these
commands answer with compact summaries (without the app they keep their
interactive lists and keyboard). The commands work in a group as well, but the
bot never sends assignments, grades or reminders there.

| Command          | Behaviour                                                    |
|------------------|--------------------------------------------------------------|
| `/start`, `/menu`| Greeting and menu.                                           |
| `/help`          | Command reference.                                           |
| `/status`        | Canvas state, last poll, counts, reminder lead.              |
| `/refresh`       | Poll Canvas right now.                                       |
| `/setup`         | Connect or replace the Canvas token inside the chat.         |
| `/canvas`        | Canvas connection details for this chat.                     |
| `/disconnect`    | Delete the stored Canvas token.                              |
| `/cancel`        | Leave the token dialog.                                      |
| `/settings`      | Current settings (reminder lead, sync interval, connection). |
| `/assignments`, `/missing`, `/overdue`, `/grades` | Chat-generated summaries (full lists live in the app). |
| `/course <text>`, `/courses` | List the courses or search one by name.           |
| `/mute <course>`, `/unmute <course>` | Silence notifications for a course.        |

Status icons in chat replies: 🟢 more than 3 days, 🟡 less than 3 days, 🔴 less
than 24h, ⛔ overdue, ✅ submitted, ⚪ no deadline.

## Troubleshooting

**`Conflict: terminated by other getUpdates request` in the logs.**
Two processes are long-polling the same `TELEGRAM_BOT_TOKEN`. Telegram allows
exactly one, so messages are split randomly between them (a `/setup` dialog can
be answered by one instance while the paste goes to the other). Keep a single
instance: `docker compose down` locally before starting the bot on the VDS (and
vice versa), and check for leftovers with `docker ps` / `ps aux | grep bot`.

**No "Open" button in the chat.** It appears only when `WEBAPP_URL` is set and
the bot managed to install it: look for `chat menu button points at the mini
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

**Caddy restarts in a loop right after `docker compose up`.** The site address
in the Caddyfile was empty. Set `WEBAPP_DOMAIN` to a real hostname, or run
`docker compose up -d bot` without the proxy.

**"Canvas не ответил на проверку" / `context deadline exceeded`.**
The token is stored anyway (marked *ждёт проверки ⏳*) and verified in the
background, so it does not have to be pasted again. Check the raw error in the
app's settings or in `docker compose logs | grep canvas`. Typical causes:

- the host or container where the bot runs cannot reach `CANVAS_BASE_URL`
  (firewall, broken DNS, blocked outbound TLS). Verify from that host with
  `curl -sS -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer x' \
   https://canvas.narxoz.kz/api/v1/users/self` (expect `401` for a bogus token);
- a transient Canvas or network hiccup: press the refresh button in the app.

**A VDS whose datacenter IP is blocked by the university.** Some hosts (and whole
countries) cannot open `canvas.narxoz.kz` at all: connections hang until the
timeout. Diagnose from the server:

```bash
curl -s https://ipinfo.io/json | head -c 200; echo                     # where the VDS actually is
getent hosts canvas.narxoz.kz                                          # DNS answer
curl -sS -o /dev/null -w 'http=%{http_code} connect=%{time_connect}s total=%{time_total}s\n' \
  --max-time 20 -4 -H 'Authorization: Bearer x' https://canvas.narxoz.kz/api/v1/users/self
curl -sS -o /dev/null -w 'http=%{http_code} total=%{time_total}s\n' --max-time 20 \
  --resolve canvas.narxoz.kz:443:91.214.174.9 -H 'Authorization: Bearer x' https://canvas.narxoz.kz/api/v1/users/self
```

If the first command hangs while the resolved-IP one works, the problem is DNS;
if both hang, the route to the Canvas IP is filtered. Options: move the bot to a
VDS in Kazakhstan, run it on a network that can reach Canvas, or keep the VDS and
route Canvas traffic through a proxy with a local IP:

```dotenv
# .env on the VDS
CANVAS_PROXY=socks5://user:pass@kazakh-proxy-host:1080
```
`CANVAS_PROXY` affects Canvas only: Telegram keeps using the direct connection.

**"Canvas отклонил токен (401/403)".** The token itself is wrong (not issued on
`CANVAS_BASE_URL`, expired, or copied incompletely). Generate a new one and send
`/setup` again.

**The bot answers nothing.** Check `docker compose logs -f`: the process logs
its startup line and every poller error in JSON. Use `/status` or the app's
settings tab for the Canvas/connection state.

**The database cannot be opened.** The parent directory of `DB_PATH` is created
automatically, so this normally means a permission problem. Check that the
`bot-data` volume is writable by the container user.

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
and a temporary database: state/counts, every list view, mute and lead-time
changes, the token flow (rejected / unreachable / accepted), refresh cooldown,
owner restriction and the SPA fallback.

End-to-end: `internal/bot/flow_test.go` runs the complete `/setup` dialog
against a fake Canvas and a fake Telegram Bot API. It asserts that a wrong
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
├── Dockerfile               # multi-stage, CGO disabled, HEALTHCHECK on /healthz
├── docker-compose.yml       # bot + Caddy (HTTPS), DB and certificate volumes
├── Caddyfile                # automatic Let's Encrypt certificate for the app
├── .dockerignore            # keeps .env / *.db out of the image
├── .env.example             # all variables
├── Makefile                 # build / run / test / vet / fmt / docker
├── README.md                # this file (English)
├── README.ru.md             # the same guide in Russian
└── prompt.md
```
