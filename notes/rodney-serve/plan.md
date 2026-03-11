# Plan: Implement `rodney serve` command

## Summary

Add a `rodney serve` command that runs a long-lived process communicating over newline-delimited JSON on stdin/stdout. This enables the Python library to own Chrome's lifecycle via pipe-based crash safety, and eliminates per-command subprocess + WebSocket reconnection overhead.

This also involves refactoring the existing command handlers so they can be shared between CLI mode and serve mode, and splitting the single `main.go` into multiple files.

## Protocol

Newline-delimited JSON over stdin (requests) and stdout (responses):

```
→ {"id": 1, "cmd": "open", "args": ["https://example.com"]}
← {"id": 1, "ok": true, "stdout": "Example Domain", "stderr": ""}

→ {"id": 2, "cmd": "click", "args": ["#missing"]}
← {"id": 2, "ok": false, "exit_code": 2, "stdout": "", "stderr": "error: element not found"}
```

## Implementation Steps

### Step 1: Split main.go into multiple files

The current `main.go` is ~1970 lines. Split into:

| File | Contents |
|------|----------|
| `main.go` | Entry point (`main()`), command dispatch switch, scope/state types and helpers (`State`, `loadState`, `saveState`, `stateDir`, `withPage`, `fatal`, etc.), `help.txt` embed |
| `commands.go` | All `cmdXxx` functions for browser commands (open, click, title, html, js, screenshot, pages, etc.) |
| `accessibility.go` | Accessibility commands (`cmdAXTree`, `cmdAXFind`, `cmdAXNode`) and all AX formatting helpers (`formatAXTree`, `formatAXNodeList`, `queryAXNodes`, `getAXNode`, `axValueStr`, `formatProperties`, etc.) |
| `proxy.go` | Auth proxy code (`detectProxy`, `cmdInternalProxy`, `proxyConnect`, `proxyHTTP`) |
| `helpers.go` | Utility functions (`decodeDataURL`, `inferDownloadFilename`, `mimeToExt`, `nextAvailableFile`, `parseAssertArgs`, `formatAssertFail`) |
| `serve.go` | The new serve command (step 4 below) |

Platform files stay as-is: `proc_unix.go`, `proc_windows.go`.

All files remain in `package main` so they share types freely.

**Test files split:**

| File | Contents |
|------|----------|
| `main_test.go` | `TestMain`, `testEnv` setup, HTML fixtures, existing browser command tests |
| `serve_test.go` | Serve-mode tests (new) |
| `helpers_test.go` | Unit tests for pure helper functions (if any exist or are added) |

### Step 2: Refactor command handlers to share logic between CLI and serve mode

The current `cmdXxx` functions are tightly coupled to CLI concerns:
- They call `withPage()` which loads state from disk and reconnects each time
- They call `fatal()` which prints to stderr and calls `os.Exit(2)`
- They print output directly to `os.Stdout`
- Some call `os.Exit(0)` or `os.Exit(1)` for non-error exit codes (exists, visible, assert)

**Refactoring approach:** Extract the core logic from each command into an inner function that takes the browser/page/state it needs and returns results via `(string, error)` or similar. The existing `cmdXxx` CLI wrapper calls the inner function and handles fatal/stdout/exit. The serve dispatcher calls the same inner function and captures the result into a JSON response.

**Concrete pattern for most commands:**

```go
// Inner function — shared logic, no side effects
func runTitle(page *rod.Page) (string, error) {
    info, err := page.Info()
    if err != nil {
        return "", fmt.Errorf("failed to get page info: %w", err)
    }
    return info.Title, nil
}

// CLI wrapper — calls inner function, handles output/exit
func cmdTitle(args []string) {
    _, _, page := withPage()
    result, err := runTitle(page)
    if err != nil {
        fatal("%v", err)
    }
    fmt.Println(result)
}
```

**Categories of commands by signature pattern:**

1. **Simple page commands** (title, url, back, forward, waitload, waitstable, waitidle, clear-cache, reload): Take `page` (and sometimes `args`), return `(string, error)`. ~15 commands.

2. **Element commands** (click, input, clear, text, attr, hover, focus, wait, html): Take `page` + parsed args, return `(string, error)`. ~10 commands.

3. **State-mutating commands** (open, page/switch, newpage, closepage): Take `browser` + `state` + args, need to update `ActivePage`. Return `(string, error)` and mutate state in-place. In CLI mode, `saveState()` persists after the call. In serve mode, state is in-memory so mutation is enough. ~4 commands.

4. **Exit-code commands** (exists, visible, assert): Return `(string, int, error)` where int is the exit code (0 or 1). CLI wrapper maps to `os.Exit()`. Serve wrapper maps to `exit_code` in the JSON response. ~3 commands.

5. **File-producing commands** (screenshot, screenshot-el, pdf, download): Take page + args, produce files and return `(string, error)`. ~4 commands.

6. **Special commands** (js, pages, status, sleep): Various signatures. ~4 commands.

**Key rule:** The `runXxx` inner functions must never call `fatal()`, `os.Exit()`, or print to stdout/stderr. All output goes through return values. All errors go through returned `error`.

**Handling the state-mutating commands:** Commands like `open`, `newpage`, `closepage`, `page` need to update `ActivePage`. In CLI mode this means calling `saveState()`. In serve mode the state is in-memory. Solution: the inner function mutates `*State` in-place. The CLI wrapper calls `saveState(s)` after. The serve mode doesn't need to do anything extra since state is already in-memory.

