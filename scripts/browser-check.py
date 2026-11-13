#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.64.1"]
# ///
"""Board check in system Chrome on a clone of the task store: no console errors, the panel opens and
closes, a `tix note` and a `tix move` in the same TIX_HOME show within one poll, and drag and drop
moves cards through tix: within a column (no prompt in blocked), between columns, into blocked with a reason or cancelled,
into closed with a result, and a tix refusal shows as a toast with the card back in place; the panel
edits title, tags, criteria and the body (a CLI edit meanwhile is a conflict that keeps the owner's
text) and sends a comment; a new task is created in idea and in open, and open without criteria is
refused in the form with the input kept.

Usage: scripts/browser-check.py [TASKS_DIR] (default ~/tasks; it is cloned, never written).
Screenshots go to the temporary folder printed at the end.
"""

# ruff: noqa: T201, S603, S607 - a CLI check: prints its result, runs git, tix and the built server

import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.request
from collections.abc import Callable
from pathlib import Path

from playwright.sync_api import Dialog, Page, sync_playwright
from playwright.sync_api import TimeoutError as PlaywrightTimeout

ROOT = Path(__file__).resolve().parent.parent
POLL_S = 5


def main() -> int:
    src = Path(sys.argv[1] if len(sys.argv) > 1 else "~/tasks").expanduser()
    tmp = Path(tempfile.mkdtemp(prefix="tix-web-check-"))
    home = tmp / "tasks"
    subprocess.run(["git", "clone", "-q", str(src), str(home)], check=True)
    env = {**os.environ, "TIX_HOME": str(home)}
    subprocess.run(["make", "-s", "-C", str(ROOT), "build"], check=True)
    server = subprocess.Popen(
        [str(ROOT / "bin/tix-web"), "-addr", "127.0.0.1:0"],
        env=env,
        stdout=subprocess.PIPE,
        text=True,
    )
    try:
        assert server.stdout is not None  # noqa: S101 - stdout=PIPE above
        line = server.stdout.readline()
        if not line:
            print("FAIL tix-web exited before printing its address")
            return 1
        return check(line.split()[-1], env, tmp)
    finally:
        server.terminate()


def tix(env: dict, *args: str, stdin: str | None = None) -> str:
    """Run tix in the cloned store; a refusal stops the check with the tix text."""
    r = subprocess.run(
        ["tix", *args, "--as", "human"],
        env=env,
        input=stdin,
        capture_output=True,
        text=True,
        check=False,
    )
    if r.returncode:
        raise SystemExit(f"FAIL tix {' '.join(args)}: exit {r.returncode}: {r.stderr.strip()}")
    return r.stdout


def seed(env: dict) -> dict[str, str]:
    """Three open tasks on top of the open column, C B A; only C has its criterion checked."""
    ids = {}
    for name, box in (("A", " "), ("B", " "), ("C", "x")):
        body = f"## Details\n\n- [{box}] one\n"
        out = tix(
            env,
            "new",
            f"browser-check {name}",
            "--to",
            "open",
            "--project",
            "tix-web",
            "--body-file",
            "-",
            stdin=body,
        )
        ids[name] = out.split()[1]
    for name in ("A", "B", "C"):
        tix(env, "move", ids[name], "open", "--first")
    return ids


def drag(page: Page, card: str, target: str) -> None:
    """Pointer drag of a card onto the upper part of target: SortableJS fallback needs real moves."""
    a = page.locator(f'.card[data-id="{card}"]').bounding_box()
    b = page.locator(target).first.bounding_box()
    if not a or not b:
        raise SystemExit(f"FAIL drag {card}: card or {target} is not on screen")
    page.mouse.move(a["x"] + a["width"] / 2, a["y"] + 20)
    page.mouse.down()
    page.mouse.move(a["x"] + a["width"] / 2 + 10, a["y"] + 30, steps=5)
    page.mouse.move(b["x"] + 40, b["y"] + 8, steps=20)
    page.wait_for_timeout(150)
    page.mouse.up()


