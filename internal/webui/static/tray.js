// The menu of the tray icon (right click): a small window of its own, drawn by the tray-menu process of the
// program (cmd/equinox/trayhost_windows.go), which shows it near the icon and hides it when it loses the focus.
// It talks to the program through the same API as the main window and to its own process through the bound
// functions trayMeasure, trayHide and trayAction.
(function () {
  "use strict";
  const token = window.__equinoxToken || "";
  const $ = (id) => document.getElementById(id);
  let EN = !/^ru/i.test(navigator.language || "");
  const T = {
    open: ["Открыть Equinox", "Open Equinox"], add: ["Добавить раздачу…", "Add a torrent…"], folder: ["Папка загрузок", "Downloads folder"],
    turtle: ["Ограничение скорости", "Speed limit"], settings: ["Настройки", "Settings"], quit: ["Выход", "Quit"],
    pause: ["Поставить все на паузу", "Pause all"], resume: ["Продолжить все", "Resume all"],
    dl: ["качаются", "downloading"], seed: ["раздаются", "seeding"], paused: ["на паузе", "paused"], none: ["Раздач нет", "No torrents"],
  };
  const tr = (k) => T[k][EN ? 1 : 0];
  function applyLang() {
    document.documentElement.lang = EN ? "en" : "ru";
    for (const el of document.querySelectorAll("[data-t]")) el.textContent = tr(el.dataset.t);
  }

  async function api(method, path, body) {
    const r = await fetch(path, { method, headers: { Authorization: "Bearer " + token, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
    if (!r.ok) throw new Error(await r.text());
    return r.status === 204 ? null : r.json();
  }

  // speeds, in bytes or bits as the settings say
  function rate(n, bits) {
    const units = bits ? (EN ? ["b", "Kbit", "Mbit", "Gbit"] : ["б", "Кбит", "Мбит", "Гбит"]) : (EN ? ["B", "KB", "MB", "GB"] : ["Б", "КБ", "МБ", "ГБ"]);
    if (!(n > 0)) return "0 " + units[0] + (EN ? "/s" : "/с");
    let v = bits ? n * 8 : n, i = 0;
    const k = bits ? 1000 : 1024;
    while (v >= k && i < units.length - 1) { v /= k; i++; }
    const num = (i === 0 || v >= 100 ? v.toFixed(0) : v.toFixed(1)).replace(".", EN ? "." : ",");
    return num + " " + units[i] + (EN ? "/s" : "/с");
  }

  let state = { running: false, turtle: false, dir: "", any: false };
  async function refresh() {
    try {
      const [list, stats, cfg] = await Promise.all([api("GET", "/api/torrents"), api("GET", "/api/stats"), api("GET", "/api/settings")]);
      EN = cfg.language ? cfg.language === "en" : !/^ru/i.test(navigator.language || "");
      const bits = cfg.speedUnit === "bits";
      let down = 0, up = 0, dl = 0, seed = 0, paused = 0;
      for (const t of list) {
        down += t.downRate; up += t.upRate;
        if (t.paused) paused++; else if (t.progress >= 1) seed++; else dl++;
      }
      $("t-down").textContent = "↓ " + rate(down, bits);
      $("t-up").textContent = "↑ " + rate(up, bits);
      $("t-cnt").textContent = list.length ? dl + " " + tr("dl") + " · " + seed + " " + tr("seed") + " · " + paused + " " + tr("paused") : tr("none");
      state = { running: list.some((t) => !t.paused), turtle: !!stats.altSpeed, dir: cfg.dataDir, any: list.length > 0 };
      $("t-pause").textContent = state.running ? tr("pause") : tr("resume");
      $("g-pause").firstElementChild.setAttribute("href", state.running ? "#ti-pause" : "#ti-play");
      $("a-pause").disabled = !state.any;
      $("sw-turtle").classList.toggle("on", state.turtle);
      $("a-turtle").setAttribute("aria-checked", String(state.turtle));
    } catch (_) { /* the program is busy or gone: keep what is shown */ }
    applyLang();
    measure();
  }
  // the window gets the size of the content (in physical pixels)
  function measure() {
    const m = $("menu").getBoundingClientRect(), r = window.devicePixelRatio || 1;
    if (typeof window.trayMeasure === "function") window.trayMeasure(Math.ceil(m.width * r), Math.ceil(m.height * r));
  }

  const hide = () => { if (typeof window.trayHide === "function") window.trayHide(); };
  const act = (name) => { hide(); if (typeof window.trayAction === "function") window.trayAction(name); };
  $("a-open").onclick = () => act("open");
  $("a-add").onclick = () => act("add");
  $("a-folder").onclick = () => act("folder:" + state.dir);
  $("a-settings").onclick = () => act("settings");
  $("a-quit").onclick = () => act("quit");
  $("a-pause").onclick = async () => { hide(); try { await api("POST", "/api/pause-all", { paused: state.running }); } catch (_) { /* nothing to do */ } };
  $("a-turtle").onclick = async () => { hide(); try { await api("POST", "/api/altspeed", { enabled: !state.turtle }); } catch (_) { /* nothing to do */ } };
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") { e.preventDefault(); hide(); return; }
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const items = [...document.querySelectorAll(".it:not(:disabled)")], i = items.indexOf(document.activeElement);
    items[(i + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
  });

  // the process shows and hides the window: the page refreshes only while it is on screen
  let timer = null;
  window.trayShown = () => { clearInterval(timer); refresh(); timer = setInterval(refresh, 1000); };
  window.trayHidden = () => { clearInterval(timer); timer = null; if (document.activeElement) document.activeElement.blur(); };
  applyLang();
  refresh();
})();
