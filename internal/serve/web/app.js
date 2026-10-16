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

const fmt = {
  usd: (v) => (v == null ? "–" : v >= 100 ? "$" + Math.round(v).toLocaleString() : "$" + Number(v).toFixed(2)),
  int: (v) => (v == null ? "–" : Math.round(v).toLocaleString()),
  tok: (v) => {
    if (v == null) return "–";
    const a = Math.abs(v);
    return a >= 1e9 ? (v / 1e9).toFixed(2) + "B" : a >= 1e6 ? (v / 1e6).toFixed(1) + "M" : a >= 1e3 ? (v / 1e3).toFixed(1) + "K" : String(Math.round(v));
  },
  pct: (v) => (v == null || !isFinite(v) ? "–" : (v * 100).toFixed(v > 0 && v < 0.1 ? 1 : 0) + "%"),
  chars: (v) => (v == null ? "–" : fmt.tok(v) + " ch"),
  date: (iso) => {
    const d = validTime(iso);
    return d ? `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}` : "";
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

// table renders rows in the order given. A column is
// {key, label, num, mono, wrap, fmt(value,row) -> text, html(row) -> html, bar, color}.
// A bar column adds a cell after it scaled to the column's largest value.
// Widths are set through the DOM after insertion (hydrate), not inline styles.
function table(columns, rows, opts = {}) {
  if (!rows || !rows.length) return `<p class="empty">${esc(opts.empty || "Nothing to show.")}</p>`;
  const maxes = {};
  for (const c of columns) if (c.bar) maxes[c.key] = Math.max(0, ...rows.map((r) => +(c.value ? c.value(r) : r[c.key]) || 0)) || 1;
  const head = columns
    .map((c) => `<th class="${c.num ? "num" : ""}">${esc(c.label)}</th>` + (c.bar ? `<th class="bar-col" aria-hidden="true"></th>` : ""))
    .join("");
  const body = rows
    .map(
      (r) =>
        "<tr>" +
        columns
          .map((c) => {
            const v = c.value ? c.value(r) : r[c.key];
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
  return `<div class="table-wrap"><table><thead><tr>${head}</tr></thead><tbody>${body}</tbody></table></div>`;
}

function hydrate(el) {
  for (const b of el.querySelectorAll(".bar[data-w]")) {
    b.style.width = b.dataset.w + "%";
    if (b.dataset.c) b.style.background = `var(${b.dataset.c})`;
  }
}

function kv(pairs) {
  return `<dl class="kv">${pairs.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`).join("")}</dl>`;
}

function select(param, value, options, label) {
  const opts = options.map(([v, l]) => `<option value="${esc(v)}"${String(v) === String(value) ? " selected" : ""}>${esc(l)}</option>`).join("");
  return `<label>${esc(label)} <select data-param="${esc(param)}">${opts}</select></label>`;
}

const RANGES = [["7", "Last 7 days"], ["30", "Last 30 days"], ["90", "Last 90 days"], ["180", "Last 180 days"], ["0", "All time"]];
const sevChip = (s) => `<span class="chip sev-${esc(s)}">${esc(s)}</span>`;

// ---------- pages ----------

// Remembered across pages for this visit only: the range picked on Usage
// carries to Health, and the accounts seen so far stay in the account picker.
const memory = { days: "30", accounts: new Set() };
const rangeParam = (p) => p.get("days") ?? memory.days;

// PAGES is the sidebar, in order. A page is
// {id, label, endpoint, query(params) -> {k: v}, toolbar(params, data) -> html, render(data, el, params)}.
// Adding a page is adding an entry here, plus its endpoint on the server.
const PAGES = [
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
    render: renderUsage,
  },
  {
    id: "overview",
    label: "Overview",
    endpoint: "api/brief",
    query: (p) => ({ all: p.get("all") === "1" ? "1" : "" }),
    toolbar: (p) => `<label><input type="checkbox" data-param="all" value="1"${p.get("all") === "1" ? " checked" : ""}> Show all</label>`,
    render: renderOverview,
  },
  {
    id: "setup",
    label: "Setup",
    endpoint: "api/setup",
    render: renderSetup,
  },
  {
    id: "health",
    label: "Health",
    endpoint: "api/health",
    query: (p) => ({ days: rangeParam(p) }),
    toolbar: (p) => select("days", rangeParam(p), RANGES, "Range"),
    render: renderHealth,
  },
];

function renderUsage(d, el) {
  const total = costTotal(d.cost);
  if (!d.sessions && !(d.findings || []).length) {
    const body = card(el, "No usage in this range");
    body.innerHTML = d.hint
      ? `<p class="empty">${esc(d.hint)}</p>`
      : `<p class="empty">The <code>Stop</code> hook records each session as you work. For sessions from before the hooks were installed, run <code>mindskein usage --backfill</code> once.</p>`;
    return;
  }

  kpis(el, [
    { label: "API-equivalent cost", value: fmt.usd(total), sub: d.from ? `since ${d.from}` : "all time", title: "API list prices; on a subscription this is value consumed, not the bill" },
    { label: "Sessions", value: fmt.int(d.sessions) },
    { label: "Prompts", value: fmt.int(d.prompts) },
    { label: "Cache hit", value: fmt.pct(d.cache_hit), sub: `${fmt.tok(d.tokens?.cache_read)} read from cache` },
    { label: "Subagent cost", value: fmt.usd(d.subagent_cost), sub: total ? `${fmt.pct(d.subagent_cost / total)} of the total` : "" },
  ]);
  const priceNote = document.createElement("p");
  priceNote.className = "note";
  priceNote.textContent = "Costs use API list prices. On a subscription they are the value consumed, not the bill.";
  el.appendChild(priceNote);

  const findings = d.findings || [];
  const fix = card(el, "Fix first", {
    right: findings.length ? `${findings.length} finding${findings.length === 1 ? "" : "s"}` : "",
    note: d.history_days ? `Unused-item findings rest on ${esc(fmt.int(d.history_days))} days of recorded history.` : "",
  });
  fix.innerHTML = findings.length
    ? findings
        .map(
          (f) => `<article class="finding">
            <div>${sevChip(f.severity)}</div>
            <div>
              <h3>${esc(f.title)}</h3>
              <p>${esc(f.advice)}</p>
              ${f.saving ? `<p class="saving">Saving: ${esc(f.saving)}</p>` : ""}
              ${f.evidence && f.evidence.length ? `<details><summary>Evidence (${f.evidence.length})</summary><ul class="evidence">${f.evidence.map((e) => `<li>${esc(e)}</li>`).join("")}</ul></details>` : ""}
            </div>
          </article>`
        )
        .join("")
    : `<p class="empty">Nothing to fix in this range.</p>`;

  const c = d.cost || {};
  const kinds = [
    { name: "Cache writes", cost: c.cache_write, tokens: (d.tokens?.cache_write_5m || 0) + (d.tokens?.cache_write_1h || 0) },
    { name: "Cache reads", cost: c.cache_read, tokens: d.tokens?.cache_read },
    { name: "Output", cost: c.output, tokens: d.tokens?.output },
    { name: "Fresh input", cost: c.input, tokens: d.tokens?.input },
  ];
  card(el, "Cost by kind", { span: 6 }).innerHTML = table(
    [
      { key: "name", label: "Kind" },
      { key: "cost", label: "Cost", num: true, fmt: fmt.usd, bar: true },
      { key: "share", label: "Share", num: true, value: (r) => (total ? (r.cost || 0) / total : 0), fmt: fmt.pct },
      { key: "tokens", label: "Tokens", num: true, fmt: fmt.tok },
    ],
    kinds
  );

  const spendCols = (label, nameFmt = (v) => v) => [
    { key: "name", label, fmt: nameFmt, wrap: true },
    { key: "cost", label: "Cost", num: true, fmt: fmt.usd, bar: true },
    { key: "share", label: "Share", num: true, fmt: fmt.pct },
    { key: "turns", label: "Turns", num: true, fmt: fmt.int },
    { key: "output", label: "Output", num: true, fmt: fmt.tok },
  ];
  card(el, "Cost by model", { span: 6 }).innerHTML = table(spendCols("Model", fmt.model), d.models, { empty: "No model use in this range." });
  card(el, "Cost by workstream", { span: 6 }).innerHTML = table(spendCols("Workstream"), d.workstreams, { empty: "No workstreams in this range." });
  if ((d.accounts || []).length > 1) {
    card(el, "Cost by account", { span: 6 }).innerHTML = table(spendCols("Account", fmt.tilde), d.accounts);
  }
  card(el, "Cost by subagent", { span: 6 }).innerHTML = table(spendCols("Agent"), d.agents, { empty: "No subagents ran in this range." });

  card(el, "Top sessions").innerHTML = table(
    [
      { key: "title", label: "Session", wrap: true, html: (r) => `${esc(r.title || r.id)}<div class="mono sub-id">${esc(String(r.id || "").slice(0, 8))}</div>` },
      { key: "repo", label: "Workstream", wrap: true },
      { key: "start", label: "Started", fmt: fmt.dateTime, nowrap: true },
      { key: "prompts", label: "Prompts", num: true, fmt: fmt.int },
      { key: "cost", label: "Cost", num: true, fmt: fmt.usd, bar: true },
      { key: "subagent_cost", label: "Subagents", num: true, fmt: fmt.usd },
      { key: "context_max", label: "Max context", num: true, fmt: fmt.tok },
    ],
    d.top_sessions,
    { empty: "No sessions in this range." }
  );

  card(el, "Tools", { span: 6 }).innerHTML = table(
    [
      { key: "name", label: "Tool", mono: true, wrap: true },
      { key: "calls", label: "Calls", num: true, fmt: fmt.int, bar: true },
      { key: "errors", label: "Errors", num: true, fmt: fmt.int },
      { key: "result_bytes", label: "Result size", num: true, fmt: fmt.chars },
    ],
    d.tools,
    { empty: "No tool calls in this range." }
  );

  const events = Object.entries(d.events || {})
    .map(([name, n]) => ({ name, n }))
    .sort((a, b) => b.n - a.n);
  card(el, "Events", { span: 6 }).innerHTML = table(
    [
      { key: "name", label: "Event" },
      { key: "n", label: "Count", num: true, fmt: fmt.int, bar: true, color: "--s2" },
    ],
    events,
    { empty: "No events in this range." }
  );
}

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
    ssBody.innerHTML = table(
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

}

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

  card(el, "Stores", { span: 6 }).innerHTML = table(
    [
      { key: "name", label: "Store" },
      { key: "records", label: "Records", num: true, fmt: fmt.int },
      { key: "retention", label: "Kept for", nowrap: true },
      { key: "path", label: "Path", mono: true, wrap: true, fmt: fmt.tilde },
    ],
    d.stores,
    { empty: "No stores yet." }
  );

  card(el, "Recent hook failures", { span: 6, note: "From <code>hooks.log</code>, newest first. A hook that fails logs here and still lets the session continue." }).innerHTML = table(
    [
      { key: "time", label: "When", nowrap: true, fmt: fmt.dateTime },
      { key: "event", label: "Hook", mono: true, nowrap: true },
      { key: "error", label: "Error", mono: true, wrap: true },
    ],
    d.failures,
    { empty: "No failures logged." }
  );
}

function renderHealth(d, el) {
  card(el, "Failing hooks", { note: "Hooks of any plugin or setting that failed in sessions in this range, with how often." }).innerHTML = table(
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
    card(el, "Always-loaded instruction files", { span: 6, right: where, note: "Loaded into every session. ≈ tokens is characters / 4." }).innerHTML = table(
      [
        { key: "path", label: "File", mono: true, wrap: true, fmt: fmt.tilde },
        { key: "kind", label: "Kind" },
        { key: "lines", label: "Lines", num: true, fmt: fmt.int },
        { key: "tokens", label: "≈ Tokens", num: true, value: (f) => Math.round((f.chars || 0) / 4), fmt: fmt.int, bar: true, color: "--s4" },
      ],
      r.context,
      { empty: "No instruction files found." }
    );
    card(el, "MCP servers", { span: 6, right: where }).innerHTML = table(
      [
        { key: "name", label: "Server", mono: true, wrap: true },
        { key: "scopes", label: "Scopes", fmt: (v) => (v || []).join(", ") },
      ],
      r.mcp,
      { empty: "No MCP servers configured." }
    );
    card(el, "Installed hooks", { right: where }).innerHTML = table(
      [
        { key: "event", label: "Event", nowrap: true },
        { key: "matcher", label: "Matcher", mono: true, wrap: true },
        { key: "command", label: "Command", mono: true, wrap: true },
      ],
      r.hooks,
      { empty: "No hooks installed." }
    );
  }
}

// ---------- routing ----------

function parseHash() {
  const raw = location.hash.replace(/^#\/?/, "");
  const q = raw.indexOf("?");
  const id = (q < 0 ? raw : raw.slice(0, q)) || "usage";
  const params = new URLSearchParams(q < 0 ? "" : raw.slice(q + 1));
  const page = PAGES.find((p) => p.id === id) || PAGES[0];
  return { page, params };
}

function go(page, params) {
  const qs = params.toString();
  location.hash = "#/" + page.id + (qs ? "?" + qs : "");
}

function queryString(page, params) {
  const q = page.query ? page.query(params) : {};
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

let seq = 0;

async function route() {
  const { page, params } = parseHash();
  if (params.has("days")) memory.days = params.get("days");

  for (const a of document.querySelectorAll("#nav a")) a.classList.toggle("active", a.dataset.page === page.id);
  $("#title").textContent = page.label;
  document.title = page.label + " · mindskein";
  $("#stamp").textContent = "";
  const bar = $("#toolbar");
  bar.innerHTML = page.toolbar ? page.toolbar(params, null) : "";

  const el = $("#page");
  el.innerHTML = `<p class="loading">Loading…</p>`;
  const mine = ++seq;
  let data;
  try {
    data = await fetchJSON(page.endpoint + queryString(page, params));
  } catch (e) {
    if (mine !== seq) return;
    el.innerHTML = "";
    errorCard(el, `Could not load ${page.label}`, e.message);
    return;
  }
  if (mine !== seq) return;

  if (page.toolbar) bar.innerHTML = page.toolbar(params, data);
  if (data.generated_at && validTime(data.generated_at)) $("#stamp").textContent = "as of " + fmt.time(data.generated_at);
  el.innerHTML = "";
  try {
    page.render(data, el, params);
  } catch (e) {
    el.innerHTML = "";
    errorCard(el, `Could not show ${page.label}`, e && e.message ? e.message : String(e));
    console.error(e);
  }
  hydrate(el);
}

// Toolbar controls carry data-param: changing one rewrites that parameter in
// the hash, which re-routes. A cleared value drops the parameter.
$("#toolbar").addEventListener("change", (ev) => {
  const t = ev.target;
  if (!t.dataset || !t.dataset.param) return;
  const { page, params } = parseHash();
  const v = t.type === "checkbox" ? (t.checked ? t.value : "") : t.value;
  if (v === "") params.delete(t.dataset.param);
  else params.set(t.dataset.param, v);
  go(page, params);
});

$("#refresh").addEventListener("click", route);
window.addEventListener("hashchange", route);

// ---------- chrome ----------

function setVersion(v) {
  if (v) $("#version").textContent = v;
}

$("#nav").innerHTML = PAGES.map((p) => `<a href="#/${esc(p.id)}" data-page="${esc(p.id)}">${esc(p.label)}</a>`).join("");

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
