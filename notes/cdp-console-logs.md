# CDP console log capture — findings

Research and empirical testing done while implementing `rodney logs`.

## The three CDP mechanisms

### 1. `Runtime.consoleAPICalled` (Runtime domain)

- Fires for every `console.log/warn/error/debug/info/...` call made by JavaScript
  running in the page, including calls made via `Runtime.evaluate`
- **Live only** — no cross-session replay
- When `Runtime.enable` is called in a new CDP session, Chrome does *not* replay
  previous `consoleAPICalled` events to that session
- This is what rodney uses for **follow mode** (`rodney logs -f`) and what
  Playwright uses for `page.on('console')`

### 2. `Log.entryAdded` (Log domain)

- Fires for **browser-generated** log entries: network errors, CSP violations,
  mixed-content warnings, deprecation notices, intervention messages, etc.
- Does **not** fire for JavaScript `console.*` API calls (those go exclusively
  to `Runtime.consoleAPICalled`)
- `Log.enable` does replay buffered browser-level entries to new sessions
- This is what rodney uses for **snapshot mode** (`rodney logs`)

### 3. `Console.messageAdded` (Console domain — deprecated)

- The older, deprecated predecessor to the Runtime + Log split
- Replays collected messages on `Console.enable`, per the CDP spec
- In practice (Chrome ~124+) empirically found to replay 0 messages, consistent
  with the Log domain behaviour above
- Playwright never uses this domain

## Why `rodney js "console.log(...)"` doesn't appear in `rodney logs`

Each `rodney` invocation is a **separate OS process with a separate CDP session**.

When `rodney js "console.log('hello')"` runs:
1. A new CDP session is opened
2. `Runtime.evaluate` is called — `console.log` fires
3. The session disconnects

When `rodney logs` runs next:
1. A new CDP session is opened
2. `Runtime.enable` is called
3. Chrome does **not** replay the previous session's `consoleAPICalled` events

There is no CDP mechanism that makes cross-session `console.*` replay possible.
We tested and confirmed: `Runtime.disable` + `Runtime.enable`, `Log.enable`, and
`Console.enable` all return 0 events for messages produced by a prior session's
`Runtime.evaluate`.

## How Playwright avoids this problem

Playwright maintains a **persistent CDP connection** for the entire test/automation
lifetime. `page.on('console')` hooks into `Runtime.consoleAPICalled` on that
same persistent session, so it captures every `console.*` call — including those
from `page.evaluate(...)` — because they all happen within the same session.

Relevant detail from Playwright source (`crPage.ts`): when `Runtime.enable` is
first called, Chrome *does* replay the last ~1000 console messages with
`executionContextId = 0`. Playwright explicitly **drops** these replays because
(a) the original execution context is gone so args can't be inspected, and
(b) they arrive before user listeners are registered anyway.

```typescript
// crPage.ts – FrameSession._onConsoleAPI
if (event.executionContextId === 0) {
  // DevTools protocol stores the last 1000 console messages…
  // Ignore these messages since there's no execution context we can use.
  return;
}
```

## Considered workaround: JS interceptor

We briefly implemented a `window.__rodney_logs` buffer — `rodney logs` would
inject an override of `console.*` that stores entries in `window.__rodney_logs`,
and subsequent invocations would read and clear that buffer.

This worked but was rejected because:
- It mutates the page's JavaScript environment
- It only captures messages after the first `rodney logs` invocation
- It feels wrong for a debugging tool to modify what it observes

## Key discovery: `Runtime.consoleAPICalled` is NOT broadcast cross-session

Empirically confirmed: when Session A calls `Runtime.evaluate` and the evaluated
code calls `console.log`, Chrome delivers the `Runtime.consoleAPICalled` event
**only to Session A**. A separate Session B with `Runtime.enable` active on the
same page target does **not** receive the event.

This means the background `_logger` approach (which connects via its own CDP
session) **cannot capture `console.log` calls made through `rodney js`**.

Page-native console calls (inline scripts, timers, event handlers — anything
that runs outside of `Runtime.evaluate`) *are* broadcast to all sessions with
Runtime enabled. So the logger does capture those, just not CDP-evaluate events.

Test used to confirm (simplified):
```go
// Session A: registers EachEvent + RuntimeEnable, blocks waiting for events
// Session B: calls page.MustEval(`() => { console.log("cross-session-test") }`)
// Result: Session A receives nothing → timeout
```

## Considered alternative: Chrome `--enable-logging` file

Chrome supports `--enable-logging --log-level=0` which writes all JavaScript
`console.*` calls to `<userDataDir>/chrome_debug.log`, regardless of which CDP
session triggered them. This solves the cross-session problem entirely.

Log line format:
```
[PID:TID:DATE:INFO:CONSOLE(N)] "message text", source: https://example.com (42)
```

Empirically confirmed (Chrome ~128):
- Captures `console.log`, `console.warn`, `console.error` from inline scripts ✓
- Captures `console.log` fired via `Runtime.evaluate` in a separate session ✓
- All levels (`log`, `warn`, `error`) map to `INFO:CONSOLE` — **no severity info** ✗
- Single file for the entire browser session — not scoped per page ✗
  (URL in the `source:` field enables filtering, but adds complexity)
- Requires baking `--enable-logging` into `rodney start` ✗

Rejected because losing level info (`[info]`/`[warning]`/`[error]`) is a
meaningful regression, and the per-page scoping would need extra work.
Preserved here as a viable fallback if the current approach proves insufficient.

## Current implementation (in-process capture in `cmdJS`)

The background `_logger` subprocess approach was implemented and then removed.
It was unnecessary because the primary use case — capturing `console.*` calls
made via `rodney js` — cannot be served by a separate CDP session anyway (see
"Key discovery" above). Page-native console events between CLI invocations are
not a common enough use case to justify the complexity.

Instead, `rodney js` captures console events **in-process**, within the same CDP
session that runs the `Runtime.evaluate` call:

1. Before evaluating, open (or create) `<stateDir>/logs/<targetID>.ndjson`
2. Register an `EachEvent` handler for `Runtime.consoleAPICalled`
3. Enable the Runtime domain and start the event loop (`go wait()`)
4. Evaluate the JS expression
5. Sleep 50 ms to let the event goroutine flush any queued events
6. Close the log file

`rodney logs` reads the NDJSON file directly — no CDP session required.

| Mode | Mechanism | What it captures |
|------|-----------|-----------------|
| `rodney logs` | Read NDJSON file | All `console.*` calls made via `rodney js` |
| `rodney logs -f` | Tail NDJSON file | Same, plus new entries as they arrive |
| `rodney logs -n N` | Read last N lines from NDJSON file | Last N `console.*` calls |

Log files live at `<stateDir>/logs/<targetID>.ndjson` and persist until the
state directory is cleaned up manually or a new session is started.
