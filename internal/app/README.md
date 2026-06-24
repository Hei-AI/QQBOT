# Go Kagami App Layout

This directory mirrors the TypeScript app-level assembly layer.

- `internal/agentruntime`: generic loop, event queue, ReAct kernel, tool catalog.
- `internal/agent/context`: root agent context and compaction helpers.
- `internal/agent/root`: root agent runtime, session, and control tools.
- `internal/capabilities/*`: messaging, news, story, terminal, vision, web-search, auth.
- `web/static`: bundled management console served from `/`.

The root package still owns executable wiring so the current project remains easy to run with
`go run .` while the TS feature domains now have corresponding Go folders.