def first_in(page: Page, status: str, task: str) -> bool:
    """True when task becomes the first card of the status column within 3 s."""
    try:
        page.wait_for_selector(
            f'.col[data-status="{status}"] .card:first-child[data-id="{task}"]', timeout=3000
        )
    except PlaywrightTimeout:
        return False
    return True


def task_json(url: str, task: str) -> dict:
    with urllib.request.urlopen(f"{url}/api/tasks/{task}") as r:  # noqa: S310 - the local server
        return json.load(r)


def check_moves(page: Page, url: str, ids: dict[str, str], out: Path) -> list[str]:
    a, b, c = ids["A"], ids["B"], ids["C"]
    failed = []
    open_top = '.col[data-status="open"] .card'
    drag(page, a, open_top)
    if not first_in(page, "open", a):
        failed.append(f"within open: {a} is not first")
    drag(page, a, '.col[data-status="in_progress"] .cards')
    if not first_in(page, "in_progress", a):
        failed.append(f"open > in_progress: {a} did not land")
    page.once("dialog", lambda d: d.accept("waits on review"))
    drag(page, b, '.col[data-status="blocked"] .cards')
    if not first_in(page, "blocked", b) or task_json(url, b)["waiting"] != "waits on review":
        failed.append(f"open > blocked: {b} not blocked with the reason")
    # A reorder inside blocked keeps the reason: no prompt, waiting unchanged.
    second = page.locator('.col[data-status="blocked"] .card').nth(1).get_attribute("data-id")
    waiting = task_json(url, second)["waiting"]
    asked: list[str] = []

    def no_prompt(d: Dialog) -> None:
        asked.append(d.message)
        d.dismiss()

    page.on("dialog", no_prompt)
    drag(page, second, '.col[data-status="blocked"] .card')
    landed = first_in(page, "blocked", second)
    page.remove_listener("dialog", no_prompt)
    if asked or not landed or task_json(url, second)["waiting"] != waiting:
        failed.append(f"reorder in blocked: asked {asked}, first {landed}, waiting changed")
    page.once("dialog", lambda d: d.dismiss())
    drag(page, a, '.col[data-status="blocked"] .cards')
    page.wait_for_timeout(800)
    if task_json(url, a)["status"] != "in_progress" or page.locator(".toast").count():
        failed.append(f"cancelled block: {a} moved or a toast showed")
    page.once("dialog", lambda d: d.accept("done"))
    drag(page, a, '.col[data-status="closed"] .cards')
    try:
        toast = page.wait_for_selector(".toast", timeout=3000).inner_text()
    except PlaywrightTimeout:
        toast = ""
    page.screenshot(path=out / "refused.png")
    if "criteria checked" not in toast or not first_in(page, "in_progress", a):
        failed.append(f"refused close: toast {toast!r}, {a} not back in in_progress")
    page.once("dialog", lambda d: d.accept("finished"))
    drag(page, c, '.col[data-status="closed"] .cards')
    if not first_in(page, "closed", c):
        failed.append(f"open > closed: {c} not on top of closed")
    page.screenshot(path=out / "moved.png")
    return failed


def wait(page: Page, selector: str) -> str:
    """Seconds until selector shows, or "never" after one poll and a margin."""
    start = time.monotonic()
    try:
        page.wait_for_selector(selector, timeout=(POLL_S + 1.5) * 1000)
    except PlaywrightTimeout:
        return "never"
    return f"{time.monotonic() - start:.1f}s"


def until(probe: Callable[[], bool], what: str, failed: list[str], timeout: float = 4) -> None:
    """Poll probe() for up to timeout seconds; record what when it never turns true."""
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if probe():
            return
        time.sleep(0.2)
    failed.append(what)


