"use strict";

// The server decides what to show; this file only lays it out. Every string
// from the server is user content (titles, prompts, commands) and goes through
// esc() before it reaches innerHTML.

const $ = (s, el = document) => el.querySelector(s);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const validTime = (iso) => {
  if (!iso) return null;
  const d = new Date(iso);
  return isNaN(d) || d.getFullYear() < 1971 ? null : d;
};
const pad2 = (n) => String(n).padStart(2, "0");
const localDay = (d) => `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;

const fmt = {
  usd: (v) => (v == null ? "–" : v >= 100 ? "$" + Math.round(v).toLocaleString() : "$" + Number(v).toFixed(2)),
  usdAxis: (v) => "$" + (Number.isInteger(v) ? v.toLocaleString() : v.toFixed(2)),
  int: (v) => (v == null ? "–" : Math.round(v).toLocaleString()),
  tok: (v) => {
    if (v == null) return "–";
    const a = Math.abs(v);
    return a >= 1e9 ? (v / 1e9).toFixed(2) + "B" : a >= 1e6 ? (v / 1e6).toFixed(1) + "M" : a >= 1e3 ? (v / 1e3).toFixed(1) + "K" : String(Math.round(v));
  },
  pct: (v) => (v == null || !isFinite(v) ? "–" : (v * 100).toFixed(v > 0 && v < 0.1 ? 1 : 0) + "%"),
  size: (v) => (v == null ? "–" : fmt.tok(v) + "B"),
  date: (iso) => {
    const d = validTime(iso);
    return d ? localDay(d) : "";
  },
  dateTime: (iso) => {
    const d = validTime(iso);
    return d ? `${fmt.date(iso)} ${pad2(d.getHours())}:${pad2(d.getMinutes())}` : "";
  },
  time: (iso) => {
    const d = validTime(iso);
    return d ? `${pad2(d.getHours())}:${pad2(d.getMinutes())}` : "";
  },
  ago: (iso) => {
    const d = validTime(iso);
    if (!d) return "";
    const s = (Date.now() - d) / 1000;
    if (s < 60) return "just now";
    if (s < 3600) return Math.round(s / 60) + "m ago";
    if (s < 86400) return Math.round(s / 3600) + "h ago";
    return Math.round(s / 86400) + "d ago";
  },
  tilde: (p) => String(p ?? "").replace(/^\/(Users|home)\/[^/]+(?=\/|$)/, "~"),
  model: (m) => String(m ?? "").replace(/^claude-/, ""),
};

const costTotal = (c) => (c ? (c.input || 0) + (c.output || 0) + (c.cache_read || 0) + (c.cache_write || 0) : 0);
const SLOTS = ["--s1", "--s2", "--s3", "--s4", "--s5", "--s6", "--s7"];
const UNUSED_DAYS = 90;

// ---------- building blocks ----------

// card appends a card to parent and returns the element its content goes in.
function card(parent, title, opts = {}) {
  const el = document.createElement("section");
  el.className = "card" + (opts.span ? " span-" + opts.span : "") + (opts.cls ? " " + opts.cls : "");
  el.innerHTML =
    `<div class="card-head"><h2${opts.caps ? ' class="caps"' : ""}>${esc(title)}</h2>${opts.right ? `<span class="right">${opts.right}</span>` : ""}</div>` +
    (opts.note ? `<p class="note">${opts.note}</p>` : "") +
    `<div class="body"></div>`;
  parent.appendChild(el);
  return $(".body", el);
}

function errorCard(parent, title, message) {
  const body = card(parent, title, { cls: "error-card" });
  body.innerHTML = `<p class="error-text">${esc(message)}</p>`;
}

function kpis(parent, items) {
  const el = document.createElement("section");
  el.className = "kpis";
  el.innerHTML = items
    .map((k) => `<div class="kpi"${k.title ? ` title="${esc(k.title)}"` : ""}><div class="label">${esc(k.label)}</div><div class="value">${esc(k.value)}</div>${k.sub ? `<div class="sub">${esc(k.sub)}</div>` : ""}</div>`)
    .join("");
  parent.appendChild(el);
}

// Sort choices per table, kept for this visit.
const sorts = {};

// table renders rows into body in the order the server gave them until a
// header is clicked; then by that column, and a second click reverses it. Only
// a table with opts.id can be sorted; opts.sort names a column to start
// sorted by, largest first. A column is
// {key, label, num, mono, wrap, nowrap, value(row), fmt(value,row) -> text, html(row) -> html, bar, color}.
// A bar column adds a cell after it scaled to the column's largest value.
function table(body, columns, rows, opts = {}) {
  if (!rows || !rows.length) {
    body.innerHTML = `<p class="empty">${esc(opts.empty || "Nothing to show.")}</p>`;
    return;
  }
  const val = (c, r) => (c.value ? c.value(r) : r[c.key]);
  const st = (opts.id && sorts[opts.id]) || (opts.sort ? { key: opts.sort, dir: -1 } : null);
  const sortCol = st && columns.find((c) => c.key === st.key);
  let list = rows;
  if (sortCol) {
    list = [...rows].sort((a, b) => {
      const av = val(sortCol, a), bv = val(sortCol, b);
      const cmp = typeof av === "number" || typeof bv === "number" ? (+av || 0) - (+bv || 0) : String(av ?? "").localeCompare(String(bv ?? ""));
      return cmp * st.dir;
    });
  }
  const shown = opts.limit ? list.slice(0, opts.limit) : list;
  const maxes = {};
  for (const c of columns) if (c.bar) maxes[c.key] = Math.max(0, ...rows.map((r) => +val(c, r) || 0)) || 1;
  const head = columns
    .map((c) => {
      const cls = [c.num && "num", opts.id && "sortable"].filter(Boolean).join(" ");
      const arrow = sortCol === c ? (st.dir < 0 ? " ↓" : " ↑") : "";
      const aria = sortCol === c ? ` aria-sort="${st.dir < 0 ? "descending" : "ascending"}"` : "";
      return `<th${cls ? ` class="${cls}"` : ""}${opts.id ? ` data-k="${esc(c.key)}"` : ""}${aria}>${esc(c.label)}${arrow}</th>` + (c.bar ? `<th class="bar-col" aria-hidden="true"></th>` : "");
    })
    .join("");
  const rowsHtml = shown
    .map(
      (r) =>
        "<tr>" +
        columns
          .map((c) => {
            const v = val(c, r);
            const content = c.html ? c.html(r) : c.fmt ? esc(c.fmt(v, r)) : esc(v);
            const cls = [c.num && "num", c.mono && "mono", c.wrap && "wrap", c.nowrap && "nowrap"].filter(Boolean).join(" ");
            let cell = `<td${cls ? ` class="${cls}"` : ""}>${content}</td>`;
            if (c.bar) {
              const w = Math.max(0, Math.min(100, ((+v || 0) / maxes[c.key]) * 100));
              cell += `<td class="bar-cell"><span class="bar" data-w="${w.toFixed(1)}" data-c="${esc(c.color || "--s1")}"></span></td>`;
            }
            return cell;
          })
          .join("") +
        "</tr>"
    )
    .join("");
  body.innerHTML =
    `<div class="table-wrap"><table><thead><tr>${head}</tr></thead><tbody>${rowsHtml}</tbody></table></div>` +
    (shown.length < list.length ? `<p class="foot">Showing ${esc(fmt.int(shown.length))} of ${esc(fmt.int(list.length))}.</p>` : "");
  hydrate(body);
  if (!opts.id) return;
  for (const th of body.querySelectorAll("th[data-k]")) {
    th.addEventListener("click", () => {
      const c = columns.find((x) => x.key === th.dataset.k);
      sorts[opts.id] = { key: c.key, dir: sortCol === c ? -st.dir : c.num ? -1 : 1 };
      table(body, columns, rows, opts);
    });
  }
}

// hydrate applies the widths and colours carried in data attributes, so no
// markup needs an inline style.
function hydrate(el) {
  for (const b of el.querySelectorAll("[data-w]")) b.style.width = b.dataset.w + "%";
  for (const b of el.querySelectorAll("[data-c]")) b.style.setProperty("--c", `var(${b.dataset.c})`);
  for (const b of el.querySelectorAll("[data-fill]")) b.style.fill = `var(${b.dataset.fill})`;
}

function kv(pairs) {
  return `<dl class="kv">${pairs.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`).join("")}</dl>`;
}

