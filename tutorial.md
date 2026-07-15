<div align="center">

# Step by step beginners tutorial

Welcome! This tutorial is for absolute beginners. No server experience or Raspberry Pi knowledge required. If you get stuck, ask for help in the [Telegram community](https://t.me/+fhbeYgPzKU44MzVk).

</div>

---

## Method 1: Docker (Recommended — Easiest)

If you have [Docker](https://docs.docker.com/get-docker/) installed, this is the fastest way to get running.

### 1. Pull and run the image

```sh
docker run -d \
    --name podimo-rss \
    --restart unless-stopped \
    -e PODIMO_BIND_HOST=0.0.0.0:12104 \
    -p 12104:12104 \
    -v $(pwd)/podimo-cache:/tmp/podimo-rss-cache \
    ghcr.io/solidrhino/podimo:latest
```

### 2. Open the web interface

Visit `http://YOUR-IP-ADDRESS:12104` in your browser.

Replace `YOUR-IP-ADDRESS` with your computer's local IP. Find it with:

- **macOS/Linux**: Run `hostname -I` or check System Preferences → Network
- **Windows**: Run `ipconfig` in Command Prompt

### 3. Configure your settings

Click through the web form:

1. **Search for a podcast** — Type the podcast name and hit **Search**. Pick a result and the ID auto-fills.
2. **Or paste a Podimo URL** — e.g. `https://open.podimo.com/podcast/09c55c96-...`
3. **Enter your Podimo email & password**
4. **Select your region** (e.g. `nl` for Netherlands) and **locale** (e.g. `nl-NL`)
5. Click **Create RSS URL**

🎉 Done! Copy the generated URL into your podcast app.

### Persistent configuration (optional)

Create a `.env` file to avoid re-entering credentials:

```sh
cat > .env <<EOF
PODIMO_HOSTNAME=your.ip.address:12104
PODIMO_BIND_HOST=0.0.0.0:12104
PODIMO_LOCAL_CREDENTIALS=true
PODIMO_EMAIL=your-email@example.com
PODIMO_PASSWORD=your-password
EOF
```

Then run with:

```sh
docker run -d \
    --name podimo-rss \
    --restart unless-stopped \
    --env-file .env \
    -p 12104:12104 \
    -v $(pwd)/podimo-cache:/tmp/podimo-rss-cache \
    ghcr.io/solidrhino/podimo:latest
```

---

## Method 2: Direct install (Go binary)

This method runs the tool directly on your machine or Pi without Docker.

### Requirements

- Go 1.26+ (install from [go.dev/dl](https://go.dev/dl/) or your package manager)
- Network connection

### 1. Install the tool

```sh
git clone https://github.com/SolidRhino/podimo
cd podimo
go build -o podimo-rss .
```

### 2. Configure

```sh
# Copy the example config
cp config.example.yaml config.yaml
nano config.yaml
```

Edit the following lines in `config.yaml`:

```yaml
# Your machine's local IP address (find it with: hostname -I)
hostname: "192.168.1.50:12104"

# Listen on all interfaces so other devices can reach it
bind_host: "0.0.0.0:12104"

# (Optional) Store credentials server-side instead of embedding them in URLs
local_credentials: true
email: "your-podimo-email@example.com"
password: "your-podimo-password"
```

Save with **Ctrl+X** → **Y** → **Enter**.

### 3. Start the server

```sh
./podimo-rss
```

Visit `http://your-ip-address:12104` in your browser.

To run it in the background as a systemd service:

```sh
just install
just start
```

---

## Using the web interface

### Searching for a podcast

1. Enter your Podimo **email** and **password** in the form
2. Type a podcast name in the **Search by name** field
3. Click **Search**
4. Results appear with cover images. **Click one** to auto-fill the Podcast ID

### From your subscriptions

After logging in, you can also view your followed podcasts by visiting `/subscriptions` (or use the **Subscriptions** link if added to the UI).

### Manual ID extraction

If you already know the URL:

1. Go to [open.podimo.com](https://open.podimo.com)
2. Find your podcast and copy the UUID from the URL:

```text
https://open.podimo.com/podcast/09c55c96-9b1b-456e-bdf2-3abed3b61db5
                                 ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
                                 Podcast ID
```

3. Paste it into the **Podcast ID or URL** field

---

## Tips & Troubleshooting

### Bot detection / rate limiting

If Podimo blocks your requests, use a proxy:

**Zenrows** (recommended):
1. Create a free account at [app.zenrows.com](https://app.zenrows.com/register)
2. Add to your `.env`:

```sh
PODIMO_ZENROWS_API="your-api-key"
```

**ScraperAPI** (alternative):
1. Create an account at [scraperapi.com](https://scraperapi.com)
2. Add to your `.env`:

```sh
PODIMO_SCRAPER_API="your-api-key"
```

Restart the container or server after editing `.env`.

### Health checks

The server exposes a `/health` endpoint for monitoring tools:

```sh
curl http://your-ip:12104/health
# → {"status": "ok", "service": "podimo-rss"}
```

Docker Compose and Kubernetes users can use this for health probes.

### Making it accessible from outside your home

1. In your router, assign a **static IP** to your Pi/computer
2. Set up **port forwarding** for port `12104`
3. Use your public IP address (find it at [whatismyipaddress.com](https://whatismyipaddress.com))
4. Consider using a reverse proxy with HTTPS (e.g. Nginx Proxy Manager, Traefik, or Cloudflare Tunnel) — Basic Auth is cleartext over HTTP

### Updating the tool

**Docker:**

```sh
docker pull ghcr.io/solidrhino/podimo:latest
docker stop podimo-rss && docker rm podimo-rss
# Then re-run the docker run command from Method 1
```

**Direct install (Go):**

```sh
cd podimo
git pull
go build -o podimo-rss .
# If running as a systemd service:
just restart
```

### Logs

**Docker:**

```sh
docker logs -f podimo-rss
```

**Direct install (systemd):**

```sh
just logs
```

### Getting help

- Check [AGENTS.md](AGENTS.md) for AI-friendly codebase documentation
- Open an issue on [GitHub](https://github.com/SolidRhino/podimo)
- Ask in the [Telegram community](https://t.me/+fhbeYgPzKU44MzVk)

---

## Security notes

- **Never commit your `.env` file** — it contains your Podimo password
- **Use HTTPS in production** — Basic Auth credentials are sent in cleartext over HTTP
- **Set `PODIMO_LOCAL_CREDENTIALS=true` for personal use** — this avoids embedding your password in generated RSS URLs

---

<div align="center">

If this tool saves you time, consider buying the original author a coffee! ☕

<a href="https://www.buymeacoffee.com/thijsr"><img src="https://img.buymeacoffee.com/button-api/?text=Buy%20me%20a%20coffee&emoji=&slug=thijsr&button_colour=BD5FFF&font_colour=ffffff&font_family=Poppins&outline_colour=000000&coffee_colour=FFDD00" /></a>

</div>
