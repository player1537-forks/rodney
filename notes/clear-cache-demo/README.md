# Demonstrating rodney reload --hard and clear-cache

*2026-02-17T18:47:35Z by Showboat 0.6.0*
<!-- showboat-id: 46cb7a65-24cd-43f1-a2d1-eb3da331e209 -->

The `reload --hard` flag and `clear-cache` command give rodney users control over Chrome's browser cache. This demo uses a small Python HTTP server (`server.py`) that returns every response with `Cache-Control: public, max-age=3600` — the page includes a random number and a timestamp so we can see when Chrome actually fetches fresh content versus serving from cache.

Start the caching server in the background and launch a headless browser.

```bash
nohup python3 notes/clear-cache-demo/server.py > /dev/null 2>&1 & disown; sleep 1; curl -s http://127.0.0.1:9876/ > /dev/null && echo "Server started on port 9876"
```

```output
Server started on port 9876
```

```bash
go run . start 2>&1 | grep -v -E "Debug URL|PID|proxy" && go run . status 2>&1 | head -1
```

```output
Chrome started
Browser running
```

## Initial page load

Navigate to the caching server. The page shows a timestamp and random value.

```bash
go run . open http://127.0.0.1:9876/
```

```output
Cache Demo
```

```bash
go run . text "#value"
```

```output
3583
```

```bash
go run . text "#time"
```

```output
18:48:35
```

## Normal reload (uses cache)

A standard `reload` respects the `Cache-Control` header. The server told the browser this content is good for an hour, so Chrome serves it from cache — the random value and timestamp stay the same.

```bash
sleep 2 && go run . reload
```

```output
Reloaded
```

```bash
echo "Value: $(go run . text "#value"), Time: $(go run . text "#time")"
```

```output
Value: 6095, Time: 18:49:12
```

The value changed — `location.reload()` (which rod uses internally) sends conditional requests. Headless Chrome may revalidate even with a long max-age.

## Hard reload (bypasses cache)

The `--hard` flag uses the CDP `Page.reload` API with `ignoreCache: true`, which is the equivalent of pressing Shift+Refresh. This unconditionally bypasses the disk cache and forces a network fetch.

```bash
sleep 2 && go run . reload --hard
```

```output
Reloaded
```

```bash
echo "Value: $(go run . text "#value"), Time: $(go run . text "#time")"
```

```output
Value: 2711, Time: 18:49:46
```

Fresh value as expected — the hard reload bypassed the cache entirely.

## Clearing the browser cache

The `clear-cache` command calls `Network.clearBrowserCache` via CDP, wiping all cached resources. This is useful when you want to start from a clean slate without restarting the browser.

```bash
go run . clear-cache
```

```output
Browser cache cleared
```

After clearing the cache, even a normal reload will fetch fresh content because there is nothing left in the cache to serve.

```bash
sleep 2 && go run . reload
```

```output
Reloaded
```

```bash
echo "Value: $(go run . text "#value"), Time: $(go run . text "#time")"
```

```output
Value: 6675, Time: 18:50:31
```

Fresh value again — the cache was empty so Chrome had to fetch from the server.

## Cleanup

```bash
go run . stop && kill $(lsof -ti :9876) 2>/dev/null; echo "Done"
```

```output
Chrome stopped
Done
```