function select(param, value, options, label) {
  const opts = options.map(([v, l]) => `<option value="${esc(v)}"${String(v) === String(value) ? " selected" : ""}>${esc(l)}</option>`).join("");
  return `<label>${esc(label)} <select data-param="${esc(param)}">${opts}</select></label>`;
}

const legend = (items) => `<div class="legend">${items.map((i) => `<span data-c="${esc(i.color)}">${esc(i.label)}</span>`).join("")}</div>`;
const swatch = (color, label) => `<span class="legend inline"><span data-c="${esc(color)}">${esc(label)}</span></span>`;
const pill = (text) => `<span class="pill">${esc(text)}</span>`;

const RANGES = [["7", "Last 7 days"], ["30", "Last 30 days"], ["90", "Last 90 days"], ["180", "Last 180 days"], ["0", "All time"]];
const sevChip = (s) => `<span class="chip sev-${esc(s)}">${esc(s)}</span>`;

// ---------- tooltip ----------

const tip = $("#tooltip");
function showTip(html, ev) {
  tip.innerHTML = html;
  hydrate(tip);
  tip.hidden = false;
  const pad = 14;
  const r = tip.getBoundingClientRect();
  let x = ev.clientX + pad, y = ev.clientY + pad;
  if (x + r.width > innerWidth - 8) x = Math.max(8, ev.clientX - r.width - pad);
  if (y + r.height > innerHeight - 8) y = Math.max(8, ev.clientY - r.height - pad);
  tip.style.left = x + "px";
  tip.style.top = y + "px";
}
const hideTip = () => {
  tip.hidden = true;
};
const tipRow = (label, value, color) => `<div class="row"><span>${color ? `<i class="sw" data-c="${esc(color)}"></i>` : ""}${esc(label)}</span><b>${esc(value)}</b></div>`;

// ---------- charts ----------

// niceTicks picks about n round steps up to max; whole keeps them integers,
// for counts.
function niceTicks(max, n, whole) {
  const step0 = max / n;
  const p = Math.pow(10, Math.floor(Math.log10(step0)));
  let step = [1, 2, 2.5, 5, 10].map((k) => k * p).find((s) => s >= step0) || p * 10;
  if (whole) step = Math.max(1, Math.ceil(step));
  const out = [];
  for (let v = 0; v <= max + step * 0.999; v += step) out.push(+v.toPrecision(12));
  return out.length > 1 ? out : [0, step];
}

// perBucket sums pick(day) -> {series: value} over the server's days, or over
// weeks starting Monday once the range is longer than a quarter.
function perBucket(daily, pick) {
  const weekly = daily.length > 92;
  const buckets = [];
  const values = new Map();
  for (const r of daily) {
    let b = r.day;
    if (weekly) {
      const d = new Date(r.day + "T12:00:00");
      d.setDate(d.getDate() - ((d.getDay() + 6) % 7));
      b = localDay(d);
    }
    if (!values.has(b)) {
      values.set(b, {});
      buckets.push(b);
    }
    const o = values.get(b);
    for (const [k, v] of Object.entries(pick(r))) o[k] = (o[k] || 0) + (v || 0);
  }
  return { weekly, buckets, values };
}

// stackedColumns draws a column per bucket with the series stacked bottom-up
// in the order given. series: [{key, label, color}].
function stackedColumns(body, { weekly, buckets, values }, series, valFmt, opts = {}) {
  const W = Math.max(280, Math.floor(body.clientWidth) || 800), H = opts.height || 220;
  const m = { l: 56, r: 8, t: 8, b: 24 };
  const iw = W - m.l - m.r, ih = H - m.t - m.b;
  const totals = buckets.map((b) => series.reduce((s, x) => s + ((values.get(b) || {})[x.key] || 0), 0));
  const ticks = niceTicks(Math.max(...totals, 0) || 1, 4, opts.whole);
  const top = ticks[ticks.length - 1];
  const bw = iw / Math.max(buckets.length, 1);
  const barW = Math.max(1, Math.min(28, bw - 2));
  const y = (v) => m.t + ih - (v / top) * ih;
  const n = (v) => +v.toFixed(1);
  let svg = `<svg class="chart" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" role="img" aria-label="${esc(opts.label || "chart")}">`;
  for (const t of ticks) svg += `<line class="gl" x1="${m.l}" x2="${W - m.r}" y1="${n(y(t))}" y2="${n(y(t))}"/><text x="${m.l - 6}" y="${n(y(t) + 4)}" text-anchor="end">${esc(valFmt(t))}</text>`;
  const every = Math.ceil(buckets.length / Math.max(1, Math.floor(iw / 70)));
  buckets.forEach((b, i) => {
    const x = m.l + i * bw + (bw - barW) / 2;
    let acc = 0;
    const segs = series.map((s) => ({ s, v: (values.get(b) || {})[s.key] || 0 })).filter((sg) => sg.v > 0);
    segs.forEach((sg, j) => {
      const y1 = y(acc + sg.v);
      const h = Math.max(0, y(acc) - y1 - (j > 0 ? 2 : 0));
      const r = j === segs.length - 1 ? Math.min(4, barW / 2, h) : 0;
      const yb = y1 + h;
      svg += r
        ? `<path d="M${n(x)},${n(yb)} V${n(y1 + r)} Q${n(x)},${n(y1)} ${n(x + r)},${n(y1)} H${n(x + barW - r)} Q${n(x + barW)},${n(y1)} ${n(x + barW)},${n(y1 + r)} V${n(yb)} Z" data-fill="${esc(sg.s.color)}"/>`
        : `<rect x="${n(x)}" y="${n(y1)}" width="${n(barW)}" height="${n(h)}" data-fill="${esc(sg.s.color)}"/>`;
      acc += sg.v;
    });
    svg += `<rect class="hit" data-i="${i}" x="${n(m.l + i * bw)}" y="${m.t}" width="${n(bw)}" height="${ih}" fill="transparent"/>`;
    if (i % every === 0) svg += `<text x="${n(m.l + i * bw + bw / 2)}" y="${H - 6}" text-anchor="middle">${esc(b.slice(5))}</text>`;
  });
  svg += `<line class="ax" x1="${m.l}" x2="${W - m.r}" y1="${n(y(0))}" y2="${n(y(0))}"/></svg>`;
  body.innerHTML = (series.length > 1 ? legend(series) : "") + svg;
  hydrate(body);
  for (const h of body.querySelectorAll(".hit")) {
    h.addEventListener("mousemove", (ev) => {
      const i = +h.dataset.i, b = buckets[i], v = values.get(b) || {};
      const rows = series
        .filter((s) => v[s.key])
        .reverse()
        .map((s) => tipRow(s.label, valFmt(v[s.key]), s.color))
        .join("");
      showTip(`<b>${esc(weekly ? "Week of " + b : b)}</b>${rows}<div class="row muted"><span>Total</span><span>${esc(valFmt(totals[i]))}</span></div>`, ev);
      h.setAttribute("fill", "rgba(127,127,127,.08)");
    });
    h.addEventListener("mouseleave", () => {
      hideTip();
      h.setAttribute("fill", "transparent");
    });
  }
}

