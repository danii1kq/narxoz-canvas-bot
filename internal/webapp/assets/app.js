/* Canvas Mini App.
   Plain ES2020, no build step. The Telegram SDK is used when present (theme,
   expand, back button); the app stays fully usable in a browser without it. */

(() => {
  "use strict";

  const tg = window.Telegram && window.Telegram.WebApp ? window.Telegram.WebApp : null;
  const $ = (sel) => document.querySelector(sel);

  const state = {
    tab: "tasks",
    filter: "all",
    course: null,          // {id, name} when the list is filtered by course
    user: null,
    canvas: null,
    counts: null,
    settings: null,
    sync: null,
    items: [],
    page: 1,
    pages: 1,
    total: 0,
    courses: [],
    loading: true,
    loadingMore: false,
    refreshing: false,
    error: null,
    tokenForm: false,
  };

  /* ---------- helpers ---------- */

  const esc = (s) =>
    String(s == null ? "" : s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");

  const fmtNum = (v) => {
    if (v === null || v === undefined) return "—";
    return new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 2 }).format(v);
  };

  const dtf = new Intl.DateTimeFormat("ru-RU", {
    day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit",
  });

  // relativeDeadline returns a short human label for a due date. Overdue rows
  // are labelled by the status chip instead, so the date itself carries no
  // "просрочено" prefix (labels must never contradict each other).
  const relativeDeadline = (iso) => {
    if (!iso) return "";
    const due = new Date(iso);
    if (Number.isNaN(due.getTime())) return "";
    const now = new Date();
    const dayMs = 86400000;
    const startOfDay = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
    const days = Math.round((startOfDay(due) - startOfDay(now)) / dayMs);
    const time = due.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
    if (days < 0) return dtf.format(due);
    if (days === 0) return `сегодня · ${time}`;
    if (days === 1) return `завтра · ${time}`;
    if (days <= 7) return `через ${days} дн · ${time}`;
    return dtf.format(due);
  };

  const relClass = (status) => {
    switch (status) {
      case "overdue": return "overdue";
      case "today": return "today";
      case "soon": return "soon";
      case "submitted":
      case "submitted_late":
      case "graded": return "done";
      default: return "";
    }
  };

  // statusLabel is only shown when it adds information the date text cannot
  // carry: submission state. "today"/"soon"/"later" are expressed by the date.
  // A submitted assignment is simply "сдано" — the deadline no longer matters.
  const statusLabel = (a) => {
    if (a.submitted) return "сдано";
    if (a.status === "overdue") return "просрочено";
    return "";
  };

  const toast = (msg) => {
    const el = $("#toast");
    el.textContent = msg;
    el.hidden = false;
    clearTimeout(toast._t);
    toast._t = setTimeout(() => { el.hidden = true; }, 3200);
    if (tg && tg.HapticFeedback) tg.HapticFeedback.impactOccurred("light");
  };

  /* ---------- api ---------- */

  class ApiError extends Error {
    constructor(message, status, payload) {
      super(message);
      this.status = status;
      this.payload = payload || {};
    }
  }

  const api = async (path, opts = {}) => {
    const headers = Object.assign({}, opts.headers);
    if (tg && tg.initData) headers["Authorization"] = "tma " + tg.initData;
    if (opts.body !== undefined) headers["Content-Type"] = "application/json";
    const res = await fetch(path, {
      method: opts.method || "GET",
      headers,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    });
    let payload = {};
    try { payload = await res.json(); } catch (_) { /* empty body */ }
    if (!res.ok) {
      throw new ApiError(payload.error || `HTTP ${res.status}`, res.status, payload);
    }
    return payload;
  };

  /* ---------- loading ---------- */

  async function loadState() {
    const s = await api("/api/state");
    state.user = s.user;
    state.canvas = s.canvas;
    state.counts = s.counts;
    state.settings = s.settings;
    state.sync = s.sync;
  }

  async function loadAssignments(more) {
    const params = new URLSearchParams({ view: state.filter, page: String(more ? state.page + 1 : 1) });
    if (state.course) params.set("course", String(state.course.id));
    const page = await api("/api/assignments?" + params.toString());
    state.items = more ? state.items.concat(page.items) : page.items;
    state.page = page.page;
    state.pages = page.pages;
    state.total = page.total;
  }

  async function loadCourses() {
    const data = await api("/api/courses");
    state.courses = data.items || [];
  }

  async function reload() {
    state.loading = true;
    state.error = null;
    render();
    try {
      await loadState();
      if (state.tab === "courses") await loadCourses();
      else if (state.tab === "tasks") await loadAssignments(false);
      state.error = null;
    } catch (err) {
      state.error = err.status === 401
        ? { title: "Откройте приложение из Telegram", text: err.message, retry: true }
        : { title: "Не удалось загрузить данные", text: err.message, retry: true };
      if (err.status === 409) {
        state.error = { title: "Canvas API не подключён", text: "Вставьте токен доступа Canvas в настройках.", retry: false, settings: true };
      }
    } finally {
      state.loading = false;
      render();
    }
  }

  /* ---------- actions ---------- */

  async function refresh() {
    if (state.refreshing) return;
    state.refreshing = true;
    renderTopbar();
    try {
      await api("/api/refresh", { method: "POST" });
      toast("Данные обновлены из Canvas");
      await reload();
    } catch (err) {
      if (err.status === 429) {
        toast(`Обновление уже было только что — ещё через ${err.payload.retry_after || 20} с`);
      } else if (err.status === 409) {
        state.tab = "settings";
        state.tokenForm = true;
        render();
        toast("Сначала подключите Canvas API");
      } else {
        toast(err.message || "Canvas не ответил");
      }
    } finally {
      state.refreshing = false;
      renderTopbar();
    }
  }

  async function setMute(courseId, muted) {
    const course = state.courses.find((c) => c.id === courseId);
    if (course) course.muted = muted;      // optimistic
    render();
    try {
      await api(`/api/courses/${courseId}/mute`, { method: "PUT", body: { muted } });
      await loadState();
    } catch (err) {
      if (course) course.muted = !muted;   // rollback
      toast(err.message || "Не удалось сохранить настройку");
    }
    render();
  }

  async function setLead(hours) {
    const prev = state.settings ? state.settings.lead_hours : 24;
    if (state.settings) state.settings.lead_hours = hours; // optimistic
    render();
    try {
      await api("/api/settings/lead", { method: "PUT", body: { lead_hours: hours } });
      toast(`Напоминание за ${hours} ч`);
    } catch (err) {
      if (state.settings) state.settings.lead_hours = prev;
      toast(err.message || "Не удалось сохранить интервал");
    }
    render();
  }

  async function saveToken(token) {
    try {
      const res = await api("/api/canvas/token", { method: "PUT", body: { token } });
      state.tokenForm = false;
      await loadState();
      await loadAssignments(false);
      if (res.verified) {
        toast(`Canvas подключён: ${res.user || "OK"}`);
      } else {
        toast(res.warning || "Токен сохранён, проверяю в фоне");
      }
      state.tab = "tasks";
      render();
    } catch (err) {
      toast(err.message || "Не удалось сохранить токен");
    }
  }

  async function dropToken() {
    if (!window.confirm("Отключить Canvas? Автоматический опрос и уведомления остановятся.")) return;
    try {
      await api("/api/canvas/token", { method: "DELETE" });
      await loadState();
      toast("Токен Canvas удалён");
    } catch (err) {
      toast(err.message || "Не удалось удалить токен");
    }
    render();
  }

  async function checkCanvas() {
    toast("Проверяю связь…");
    try {
      const res = await api("/api/canvas/check", { method: "POST" });
      toast(res.ok ? `Canvas доступен: ${res.user || "OK"}` : (res.error || "Canvas недоступен"));
    } catch (err) {
      toast(err.message || "Canvas недоступен");
    }
  }

  /* ---------- rendering ---------- */

  function render() {
    renderTopbar();
    renderBanner();
    const el = $("#content");
    if (state.loading) { el.innerHTML = skeleton(); return; }
    if (state.tab === "courses") { el.innerHTML = viewCourses(); return; }
    if (state.tab === "settings") { el.innerHTML = viewSettings(); return; }
    el.innerHTML = viewTasks();
  }

  function renderTopbar() {
    const btn = $("#refresh");
    btn.classList.toggle("is-busy", state.refreshing);
    btn.disabled = state.refreshing || !state.canvas || !state.canvas.connected;

    const chips = [];
    if (state.canvas) {
      if (!state.canvas.connected) {
        chips.push(`<span class="chip chip--bad">Canvas не подключён</span>`);
      } else if (state.canvas.ok) {
        chips.push(`<span class="chip chip--ok">Canvas доступен</span>`);
      } else if (state.canvas.error === "no token") {
        chips.push(`<span class="chip chip--warn">нет токена</span>`);
      } else {
        chips.push(`<span class="chip chip--bad">Canvas недоступен</span>`);
      }
      if (state.canvas.connected && !state.canvas.verified && state.canvas.source === "chat") {
        chips.push(`<span class="chip chip--warn">токен проверяется</span>`);
      }
    }
    if (state.sync && state.sync.last_poll) {
      chips.push(`<span>обновлено ${esc(dtf.format(new Date(state.sync.last_poll)))}</span>`);
    }
    $("#status").innerHTML = chips.join("");
  }

  function renderBanner() {
    const el = $("#banner");
    if (!state.error) { el.hidden = true; el.innerHTML = ""; return; }
    const retry = state.error.retry
      ? `<button class="btn btn--sm" data-action="retry">Повторить</button>` : "";
    const settings = state.error.settings
      ? `<button class="btn btn--sm btn--primary" data-action="open-settings">Подключить Canvas</button>` : "";
    el.className = "banner" + (state.error.settings ? " banner--warn" : "");
    el.innerHTML = `<b>${esc(state.error.title)}</b><span>${esc(state.error.text || "")}</span>
      <div class="banner__actions">${retry}${settings}</div>`;
    el.hidden = false;
  }

  const skeleton = () => `
    <div class="skeleton">
      <div class="list">
        ${Array.from({ length: 6 }).map(() => `
          <div class="row">
            <div class="row__body"><div class="sk sk--name"></div><div class="sk sk--meta"></div></div>
            <div class="row__side"><div class="sk sk--side"></div></div>
          </div>`).join("")}
      </div>
    </div>`;

  function viewTasks() {
    const c = state.canvas || {};
    if (!c.connected) {
      return `<div class="empty">
        <div class="empty__title">Canvas не подключён</div>
        <p class="empty__text">Вставьте токен доступа Canvas — приложение сразу загрузит курсы и задания.</p>
        <button class="btn btn--primary" data-action="open-settings">Подключить Canvas</button>
      </div>`;
    }

    const counts = state.counts || {};
    const filters = [
      ["all", "Все", counts.assignments],
      ["missing", "Не сдано", counts.missing],
      ["overdue", "Просрочено", counts.overdue],
      ["grades", "Оценки", counts.graded],
    ];
    const segment = `<div class="segment" role="tablist">
      ${filters.map(([id, label, n]) => `
        <button type="button" role="tab" data-action="filter" data-filter="${id}"
          aria-pressed="${state.filter === id}">${label}${n ? `<span class="count">${n}</span>` : ""}</button>`).join("")}
    </div>`;

    const chip = state.course
      ? `<div class="filters"><span class="filterchip">${esc(state.course.name)}
           <button type="button" data-action="clear-course" aria-label="Сбросить фильтр курса">✕</button></span></div>`
      : "";

    if (!state.items.length) {
      const empty = {
        all: ["Заданий нет", "Как только в Canvas появится задание, бот пришлёт уведомление."],
        missing: ["Всё сдано", "Незакрытых заданий с дедлайном нет."],
        overdue: ["Просроченного нет", "Заданий с прошедшим дедлайном и без сдачи нет."],
        grades: ["Оценок пока нет", "Оценки появятся здесь сразу после проверки."],
      }[state.filter] || ["Пусто", ""];
      return segment + chip + `<div class="empty"><div class="empty__title">${empty[0]}</div>
        <p class="empty__text">${empty[1]}</p></div>`;
    }

    const rows = state.items.map((a) => {
      const rel = relativeDeadline(a.due_at);
      const cls = relClass(a.status);
      const label = statusLabel(a);
      const graded = a.score !== null && a.score !== undefined;
      const score = graded
        ? `<div class="row__sub"><span class="score">${fmtNum(a.score)}</span>/${fmtNum(a.points)}</div>`
        : (a.points !== null && a.points !== undefined ? `<div class="row__sub">${fmtNum(a.points)} б.</div>` : "");
      return `<button class="row" type="button" data-action="open" data-id="${a.id}">
        <span class="row__body">
          <span class="row__name">${esc(a.name)}</span>
          <span class="row__meta">${esc(a.course || "Без курса")}</span>
          ${graded ? "" : `<span class="row__grade">не оценено</span>`}
        </span>
        <span class="row__side">
          ${label ? `<span class="status status--${cls}">${label}</span>` : ""}
          <div class="row__due row__due--${cls}">${esc(rel || "без дедлайна")}</div>
          ${score}
        </span>
      </button>`;
    }).join("");

    const more = state.page < state.pages
      ? `<div class="actions"><button class="btn btn--block" data-action="more"${state.loadingMore ? " disabled" : ""}>
           ${state.loadingMore ? "Загружаю…" : `Показать ещё (${state.total - state.items.length})`}</button></div>`
      : "";

    return segment + chip + `<div class="list">${rows}</div>${more}`;
  }

  function viewCourses() {
    if (!state.courses.length) {
      return `<div class="empty"><div class="empty__title">Курсов нет</div>
        <p class="empty__text">Активные курсы появятся здесь после синхронизации с Canvas.</p></div>`;
    }
    const rows = state.courses.map((c) => `
      <div class="field">
        <button class="field__label" type="button" data-action="open-course" data-id="${c.id}"
          data-name="${esc(c.name)}" style="text-align:left;flex:1;min-width:0">
          ${esc(c.name)}
          <div class="field__value" style="text-align:left">
            ${c.total} заданий${c.overdue ? ` · ${c.overdue} просрочено` : (c.missing ? ` · ${c.missing} не сдано` : "")}
          </div>
        </button>
        <button class="switch" type="button" role="switch" aria-pressed="${c.muted}"
          aria-label="Уведомления по курсу ${esc(c.name)}"
          data-action="mute" data-id="${c.id}" data-muted="${c.muted}"></button>
      </div>`).join("");
    return `<div class="section">
      <div class="section__title">Уведомления по курсам</div>
      ${rows}
      <div class="section__note">Выключенный курс не присылает уведомления, но остаётся в списках.</div>
    </div>`;
  }

  function viewSettings() {
    const c = state.canvas || {};
    const s = state.settings || { lead_hours: 24 };
    const sync = state.sync || {};

    let canvasRows = `
      <div class="field"><span class="field__label">Инстанс</span><span class="field__value">${esc(shortURL(c.base_url))}</span></div>
      <div class="field"><span class="field__label">Состояние</span>
        <span class="field__value">${esc(canvasStateText(c))}</span></div>`;
    if (c.source === "chat") {
      canvasRows += `<div class="field"><span class="field__label">Токен</span>
        <span class="field__value">${esc(c.masked || "")}${c.verified ? " · подтверждён" : " · ждёт проверки"}</span></div>`;
    }
    if (c.user) {
      canvasRows += `<div class="field"><span class="field__label">Canvas-пользователь</span><span class="field__value">${esc(c.user)}</span></div>`;
    }

    const tokenForm = state.tokenForm ? `
      <div class="field field--stack">
        <label class="field__label" for="token">Токен доступа Canvas</label>
        <input id="token" type="text" inputmode="text" autocomplete="off" autocapitalize="off"
               spellcheck="false" placeholder="Вставьте токен">
      </div>
      <div class="actions">
        <button class="btn btn--primary" data-action="save-token">Сохранить токен</button>
        <button class="btn" data-action="cancel-token">Отмена</button>
      </div>
      <details class="howto"><summary>Где взять токен?</summary>
        <ol>
          <li>Canvas → аватар → <b>Account</b> → <b>Settings</b></li>
          <li><b>Approved Integrations</b> → <b>New Access Token</b></li>
          <li>Назначение, например <code>tg-bot</code> → <b>Generate Token</b></li>
          <li>Скопируйте токен сюда: Canvas показывает его один раз</li>
        </ol>
        <div>Токен хранится на вашем сервере и удаляется командой <code>/disconnect</code> или кнопкой ниже.</div>
      </details>` : `
      <div class="actions">
        <button class="btn btn--primary" data-action="new-token">${c.connected ? "Заменить токен" : "Подключить Canvas"}</button>
        ${c.connected ? `<button class="btn" data-action="check">Проверить связь</button>` : ""}
        ${c.source === "chat" ? `<button class="btn btn--danger" data-action="drop">Отключить</button>` : ""}
      </div>`;

    const lead = [6, 12, 24].map((h) => `
      <button type="button" data-action="lead" data-hours="${h}" aria-pressed="${s.lead_hours === h}">за ${h} ч</button>`).join("");

    let statusNote = "";
    if (c.connected && c.error && c.error !== "no token" && c.error !== "unauthorized") {
      statusNote = `<div class="section__note">⚠️ Последняя ошибка Canvas: ${esc(c.error)}</div>`;
    } else if (c.connected && c.error === "unauthorized") {
      statusNote = `<div class="section__note">⚠️ Canvas отклонил токен (401/403) — замените его.</div>`;
    } else if (c.source === "another chat") {
      statusNote = `<div class="section__note">Данные синхронизируются по токену другого чата: уведомления работают, подключить свой токен не обязательно.</div>`;
    } else if (c.source === ".env") {
      statusNote = `<div class="section__note">Токен задан в окружении сервера (<code>CANVAS_API_TOKEN</code>) — он используется для синхронизации.</div>`;
    }

    return `
      <div class="section">
        <div class="section__title">Canvas API</div>
        ${canvasRows}
        ${tokenForm}
        ${statusNote}
      </div>

      <div class="section">
        <div class="section__title">Напоминания</div>
        <div class="field--stack" style="padding:12px 14px">
          <div class="segment" role="group">${lead}</div>
        </div>
        <div class="section__note">За сколько часов до дедлайна присылать напоминание. За час до срока и после него уведомление приходит всегда.</div>
      </div>

      <div class="section">
        <div class="section__title">Данные</div>
        <div class="field"><span class="field__label">Курсов</span><span class="field__value">${state.counts ? state.counts.courses : "—"}</span></div>
        <div class="field"><span class="field__label">Заданий</span><span class="field__value">${state.counts ? state.counts.assignments : "—"}</span></div>
        <div class="field"><span class="field__label">Последний опрос</span>
          <span class="field__value">${sync.last_poll ? esc(dtf.format(new Date(sync.last_poll))) : "ещё не было"}</span></div>
        <div class="field"><span class="field__label">Уведомлений отправлено</span><span class="field__value">${sync.notifications || 0}</span></div>
        <div class="actions">
          <button class="btn" data-action="refresh-now"${state.refreshing ? " disabled" : ""}>Обновить сейчас</button>
        </div>
      </div>`;
  }

  const shortURL = (url) => String(url || "").replace(/^https?:\/\//, "");
  const canvasStateText = (c) => {
    if (!c.connected) return "не подключён";
    if (c.ok) return "доступен ✅";
    if (c.error === "no token") return "нет токена";
    if (c.error === "unauthorized") return "токен отклонён";
    if (c.error) return "ошибка: " + c.error;
    return "ещё не проверяли";
  };

  /* ---------- sheet ---------- */

  function openSheet(id) {
    const a = state.items.find((x) => x.id === id);
    if (!a) return;
    const lines = [];
    lines.push(`<h2 id="sheet-title">${esc(a.name)}</h2>`);
    lines.push(`<dl style="margin:8px 0 0">`);
    lines.push(`<div class="kv"><dt>Курс</dt><dd>${esc(a.course || "—")}</dd></div>`);
    lines.push(`<div class="kv"><dt>Статус</dt><dd>${esc(statusText(a))}</dd></div>`);
    lines.push(`<div class="kv"><dt>Оценка</dt><dd>${a.score !== null && a.score !== undefined
      ? `${fmtNum(a.score)} / ${fmtNum(a.points)}${a.grade ? " · " + esc(a.grade) : ""}`
      : '<span class="muted">не оценено</span>'}</dd></div>`);
    lines.push(`<div class="kv"><dt>Дедлайн</dt><dd>${a.due_at ? `${esc(dtf.format(new Date(a.due_at)))}<br><span style="color:var(--hint)">${esc(relativeDeadline(a.due_at))}</span>` : "без дедлайна"}</dd></div>`);
    if (a.points !== null && a.points !== undefined) {
      lines.push(`<div class="kv"><dt>Максимум баллов</dt><dd>${fmtNum(a.points)}</dd></div>`);
    }
    lines.push(`</dl>`);
    if (a.url) {
      lines.push(`<div class="actions"><a class="btn btn--primary" href="${esc(a.url)}" target="_blank" rel="noopener">Открыть в Canvas</a></div>`);
    }

    $("#sheet-body").innerHTML = lines.join("");
    $("#sheet").hidden = false;
    if (tg && tg.BackButton) tg.BackButton.show();
  }

  function statusText(a) {
    if (a.submitted) return "сдано";
    if (a.status === "overdue") return "просрочено, не сдано";
    if (a.score !== null && a.score !== undefined) return "есть оценка";
    if (a.status === "no_deadline") return "без дедлайна";
    return "не сдано";
  }

  function closeSheet() {
    $("#sheet").hidden = true;
    if (tg && tg.BackButton) tg.BackButton.hide();
  }

  /* ---------- events ---------- */

  document.addEventListener("click", async (e) => {
    const el = e.target.closest("[data-action]");
    if (!el) return;
    const action = el.dataset.action;
    switch (action) {
      case "filter":
        state.filter = el.dataset.filter;
        state.course = null;
        state.loading = true;
        render();
        try { await loadAssignments(false); } finally { state.loading = false; render(); }
        break;
      case "open-course":
        state.course = { id: Number(el.dataset.id), name: el.dataset.name };
        state.filter = "all";
        state.tab = "tasks";
        setTab("tasks");
        state.loading = true;
        render();
        try { await loadAssignments(false); } finally { state.loading = false; render(); }
        break;
      case "clear-course":
        state.course = null;
        state.loading = true;
        render();
        try { await loadAssignments(false); } finally { state.loading = false; render(); }
        break;
      case "more":
        state.loadingMore = true;
        render();
        try { await loadAssignments(true); } catch (err) { toast(err.message); }
        state.loadingMore = false;
        render();
        break;
      case "open": openSheet(Number(el.dataset.id)); break;
      case "close-sheet": closeSheet(); break;
      case "mute": await setMute(Number(el.dataset.id), el.dataset.muted !== "true"); break;
      case "lead": await setLead(Number(el.dataset.hours)); break;
      case "new-token":
        state.tokenForm = true;
        render();
        setTimeout(() => { const i = $("#token"); if (i) i.focus(); }, 30);
        break;
      case "cancel-token": state.tokenForm = false; render(); break;
      case "save-token": {
        const input = $("#token");
        const value = input ? input.value.trim() : "";
        if (!value) { toast("Вставьте токен"); break; }
        el.disabled = true;
        await saveToken(value);
        break;
      }
      case "drop": await dropToken(); break;
      case "check": await checkCanvas(); break;
      case "refresh-now": await refresh(); break;
      case "retry": await reload(); break;
      case "open-settings": setTab("settings"); await reload(); break;
    }
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && e.target && e.target.id === "token") {
      const btn = document.querySelector('[data-action="save-token"]');
      if (btn) btn.click();
    }
    if (e.key === "Escape") closeSheet();
  });

  function setTab(tab) {
    state.tab = tab;
    document.querySelectorAll(".tab").forEach((b) => {
      b.setAttribute("aria-current", b.dataset.tab === tab ? "page" : "false");
    });
  }

  document.querySelectorAll(".tab").forEach((btn) => {
    btn.addEventListener("click", async () => {
      setTab(btn.dataset.tab);
      state.loading = true;
      state.error = null;
      render();
      try {
        if (state.tab === "courses") await loadCourses();
        else if (state.tab === "tasks") await loadAssignments(false);
        await loadState();
      } catch (err) {
        state.error = { title: "Не удалось загрузить", text: err.message, retry: true };
      } finally {
        state.loading = false;
        render();
      }
    });
  });

  $("#refresh").addEventListener("click", refresh);
  $("#sheet").addEventListener("click", (e) => {
    if (e.target.closest(".sheet__panel") === null) closeSheet();
  });

  /* ---------- init ---------- */

  function applyTheme() {
    // Telegram reports the client's colour scheme; a browser falls back to the
    // system preference. The attribute drives every colour token in styles.css.
    const scheme = tg
      ? (tg.colorScheme || "light")
      : (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
    document.documentElement.dataset.scheme = scheme;
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) {
      meta.setAttribute("content", scheme === "dark" ? "#0e1621" : "#ffffff");
    }
  }

  function initTelegram() {
    if (!tg) return;
    tg.ready();
    if (tg.expand) tg.expand();
    if (tg.disableVerticalSwipes) tg.disableVerticalSwipes();
    // Native chrome: header and bottom bar keep the client's own colours
    // ("bg_color" is a symbolic value the client resolves from its theme).
    if (tg.setHeaderColor) {
      try { tg.setHeaderColor("bg_color"); } catch (_) {}
    }
    if (tg.setBackgroundColor) {
      try { tg.setBackgroundColor("bg_color"); } catch (_) {}
    }
    if (tg.setBottomBarColor) {
      try { tg.setBottomBarColor("bg_color"); } catch (_) {}
    }
    if (tg.onEvent) {
      tg.onEvent("themeChanged", applyTheme);
      tg.onEvent("viewportChanged", () => { /* layout is CSS-driven */ });
    }
    if (tg.BackButton) tg.BackButton.onClick(closeSheet);
  }

  applyTheme();
  initTelegram();
  setTab(state.tab);
  reload();
})();
