# tix-web

A local web board for the [tix](https://github.com/daniil4545/tix) task tracker: one binary on
127.0.0.1 that reads and changes tasks only through the `tix` CLI.

## Stage

Prototype. Works: the board with a task panel that polls every 5 s, moving cards through `tix move`, and
editing a task in the panel. Coming next: new tasks.

## Quick start

Requirements: Go 1.27.4 or newer, tix 0.9.1 in `PATH`.

1. `go install github.com/daniil4545/tix-web@latest`
2. `tix-web` prints `tix-web: http://127.0.0.1:8737`
3. Open that address. `curl -s http://127.0.0.1:8737/api/board` returns statuses, projects and tasks

Flags: `-addr 127.0.0.1:PORT` (only 127.0.0.1 is accepted), `-tix PATH` for a tix binary outside
`PATH`. The store is the one tix uses: `TIX_HOME`, `~/tasks` by default.

Tests and linters: `make check`. The full test tier needs tix 0.9.1; `make test-fast` runs on a fake.
`make browser-check` drives the board in system Chrome on a clone of `~/tasks` (needs uv).

## API

| Request | tix call |
|---|---|
| `GET /api/board` | `tix status`, `tix projects`, `tix list --all --json --project ""` |
| `GET /api/tasks/T-N` | `tix show T-N -v --json`, plus `body_html` and `html` of each note and comment |
| `POST /api/tasks/T-N/move` `{status, after, first, message}` | `tix move T-N STATUS --as human [--after T-M \| --first] [-m MESSAGE]`; no placement keeps the tix default, the end of the column |
| `PATCH /api/tasks/T-N` `{title, tags}` | `tix set T-N title=... tags=... --as human`; tags are one comma-separated string |
| `PUT /api/tasks/T-N` `{rev, body}` | `tix edit T-N --base REV --as human` with the body on stdin; a changed body is exit 3, HTTP 409 |
| `POST /api/tasks/T-N/check`, `/uncheck` `{n}` | `tix check` or `tix uncheck T-N N --as human` |
| `POST /api/tasks/T-N/comment` `{text}` | `tix comment T-N --as human -- TEXT` |

A tix failure comes back as `{"error": "<tix text>", "code": <exit code>}`: exit 3 and 5 are 409,
4 is 404, 6 is 400, anything else is 500.

## Security

- Listens on 127.0.0.1 only and answers only to `Host` 127.0.0.1:PORT or localhost:PORT (DNS rebinding).
- Writes from another origin are refused (`http.CrossOriginProtection`).
- Markdown is rendered on the server by goldmark without raw HTML, so a `<script>` in a task stays text.
- tix runs with an argument array, never through a shell; the server never touches task files itself.

## Documents

| Question | Where |
|---|---|
| rules for agents | `AGENTS.md` |
| what changed and when | `git log` |