const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

// heatmap draws a 7×24 grid, Monday first, in six shades of the busiest cell.
function heatmap(body, grid) {
  const max = Math.max(1, ...grid.flat());
  const W = Math.max(280, Math.floor(body.clientWidth) || 700), lx = 34, cw = (W - lx) / 24, ch = 22;
  const H = ch * 7 + 22;
  const step = (v) => (v === 0 ? 0 : Math.min(6, 1 + Math.floor((v / max) * 5.999)));
  let svg = `<svg class="chart" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" role="img" aria-label="Prompts by weekday and hour">`;
  grid.forEach((row, d) => {
    svg += `<text x="0" y="${d * ch + 15}">${WEEKDAYS[d]}</text>`;
    row.forEach((v, h) => {
      svg += `<rect class="cell" data-d="${d}" data-h="${h}" x="${(lx + h * cw).toFixed(1)}" y="${d * ch}" width="${Math.max(1, cw - 2).toFixed(1)}" height="${ch - 2}" rx="3" data-fill="--seq-${step(v)}"/>`;
    });
  });
  for (let h = 0; h < 24; h += 3) svg += `<text x="${(lx + h * cw + cw / 2).toFixed(1)}" y="${ch * 7 + 14}" text-anchor="middle">${pad2(h)}</text>`;
  body.innerHTML = svg + "</svg>";
  hydrate(body);
  for (const c of body.querySelectorAll(".cell")) {
    c.addEventListener("mousemove", (ev) => {
      const d = +c.dataset.d, h = +c.dataset.h;
      showTip(`<b>${WEEKDAYS[d]} ${pad2(h)}:00</b>${tipRow("Prompts", fmt.int(grid[d][h]))}`, ev);
    });
    c.addEventListener("mouseleave", hideTip);
  }
}

// ---------- sections ----------

// Remembered across pages for this visit only: the range picked on Usage
// carries to Health, the accounts seen so far stay in the account picker, and
// the inventory filters survive a tab switch.
const memory = { days: "30", accounts: new Set(), inv: { unused: false, kind: "", q: "" } };
const rangeParam = (p) => p.get("days") ?? memory.days;

// SECTIONS is the sidebar, in order; a section's views are its tabs, and a
// section with one view shows none. A section is
// {id, label, endpoint, query(params) -> {k: v}, toolbar(params, data) -> html, views: [{id, label, badge(data), render(data, el, params)}]}.
// Every view of a section draws from the same response, so switching tabs
// fetches nothing. Adding a page is adding a view or a section here, plus its
// endpoint on the server.
const SECTIONS = [
  {
    id: "usage",
    label: "Usage",
    endpoint: "api/usage",
    query: (p) => ({ days: rangeParam(p), root: p.get("root") || "" }),
    toolbar: (p, data) => {
      for (const a of data?.accounts || []) if (a.name && a.name !== "unknown") memory.accounts.add(a.name);
      const root = p.get("root") || "";
      if (root) memory.accounts.add(root);
      let html = select("days", rangeParam(p), RANGES, "Range");
      if (memory.accounts.size > 1 || root) {
        html += select("root", root, [["", "All accounts"], ...[...memory.accounts].sort().map((a) => [a, fmt.tilde(a)])], "Account");
      }
      return html;
    },
    views: [
      { id: "overview", label: "Overview", render: usageOverview },
      { id: "recommendations", label: "Recommendations", badge: (d) => (d.findings || []).length, render: usageRecommendations },
      { id: "tokens", label: "Tokens", render: usageTokens },
      { id: "tools", label: "Tools & skills", render: usageTools },
      { id: "inventory", label: "Inventory", render: usageInventory },
      { id: "workflow", label: "Workflow", render: usageWorkflow },
    ],
  },
  {
    id: "overview",
    label: "Brief",
    endpoint: "api/brief",
    query: (p) => ({ all: p.get("all") === "1" ? "1" : "" }),
    toolbar: (p) => `<label><input type="checkbox" data-param="all" value="1"${p.get("all") === "1" ? " checked" : ""}> Show all</label>`,
    views: [{ id: "brief", label: "Brief", render: renderOverview }],
  },
  {
    id: "setup",
    label: "Setup",
    endpoint: "api/setup",
    views: [{ id: "setup", label: "Setup", render: renderSetup }],
  },
  {
    id: "health",
    label: "Health",
    endpoint: "api/health",
    query: (p) => ({ days: rangeParam(p) }),
    toolbar: (p) => select("days", rangeParam(p), RANGES, "Range"),
    views: [{ id: "health", label: "Health", render: renderHealth }],
  },
];

// ---------- usage ----------

// noUsage explains an empty range, and tells the caller there is nothing
// else to draw.
function noUsage(d, el) {
  if (d.sessions) return false;
  const body = card(el, "No usage in this range");
  body.innerHTML = d.hint
    ? `<p class="empty">${esc(d.hint)}</p>`
    : `<p class="empty">The <code>Stop</code> hook records each session as you work. For sessions from before the hooks were installed, run <code>mindskein usage --backfill</code> once.</p>`;
  return true;
}

function notReported(body, what) {
  body.innerHTML = `<p class="empty">This mindskein does not report ${esc(what)} yet. Update it and restart <code>mindskein serve</code>.</p>`;
}

const multiRoot = (d) => new Set((d.inventory || []).map((i) => i.root)).size > 1 || (d.accounts || []).length > 1;

// modelSeries gives the seven costliest models in the range a colour each and
// folds the rest into Other.
function modelSeries(d) {
  const ranked = [...(d.models || [])].sort((a, b) => (b.cost || 0) - (a.cost || 0)).map((m) => m.name);
  const top = new Map(ranked.slice(0, SLOTS.length).map((name, i) => [name, SLOTS[i]]));
  const keyOf = (name) => (top.has(name) ? name : "Other");
  const colorOf = (key) => top.get(key) || "--s8";
  return { top, keyOf, colorOf };
}

