# Design: Console Log Reading

## Motivation

Rodney currently has no way to observe JavaScript console output (`console.log`, `console.warn`, `console.error`, etc.) or browser-level log messages (network errors, security warnings). This is a significant gap for debugging and automation workflows. A user scripting a multi-step interaction has no visibility into what the page is logging unless they manually run `rodney js "..."` to poll for state.

### Use cases

1. **Debug a click handler**: Run `rodney click "#submit"`, then check what the page logged.
2. **Monitor a live page**: Stream console output in real-time while interacting in another terminal.
3. **Detect JS errors**: Check if a page has any `console.error` calls after loading.
4. **Capture logs during a scripted workflow**: Collect all console output across a sequence of rodney commands for later analysis.

## Key constraint: rodney's architecture

Each `rodney <cmd>` invocation is a **short-lived process** that connects to Chrome, runs one operation, and disconnects. Console log events (`Runtime.consoleAPICalled`) are **real-time CDP events**—they only fire while a client is actively subscribed. There is no CDP method to retrieve past console messages after the fact.

This means a simple "connect and read" approach cannot capture logs that happened during a previous command. Two strategies can work within rodney's architecture:

1. **Foreground streaming** — connect, subscribe, and print events as they arrive (blocking).
2. **Background collector** — a persistent subprocess (like the existing `_proxy` helper) that stays connected to Chrome and writes events to a log file that other commands can read.

## Proposed commands

### Phase 1: Real-time streaming

```
rodney console                     # Stream console output (blocking, Ctrl+C to stop)
rodney console --json              # Stream as JSON lines
rodney console --level <level>     # Filter: log, warn, error, info, debug
rodney console --browser           # Also include browser-level log entries
```

This is the simplest useful feature. It connects to Chrome, subscribes to `Runtime.consoleAPICalled` (and optionally `Log.entryAdded`), and prints messages to stdout as they arrive. The process blocks until interrupted.

**Output format (default):**
```
[log] hello world
[warn] deprecated API used
[error] TypeError: Cannot read property 'foo' of undefined
[info] request completed in 142ms
```

**Output format (`--json`):**
```json
{"type":"log","timestamp":1707566400000,"args":["hello","world"]}
{"type":"error","timestamp":1707566401000,"args":["TypeError: Cannot read property 'foo' of undefined"]}
```

The `--json` format uses JSON lines (one JSON object per line) for easy piping to `jq` and other tools.

**Scope:** Listens on the active page only. Switching pages with `rodney page <N>` in another terminal does not affect which page the running `console` command is monitoring. If the page navigates, events from the new page are still received (same CDP target).

### Phase 2: Background collector

```
rodney console-start               # Start background console collector
rodney console                     # Read collected messages (non-blocking)
rodney console --follow            # Tail mode: print collected + stream new (blocking)
rodney console --clear             # Read and clear the buffer
rodney console --json              # JSON output
rodney console --level <level>     # Filter by level
rodney console-stop                # Stop background collector
```

The background collector is a persistent subprocess that writes console events to a JSONL file. This captures logs that happen between CLI invocations, solving the primary architectural challenge.

#### How it works

1. `rodney console-start` spawns a hidden subprocess (`rodney _console <page-target-id>`) that:
   - Connects to Chrome via the debug WebSocket URL
   - Subscribes to `Runtime.consoleAPICalled` and `Log.entryAdded` on the active page
   - Appends each event as a JSON line to `~/.rodney/console.jsonl`
   - Runs until killed

2. `rodney console` reads `~/.rodney/console.jsonl` and prints the entries. Without `--follow`, it prints what's collected so far and exits. With `--follow`, it tails the file and also streams new entries.

3. `rodney console-stop` kills the collector process and optionally cleans up the log file.

This mirrors the existing `_proxy` helper pattern. The collector PID and log path are stored in the state file.

#### State file additions

```json
{
  "debug_url": "ws://...",
  "chrome_pid": 12345,
  "active_page": 0,
  "data_dir": "/root/.rodney/chrome-data",
  "console_pid": 12346,
  "console_log": "/root/.rodney/console.jsonl"
}
```

`rodney stop` cleans up the collector process alongside Chrome (just as it already does for the proxy helper).

#### JSONL log format

Each line in `console.jsonl`:

```json
{"source":"console","type":"log","timestamp":1707566400000,"args":["hello","world"],"url":"https://example.com/app.js","line":42,"column":10}
{"source":"console","type":"error","timestamp":1707566401000,"args":["TypeError: ..."],"url":"https://example.com/app.js","line":87,"column":5}
{"source":"browser","level":"warning","timestamp":1707566402000,"text":"Mixed Content: ...","url":"https://example.com"}
```

Fields:
- `source`: `"console"` for `Runtime.consoleAPICalled`, `"browser"` for `Log.entryAdded`
- `type` (console): the `console.*` method — `log`, `warn`, `error`, `info`, `debug`, `trace`, etc.
- `level` (browser): the browser log level — `verbose`, `info`, `warning`, `error`
- `timestamp`: milliseconds since epoch (from the CDP event)
- `args` (console): array of serialized argument values
- `text` (browser): the log message string
- `url`, `line`, `column`: source location when available

