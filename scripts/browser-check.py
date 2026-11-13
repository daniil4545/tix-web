#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.64.1"]
# ///
"""Board check in system Chrome on a clone of the task store: no console errors, the panel opens and
closes, and a `tix move` in the same TIX_HOME shows on the board within one poll.

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
from pathlib import Path

from playwright.sync_api import Page, sync_playwright
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


def tix(env: dict, *args: str) -> None:
    """Run tix in the cloned store; a refusal stops the check with the tix text."""
    r = subprocess.run(
        ["tix", *args, "--as", "human"], env=env, capture_output=True, text=True, check=False
    )
    if r.returncode:
        raise SystemExit(f"FAIL tix {' '.join(args)}: exit {r.returncode}: {r.stderr.strip()}")


def wait(page: Page, selector: str) -> str:
    """Seconds until selector shows, or "never" after one poll and a margin."""
    start = time.monotonic()
    try:
        page.wait_for_selector(selector, timeout=(POLL_S + 1.5) * 1000)
    except PlaywrightTimeout:
        return "never"
    return f"{time.monotonic() - start:.1f}s"


def check(url: str, env: dict, out: Path) -> int:
    with urllib.request.urlopen(url + "/api/board") as r:  # noqa: S310 - the local server started above
        board = json.load(r)
    failed = []
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome", headless=True)
        page = browser.new_page(viewport={"width": 1600, "height": 950})
        errors = []
        page.on("pageerror", lambda e: errors.append(str(e)))
        page.on("console", lambda m: m.type == "error" and errors.append(m.text))
        page.on("response", lambda r: r.status >= 400 and errors.append(f"{r.status} {r.url}"))
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
        open_ids = [t["id"] for t in board["tasks"] if t["status"] == "open"]
        if not open_ids:
            raise SystemExit("FAIL the store has no open task to move")
        moved = open_ids[0]
        tix(env, "move", moved, "idea")
        seen = wait(page, f'.col[data-status="idea"] .card[data-id="{moved}"]')
        if seen == "never":
            failed.append(f"{moved} moved by tix did not show within {POLL_S}s")
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