const spendCols = (label, nameFmt = (v) => v, nameHtml) => [
  { key: "name", label, fmt: nameFmt, html: nameHtml, wrap: true },
  { key: "cost", label: "Cost", num: true, fmt: fmt.usd, bar: true },
  { key: "share", label: "Share", num: true, fmt: fmt.pct },
  { key: "turns", label: "Turns", num: true, fmt: fmt.int },
  { key: "output", label: "Output", num: true, fmt: fmt.tok },
];

function usageOverview(d, el) {
  if (noUsage(d, el)) return;
  const total = costTotal(d.cost);
  const t = d.tokens || {};
  const inputAll = (t.input || 0) + (t.cache_read || 0) + (t.cache_write_5m || 0) + (t.cache_write_1h || 0);
  kpis(el, [
    { label: "Sessions", value: fmt.int(d.sessions), sub: `${fmt.int(d.prompts)} prompts` },
    { label: "API-equivalent cost", value: fmt.usd(total), sub: `${fmt.usd(total / d.sessions)} / session`, title: "API list prices; on a subscription this is value consumed, not the bill" },
    { label: "Tokens processed", value: fmt.tok(inputAll + (t.output || 0)), sub: `${fmt.tok(t.output)} output` },
    { label: "Cache hit", value: fmt.pct(d.cache_hit), sub: "cache reads / all input" },
    { label: "Subagent share", value: fmt.pct(total ? d.subagent_cost / total : NaN), sub: fmt.usd(d.subagent_cost) },
    { label: "Assistant turns", value: fmt.int(t.turns), sub: `${fmt.int((t.turns || 0) / d.sessions)} / session` },
  ]);

  const ms = modelSeries(d);
  if (Array.isArray(d.daily)) {
    const buckets = perBucket(d.daily, (r) => {
      const o = {};
      for (const [name, v] of Object.entries(r.cost || {})) o[ms.keyOf(name)] = (o[ms.keyOf(name)] || 0) + v;
      return o;
    });
    const present = new Set();
    for (const o of buckets.values.values()) for (const k of Object.keys(o)) present.add(k);
    const series = [...ms.top.keys(), "Other"].filter((k) => present.has(k)).map((k) => ({ key: k, label: fmt.model(k), color: ms.colorOf(k) }));
    const body = card(el, `${buckets.weekly ? "Weekly" : "Daily"} cost by model`, { note: "API list prices. On a subscription this is the value consumed, not the bill." });
    stackedColumns(body, buckets, series, fmt.usdAxis, { label: "Cost by model" });
  } else {
    notReported(card(el, "Daily cost by model"), "daily figures");
  }

  table(card(el, "By workstream", { span: 6 }), spendCols("Workstream"), d.workstreams, { id: "workstreams", empty: "No workstreams in this range." });
  table(
    card(el, "By model", { span: 6 }),
    spendCols("Model", fmt.model, (r) => swatch(ms.colorOf(ms.keyOf(r.name)), fmt.model(r.name))),
    d.models,
    { id: "models", empty: "No model use in this range." }
  );
  if ((d.accounts || []).length > 1) {
    table(card(el, "By account", { span: 6 }), spendCols("Account", fmt.tilde), d.accounts, { id: "accounts" });
  }
}

function usageRecommendations(d, el) {
  const findings = d.findings || [];
  const hist = d.history_days || 0;
  const note = `“Unused” looks at the last ${UNUSED_DAYS} days${hist && hist < UNUSED_DAYS ? `, and only ${esc(fmt.int(hist))} days of history are recorded so far` : ""}.`;
  const hidden = (d.resolved || []).filter((r) => r.hidden > 0).length;
  const right = [
    findings.length ? `${findings.length} finding${findings.length === 1 ? "" : "s"}` : "",
    hidden ? `<span title="Hidden until they happen again. See them with: mindskein resolve --list">${hidden} resolved</span>` : "",
  ].filter(Boolean).join(" · ");
  const body = card(el, "Recommendations", { right, note });
  body.innerHTML = findings.length
    ? findings
        .map(
          (f) => `<article class="finding">
            <div>${sevChip(f.severity)}</div>
            <div>
              <h3>${esc(f.title)}${f.id ? ` <code class="finding-id">${esc(f.id)}</code>` : ""}</h3>
              <p>${esc(f.advice)}</p>
              ${f.saving ? `<p class="saving">Saving: ${esc(f.saving)}</p>` : ""}
              ${f.evidence && f.evidence.length ? `<details><summary>Evidence (${f.evidence.length})</summary><ul class="evidence">${f.evidence.map((e) => `<li>${esc(e)}</li>`).join("")}</ul></details>` : ""}
            </div>
          </article>`
        )
        .join("")
    : `<p class="empty">Nothing to flag in this range.</p>`;
}

function usageTokens(d, el) {
  if (noUsage(d, el)) return;
  const c = d.cost || {};
  const t = d.tokens || {};
  const total = costTotal(c);
  const mix = [
    { name: "Cache read", tokens: t.cache_read, cost: c.cache_read, color: "--s1" },
    { name: "Cache write", tokens: (t.cache_write_5m || 0) + (t.cache_write_1h || 0), cost: c.cache_write, color: "--s2" },
    { name: "Fresh input", tokens: t.input, cost: c.input, color: "--s3" },
    { name: "Output (incl. thinking)", tokens: t.output, cost: c.output, color: "--s7" },
  ];
  table(
    card(el, "Where the money goes", {
      span: 6,
      note: `Cache writes: ${esc(fmt.tok(t.cache_write_5m))} kept 5 minutes, ${esc(fmt.tok(t.cache_write_1h))} kept an hour. Thinking: ${esc(fmt.tok(t.thinking))} of ${esc(fmt.tok(t.output))} output tokens.`,
    }),
    [
      { key: "name", label: "Token type", html: (r) => swatch(r.color, r.name) },
      { key: "tokens", label: "Tokens", num: true, fmt: fmt.tok },
      { key: "share", label: "Cost share", num: true, value: (r) => (total ? (r.cost || 0) / total : 0), fmt: fmt.pct },
      { key: "cost", label: "Cost", num: true, fmt: fmt.usd, bar: true },
    ],
    mix,
    { id: "mix" }
  );

  table(
    card(el, "Main vs subagents", { span: 6, note: "Subagents inherit the parent model unless their definition pins one." }),
    [
      { key: "name", label: "Agent and model", wrap: true },
      { key: "turns", label: "Turns", num: true, fmt: fmt.int },
      { key: "runs", label: "Runs", num: true, fmt: (v) => (v ? fmt.int(v) : "") },
      { key: "per_run", label: "Per run", num: true, value: (r) => (r.runs ? r.cost / r.runs : 0), fmt: (v) => (v ? fmt.usd(v) : "") },
      { key: "share", label: "Share", num: true, fmt: fmt.pct },
      { key: "cost", label: "Cost", num: true, fmt: fmt.usd, bar: true },
    ],
    d.agents,
    { id: "agents", empty: "No model use in this range." }
  );

  table(
    card(el, "Most expensive sessions", { note: "Context is the input and cache tokens of one turn. Big turns run above 150K context." }),
    [
      { key: "start", label: "Date", fmt: fmt.date, nowrap: true },
      { key: "title", label: "Session", wrap: true, value: (r) => r.title || r.id, html: (r) => `${esc(r.title || r.id)}<div class="mono sub-id">${esc(r.project || r.repo)} · ${esc(String(r.id || "").slice(0, 8))}</div>` },
      { key: "prompts", label: "Prompts", num: true, fmt: fmt.int },
      { key: "turns", label: "Turns", num: true, fmt: fmt.int },
      { key: "context_max", label: "Max context", num: true, fmt: fmt.tok },
      { key: "big", label: "Big turns", num: true, value: (r) => (r.turns ? (r.context_big || 0) / r.turns : 0), fmt: fmt.pct },
      { key: "subagent_cost", label: "Subagents", num: true, fmt: fmt.usd },
      { key: "cost", label: "Cost", num: true, fmt: fmt.usd, bar: true },
    ],
    d.top_sessions,
    { id: "sessions", empty: "No sessions in this range." }
  );

  const big = card(el, "Largest tool results", { note: "Each lands in context and is re-read from cache on every later turn." });
  if (!Array.isArray(d.big_results)) notReported(big, "the largest tool results");
  else
    table(
      big,
      [
        { key: "day", label: "Date", nowrap: true },
        { key: "tool", label: "Tool", mono: true, nowrap: true },
        { key: "input", label: "Input", mono: true, wrap: true, fmt: fmt.tilde },
        { key: "bytes", label: "Size", num: true, fmt: fmt.size, bar: true, color: "--s2" },
      ],
      d.big_results,
      { id: "big", empty: "No large tool results in this range." }
    );
}