def check_edits(page: Page, url: str, env: dict, ids: dict[str, str], out: Path) -> list[str]:
    a, c = ids["A"], ids["C"]
    failed: list[str] = []
    page.locator(f'.card[data-id="{a}"]').click()
    page.wait_for_selector(".panel .md")

    page.locator(".panel h1.t").click()
    page.locator(".panel .t-input").fill("browser-check A edited")
    page.keyboard.press("Enter")
    until(lambda: task_json(url, a)["title"] == "browser-check A edited", "title not saved", failed)

    page.locator(".panel .prop .editable").click()
    page.locator(".panel .tags-input").fill("agent:claude, ui")
    page.keyboard.press("Enter")
    try:
        page.wait_for_selector(f'.card[data-id="{a}"] .chip:text-is("agent:claude")', timeout=4000)
    except PlaywrightTimeout:
        failed.append("tag agent:claude not on the card")

    page.locator('.panel .md input[data-n="1"]').click()
    until(lambda: "- [x] one" in task_json(url, a)["body"], "criterion 1 not checked", failed)

    page.locator(".panel textarea.compose").fill("please continue")
    page.keyboard.press("Meta+Enter" if sys.platform == "darwin" else "Control+Enter")
    try:
        page.wait_for_selector('.panel .item.comment:has-text("please continue")', timeout=4000)
    except PlaywrightTimeout:
        failed.append("comment not in the feed")

    # An unsent comment asks before the close button drops it; a dismissed question keeps the panel.
    page.locator(".panel textarea.compose").fill("not sent yet")
    asked = []
    page.once("dialog", lambda d: (asked.append(d.message), d.dismiss()))
    page.locator(".panel button.x").click()
    kept = page.locator(".panel textarea.compose")
    if not asked or kept.count() == 0 or kept.input_value() != "not sent yet":
        failed.append(f"close with an unsent comment: asked {asked}, panel or text gone")
        return failed
    kept.fill("")

    # Conflict: the CLI edits the body while the editor is open; the owner's text must survive.
    page.locator('.panel button:text-is("Править")').click()
    mine = "## Details\n\n- [x] one\n- [ ] from the browser\n"
    page.locator(".panel textarea.editor").fill(mine)
    tix(
        env,
        "edit",
        a,
        "--base",
        task_json(url, a)["rev"],
        stdin="## Details\n\n- [x] one\n- [ ] from the cli\n",
    )
    page.locator('.panel button:text-is("Сохранить")').click()
    try:
        warn = page.wait_for_selector(".panel .warn", timeout=4000).inner_text()
    except PlaywrightTimeout:
        warn = ""
    page.screenshot(path=out / "conflict.png")
    kept = (
        page.locator(".panel textarea.editor").count()
        and page.locator(".panel textarea.editor").input_value() == mine
    )
    if "conflict" not in warn or not kept:
        failed.append(f"conflict: warning {warn!r}, owner text kept: {bool(kept)}")
        return failed
    page.locator('.panel button:text-is("Перезаписать поверх новой")').click()
    until(
        lambda: "from the browser" in task_json(url, a)["body"],
        "overwrite did not save the owner text",
        failed,
    )
    page.keyboard.press("Escape")
    page.wait_for_timeout(300)

    # A refusal: unchecking a criterion of a closed task shows the tix text as a toast.
    page.locator(f'.card[data-id="{c}"]').click()
    page.wait_for_selector('.panel .md input[data-n="1"]')
    page.locator('.panel .md input[data-n="1"]').click()
    try:
        toast = page.wait_for_selector(".toast", timeout=4000).inner_text()
    except PlaywrightTimeout:
        toast = ""
    if not toast.startswith("err "):
        failed.append(f"uncheck on closed: toast {toast!r}")
    page.keyboard.press("Escape")
    return failed


def create(page: Page, title: str, status: str, body: str | None = None) -> None:
    page.locator('.top button:text-is("Новая задача")').click()
    page.locator(".new-task .t-input").fill(title)
    page.locator(".new-task select").nth(1).select_option(status)
    if body is not None:
        page.locator(".new-task textarea.editor").fill(body)
    page.locator('.new-task button:text-is("Создать")').click()


