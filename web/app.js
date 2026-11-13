// Read-only board: columns in `tix status` order, a task panel, polling. Styles are spike A as is.
// Task text reaches the DOM as text, except body and feed HTML that the server renders without raw HTML.
import { h, render } from "preact";
import { useState, useEffect, useCallback } from "preact/hooks";
import htm from "htm";

const html = htm.bind(h);
const POLL_MS = 5000;
const CLOSED_LIMIT = 12;

async function getJSON(url) {
  const res = await fetch(url);
  const data = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

// usePoll runs load now, every POLL_MS while the tab is visible, and when the tab comes back.
function usePoll(load) {
  useEffect(() => {
    load();
    const tick = () => document.hidden || load();
    const id = setInterval(tick, POLL_MS);
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [load]);
}

function ago(at) {
  const m = Math.round((Date.now() - new Date(at).getTime()) / 60000);
  if (m < 1) return "сейчас";
  if (m < 60) return `${m} мин`;
  const hours = Math.round(m / 60);
  return hours < 24 ? `${hours} ч` : `${Math.round(hours / 24)} д`;
}

const who = (actor) => (actor || "").split(":")[0] || "владелец";

// claimText turns "claude:3d8e... until 2026-11-13T13:15" into "claude, до 13:15".
function claimText(claim) {
  if (!claim) return "нет";
  const until = claim.match(/until \S*T(\d\d:\d\d)/);
  return until ? `${who(claim)}, до ${until[1]}` : who(claim);
}

// columnTasks keeps tix order; closed shows the most recently closed first, which tix lists last.
function columnTasks(tasks, status, more) {
  const own = tasks.filter((t) => t.status === status.name);
  if (status.kind !== "closed") return { shown: own, total: own.length };
  const recent = own.reverse();
  return { shown: more ? recent : recent.slice(0, CLOSED_LIMIT), total: recent.length };
}

function App() {
  const [board, setBoard] = useState(null);
  const [error, setError] = useState("");
  const [openId, setOpenId] = useState(null);
  const [more, setMore] = useState(false);

  const load = useCallback(
    () => getJSON("/api/board").then((b) => { setBoard(b); setError(""); }, (e) => setError(e.message)),
    [],
  );
  usePoll(load);

  useEffect(() => {
    const key = (e) => e.key === "Escape" && setOpenId(null);
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, []);

  let body;
  if (!board) body = !error && html`<p class="tag empty">Загрузка…</p>`;
  else if (board.tasks.length === 0) body = html`<p class="tag empty">Задач нет. Заведите первую: tix new "заголовок"</p>`;
  else
    body = html`<div class="board">${board.statuses.map((s) => html`<${Column} key=${s.name} status=${s}
      ...${columnTasks(board.tasks, s, more)} onMore=${() => setMore(true)} onOpen=${setOpenId} />`)}</div>`;

  return html`<div class="main">
    <header class="top"><h1>Доска</h1><span class="spacer"></span>
      ${board && html`<span class="tag">${board.tasks.length} задач, опрос раз в 5 с</span>`}</header>
    ${body}
    ${openId && html`<${Panel} id=${openId} statuses=${board?.statuses || []} onClose=${() => setOpenId(null)} />`}
    ${error && html`<div class="toasts"><div class="toast bad">${error}</div></div>`}
  </div>`;
}

function Column({ status, shown, total, onMore, onOpen }) {
  return html`<div class="col" data-status=${status.name}>
    <div class="col-head"><span class=${"dot " + status.kind}></span>${status.name}<span class="n">${total}</span></div>
    <div class="cards">
      ${shown.map((t) => html`<${Card} key=${t.id} t=${t} onOpen=${onOpen} />`)}
      ${total > shown.length && html`<button class="more" onClick=${onMore}>ещё ${total - shown.length}</button>`}
    </div>
  </div>`;
}

function Card({ t, onOpen }) {
  const n = t.last_note;
  return html`<div class="card" data-id=${t.id} onClick=${() => onOpen(t.id)}>
    <div class="card-top"><span class="mono">${t.id}</span>
      ${t.claim && html`<span class="claim" title=${claimText(t.claim)}>${who(t.claim)[0].toUpperCase()}</span>`}</div>
    <div class="title">${t.title}</div>
    <div class="meta"><span class="chip">${t.project}</span>
      ${(t.tags || []).map((tag) => html`<span class="chip" key=${tag}>${tag}</span>`)}
      ${t.children > 0 && html`<${Progress} t=${t} />`}</div>
    ${n && html`<div class="note"><b>${ago(n.at)} ·</b> ${n.text}</div>`}
  </div>`;
}

const Progress = ({ t }) => html`<span class="progress"><span class="bar">
  <i style=${{ width: (100 * t.children_closed) / t.children + "%" }}></i></span>${t.children_closed}/${t.children}</span>`;

// feed merges notes and comments into one timeline, oldest first.
function feed(t) {
  const items = [
    ...(t.notes || []).map((n) => ({ ...n, type: "note" })),
    ...(t.comments || []).map((c) => ({ ...c, type: "comment" })),
  ];
  return items.sort((a, b) => new Date(a.at) - new Date(b.at));
}

// External is written by agents: only an http(s) URL becomes a link.
const External = ({ value }) =>
  /^https?:\/\//.test(value) ? html`<a href=${value} target="_blank" rel="noopener noreferrer">${value}</a>` : value;

function Panel({ id, statuses, onClose }) {
  const [t, setT] = useState(null);
  const [error, setError] = useState("");
  const load = useCallback(
    () => getJSON(`/api/tasks/${encodeURIComponent(id)}`).then((x) => { setT(x); setError(""); }, (e) => setError(e.message)),
    [id],
  );
  usePoll(load);

  const kind = t && statuses.find((s) => s.name === t.status)?.kind;
  const items = t ? feed(t) : [];
  return html`<div>
    <div class="scrim" onClick=${onClose}></div>
    <div class="panel">
      <div class="panel-head"><span class="mono">${id}</span>${t?.parent && html`<span>· в эпике ${t.parent}</span>`}
        <button class="x" onClick=${onClose} title="Закрыть (Esc)">×</button></div>
      ${error && html`<div class="panel-head">${error}</div>`}
      ${t && html`<div class="panel-body">
        <div class="doc">
          <h1 class="t">${t.title}</h1>
          <div class="md" dangerouslySetInnerHTML=${{ __html: t.body_html }}></div>
          <div class="feed-title">Активность <span class="n">${items.length}</span></div>
          <div class="feed">${items.map((it) => html`<div class=${"item " + it.type}>
            <span class="av">${who(it.actor)[0].toUpperCase()}</span>
            <div><div class="head"><b>${who(it.actor)}</b> ${it.type === "note" ? "заметка" : "комментарий"} · ${ago(it.at)}</div>
            <div class=${it.type === "comment" ? "bubble md" : "md"} dangerouslySetInnerHTML=${{ __html: it.html }}></div></div></div>`)}</div>
        </div>
        <div class="props">
          <div class="prop"><label>Статус</label><div><span class=${"dot " + kind}></span>${t.status}</div></div>
          <div class="prop"><label>Проект</label><div>${t.project}</div></div>
          ${t.tags?.length > 0 && html`<div class="prop"><label>Метки</label><div>${t.tags.map((tag) => html`<span class="chip">${tag}</span>`)}</div></div>`}
          <div class="prop"><label>Исполнитель</label><div>${claimText(t.claim)}</div></div>
          ${t.waiting && html`<div class="prop"><label>Ждёт</label><div>${t.waiting}</div></div>`}
          ${t.external && html`<div class="prop"><label>PR</label><div><${External} value=${t.external} /></div></div>`}
          <div class="prop"><label>Создана</label><div>${t.created.slice(0, 10)}</div></div>
        </div>
      </div>`}
    </div>
  </div>`;
}

render(html`<${App} />`, document.getElementById("root"));
