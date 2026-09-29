"use strict";

const $ = (id) => document.getElementById(id);

// Block edge in px for the largest GPU; smaller GPUs scale by area.
const BLOCK_MAX = 64;
const BLOCK_MIN = 24;

let refreshSeconds = 10;
let lastOK = null;
let lastRendered = "";

// ---- theme --------------------------------------------------------------

const THEMES = ["system", "light", "dark"];
const THEME_ICONS = {
  system: '<svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/></svg>',
  light: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>',
  dark: '<svg viewBox="0 0 24 24"><path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"/></svg>',
};

function currentTheme() {
  return document.documentElement.dataset.theme || "system";
}

function setTheme(t) {
  if (t === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
  try {
    if (t === "system") localStorage.removeItem("gpukoll-theme");
    else localStorage.setItem("gpukoll-theme", t);
  } catch (e) {}
  const btn = $("theme");
  btn.innerHTML = THEME_ICONS[t];
  btn.title = "Theme: " + t;
  btn.setAttribute("aria-label", "Theme: " + t + " (click to change)");
}

$("theme").addEventListener("click", () => {
  const i = THEMES.indexOf(currentTheme());
  setTheme(THEMES[(i + 1) % THEMES.length]);
});
setTheme(currentTheme());

// ---- helpers ------------------------------------------------------------

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

function prettyProduct(p) {
  return (p || "").replace(/-/g, " ").replace(/\s+/g, " ").trim();
}

function gb(mib) {
  return Math.round(mib / 1024);
}

function ago(date) {
  const s = Math.max(0, Math.round((Date.now() - date.getTime()) / 1000));
  if (s < 60) return s + "s";
  const m = Math.round(s / 60);
  if (m < 60) return m + "m";
  const h = Math.round(m / 60);
  if (h < 48) return h + "h";
  return Math.round(h / 24) + "d";
}

// ---- rendering ----------------------------------------------------------

function renderSummary(sum) {
  $("s-available").textContent = sum.available;
  $("s-used").textContent = sum.used;
  $("s-total").textContent = sum.total;
  $("s-servers").textContent = sum.serversOnline + " / " + sum.servers;

  const online = sum.used + sum.available;
  $("s-used-sub").textContent = online ? Math.round((100 * sum.used) / online) + "% of online GPUs" : " ";
  const notes = [];
  if (sum.offline) notes.push(sum.offline + " on offline servers");
  if (sum.unknown) notes.push(sum.unknown + " with unknown usage");
  $("s-total-sub").textContent = notes.length ? notes.join(", ") : "all on online servers";
  $("legend-unknown").hidden = !sum.unknown;
  const down = sum.servers - sum.serversOnline;
  $("s-servers-sub").textContent = down ? down + (down === 1 ? " server down" : " servers down") : "all servers up";

  const parts = [
    ["used", sum.used],
    ["free", sum.available],
    ["unknown", sum.unknown],
    ["off", sum.offline],
  ].filter(([, n]) => n > 0);
  $("usage").innerHTML = parts.map(([cls, n]) => `<div class="${cls}" style="flex-grow:${n}"></div>`).join("");
}

function blockSize(mib, maxMiB) {
  if (!mib || !maxMiB) return Math.round(BLOCK_MAX * 0.6);
  return Math.max(BLOCK_MIN, Math.round(BLOCK_MAX * Math.sqrt(mib / maxMiB)));
}

function renderBlock(g, i, server, maxMiB) {
  const size = blockSize(g.memoryMiB, maxMiB);
  const state = !server.online ? "off" : !server.usageKnown ? "unknown" : g.used ? "used" : "free";
  const stateText = { off: "server offline", unknown: "usage unknown", used: "in use", free: "available" }[state];
  const mem = g.memoryMiB ? gb(g.memoryMiB) + " GB" : "memory unknown";
  let label = "";
  if (g.memoryMiB && size >= 40) label = `<span>${gb(g.memoryMiB)}<small>GB</small></span>`;
  else if (g.memoryMiB && size >= 26) label = String(gb(g.memoryMiB));
  const cls = ["gpu", state === "free" ? "" : state, g.mig ? "mig" : ""].join(" ").trim();
  return `<div class="${cls}" style="width:${size}px;height:${size}px" role="img"
    aria-label="${esc(`${g.mig ? "MIG device" : "GPU"} ${i}, ${prettyProduct(g.product)}, ${mem}, ${stateText}`)}"
    data-title="${esc(`${g.mig ? "MIG" : "GPU"} ${i} · ${stateText}`)}"
    data-body="${esc(`${prettyProduct(g.product) || "Unknown GPU"} · ${mem}`)}">${label}</div>`;
}

function serverMeta(s) {
  const parts = [];
  const full = s.gpus.filter((g) => !g.mig);
  const mig = s.gpus.length - full.length;
  if (full.length) {
    let p = `${full.length}× ${prettyProduct(s.product) || "GPU"}`;
    if (full[0].memoryMiB) p += ` · ${gb(full[0].memoryMiB)} GB`;
    parts.push(p);
  }
  if (mig) parts.push(`${mig} MIG devices`);
  if (s.driver) parts.push("driver " + s.driver);
  return parts.join(" · ");
}

function renderServer(s, maxMiB) {
  const since = s.since ? ` for ${ago(new Date(s.since))}` : "";
  const pill = s.online
    ? `<span class="pill on"><i></i>Online</span>`
    : `<span class="pill down" title="${esc(s.status + since)}"><i></i>Offline</span>`;
  let count;
  if (!s.online) count = `${s.total} GPUs unavailable · ${esc(s.status)}${esc(since)}`;
  else if (!s.usageKnown)
    count = `${s.total} GPUs · usage unknown<div class="server-error" title="${esc(s.usageError)}">${esc(s.usageError)}</div>`;
  else count = `<b>${s.used}</b> of ${s.total} in use · <b>${s.total - s.used}</b> available`;
  const blocks = s.gpus.map((g, i) => renderBlock(g, i, s, maxMiB)).join("");
  return `<article class="server${s.online ? "" : " offline"}">
    <div class="server-head">
      <div>
        <h2 class="server-name">${esc(s.name)}</h2>
        <div class="server-meta">${esc(serverMeta(s))}</div>
      </div>
      ${pill}
    </div>
    <div class="server-count">${count}</div>
    <div class="blocks">${blocks}</div>
  </article>`;
}

function render(data) {
  renderSummary(data.summary);
  const key = JSON.stringify(data.servers);
  if (key === lastRendered) return;
  lastRendered = key;
  hideTip();

  if (!data.servers.length) {
    $("servers").innerHTML = `<div class="empty">No GPU servers found.<br>
      <span class="muted">gpukoll looks for nodes labelled <code>nvidia.com/gpu.present=true</code> or with <code>nvidia.com/gpu</code> capacity.</span></div>`;
    return;
  }
  const maxMiB = Math.max(0, ...data.servers.flatMap((s) => s.gpus.map((g) => g.memoryMiB || 0)));
  $("servers").innerHTML = data.servers.map((s) => renderServer(s, maxMiB)).join("");
}

function showError(msg) {
  const el = $("error");
  el.hidden = !msg;
  el.textContent = msg || "";
}

function tickUpdated() {
  $("updated").textContent = lastOK ? "Updated " + ago(lastOK) + " ago" : "";
}

// ---- tooltip ------------------------------------------------------------

const tip = $("tip");

function hideTip() {
  tip.hidden = true;
}

document.addEventListener("mouseover", (e) => {
  const el = e.target.closest(".gpu");
  if (!el) return hideTip();
  tip.innerHTML = `<b>${esc(el.dataset.title)}</b>${esc(el.dataset.body)}`;
  tip.hidden = false;
  const r = el.getBoundingClientRect();
  const t = tip.getBoundingClientRect();
  let x = r.left + r.width / 2 - t.width / 2;
  x = Math.max(8, Math.min(x, window.innerWidth - t.width - 8));
  let y = r.top - t.height - 8;
  if (y < 8) y = r.bottom + 8;
  tip.style.left = x + "px";
  tip.style.top = y + "px";
});
window.addEventListener("scroll", hideTip, { passive: true });

// ---- polling ------------------------------------------------------------

let timer = null;

async function refresh() {
  clearTimeout(timer);
  try {
    const res = await fetch("api/gpus", { cache: "no-store" });
    const data = await res.json();
    if (data.refreshSeconds) refreshSeconds = data.refreshSeconds;
    if (data.summary) {
      render(data);
      lastOK = new Date(data.updatedAt);
      showError(data.error ? "Showing last known state. Cluster poll failed: " + data.error : "");
    } else {
      showError("No data yet: " + (data.error || res.statusText));
    }
  } catch (err) {
    showError("Cannot reach gpukoll: " + err.message);
  }
  tickUpdated();
  timer = setTimeout(refresh, refreshSeconds * 1000);
}

document.addEventListener("visibilitychange", () => {
  if (!document.hidden) refresh();
});
setInterval(tickUpdated, 1000);
refresh();
