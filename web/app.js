// Placeholder page: tasks by status as plain lists. The board comes with its own task.
// Text goes in through textContent only, task data is never parsed as markup.
async function load() {
  const board = document.getElementById("board");
  let res, data;
  try {
    res = await fetch("/api/board");
    data = await res.json();
  } catch (err) {
    board.textContent = `No answer from tix-web: ${err}. Is the server running?`;
    return;
  }
  if (!res.ok) {
    board.replaceChildren();
    document.getElementById("error").textContent = data.error;
    return;
  }
  board.replaceChildren();
  if (data.tasks.length === 0) {
    board.textContent = 'No tasks yet. Create one: tix new "title"';
    return;
  }
  for (const st of data.statuses) {
    const h = document.createElement("h2");
    h.textContent = `${st.name} (${st.count})`;
    const ul = document.createElement("ul");
    for (const t of data.tasks.filter((x) => x.status === st.name)) {
      const li = document.createElement("li");
      li.textContent = `${t.id} [${t.project}] ${t.title}`;
      ul.append(li);
    }
    board.append(h, ul);
  }
}

load();
