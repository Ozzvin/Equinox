// Runs from <head>, before the first paint: the theme and the density the user chose are put on <html> so the page
// does not flash in the default ones first. It is a file, not inline, because the Content-Security-Policy
// (default-src 'self') does not allow inline scripts.
try { var t = localStorage.getItem("uiTheme"); document.documentElement.dataset.theme = t === "light" || (t === "system" && !matchMedia("(prefers-color-scheme: dark)").matches) ? "light" : "dark"; } catch (e) { document.documentElement.dataset.theme = "dark"; }
try { var d = localStorage.getItem("uiDensity"); if (d === "large" || d === "compact") document.documentElement.dataset.density = d; } catch (e) {}