const USE_KINDS = [
  ["skill", "Skills"],
  ["command", "Slash commands"],
  ["agent", "Subagents"],
  ["mcp", "MCP servers"],
  ["bash", "Bash commands"],
];

function usageTools(d, el) {
  if (noUsage(d, el)) return;
  table(
    card(el, "Tools", { note: "Result volume is what the tool pushed into context." }),
    [
      { key: "name", label: "Tool", mono: true, wrap: true },
      { key: "calls", label: "Calls", num: true, fmt: fmt.int, bar: true },
      { key: "err", label: "Error rate", num: true, value: (r) => (r.calls ? (r.errors || 0) / r.calls : 0), fmt: fmt.pct },
      { key: "avg", label: "Avg result", num: true, value: (r) => (r.calls ? (r.result_bytes || 0) / r.calls : 0), fmt: fmt.size },
      { key: "result_bytes", label: "Result volume", num: true, fmt: fmt.size, bar: true, color: "--s2" },
    ],
    d.tools,
    { id: "tools", limit: 60, empty: "No tool calls in this range." }
  );

  const multi = multiRoot(d);
  for (const [kind, label] of USE_KINDS) {
    const body = card(el, label, { span: 4 });
    if (!Array.isArray(d.uses)) {
      notReported(body, "uses per skill, command, agent and MCP server");
      continue;
    }
    table(
      body,
      [
        { key: "name", label: "Name", mono: true, wrap: true, html: multi ? (r) => `${esc(r.name)}<div class="sub-id">${esc(fmt.tilde(r.root))}</div>` : null },
        { key: "count", label: "Uses", num: true, fmt: fmt.int, bar: true },
      ],
      d.uses.filter((u) => u.kind === kind),
      { id: "uses-" + kind, limit: 40, empty: "None in this range." }
    );
  }
}

function usageInventory(d, el) {
  const inv = d.inventory || [];
  const multi = multiRoot(d);
  const items = inv.flatMap((i) => i.items || []);
  const hist = d.history_days || 0;
  const reported = items.every((i) => "uses_90d" in i);
  const body = card(el, "Installed skills, agents and commands", {
    note:
      `Unused means no use in the last ${UNUSED_DAYS} days${hist && hist < UNUSED_DAYS ? ` (only ${esc(fmt.int(hist))} days are recorded so far)` : ""}. ` +
      "Listing tokens are what a skill's name and description add to the list loaded into every session.",
  });
  const f = memory.inv;
  body.innerHTML = `<div class="controls">
      <label><input type="checkbox" data-f="unused"${f.unused ? " checked" : ""}> Unused only</label>
      <select data-f="kind" aria-label="Kind">${[["", "All kinds"], ["skill", "Skills"], ["agent", "Agents"], ["command", "Commands"]]
        .map(([v, l]) => `<option value="${v}"${f.kind === v ? " selected" : ""}>${l}</option>`)
        .join("")}</select>
      <input type="search" data-f="q" placeholder="Filter…" aria-label="Filter by name or source" value="${esc(f.q)}">
    </div><div class="inv-table"></div>`;
  const out = $(".inv-table", body);
  const cols = [
    { key: "name", label: "Name", mono: true, wrap: true },
    { key: "kind", label: "Kind" },
    { key: "source", label: "Source", html: (r) => pill(r.source) + (r.plugin_enabled === false ? " " + pill("disabled") : "") },
    ...(multi ? [{ key: "root", label: "Account", fmt: fmt.tilde, nowrap: true }] : []),
    { key: "model", label: "Model", fmt: fmt.model },
    { key: "uses_90d", label: `Uses ${UNUSED_DAYS}d`, num: true, fmt: fmt.int },
    { key: "listing_tokens", label: "Listing tok", num: true, fmt: (v, r) => (r.kind === "agent" ? "–" : fmt.int(v)) },
  ];
  const draw = () => {
    const q = f.q.toLowerCase();
    const rows = items.filter((i) => (!f.unused || i.uses_90d === 0) && (!f.kind || i.kind === f.kind) && (!q || (i.name + " " + i.source).toLowerCase().includes(q)));
    table(out, cols, rows, { id: "inventory", sort: reported ? "uses_90d" : null, limit: 400, empty: items.length ? "No installed item matches." : "No skills, agents or commands installed." });
  };
  body.querySelector(".controls").addEventListener("input", (ev) => {
    const t = ev.target;
    if (t.dataset.f === "unused") f.unused = t.checked;
    else if (t.dataset.f) f[t.dataset.f] = t.value;
    draw();
  });
  draw();

  table(
    card(el, "MCP servers", { span: 6, note: "claude.ai connectors are not in local config; they show under Tools & skills once used." }),
    [
      { key: "name", label: "Server", mono: true, wrap: true },
      { key: "scopes", label: "Scope", wrap: true, value: (r) => (r.scopes || []).join(", ") },
      ...(multi ? [{ key: "root", label: "Account", fmt: fmt.tilde, nowrap: true }] : []),
      { key: "uses_90d", label: `Calls ${UNUSED_DAYS}d`, num: true, fmt: fmt.int },
    ],
    inv.flatMap((i) => i.mcp || []),
    { id: "mcp", empty: "No MCP servers configured." }
  );

  const plugins = new Map();
  for (const it of items) {
    if (!String(it.source || "").startsWith("plugin:")) continue;
    const k = it.root + "|" + it.source;
    const p = plugins.get(k) || { name: it.source.slice(7), root: it.root, enabled: it.plugin_enabled !== false, items: 0, listing_tokens: 0, uses_90d: 0 };
    p.items++;
    if (it.kind !== "agent") p.listing_tokens += it.listing_tokens || 0;
    p.uses_90d += it.uses_90d || 0;
    plugins.set(k, p);
  }
  table(
    card(el, "Plugins", { span: 6 }),
    [
      { key: "name", label: "Plugin", mono: true, wrap: true },
      ...(multi ? [{ key: "root", label: "Account", fmt: fmt.tilde, nowrap: true }] : []),
      { key: "enabled", label: "Enabled", fmt: (v) => (v ? "yes" : "no") },
      { key: "items", label: "Items", num: true, fmt: fmt.int },
      { key: "listing_tokens", label: "Listing tok", num: true, fmt: fmt.int },
      { key: "uses_90d", label: `Uses ${UNUSED_DAYS}d`, num: true, fmt: fmt.int },
    ],
    [...plugins.values()],
    { id: "plugins", sort: "listing_tokens", empty: "No plugin adds skills, agents or commands." }
  );
}

