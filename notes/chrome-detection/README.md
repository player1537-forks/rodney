# Chrome Detection and Environment Variables

*2026-02-11T09:08:33Z*

Rodney can find and use a preinstalled Chrome or Chromium instead of downloading its own via the rod library. This demo shows how Chrome detection works and how the environment variables control it.

## Default: Preinstalled Chrome Detection

By default, rodney searches for Chrome in this order:

1. `ROD_CHROME_BIN` environment variable (explicit path)
2. System Chrome via standard paths (`/usr/bin/google-chrome`, `/usr/bin/chromium`, etc.)
3. Playwright's cache (`~/.cache/ms-playwright/chromium-*/chrome-linux/chrome`)
4. Rod's auto-download (downloads Chromium to `~/.cache/rod/`)

In this environment there's no system Chrome, but Playwright's Chromium is available.

Confirm there's no system Chrome in the standard locations:

```bash
which google-chrome chromium chromium-browser 2>&1 || echo "No system Chrome found"
```

```output
No system Chrome found
```

But Playwright's Chromium is preinstalled:

```bash
ls ~/.cache/ms-playwright/chromium-*/chrome-linux/chrome && ~/.cache/ms-playwright/chromium-*/chrome-linux/chrome --version
```

```output
/root/.cache/ms-playwright/chromium-1194/chrome-linux/chrome
Chromium 141.0.7390.37 
```

Start rodney with no environment variables — it auto-detects the Playwright Chrome:

```bash
./rodney start
```

```output
Auth proxy started (PID 20037, port 58684) -> 21.0.0.211:15004
Chrome started (PID 20043)
Debug URL: ws://127.0.0.1:58012/devtools/browser/c8116471-4d53-450e-9cdb-37819eeb2954
```

Verify it's using Playwright's Chromium by checking the process command line:

```bash
cat /proc/$(cat ~/.rodney/state.json | python3 -c "import sys,json; print(json.load(sys.stdin)['chrome_pid'])")/cmdline | tr '\0' ' ' | grep -o '[^ ]*chrome[^ ]*' | head -1
```

```output
/root/.cache/ms-playwright/chromium-1194/chrome-linux/chrome
```

Confirm no rod-managed Chrome was downloaded:

```bash
ls ~/.cache/rod 2>&1 || echo "No ~/.cache/rod directory — nothing was downloaded"
```

```output
ls: cannot access '/root/.cache/rod': No such file or directory
No ~/.cache/rod directory — nothing was downloaded
```

Quick smoke test — open a page to prove it works:

```bash
./rodney open https://example.com && ./rodney js "document.title"
```

```output
Example Domain
Example Domain
```

```bash
./rodney stop
```

```output
Chrome stopped
```

## ROD_CHROME_BIN: Specify an Exact Chrome Binary

Set `ROD_CHROME_BIN` to point at any Chrome or Chromium binary. This takes the highest priority — rodney will use exactly that path without searching for anything else.

```bash
ROD_CHROME_BIN=/root/.cache/ms-playwright/chromium_headless_shell-1194/chrome-linux/headless_shell ./rodney start
```

```output
Auth proxy started (PID 24297, port 36846) -> 21.0.0.211:15004
Chrome started (PID 24303)
Debug URL: ws://127.0.0.1:36425/devtools/browser/332109d2-19c3-476a-8833-a23be1c97645
```

Verify it's running the headless_shell binary we specified:

```bash
cat /proc/$(cat ~/.rodney/state.json | python3 -c "import sys,json; print(json.load(sys.stdin)['chrome_pid'])")/cmdline | tr '\0' ' ' | grep -o '[^ ]*headless_shell[^ ]*' | head -1
```

```output
/root/.cache/ms-playwright/chromium_headless_shell-1194/chrome-linux/headless_shell
```

```bash
./rodney open https://example.com && ./rodney js "document.title"
```

```output
Example Domain
Example Domain
```

```bash
./rodney stop
```

```output
Chrome stopped
```

## RODNEY_USE_ROD_CHROME: Force Rod's Own Download

Set `RODNEY_USE_ROD_CHROME=1` to skip all preinstalled Chrome detection. Rod will download and manage its own Chromium binary in `~/.cache/rod/`. This is useful if you need a specific Chromium revision that rod pins, or if the preinstalled Chrome has compatibility issues.

First, confirm there's no rod-managed Chrome yet:

```bash
ls ~/.cache/rod 2>&1 || echo "No ~/.cache/rod — rod has not downloaded Chrome yet"
```

```output
ls: cannot access '/root/.cache/rod': No such file or directory
No ~/.cache/rod — rod has not downloaded Chrome yet
```

Now start rodney with `RODNEY_USE_ROD_CHROME=1`. Rod will download its own Chromium (this may take a moment):

```bash
RODNEY_USE_ROD_CHROME=1 ./rodney start
```

