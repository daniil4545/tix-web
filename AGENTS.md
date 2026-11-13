# tix-web - context for AI agents

## Project

A local web board over the tix CLI for the owner: create a task, edit it, move it on the board, close
it, and watch agent notes. The plan, API contract and invariants live in the tix epic T-755
(`tix show T-755`); each slice is a child task.

## Principles

- The server holds no task rules and never reads or writes `~/tasks` files: reads are `tix ... --json`,
  writes are tix commands with `--as human`.
- tix text is shown to the owner unchanged; the server only maps the exit code to HTTP.
- Front end: Preact + htm + SortableJS from `web/vendor/`, no build step (decision-6). Spike A styles as is.

## Stack

Go standard library, `go:embed` for `web/`, goldmark for Markdown. Versions are in `go.mod`.

## Layout

| Path | What |
|---|---|
| `main.go` | flags, startup checks, listener |
| `server.go` | routes, Host check, JSON and Markdown |
| `tix.go` | running tix, exit codes, parsing `tix status` and `tix projects` text |
| `web/` | the page (Preact + htm, spike A styles), embedded into the binary |
| `web/vendor/` | front-end libraries, version in the file name, MIT licenses next to them |
| `testdata/fake-tix.sh` | fake tix of the fast tier, logs its arguments |
| `scripts/browser-check.py` | Playwright check of the board on a clone of the task store |

## Commands

| What | Command |
|---|---|
| fast tests (fake tix) | `make test-fast` |
| all tests (needs tix 0.9.1) | `make test` |
| linters | `make lint` |
| board in a browser (local only) | `make browser-check` |
| run | `make build && bin/tix-web` |
| before a PR | `make pr-check` |

## Process

- GitHub flow: task branch `T-<n>-<slug>` from `main`, PR, squash. CI runs `make ci-check` on PRs into main.
- Public repository: code, comments and commits in English.

## Critical invariants

- R1 the server never writes to TIX_HOME itself.
- R2 a request with a foreign Host (or Origin, on writes) is refused.
- R3 raw HTML from a task never reaches the page as markup.
- R4 tix runs with an argument array, never through a shell.
- R5 tests never touch `~/tasks`: TIX_HOME points at a temporary folder.