We can define a `session` interface or struct that both modes use:

```go
// session holds the browser connection + mutable state for one command execution.
// In CLI mode, this is built fresh each time by withPage().
// In serve mode, this persists across commands.
type session struct {
    browser *rod.Browser
    state   *State
    page    *rod.Page  // active page with timeout applied
}
```

The `withPage()` function returns a `*session`. Inner functions take `*session`.

### Step 3: Define serve request/response types (in serve.go)

```go
type serveRequest struct {
    ID   int      `json:"id"`
    Cmd  string   `json:"cmd"`
    Args []string `json:"args,omitempty"`
}

type serveResponse struct {
    ID       int    `json:"id"`
    OK       bool   `json:"ok"`
    Stdout   string `json:"stdout,omitempty"`
    Stderr   string `json:"stderr,omitempty"`
    ExitCode int    `json:"exit_code,omitempty"`
}
```

### Step 4: Implement `cmdServe` (in serve.go)

The core function that:

1. **Parses flags** — same as `start`: `--show`, `--insecure`/`-k`.
2. **Launches Chrome** — reuses same launcher logic from `cmdStart`, but with `Leakless(true)` so Chrome dies when rodney dies. No `state.json` written — state lives in-memory only.
3. **Connects and keeps connection open** — one `rod.Browser` + persistent `session` struct.
4. **Reads stdin line-by-line** via `bufio.Scanner`, decoding each line as a `serveRequest`.
5. **Dispatches each request** to the appropriate `runXxx` function via a switch, capturing stdout/stderr into the response.
6. **Writes JSON response** to stdout (one line per response).
7. **On stdin EOF** (scanner.Scan() returns false): kills Chrome, exits. This is the crash-safety mechanism — when Python dies, OS closes the pipe, Go reads EOF.

```go
func cmdServe(args []string) {
    // Parse flags (--show, --insecure)
    // Launch Chrome with Leakless(true)
    // Connect, create session
    // Print ready line: {"ok": true} so caller knows Chrome is up
    // Main loop: scanner.Scan() → decode → dispatch → encode → write
    // On EOF: browser.MustClose(), exit
}
```

**Dispatch function:**

```go
func dispatchServe(sess *session, cmd string, args []string) (stdout string, stderr string, exitCode int) {
    // Refresh the active page reference (page list may have changed)
    // Switch on cmd, call the appropriate runXxx function
    // Catch panics from Must* calls via recover()
    // Map errors to stderr + exitCode=2
    // Map exit-code results (exists/visible/assert) to exitCode=0 or 1
}
```

### Step 5: Add "serve" to the command dispatcher in main()

```go
case "serve":
    cmdServe(args)
```

### Step 6: Handle `start` and `stop` within serve mode

- `start` is implicit — Chrome launches when `rodney serve` starts. A `{"ok": true}` ready message is written to stdout before the main loop begins, so the caller knows Chrome is ready.
- `stop` = either close stdin (from Python side, triggering EOF) or send `{"cmd": "stop"}` which triggers graceful shutdown and writes a final response before exiting.

### Step 7: Tests (in serve_test.go)

Tests launch `rodney serve` as a subprocess with piped stdin/stdout and send/receive JSON:

- **Basic command flow**: open a URL, check title, get html, click, etc.
- **Error handling**: send a command for a missing element, verify error response with `ok: false` and `exit_code: 2`.
- **Exit-code commands**: exists/visible/assert return `exit_code: 1` for false/fail.
- **Stdin EOF cleanup**: close stdin, verify the process exits within a timeout.
- **Stop command**: send `{"cmd": "stop"}`, verify graceful shutdown.

### Step 8: Update help.txt

Add `serve` to the help text:

```
  serve [--show] [--insecure]   Start long-lived JSON-over-stdio session
```

## File Changes Summary

| File | Change |
|------|--------|
| `main.go` | Slim down to entry point + dispatch + state helpers. Add `session` struct. Refactor `withPage()` to return `*session`. Add `"serve"` case. |
| `commands.go` | **New file.** Move all `cmdXxx` functions here. Extract `runXxx` inner functions. CLI wrappers become thin: call `runXxx`, handle fatal/stdout/exit. |
| `accessibility.go` | **New file.** Move AX commands + all formatting helpers. |
| `proxy.go` | **New file.** Move `detectProxy`, `cmdInternalProxy`, `proxyConnect`, `proxyHTTP`. |
| `helpers.go` | **New file.** Move `decodeDataURL`, `inferDownloadFilename`, `mimeToExt`, `nextAvailableFile`, `parseAssertArgs`, `formatAssertFail`. |
| `serve.go` | **New file.** `serveRequest`/`serveResponse` types, `cmdServe`, `dispatchServe`. |
| `help.txt` | Add `serve` command. |
| `serve_test.go` | **New file.** Serve-mode tests. |
| `main_test.go` | Existing tests remain here (may need minor imports adjustment after file split). |

## Execution Order

1. **Split files first** (pure move, no logic changes) — verify all existing tests pass.
2. **Extract `runXxx` inner functions** from `cmdXxx` — verify all existing tests still pass after each batch.
3. **Implement `cmdServe`** using the refactored `runXxx` functions.
4. **Add serve tests.**
5. **Update help.txt.**

## What's NOT in scope

- Multi-session / browser contexts (future enhancement per the design doc)
- Python library changes (separate repo/effort)
- Any changes to existing CLI command behavior or test outcomes
