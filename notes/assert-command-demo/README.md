# rodney assert: asserting JavaScript expressions

*2026-02-17T21:46:40Z by Showboat 0.6.0*
<!-- showboat-id: 6dd55f0e-350e-4323-9650-92bdf2739734 -->

The new `rodney assert` command lets you assert JavaScript expressions directly from the command line. It supports two modes: **truthy mode** (one argument) checks that an expression evaluates to a truthy value, and **equality mode** (two arguments) checks that the result matches an expected string. Both modes use exit code 0 for pass and exit code 1 for failure, consistent with the other check commands (`exists`, `visible`, `ax-find`).

We will test against a small Starlette app (`demo_app.py` in this directory) that serves a task tracker page with a title, heading, task list, and login indicator. Start it with `uv run demo_app.py`.

Start the browser and open the demo app.

```bash
./rodney start 2>/dev/null | grep -v "^Auth proxy\|^Debug URL" && ./rodney open http://127.0.0.1:18092/
```

```output
Chrome started (PID 18362)
Task Tracker
```

## Truthy mode (one argument)

With a single argument, `rodney assert` evaluates the JavaScript expression and exits 0 if the result is truthy, or exits 1 if falsy (false, 0, null, undefined, or empty string).

Check that a `.logged-in` element exists on the page:

```bash
./rodney assert "document.querySelector('.logged-in')" && echo "exit code: $?"
```

```output
pass
exit code: 0
```

A boolean expression evaluates to `true` which is truthy:

```bash
./rodney assert "document.title === 'Task Tracker'" && echo "exit code: $?"
```

```output
pass
exit code: 0
```

When the expression evaluates to a falsy value — `null`, `false`, `0`, `undefined`, or an empty string — the command prints a failure message and exits 1.

Query for a nonexistent element (returns `null`):

```bash
./rodney assert "document.querySelector('.nonexistent')"; echo "exit code: $?"
```

```output
fail: got null
exit code: 1
```

A boolean `false`:

```bash
./rodney assert "1 === 2"; echo "exit code: $?"
```

```output
fail: got false
exit code: 1
```

## Equality mode (two arguments)

With two arguments, `rodney assert` evaluates the expression and compares its string representation to the expected value. The formatting matches what `rodney js` would output — strings are unquoted, numbers are plain digits.

Check the page title:

```bash
./rodney assert "document.title" "Task Tracker" && echo "exit code: $?"
```

```output
pass
exit code: 0
```

Count the number of tasks:

```bash
./rodney assert "document.querySelectorAll('.task').length" "3" && echo "exit code: $?"
```

```output
pass
exit code: 0
```

Check the text content of the heading:

```bash
./rodney assert "document.querySelector('h1').textContent" "Task Tracker" && echo "exit code: $?"
```

```output
pass
exit code: 0
```

Read a data attribute:

```bash
./rodney assert "document.querySelector('.logged-in').dataset.user" "alice" && echo "exit code: $?"
```

```output
pass
exit code: 0
```

When the values do not match, the failure message shows both the actual and expected values for easy debugging:

```bash
./rodney assert "document.title" "Wrong Title"; echo "exit code: $?"
```

```output
fail: got "Task Tracker", expected "Wrong Title"
exit code: 1
```

```bash
./rodney assert "document.querySelectorAll('.task').length" "5"; echo "exit code: $?"
```

```output
fail: got "3", expected "5"
exit code: 1
```

## Asserting across pages

Navigate to the About page and assert its title, then navigate back and re-check.

```bash
./rodney open http://127.0.0.1:18092/about && ./rodney assert "document.title" "About - Task Tracker"
```

```output
About - Task Tracker
pass
```

```bash
./rodney back && ./rodney assert "document.title" "Task Tracker"
```

```output
http://127.0.0.1:18092/
pass
```

## Combining assert with other check commands

The `assert` command uses exit code 1 for failures, just like `exists`, `visible`, and `ax-find`. This means it works naturally with the `check` helper pattern for running multiple assertions without aborting on the first failure.

```bash
FAIL=0
check() {
    "$@" 2>/dev/null || { echo "FAIL: $*"; FAIL=1; }
}

# These pass
check ./rodney exists "h1"
check ./rodney visible "h1"
check ./rodney assert "document.title" "Task Tracker"
check ./rodney assert "document.querySelectorAll('.task').length" "3"
check ./rodney assert "document.querySelector('.logged-in').dataset.user" "alice"

# These fail
check ./rodney assert "document.title" "Wrong Title"
check ./rodney assert "document.querySelectorAll('.task').length" "10"
check ./rodney assert "document.querySelector('.nonexistent')"

if [ "$FAIL" -ne 0 ]; then
    echo "---"
    echo "Some checks failed"
else
    echo "All checks passed"
fi

```

```output
true
true
pass
pass
pass
fail: got "Task Tracker", expected "Wrong Title"
FAIL: ./rodney assert document.title Wrong Title
fail: got "3", expected "10"
FAIL: ./rodney assert document.querySelectorAll('.task').length 10
fail: got null
FAIL: ./rodney assert document.querySelector('.nonexistent')
---
Some checks failed
```

All five passing checks ran silently, while the three failing checks printed diagnostics with the actual vs expected values. The script collected all failures before reporting, rather than aborting on the first one.

Stop the browser.

```bash
./rodney stop
```

```output
Chrome stopped
```
