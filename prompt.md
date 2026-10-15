You are an experienced Go developer. Build a complete Telegram bot that
tracks assignments and grades in the Canvas LMS of Narxoz University and
sends notifications. Name the project narxoz-canvas-bot.

IMPORTANT: ALL user-facing messages sent by the bot (notifications,
menus, lists, errors) MUST be written in Russian. Code, comments, logs
and README in English.

=== CONTEXT ===
Canvas instance: https://canvas.narxoz.kz (standard Canvas LMS by
Instructure). API is available over HTTPS at
https://canvas.narxoz.kz/api/v1/
Authentication: "Authorization: Bearer <CANVAS_API_TOKEN>" header.
Responses are JSON, dates in ISO 8601 UTC. Pagination via the Link
header (rel="next") — you MUST follow all pages.

Key endpoints:
- GET /api/v1/courses?enrollment_state=active — active courses
  (id, name, course_code)
- GET /api/v1/courses/:course_id/assignments?include[]=submission
  — course assignments (id, name, points_possible, due_at, unlock_at,
  lock_at, submission_types, html_url) plus embedded submission
  (score, grade, submitted_at, graded_at, workflow_state)
- GET /api/v1/users/self/missing_submissions — unsubmitted assignments
- GET /api/v1/users/self — token validity check

=== REQUIREMENTS ===
Language: Go (latest stable). Clean project structure:
cmd/bot/main.go, internal/canvas (API HTTP client),
internal/store (SQLite repository), internal/notify (formatting and
Telegram delivery), internal/poller (scheduler).
Libraries: go-telegram-bot-api/telegram-bot-api (or go-telegram/bot),
mattn/go-sqlite3 or modernc.org/sqlite (no CGO), golang.org/x/time/rate.

