# Changelog

## [0.1.0] - 2026-11-13

First release: the minimal path of a task in the browser over tix 0.9.1.

### Added

- Board on 127.0.0.1:8737: columns in `tix status` order, cards with tags, claim, last note and epic
  progress, polling every 5 s. Closed shows the newest 12 first.
- Task panel: the body as Markdown, notes and comments in one feed, the PR link.
- Drag and drop through `tix move`: a reason for blocked, a result for closed; a tix refusal shows its
  text and the card goes back.
- Editing in the panel: title, tags, body with an edit conflict that keeps the owner's text, criteria
  checkboxes, a comment to the agent. Closing the panel asks before it drops unsaved text.
- New task form: catalog projects and `personal`, idea or open, a body template with one criterion.
- Safety: only 127.0.0.1, a Host check, cross-origin writes refused, raw HTML from tasks dropped, tix
  run with an argument array and `--as human`.

[0.1.0]: https://github.com/daniil4545/tix-web/releases/tag/v0.1.0
