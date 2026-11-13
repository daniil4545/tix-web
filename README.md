# tix-web

A local web board for the [tix](https://github.com/daniil4545/tix) task tracker: one binary on
127.0.0.1 that reads and changes tasks only through the `tix` CLI.

## Stage

Prototype. Works: the server, the read API and a placeholder page that lists tasks by status.
Coming next: the board and the task panel, moving cards, editing, new tasks.

## Quick start

Requirements: Go 1.27.2 or newer, tix 0.9.1 in `PATH`.

1. `go install github.com/daniil4545/tix-web@latest`
2. `tix-web` prints `tix-web: http://127.0.0.1:8737`
3. Open that address. `curl -s http://127.0.0.1:8737/api/board` returns statuses, projects and tasks

Flags: `-addr 127.0.0.1:PORT` (only 127.0.0.1 is accepted), `-tix PATH` for a tix binary outside
`PATH`. The store is the one tix uses: `TIX_HOME`, `~/tasks` by default.

Tests and linters: `make check`. The full test tier needs tix 0.9.1; `make test-fast` runs on a fake.

## API

| Request | tix call |
|---|---|
| `GET /api/board` | `tix status`, `tix projects`, `tix list --all --json --project ""` |
| `GET /api/tasks/T-N` | `tix show T-N -v --json`, plus `body_html` and `html` of each note and comment |

A tix failure comes back as `{"error": "<tix text>", "code": <exit code>}`: exit 3 and 5 are 409,
4 is 404, 6 is 400, anything else is 500.

## Security

- Listens on 127.0.0.1 only and answers only to `Host` 127.0.0.1:PORT or localhost:PORT (DNS rebinding).
- Markdown is rendered on the server by goldmark without raw HTML, so a `<script>` in a task stays text.
- tix runs with an argument array, never through a shell; the server never touches task files itself.

## Documents

| Question | Where |
|---|---|
| rules for agents | `AGENTS.md` |
| what changed and when | `git log` |