1. CONFIGURATION (env variables, validated at startup):
   TELEGRAM_BOT_TOKEN, TELEGRAM_CHAT_ID, CANVAS_API_TOKEN,
   CANVAS_BASE_URL (default https://canvas.narxoz.kz),
   POLL_INTERVAL (default 5m), TZ (default Asia/Almaty, UTC+5).
   CRITICAL: convert all due_at timestamps from UTC to Asia/Almaty
   (UTC+5) both for display in Telegram and for reminder logic.

2. POLLER (scheduler):
   - Every POLL_INTERVAL minutes: fetch courses → for each course fetch
     assignments with include[]=submission → diff against state in
     SQLite.
   - SQLite tables: courses(id, name, updated_at), assignments(
     course_id, assignment_id, name, points, due_at, submitted_at,
     graded_at, score, grade, notified_new, notified_deadline24h,
     notified_deadline1h, notified_overdue, last_seen).
   - NEW ASSIGNMENT detection: assignment_id not in DB → send
     notification "🆕 Новое задание" with name, course (course.name),
     points (points_possible), deadline (due_at in Asia/Almaty; if
     null — "без дедлайна") and html_url link.
   - NEW GRADE detection: for a known assignment graded_at or score
     changed vs DB → notification "✅ Оценка поставлена": course,
     name, "score / points_possible", grade.
   - SUBMISSION detection: submitted_at appeared → notification
     "📤 Работа отправлена" (quiet confirmation that the system saw
     the submission). Once submitted, the assignment automatically
     stops qualifying for deadline reminders.
   - DEADLINE REMINDERS (separate job every 30 minutes): apply ONLY
     to UNSUBMITTED assignments — where submitted_at IS NULL and the
     submission workflow_state is NOT graded/submitted. Logic:
     * deadline < 24 hours away and notified_deadline24h=false →
       "⏰ Дедлайн через 24 часа" + name, course, points, link;
     * deadline < 1 hour away and notified_deadline1h=false →
       "🔥 Дедлайн через час!";
     * deadline passed, still unsubmitted, notified_overdue=false →
       send ONE notification "⚠️ Дедлайн прошёл, задание не сдано"
       (then set the flag permanently to avoid spam).
     If the user submits AFTER a reminder but BEFORE the deadline —
     future reminders are automatically skipped (submitted_at IS NULL
     condition). If submitted after the deadline — reset
     notified_overdue, deliver the usual "📤 Работа отправлена", and
     show status in /assignments as "сдано с опозданием".
   - Canvas rate limit (~10 req/sec max): client with x/time/rate,
     retries with exponential backoff on 429/5xx, 30s request
     timeout context.
   - Courses missing from the API response are marked inactive, NOT
     deleted (otherwise an API glitch would trigger false "new
     assignment" notifications).

3. TELEGRAM BOT (commands + BUTTONS, primary control via menus):
   MAIN REQUIREMENT: all functionality is accessible WITHOUT typing
   text commands — via button menus. Commands remain as an
   alternative for advanced use.

   Main menu (reply keyboard, shown after /start):
   [ 📋 Задания ] [ ⚠️ Не сдано ]
   [ 📝 Оценки ] [ ⛔ Просрочено ]
   [ 📚 Курсы ]  [ ⚙️ Настройки ]
   (All button labels and everything the bot replies in Russian.)

   Button "Задания" → equivalent of /assignments: upcoming assignments
     sorted by due_at ASCENDING (closest deadline first). Each item:
     status emoji (🔴 <24h, 🟡 <3 days, 🟢 more, ✅ submitted,
     ⛔ overdue unsubmitted), course, name, points, deadline, link.
   Button "Не сдано" → /missing: unsubmitted assignments with a
     deadline that has not yet passed.
   Button "Оценки" → /grades: last 10 grades sorted by graded_at DESC.
   Button "Просрочено" → /overdue: overdue unsubmitted assignments
     (useful when a teacher reopens submissions).
   Button "Курсы" → INLINE buttons listing active courses
     (dynamically from the API). Tapping a course → all its
     assignments. A "⬅️ Назад" button on top.
   Button "Настройки" → inline menu:
     * list of courses with current notification state (🔔/🔕),
       tapping toggles mute/unmute for that course;
     * reminder interval button (switch between 24h/12h/6h before
       deadline, persisted in DB);
     * "🧪 Проверить связь" button → ping /users/self and report
       token validity.

   LIST PAGINATION — inline buttons under the message:
   [ ◀️ Назад ] [ 2/5 ] [ Вперёд ▶️ ] — 7 assignments per page,
   EDIT the existing message (editMessageText) instead of sending
   a new one.

   BUTTON TECHNICAL DETAILS:
   - Reply keyboard for the main menu (persistent, always above the
     input field, one_time_keyboard=false, is_persistent=true).
   - Inline keyboard for in-list navigation and settings.
   - Handle CallbackQuery and call answerCallbackQuery (otherwise
     the user sees a stuck "loading clock" on the button). All
     handlers use unique callback_data prefixes (c:course_id,
     p:page, m:mute, s:setting, etc.), callback_data ≤ 64 bytes.
   - Taps on stale/already-edited messages (double taps) must not
     crash the bot — reply "⏳ Уже обновляю..." or silently ignore
     duplicates.
   - Always re-render buttons with fresh state after an action
     (e.g., after muting a course, re-sort and re-render).

   Text commands (duplicate the buttons): /start (shows the main
   menu + short help), /assignments, /missing, /overdue, /grades,
   /course <substring>, /status, /mute, /unmute, /help, /menu
   (re-show the main menu).
   All messages via HTML or MarkdownV2 (escape special chars!),
   graceful shutdown on SIGINT/SIGTERM (finish the current poll,
   close the DB).

4. RELIABILITY:
   - On API 401 — notify Telegram "токен истёк" once and keep
     retrying, no spam.
   - Logging via slog (JSON, level from LOG_LEVEL env), errors must
     not crash the process.
   - Each poll iteration is independent: a panic in one goroutine
     must not bring everything down (recover + log).
   - Telegram sends with retries.

5. DEPLOYMENT:
   - Multi-stage Dockerfile (golang:alpine → alpine), docker-
     compose.yml with a volume for the DB and restart:
     unless-stopped.
   - .env.example with all variables.
   - README.md: how to get a Canvas API token (Profile → Settings →
     New Access Token), how to create the bot via @BotFather, how to
     run with docker compose, how to deploy to a VPS.
   - Makefile: build, run, test.

6. TESTS:
   - Unit tests for diff logic: new assignment, new grade, deadline
     reminder fires ONLY for unsubmitted assignments, reminder does
     NOT fire for submitted ones, overdue notification fires exactly
     once, callback button routing.
   - Table-driven tests for /assignments sorting and pagination.

Generate the complete working code for all files, no TODOs or
stubs. At the end print the project tree and a step-by-step
run instruction.