def check_new(page: Page, out: Path) -> list[str]:
    failed: list[str] = []
    for title, status in (("browser-check new idea", "idea"), ("browser-check new open", "open")):
        create(page, title, status)
        try:
            page.wait_for_selector(f'.panel h1.t:text-is("{title}")', timeout=4000)
            page.wait_for_selector(
                f'.col[data-status="{status}"] .card:has-text("{title}")', timeout=4000
            )
        except PlaywrightTimeout:
            failed.append(f"new task in {status}: no panel or card")
        page.keyboard.press("Escape")
        page.wait_for_timeout(300)

    create(page, "browser-check refused", "open", "## Abstract\n\nno criteria\n")
    try:
        warn = page.wait_for_selector(".new-task .warn", timeout=4000).inner_text()
    except PlaywrightTimeout:
        warn = ""
    page.screenshot(path=out / "new-refused.png")
    if (
        "no criteria" not in warn
        or page.locator(".new-task .t-input").input_value() != "browser-check refused"
    ):
        failed.append(f"open without criteria: warning {warn!r}, title kept in the form")
    page.once("dialog", lambda d: d.accept())
    page.keyboard.press("Escape")
    return failed


def check(url: str, env: dict, out: Path) -> int:
    ids = seed(env)
    with urllib.request.urlopen(url + "/api/board") as r:  # noqa: S310 - the local server started above
        board = json.load(r)
    failed = []
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome", headless=True)
        page = browser.new_page(viewport={"width": 1600, "height": 950})
        errors = []
        page.on("pageerror", lambda e: errors.append(str(e)))
        # The refused close answers 409 on purpose; the toast check covers it.
        page.on(
            "console",
            lambda m: m.type == "error" and "status of 409" not in m.text and errors.append(m.text),
        )
        page.on(
            "response",
            lambda r: r.status >= 400 and r.status != 409 and errors.append(f"{r.status} {r.url}"),
        )
        page.goto(url)
        page.wait_for_selector(".card")
        page.screenshot(path=out / "board.png")

        cols = page.locator(".col").count()
        if cols != len(board["statuses"]):
            failed.append(f"columns {cols}, statuses {len(board['statuses'])}")
        closed = [s["name"] for s in board["statuses"] if s["kind"] == "closed"]
        if closed and page.locator(f'.col[data-status="{closed[0]}"] .card').count() > 12:
            failed.append("closed column shows more than 12 cards")

        # The open panel re-reads its task: a note added by tix shows without a reload.
        card = page.locator(".card:has(.note)").first
        noted = card.get_attribute("data-id")
        card.click()
        page.wait_for_selector(".panel .md", timeout=3000)
        page.wait_for_timeout(300)
        page.screenshot(path=out / "task.png")
        feed = page.locator(".panel .item").count()
        tix(env, "note", noted, "browser-check note")
        note_seen = wait(page, f".panel .item:nth-child({feed + 1})")
        if note_seen == "never":
            failed.append(f"note on {noted} did not show in the open panel within {POLL_S}s")
        page.keyboard.press("Escape")
        page.wait_for_timeout(300)
        if page.locator(".panel").count():
            failed.append("Esc did not close the panel")

        # Move an open task back to idea in the same store and wait for the board to show it.
        open_ids = [
            t["id"] for t in board["tasks"] if t["status"] == "open" and t["id"] not in ids.values()
        ]
        if not open_ids:
            raise SystemExit("FAIL the store has no open task to move")
        moved = open_ids[0]
        tix(env, "move", moved, "idea")
        seen = wait(page, f'.col[data-status="idea"] .card[data-id="{moved}"]')
        if seen == "never":
            failed.append(f"{moved} moved by tix did not show within {POLL_S}s")
        failed += check_moves(page, url, ids, out)
        failed += check_edits(page, url, env, ids, out)
        failed += check_new(page, out)
        browser.close()
    failed += [f"console: {e}" for e in errors]
    print(
        f"cols={cols} note_seen_after={note_seen} moved={moved} move_seen_after={seen} screenshots={out}"
    )
    for f in failed:
        print("FAIL", f)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
