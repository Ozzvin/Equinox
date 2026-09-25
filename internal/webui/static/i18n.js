// The language of the page. It runs from <head>, before the page is drawn, and does two things:
//  - decides the language: the choice saved in Settings ("uiLang": ru, en, or as in the system);
//  - in English, translates what the page shows. The page is written in Russian; every text that comes to the
//    screen (text nodes and the attributes people read: title, placeholder, aria-label, data-tip) is looked up in the
//    dictionary of i18n-en.js and replaced, both what is in the file and what app.js draws later (a
//    MutationObserver watches for it). A Russian text with no entry stays as it is and is listed in
//    window.__i18nMissing, which is how the dictionary is kept complete.
// The numbers, units and dates are made in the right language by app.js itself (it reads window.__lang).
(function () {
  var pref = "system";
  try { pref = localStorage.getItem("uiLang") || "system"; } catch (e) {}
  var lang = pref === "ru" || pref === "en" ? pref : (String(navigator.language || "").toLowerCase().indexOf("ru") === 0 ? "ru" : "en");
  window.__langPref = pref;
  window.__lang = lang;
  document.documentElement.lang = lang;
  if (lang !== "en") return;

  var dict = window.__i18nEn || [];
  var exact = Object.create(null), patterns = [];
  dict.forEach(function (e) {
    var ru = e[0], en = e[1];
    if (ru.indexOf("{}") < 0 && ru.indexOf("{*}") < 0) { exact[ru] = en; return; }
    // {} is a piece of one line; {*} may run over several lines (the text of an error)
    var src = ru.replace(/\{\*\}/g, "\u0001").replace(/[.*+?^$()|[\]\\]/g, "\\$&").replace(/\{\}/g, "([^\\n]+?)").replace(/\u0001/g, "([\\s\\S]+?)");
    patterns.push({ re: new RegExp("^" + src + "$"), en: en, literal: ru.replace(/\{\*?\}/g, "").length });
  });
  // a text that fits several patterns takes the one with the most words of its own ("{} из {}" is after "{} из {} · по {}")
  patterns.sort(function (a, b) { return b.literal - a.literal; });

  var CYR = /[Ѐ-ӿ]/;
  var missing = new Set();
  window.__i18nMissing = missing;

  function fill(en, args) {
    var i = 0;
    return en.replace(/\{(\d*)\}/g, function (m, n) { var a = args[n ? Number(n) - 1 : i++]; return a === undefined ? m : tr(a); });
  }
  // one text: whole, then by the patterns, then (a tip of several lines, a list) piece by piece
  function lookup(core) {
    var hit = exact[core];
    if (hit !== undefined) return hit;
    var flat = core.replace(/\s+/g, " ");
    if (flat !== core) { hit = exact[flat]; if (hit !== undefined) return hit; }
    for (var i = 0; i < patterns.length; i++) {
      var m = patterns[i].re.exec(core);
      if (m) return fill(patterns[i].en, m.slice(1));
    }
    return undefined;
  }
  // strict: every piece must be known (a list of words); else what is known is translated and the rest is listed
  function pieces(core, sep, strict) {
    var parts = core.split(sep), out = [], any = false, unknown = [];
    for (var i = 0; i < parts.length; i++) {
      if (!CYR.test(parts[i])) { out.push(parts[i]); continue; }
      var t = lookup(parts[i].trim());
      if (t === undefined) { if (strict) return undefined; unknown.push(parts[i].trim()); out.push(parts[i]); continue; }
      out.push(parts[i].replace(parts[i].trim(), t)); any = true;
    }
    if (!any) return undefined;
    unknown.forEach(function (u) { missing.add(u); });
    return out.join(sep);
  }
  function tr(s) {
    if (!CYR.test(s)) return s;
    var lead = /^\s*/.exec(s)[0], trail = /\s*$/.exec(s)[0];
    var core = s.slice(lead.length, s.length - trail.length);
    if (!core) return s;
    var hit = lookup(core);
    if (hit === undefined && core.indexOf("\n") >= 0) hit = pieces(core, "\n");
    if (hit === undefined && core.indexOf(", ") >= 0) hit = pieces(core, ", ", true);
    if (hit === undefined) { missing.add(core); return s; }
    return lead + hit + trail;
  }
  window.__tr = tr;

  var ATTRS = ["title", "placeholder", "aria-label", "alt", "data-tip", "label"];
  function skip(el) { return el.nodeType === 1 && (el.tagName === "SCRIPT" || el.tagName === "STYLE" || el.tagName === "TEXTAREA" || el.hasAttribute("data-notr") || el.classList.contains("nm")); }
  function attrs(el) {
    for (var i = 0; i < ATTRS.length; i++) {
      var v = el.getAttribute(ATTRS[i]);
      if (v && CYR.test(v)) { var t = tr(v); if (t !== v) el.setAttribute(ATTRS[i], t); }
    }
  }
  function walk(node) {
    if (node.nodeType === 3) { if (CYR.test(node.data) && !(node.parentNode && skip(node.parentNode))) { var t = tr(node.data); if (t !== node.data) node.data = t; } return; }
    if (node.nodeType !== 1 && node.nodeType !== 9 && node.nodeType !== 11) return;
    if (node.nodeType === 1) { if (skip(node)) return; attrs(node); }
    for (var c = node.firstChild; c; c = c.nextSibling) walk(c);
  }
  var busy = false;
  var mo = new MutationObserver(function (records) {
    if (busy) return;
    busy = true;
    try {
      records.forEach(function (r) {
        if (r.type === "childList") r.addedNodes.forEach(walk);
        else if (r.type === "characterData") walk(r.target);
        else if (r.type === "attributes" && r.target.nodeType === 1) attrs(r.target);
      });
    } finally { mo.takeRecords(); busy = false; }
  });
  mo.observe(document, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ATTRS });
  document.addEventListener("DOMContentLoaded", function () { walk(document.documentElement); });

  // the native dialogs
  ["alert", "confirm", "prompt"].forEach(function (name) {
    var orig = window[name];
    window[name] = function (msg, def) { return orig.call(window, tr(String(msg)), def); };
  });
})();
