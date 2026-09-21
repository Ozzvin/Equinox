"use strict";
(() => {
  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

  // ---------- token ----------
  // The key comes, in this order, from the desktop window (injected before the page loads), from
  // the link that was opened (#token=...), from what this browser remembered. Each step stands
  // alone: a storage or history error must never lose a key that was already found.
  let token = "";
  if (typeof window.__equinoxToken === "string") token = window.__equinoxToken;
  const fromLink = location.hash.match(/token=([0-9a-f]+)/i);
  if (!token && fromLink) token = fromLink[1];
  if (token) { try { localStorage.setItem("token", token); } catch (_) {} }
  else { try { token = localStorage.getItem("token") || ""; } catch (_) {} }
  if (fromLink) { try { history.replaceState(null, "", location.pathname); } catch (_) {} }

  async function api(method, path, body) {
    const opt = { method, headers: { Authorization: "Bearer " + token } };
    if (body instanceof FormData) opt.body = body;
    else if (body !== undefined) { opt.body = JSON.stringify(body); opt.headers["Content-Type"] = "application/json"; }
    const res = await fetch(path, opt);
    if (res.status === 401) { askToken(); throw new Error("Нет доступа"); }
    if (res.status === 204) return null;
    const data = await res.json().catch(() => null);
    if (!res.ok) throw new Error((data && data.error) || res.statusText);
    return data;
  }

  // ---------- formatting ----------
  const fmtHours = (min) => (min % 60 === 0 ? min / 60 : (min / 60).toFixed(1)) + " ч";
  function fmtDuration(sec) {
    const d = Math.floor(sec / 86400), h = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
    return d ? `${d} д ` + `${h} ч` : h ? `${h} ч ${m} мин` : `${m} мин`;
  }
  const units = ["Б", "КБ", "МБ", "ГБ", "ТБ"];
  function bytes(n) {
    if (!n) return "0 Б";
    let i = 0; while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return (n >= 100 || i === 0 ? n.toFixed(0) : n.toFixed(1)) + " " + units[i];
  }
  // Speeds are shown in bytes per second (binary prefixes, as sizes) or, by the setting, in bits per second
  // (decimal prefixes, as networks count them).
  const bitsMode = () => !!(typeof settings !== "undefined" && settings && settings.speedUnit === "bits");
  function bitrate(n) {
    const units = ["бит/с", "Кбит/с", "Мбит/с", "Гбит/с"];
    let v = n * 8, i = 0;
    while (v >= 1000 && i < units.length - 1) { v /= 1000; i++; }
    return (i === 0 || v >= 100 ? v.toFixed(0) : v.toFixed(1)) + " " + units[i];
  }
  const speed = (n) => (n > 0 ? (bitsMode() ? bitrate(n) : bytes(n) + "/с") : "—");
  const speedZero = () => (bitsMode() ? "0 бит/с" : "0 Б/с");
  // Speed limits are stored in KiB/s. They are shown and typed in the unit chosen for speeds: the same КБ/с
  // as in the list, or Кбит/с (1 KiB/s = 8.192 kbit/s). A field that was not touched keeps the stored value
  // as it is, so saving never shifts a limit by rounding.
  const KIB_KBIT = 8.192;
  const limitUnit = (bits) => (bits ? "Кбит/с" : "КБ/с");
  const limitShow = (kib, bits) => (bits ? Math.round(kib * KIB_KBIT) : kib);
  const limitStore = (v, bits) => Math.max(0, bits ? Math.round(v / KIB_KBIT) : Math.floor(v));
  // a limit field remembers the stored value; `dirty` says the person changed what it shows
  function limitFill(id, kib, bits) { const el = $(id); el.dataset.kib = String(kib || 0); delete el.dataset.dirty; el.value = limitShow(kib || 0, bits); }
  function limitRead(id, bits) { const el = $(id); return el.dataset.dirty ? limitStore(Number(el.value) || 0, bits) : Number(el.dataset.kib || 0); }
  // the unit changes while the fields are shown: what was typed becomes the stored value, then it is shown in the new unit
  function limitRefit(ids, oldBits, newBits) { for (const id of ids) limitFill(id, limitRead(id, oldBits), newBits); }
  let limitShownBits = false; // the unit the limit fields of the settings window are shown in
  const limitFieldsDirty = (ids) => { for (const id of ids) $(id).addEventListener("input", () => { $(id).dataset.dirty = "1"; }); };



  // ---------- state ----------
  let torrents = [], settings = null, stats = null, port = null, files = [];
  const sel = new Set();          // hashes of the selected rows
  let anchor = null;              // where a Shift-range starts
  let shownHashes = [];           // hashes of the rows on screen, in display order

  // Actions apply only to selected rows that are visible: a filter must never make an action
  // reach torrents the user cannot see.
  const chosen = () => torrents.filter((t) => sel.has(t.hash) && shownHashes.includes(t.hash));
  const cur = () => { const c = chosen(); return c.length === 1 ? c[0] : undefined; };

  // ---------- sorting ----------
  // ---------- columns of the list ----------
  // What the list shows is the user's choice: right-click the header to switch columns on and off,
  // drag a header to move a column. The choice is remembered by this browser or window.
  const fmtEta = (sec) => {
    if (!isFinite(sec)) return "∞";
    if (sec < 60) return "< 1 мин";
    const d = Math.floor(sec / 86400), h = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
    return d ? `${d} д ${h} ч` : h ? `${h} ч ${m} мин` : `${m} мин`;
  };
  const etaOf = (t) => (t.progress >= 1 || !t.hasMetadata ? Infinity : t.downRate > 0 ? (t.size - t.done) / t.downRate : Infinity);
  const dateOf = (s) => (s && !s.startsWith("0001") ? Date.parse(s) : 0);
  const shortDate = (s) => (dateOf(s) ? new Date(s).toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit", year: "numeric" }) : "—");
  const meta = (t, v) => (t.hasMetadata ? v : "—");
  let queuePos = new Map();
  // green while downloading, blue while seeding, yellow while files are being checked, grey when stopped
  const barState = (t) => ((t.checking || t.checkQueued > 0) ? "check" : t.paused ? "paused" : t.progress >= 1 ? "seed" : "down");
  // The progress bar carries the status text and the percentage inside it. The text is drawn twice,
  // dark over the empty part and light over the filled part, so it reads wherever the fill ends.
  function progressBar(t) {
    const st = stateOf(t), pct = t.progress * 100, kind = t.error ? "bad" : barState(t);
    let text = st.text;
    if (t.progress < 1 && !t.error && !t.checkQueued && !text.includes("%")) text += ` ${pct.toFixed(1)}%`;
    const label = esc(text);
    return `<div class="pbar ${kind}" title="${esc(st.title || text)}"><div class="fill" style="width:${pct.toFixed(1)}%"><span class="lb">${label}</span></div><span class="lb base">${label}</span></div>`;
  }
  const COLS = [
    // Place in the download queue (queue order, whatever the filter or sort). Finished torrents, which are seeding, have none.
    { id: "num", title: "#", menu: "Место в очереди загрузки", tip: "Место в очереди загрузки", always: true, fit: { pad: 12, text: (t) => String(queuePos.get(t.hash) || "") }, cls: "c-num c-qn", w: 48, sort: (t) => queuePos.get(t.hash) || 1e9, cell: (t) => queuePos.get(t.hash) || "" },
    { id: "name", title: "Название", always: true, cls: "c-name", sort: (t) => (t.name || t.hash).toLowerCase(),
      cell: (t) => `<div class="nm" title="${esc(t.name)}">${esc(t.name || t.hash)}${t.sequential ? '<span class="badge">по ходу</span>' : ""}${t.label ? `<span class="badge lbl lc-${labelColor(t.label)}">${esc(t.label)}</span>` : ""}</div>` },
    { id: "size", title: "Размер", fit: { pad: 24, text: (t) => (t.hasMetadata || t.checkQueued > 0 ? bytes(t.size) : "—") }, cls: "c-size", w: 92, sort: (t) => t.size, cell: (t) => (t.hasMetadata || t.checkQueued > 0 ? bytes(t.size) : "—") },
    { id: "downloaded", title: "Скачано", menu: "Скачано (за всё время)", cls: "c-num", w: 92, sort: (t) => t.downloaded, cell: (t) => bytes(t.downloaded) },
    { id: "uploaded", title: "Отдано", menu: "Отдано (за всё время)", cls: "c-num", w: 92, sort: (t) => t.uploaded, cell: (t) => bytes(t.uploaded) },
    { id: "remaining", title: "Осталось", menu: "Осталось скачать", cls: "c-num", w: 92, sort: (t) => t.size - t.done, cell: (t) => meta(t, bytes(Math.max(0, t.size - t.done))) },
    { id: "progress", title: "Прогресс", cls: "c-prog", w: 210, sort: (t) => t.progress, cell: (t) => progressBar(t) },
    { id: "seeds", title: "Сиды", cls: "c-num", w: 70, sort: (t) => t.seeds, cell: (t) => t.seeds },
    { id: "peers", title: "Пиры", cls: "c-num", w: 70, sort: (t) => t.peers, cell: (t) => t.peers },
    { id: "sp", title: "Сиды:Пиры", cls: "c-num", w: 92, sort: (t) => t.seeds * 1e6 + t.peers, cell: (t) => `${t.seeds} : ${t.peers}` },
    { id: "down", title: "Загрузка ↓", menu: "Скорость загрузки", tip: "Скорость загрузки", cls: "c-num", w: 104, sort: (t) => t.downRate, cell: (t) => speed(t.downRate) },
    { id: "up", title: "Отдача ↑", menu: "Скорость отдачи", tip: "Скорость отдачи", cls: "c-num", w: 104, sort: (t) => t.upRate, cell: (t) => speed(t.upRate) },
    { id: "eta", title: "ETA", menu: "ETA — время до завершения", tip: "Время до завершения загрузки", cls: "c-num", w: 100, sort: (t) => etaOf(t), cell: (t) => (t.progress >= 1 || !t.hasMetadata ? "—" : fmtEta(etaOf(t))) },
    { id: "ratio", title: "Рейтинг", cls: "c-num", w: 86, sort: (t) => t.ratio, cell: (t) => t.ratio.toFixed(2), tipOf: (t) => (t.ratioLimit ? "Лимит " + t.ratioLimit : "") },
    { id: "seedtime", title: "Время раздачи", cls: "c-num", w: 120, sort: (t) => t.seedSeconds || 0, cell: (t) => (t.seedSeconds ? fmtDuration(t.seedSeconds) : "—") },
    { id: "added", title: "Добавлена", cls: "c-txt", w: 110, sort: (t) => dateOf(t.added), cell: (t) => shortDate(t.added) },
    { id: "label", title: "Метка", cls: "c-txt", w: 120, sort: (t) => (t.label || "").toLowerCase(), cell: (t) => (t.label ? `<span class="lbl-cell lc-${labelColor(t.label)}"><i class="tagdot"></i>${esc(t.label)}</span>` : "—") },
    { id: "path", title: "Папка", menu: "Папка загрузки", cls: "c-txt", w: 240, sort: (t) => (t.savePath || "").toLowerCase(), cell: (t) => `<span title="${esc(t.savePath || "")}">${esc(t.savePath || "—")}</span>` },
  ];
  const COL = Object.fromEntries(COLS.map((c) => [c.id, c]));
  const DEFAULT_COLS = ["num", "name", "size", "progress", "down", "up", "ratio", "peers"];
  const PINNED = ["num", "name"]; // always the first two, in this order
  let colIds = [...DEFAULT_COLS];
  try {
    const saved = JSON.parse(localStorage.getItem("cols") || "null");
    if (Array.isArray(saved)) colIds = [...new Set(saved)].filter((id) => id in COL);
  } catch (_) {}
  colIds = [...PINNED, ...colIds.filter((id) => !PINNED.includes(id))];
  const saveCols = () => { try { localStorage.setItem("cols", JSON.stringify(colIds)); } catch (_) {} };
  const SORTS = Object.fromEntries(COLS.map((c) => [c.id, c.sort]));
  const sortBy = { key: "", dir: 1 }; // key "" = the queue order the server sends
  try { Object.assign(sortBy, JSON.parse(localStorage.getItem("sort") || "{}")); } catch (_) {}
  if (!(sortBy.key in SORTS)) sortBy.key = "";
  const saveSort = () => { try { localStorage.setItem("sort", JSON.stringify(sortBy)); } catch (_) {} };
  function sorted(list) {
    if (!sortBy.key) return list;
    const f = SORTS[sortBy.key];
    return [...list].sort((a, b) => {
      const x = f(a), y = f(b);
      const c = typeof x === "string" ? x.localeCompare(y) : x - y;
      return (c || (a.name || "").localeCompare(b.name || "")) * sortBy.dir;
    });
  }

  // ---------- filters ----------
  const filter = { q: "", state: "", label: "" };
  try { Object.assign(filter, JSON.parse(localStorage.getItem("filter") || "{}")); } catch (_) {}
  if (filter.state === "done") filter.state = "seeding"; // the "finished" view was merged into "seeding"
  const saveFilter = () => { try { localStorage.setItem("filter", JSON.stringify(filter)); } catch (_) {} };

  function matches(t, over) {
    const f = over ? { q: "", label: "", state: "", ...over } : filter;
    if (f.q && !(t.name || t.hash).toLowerCase().includes(f.q.toLowerCase())) return false;
    if (f.label === "none") { if (t.label) return false; }
    else if (f.label.startsWith("l:") && t.label !== f.label.slice(2)) return false;
    switch (f.state) {
      case "active": return !!t.checking || (!t.paused && (t.active || t.downRate > 0 || t.upRate > 0));
      case "downloading": return !t.paused && t.queued === 0 && !t.checking && !t.checkQueued && (!t.hasMetadata || t.progress < 1);
      case "checking": return !!t.checking || t.checkQueued > 0;
      case "seeding": return !t.paused && t.hasMetadata && t.progress >= 1;
      case "paused": return t.paused;
      case "queued": return t.queued > 0 || t.checkQueued > 0;
    }
    return true;
  }

  const VIEWS = [
    ["", "Все раздачи", "i-all"], ["active", "Активные", "i-bolt"], ["downloading", "Загружаются", "i-download"],
    ["seeding", "Раздаются", "i-up"], ["checking", "Проверяются", "i-check"], ["paused", "На паузе", "i-pause"], ["queued", "В очереди", "i-clock"],
  ];
  const sideItem = (key, name, icon, count, current) =>
    `<button class="side-item" data-view="${esc(key)}" aria-current="${current}">${icon}<span class="name">${name}</span><span class="cnt">${count}</span></button>`;

  function renderSide() {
    const svg = (id) => `<svg class="i"><use href="#${id}"/></svg>`;
    $("side-states").innerHTML = VIEWS.map(([k, n, ic]) =>
      sideItem("s:" + k, n, svg(ic), k ? torrents.filter((t) => matches(t, { state: k })).length : torrents.length,
        !filter.label && filter.state === k)).join("");
    const labels = allLabels();
    $("side-labels-title").hidden = labels.length === 0;
    $("side-labels").innerHTML = labels.length === 0 ? "" :
      labels.map((l) => sideItem("l:" + l, esc(l), `<i class="tagdot lc-${labelColor(l)}"></i>`, torrents.filter((t) => t.label === l).length, filter.label === "l:" + l)).join("") +
      sideItem("none", "Без метки", '<i class="tagdot" style="opacity:.35"></i>', torrents.filter((t) => !t.label).length, filter.label === "none");
  }

  let labelsKey = "";
  // Colours labels can have; none is a state colour. There is no setting for it: a new label gets a random
  // colour that no other label has yet (if the palette is used up, any), and one without a stored colour
  // gets a steady colour from its name.
  const LABEL_COLORS = [["violet", "Фиолетовый"], ["purple", "Пурпурный"], ["pink", "Розовый"], ["cyan", "Бирюзовый"], ["brown", "Коричневый"]];
  const labelColor = (name) => {
    const own = settings && settings.labelColors && settings.labelColors[name];
    if (own && LABEL_COLORS.some(([id]) => id === own)) return own;
    let h = 0; for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
    return LABEL_COLORS[h % LABEL_COLORS.length][0];
  };
  function newLabelColor() {
    const taken = new Set(lbRows.map((r) => r.color));
    const free = LABEL_COLORS.map(([id]) => id).filter((id) => !taken.has(id));
    const from = free.length ? free : LABEL_COLORS.map(([id]) => id);
    return from[Math.floor(Math.random() * from.length)];
  }
  // Labels the user created in the settings count even when no torrent carries them yet.
  const allLabels = () => [...new Set([...torrents.map((t) => t.label), ...Object.keys((settings && settings.labelPaths) || {})].filter(Boolean))].sort((a, b) => a.localeCompare(b));
  function renderLabelFilter() {
    const labels = allLabels();
    const key = labels.join("\u0001");
    if (key === labelsKey) return;
    labelsKey = key;
    const sel = $("f-label");
    // Option values are "" (all), "none" or "l:<label>", so no label text can clash with a keyword.
    sel.innerHTML = `<option value="">Все метки</option><option value="none">Без метки</option>` +
      labels.map((l) => `<option value="l:${esc(l)}">${esc(l)}</option>`).join("");
    if (filter.label.startsWith("l:") && !labels.includes(filter.label.slice(2))) { filter.label = ""; saveFilter(); }
    sel.value = filter.label;
    $("lb-list").innerHTML = labels.map((l) => `<option value="${esc(l)}">`).join("");
  }

  function stateOf(t) {
    if (t.error) return { cls: "bad", title: t.error, text: t.errorKind === "disk_full" ? "Нет места на диске" : t.errorKind === "write" ? "Ошибка записи на диск" : "Ошибка" };
    if (t.checking) return { cls: "wait", text: t.checkProgress > 0 ? `Проверка файлов ${Math.floor(t.checkProgress * 100)}%` : "Проверка файлов…" };
    if (t.moving) return { cls: "wait", text: t.moving > 0 ? `Перемещение ${Math.round(t.moving * 100)}%` : "Перемещение…" };
    if (t.checkQueued > 0) return { cls: "wait", text: "Ждёт проверки", title: `Стоит в очереди на проверку локальных файлов: №${t.checkQueued}` };
    if (!t.hasMetadata) return { cls: "wait", text: "Метаданные…" };
    if (t.paused) return { cls: "", text: t.progress >= 1 ? "Остановлено" : "Пауза" };
    if (t.queued > 0) return { cls: "wait", text: `В очереди №${t.queued}` };
    if (t.progress < 1) return t.downRate > 0 ? { cls: "down", text: "Загрузка" } : { cls: "wait", text: "Ожидание пиров" };
    return { cls: "seed", text: "Раздаётся" };
  }

  // ---------- rendering ----------
  function render() {
    const rows = $("rows");
    renderLabelFilter();
    $("f-state").value = filter.state; $("f-label").value = filter.label;
    renderSide();
    const shown = sorted(torrents.filter((t) => matches(t)));
    shownHashes = shown.map((t) => t.hash);
    for (const th of document.querySelectorAll("th[data-sort]")) th.setAttribute("aria-sort", th.dataset.sort === sortBy.key ? (sortBy.dir > 0 ? "ascending" : "descending") : "none");
    queuePos = new Map(); // place in the download queue: only torrents still to download have one
    { let n = 0; for (const t of torrents) if (t.progress < 1 || !t.hasMetadata) queuePos.set(t.hash, ++n); }
    if (updateFit()) applyWidths();
    const cols = colIds.map((id) => COL[id]);
    rows.innerHTML = shown.map((t) => `<tr data-h="${t.hash}" class="${sel.has(t.hash) ? "sel" : ""}" aria-selected="${sel.has(t.hash)}">` +
      cols.map((c) => `<td class="${c.cls}"${c.tipOf && c.tipOf(t) ? ` title="${esc(c.tipOf(t))}"` : ""}>${c.cell(t)}</td>`).join("") + "</tr>").join("");

    $("empty").hidden = torrents.length > 0;
    $("none").hidden = torrents.length === 0 || shown.length > 0;

    $("s-down").textContent = speed(torrents.reduce((a, t) => a + t.downRate, 0)).replace("—", speedZero());
    $("s-up").textContent = speed(torrents.reduce((a, t) => a + t.upRate, 0)).replace("—", speedZero());
    if (stats) $("s-ratio").textContent = stats.ratio.toFixed(2);
    renderButtons();
    renderTurtle();
    renderPort();
  }

  function renderButtons() {
    const list = chosen(), n = list.length, has = n > 0;
    const busy = list.some((t) => t.moving), noMeta = list.some((t) => !t.hasMetadata);
    for (const id of ["btn-toggle", "btn-remove", "btn-up", "btn-down", "btn-label"]) $(id).disabled = !has || busy;
    $("btn-move").disabled = !has || busy || noMeta;
    $("btn-open").disabled = n !== 1;
    $("btn-seq").disabled = !has || busy || noMeta;
    $("btn-seq").classList.toggle("on", has && list.every((t) => t.sequential));
    if (has) {
      const allPaused = list.every((t) => t.paused), s = n > 1 ? ` (${n})` : "";
      $("btn-toggle").querySelector("use").setAttribute("href", allPaused ? "#i-resume" : "#i-pause");
      $("btn-toggle").title = (allPaused ? "Продолжить" : "Пауза") + s;
    }
  }
  function renderTurtle() {
    const on = !!(stats && stats.altSpeed), b = $("s-turtle");
    b.setAttribute("aria-pressed", on);
    const lim = (v) => (v ? v : "∞");
    b.dataset.tip = (on
      ? `Ограничение скорости включено: ↓${lim(settings && limitShow(settings.altDownLimitKBps, bitsMode()))} ↑${lim(settings && limitShow(settings.altUpLimitKBps, bitsMode()))} ${limitUnit(bitsMode())}\nЛевый клик — выключить`
      : "Ограничение скорости выключено\nЛевый клик — включить") + "\nПравый клик — настроить пределы";
  }

  const PORT_UI = {
    open:     { cls: "ok",   text: (p) => `Порт ${p.port} открыт · есть входящие` },
    mapped:   { cls: "warn", text: (p) => `Порт ${p.port} проброшен · ждём входящих (${p.method})` },
    cgnat:    { cls: "bad",  text: (p) => `Порт ${p.port} недоступен · серый IP у провайдера` },
    closed:   { cls: "bad",  text: (p) => `Порт ${p.port} закрыт · роутер не отвечает` },
    manual:   { cls: "",     text: (p) => `Порт ${p.port} · автопроброс выключен` },
    checking: { cls: "warn", text: (p) => `Порт ${p.port} · проверка…` },
  };

  function renderPort() {
    const el = $("s-port");
    if (!port) return;
    const ui = PORT_UI[port.verdict] || PORT_UI.checking;
    el.className = "pill tip-host " + ui.cls;
    const head = ui.text(port) + (port.wantedPort ? ` · нужный ${port.wantedPort} занят` : "");
    let more = port.advice || "";
    if (port.wantedPort) more = `Порт ${port.wantedPort} занят другой программой или недоступен, поэтому выбран порт ${port.port}. ${port.advice || ""}`.trim();
    el.lastElementChild.textContent = head; // for screen readers; the page shows only the dot
    el.dataset.tip = more ? head + "\n" + more : head;
  }

  function portDetails(p) {
    let t = p.advice || "";
    if (p.mapped) t += ` Метод: ${p.method}, внешний IP ${p.externalIP}.`;
    if (p.remaps) t += ` Проброс пришлось восстанавливать после потери: ${p.remaps}.`;
    return t;
  }
  // The file list is built once per torrent and then updated in place, so an open
  // priority menu is not destroyed by the periodic refresh.
  let filesKey = "";
  const PRIO = [["skip", "Не скачивать"], ["normal", "Обычный"], ["high", "Высокий"]];

  function fileRowHTML(f) {
    const opts = PRIO.map(([v, n]) => `<option value="${v}">${n}</option>`).join("");
    return `<div class="file" data-i="${f.index}">
      <div class="fn" title="${esc(f.path)}">${esc(f.path.split("/").slice(1).join("/") || f.path)}</div>
      <div class="fs">${bytes(f.size)}</div>
      <div class="bar"><i></i></div>
      <select data-prio="${f.index}" aria-label="Приоритет файла">${opts}</select>
      <button class="btn icon" data-play="${f.index}" title="Скопировать ссылку для плеера (VLC, mpv)"><svg class="i"><use href="#i-play"/></svg></button></div>`;
  }

  // ---- several files at once: click selects, Ctrl+click adds or removes, Shift+click takes a range, Ctrl+A takes all
  const fileSel = new Set();
  let fileAnchor = null;
  function renderFileSel() {
    const box = $("files");
    for (const row of box.querySelectorAll(".file")) row.classList.toggle("sel", fileSel.has(Number(row.dataset.i)));
    const n = fileSel.size;
    $("file-sel").hidden = n === 0;
    if (n) {
      const total = files.filter((f) => fileSel.has(f.index)).reduce((a, f) => a + f.size, 0);
      $("fs-info").textContent = `Выбрано ${n} из ${files.length} · ${bytes(total)}`;
    }
    $("f-prio").value = "";
  }
  function fileClick(e, index) {
    if (e.shiftKey && fileAnchor !== null) {
      const a = files.findIndex((f) => f.index === fileAnchor), b = files.findIndex((f) => f.index === index);
      if (!e.ctrlKey && !e.metaKey) fileSel.clear();
      for (let i = Math.min(a, b); i <= Math.max(a, b); i++) fileSel.add(files[i].index);
    } else if (e.ctrlKey || e.metaKey) {
      fileSel.has(index) ? fileSel.delete(index) : fileSel.add(index);
      fileAnchor = index;
    } else {
      fileSel.clear(); fileSel.add(index); fileAnchor = index;
    }
    renderFileSel();
  }
  const filesForAction = () => [...fileSel].sort((x, y) => x - y);

  async function renderFiles() {
    const t = cur();
    if (!t) return;
    if (!t.hasMetadata) { $("files").innerHTML = '<p class="muted" style="padding:6px 16px">Ждём метаданные…</p>'; filesKey = ""; return; }
    try { files = await api("GET", `/api/torrents/${t.hash}/files`) || []; } catch (_) { return; }
    if (!cur() || cur().hash !== t.hash) return;
    const box = $("files"), key = t.hash + ":" + files.length;
    if (key !== filesKey) { box.innerHTML = files.map(fileRowHTML).join(""); filesKey = key; fileSel.clear(); fileAnchor = null; }
    for (const i of [...fileSel]) if (!files.some((f) => f.index === i)) fileSel.delete(i);
    for (const f of files) {
      const row = box.querySelector(`.file[data-i="${f.index}"]`); if (!row) continue;
      row.classList.toggle("skip", f.priority === "skip");
      const bar = row.querySelector(".bar");
      bar.classList.toggle("done", f.progress >= 1);
      bar.firstElementChild.style.width = (f.progress * 100).toFixed(1) + "%";
      const sel = row.querySelector("select");
      if (document.activeElement !== sel) sel.value = f.priority;
    }
    renderFileSel();
  }

  async function setPriority(indexes, priority) {
    const t = cur(); if (!t) return;
    try { await api("POST", `/api/torrents/${t.hash}/files/priority`, { files: indexes, priority }); }
    catch (e) { toast(e.message, true); }
    refresh();
  }
  // ---------- details tabs ----------
  let tab = "files";
  try { tab = localStorage.getItem("tab") || "files"; } catch (_) {}
  const PANES = { files: "files", general: "pane-general", peers: "pane-peers", trackers: "pane-trackers" };
  if (!(tab in PANES)) tab = "files";

  function showTab() {
    for (const b of document.querySelectorAll("#tabs button")) b.setAttribute("aria-selected", b.dataset.tab === tab);
    for (const [k, id] of Object.entries(PANES)) $(id).hidden = k !== tab;
    $("file-actions").hidden = tab !== "files";
    $("d-empty").hidden = true;
  }
  $("tabs").addEventListener("click", (e) => {
    const b = e.target.closest("[data-tab]"); if (!b) return;
    tab = b.dataset.tab; try { localStorage.setItem("tab", tab); } catch (_) {}
    renderDetails();
  });

  const when = (s) => (s && !s.startsWith("0001") ? new Date(s).toLocaleString("ru-RU") : "—");
  const SOURCES = { tracker: "Трекер", dht: "DHT", pex: "PEX", incoming: "Входящий", other: "—" };
  // what each source means, for the tooltips of the "Источник" column
  const SOURCE_TIPS = {
    tracker: "Адрес этого пира сообщил трекер раздачи.",
    dht: "Адрес нашёлся через DHT, распределённую сеть поиска пиров без трекера.",
    pex: "Адрес сообщил другой пир, с которым вы уже связаны (обмен пирами, PEX).",
    incoming: "Пир сам подключился к вам. Значит, ваш порт доступен снаружи.",
    other: "Источник неизвестен (например, пир добавлен вручную).",
  };
  const SOURCES_TIP = Object.keys(SOURCE_TIPS).filter((k) => k !== "other").map((k) => SOURCES[k] + " — " + SOURCE_TIPS[k]).join("\n");

  // ---------- the peers table: columns, order, widths and sorting are the user's, like in the main list ----------
  // The peer's progress is a bar like the torrents' own: blue for a peer that has everything (a seed), green for one that is still downloading.
  function peerBar(x) {
    const pct = x.progress * 100, text = pct >= 100 ? "100%" : pct.toFixed(1) + "%", label = esc(text);
    return `<div class="pbar ${pct >= 100 ? "seed" : "down"}" title="${label}"><div class="fill" style="width:${pct.toFixed(1)}%"><span class="lb">${label}</span></div><span class="lb base">${label}</span></div>`;
  }
  const PCOLS = [
    { id: "addr", title: "Адрес", always: true, w: 240, sort: (x) => x.addr, cell: (x) => `${x.incoming ? "←" : "→"} ${esc(x.addr)}`, tipOf: (x) => (x.incoming ? "входящее подключение" : "исходящее подключение") },
    { id: "client", title: "Клиент", w: 190, sort: (x) => (x.client || "").toLowerCase(), cell: (x) => esc(x.client || "—") },
    { id: "source", title: "Источник", tip: "Откуда взялся пир:\n" + SOURCES_TIP, w: 120, tipOf: (x) => SOURCE_TIPS[x.source] || SOURCE_TIPS.other, sort: (x) => SOURCES[x.source] || "", cell: (x) => SOURCES[x.source] || "—" },
    { id: "dir", title: "Направление", w: 120, sort: (x) => (x.incoming ? 0 : 1), cell: (x) => (x.incoming ? "Входящее" : "Исходящее") },
    { id: "network", title: "Сеть", w: 80, sort: (x) => x.network || "", cell: (x) => esc(x.network || "—") },
    { id: "progress", title: "Прогресс", w: 130, sort: (x) => x.progress, cell: (x) => peerBar(x) },
    { id: "down", title: "Загрузка ↓", menu: "Скорость загрузки", r: true, w: 110, sort: (x) => x.downRate, cell: (x) => speed(x.downRate) },
    { id: "up", title: "Отдача ↑", menu: "Скорость отдачи", r: true, w: 110, sort: (x) => x.upRate, cell: (x) => speed(x.upRate) },
    { id: "downloaded", title: "Скачано", r: true, w: 100, sort: (x) => x.downloaded, cell: (x) => bytes(x.downloaded) },
    { id: "uploaded", title: "Отдано", r: true, w: 100, sort: (x) => x.uploaded, cell: (x) => bytes(x.uploaded) },
  ];
  const PCOL = Object.fromEntries(PCOLS.map((c) => [c.id, c]));
  const P_DEFAULT = ["addr", "client", "source", "progress", "down", "up", "downloaded", "uploaded"];
  let pIds = [...P_DEFAULT], pW = {}, pSort = { key: "", dir: 1 }, lastPeers = [], peersBusy = false, pSuppress = false;
  try { const v = JSON.parse(localStorage.getItem("pcols") || "null"); if (Array.isArray(v)) pIds = [...new Set(v)].filter((id) => id in PCOL); } catch (_) {}
  try { const v = JSON.parse(localStorage.getItem("pcolw") || "{}"); if (v && typeof v === "object") pW = v; } catch (_) {}
  try { Object.assign(pSort, JSON.parse(localStorage.getItem("psort") || "{}")); } catch (_) {}
  pIds = ["addr", ...pIds.filter((id) => id !== "addr")];
  if (!(pSort.key in PCOL)) pSort.key = "";
  const pSave = () => { try { localStorage.setItem("pcols", JSON.stringify(pIds)); localStorage.setItem("pcolw", JSON.stringify(pW)); localStorage.setItem("psort", JSON.stringify(pSort)); } catch (_) {} };
  const pWidth = (c) => pW[c.id] || c.w;

  function drawPeers() {
    const box = $("pane-peers");
    if (lastPeers.length === 0) { box.innerHTML = '<p class="none">Подключённых пиров нет</p>'; return; }
    const cols = pIds.map((id) => PCOL[id]), total = cols.reduce((a, c) => a + pWidth(c), 0);
    let rows = lastPeers;
    if (pSort.key) {
      const f = PCOL[pSort.key].sort;
      rows = [...rows].sort((a, b) => { const x = f(a), y = f(b); return (typeof x === "string" ? x.localeCompare(y) : x - y) * pSort.dir; });
    }
    box.innerHTML = `<table class="mini pt"><colgroup>${cols.map((c) => `<col style="width:${(pWidth(c) / total * 100).toFixed(3)}%">`).join("")}</colgroup>` +
      `<thead><tr>${cols.map((c) => `<th class="${c.r ? "r" : ""}" data-pcol="${c.id}"${c.tip ? ` title="${esc(c.tip)}"` : ""} aria-sort="${pSort.key === c.id ? (pSort.dir > 0 ? "ascending" : "descending") : "none"}">${c.title}<i class="rs" data-prs="${c.id}" title="Потяните, чтобы изменить ширину; двойной клик — по умолчанию"></i></th>`).join("")}</tr></thead><tbody>` +
      rows.map((x) => "<tr>" + cols.map((c) => `<td class="${c.r ? "r" : ""}"${c.tipOf ? ` title="${c.tipOf(x)}"` : ""}>${c.cell(x)}</td>`).join("") + "</tr>").join("") + "</tbody></table>";
  }
  const pApplyWidths = () => {
    const cols = pIds.map((id) => PCOL[id]), total = cols.reduce((a, c) => a + pWidth(c), 0);
    [...$("pane-peers").querySelectorAll("colgroup col")].forEach((el, i) => { if (cols[i]) el.style.width = (pWidth(cols[i]) / total * 100).toFixed(3) + "%"; });
  };
  const pStop = () => { pSuppress = true; setTimeout(() => (pSuppress = false), 0); };
  const peersPane = $("pane-peers");
  peersPane.addEventListener("pointerdown", (e) => {
    peersBusy = true;
    const up = () => { peersBusy = false; document.removeEventListener("pointerup", up); document.removeEventListener("pointercancel", up); };
    document.addEventListener("pointerup", up); document.addEventListener("pointercancel", up);
    if (e.button !== 0) return;
    const h = e.target.closest(".rs"), th = e.target.closest("th[data-pcol]");
    if (h) { // resize: the column and its right neighbour share a fixed sum (the last one takes from the address)
      e.preventDefault();
      const ths = [...peersPane.querySelectorAll("th[data-pcol]")];
      const idx = ths.findIndex((x) => x.dataset.pcol === h.dataset.prs);
      const nb = idx < ths.length - 1 ? idx + 1 : 0;
      if (idx < 0 || nb === idx) return;
      const start = ths.map((x) => x.getBoundingClientRect().width), startX = e.clientX; let moved = false;
      track(e, (ev) => {
        const dx = Math.max(MIN_COL - start[idx], Math.min(ev.clientX - startX, start[nb] - MIN_COL));
        moved = true;
        ths.forEach((x, k) => { pW[x.dataset.pcol] = Math.round(start[k] + (k === idx ? dx : k === nb ? -dx : 0)); });
        pApplyWidths();
      }, () => { if (moved) { pStop(); pSave(); } });
    } else if (th && th.dataset.pcol !== "addr") { // move: drag a header sideways
      const from = th.dataset.pcol, startX = e.clientX; let active = false, target = "", after = false;
      const mark = () => { for (const x of peersPane.querySelectorAll("th")) x.classList.remove("drop-before", "drop-after"); };
      track(e, (ev) => {
        if (!active && Math.abs(ev.clientX - startX) < 6) return;
        active = true; target = "";
        mark();
        for (const x of peersPane.querySelectorAll("th[data-pcol]")) {
          const r = x.getBoundingClientRect();
          if (ev.clientX >= r.left && ev.clientX < r.right) { target = x.dataset.pcol; after = target === "addr" || ev.clientX > r.left + r.width / 2; x.classList.add(after ? "drop-after" : "drop-before"); }
        }
      }, () => {
        mark();
        if (!active) return;
        pStop();
        if (!target || target === from) return;
        const ids = pIds.filter((id) => id !== from);
        ids.splice(ids.indexOf(target) + (after ? 1 : 0), 0, from);
        pIds = ids; pSave(); drawPeers();
      });
    }
  });
  peersPane.addEventListener("dblclick", (e) => { const h = e.target.closest(".rs"); if (h) { delete pW[h.dataset.prs]; pSave(); drawPeers(); } });
  // sorting: ascending, descending, back to the order the server sends (busiest first)
  peersPane.addEventListener("click", (e) => {
    const th = e.target.closest("th[data-pcol]"); if (!th || pSuppress || e.target.closest(".rs")) return;
    const k = th.dataset.pcol;
    if (pSort.key !== k) { pSort.key = k; pSort.dir = 1; } else if (pSort.dir > 0) pSort.dir = -1; else pSort.key = "";
    pSave(); drawPeers();
  });
  // right-click on a header: which columns to show
  function peerColMenu(x, y) {
    const menu = $("ctx");
    menu.innerHTML = PCOLS.map((c) => `<button role="menuitemcheckbox" aria-checked="${pIds.includes(c.id)}" data-pcolmenu="${c.id}" ${c.always ? "disabled" : ""}><span class="ck">${pIds.includes(c.id) ? "✓" : ""}</span>${c.menu || c.title}</button>`).join("") +
      '<hr><button role="menuitem" data-pcolreset="1">Столбцы по умолчанию</button>';
    menu.hidden = false;
    if (x !== undefined) { menu.style.left = Math.min(x, innerWidth - menu.offsetWidth - 8) + "px"; menu.style.top = Math.max(8, Math.min(y, innerHeight - menu.offsetHeight - 8)) + "px"; }
  }
  peersPane.addEventListener("contextmenu", (e) => { if (!e.target.closest("th[data-pcol]")) return; e.preventDefault(); peerColMenu(e.clientX, e.clientY); });
  $("ctx").addEventListener("click", (e) => {
    const b = e.target.closest("[data-pcolmenu],[data-pcolreset]"); if (!b || b.disabled) return;
    e.stopPropagation(); // stays open so several columns can be switched in a row
    if (b.dataset.pcolreset) { pIds = [...P_DEFAULT]; pW = {}; pSort = { key: "", dir: 1 }; }
    else { const id = b.dataset.pcolmenu; pIds = pIds.includes(id) ? pIds.filter((x) => x !== id) : [...pIds, id]; if (pSort.key === id && !pIds.includes(id)) pSort.key = ""; }
    pSave(); drawPeers(); peerColMenu();
  });

  async function renderDetails() {
    const t = cur();
    showTab();
    if (!t) { // the panel is always there; without one chosen torrent it only says so
      const n = chosen().length;
      $("btn-recheck").disabled = true;
      $("file-actions").hidden = true;
      filesKey = "";
      for (const id of ["files", "pane-general", "pane-peers", "tr-list"]) $(id).innerHTML = "";
      for (const id of Object.values(PANES)) $(id).hidden = true; // the address form of the trackers belongs to one torrent
      $("d-empty").textContent = n > 1 ? `Выбрано раздач: ${n}. Подробности показываются, когда выбрана одна.` : "Выберите раздачу, чтобы увидеть подробности.";
      $("d-empty").hidden = false;
      return;
    }
    $("btn-recheck").disabled = !t.hasMetadata || t.checking || !!t.moving;
    if (tab === "files") return renderFiles();
    if (!t.hasMetadata) { $(PANES[tab]).innerHTML = '<p class="none">Ждём метаданные…</p>'; return; }
    const same = () => cur() && cur().hash === t.hash; // the selection may change while a request is out
    if (tab === "general" && $("pane-general").contains(document.activeElement)) return; // do not rebuild under the cursor
    try {
      if (tab === "general") {
        const d = await api("GET", `/api/torrents/${t.hash}/details`); if (!same()) return;
        $("pane-general").innerHTML = `<dl class="kv">
          <dt>Название</dt><dd>${esc(d.name)}</dd>
          <dt>Хэш</dt><dd>${d.hash}</dd>
          <dt>Размер</dt><dd>${bytes(d.totalSize)} · файлов: ${d.files}</dd>
          <dt>Части</dt><dd>${d.piecesDone} из ${d.pieces} · по ${bytes(d.pieceLength)}</dd>
          <dt>Папка</dt><dd>${esc(d.savePath)}</dd>
          <dt>Копия .torrent</dt><dd>${esc(d.copyPath || "не сохранена")}</dd>
          <dt>Добавлена</dt><dd>${when(d.added)}</dd>
          <dt>Создана</dt><dd>${when(d.createdAt)}${d.createdBy ? " · " + esc(d.createdBy) : ""}</dd>
          <dt>Тип</dt><dd>${d.private ? "Приватная" : "Публичная"}</dd>
          ${d.comment ? `<dt>Комментарий</dt><dd>${esc(d.comment)}</dd>` : ""}
          <dt>Лимит подключений</dt><dd><input type="number" id="mc-input" min="0" max="1000" value="${d.maxConns}" style="width:84px;margin:0;height:28px"> <button class="btn small" id="mc-save">Применить</button> <span class="muted small">0 — как в настройках (сейчас ${d.connLimit})</span></dd>
          <dt>Лимит рейтинга</dt><dd><input type="number" id="rl-input" min="0" step="0.1" value="${d.ratioLimit}" style="width:84px;margin:0;height:28px"> <button class="btn small" id="rl-save">Применить</button> <span class="muted small">0 — как в настройках (сейчас ${d.ratioInForce ? d.ratioInForce : "без лимита"}); при достижении раздача останавливается</span></dd>
          <dt>Время раздачи</dt><dd><input type="number" id="sl-input" min="0" step="0.5" value="${d.seedTimeLimit / 60}" style="width:84px;margin:0;height:28px"> ч <button class="btn small" id="sl-save">Применить</button> <span class="muted small">0 — как в настройках (сейчас ${d.seedTimeInForce ? fmtHours(d.seedTimeInForce) : "без лимита"}); уже раздаётся ${fmtDuration(d.seedSeconds)}</span></dd>
          <dt>Magnet-ссылка</dt><dd><button class="btn small" id="copy-magnet">Скопировать</button></dd></dl>`;
        $("rl-save").onclick = async () => {
          try { await post(t, "ratio-limit", { limit: Number($("rl-input").value) || 0 }); toast("Лимит рейтинга сохранён"); $("rl-input").blur(); renderDetails(); }
          catch (x) { toast(x.message, true); }
        };
        $("sl-save").onclick = async () => {
          try { await post(t, "seed-time-limit", { minutes: Math.round((Number($("sl-input").value) || 0) * 60) }); toast("Лимит времени раздачи сохранён"); $("sl-input").blur(); renderDetails(); }
          catch (x) { toast(x.message, true); }
        };
        $("mc-save").onclick = async () => {
          try { await post(t, "max-connections", { limit: Number($("mc-input").value) || 0 }); toast("Лимит подключений сохранён"); $("mc-input").blur(); renderDetails(); }
          catch (x) { toast(x.message, true); }
        };
        $("copy-magnet").onclick = () => navigator.clipboard.writeText(d.magnet).then(() => toast("Magnet-ссылка скопирована"), () => toast("Не удалось скопировать", true));
      } else if (tab === "peers") {
        const p = await api("GET", `/api/torrents/${t.hash}/peers`); if (!same()) return;
        if (peersBusy) return; // a click or a drag is under way: drawing again would lose it
        lastPeers = p; drawPeers();
      } else if (tab === "trackers") {
        const d = await api("GET", `/api/torrents/${t.hash}/details`); if (!same()) return;
        $("tr-list").innerHTML = d.trackers.length === 0 ? '<p class="none">У раздачи нет трекеров: пиры ищутся через DHT</p>' :
          `<table class="mini trk"><colgroup><col><col style="width:84px"><col style="width:250px"></colgroup><thead><tr><th>Адрес</th><th>Уровень</th><th></th></tr></thead><tbody>` +
          d.trackers.map((x) => `<tr><td><div class="tr-url${trOpen.has(x.url) ? " open" : ""}" role="button" tabindex="0" aria-expanded="${trOpen.has(x.url)}" data-url="${esc(x.url)}" title="${trOpen.has(x.url) ? "Нажмите, чтобы свернуть" : "Нажмите, чтобы показать целиком"}">${esc(x.url)}</div></td><td>${x.tier}</td><td><div class="tr-act">${x.added ? '<span class="muted small">добавлен вами</span>' : ""}<button type="button" class="btn small tr-copy" data-url="${esc(x.url)}" title="Скопировать адрес трекера">Копировать</button></div></td></tr>`).join("") + "</tbody></table>";
      }
    } catch (_) { /* transient: retried on the next tick */ }
  }
  // A long announce URL is cut with an ellipsis; a click shows all of it over several lines. What is open
  // is remembered here because the list is drawn again on every refresh.
  const trOpen = new Set();
  function toggleTracker(el) {
    const u = el.dataset.url, open = !trOpen.has(u);
    open ? trOpen.add(u) : trOpen.delete(u);
    el.classList.toggle("open", open); el.setAttribute("aria-expanded", open);
    el.title = open ? "Нажмите, чтобы свернуть" : "Нажмите, чтобы показать целиком";
  }
  $("tr-list").addEventListener("click", async (e) => {
    const b = e.target.closest(".tr-copy"); if (!b) return;
    try { await navigator.clipboard.writeText(b.dataset.url); toast("Адрес трекера скопирован"); }
    catch (_) { toast("Не удалось скопировать", true); }
  });
  $("tr-list").addEventListener("click", (e) => { const el = e.target.closest(".tr-url"); if (el && !getSelection().toString()) toggleTracker(el); });
  $("tr-list").addEventListener("keydown", (e) => { const el = e.target.closest(".tr-url"); if (el && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); toggleTracker(el); } });
  $("tr-form").addEventListener("submit", async (e) => {
    e.preventDefault(); const t = cur(), url = $("tr-input").value.trim(); if (!t || !url) return;
    try { await post(t, "trackers", { url }); $("tr-input").value = ""; toast("Трекер добавлен"); renderDetails(); }
    catch (x) { toast(x.message, true); }
  });
  $("btn-recheck").onclick = async () => {
    const t = cur(); if (!t) return;
    try { await post(t, "recheck"); toast("Проверка файлов запущена"); refresh(); } catch (x) { toast(x.message, true); }
  };
  // ---------- data loop ----------
  async function refresh() {
    try {
      [torrents, stats, port] = await Promise.all([api("GET", "/api/torrents"), api("GET", "/api/stats"), api("GET", "/api/port")]);
      if (!settings) settings = await api("GET", "/api/settings");
      for (const h of [...sel]) if (!torrents.some((t) => t.hash === h)) sel.delete(h); // torrents that are gone
      reportMoves();
      reportErrors();
      recordSample();
      reportCompleted();
      render();
      renderDetails();
      if ($("dlg-stats").open) renderStats();
    } catch (e) { /* transient: retried on the next tick */ }
  }
  function loop() { refresh().finally(() => setTimeout(loop, 1500)); }

  // ---------- helpers ----------
  let toastTimer;
  function toast(msg, bad) {
    const el = $("toast"); el.textContent = msg; el.className = "show" + (bad ? " bad" : "");
    clearTimeout(toastTimer); toastTimer = setTimeout(() => (el.className = ""), 3500);
  }
  const act = (p) => p.then(refresh).catch((e) => toast(e.message, true));
  const streamPath = (h, i) => `/api/torrents/${h}/files/${i}/stream?token=${token}`;

  function askToken() { const d = $("dlg-token"); if (!d.open) d.showModal(); }

  // ---------- events ----------
  function select(h, e) {
    const ctrl = e && (e.ctrlKey || e.metaKey);
    if (e && e.shiftKey && anchor && shownHashes.includes(anchor)) {
      const a = shownHashes.indexOf(anchor), b = shownHashes.indexOf(h);
      if (!ctrl) sel.clear();
      for (let i = Math.min(a, b); i <= Math.max(a, b); i++) sel.add(shownHashes[i]);
    } else if (ctrl) {
      if (!sel.delete(h)) sel.add(h);
      anchor = h;
    } else { sel.clear(); sel.add(h); anchor = h; }
    files = []; filesKey = ""; $("files").innerHTML = "";
    for (const id of ["pane-general", "pane-peers", "tr-list"]) $(id).innerHTML = "";
    render(); renderDetails();
  }
  $("rows").addEventListener("mousedown", (e) => { if (e.shiftKey) e.preventDefault(); }); // no text selection while ranging
  $("rows").addEventListener("click", (e) => { const tr = e.target.closest("tr"); if (tr) select(tr.dataset.h, e); });

  // Run one request per selected torrent; report failures together.
  async function each(list, fn, okMsg) {
    const errs = [];
    for (const t of list) { try { await fn(t); } catch (x) { errs.push(`${t.name || t.hash.slice(0, 8)}: ${x.message}`); } }
    await refresh();
    if (errs.length) toast(`Не удалось для ${errs.length} из ${list.length}: ${errs[0]}`, true); else if (okMsg) toast(okMsg);
  }
  const post = (t, path, body) => api("POST", `/api/torrents/${t.hash}/${path}`, body);

  // The list always fits the window: the widths below are only proportions, and the table takes the whole
  // width of the window, so the columns grow and shrink with it (a narrow window cuts long texts with an
  // ellipsis instead of adding a scroll bar). The user's widths (px at the time they were set) are kept the
  // same way, by column id.
  let colW = {};
  try { const w = JSON.parse(localStorage.getItem("colw") || "{}"); if (w && typeof w === "object") colW = w; } catch (_) {}
  const saveColW = () => { try { localStorage.setItem("colw", JSON.stringify(colW)); } catch (_) {} };
  const MIN_COL = 44;
  const widthOf = (c) => colW[c.id] || c.w || (c.id === "name" ? 340 : 100);
  let suppressClick = false; // a drag or a resize ends with a click that must not sort

  // Columns marked `fit` ("#" and "Размер") are exactly as wide as their widest text needs, header and sort
  // arrow included, in pixels: never cut, never wider than necessary. The other columns share what is left in
  // proportion to their widths.
  const measureCtx = document.createElement("canvas").getContext("2d");
  const textWidth = (text, font) => { measureCtx.font = font; return measureCtx.measureText(text).width; };
  let fitPx = {}, fitKey = "";
  function updateFit() {
    const cs = getComputedStyle(document.body), fam = cs.fontFamily, out = {};
    const fs = parseFloat(cs.fontSize) || 14, th = document.querySelector("thead th"), hfs = (th && parseFloat(getComputedStyle(th).fontSize)) || 12;
    for (const id of colIds) {
      const c = COL[id]; if (!c.fit) continue;
      // the header, with room for its sort arrow only while the list is sorted by this column
      let w = textWidth(c.title, `600 ${hfs}px ${fam}`) + (sortBy.key === id ? 14 : 0);
      for (const t of torrents) w = Math.max(w, textWidth(c.fit.text(t), `${fs}px ${fam}`));
      out[id] = Math.ceil(w) + c.fit.pad + 2;
    }
    const key = JSON.stringify(out);
    if (key === fitKey) return false;
    fitPx = out; fitKey = key;
    return true;
  }
  function applyWidths() {
    const cols = colIds.map((id) => COL[id]);
    const width = $("tbl").getBoundingClientRect().width || document.querySelector(".list").clientWidth || 1000;
    // every column is given in percent of the table (the browser does not resolve calc() with percent for
    // columns); a fitted column's percent follows from its pixels, so it keeps its size when the window changes
    const fixedPx = cols.reduce((a, c) => a + (c.fit ? fitPx[c.id] || 0 : 0), 0);
    const rest = Math.max(0, 100 - fixedPx / width * 100);
    const total = cols.reduce((a, c) => a + (c.fit ? 0 : widthOf(c)), 0) || 1;
    const cs = $("cols").children;
    cols.forEach((c, i) => {
      if (cs[i]) cs[i].style.width = (c.fit ? (fitPx[c.id] || 0) / width * 100 : widthOf(c) / total * rest).toFixed(4) + "%";
    });
  }
  try { new ResizeObserver(() => applyWidths()).observe(document.querySelector(".list")); } catch (_) { addEventListener("resize", applyWidths); }
  // header row and column widths follow the chosen columns
  function renderHead() {
    const cols = colIds.map((id) => COL[id]);
    $("head-row").innerHTML = cols.map((c) => `<th class="${c.cls}" data-sort="${c.id}" data-col="${c.id}"${c.tip ? ` title="${c.tip}"` : ""}>${c.title}${c.fit ? "" : `<i class="rs" data-rs="${c.id}" title="Потяните, чтобы изменить ширину; двойной клик — по умолчанию"></i>`}</th>`).join("");
    $("cols").innerHTML = cols.map(() => "<col>").join("");
    applyWidths();
  }
  function setCols(ids) { colIds = ids; saveCols(); renderHead(); render(); }

  // Pointer-driven so it behaves the same in the browser and in the desktop window.
  function track(e, move, done) {
    const onMove = (ev) => move(ev), onUp = (ev) => { document.removeEventListener("pointermove", onMove); document.removeEventListener("pointerup", onUp); document.removeEventListener("pointercancel", onUp); document.body.classList.remove("dragging"); done(ev); };
    document.addEventListener("pointermove", onMove); document.addEventListener("pointerup", onUp); document.addEventListener("pointercancel", onUp);
    document.body.classList.add("dragging");
  }

  // resize: drag the right edge of a header. The column takes from (or gives to) its right neighbour, so the
  // total stays the width of the window; for the last column the neighbour is the name. Double click puts
  // the default width back.
  $("head-row").addEventListener("pointerdown", (e) => {
    const h = e.target.closest(".rs"); if (!h || e.button !== 0) return;
    e.preventDefault();
    const ths = [...$("head-row").querySelectorAll("th[data-col]")];
    const idx = ths.findIndex((t) => t.dataset.col === h.dataset.rs);
    let nb = -1; // the neighbour on the right that is not fitted (the last column: the name)
    for (let k = idx + 1; k < ths.length; k++) if (!COL[ths[k].dataset.col].fit) { nb = k; break; }
    if (nb < 0) nb = ths.findIndex((t) => t.dataset.col === "name");
    if (idx < 0 || nb < 0 || nb === idx) return;
    const start = ths.map((t) => t.getBoundingClientRect().width), startX = e.clientX;
    let moved = false;
    track(e, (ev) => {
      const dx = Math.max(MIN_COL - start[idx], Math.min(ev.clientX - startX, start[nb] - MIN_COL));
      moved = true;
      ths.forEach((t, k) => { if (!COL[t.dataset.col].fit) colW[t.dataset.col] = Math.round(start[k] + (k === idx ? dx : k === nb ? -dx : 0)); });
      applyWidths();
    }, () => { if (moved) { suppressClick = true; setTimeout(() => (suppressClick = false), 0); saveColW(); } });
  });
  $("head-row").addEventListener("dblclick", (e) => {
    const h = e.target.closest(".rs"); if (!h) return;
    delete colW[h.dataset.rs]; saveColW(); applyWidths();
  });

  // move: drag a header sideways; a line shows where the column will land
  $("head-row").addEventListener("pointerdown", (e) => {
    const th = e.target.closest("th[data-col]"); if (!th || e.target.closest(".rs") || e.button !== 0) return;
    const from = th.dataset.col; if (PINNED.includes(from)) return; // the number and the name stay first
    const startX = e.clientX; let active = false, target = null, after = false;
    const clear = () => { for (const t of document.querySelectorAll("#head-row th")) t.classList.remove("drop-before", "drop-after", "drag-src"); };
    track(e, (ev) => {
      if (!active && Math.abs(ev.clientX - startX) < 6) return;
      active = true; th.classList.add("drag-src");
      target = null;
      for (const t of document.querySelectorAll("#head-row th[data-col]")) {
        const r = t.getBoundingClientRect();
        if (ev.clientX >= r.left && ev.clientX < r.right) { target = t; after = PINNED.includes(t.dataset.col) || ev.clientX > r.left + r.width / 2; }
      }
      for (const t of document.querySelectorAll("#head-row th")) t.classList.remove("drop-before", "drop-after");
      if (target && target !== th) target.classList.add(after ? "drop-after" : "drop-before");
    }, () => {
      clear();
      if (!active) return;
      suppressClick = true; setTimeout(() => (suppressClick = false), 0);
      if (!target || target === th) return;
      const to = PINNED.includes(target.dataset.col) ? "name" : target.dataset.col, ids = colIds.filter((id) => id !== from);
      ids.splice(ids.indexOf(to) + (after ? 1 : 0), 0, from);
      setCols(ids);
    });
  });
  renderHead();

  const colCtx = $("ctx");
  // ---------- density: large, standard (the default), compact (no left panel, like Transmission) ----------
  const DENSITIES = [["large", "Увеличенный", "крупные строки и отступы"], ["standard", "Стандартный", "по умолчанию"], ["compact", "Компактный", "без левой панели, прогресс линией"]];
  let density = document.documentElement.dataset.density || "standard";
  function setDensity(d) {
    if (!DENSITIES.some((x) => x[0] === d)) d = "standard";
    density = d;
    if (d === "standard") delete document.documentElement.dataset.density; else document.documentElement.dataset.density = d;
    try { localStorage.setItem("uiDensity", d); } catch (_) {}
    if (typeof window.windowDensity === "function") window.windowDensity(d); // the app window takes the size of this density
    render(); // columns sized by their text are measured again in the new font
    dispatchEvent(new Event("resize"));
  }
  if (typeof window.windowDensity === "function") window.windowDensity(density); // tell the app window which density the page starts in
  function densityMenu() {
    const menu = $("ctx"), b = $("btn-density").getBoundingClientRect();
    menu.innerHTML = `<div class="ctx-head">Плотность интерфейса</div>` + DENSITIES.map(([k, name, hint]) =>
      `<button role="menuitemradio" aria-checked="${density === k}" data-density="${k}"><span class="ck">${density === k ? "✓" : ""}</span>${name}<span class="muted small" style="margin-left:auto;padding-left:14px">${hint}</span></button>`).join("") +
      (typeof window.resetWindowSize === "function" ? `<hr><button role="menuitem" data-resetwin="1">Сбросить размер окна</button>` : "");
    menu.hidden = false;
    menu.style.left = Math.max(8, Math.min(b.right - menu.offsetWidth, innerWidth - menu.offsetWidth - 8)) + "px";
    menu.style.top = (b.bottom + 6) + "px";
  }
  $("btn-density").onclick = (e) => { e.stopPropagation(); const m = $("ctx"); if (!m.hidden && m.querySelector("[data-density]")) m.hidden = true; else densityMenu(); };
  $("ctx").addEventListener("click", (e) => {
    const b = e.target.closest("[data-density]"); if (b) setDensity(b.dataset.density);
    else if (e.target.closest("[data-resetwin]")) window.resetWindowSize();
  }); // the click then closes the menu

  // right-click on the header: which columns to show
  function colMenu(x, y) {
    const items = COLS.map((c) => `<button role="menuitemcheckbox" aria-checked="${colIds.includes(c.id)}" data-col="${c.id}" ${c.always ? "disabled" : ""}><span class="ck">${colIds.includes(c.id) ? "✓" : ""}</span>${c.menu || c.title}</button>`);
    colCtx.innerHTML = items.join("") + '<hr><button role="menuitem" data-colreset="1">Столбцы по умолчанию</button>';
    colCtx.hidden = false;
    if (x !== undefined) { colCtx.style.left = Math.min(x, innerWidth - colCtx.offsetWidth - 8) + "px"; colCtx.style.top = Math.max(8, Math.min(y, innerHeight - colCtx.offsetHeight - 8)) + "px"; }
  }
  $("head-row").addEventListener("contextmenu", (e) => { e.preventDefault(); colMenu(e.clientX, e.clientY); });
  colCtx.addEventListener("click", (e) => {
    const b = e.target.closest("[data-col],[data-colreset]"); if (!b || b.disabled) return;
    e.stopPropagation(); // the menu stays open so several columns can be switched in a row
    if (b.dataset.colreset) { setCols([...DEFAULT_COLS]); return colMenu(); }
    const id = b.dataset.col;
    setCols(colIds.includes(id) ? colIds.filter((x) => x !== id) : [...colIds, id]);
    colMenu();
  });

  // ---------- height of the details panel ----------
  // Dragged by the bar above it (or with the arrow keys once it has focus), remembered.
  const detailsEl = $("details"), MIN_DETAILS = 120;
  function setDetailsH(px, save) {
    if (px == null) { detailsEl.style.height = ""; if (save) try { localStorage.removeItem("detailsH"); } catch (_) {} return; }
    const layoutH = document.querySelector(".layout").getBoundingClientRect().height + detailsEl.getBoundingClientRect().height;
    const h = Math.round(Math.min(Math.max(px, MIN_DETAILS), Math.max(MIN_DETAILS, layoutH - 260)));
    detailsEl.style.height = h + "px";
    if (save) try { localStorage.setItem("detailsH", String(h)); } catch (_) {}
  }
  try { const v = Number(localStorage.getItem("detailsH")); if (v > 0) setDetailsH(v, false); } catch (_) {}
  $("splitter").addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return; e.preventDefault();
    const startY = e.clientY, startH = detailsEl.getBoundingClientRect().height;
    track(e, (ev) => setDetailsH(startH + startY - ev.clientY, false), () => setDetailsH(detailsEl.getBoundingClientRect().height, true));
  });
  $("splitter").addEventListener("dblclick", () => setDetailsH(null, true));
  $("splitter").addEventListener("keydown", (e) => {
    const step = e.shiftKey ? 80 : 20, cur = detailsEl.getBoundingClientRect().height;
    if (e.key === "ArrowUp") { e.preventDefault(); setDetailsH(cur + step, true); }
    else if (e.key === "ArrowDown") { e.preventDefault(); setDetailsH(cur - step, true); }
  });
  addEventListener("resize", () => { if (detailsEl.style.height) setDetailsH(detailsEl.getBoundingClientRect().height, false); });

  // ---------- width of the left panel ----------
  const sideEl = $("side"), DEFAULT_SIDE = 224, MIN_SIDE = 150;
  function setSideW(px, save) {
    if (px == null) { sideEl.style.width = ""; if (save) try { localStorage.removeItem("sideW"); } catch (_) {} return; }
    const w = Math.round(Math.min(Math.max(px, MIN_SIDE), Math.max(MIN_SIDE, innerWidth * 0.5)));
    sideEl.style.width = w + "px";
    if (save) try { localStorage.setItem("sideW", String(w)); } catch (_) {}
  }
  try { const v = Number(localStorage.getItem("sideW")); if (v > 0) setSideW(v, false); } catch (_) {}
  $("side-split").addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return; e.preventDefault();
    const startX = e.clientX, startW = sideEl.getBoundingClientRect().width;
    track(e, (ev) => setSideW(startW + ev.clientX - startX, false), () => setSideW(sideEl.getBoundingClientRect().width, true));
  });
  $("side-split").addEventListener("dblclick", () => setSideW(null, true));
  $("side-split").addEventListener("keydown", (e) => {
    const step = e.shiftKey ? 80 : 20, cur = sideEl.getBoundingClientRect().width;
    if (e.key === "ArrowLeft") { e.preventDefault(); setSideW(cur - step, true); }
    else if (e.key === "ArrowRight") { e.preventDefault(); setSideW(cur + step, true); }
  });
  addEventListener("resize", () => { if (sideEl.style.width) setSideW(sideEl.getBoundingClientRect().width, false); });

  // sorting by clicking a column header: ascending, descending, back to queue order
  $("tbl").tHead.addEventListener("click", (e) => {
    if (suppressClick || e.target.closest(".rs")) return;
    const th = e.target.closest("th[data-sort]"); if (!th) return;
    const k = th.dataset.sort;
    if (sortBy.key !== k) { sortBy.key = k; sortBy.dir = 1; } else if (sortBy.dir > 0) sortBy.dir = -1; else sortBy.key = "";
    saveSort(); render();
  });

  $("btn-toggle").onclick = () => {
    const list = chosen(); if (!list.length) return;
    const resume = list.every((t) => t.paused);
    each(list.filter((t) => t.paused === resume), (t) => post(t, resume ? "resume" : "pause"));
  };
  // Queue moves keep the relative order of the selection: going up, the first goes first.
  $("btn-up").onclick = () => each(chosen(), (t) => post(t, "queue", { move: "up" }));
  $("btn-down").onclick = () => each(chosen().reverse(), (t) => post(t, "queue", { move: "down" }));
  $("btn-seq").onclick = () => {
    const list = chosen(); if (!list.length) return;
    const on = !list.every((t) => t.sequential);
    each(list, (t) => post(t, "sequential", { enabled: on }));
  };

  // Copy a stream link for an external player (VLC, mpv). Sequential mode is switched on
  // so the pieces are fetched in playback order.
  async function copyLink(index) {
    const t = cur(); if (!t) return;
    if (!t.sequential) await api("POST", `/api/torrents/${t.hash}/sequential`, { enabled: true }).catch(() => {});
    try {
      await navigator.clipboard.writeText(location.origin + streamPath(t.hash, index));
      toast("Ссылка скопирована — вставьте её в VLC или mpv (Медиа → Открыть URL)");
    } catch (_) { toast("Не удалось скопировать ссылку", true); }
    refresh();
  }
  $("files").addEventListener("change", (e) => {
    const sel = e.target.closest("[data-prio]");
    if (sel) { const i = Number(sel.dataset.prio); setPriority(fileSel.has(i) && fileSel.size > 1 ? filesForAction() : [i], sel.value); }
  });
  $("files").addEventListener("click", (e) => {
    if (e.target.closest("select, button, option")) return;
    const row = e.target.closest(".file"); if (row) fileClick(e, Number(row.dataset.i));
  });
  $("f-prio").addEventListener("change", () => {
    const v = $("f-prio").value; if (!v || !fileSel.size) return;
    setPriority(filesForAction(), v); $("f-prio").value = "";
  });
  // a click on the empty place of the files tab (between or below the rows) clears the selection, like in a list of torrents
  document.querySelector(".details-body").addEventListener("click", (e) => {
    if (tab !== "files" || !fileSel.size || e.target.closest(".file, select, button, option")) return;
    fileSel.clear(); fileAnchor = null; renderFileSel();
  });
  $("files").addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "a") { e.preventDefault(); e.stopPropagation(); files.forEach((f) => fileSel.add(f.index)); renderFileSel(); }
    else if (e.key === "Escape" && fileSel.size) { e.stopPropagation(); fileSel.clear(); fileAnchor = null; renderFileSel(); }
    else if (e.key === "Delete" && fileSel.size) { e.preventDefault(); e.stopPropagation(); askDeleteFiles(); }
  });
  // right click on a file: the same priorities for the selection
  $("files").addEventListener("contextmenu", (e) => {
    const row = e.target.closest(".file"); if (!row) return;
    e.preventDefault();
    const i = Number(row.dataset.i);
    if (!fileSel.has(i)) { fileSel.clear(); fileSel.add(i); fileAnchor = i; renderFileSel(); }
    const menu = $("ctx"), n = fileSel.size;
    menu.innerHTML = `<div class="ctx-head">${n > 1 ? `Выбрано файлов: ${n}` : "Файл"}</div>` +
      PRIO.map(([v, name]) => `<button role="menuitem" data-fprio="${v}">${name}</button>`).join("") +
      `<hr><button role="menuitem" data-fopen="${i}"${n > 1 ? " disabled" : ""}>Показать в Проводнике</button>` +
      `<button role="menuitem" class="danger" data-fdel="1">Удалить с диска…</button>`;
    menu.hidden = false;
    menu.style.left = Math.min(e.clientX, innerWidth - menu.offsetWidth - 8) + "px";
    menu.style.top = Math.max(8, Math.min(e.clientY, innerHeight - menu.offsetHeight - 8)) + "px";
    menu.querySelector("button")?.focus();
  });
  // show one file in the file manager; delete the selected files from the disk after a confirmation
  $("ctx").addEventListener("click", async (e) => {
    const open = e.target.closest("[data-fopen]"), del = e.target.closest("[data-fdel]");
    if (!open && !del) return;
    e.stopPropagation(); $("ctx").hidden = true;
    if (open && !open.disabled) {
      const t = cur(); if (!t) return;
      try { await api("POST", `/api/torrents/${t.hash}/files/${open.dataset.fopen}/open`); } catch (x) { toast(x.message, true); }
    } else if (del) askDeleteFiles();
  });
  function askDeleteFiles() {
    if (!fileSel.size) return;
    const chosen = files.filter((f) => fileSel.has(f.index));
    const onDisk = chosen.reduce((a, f) => a + f.size * f.progress, 0);
    $("filedel-text").textContent = `Будет удалено файлов: ${chosen.length}` + (onDisk > 0 ? ` (на диске около ${bytes(onDisk)})` : " (на диске их ещё нет)") +
      (chosen.length === 1 ? `: ${chosen[0].path.split("/").pop()}` : ".");
    $("dlg-filedel").showModal();
  }
  $("f-del").onclick = askDeleteFiles;
  $("f-filedel").addEventListener("submit", async () => {
    const t = cur(); if (!t || !fileSel.size) return;
    const idx = filesForAction();
    try {
      const r = await api("POST", `/api/torrents/${t.hash}/files/delete`, { files: idx });
      toast(r && r.deleted ? `Удалено файлов: ${r.deleted}` : "Файлы отмечены «Не скачивать»");
    } catch (x) { toast(x.message, true); }
    fileSel.clear(); fileAnchor = null; renderFileSel(); refresh();
  });
  $("ctx").addEventListener("click", (e) => {
    const b = e.target.closest("[data-fprio]"); if (!b) return;

    e.stopPropagation(); $("ctx").hidden = true;
    setPriority(filesForAction(), b.dataset.fprio);
  });
  $("f-all").onclick = () => setPriority(files.map((f) => f.index), "normal");
  $("f-none").onclick = () => setPriority(files.map((f) => f.index), "skip");
  $("files").addEventListener("click", (e) => {
    const b = e.target.closest("[data-play]"); if (b) copyLink(Number(b.dataset.play));
  });
  // sidebar: one view at a time (a state or a label)
  $("side").addEventListener("click", (e) => {
    const b = e.target.closest("[data-view]"); if (!b) return;
    const v = b.dataset.view;
    if (v.startsWith("s:")) { filter.state = v.slice(2); filter.label = ""; } else { filter.label = v; filter.state = ""; }
    saveFilter(); render();
  });

  // move to another folder
  $("btn-move").onclick = () => {
    const list = chosen(); if (!list.length) return;
    const same = list.every((t) => t.savePath === list[0].savePath);
    $("mv-name").textContent = list.length === 1 ? `${list[0].name} — сейчас в: ${list[0].savePath}` : `Выбрано раздач: ${list.length}`;
    $("mv-input").value = same ? list[0].savePath || "" : "";
    $("mv-err").hidden = true; $("dlg-move").showModal(); $("mv-input").select();
  };
  $("f-move").addEventListener("submit", async (e) => {
    e.preventDefault(); const list = chosen(); const path = $("mv-input").value.trim(); if (!list.length || !path) return;
    // The first request shows a wrong folder in the dialog; later ones report through the toast.
    try { await post(list[0], "move", { path }); } catch (x) { $("mv-err").textContent = x.message; $("mv-err").hidden = false; return; }
    $("dlg-move").close(); toast("Перемещение начато");
    await each(list.slice(1), (t) => post(t, "move", { path }));
  });
  // Speed history for the chart: one point per refresh while the page is open.
  const history = [];
  const HISTORY_MAX = 240;
  function recordSample() {
    history.push({ t: Date.now(), down: torrents.reduce((a, t) => a + t.downRate, 0), up: torrents.reduce((a, t) => a + t.upRate, 0) });
    if (history.length > HISTORY_MAX) history.shift();
  }

  // ---------- push-style notifications ----------
  // A card in the corner of the window for events worth noticing (a check or a download finished). It goes
  // away by itself after a few seconds (not while the pointer is on it), by its cross, or with a click, which
  // also selects the torrent it is about.
  const PUSH_LIFE = 7000, PUSH_MAX = 4;
  const PUSH_ICON = {
    check: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l7 3v5c0 5-3 8.5-7 10-4-1.5-7-5-7-10V6z"/><path d="M8.5 12l2.5 2.5 4.5-5"/></svg>',
    done: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>',
  };
  function push({ kind = "done", title, text, hash }) {
    const box = $("push");
    const card = document.createElement("div");
    card.className = "push " + kind; card.setAttribute("role", "status");
    card.style.setProperty("--life", PUSH_LIFE + "ms");
    card.innerHTML = `<span class="pi">${PUSH_ICON[kind] || PUSH_ICON.done}</span><span class="pt">${esc(title)}</span>` +
      `<button type="button" class="pc" aria-label="Закрыть">×</button><span class="px" title="${esc(text)}">${esc(text)}</span><i class="pbar"></i>`;
    let timer = 0, left = PUSH_LIFE, started = Date.now();
    const close = () => { clearTimeout(timer); if (card.classList.contains("out")) return; card.classList.add("out"); setTimeout(() => card.remove(), 230); };
    const arm = () => { started = Date.now(); timer = setTimeout(close, left); };
    card.addEventListener("pointerenter", () => { clearTimeout(timer); left -= Date.now() - started; });
    card.addEventListener("pointerleave", () => { if (left > 0) arm(); });
    card.querySelector(".pc").addEventListener("click", (e) => { e.stopPropagation(); close(); });
    card.addEventListener("click", () => { if (hash && torrents.some((t) => t.hash === hash)) { select(hash, null); render(); renderDetails(); } close(); });
    box.appendChild(card);
    while (box.querySelectorAll(".push:not(.out)").length > PUSH_MAX) box.querySelector(".push:not(.out)").remove(); // the oldest goes first
    arm();
  }

  // Push notifications tell about a change of a torrent's status, once, and about nothing else:
  //  - "Проверка завершена": a real check of local files (most of the pieces waited for it) is over;
  //  - "Загрузка завершена": a torrent that was downloading is complete.
  // A torrent that only waited (for its turn to be checked, or for its info) and then turned out to be
  // complete says nothing, and neither do the short blips of the engine while seeding. Several at once
  // (a start of the application brings many checks to an end) make one card, not a heap of them.
  let phaseSeen = null;
  const checkTrack = {}, pushedAt = {};
  const phaseOf = (t) => (t.checkQueued > 0 || !t.hasMetadata ? "waiting" : t.checking ? "checking" : t.progress >= 1 ? "complete" : "incomplete");
  function reportCompleted() {
    const now = {}, events = [];
    for (const t of torrents) {
      const ph = phaseOf(t), before = phaseSeen && phaseSeen[t.hash], prev = checkTrack[t.hash];
      now[t.hash] = ph;
      if (ph === "checking") {
        const seen = typeof t.checkProgress === "number" ? t.checkProgress : 1;
        checkTrack[t.hash] = { min: prev ? Math.min(prev.min, seen) : seen };
      } else if (prev && ph !== "waiting") { // the check is over (a torrent sent back to wait has not finished it)
        delete checkTrack[t.hash];
        if (prev.min < 0.95 && phaseSeen && !t.moving) events.push({ kind: "check", t });
        continue;
      }
      if (phaseSeen && !t.moving && before === "incomplete" && ph === "complete") events.push({ kind: "done", t });
    }
    phaseSeen = now;
    flushEvents(events);
  }
  function flushEvents(events) {
    const at = Date.now();
    for (const kind of ["check", "done"]) {
      const list = events.filter((e) => e.kind === kind && !(pushedAt[e.t.hash + kind] > at - 30000)); // the same torrent, the same news: once
      for (const e of list) pushedAt[e.t.hash + kind] = at;
      const title = kind === "check" ? "Проверка завершена" : "Загрузка завершена";
      if (list.length >= 3) {
        push({ kind, title, text: `${list.length} раздач: ${list.slice(0, 2).map((e) => e.t.name).join(", ")} и ещё ${list.length - 2}` });
      } else {
        for (const { t } of list) push({ kind, hash: t.hash, title, text: t.name + (kind === "check" && t.progress < 1 ? " — найдено " + Math.floor(t.progress * 100) + "%" : "") });
      }
    }
  }

  // ---------- statistics ----------
  function renderStats() {
    if (stats) {
      $("k-down").textContent = bytes(stats.downloaded); $("k-up").textContent = bytes(stats.uploaded);
      $("k-ratio").textContent = stats.ratio.toFixed(2);
    }
    $("k-count").textContent = torrents.length;
    const last = history[history.length - 1] || { down: 0, up: 0 };
    $("c-down").textContent = speed(last.down).replace("—", speedZero()); $("c-up").textContent = speed(last.up).replace("—", speedZero());

    const W = 640, H = 170, padL = 6, padR = 6, padT = 12, padB = 18;
    const svg = $("chart");
    if (history.length < 2) { svg.innerHTML = ""; $("c-scale").textContent = ""; $("c-span").textContent = "Собираю данные…"; return; }
    const t0 = history[0].t, t1 = history[history.length - 1].t;
    let max = Math.max(1024, ...history.map((p) => Math.max(p.down, p.up)));
    // Round the top of the scale to a friendly number (1, 2, 5 x 10^n).
    const mag = Math.pow(10, Math.floor(Math.log10(max))); max = [1, 2, 5, 10].map((m) => m * mag).find((v) => v >= max);
    const x = (t) => padL + ((t - t0) / Math.max(1, t1 - t0)) * (W - padL - padR);
    const y = (v) => H - padB - (v / max) * (H - padT - padB);
    const line = (key) => history.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)},${y(p[key]).toFixed(1)}`).join(" ");
    const area = (key) => `${line(key)} L${x(t1).toFixed(1)},${H - padB} L${x(t0).toFixed(1)},${H - padB} Z`;
    const grid = [0, 0.5, 1].map((f) => `<line class="grid" x1="${padL}" x2="${W - padR}" y1="${y(max * f)}" y2="${y(max * f)}"/>`).join("");
    $("c-scale").textContent = `шкала до ${speed(max)}`;
    svg.innerHTML = grid +
      `<path class="area-down" d="${area("down")}"/><path class="area-up" d="${area("up")}"/>` +
      `<path class="line l-down" d="${line("down")}"/><path class="line l-up" d="${line("up")}"/>`;
    const secs = Math.round((t1 - t0) / 1000);
    $("c-span").textContent = secs >= 60 ? `последние ${Math.floor(secs / 60)} мин ${secs % 60} с` : `последние ${secs} с`;

    const top = [...torrents].filter((t) => t.uploaded > 0).sort((a, b) => b.uploaded - a.uploaded).slice(0, 5);
    $("k-top-wrap").hidden = top.length === 0;
    $("k-top").innerHTML = top.map((t) => `<div class="toprow"><span title="${esc(t.name)}">${esc(t.name)}</span><span>${bytes(t.uploaded)}</span><span>${t.ratio.toFixed(2)}</span></div>`).join("");
  }
  $("btn-stats").onclick = () => { renderStats(); $("dlg-stats").showModal(); };

  const shownErrors = {};
  function reportErrors() {
    for (const t of torrents) {
      const key = t.error || "";
      if (key && shownErrors[t.hash] !== key) toast(`«${t.name}»: ${t.errorKind === "disk_full" ? "не хватает места на диске. Освободите место и нажмите «Продолжить»" : t.errorKind === "write" ? "не удалось записать данные на диск, раздача остановлена" : t.error}`, true);
      shownErrors[t.hash] = key;
    }
  }
  const shownMoveMsg = {};
  function reportMoves() {
    for (const t of torrents) {
      const msg = t.moveError ? `Не удалось переместить «${t.name}»: ${t.moveError}` : t.moveNote ? `«${t.name}»: ${t.moveNote}` : "";
      if (msg && shownMoveMsg[t.hash] !== msg) toast(msg, !!t.moveError);
      shownMoveMsg[t.hash] = msg;
    }
  }

  // keyboard: works when no dialog is open and no field has the focus
  document.addEventListener("keydown", (e) => {
    if (document.querySelector("dialog[open]")) return;
    const tag = (e.target.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea" || tag === "select") { if (e.key === "Escape") e.target.blur(); return; }
    const mod = e.ctrlKey || e.metaKey;
    if (mod && e.key.toLowerCase() === "a") { e.preventDefault(); shownHashes.forEach((h) => sel.add(h)); render(); }
    else if (mod && e.shiftKey && e.key.toLowerCase() === "d") { e.preventDefault(); setDensity(DENSITIES[(DENSITIES.findIndex((x) => x[0] === density) + 1) % DENSITIES.length][0]); }
    else if ((mod && e.key.toLowerCase() === "f") || e.key === "/") { e.preventDefault(); openSearch(true); }
    else if (e.key === "Escape") { sel.clear(); anchor = null; render(); }
    else if (e.key === "Delete") { if (chosen().length) { e.preventDefault(); $("btn-remove").click(); } }
    else if (e.key === " ") { if (chosen().length) { e.preventDefault(); $("btn-toggle").click(); } }
    else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      if (!shownHashes.length) return;
      e.preventDefault();
      const at = shownHashes.indexOf(anchor), step = e.key === "ArrowDown" ? 1 : -1;
      const next = shownHashes[Math.min(shownHashes.length - 1, Math.max(0, at < 0 ? (step > 0 ? 0 : shownHashes.length - 1) : at + step))];
      select(next, e.shiftKey ? { shiftKey: true } : null);
      document.querySelector(`#rows tr[data-h="${next}"]`)?.scrollIntoView({ block: "nearest" });
    }
  });

  // create a .torrent from local data
  let creating = false;
  $("btn-create").onclick = () => {
    for (const id of ["cr-src", "cr-out", "cr-trackers", "cr-comment"]) $(id).value = "";
    $("cr-piece").value = "0"; $("cr-seed").checked = true; $("cr-private").checked = false;
    $("cr-err").hidden = true; $("cr-status").hidden = true; $("cr-go").disabled = false; creating = false;
    $("dlg-create").showModal(); $("cr-src").focus();
  };
  $("dlg-create").addEventListener("cancel", (e) => { if (creating) e.preventDefault(); }); // do not close while hashing
  $("f-create").addEventListener("submit", async (e) => {
    e.preventDefault(); if (creating) return;
    const body = {
      source: $("cr-src").value.trim(), output: $("cr-out").value.trim(), comment: $("cr-comment").value.trim(),
      trackers: $("cr-trackers").value.split("\n").map((s) => s.trim()).filter(Boolean),
      pieceLength: Number($("cr-piece").value) || 0, seed: $("cr-seed").checked, private: $("cr-private").checked,
    };
    $("cr-err").hidden = true;
    let job;
    try { job = await api("POST", "/api/create", body); } catch (x) { $("cr-err").textContent = x.message; $("cr-err").hidden = false; return; }
    creating = true; $("cr-go").disabled = true; $("cr-status").hidden = false;
    const t0 = Date.now();
    while (job.running) {
      $("cr-status").textContent = `Создание… ${Math.round((Date.now() - t0) / 1000)} с (считаются хэши ${bytes(job.size)} данных, для больших файлов это может занять несколько минут)`;
      await new Promise((r) => setTimeout(r, 1000));
      try { job = await api("GET", `/api/create/${job.id}`); } catch (x) { job.running = false; job.error = x.message; }
    }
    creating = false; $("cr-go").disabled = false;
    if (job.error) { $("cr-status").hidden = true; $("cr-err").textContent = job.error; $("cr-err").hidden = false; refresh(); return; }
    $("dlg-create").close();
    toast(`Готово: ${job.output}` + (job.seeding ? " — раздача добавлена в список" : ""));
    refresh();
  });

  $("btn-open").onclick = async () => { const t = cur(); if (t) { try { await post(t, "open-folder"); } catch (x) { toast(x.message, true); } } };

  // right-click menu on rows: the same actions as the toolbar, for the current selection
  const ctx = $("ctx");
  const hideCtx = () => { ctx.hidden = true; };
  $("rows").addEventListener("contextmenu", (e) => {
    const tr = e.target.closest("tr"); if (!tr) return;
    e.preventDefault();
    if (!sel.has(tr.dataset.h)) select(tr.dataset.h, null);
    const list = chosen(), one = list.length === 1, busy = list.some((t) => t.moving);
    const allPaused = list.every((t) => t.paused);
    const items = [
      [allPaused ? "Продолжить" : "Приостановить", "btn-toggle", busy],
      [one ? "Показать в Проводнике" : undefined, "btn-open", false],
      [null],
      ["Метка…", "btn-label", busy], ["Переместить в другую папку…", "btn-move", busy || list.some((t) => !t.hasMetadata)],
      [one ? "Проверить файлы" : undefined, "btn-recheck", busy || !list[0]?.hasMetadata],
      ["Выше в очереди", "btn-up", busy], ["Ниже в очереди", "btn-down", busy],
      [null],
      ["Удалить…", "btn-remove", busy, true],
    ];
    ctx.innerHTML = items.map(([label, id, disabled, danger]) => label === null ? "<hr>" : label === undefined ? "" :
      `<button role="menuitem" data-act="${id}" ${disabled ? "disabled" : ""} class="${danger ? "danger" : ""}">${label}</button>`).join("").replace(/(<hr>){2,}/g, "<hr>");
    ctx.hidden = false;
    ctx.style.left = Math.min(e.clientX, innerWidth - ctx.offsetWidth - 8) + "px";
    ctx.style.top = Math.min(e.clientY, innerHeight - ctx.offsetHeight - 8) + "px";
    ctx.querySelector("button:not(:disabled)")?.focus();
  });
  ctx.addEventListener("click", (e) => { const b = e.target.closest("[data-act]"); if (!b || b.disabled) return; hideCtx(); $(b.dataset.act).click(); });
  document.addEventListener("click", (e) => { if (!ctx.contains(e.target)) hideCtx(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") hideCtx(); }, true);
  addEventListener("blur", hideCtx); addEventListener("resize", hideCtx);
  $("rows").addEventListener("scroll", hideCtx); document.querySelector(".list").addEventListener("scroll", hideCtx);

  // filters
  $("f-q").value = filter.q; $("f-state").value = filter.state;
  $("f-q").addEventListener("input", (e) => { filter.q = e.target.value; saveFilter(); render(); renderSearchButton(); });
  // The field is open while it has a query or the focus; a click on the magnifier opens it, or closes it when it is empty.
  function openSearch(open, focus = open) {
    const box = $("search"), input = $("f-q");
    box.classList.toggle("open", open);
    input.tabIndex = open ? 0 : -1;
    $("btn-search").setAttribute("aria-expanded", open);
    if (open && focus) { input.focus(); input.select(); }
  }
  function renderSearchButton() { $("btn-search").classList.toggle("on", !!filter.q); }
  $("btn-search").onclick = () => openSearch(!($("search").classList.contains("open") && !$("f-q").value));
  $("f-q").addEventListener("blur", () => { if (!$("f-q").value) setTimeout(() => { if (document.activeElement !== $("f-q") && document.activeElement !== $("btn-search")) openSearch(false); }, 120); });
  $("f-q").addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    e.stopPropagation();
    if ($("f-q").value) { $("f-q").value = ""; filter.q = ""; saveFilter(); render(); renderSearchButton(); } else { openSearch(false); $("btn-search").focus(); }
  });
  if (filter.q) openSearch(true, false); // a query left from the last time keeps the field open
  renderSearchButton();

  $("f-state").addEventListener("change", (e) => { filter.state = e.target.value; saveFilter(); render(); });
  $("f-label").addEventListener("change", (e) => { filter.label = e.target.value; saveFilter(); render(); });

  // label
  $("btn-label").onclick = () => {
    const list = chosen(); if (!list.length) return;
    $("lb-name").textContent = list.length === 1 ? list[0].name : `Выбрано раздач: ${list.length}`;
    $("lb-input").value = list.every((t) => t.label === list[0].label) ? list[0].label || "" : "";
    $("dlg-label").showModal(); $("lb-input").select();
  };
  $("f-label-form").addEventListener("submit", (e) => {
    e.preventDefault(); const list = chosen(); if (!list.length) return;
    const label = $("lb-input").value.trim();
    $("dlg-label").close();
    each(list, (t) => post(t, "label", { label }));
  });

  // remove
  let removing = [];
  $("btn-remove").onclick = () => {
    removing = chosen(); if (!removing.length) return;
    const names = removing.slice(0, 3).map((t) => t.name || t.hash.slice(0, 8)).join(", ");
    $("rm-title").textContent = removing.length === 1 ? "Удалить раздачу?" : `Удалить раздачи (${removing.length})?`;
    $("rm-name").textContent = removing.length > 3 ? `${names} и ещё ${removing.length - 3}` : names;
    $("rm-data").checked = false; $("dlg-remove").showModal();
  };
  $("f-remove").addEventListener("submit", async (e) => {
    e.preventDefault();
    const list = removing, withData = $("rm-data").checked;
    $("dlg-remove").close();
    await each(list, (t) => api("DELETE", `/api/torrents/${t.hash}${withData ? "?data=1" : ""}`),
      withData ? "Удалено вместе с файлами и копиями .torrent" : "Удалено из списка (файлы остались)");
    for (const t of list) if (!torrents.some((x) => x.hash === t.hash)) sel.delete(t.hash);
    render();
  });

  // ---------- "Add torrents" dialog ----------
  // A list of torrents (files, magnet links, infohashes), each with its own options and, for
  // .torrent files, a choice of files. "Apply to all" copies one entry's options to the rest.
  let addInfo = null;        // what GET /api/add-dialog returned
  let adItems = [];          // the entries on the list
  let adSel = -1;            // index of the selected entry
  let adTab = "files";
  let adBusy = false;

  const optDefaults = () => {
    const d = addInfo.defaults;
    return { savePath: "", moveDoneOn: !!d.moveDoneEnabled, moveDone: d.moveDone || "", label: "", paused: !!d.paused, sequential: !!d.sequential,
      edgePieces: !!d.edgePieces, skipCheck: !!d.skipCheck, preallocate: !!addInfo.preallocate, maxConns: 0 };
  };
  const adCur = () => adItems[adSel];

  async function openAdd(files) {
    try { addInfo = await api("GET", "/api/add-dialog"); } catch (x) { return toast(x.message, true); }
    if (!$("dlg-add").open) {
      adItems = []; adSel = -1; adTab = "files"; adBusy = false; $("ad-err").hidden = true;
      renderAdd(); $("dlg-add").showModal();
    }
    if (files && files.length) await stageFiles(files);
  }

  async function stageFiles(list) {
    for (const f of list) {
      const fd = new FormData(); fd.append("file", f);
      try {
        const st = await api("POST", "/api/stage", fd);
        adItems.push({ kind: "file", name: st.name, size: st.size, hash: st.hash, exists: st.exists, stage: st.id, files: st.files || [],
          sel: (st.files || []).map(() => true), open: null, opts: optDefaults(), error: "" });
        adSel = adItems.length - 1;
      } catch (x) { toast(`${f.name}: ${x.message}`, true); }
    }
    renderAdd();
  }

  // Parses the lines first and adds nothing if one of them is not understood, so the text can
  // be corrected and sent again without duplicates; entries already on the list are skipped.
  function addLinks(text) {
    const found = [], bad = [];
    for (const raw of text.split(/\r?\n/).map((l) => l.trim()).filter(Boolean)) {
      if (/^magnet:/i.test(raw)) {
        const q = new URLSearchParams(raw.slice(raw.indexOf("?") + 1));
        const hash = (q.get("xt") || "").replace(/^urn:btih:/i, "").toLowerCase();
        found.push({ kind: "magnet", name: q.get("dn") || (hash ? "magnet: " + hash.slice(0, 12) + "…" : "magnet"), size: 0, hash, exists: false, magnet: raw });
      } else if (/^[0-9a-f]{40}$/i.test(raw) || /^[a-z2-7]{32}$/i.test(raw)) {
        found.push({ kind: "hash", name: raw, size: 0, hash: raw.toLowerCase(), exists: false, infohash: raw });
      } else bad.push(raw.length > 60 ? raw.slice(0, 60) + "…" : raw);
    }
    if (bad.length) return bad;
    for (const e of found) {
      if (e.hash && adItems.some((x) => x.hash === e.hash)) continue;
      adItems.push({ ...e, opts: optDefaults(), error: "" });
      adSel = adItems.length - 1;
    }
    return bad;
  }
  // -- list
  function renderAdd() {
    const n = adItems.length;
    $("ad-title").textContent = `Добавить раздачи (${n})`;
    $("ad-list").innerHTML = n === 0 ? '<div class="none">Список пуст. Добавьте .torrent файлы, magnet-ссылки или infohash.</div>' :
      adItems.map((e, i) => `<div class="ad-row" role="option" data-i="${i}" aria-selected="${i === adSel}">
        <span class="nm" title="${esc(e.name)}">${esc(e.name)}</span>
        ${e.kind === "magnet" ? '<span class="badge">magnet</span>' : e.kind === "hash" ? '<span class="badge">infohash</span>' : ""}
        ${e.exists ? '<span class="badge warn">уже добавлена</span>' : ""}
        ${e.error ? `<span class="badge bad" title="${esc(e.error)}">ошибка</span>` : ""}
        <span class="sz">${e.size ? bytes(e.size) : ""}</span></div>`).join("");
    $("ad-remove").disabled = adSel < 0 || adBusy;
    $("ad-go").disabled = n === 0 || adBusy;
    $("ad-go").textContent = n > 1 ? `Добавить (${n})` : "Добавить";
    for (const b of document.querySelectorAll("#ad-tabs button")) b.setAttribute("aria-selected", b.dataset.tab === adTab);
    $("ad-files").hidden = adTab !== "files"; $("ad-opts").hidden = adTab !== "opts";
    renderAdFiles(); fillOpts();
    const err = adCur() && adCur().error;
    $("ad-err").hidden = !err; if (err) $("ad-err").textContent = `${adCur().name}: ${err}`;
  }

  // -- files tab: a tree with check boxes
  function fileTree(e) {
    const root = { name: e.name, path: "", dirs: new Map(), files: [] };
    e.files.forEach((f, i) => {
      const parts = (f.path || e.name).split("/");
      let node = root;
      for (const p of parts.slice(0, -1)) {
        if (!node.dirs.has(p)) node.dirs.set(p, { name: p, path: node.path + p + "/", dirs: new Map(), files: [] });
        node = node.dirs.get(p);
      }
      node.files.push({ i, name: parts[parts.length - 1], size: f.size });
    });
    return root;
  }
  const treeIdx = (n) => [...n.files.map((f) => f.i), ...[...n.dirs.values()].flatMap(treeIdx)];

  function renderAdFiles() {
    const box = $("ad-files"), e = adCur();
    if (!e) { box.innerHTML = '<p class="note">Выберите раздачу из списка.</p>'; return; }
    if (e.kind !== "file") {
      box.innerHTML = '<p class="note">Список файлов появится только после получения метаданных. Чтобы выбрать файлы до начала загрузки, отметьте на вкладке «Параметры» «Добавить в паузе» и выберите файлы в панели подробностей.</p>';
      return;
    }
    const root = fileTree(e);
    if (e.open === null) { // expand everything unless the tree is huge
      e.open = new Set([""]);
      if (e.files.length <= 300) { const walk = (n) => { e.open.add(n.path); n.dirs.forEach(walk); }; walk(root); }
    }
    const rows = [];
    const draw = (n, depth) => {
      const idx = treeIdx(n), on = idx.filter((i) => e.sel[i]).length, total = idx.reduce((a, i) => a + e.files[i].size, 0);
      const state = on === 0 ? "none" : on === idx.length ? "all" : "some";
      const isRoot = n.path === "", single = isRoot && e.files.length === 1;
      if (!isRoot) { // the top level itself gets no row: its folder is the torrent's own

        const open = e.open.has(n.path);
        rows.push(`<div class="tree-row" style="padding-left:${depth * 18}px"><button type="button" class="fold" data-fold="${esc(n.path)}" aria-label="Свернуть или развернуть">${open ? "▾" : "▸"}</button>
          <input type="checkbox" data-dir="${esc(n.path)}" data-state="${state}" ${state === "all" ? "checked" : ""} aria-label="${esc(n.name)}"><span class="nm" title="${esc(n.name)}">${esc(n.name)}</span><span class="sz">${bytes(total)}</span></div>`);
        if (!open) return;
      }
      for (const d of n.dirs.values()) draw(d, isRoot ? 0 : depth + 1);
      for (const f of n.files) rows.push(`<div class="tree-row" style="padding-left:${single ? 0 : (isRoot ? 0 : depth + 1) * 18 + 18}px">
        <input type="checkbox" data-file="${f.i}" ${e.sel[f.i] ? "checked" : ""} aria-label="${esc(f.name)}"><span class="nm" title="${esc(f.name)}">${esc(f.name)}</span><span class="sz">${bytes(f.size)}</span></div>`);
    };
    draw(root, 0);
    const picked = e.sel.filter(Boolean).length, pickedSize = e.files.reduce((a, f, i) => a + (e.sel[i] ? f.size : 0), 0);
    box.innerHTML = `<div class="tree-tools"><button type="button" class="btn small" id="tr-all">Все</button><button type="button" class="btn small" id="tr-none">Ничего</button>
      <span class="muted small">Выбрано ${picked} из ${e.files.length} · ${bytes(pickedSize)}</span></div><div class="tree">${rows.join("")}</div>`;
    box.querySelectorAll("input[data-state=some]").forEach((c) => (c.indeterminate = true));
  }

  // -- options tab
  const AO = { save: "ao-save", moveOn: "ao-movedone-on", move: "ao-movedone", label: "ao-label", paused: "ao-paused", edges: "ao-edges", seq: "ao-seq", skip: "ao-skip", prealloc: "ao-prealloc", conns: "ao-conns" };
  function fillOpts() {
    const e = adCur(), on = !!e;
    for (const id of Object.values(AO)) $(id).disabled = !on;
    for (const b of ["ao-reset", "ao-all", "ao-save-defaults"]) $(b).disabled = !on;
    if (!e) return;
    const o = e.opts;
    $(AO.save).value = o.savePath; $(AO.moveOn).checked = o.moveDoneOn; $(AO.move).value = o.moveDone; $(AO.move).disabled = !o.moveDoneOn;
    document.querySelector('[data-pick="ao-movedone"]').disabled = !o.moveDoneOn;
    $(AO.label).value = o.label; $(AO.paused).checked = o.paused; $(AO.edges).checked = o.edgePieces; $(AO.seq).checked = o.sequential;
    $(AO.skip).checked = o.skipCheck; $(AO.prealloc).checked = o.preallocate; $(AO.conns).value = o.maxConns;
    const lf = addInfo && addInfo.labels && addInfo.labels[o.label.trim()];
    $(AO.save).placeholder = lf ? `папка метки: ${lf}` : `по умолчанию: ${addInfo ? addInfo.dataDir : ""}`;
    $(AO.move).placeholder = addInfo && addInfo.moveCompletedDir ? `общая настройка: ${addInfo.moveCompletedDir}` : "папка для завершённых";
  }
  function readOpts() {
    const e = adCur(); if (!e) return;
    const o = e.opts;
    o.savePath = $(AO.save).value; o.moveDoneOn = $(AO.moveOn).checked; o.moveDone = $(AO.move).value; o.label = $(AO.label).value;
    o.paused = $(AO.paused).checked; o.edgePieces = $(AO.edges).checked; o.sequential = $(AO.seq).checked;
    o.skipCheck = $(AO.skip).checked; o.preallocate = $(AO.prealloc).checked; o.maxConns = Number($(AO.conns).value) || 0;
    $(AO.move).disabled = !o.moveDoneOn; document.querySelector('[data-pick="ao-movedone"]').disabled = !o.moveDoneOn;
    const lf = addInfo && addInfo.labels && addInfo.labels[o.label.trim()];
    $(AO.save).placeholder = lf ? `папка метки: ${lf}` : `по умолчанию: ${addInfo ? addInfo.dataDir : ""}`;
  }

  // -- events
  $("btn-add").onclick = () => openAdd();
  $("ad-list").addEventListener("click", (e) => { const r = e.target.closest("[data-i]"); if (r) { adSel = Number(r.dataset.i); renderAdd(); } });
  $("ad-tabs").addEventListener("click", (e) => { const b = e.target.closest("[data-tab]"); if (b) { adTab = b.dataset.tab; renderAdd(); } });
  $("ad-file").onclick = () => $("ad-fileinput").click();
  $("ad-fileinput").addEventListener("change", async (e) => { const fl = [...e.target.files]; e.target.value = ""; if (fl.length) await stageFiles(fl); });
  $("ad-remove").onclick = () => {
    const e = adCur(); if (!e) return;
    if (e.stage) api("DELETE", `/api/stage/${e.stage}`).catch(() => {});
    adItems.splice(adSel, 1); adSel = Math.min(adSel, adItems.length - 1); renderAdd();
  };
  $("ad-links").onclick = () => { $("lk-text").value = ""; $("lk-err").hidden = true; $("dlg-links").showModal(); $("lk-text").focus(); };
  $("lk-ok").onclick = () => {
    const bad = addLinks($("lk-text").value);
    if (bad.length) { $("lk-err").textContent = `Не похоже на magnet-ссылку или infohash: ${bad.join("; ")}`; $("lk-err").hidden = false; renderAdd(); return; }
    $("dlg-links").close(); renderAdd();
  };
  $("ad-opts").addEventListener("input", readOpts);
  $("ad-opts").addEventListener("change", () => { readOpts(); });
  $("ao-reset").onclick = () => { const e = adCur(); if (e) { e.opts = optDefaults(); fillOpts(); } };
  $("ao-all").onclick = () => {
    const e = adCur(); if (!e) return;
    adItems.forEach((x) => { x.opts = { ...e.opts }; });
    toast(`Параметры применены ко всем раздачам в списке (${adItems.length})`);
  };
  $("ao-save-defaults").onclick = async () => {
    const o = adCur() && adCur().opts; if (!o) return;
    try {
      addInfo = await api("PUT", "/api/add-defaults", { paused: o.paused, sequential: o.sequential, edgePieces: o.edgePieces, skipCheck: o.skipCheck, moveDoneEnabled: o.moveDoneOn, moveDone: o.moveDone.trim() });
      toast("Сохранено: эти значения будут предлагаться при следующих добавлениях");
    } catch (x) { toast(x.message, true); }
  };
  $("ad-files").addEventListener("click", (ev) => {
    const e = adCur(); if (!e) return;
    const fold = ev.target.closest("[data-fold]");
    if (fold) { const p = fold.dataset.fold; e.open.has(p) ? e.open.delete(p) : e.open.add(p); renderAdFiles(); return; }
    if (ev.target.id === "tr-all" || ev.target.id === "tr-none") { e.sel.fill(ev.target.id === "tr-all"); renderAdFiles(); }
  });
  $("ad-files").addEventListener("change", (ev) => {
    const e = adCur(); if (!e) return;
    const f = ev.target.closest("[data-file]"), d = ev.target.closest("[data-dir]");
    if (f) e.sel[Number(f.dataset.file)] = f.checked;
    else if (d) { // a folder switches everything below it
      const idx = treeIdx((function find(n) { if (n.path === d.dataset.dir) return n; for (const c of n.dirs.values()) { const r = find(c); if (r) return r; } return null; })(fileTree(e)) || { files: [], dirs: new Map() });
      idx.forEach((i) => (e.sel[i] = d.checked));
    }
    renderAdFiles();
  });
  $("dlg-add").addEventListener("cancel", (ev) => { if (adBusy) ev.preventDefault(); });
  $("dlg-add").addEventListener("close", () => { // forget what was staged but not added
    for (const e of adItems) if (e.stage) api("DELETE", `/api/stage/${e.stage}`).catch(() => {});
    adItems = []; adSel = -1;
  });

  $("ad-go").onclick = async () => {
    if (adBusy || !adItems.length) return;
    for (const [i, e] of adItems.entries()) {
      e.error = "";
      if (e.kind === "file" && e.files.length && !e.sel.some(Boolean)) { adSel = i; adTab = "files"; e.error = "выберите хотя бы один файл"; renderAdd(); return; }
    }
    const items = adItems.map((e) => {
      const o = e.opts;
      const options = { savePath: o.savePath.trim(), label: o.label.trim(), paused: o.paused, sequential: o.sequential, edgePieces: o.edgePieces, skipCheck: o.skipCheck,
        moveDone: o.moveDoneOn ? o.moveDone.trim() : "", preallocate: o.preallocate === addInfo.preallocate ? null : o.preallocate, maxConns: Number(o.maxConns) || 0,
        files: e.kind === "file" && e.sel.some((x) => !x) ? e.sel.map((x) => (x ? "normal" : "skip")) : [] };
      return e.kind === "file" ? { stage: e.stage, options } : e.kind === "magnet" ? { magnet: e.magnet, options } : { infohash: e.infohash, options };
    });
    adBusy = true; $("ad-go").disabled = true; $("ad-go").textContent = "Добавляю…";
    let res;
    try { res = (await api("POST", "/api/add-batch", { items })).results; }
    catch (x) { adBusy = false; renderAdd(); $("ad-err").textContent = x.message; $("ad-err").hidden = false; return; }
    adBusy = false;
    const failed = [];
    let added = 0, already = 0;
    adItems.forEach((e, i) => {
      const r = res[i];
      if (r && r.ok) { r.exists ? already++ : added++; if (e.stage) e.stage = ""; }
      else { e.error = (r && r.error) || "не удалось добавить"; failed.push(e); }
    });
    adItems = failed; adSel = failed.length ? 0 : -1;
    refresh();
    if (!failed.length) {
      $("dlg-add").close();
      toast(`Добавлено: ${added}` + (already ? `, уже были в списке: ${already}` : ""));
    } else {
      renderAdd();
      toast(`Добавлено: ${added}. Не удалось: ${failed.length} — они остались в списке с описанием ошибки`, true);
    }
  };

  // drag & drop of .torrent files opens the dialog with them on the list
  let dragDepth = 0;
  addEventListener("dragenter", (e) => { if (e.dataTransfer && [...e.dataTransfer.types].includes("Files")) { dragDepth++; $("drop").hidden = false; } });
  addEventListener("dragleave", () => { if (--dragDepth <= 0) { dragDepth = 0; $("drop").hidden = true; } });
  addEventListener("dragover", (e) => e.preventDefault());
  addEventListener("drop", (e) => {
    if (!e.dataTransfer || !e.dataTransfer.files || !e.dataTransfer.files.length) { dragDepth = 0; $("drop").hidden = true; return; } // a column being moved, not files
    e.preventDefault(); dragDepth = 0; $("drop").hidden = true;
    const fl = [...(e.dataTransfer.files || [])].filter((f) => /\.torrent$/i.test(f.name));
    if (fl.length) openAdd(fl); else toast("Нужен файл .torrent", true);
  });
  // turtle
  $("s-turtle").onclick = () => act(api("POST", "/api/altspeed", { enabled: !(stats && stats.altSpeed) }));

  // Right click on the turtle: the limits in one small window, no trip to the settings.
  const limitsPop = $("limits-pop");
  limitFieldsDirty(["lp-down", "lp-up", "lp-adown", "lp-aup"]);
  const hideLimits = () => { limitsPop.hidden = true; $("s-turtle").classList.remove("tip-off"); };
  function showLimits() {
    if (!settings) return;
    { const bits = bitsMode();
      limitFill("lp-down", settings.downLimitKBps, bits); limitFill("lp-up", settings.upLimitKBps, bits);
      limitFill("lp-adown", settings.altDownLimitKBps, bits); limitFill("lp-aup", settings.altUpLimitKBps, bits);
      $("lp-unit").textContent = limitUnit(bits); }

    $("lp-on").checked = !!(stats && stats.altSpeed); $("lp-err").hidden = true;
    limitsPop.hidden = false;
    $("s-turtle").classList.add("tip-off"); // the tooltip would cover the window
    const r = $("s-turtle").getBoundingClientRect();
    limitsPop.style.left = Math.max(8, Math.min(r.left, innerWidth - limitsPop.offsetWidth - 8)) + "px";
    limitsPop.style.top = Math.max(8, r.top - limitsPop.offsetHeight - 10) + "px";
    $("lp-down").focus();
  }
  $("s-turtle").addEventListener("contextmenu", (e) => { e.preventDefault(); limitsPop.hidden ? showLimits() : hideLimits(); });
  document.addEventListener("pointerdown", (e) => { if (!limitsPop.hidden && !limitsPop.contains(e.target) && !$("s-turtle").contains(e.target)) hideLimits(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !limitsPop.hidden) { hideLimits(); $("s-turtle").focus(); } }, true);
  addEventListener("resize", hideLimits);
  $("f-limits").addEventListener("submit", async (e) => {
    e.preventDefault();
    const n = (id) => Math.max(0, Math.floor(Number($(id).value) || 0));
    try {
      // the settings call takes the whole set of these values, so the ones that were not touched are sent back as they are
      settings = await api("PUT", "/api/settings", {
        downLimitKBps: limitRead("lp-down", bitsMode()), upLimitKBps: limitRead("lp-up", bitsMode()), altDownLimitKBps: limitRead("lp-adown", bitsMode()), altUpLimitKBps: limitRead("lp-aup", bitsMode()),
        ratioLimit: settings.ratioLimit || 0, maxActiveDownloads: settings.maxActiveDownloads || 0, copyRemovePolicy: settings.copyRemovePolicy,
      });
      if (!!$("lp-on").checked !== !!(stats && stats.altSpeed)) await api("POST", "/api/altspeed", { enabled: $("lp-on").checked });
      hideLimits(); toast("Ограничения сохранены"); refresh();
    } catch (x) { $("lp-err").textContent = x.message; $("lp-err").hidden = false; }
  });
  $("lp-more").onclick = () => { hideLimits(); try { localStorage.setItem("setSection", "speed"); } catch (_) {} $("btn-settings").click(); };


  // ---------- sections of the settings ----------
  function showSection(id) {
    const btns = [...document.querySelectorAll("#set-nav [data-sec]")];
    const btn = btns.find((b) => b.dataset.sec === id && !b.hidden) || btns.find((b) => !b.hidden);
    id = btn.dataset.sec;
    for (const b of btns) { b.setAttribute("aria-selected", b === btn); b.tabIndex = b === btn ? 0 : -1; }
    for (const p of document.querySelectorAll("#dlg-settings .set-pane")) p.hidden = p.dataset.sec !== id;
    try { localStorage.setItem("setSection", id); } catch (_) {}
  }
  $("set-nav").addEventListener("click", (e) => { const b = e.target.closest("[data-sec]"); if (b) showSection(b.dataset.sec); });
  $("set-nav").addEventListener("keydown", (e) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const btns = [...document.querySelectorAll("#set-nav [data-sec]")].filter((b) => !b.hidden);
    const i = btns.findIndex((b) => b.getAttribute("aria-selected") === "true");
    const next = btns[(i + (e.key === "ArrowDown" ? 1 : btns.length - 1)) % btns.length];
    showSection(next.dataset.sec); next.focus();
  });
  // A field the browser refuses (say a negative number) may sit in a section that is not shown:
  // switch to it, or the form would fail without a word.
  $("f-settings").addEventListener("invalid", (e) => { const p = e.target.closest(".set-pane"); if (p) showSection(p.dataset.sec); }, true);
  const openSection = () => { let id = "speed"; try { id = localStorage.getItem("setSection") || id; } catch (_) {} showSection(id); };

  // settings
  $("btn-settings").onclick = async () => {
    try { settings = await api("GET", "/api/settings"); port = await api("GET", "/api/port"); } catch (e) { return toast(e.message, true); }
    { const bits = settings.speedUnit === "bits"; limitShownBits = bits;
      limitFill("st-down", settings.downLimitKBps, bits); limitFill("st-up", settings.upLimitKBps, bits);
      limitFill("st-altdown", settings.altDownLimitKBps, bits); limitFill("st-altup", settings.altUpLimitKBps, bits);
      $("st-speed-legend").textContent = `Ограничения скорости, ${limitUnit(bits)} (0 — без ограничения)`; }

    fillNetwork(settings.network || {});
    fillSchedule(settings.altSchedule || {}); $("st-unit-bits").checked = settings.speedUnit === "bits"; $("st-unit-bytes").checked = settings.speedUnit !== "bits"; $("st-maxactive").value = settings.maxActiveDownloads; $("st-maxchecks").value = settings.maxConcurrentChecks ?? 2; $("st-addpaused").checked = !!(settings.add && settings.add.paused); $("st-ratio").value = settings.ratioLimit; $("st-seedtime").value = (settings.seedTimeLimitMinutes || 0) / 60; $("st-notify").checked = settings.notifyOnComplete !== false; $("sn-system").hidden = !window.__equinoxDesktop; $("st-starthidden").checked = settings.startHidden !== false; $("st-closetray").checked = settings.closeToTray !== false; $("st-mintray").checked = !!settings.minimizeToTray;
    if (window.__equinoxDesktop && typeof window.getAutostart === "function") window.getAutostart().then((on) => { $("st-autostart").checked = !!on; }).catch(() => {}); $("st-copy").value = settings.copyRemovePolicy;
    $("st-data").value = settings.dataDir; $("st-movedone").value = settings.moveCompletedDir || ""; $("st-watch").value = settings.watchDir || ""; $("st-copydir").value = settings.torrentCopyDir || "";
    openLabelRows();
    $("st-prealloc").checked = !!settings.preallocate; $("st-port-num").value = settings.listenPort;
    $("st-port-note").hidden = !(port && settings.listenPort !== port.port);
    $("st-port").textContent = portDetails(port); $("st-mapping").checked = !!port.enabled;
    openSection();
    $("st-err").hidden = true; $("dlg-settings").showModal();
  };
  { const ids = ["st-down", "st-up", "st-altdown", "st-altup"];
    limitFieldsDirty(ids);
    for (const r of [$("st-unit-bytes"), $("st-unit-bits")]) r.addEventListener("change", () => {
      const bits = $("st-unit-bits").checked;
      if (bits === limitShownBits) return;
      limitRefit(ids, limitShownBits, bits); limitShownBits = bits;
      $("st-speed-legend").textContent = `Ограничения скорости, ${limitUnit(bits)} (0 — без ограничения)`;
    });
  }
  $("st-mapping").onchange = async (e) => {
    try { port = await api("POST", "/api/port/mapping", { enabled: e.target.checked }); $("st-port").textContent = portDetails(port); renderPort(); }
    catch (x) { e.target.checked = !e.target.checked; toast(x.message, true); }
  };
  $("st-port-refresh").onclick = () => api("POST", "/api/port/refresh").then(() => toast("Проверка запущена")).catch((e) => toast(e.message, true));
  $("f-settings").addEventListener("submit", async (e) => {
    e.preventDefault();
    const n = (id) => Number($(id).value) || 0;
    const bitsNow = $("st-unit-bits").checked; // the unit the limit fields are shown in right now
    try {
      // labels deleted in the settings are taken off their torrents first
      for (const name of lbDeleted) for (const t of torrents.filter((x) => x.label === name)) await api("POST", `/api/torrents/${t.hash}/label`, { label: "" });
      lbDeleted = new Set();
      settings = await api("PUT", "/api/settings", {
        downLimitKBps: limitRead("st-down", bitsNow), upLimitKBps: limitRead("st-up", bitsNow), altDownLimitKBps: limitRead("st-altdown", bitsNow), altUpLimitKBps: limitRead("st-altup", bitsNow),
        network: readNetwork(),
        dataDir: $("st-data").value.trim(), moveCompletedDir: $("st-movedone").value.trim(), watchDir: $("st-watch").value.trim(), torrentCopyDir: $("st-copydir").value.trim(), labelPaths: readLabelPaths(), labelColors: readLabelColors(),
        preallocate: $("st-prealloc").checked, listenPort: n("st-port-num"),
        ratioLimit: n("st-ratio"), seedTimeLimitMinutes: Math.round(n("st-seedtime") * 60), maxConcurrentChecks: n("st-maxchecks"), speedUnit: $("st-unit-bits").checked ? "bits" : "bytes", addPaused: $("st-addpaused").checked, notifyOnComplete: $("st-notify").checked, startHidden: $("st-starthidden").checked, closeToTray: $("st-closetray").checked, minimizeToTray: $("st-mintray").checked, maxActiveDownloads: n("st-maxactive"), altSchedule: readSchedule(), copyRemovePolicy: $("st-copy").value,
      });
      if (window.__equinoxDesktop && typeof window.setAutostart === "function") {
        const err = await window.setAutostart($("st-autostart").checked);
        if (err) { toast("Не удалось изменить автозапуск: " + err, true); }
      }
      $("dlg-settings").close(); toast("Настройки сохранены"); refresh(); checkRestart();
    } catch (x) { $("st-err").textContent = x.message; $("st-err").hidden = false; }
  });

  $("st-assoc").onclick = async () => {
    if (typeof window.registerHandlers !== "function") return;
    const err = await window.registerHandlers();
    if (err) toast("Не удалось зарегистрировать приложение: " + err, true);
  };

  // connection settings
  const NW_CHECKS = { dht: "nw-dht", pex: "nw-pex", utp: "nw-utp", tcp: "nw-tcp", ipv6: "nw-ipv6", webseeds: "nw-web", acceptIncoming: "nw-in" };
  function fillNetwork(n) {
    $("nw-conns").value = n.maxConnsPerTorrent; $("nw-half").value = n.maxHalfOpenPerTorrent; $("nw-enc").value = n.encryption || "prefer";
    for (const [k, id] of Object.entries(NW_CHECKS)) $(id).checked = !!n[k];
  }
  function readNetwork() {
    const n = { maxConnsPerTorrent: Number($("nw-conns").value) || 0, maxHalfOpenPerTorrent: Number($("nw-half").value) || 0, encryption: $("nw-enc").value };
    for (const [k, id] of Object.entries(NW_CHECKS)) n[k] = $(id).checked;
    return n;
  }

  // "restart required" bar: some settings are read only when the engine starts
  const WHY = { dht: "DHT", pex: "PEX", utp: "uTP", tcp: "TCP", ipv6: "IPv6", webseeds: "веб-сиды", incoming: "входящие соединения",
    encryption: "шифрование", halfopen: "попытки соединения", port: "порт" };
  async function checkRestart() {
    try {
      const r = await api("GET", "/api/restart-required");
      $("restart-bar").hidden = !r.required;
      if (!r.required) return;
      const canRestart = typeof window.restartApp === "function";
      $("restart-text").textContent = `Изменения вступят в силу после перезапуска: ${r.reasons.map((k) => WHY[k] || k).join(", ")}.` + (canRestart ? "" : " Перезапустите приложение.");
      $("btn-restart").hidden = !canRestart;
    } catch (_) { /* not critical */ }
  }
  $("btn-restart").onclick = async () => {
    try { toast("Перезапуск…"); const err = await window.restartApp(); if (err) toast("Не удалось перезапустить: " + err, true); }
    catch (x) { toast("Не удалось перезапустить: " + x, true); }
  };

  // schedule: weekday chips (values are JS weekdays: 0 = Sunday)
  const DAYS = [[1, "Пн"], [2, "Вт"], [3, "Ср"], [4, "Чт"], [5, "Пт"], [6, "Сб"], [0, "Вс"]];
  $("sc-days").innerHTML = DAYS.map(([v, n]) => `<label><input type="checkbox" value="${v}"><span>${n}</span></label>`).join("");
  function fillSchedule(sc) {
    $("sc-on").checked = !!sc.enabled; $("sc-from").value = sc.from || "23:00"; $("sc-to").value = sc.to || "07:00";
    const on = new Set(sc.days || []);
    $("sc-days").querySelectorAll("input").forEach((i) => (i.checked = on.has(Number(i.value)))); // none checked = every day
  }
  function readSchedule() {
    return { enabled: $("sc-on").checked, from: $("sc-from").value, to: $("sc-to").value,
      days: [...$("sc-days").querySelectorAll("input:checked")].map((i) => Number(i.value)) };
  }

  // ---------- labels in the settings ----------
  // One row per label: its name and the folder for new torrents that get it. A label that torrents
  // carry cannot be deleted here (it would come back at once); take it off the torrents first.
  let lbRows = []; // { name, path, color }
  let lbDeleted = new Set(); // labels deleted here that torrents still carry: taken off them when the settings are saved
  function openLabelRows() {
    const paths = (settings && settings.labelPaths) || {};
    const names = new Set([...Object.keys(paths), ...torrents.map((t) => t.label).filter(Boolean)]);
    lbRows = [...names].sort((a, b) => a.localeCompare(b)).map((name) => ({ name, path: paths[name] || "", color: labelColor(name) }));
    lbDeleted = new Set();
    $("lb-new").value = ""; $("lb-err").hidden = true;
    renderLabelRows();
  }
  function renderLabelRows() {
    const used = (n) => torrents.filter((t) => t.label === n).length;
    $("lb-rows").innerHTML = lbRows.length === 0 ? '<div class="lb-none">Меток пока нет. Введите название ниже и нажмите «Добавить метку».</div>' :
      lbRows.map((r, i) => {
        const n = used(r.name);
        return `<div class="lb-row" data-i="${i}"><span class="lb-name" title="${esc(r.name)}"><i class="tagdot lc-${r.color}"></i>${esc(r.name)}<small>${n ? `раздач: ${n}` : "не назначена"}</small></span>` +
          `<span class="pick"><input data-lbpath="${i}" value="${esc(r.path)}" placeholder="общая папка загрузок" autocomplete="off" aria-label="Папка для метки ${esc(r.name)}">` +
          `<button type="button" class="btn small" data-lbpick="${i}">Обзор…</button></span>` +
          `<button type="button" class="btn small danger" data-lbdel="${i}">Удалить</button></div>`;
      }).join("");
  }
  // A label that no torrent carries goes at once; one that torrents carry needs a confirmation that says what happens to them.
  let lbAsk = -1;
  function askDeleteLabel(i) {
    const r = lbRows[i]; if (!r) return;
    const n = torrents.filter((t) => t.label === r.name).length;
    if (!n) { lbRows.splice(i, 1); renderLabelRows(); return; }
    lbAsk = i;
    const word = n % 10 === 1 && n % 100 !== 11 ? "раздача" : n % 10 >= 2 && n % 10 <= 4 && (n % 100 < 12 || n % 100 > 14) ? "раздачи" : "раздач";
    const fate = n % 10 === 1 && n % 100 !== 11 ? "она окажется без метки и будет перенесена" : "они окажутся без метки и будут перенесены";
    $("lbdel-text").textContent = `Метка «${r.name}» назначена: ${n} ${word}. После удаления ${fate} в группу «Без метки».`;
    $("dlg-lbdel").showModal();
  }
  $("f-lbdel").addEventListener("submit", () => {
    const r = lbRows[lbAsk]; if (!r) return;
    lbDeleted.add(r.name); lbRows.splice(lbAsk, 1); lbAsk = -1; renderLabelRows();
  });
  function addLabelRow() {
    const name = $("lb-new").value.trim(), err = $("lb-err");
    const bad = !name ? "Введите название метки." : [...name].length > 40 ? "Название длиннее 40 символов." :
      lbRows.some((r) => r.name === name) ? "Такая метка уже есть." : "";
    err.textContent = bad; err.hidden = !bad;
    if (bad) return;
    lbDeleted.delete(name); // adding it again cancels its deletion
    lbRows.push({ name, path: "", color: newLabelColor() }); lbRows.sort((a, b) => a.name.localeCompare(b.name));
    $("lb-new").value = ""; renderLabelRows();
    $("lb-rows").querySelector(`[data-lbpath="${lbRows.findIndex((r) => r.name === name)}"]`)?.focus();
  }
  $("lb-add").onclick = addLabelRow;
  $("lb-new").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); addLabelRow(); } });
  $("lb-rows").addEventListener("input", (e) => { const i = e.target.dataset.lbpath; if (i !== undefined) lbRows[i].path = e.target.value; });
  $("lb-rows").addEventListener("click", async (e) => {
    const del = e.target.closest("[data-lbdel]"), pick = e.target.closest("[data-lbpick]");
    if (del) askDeleteLabel(Number(del.dataset.lbdel));
    if (pick) {
      const r = lbRows[Number(pick.dataset.lbpick)];
      const dir = await pickFolder("Папка для метки «" + r.name + "»", r.path); if (dir) { r.path = dir; renderLabelRows(); }
    }
  });
  function readLabelColors() {
    const out = {};
    for (const r of lbRows) out[r.name] = r.color;
    return out;
  }
  function readLabelPaths() {
    const out = {};
    for (const r of lbRows) out[r.name] = r.path.trim();
    return out;
  }
  $("st-port-num").addEventListener("input", () => { $("st-port-note").hidden = !(port && Number($("st-port-num").value) !== port.port); });

  // ---------- folder picker (our own window, the same in the browser and in the app) ----------
  const pk = { resolve: null, data: null, sel: "", seq: 0 };
  const pkFinish = (v) => { const r = pk.resolve; pk.resolve = null; if (r) r(v); };
  function pickFolder(title, initial) {
    return new Promise((resolve) => {
      if ($("dlg-picker").open) return resolve("");
      pk.resolve = resolve; pk.sel = "";
      $("pk-title").textContent = title || "Выбор папки";
      $("pk-newrow").hidden = true; $("pk-err").hidden = true; $("pk-path").value = initial || "";
      $("dlg-picker").showModal();
      pkGo(initial || "");
    });
  }
  $("dlg-picker").addEventListener("close", () => pkFinish("")); // closed without choosing
  async function pkGo(path) {
    const seq = ++pk.seq;
    let d;
    try { d = await api("GET", "/api/fs?path=" + encodeURIComponent(path)); }
    catch (x) { $("pk-err").textContent = x.message; $("pk-err").hidden = false; return; }
    if (seq !== pk.seq) return; // a newer request is on its way
    if (!d) { $("pk-err").textContent = "Сервер не вернул список папок."; $("pk-err").hidden = false; return; }
    pk.data = d; pk.sel = "";
    $("pk-err").hidden = true;
    $("pk-path").value = d.path;
    renderPicker();
  }
  const pkDate = (s) => { const t = Date.parse(s); return t > 31536000000 ? new Date(t).toLocaleDateString("ru-RU") + " " + new Date(t).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" }) : ""; };
  function renderPicker() {
    const d = pk.data; if (!d) return;
    $("pk-up").disabled = d.parent === null;
    $("pk-newbtn").disabled = !d.path;
    $("pk-crumbs").innerHTML = d.crumbs.map((c, i) => (i ? '<span class="sep">›</span>' : "") + `<button type="button" data-go="${esc(c.path)}">${esc(c.name)}</button>`).join("");
    $("pk-crumbs").scrollLeft = 1e6;
    const inside = (p) => d.path && (d.path === p || d.path.toLowerCase().startsWith(p.replace(/[\\/]+$/, "").toLowerCase() + (p.endsWith("\\") || p.endsWith("/") ? "" : "\\")) );
    const cur = d.places.filter((p) => d.path === p.path);
    const curPlace = cur[0] || d.places.filter((p) => p.kind === "drive" && inside(p.path))[0];
    let last = "";
    $("pk-places").innerHTML = d.places.map((p) => {
      const sep = last && last !== p.kind && (p.kind === "drive") ? '<div class="sp"></div>' : "";
      last = p.kind;
      return sep + `<button type="button" data-go="${esc(p.path)}" title="${esc(p.path)}"${curPlace === p ? ' aria-current="true"' : ""}><svg class="i"><use href="#i-folder"/></svg><span>${esc(p.name)}</span></button>`;
    }).join("");
    const rows = d.entries.map((e, i) => e.dir ?
      `<div class="pk-row dir" role="option" data-i="${i}" data-path="${esc(e.path)}"><span class="nm"><svg class="i"><use href="#i-folder"/></svg><span>${esc(e.name)}</span></span><span class="r dt"></span><span class="dt">${pkDate(e.modified)}</span></div>` :
      `<div class="pk-row file" aria-disabled="true"><span class="nm"><svg class="i"><use href="#i-newfile"/></svg><span>${esc(e.name)}</span></span><span class="r dt">${bytes(e.size)}</span><span class="dt">${pkDate(e.modified)}</span></div>`);
    $("pk-list").innerHTML = (d.error ? `<div class="pk-msg">${esc(d.error)}</div>` : "") + rows.join("") +
      (!d.error && d.entries.length === 0 ? '<div class="pk-msg">Здесь пусто. Можно выбрать эту папку или создать в ней новую.</div>' : "") +
      (d.truncated ? '<div class="pk-msg">Показаны первые записи, остальные скрыты.</div>' : "");
    $("pk-ok").disabled = !$("pk-path").value.trim();
  }
  function pkSelect(path) {
    pk.sel = path;
    for (const r of $("pk-list").querySelectorAll(".pk-row.dir")) { const on = r.dataset.path === path; r.classList.toggle("sel", on); r.setAttribute("aria-selected", on); if (on) r.scrollIntoView({ block: "nearest" }); }
    $("pk-path").value = path || (pk.data && pk.data.path) || "";
    $("pk-ok").disabled = !$("pk-path").value.trim();
  }
  $("pk-list").addEventListener("click", (e) => { const r = e.target.closest(".pk-row.dir"); if (r) pkSelect(r.dataset.path); });
  $("pk-list").addEventListener("dblclick", (e) => { const r = e.target.closest(".pk-row.dir"); if (r) pkGo(r.dataset.path); });
  $("pk-list").addEventListener("keydown", (e) => {
    const rows = [...$("pk-list").querySelectorAll(".pk-row.dir")]; if (!rows.length) return;
    const i = rows.findIndex((r) => r.dataset.path === pk.sel);
    if (e.key === "ArrowDown") { e.preventDefault(); pkSelect(rows[Math.min(rows.length - 1, i + 1)].dataset.path); }
    else if (e.key === "ArrowUp") { e.preventDefault(); pkSelect(rows[Math.max(0, i - 1)].dataset.path); }
    else if (e.key === "Enter" && i >= 0) { e.preventDefault(); pkGo(pk.sel); }
    else if (e.key === "Backspace" && pk.data && pk.data.parent !== null) { e.preventDefault(); pkGo(pk.data.parent); }
  });
  for (const id of ["pk-crumbs", "pk-places"]) $(id).addEventListener("click", (e) => { const b = e.target.closest("[data-go]"); if (b) pkGo(b.dataset.go); });
  $("pk-up").onclick = () => { if (pk.data && pk.data.parent !== null) pkGo(pk.data.parent); };
  $("pk-path").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); const v = $("pk-path").value.trim(); if (v) pkGo(v); } });
  $("pk-path").addEventListener("input", () => { $("pk-ok").disabled = !$("pk-path").value.trim(); });
  $("pk-ok").onclick = () => { const v = $("pk-path").value.trim(); if (!v) return; pkFinish(v); $("dlg-picker").close(); };
  // new folder
  $("pk-newbtn").onclick = () => { $("pk-newrow").hidden = !$("pk-newrow").hidden; if (!$("pk-newrow").hidden) { $("pk-newname").value = ""; $("pk-newname").focus(); } };
  $("pk-newcancel").onclick = () => { $("pk-newrow").hidden = true; };
  async function pkMake() {
    const name = $("pk-newname").value.trim(); if (!name || !pk.data || !pk.data.path) return;
    try {
      const r = await api("POST", "/api/fs/mkdir", { parent: pk.data.path, name });
      $("pk-newrow").hidden = true; await pkGo(r.path);
    } catch (x) { $("pk-err").textContent = x.message; $("pk-err").hidden = false; }
  }
  $("pk-newok").onclick = pkMake;
  $("pk-newname").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); pkMake(); } else if (e.key === "Escape") { e.stopPropagation(); $("pk-newrow").hidden = true; } });

  // every "Обзор…" button in the page opens it and puts the answer into its field
  document.querySelectorAll("[data-pick]").forEach((b) => {
    b.addEventListener("click", async () => {
      const input = $(b.dataset.pick);
      const dir = await pickFolder("Выберите папку", input.value);
      if (dir) { input.value = dir; input.dispatchEvent(new Event("input", { bubbles: true })); }
    });
  });

  // In the desktop window the title bar takes the colours of the top bar (and follows the light or dark theme).
  function syncTitleBar() {
    if (typeof window.setTitleBar !== "function") return;
    const bar = document.querySelector(".topbar");
    if (!bar) return;
    window.setTitleBar(getComputedStyle(bar).backgroundColor, getComputedStyle(document.body).color);
  }
  syncTitleBar();
  try { matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => setTimeout(syncTitleBar, 60)); } catch (_) {}

  // token dialog
  $("f-token").addEventListener("submit", (e) => {
    e.preventDefault(); token = $("in-token").value.trim();
    try { localStorage.setItem("token", token); } catch (_) {}
    $("dlg-token").close(); refresh();
  });
  $("dlg-token").addEventListener("cancel", (e) => e.preventDefault()); // cannot be dismissed without a key

  document.querySelectorAll("[data-close]").forEach((b) => b.addEventListener("click", () => b.closest("dialog").close()));

  // ---------- first-run guide ("Быстрая настройка") ----------
  const wz = { steps: [], i: 0, cur: null, port: null, bits: false };
  const wzDesktop = () => !!window.__equinoxDesktop;
  const wzSections = () => [...document.querySelectorAll("#dlg-wizard [data-wz]")];
  function wzShow() {
    const sec = wz.steps[wz.i];
    for (const s of wzSections()) s.hidden = s !== sec;
    $("wz-title").textContent = sec.dataset.title;
    $("wz-count").textContent = wz.i > 0 && wz.i < wz.steps.length - 1 ? `Шаг ${wz.i} из ${wz.steps.length - 2}` : "";
    $("wz-dots").innerHTML = wz.steps.map((_, k) => `<i class="${k === wz.i ? "on" : k < wz.i ? "past" : ""}"></i>`).join("");
    const last = wz.i === wz.steps.length - 1;
    $("wz-back").hidden = wz.i === 0;
    $("wz-skip").hidden = last;
    $("wz-next").textContent = last ? "Готово" : wz.i === 0 ? "Начать" : "Далее";
    $("wz-err").hidden = true;
    if (last) wzSummary();
  }
  const wzUnit = () => $("wz-unit-bits").checked;
  function wzSummary() {
    const bits = wzUnit(), li = [];
    const lim = (id) => (limitRead(id, bits) ? `${$(id).value} ${limitUnit(bits)}` : "без ограничения");
    li.push(`Папка загрузок: ${$("wz-data").value.trim() || "—"}`);
    li.push($("wz-watch").value.trim() ? `Папка автодобавления: ${$("wz-watch").value.trim()}` : "Папка автодобавления: выключена");
    li.push(`Скорость: загрузка ${lim("wz-down")}, отдача ${lim("wz-up")}`);
    li.push(`Порт ${Number($("wz-port").value) || "—"}, автоматический проброс: ${$("wz-mapping").checked ? "включён" : "выключен"}`);
    if (wzDesktop()) li.push(`Запуск вместе с Windows: ${$("wz-autostart").checked ? ($("wz-starthidden").checked ? "да, в трее" : "да") : "нет"}; закрытие окна ${$("wz-closetray").checked ? "прячет в трей" : "завершает программу"}`);
    $("wz-sum").innerHTML = li.map((t) => `<li>${esc(t)}</li>`).join("");
  }
  async function openWizard() {
    if ($("dlg-wizard").open) return;
    try { wz.cur = await api("GET", "/api/settings"); wz.port = await api("GET", "/api/port"); } catch (e) { return toast(e.message, true); }
    const s = wz.cur, bits = s.speedUnit === "bits";
    wz.bits = bits;
    $("wz-data").value = s.dataDir || ""; $("wz-watch").value = s.watchDir || ""; $("wz-paused").checked = !!(s.add && s.add.paused);
    $("wz-unit-bits").checked = bits; $("wz-unit-bytes").checked = !bits;
    limitFill("wz-down", s.downLimitKBps, bits); limitFill("wz-up", s.upLimitKBps, bits);
    $("wz-speed-legend").textContent = `Ограничения скорости, ${limitUnit(bits)} (0 — без ограничения)`;
    $("wz-port").value = s.listenPort; $("wz-mapping").checked = !!wz.port.enabled; $("wz-port-note").hidden = true;
    $("wz-starthidden").checked = s.startHidden !== false; $("wz-closetray").checked = s.closeToTray !== false; $("wz-notify").checked = s.notifyOnComplete !== false;
    $("wz-autostart").checked = false;
    if (wzDesktop() && typeof window.getAutostart === "function") window.getAutostart().then((on) => { $("wz-autostart").checked = !!on; }).catch(() => {});
    wz.steps = wzSections().filter((x) => x.dataset.wz !== "system" || wzDesktop());
    wz.i = 0; wzShow();
    $("dlg-wizard").showModal();
  }
  limitFieldsDirty(["wz-down", "wz-up"]);
  for (const r of [$("wz-unit-bytes"), $("wz-unit-bits")]) r.addEventListener("change", () => {
    const bits = wzUnit();
    if (bits === wz.bits) return;
    limitRefit(["wz-down", "wz-up"], wz.bits, bits); wz.bits = bits;
    $("wz-speed-legend").textContent = `Ограничения скорости, ${limitUnit(bits)} (0 — без ограничения)`;
  });
  // a random port from the range the system keeps for private use, away from well-known services
  const randomPort = () => 49152 + Math.floor(Math.random() * (65535 - 49152 + 1));
  for (const [btn, field] of [["wz-port-rnd", "wz-port"], ["st-port-rnd", "st-port-num"]]) {
    $(btn).onclick = () => { $(field).value = randomPort(); $(field).dispatchEvent(new Event("input", { bubbles: true })); };
  }
  $("wz-port").addEventListener("input", () => { $("wz-port-note").hidden = !(wz.port && Number($("wz-port").value) !== wz.port.port); });
  $("wz-assoc").onclick = async () => {
    if (typeof window.registerHandlers !== "function") return;
    const err = await window.registerHandlers();
    if (err) { $("wz-err").textContent = "Не удалось зарегистрировать приложение: " + err; $("wz-err").hidden = false; }
  };
  // The fields that are not optional in the settings request keep what they are now.
  const wzBase = (s) => ({ downLimitKBps: s.downLimitKBps, upLimitKBps: s.upLimitKBps, altDownLimitKBps: s.altDownLimitKBps, altUpLimitKBps: s.altUpLimitKBps,
    ratioLimit: s.ratioLimit, maxActiveDownloads: s.maxActiveDownloads, copyRemovePolicy: s.copyRemovePolicy });
  async function wzSkip() { // leave the defaults, but do not ask again
    try { settings = await api("PUT", "/api/settings", { ...wzBase(wz.cur), setupDone: true }); } catch (_) {}
    if ($("dlg-wizard").open) $("dlg-wizard").close();
  }
  async function wzFinish() {
    const bits = wzUnit(), n = (id) => Number($(id).value) || 0;
    const port = n("wz-port");
    if (!$("wz-data").value.trim()) { $("wz-err").textContent = "Укажите папку для загрузок."; $("wz-err").hidden = false; return; }
    if (port < 1 || port > 65535) { $("wz-err").textContent = "Порт должен быть от 1 до 65535."; $("wz-err").hidden = false; return; }
    const body = { ...wzBase(wz.cur), setupDone: true,
      dataDir: $("wz-data").value.trim(), watchDir: $("wz-watch").value.trim(), addPaused: $("wz-paused").checked,
      speedUnit: bits ? "bits" : "bytes", downLimitKBps: limitRead("wz-down", bits), upLimitKBps: limitRead("wz-up", bits), listenPort: port };
    if (wzDesktop()) Object.assign(body, { startHidden: $("wz-starthidden").checked, closeToTray: $("wz-closetray").checked, notifyOnComplete: $("wz-notify").checked });
    $("wz-next").disabled = true;
    try {
      settings = await api("PUT", "/api/settings", body);
      if ($("wz-mapping").checked !== !!wz.port.enabled) await api("POST", "/api/port/mapping", { enabled: $("wz-mapping").checked });
      if (wzDesktop() && typeof window.setAutostart === "function") {
        const err = await window.setAutostart($("wz-autostart").checked);
        if (err) toast("Не удалось изменить автозапуск: " + err, true);
      }
      $("dlg-wizard").close(); toast("Настройка сохранена"); refresh(); checkRestart();
    } catch (x) { $("wz-err").textContent = x.message; $("wz-err").hidden = false; }
    finally { $("wz-next").disabled = false; }
  }
  $("wz-next").onclick = () => { if (wz.i < wz.steps.length - 1) { wz.i++; wzShow(); } else wzFinish(); };
  $("wz-back").onclick = () => { if (wz.i > 0) { wz.i--; wzShow(); } };
  $("wz-skip").onclick = wzSkip;
  $("dlg-wizard").addEventListener("cancel", (e) => { e.preventDefault(); wzSkip(); }); // Esc counts as "skip"
  $("st-wizard").onclick = () => { $("dlg-settings").close(); openWizard(); };
  // the first start: offer the guide once
  (async () => { if (!token) return; try { const s = await api("GET", "/api/settings"); if (!s.setupDone) openWizard(); } catch (_) {} })();

  if (!token) askToken();
  loop();
  checkRestart(); setInterval(checkRestart, 20000);
})();
