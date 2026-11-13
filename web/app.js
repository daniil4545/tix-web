// Board: columns in `tix status` order, drag and drop through `tix move`, a task panel, polling.
// Styles are spike A as is. After every write the board is read again: tix decides, the page shows.
// Task text reaches the DOM as text, except body and feed HTML that the server renders without raw HTML.
import { h, render } from "preact";
import { useState, useEffect, useCallback, useRef } from "preact/hooks";
import htm from "htm";

const html = htm.bind(h);
const POLL_MS = 5000;
const CLOSED_LIMIT = 12;
const NOTICE_MS = 6000;

// drag.active holds the board poll while a card is in the air: a redraw mid-drag loses the drop.
const drag = { active: false };

async function getJSON(url) {
  const res = await fetch(url);
  const data = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

// sendJSON writes through the server; a refusal throws with the tix text and its exit code.
async function sendJSON(url, body, method = "POST") {
  const res = await fetch(url, { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  if (res.ok) return;
  const data = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
  throw Object.assign(new Error(data.error || `HTTP ${res.status}`), { code: data.code });
}

// criteriaCount counts criteria the way tix does: `- [ ]` lines under "## Details", outside code blocks.
function criteriaCount(body) {
  let inDetails = false;
  let fence = "";
  let n = 0;
  for (const raw of (body || "").split("\n")) {
    const ln = raw.replace(/\r$/, "");
    const start = ln.trimStart();
    const mark = start.startsWith("```") ? "```" : start.startsWith("~~~") ? "~~~" : "";
    if (fence) {
      if (mark === fence) fence = "";
    } else if (mark) fence = mark;
    else if (ln.trimEnd() === "## Details") inDetails = true;
    else if (ln.startsWith("## ")) inDetails = false;
    else if (inDetails && /^\s*[-*] \[( |x|X)\] /.test(ln)) n++;
  }
  return n;
}

// markCriteria makes the Details checkboxes clickable, numbered as in tix. When the rendered boxes
// and the tix criteria differ in count, they stay read-only rather than tick the wrong line.
function markCriteria(root, body) {
  const boxes = [];
  let inDetails = false;
  for (const el of root.children) {
    if (el.tagName === "H2") inDetails = el.textContent.trim() === "Details";
    else if (inDetails) boxes.push(...el.querySelectorAll('input[type="checkbox"]'));
  }
  if (boxes.length !== criteriaCount(body)) return;
  boxes.forEach((box, i) => {
    box.disabled = false;
    box.dataset.n = String(i + 1);
  });
}

// moveBody asks what tix needs for the target kind; null means the owner cancelled. A reorder within
// the same column asks nothing: a new reason or result would overwrite the old one.
// Closed keeps tix placement (the end, shown first); other columns keep the drop position.
function moveBody(kind, after, same) {
  const place = after ? { after } : { first: true };
  if (same) return place;
  if (kind === "blocked") {
    const reason = prompt("Причина блокировки");
    return reason ? { message: reason, ...place } : null;
  }
  if (kind === "closed") {
    const result = prompt("Итог (можно оставить пустым)");
    return result === null ? null : { message: result };
  }
  return place;
}

// usePoll runs load now, every POLL_MS while the tab is visible, and when the tab comes back.
// One request at a time: a slow tix answer can neither overlap nor overwrite a newer one.
function usePoll(load) {
  useEffect(() => {
    let busy = false;
    const tick = () => {
      if (busy || document.hidden) return;
      busy = true;
      load().finally(() => { busy = false; });
    };
    tick();
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

// columnTasks keeps tix order. tix 0.9.1 has no close time, but `tix move` without --after or --first
// puts a task at the end of its column, so closed shows the end of the list reversed.
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
  const [notice, setNotice] = useState("");

  // Only the newest board request is applied: a poll held during a prompt may answer after the
  // read that follows the move.
  const seq = useRef(0);
  const load = useCallback(() => {
    if (drag.active) return Promise.resolve();
    const n = ++seq.current;
    const fresh = (f) => (x) => n === seq.current && f(x);
    return getJSON("/api/board").then(fresh((b) => { setBoard(b); setError(""); }), fresh((e) => setError(e.message)));
  }, []);
  usePoll(load);

  useEffect(() => {
    if (!notice) return undefined;
    const id = setTimeout(() => setNotice(""), NOTICE_MS);
    return () => clearTimeout(id);
  }, [notice]);

  async function move(id, status, after) {
    const kind = board.statuses.find((s) => s.name === status)?.kind;
    const same = board.tasks.find((t) => t.id === id)?.status === status;
    const body = moveBody(kind, after, same);
    if (!body) return;
    try {
      await sendJSON(`/api/tasks/${encodeURIComponent(id)}/move`, { status, ...body });
    } catch (err) {
      setNotice(err.message);
    }
    await load();
  }

  let body;
  if (!board) body = !error && html`<p class="tag empty">Загрузка…</p>`;
  else if (board.tasks.length === 0) body = html`<p class="tag empty">Задач нет. Заведите первую: tix new "заголовок"</p>`;
  else
    body = html`<div class="board">${board.statuses.map((s) => html`<${Column} key=${s.name} status=${s}
      ...${columnTasks(board.tasks, s, more)} onMore=${() => setMore(true)} onOpen=${setOpenId} onMove=${move} />`)}</div>`;

  return html`<div class="main">
    <header class="top"><h1>Доска</h1><span class="spacer"></span>
      ${board && html`<span class="tag">${board.tasks.length} задач, опрос раз в 5 с</span>`}</header>
    ${body}
    ${openId && html`<${Panel} key=${openId} id=${openId} statuses=${board?.statuses || []}
      onClose=${() => setOpenId(null)} onChanged=${load} onNotice=${setNotice} />`}
    ${(error || notice) && html`<div class="toasts">
      ${error && html`<div class="toast bad">${error}</div>`}${notice && html`<div class="toast bad">${notice}</div>`}</div>`}
  </div>`;
}

function Column({ status, shown, total, onMore, onOpen, onMove }) {
  const list = useRef();
  const handler = useRef();
  handler.current = onMove;
  useEffect(() => {
    // Fallback mode with the copy on body: native drag hides the card or lags behind the pointer.
    const sortable = Sortable.create(list.current, {
      group: "board", animation: 180, draggable: ".card", ghostClass: "ghost",
      forceFallback: true, fallbackClass: "lifted", fallbackTolerance: 4, fallbackOnBody: true,
      onStart: () => { drag.active = true; },
      onEnd(e) {
        drag.active = false;
        const ids = [...e.to.querySelectorAll(".card")].map((el) => el.dataset.id);
        const i = ids.indexOf(e.item.dataset.id);
        const col = e.to.closest(".col");
        // Sortable moved the node itself: put it back and let Preact draw what tix answers.
        e.item.remove();
        e.from.insertBefore(e.item, e.from.children[e.oldIndex] || null);
        if (e.from === e.to && (e.oldIndex === e.newIndex || col.dataset.kind === "closed")) return;
        handler.current(e.item.dataset.id, col.dataset.status, i > 0 ? ids[i - 1] : "");
      },
    });
    return () => sortable.destroy();
  }, []);
  return html`<div class="col" data-status=${status.name} data-kind=${status.kind}>
    <div class="col-head"><span class=${"dot " + status.kind}></span>${status.name}<span class="n">${total}</span></div>
    <div class="cards" ref=${list}>
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

// stopEsc keeps Esc inside a field: it cancels the field, not the panel.
const stopEsc = (e, cancel) => {
  if (e.key !== "Escape") return;
  e.stopPropagation();
  cancel?.();
};

const focus = (el) => el && document.activeElement !== el && el.focus();

function Panel({ id, statuses, onClose, onChanged, onNotice }) {
  const [t, setT] = useState(null);
  const [error, setError] = useState("");
  const [title, setTitle] = useState(null); // the draft while the title is edited
  const [tags, setTags] = useState(null);
  const [draft, setDraft] = useState(null); // { body, rev, error, conflict } while the body is edited
  const [comment, setComment] = useState("");
  const md = useRef();
  const url = `/api/tasks/${encodeURIComponent(id)}`;
  const load = useCallback(
    () => getJSON(url).then((x) => { setT(x); setError(""); }, (e) => setError(e.message)),
    [url],
  );
  usePoll(load);

  useEffect(() => {
    if (t && md.current) markCriteria(md.current, t.body);
  });

  // An unsaved body is not lost to Esc or a click outside.
  const close = () => {
    if (draft && draft.body !== t?.body && !confirm("Закрыть без сохранения правки?")) return;
    onClose();
  };
  useEffect(() => {
    const key = (e) => e.key === "Escape" && close();
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  });

  // change sends one write; a refusal goes to the toast. The task and the board are read again either way.
  async function change(method, path, body) {
    try {
      await sendJSON(url + path, body, method);
      return true;
    } catch (err) {
      onNotice(err.message);
      return false;
    } finally {
      await Promise.all([load(), onChanged()]);
    }
  }

  const saveField = (field, value, old, reset) => {
    reset(null);
    const v = value.trim();
    if (v !== old && (v || field === "tags")) change("PATCH", "", { [field]: v });
  };

  // saveBody keeps the owner's text on a refusal; a conflict (tix exit 3) offers to save over the newer body.
  async function saveBody(rev) {
    try {
      await sendJSON(url, { rev, body: draft.body }, "PUT");
      setDraft(null);
    } catch (err) {
      setDraft((d) => ({ ...d, error: err.message, conflict: err.code === 3 }));
    }
    await Promise.all([load(), onChanged()]);
  }
  const overwrite = async () => saveBody((await getJSON(url)).rev);

  async function sendComment() {
    const text = comment.trim();
    if (text && (await change("POST", "/comment", { text }))) setComment("");
  }

  const onBodyClick = (e) => {
    const n = e.target.dataset?.n;
    if (!n) return;
    e.preventDefault(); // the box shows what tix answers
    change("POST", e.target.checked ? "/check" : "/uncheck", { n: Number(n) });
  };

  const kind = t && statuses.find((s) => s.name === t.status)?.kind;
  const items = t ? feed(t) : [];
  return html`<div>
    <div class="scrim" onClick=${close}></div>
    <div class="panel">
      <div class="panel-head"><span class="mono">${id}</span>${t?.parent && html`<span>· в эпике ${t.parent}</span>`}
        <button class="x" onClick=${close} title="Закрыть (Esc)">×</button></div>
      ${error && html`<div class="panel-head">${error}</div>`}
      ${t && html`<div class="panel-body">
        <div class="doc">
          ${title === null
            ? html`<h1 class="t editable" title="Изменить заголовок" onClick=${() => setTitle(t.title)}>${t.title}</h1>`
            : html`<input class="t-input" ref=${focus} value=${title} onInput=${(e) => setTitle(e.target.value)}
                onBlur=${() => setTitle(null)}
                onKeyDown=${(e) => (e.key === "Enter" ? saveField("title", title, t.title, setTitle) : stopEsc(e, () => setTitle(null)))} />`}
          ${draft
            ? html`<div class="editor-box">
                <textarea class="editor" ref=${focus} value=${draft.body} onInput=${(e) => setDraft({ ...draft, body: e.target.value })}
                  onKeyDown=${(e) => stopEsc(e)}></textarea>
                ${draft.error && html`<p class="warn">${draft.error}</p>`}
                <div class="actions">
                  <button class="btn primary" onClick=${() => saveBody(draft.rev)}>Сохранить</button>
                  ${draft.conflict && html`<button class="btn" onClick=${overwrite}>Перезаписать поверх новой</button>`}
                  <button class="btn" onClick=${() => setDraft(null)}>Отмена</button>
                </div></div>`
            : html`<div class="md" ref=${md} onClick=${onBodyClick} dangerouslySetInnerHTML=${{ __html: t.body_html }}></div>
                <div class="actions"><button class="btn" onClick=${() => setDraft({ body: t.body, rev: t.rev })}>Править</button></div>`}
          <div class="feed-title">Активность <span class="n">${items.length}</span></div>
          <div class="feed">${items.map((it) => html`<div class=${"item " + it.type}>
            <span class="av">${who(it.actor)[0].toUpperCase()}</span>
            <div><div class="head"><b>${who(it.actor)}</b> ${it.type === "note" ? "заметка" : "комментарий"} · ${ago(it.at)}</div>
            <div class=${it.type === "comment" ? "bubble md" : "md"} dangerouslySetInnerHTML=${{ __html: it.html }}></div></div></div>`)}</div>
          <textarea class="compose" placeholder="Комментарий агенту. Cmd+Enter отправляет" value=${comment}
            onInput=${(e) => setComment(e.target.value)}
            onKeyDown=${(e) => ((e.metaKey || e.ctrlKey) && e.key === "Enter" ? sendComment() : stopEsc(e))}></textarea>
          <div class="actions"><button class="btn" disabled=${!comment.trim()} onClick=${sendComment}>Отправить</button></div>
        </div>
        <div class="props">
          <div class="prop"><label>Статус</label><div><span class=${"dot " + kind}></span>${t.status}</div></div>
          <div class="prop"><label>Проект</label><div>${t.project}</div></div>
          <div class="prop"><label>Метки</label>
            ${tags === null
              ? html`<div class="editable" title="Изменить метки" onClick=${() => setTags((t.tags || []).join(", "))}>
                  ${t.tags?.length ? t.tags.map((tag) => html`<span class="chip">${tag}</span>`) : "нет"}</div>`
              : html`<input class="tags-input" ref=${focus} value=${tags} placeholder="agent:claude, ui"
                  onInput=${(e) => setTags(e.target.value)} onBlur=${() => setTags(null)}
                  onKeyDown=${(e) => (e.key === "Enter" ? saveField("tags", tags, (t.tags || []).join(", "), setTags) : stopEsc(e, () => setTags(null)))} />`}</div>
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