#### Buffer management

The log file grows unbounded by default. Options for managing size:

- `rodney console --clear` truncates the file after reading.
- A `--max-lines N` flag on `console-start` could cap the buffer (drop oldest entries). Default: 10,000 lines.
- `rodney console-stop` removes the log file.

## Implementation details

### CDP events used

**`Runtime.consoleAPICalled`** (`proto.RuntimeConsoleAPICalled` in rod):
- Fires for every `console.log()`, `console.warn()`, `console.error()`, `console.info()`, `console.debug()`, etc.
- `e.Type` identifies the method (`RuntimeConsoleAPICalledTypeLog`, `...TypeError`, etc.)
- `e.Args` is `[]*proto.RuntimeRemoteObject` — convert with `page.MustObjectsToJSON(e.Args)`
- `e.Timestamp` provides the event time
- `e.StackTrace` provides source location

**`Log.entryAdded`** (`proto.LogEntryAdded` in rod):
- Fires for browser-level messages: network errors, security warnings, deprecation notices, etc.
- `e.Entry.Level` is `verbose`, `info`, `warning`, or `error`
- `e.Entry.Source` identifies the origin (network, security, etc.)
- `e.Entry.Text` is the message
- `e.Entry.URL` and `e.Entry.LineNumber` provide source location

### Rod API usage

The streaming implementation uses `page.EachEvent()`:

```go
wait := page.EachEvent(
    func(e *proto.RuntimeConsoleAPICalled) {
        args := page.MustObjectsToJSON(e.Args)
        fmt.Printf("[%s] %s\n", e.Type, args)
    },
    func(e *proto.LogEntryAdded) {
        fmt.Printf("[%s/%s] %s\n", e.Entry.Source, e.Entry.Level, e.Entry.Text)
    },
)
wait() // blocks until context cancelled
```

Rod automatically enables the required CDP domains (`Runtime`, `Log`) when `EachEvent` is called, and disables them on cleanup.

### Background collector subprocess

The collector runs as `rodney _console <debug-url> <log-path>` (hidden subcommand, like `_proxy`). It:

1. Connects to Chrome using the debug URL
2. Attaches to the active page target
3. Subscribes to `RuntimeConsoleAPICalled` and `LogEntryAdded`
4. Opens the log file in append mode
5. For each event, serializes to JSON and writes one line
6. Handles SIGTERM for clean shutdown

The parent process detaches it with `SysProcAttr{Setsid: true}` and stores the PID, identical to the proxy helper pattern.

## Alternatives considered

### JavaScript-based collection

Inject a script via `page.Eval()` that monkey-patches `console.*` methods to buffer messages into `window.__rodneyConsole`, then read the buffer in a subsequent command.

**Rejected because:**
- Buffer is lost on page navigation (no way to persist `EvalOnNewDocument` across disconnections since rodney reconnects each time)
- Monkey-patching `console` can interfere with page behavior and frameworks that depend on console internals
- Cannot capture browser-level log entries
- Adds complexity for the user (must remember to "start" collection before the logs happen)

The CDP background collector is strictly more capable and avoids these problems.

### Always-on collection in `rodney start`

Start the console collector automatically whenever Chrome launches.

**Deferred because:**
- Not all users need console logs — adds overhead and disk usage by default
- Can be revisited as a `rodney start --console` flag if the feature proves popular

## Command behavior summary

| Command | Phase | Blocking | Description |
|---------|-------|----------|-------------|
| `rodney console` (no collector) | 1 | Yes | Stream events in real-time until Ctrl+C |
| `rodney console` (collector running) | 2 | No | Print buffered messages and exit |
| `rodney console --follow` | 2 | Yes | Print buffered + stream new events |
| `rodney console --json` | 1+2 | - | JSON lines output |
| `rodney console --level <L>` | 1+2 | - | Filter by console method / log level |
| `rodney console --browser` | 1+2 | - | Include browser-level log entries |
| `rodney console --clear` | 2 | No | Print and clear the buffer |
| `rodney console-start` | 2 | No | Launch background collector |
| `rodney console-stop` | 2 | No | Stop background collector |

Note: When no background collector is running, `rodney console` defaults to Phase 1 behavior (real-time streaming). When a collector is running, it defaults to Phase 2 behavior (read buffer). The `--follow` flag opts into blocking/streaming mode regardless.

## Shell scripting examples

```bash
# Real-time monitoring (Phase 1)
rodney console &                   # Stream in background
CONSOLE_PID=$!
rodney open https://example.com
rodney click "#load-data"
rodney sleep 2
kill $CONSOLE_PID                  # Stop streaming

# Capture logs across a workflow (Phase 2)
rodney console-start
rodney open https://example.com
rodney click "#submit"
rodney waitstable
rodney console                     # Print everything that was logged
rodney console-stop

# Check for errors after page load
rodney console-start
rodney open https://example.com
rodney waitstable
errors=$(rodney console --level error)
rodney console-stop
if [ -n "$errors" ]; then
    echo "JS errors detected:"
    echo "$errors"
    exit 1
fi

# Pipe JSON output to jq
rodney console --json --follow | jq 'select(.type == "error")'
```
