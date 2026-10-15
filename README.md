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

- **Canvas token from the chat**: `CANVAS_API_TOKEN` is optional. Send `/setup`
  (or press **🔑 Canvas API**), paste a Canvas access token and the bot verifies
  it against `/api/v1/users/self`, stores it per chat (AES-256-GCM or base64
  obfuscation, see `CANVAS_TOKEN_SECRET`), deletes the message that carried the
  token and immediately pulls real courses/assignments into the chat. The token
  survives restarts; `/disconnect` removes it.
- **Live Canvas queries inside the chat**: the **🔄 Обновить** button (or
  `/refresh`) polls Canvas on demand and re-renders the current list in place —
  every list also has a "🔄 Обновить из Canvas" inline button.
- **Data sync** every `POLL_INTERVAL`: fetch active courses and their
  assignments (with the embedded submission), diff against SQLite. Follows
  **all** pagination pages, retries with backoff on 429/5xx, per-request
  timeout, and throttles to the Canvas rate limit (~10 req/s).
- **Notifications**: 🆕 new assignment, ✅ grade posted, 📤 submission detected,
  ⏰ deadline at a configurable lead time (24h/12h/6h), 🔥 1 hour before,
  ⚠️ "deadline passed, not submitted" — sent **once**.
- **Reminder job** (separate schedule) applies only to **unsubmitted**
  assignments. Submitting at any time stops future reminders; a late submission
  shows "сдано с опозданием" in the lists.
- **Courses that disappear** from the API are marked inactive (never deleted),
  so a transient API glitch cannot trigger false "new assignment" alerts.
- **Menu-driven**: a persistent reply keyboard plus inline (callback) buttons
  for lists, course browsing, settings and Canvas setup — everything works
  **without typing commands**. Commands remain as an alternative.
- **Reliability**: JSON `slog` logging, one-off "token missing/expired" notice,
  panic recovery per update and per job, graceful SIGINT/SIGTERM shutdown.

## Project structure

```
.
├── cmd/bot/main.go          # entrypoint: config, wiring, graceful shutdown
├── internal/
│   ├── canvas/              # Canvas HTTP client (pagination, rate limit, retries,
│   │                        # swappable token, live probe test)
│   ├── config/              # env configuration + validation
│   ├── model/               # shared domain types (Course, Assignment, Event)
│   ├── notify/              # Telegram delivery + Russian message formatting
│   ├── poller/              # scheduler + pure diff/reminder logic
│   ├── runtime/             # shared runtime stats (poller -> bot)
│   ├── secrets/             # sealing/encryption of chat-entered Canvas tokens
│   ├── store/               # SQLite repository (courses, assignments, settings,
│   │                        # subscribers, per-chat Canvas tokens)
│   └── bot/                 # update loop, menus, pagination, callbacks, /setup
├── Dockerfile               # multi-stage, CGO disabled
├── docker-compose.yml       # volume for the DB, restart: unless-stopped
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

## 2) Create the Telegram bot

1. Message [@BotFather](https://t.me/BotFather) → `/newbot`.
2. Pick a name and a username ending in `bot`.
3. Copy the token into `TELEGRAM_BOT_TOKEN`.
4. Optional: to learn your `TELEGRAM_CHAT_ID`, press **/start** on your bot and
   read the logs (the subscriber id is recorded), or use
   [@userinfobot](https://t.me/userinfobot).

> Leaving `TELEGRAM_CHAT_ID` empty makes the bot notify every chat that pressed
> **/start** — convenient for a personal deployment.

## 3) Run locally

```bash
# Requires Go 1.27+
cp .env.example .env          # then edit .env
set -a; . ./.env; set +a      # export vars (Linux/macOS) — on Windows: $env:X="..."
make run                      # or: go run ./cmd/bot
```

Other targets: `make build`, `make test`, `make vet`, `make fmt`, `make tidy`.

## 4) Run with Docker Compose (recommended)

```bash
cp .env.example .env          # fill in the tokens (CANVAS_API_TOKEN may stay empty)
docker compose up -d --build  # build + start (detached)
docker compose logs -f        # follow logs (JSON)
```

The SQLite database lives on the `bot-data` volume at `/data/bot.db` and
survives restarts. The container runs with `restart: unless-stopped` and no
inbound ports are exposed — the bot only makes outbound connections
(Telegram long polling + Canvas).

## 5) Deploy to a VDS (step by step)

Any small Linux VDS (1 vCPU / 1 GB RAM / 10 GB disk is plenty) works.

**a. Prepare the server (once).**

```bash
ssh root@YOUR_VDS_IP
apt update && apt upgrade -y
apt install -y ca-certificates curl git ufw
# Docker Engine + compose plugin (official convenience script):
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
# Basic firewall: SSH in, everything else out (no ports are needed for the bot):
ufw allow OpenSSH && ufw --force enable
```

**b. Get the code onto the server.**

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
`/setup` (recommended). Set a random `CANVAS_TOKEN_SECRET` so the chat-entered
token is encrypted in the database (`tr -dc A-Za-z0-9 </dev/urandom | head -c 32`).

**d. Start it.**

```bash
docker compose up -d --build
docker compose ps
docker compose logs -f --tail 50
```

You should see `telegram bot is listening for updates` and
`canvas_token":"env"|"chat"|"none"`. Then open Telegram, press **/start** and
connect Canvas with `/setup`.

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

## Commands & buttons

The persistent reply keyboard (shown after `/start`) exposes everything. All
lists use inline buttons `[◀️ Назад] [n/m] [Вперёд ▶️]` (7 per page) and **edit
the same message** instead of sending new ones.

| Button        | Command          | Shows                                                  |
|---------------|------------------|--------------------------------------------------------|
| 📋 Задания    | `/assignments`   | Upcoming assignments, nearest deadline first.          |
| ⚠️ Не сдано   | `/missing`       | Unsubmitted assignments with a future deadline.        |
| ⛔ Просрочено | `/overdue`       | Overdue, still-unsubmitted assignments.                |
| 📝 Оценки     | `/grades`        | Last 10 grades, most recent first.                     |
| 📚 Курсы      | `/course <text>` | Inline course list → tap a course → its assignments.   |
| ⚙️ Настройки  | `/settings`      | Canvas state, per-course mute (🔔/🔕), reminder lead, connectivity check. |
| 🔄 Обновить   | `/refresh`       | Polls Canvas right now and shows fresh assignments.    |
| 🔑 Canvas API | `/setup`         | Connect / replace the Canvas token from the chat.      |

Status icons: 🟢 >3 days, 🟡 <3 days, 🔴 <24h, ⛔ overdue, ✅ submitted, ⚪ no
deadline. Extra commands: `/status`, `/canvas`, `/cancel`, `/disconnect`,
`/mute <course>`, `/unmute <course>`, `/menu`, `/help`.

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

End-to-end: `internal/bot/flow_test.go` runs the complete `/setup` dialog
against a fake Canvas and a fake Telegram Bot API — it asserts that a wrong
token is rejected, the token message is deleted, the token is stored sealed,
the verified token reaches the client and that live Canvas data appears in the
chat.

Opt-in live check against the real instance (confirms reachability and that an
invalid token is classified as 401):

```bash
LIVE_CANVAS=1 go test ./internal/canvas -run TestLiveCanvasProbe -v
# or: LIVE_CANVAS=1 LIVE_CANVAS_URL=https://your-canvas.example go test ...
```