const FRICTION = [
  { key: "interrupts", label: "Interrupts", color: "--s1" },
  { key: "denials", label: "Denials", color: "--s2" },
  { key: "compactions", label: "Compactions", color: "--s3" },
  { key: "api_errors", label: "API errors", color: "--s4" },
];

function usageWorkflow(d, el) {
  if (noUsage(d, el)) return;
  const heat = card(el, "When you prompt", { span: 8, note: "Prompts per weekday and hour, local time." });
  const grid = d.prompt_hours;
  if (!Array.isArray(grid) || grid.length !== 7) notReported(heat, "prompts by hour");
  else if (!grid.flat().some((v) => v > 0))
    heat.innerHTML = `<p class="empty">No prompt in this range has a recorded hour yet. Sessions read by an older mindskein carry none; <code>mindskein usage --backfill</code> re-reads the transcripts still on disk.</p>`;
  else heatmap(heat, grid);

  const ev = d.events || {};
  const rows = [
    ["Prompts per session", (d.prompts / d.sessions).toFixed(1)],
    ["Interrupts", fmt.int(ev.interrupts || 0)],
    ["Permission denials", fmt.int(ev.denials || 0)],
    ["Compactions (auto)", fmt.int(ev.compact_auto || 0)],
    ["Compactions (manual)", fmt.int(ev.compact_manual || 0)],
    ["API errors", fmt.int(ev.api_errors || 0)],
    ["Hook errors", fmt.int(ev.hook_errors || 0)],
  ];
  card(el, "Friction", { span: 4 }).innerHTML = `<table class="plain"><tbody>${rows.map(([k, v]) => `<tr><td>${esc(k)}</td><td class="num">${esc(v)}</td></tr>`).join("")}</tbody></table>`;

  if (!Array.isArray(d.daily)) {
    notReported(card(el, "Friction per day"), "daily figures");
    return;
  }
  const buckets = perBucket(d.daily, (r) => {
    const e = r.events || {};
    return { interrupts: e.interrupts, denials: e.denials, compactions: (e.compact_auto || 0) + (e.compact_manual || 0), api_errors: e.api_errors };
  });
  const body = card(el, `Friction per ${buckets.weekly ? "week" : "day"}`);
  if (![...buckets.values.values()].some((o) => Object.values(o).some((v) => v > 0))) body.innerHTML = `<p class="empty">No friction in this range.</p>`;
  else stackedColumns(body, buckets, FRICTION, fmt.int, { label: "Friction events", whole: true });
}

// ---------- overview ----------

function sectionShell(el, s, fallbackTitle, span) {
  const body = card(el, s?.heading || fallbackTitle, { caps: true, span });
  if (!s) {
    body.innerHTML = `<p class="empty">Not reported.</p>`;
    return null;
  }
  if (s.error) {
    body.innerHTML = `<p class="error-text">${esc(s.error)}</p>`;
    return null;
  }
  if (!(s.rows || []).length) {
    body.innerHTML = `<p class="empty">${esc(s.hint || "Nothing to show.")}</p>`;
    return null;
  }
  return body;
}

const stateClass = (r) => (/stale/i.test(r.status || "") ? "st-stale" : "st-" + String(r.state || "").replace(/[^a-z]/gi, ""));

function renderOverview(d, el, p) {
  const all = p.get("all") === "1";
  const showAll = `<a href="#/overview?all=1">Show all</a>`;

  const pr = d.priorities;
  const prBody = sectionShell(el, pr, "PRIORITIES", 6);
  if (prBody) {
    const groups = [];
    for (const r of pr.rows) {
      const last = groups[groups.length - 1];
      if (last && last.level === r.level) last.rows.push(r);
      else groups.push({ level: r.level, rows: [r] });
    }
    prBody.innerHTML =
      groups
        .map(
          (g) => `<div class="prio-group">
            <span class="lvl lvl-${esc(String(g.level || "").replace(/\D/g, ""))}">${esc(g.level)}</span>
            <div class="prio-items">${g.rows
              .map((r) => `<div class="prio-item"><span class="label">${esc(r.label)}</span>${r.note ? ` <span class="note-text">— ${esc(r.note)}</span>` : ""}</div>`)
              .join("")}</div>
          </div>`
        )
        .join("") + (pr.hidden > 0 && !all ? `<p class="foot">${esc(fmt.int(pr.hidden))} more in the backlog · ${showAll}</p>` : "");
  }

  const ho = d.handoffs;
  const hoBody = sectionShell(el, ho, "WHERE WE LEFT OFF", 6);
  if (hoBody) {
    hoBody.innerHTML =
      ho.rows
        .map(
          (r) => `<div class="handoff">
            <div class="handoff-head"><span class="label">${esc(r.label)}</span><span class="when" title="${esc(fmt.dateTime(r.ended_at))}">${esc(r.when)}</span></div>
            ${r.message ? `<p class="message">${esc(r.message)}</p>` : ""}
          </div>`
        )
        .join("") + (ho.hidden > 0 && !all ? `<p class="foot">${esc(fmt.int(ho.hidden))} more · ${showAll}</p>` : "");
  }

  const ss = d.sessions;
  const ssBody = sectionShell(el, ss, "LIVE SESSIONS");
  if (ssBody) {
    table(
      ssBody,
      [
        { key: "short_id", label: "ID", mono: true, nowrap: true },
        { key: "label", label: "Session", wrap: true },
        { key: "project", label: "Folder", wrap: true },
        { key: "status", label: "Status", html: (r) => `<span class="chip ${esc(stateClass(r))}">${esc(r.status || r.state)}</span>` },
        { key: "age", label: "Quiet for", num: true, html: (r) => `<span title="${esc(fmt.dateTime(r.last_event_at))}">${esc(r.age)}</span>` },
        { key: "event", label: "Last event", mono: true, nowrap: true },
      ],
      ss.rows
    );
  }
  if (ss && !ss.error) {
    const parts = [`${fmt.int((ss.rows || []).length)} shown`, `${fmt.int(ss.running || 0)} running`];
    if (ss.interrupted) parts.push(`<span class="bad">${esc(fmt.int(ss.interrupted))} interrupted</span>`);
    if (ss.hidden) parts.push(`${esc(ss.hidden)} hidden${all ? "" : " · " + showAll}`);
    const foot = document.createElement("p");
    foot.className = "foot";
    foot.innerHTML = parts.join(" · ");
    (ssBody || el.lastElementChild.querySelector(".body")).appendChild(foot);
  }

  renderReviews(el, d.reviews);
}