```output
Auth proxy started (PID 27732, port 46457) -> 21.0.0.211:15004
[launcher.Browser]2026/02/11 09:12:09 Download: https://registry.npmmirror.com/-/binary/chromium-browser-snapshots/Linux_x64/1321438/chrome-linux.zip
[launcher.Browser]2026/02/11 09:12:09 Progress: 00%
[launcher.Browser]2026/02/11 09:12:10 Progress: 03%
[launcher.Browser]2026/02/11 09:12:11 Progress: 06%
[launcher.Browser]2026/02/11 09:12:12 Progress: 09%
[launcher.Browser]2026/02/11 09:12:13 Progress: 11%
[launcher.Browser]2026/02/11 09:12:14 Progress: 14%
[launcher.Browser]2026/02/11 09:12:15 Progress: 18%
[launcher.Browser]2026/02/11 09:12:16 Progress: 21%
[launcher.Browser]2026/02/11 09:12:17 Progress: 24%
[launcher.Browser]2026/02/11 09:12:18 Progress: 27%
[launcher.Browser]2026/02/11 09:12:19 Progress: 30%
[launcher.Browser]2026/02/11 09:12:20 Progress: 33%
[launcher.Browser]2026/02/11 09:12:21 Progress: 36%
[launcher.Browser]2026/02/11 09:12:22 Progress: 39%
[launcher.Browser]2026/02/11 09:12:23 Progress: 42%
[launcher.Browser]2026/02/11 09:12:24 Progress: 45%
[launcher.Browser]2026/02/11 09:12:25 Progress: 49%
[launcher.Browser]2026/02/11 09:12:26 Progress: 52%
[launcher.Browser]2026/02/11 09:12:27 Progress: 55%
[launcher.Browser]2026/02/11 09:12:28 Progress: 58%
[launcher.Browser]2026/02/11 09:12:29 Progress: 61%
[launcher.Browser]2026/02/11 09:12:30 Progress: 64%
[launcher.Browser]2026/02/11 09:12:31 Progress: 67%
[launcher.Browser]2026/02/11 09:12:33 Progress: 72%
[launcher.Browser]2026/02/11 09:12:34 Progress: 75%
[launcher.Browser]2026/02/11 09:12:35 Progress: 78%
[launcher.Browser]2026/02/11 09:12:36 Progress: 81%
[launcher.Browser]2026/02/11 09:12:37 Progress: 84%
[launcher.Browser]2026/02/11 09:12:38 Progress: 87%
[launcher.Browser]2026/02/11 09:12:39 Progress: 91%
[launcher.Browser]2026/02/11 09:12:40 Progress: 94%
[launcher.Browser]2026/02/11 09:12:41 Progress: 97%
[launcher.Browser]2026/02/11 09:12:42 Unzip: /root/.cache/rod/browser/chromium-1321438
[launcher.Browser]2026/02/11 09:12:42 Progress: 00%
[launcher.Browser]2026/02/11 09:12:43 Progress: 19%
[launcher.Browser]2026/02/11 09:12:44 Progress: 34%
[launcher.Browser]2026/02/11 09:12:45 Progress: 54%
[launcher.Browser]2026/02/11 09:12:46 Progress: 85%
[launcher.Browser]2026/02/11 09:12:46 Downloaded: /root/.cache/rod/browser/chromium-1321438
Chrome started (PID 27744)
Debug URL: ws://127.0.0.1:57492/devtools/browser/c1fde843-6c88-4d34-b6bb-653aa952398a
```

Rod downloaded Chromium revision 1321438 to `~/.cache/rod/`. Verify it's using that binary:

```bash
cat /proc/$(cat ~/.rodney/state.json | python3 -c "import sys,json; print(json.load(sys.stdin)['chrome_pid'])")/cmdline | tr '\0' ' ' | grep -o '[^ ]*/chrome' | head -1
```

```output
/root/.cache/rod/browser/chromium-1321438/chrome
```

```bash
./rodney open https://example.com && ./rodney js "document.title"
```

```output
Example Domain
Example Domain
```

```bash
./rodney stop
```

```output
Chrome stopped
```

Show what rod downloaded — about 300MB of Chromium:

```bash
du -sh ~/.cache/rod/browser/chromium-*
```

```output
533M	/root/.cache/rod/browser/chromium-1321438
```

Clean up the rod-downloaded Chrome to reclaim disk space:

```bash
rm -rf ~/.cache/rod && echo "Removed ~/.cache/rod"
```

```output
Removed ~/.cache/rod
```

## Summary

| Variable | Effect |
|---|---|
| *(none)* | Auto-detect: system Chrome, then Playwright cache, then rod downloads one |
| `ROD_CHROME_BIN=/path/to/chrome` | Use exactly that binary, no detection or download |
| `RODNEY_USE_ROD_CHROME=1` | Skip detection, let rod download and manage its own Chromium |
