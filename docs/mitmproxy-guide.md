# Sniffing Podimo GraphQL Traffic with mitmproxy

> A practical guide to intercept and log Podimo's GraphQL queries from the official Android app.  
> Use this to discover new query types, fields, and mutations when the API changes.

---

## Prerequisites

- Android device or emulator (Android Studio emulator works fine)
- Docker or Python on your host machine
- Podimo app installed and logged in
- The Android device and host machine on the same network (or localhost for emulator)

---

## Step 1 — Run mitmproxy

### Option A: Docker (easiest)

```bash
docker run --rm -it \
  -p 8080:8080 \
  -p 8081:8081 \
  -v $(pwd)/mitmproxy-logs:/home/mitmproxy/.mitmproxy \
  mitmproxy/mitmproxy mitmweb \
  --web-host 0.0.0.0 \
  --web-port 8081 \
  --set stream_large_bodies=1m \
  --save-stream-file /home/mitmproxy/.mitmproxy/podimo-flows.mitm
```

- **8080** — proxy port (point Android here)
- **8081** — web UI (view flows in browser)

### Option B: pip

```bash
pip install mitmproxy
mitmweb --web-host 0.0.0.0 --save-stream-file podimo-flows.mitm
```

---

## Step 2 — Install CA certificate on Android

1. Open `http://mitm.it` in Chrome on the Android device (while proxied)
2. Tap **Android** → download the certificate
3. Go to **Settings → Security → Install from storage → CA certificate**
4. Select the downloaded `.cer` file and confirm

For Android 11+ you may need to install via:
```bash
adb shell cmd trust_listener add-cacert /path/to/mitmproxy-ca-cert.cer
```

---

## Step 3 — Route Android traffic through mitmproxy

### Emulator

```bash
# On host, after emulator is running
emulator -avd YOUR_AVD_NAME -http-proxy http://10.0.2.2:8080
```

Or via emulator UI: **Settings → Proxy** → set to `10.0.2.2:8080`.

### Physical device

Go to **Wi-Fi settings → Modify network → Advanced → Proxy → Manual**:
- Proxy hostname: your host machine's IP (`192.168.x.x`)
- Proxy port: `8080`

---

## Step 4 — Use the Podimo app

Open the app and navigate:
- Search for podcasts
- Open a podcast page
- Play an episode
- Browse categories / recommendations

Every GraphQL request will be logged by mitmproxy.

---

## Step 5 — Extract GraphQL queries

### Via mitmweb UI

Open `http://localhost:8081` on your host. Filter with:
```
~u graphql
```

Inspect request bodies — they contain the full `query` string.

### Via command line

```bash
# Replay flows and grep for queries
mitmdump -r podimo-flows.mitm -n --flow-filter "~u graphql" \
  | grep -oP 'query\s+\w+\s*\([^)]*\)\s*\{[^}]*\}'
```

### Structured extraction (Python)

```python
#!/usr/bin/env python3
"""Extract GraphQL queries from mitmproxy flows."""
import json
import sys
from mitmproxy.io import FlowReader

def extract_queries(flow_file):
    with open(flow_file, 'rb') as f:
        for flow in FlowReader(f).stream():
            if 'graphql' not in flow.request.url:
                continue
            try:
                body = json.loads(flow.request.content)
                query = body.get('query', '')
                if query:
                    print(f"\n{'='*60}")
                    print(f"URL: {flow.request.url}")
                    print(f"Operation: {body.get('operationName', 'unknown')}")
                    print(f"{'='*60}")
                    print(query)
            except Exception:
                pass

if __name__ == '__main__':
    extract_queries(sys.argv[1])
```

Install dependency:
```bash
pip install mitmproxy
python extract_queries.py podimo-flows.mitm > podimo-queries.graphql
```

---

## Step 6 — Reconstruct schema fragments

Feed the extracted queries into a GraphQL SDL generator:

```bash
# Install graphql-extractor
npm install -g @graphql-tools/merge

# Or manually build types from the queries you find
```

What to look for:
- **New query roots** — `searchPodcasts`, `userSubscriptions`, `categories`
- **New types** — `PodcastShow`, `PodcastSeries`, `CreatorProfile`
- **Mutations** — `markEpisodePlayed`, `addToFavorites`, `ratePodcast`
- **Arguments** — `filter`, `sort`, `cursor` (for pagination)

---

## Tips & Warnings

| Tip | Why |
|-----|-----|
| Use `--set stream_large_bodies=1m` | Podcast images/audio can be large; this avoids memory issues |
| Filter by `~u graphql` early | Reduces noise from CDN, analytics, tracking |
| Capture a fresh session after logout/login | Catches the auth handshake queries |
| Trigger search and filters | Reveals argument types and enums |

⚠️ **Legal:** Only intercept traffic from an app install you own on a device you control. Do not distribute intercepted credentials or tokens.

⚠️ **Privacy:** mitmproxy logs may contain your Podimo login token. Store `*.mitm` files securely and delete after analysis.

---

## Troubleshooting

### "SSL handshake failed"
The CA cert is not installed correctly. Re-install via `mitm.it` or `adb`.

### "No flows showing"
Check that the emulator/device proxy points to the **host IP**, not `localhost`. For emulators, use `10.0.2.2`.

### "App won't load / crashes"
Some apps use certificate pinning. Podimo currently does not (as proven by this project working), but if they add it, you would need to bypass pinning (root + Xposed/Frida). That is out of scope.