// A PR's URL comes from GitHub; only a web link is made clickable, so a
// stray value can never become a javascript: link.
const prLink = (p) =>
  /^https:\/\//.test(p.url || "")
    ? `<a class="mono" href="${esc(p.url)}" target="_blank" rel="noopener noreferrer">${esc(p.ref)}</a>`
    : `<span class="mono">${esc(p.ref)}</span>`;

function renderReviews(el, rv) {
  const right = rv && rv.cached ? `cached ${esc(rv.cache_age)} ago` : "";
  const note = rv && rv.refresh_error ? `refresh failed: ${esc(rv.refresh_error)}` : "";
  const body = card(el, rv?.heading || "REVIEWS", { caps: true, right, note });
  if (!rv) {
    body.innerHTML = `<p class="empty">Not reported.</p>`;
    return;
  }
  if (rv.error) {
    body.innerHTML = `<p class="error-text">${esc(rv.error)}</p>`;
    return;
  }
  if (!(rv.counts || []).length) {
    body.innerHTML = `<p class="empty">Nothing waiting on you.</p>`;
    return;
  }
  const counts = rv.counts
    .map((c) => {
      const groups = c.groups.map((g) => `${esc(fmt.int(g.count))} ${esc(g.label)}${g.notified ? " 🔔" : ""}`).join(" · ");
      return `<div class="rv-count">${c.space ? `<span class="label">${esc(c.space)}</span>` : ""}<span>${groups}</span></div>`;
    })
    .join("");
  const top = (rv.top || []).map((p) => `<div class="rv-pick"><span class="arrow">→</span>${prLink(p)}<span class="why">${esc(p.why)}</span></div>`).join("");
  body.innerHTML = `<div class="rv-counts">${counts}</div>${top ? `<div class="rv-top">${top}</div>` : ""}`;
}

// ---------- setup ----------

function renderSetup(d, el) {
  setVersion(d.version);

  card(el, "mindskein", { span: 6 }).innerHTML = kv([
    ["Version", `<span class="mono">${esc(d.version || "unknown")}</span>`],
    ["Binary", `<span class="mono">${esc(fmt.tilde(d.binary) || "unknown")}</span>`],
    ["Last prune", d.last_prune ? `${esc(fmt.dateTime(d.last_prune))} <span class="note">(${esc(fmt.ago(d.last_prune))})</span>` : `<span class="warn">never</span>`],
  ]);

  const cfg = d.config || {};
  const ret = cfg.retention || {};
  card(el, "Config", { span: 6 }).innerHTML = kv([
    ["File", `<span class="mono">${esc(fmt.tilde(cfg.path))}</span>${cfg.exists ? "" : ` <span class="note">(not created; every setting uses its default)</span>`}`],
    ...(cfg.error ? [["Error", `<span class="bad">${esc(cfg.error)}</span>`]] : []),
    ["Priorities file", cfg.priorities_file ? `<span class="mono">${esc(fmt.tilde(cfg.priorities_file))}</span>` : `<span class="note">not set</span>`],
    ["Hide sessions after", esc(cfg.hide_after || "–")],
    ["Retention", `sessions ${esc(ret.sessions || "–")} · handoffs ${esc(ret.handoffs || "–")} · usage ${esc(ret.usage || "–")}`],
    ["Dashboard password", cfg.password_set ? `<span class="ok">set</span>` : `<span class="warn">not set</span> <span class="note">— anything that reaches this port sees titles, prompts and tool inputs</span>`],
  ]);

  const hooks = d.hooks || [];
  const hooksBody = card(el, "Hooks", { right: "registration in each Claude config" });
  hooksBody.innerHTML = hooks.length
    ? hooks
        .map((h) => {
          let status;
          if (h.error) status = `<span class="bad">✕ ${esc(h.error)}</span>`;
          else if (h.current) status = `<span class="ok">✓ registered and current</span>`;
          else status = `<span class="warn">⚠ would change</span> <span class="note">— run <code>mindskein init</code></span>`;
          const changes = !h.current && (h.changes || []).length ? `<ul class="changes">${h.changes.map((c) => `<li>${esc(c)}</li>`).join("")}</ul>` : "";
          return `<div class="root-block">
            <div><strong class="mono">${esc(fmt.tilde(h.root))}</strong></div>
            <div class="note mono">${esc(fmt.tilde(h.settings))}</div>
            <div>${status}</div>${changes}
          </div>`;
        })
        .join("")
    : `<p class="empty">No Claude config directory found.</p>`;

  table(
    card(el, "Stores", { span: 6 }),
    [
      { key: "name", label: "Store" },
      { key: "records", label: "Records", num: true, fmt: fmt.int },
      { key: "retention", label: "Kept for", nowrap: true },
      { key: "path", label: "Path", mono: true, wrap: true, fmt: fmt.tilde },
    ],
    d.stores,
    { empty: "No stores yet." }
  );

  table(
    card(el, "Recent hook failures", { span: 6, note: "From <code>hooks.log</code>, newest first. A hook that fails logs here and still lets the session continue." }),
    [
      { key: "time", label: "When", nowrap: true, fmt: fmt.dateTime },
      { key: "event", label: "Hook", mono: true, nowrap: true },
      { key: "error", label: "Error", mono: true, wrap: true },
    ],
    d.failures,
    { empty: "No failures logged." }
  );
}

// ---------- health ----------

function renderHealth(d, el) {
  table(
    card(el, "Failing hooks", { note: "Hooks of any plugin or setting that failed in sessions in this range, with how often." }),
    [
      { key: "name", label: "Hook :: command", mono: true, wrap: true },
      { key: "count", label: "Failures", num: true, fmt: fmt.int, bar: true, color: "--critical" },
    ],
    d.hook_errors,
    { empty: "No failing hooks in this range." }
  );

  const roots = d.roots || [];
  if (!roots.length) {
    card(el, "Installed setup").innerHTML = `<p class="empty">No Claude config directory found.</p>`;
    return;
  }
  for (const r of roots) {
    const where = esc(fmt.tilde(r.root));
    table(
      card(el, "Always-loaded instruction files", { span: 6, right: where, note: "Loaded into every session. ≈ tokens is characters / 4." }),
      [
        { key: "path", label: "File", mono: true, wrap: true, fmt: fmt.tilde },
        { key: "kind", label: "Kind" },
        { key: "lines", label: "Lines", num: true, fmt: fmt.int },
        { key: "tokens", label: "≈ Tokens", num: true, value: (f) => Math.round((f.chars || 0) / 4), fmt: fmt.int, bar: true, color: "--s4" },
      ],
      r.context,
      { id: "context-" + r.root, empty: "No instruction files found." }
    );
    table(
      card(el, "MCP servers", { span: 6, right: where }),
      [
        { key: "name", label: "Server", mono: true, wrap: true },
        { key: "scopes", label: "Scopes", value: (s) => (s.scopes || []).join(", ") },
      ],
      r.mcp,
      { empty: "No MCP servers configured." }
    );
    table(
      card(el, "Installed hooks", { right: where }),
      [
        { key: "event", label: "Event", nowrap: true },
        { key: "matcher", label: "Matcher", mono: true, wrap: true },
        { key: "command", label: "Command", mono: true, wrap: true },
      ],
      r.hooks,
      { id: "hooks-" + r.root, empty: "No hooks installed." }
    );
  }
}

// ---------- routing ----------

// Routes are #/<section>/<view>?k=v; a missing or unknown part falls back to
// the first.
function parseHash() {
  const raw = location.hash.replace(/^#\/?/, "");
  const q = raw.indexOf("?");
  const [sid, vid] = (q < 0 ? raw : raw.slice(0, q)).split("/");
  const params = new URLSearchParams(q < 0 ? "" : raw.slice(q + 1));
  const section = SECTIONS.find((s) => s.id === sid) || SECTIONS[0];
  const view = section.views.find((v) => v.id === vid) || section.views[0];
  return { section, view, params };
}

function hashFor(section, view, params) {
  const qs = params ? params.toString() : "";
  return "#/" + section.id + (section.views.length > 1 && view ? "/" + view.id : "") + (qs ? "?" + qs : "");
}

function queryString(section, params) {
  const q = section.query ? section.query(params) : {};
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) if (v !== "" && v != null) sp.set(k, v);
  const s = sp.toString();
  return s ? "?" + s : "";
}

async function fetchJSON(url) {
  let res;
  try {
    res = await fetch(url, { headers: { Accept: "application/json" }, cache: "no-store" });
  } catch (e) {
    throw new Error("Could not reach mindskein serve: " + (e && e.message ? e.message : e));
  }
  let body = null;
  try {
    body = await res.json();
  } catch {
    body = null;
  }
  if (!res.ok) throw new Error((body && body.error) || `${res.status} ${res.statusText}`.trim());
  if (body == null) throw new Error("The server answered with something other than JSON.");
  return body;
}

// The tab last opened in each section, so the sidebar returns to it.
const lastView = {};

// keepScroll redraws a row that scrolls sideways on a phone without jumping
// back to its start, and brings its active link into view.
function keepScroll(el, html) {
  const x = el.scrollLeft;
  el.innerHTML = html;
  el.scrollLeft = x;
  const a = $("a.active", el);
  if (!a) return;
  const row = el.getBoundingClientRect(), link = a.getBoundingClientRect();
  if (link.left < row.left) el.scrollLeft -= row.left - link.left + 16;
  else if (link.right > row.right) el.scrollLeft += link.right - row.right + 16;
}

function drawNav(current) {
  keepScroll(
    $("#nav"),
    SECTIONS.map((s) => {
      const v = s.views.find((x) => x.id === lastView[s.id]);
      const active = s === current ? ' class="active" aria-current="page"' : "";
      return `<a href="${esc(hashFor(s, v))}"${active}>${esc(s.label)}</a>`;
    }).join("")
  );
}

function drawTabs(section, view, params, data) {
  const tabs = $("#tabs");
  if (section.views.length < 2) {
    tabs.hidden = true;
    tabs.innerHTML = "";
    return;
  }
  tabs.hidden = false;
  keepScroll(
    tabs,
    section.views
      .map((v) => {
        const n = data && v.badge ? v.badge(data) : 0;
        const active = v === view ? ' class="active" aria-current="page"' : "";
        return `<a href="${esc(hashFor(section, v, params))}"${active}>${esc(v.label)}${n ? ` <span class="badge">${esc(fmt.int(n))}</span>` : ""}</a>`;
      })
      .join("")
  );
}

function draw(view, data, params) {
  const el = $("#page");
  hideTip();
  el.innerHTML = "";
  try {
    view.render(data, el, params);
  } catch (e) {
    el.innerHTML = "";
    errorCard(el, `Could not show ${view.label}`, e && e.message ? e.message : String(e));
    console.error(e);
  }
  hydrate(el);
}

let seq = 0;
// What the page shows now. A tab switch within the same section and filters
// draws from it instead of fetching again.
let shown = null;

async function route(force) {
  const { section, view, params } = parseHash();
  if (params.has("days")) memory.days = params.get("days");
  lastView[section.id] = view.id;
  drawNav(section);
  $("#title").textContent = section.label;
  document.title = (section.views.length > 1 ? view.label + " · " : "") + section.label + " · mindskein";

  const url = section.endpoint + queryString(section, params);
  const mine = ++seq;
  if (force !== true && shown && shown.section === section && shown.url === url) {
    drawTabs(section, view, params, shown.data);
    draw(view, shown.data, params);
    return;
  }

  const bar = $("#toolbar");
  drawTabs(section, view, params, null);
  bar.innerHTML = section.toolbar ? section.toolbar(params, null) : "";
  $("#stamp").textContent = "";
  const el = $("#page");
  el.innerHTML = `<p class="loading">Loading…</p>`;
  let data;
  try {
    data = await fetchJSON(url);
  } catch (e) {
    if (mine !== seq) return;
    shown = null;
    el.innerHTML = "";
    errorCard(el, `Could not load ${section.label}`, e.message);
    return;
  }
  if (mine !== seq) return;
  shown = { section, url, data };

  if (section.toolbar) bar.innerHTML = section.toolbar(params, data);
  if (data.generated_at && validTime(data.generated_at)) $("#stamp").textContent = "as of " + fmt.time(data.generated_at);
  drawTabs(section, view, params, data);
  draw(view, data, params);
}

// Toolbar controls carry data-param: changing one rewrites that parameter in
// the hash, which re-routes. A cleared value drops the parameter.
$("#toolbar").addEventListener("change", (ev) => {
  const t = ev.target;
  if (!t.dataset || !t.dataset.param) return;
  const { section, view, params } = parseHash();
  const v = t.type === "checkbox" ? (t.checked ? t.value : "") : t.value;
  if (v === "") params.delete(t.dataset.param);
  else params.set(t.dataset.param, v);
  location.hash = hashFor(section, view, params);
});

$("#refresh").addEventListener("click", () => route(true));
window.addEventListener("hashchange", () => route());

// Charts are drawn to the width they get, so a window that changes width
// redraws the view from what it already has.
let lastWidth = innerWidth;
let resizeTimer;
window.addEventListener("resize", () => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    if (innerWidth === lastWidth || !shown) return;
    lastWidth = innerWidth;
    const { section, view, params } = parseHash();
    if (shown.section === section) draw(view, shown.data, params);
  }, 200);
});

// ---------- chrome ----------

function setVersion(v) {
  if (v) $("#version").textContent = v;
}

const THEME_KEY = "mindskein-theme";
function applyTheme(t) {
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
  else delete document.documentElement.dataset.theme;
}
try {
  applyTheme(localStorage.getItem(THEME_KEY));
} catch {
  /* storage unavailable: follow the system */
}
$("#theme").addEventListener("click", () => {
  const cur = document.documentElement.dataset.theme;
  const dark = cur ? cur === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  const next = dark ? "light" : "dark";
  applyTheme(next);
  try {
    localStorage.setItem(THEME_KEY, next);
  } catch {
    /* not remembered; still applied */
  }
});

route();
fetchJSON("api/setup").then((d) => setVersion(d.version), () => {});
