# Discord GitHub Issue Notifier Bot

Bot Discord (Go) yang menerima data GitHub issue dari n8n dan mengirim notifikasi ke channel Discord yang sesuai, lengkap dengan mention ke assignee.

## Arsitektur

```
cmd/bot/main.go          ← Entry point & signal handling
internal/
  config/config.go       ← Konfigurasi dari environment variable
  model/issue.go         ← Domain model (Issue, Label, User, Repo)
  queue/queue.go         ← Buffered worker-pool queue
  notifier/notifier.go   ← Membangun & mengirim pesan Discord
  handler/webhook.go     ← HTTP handler untuk n8n webhook
  watcher/watcher.go     ← File watcher (polling JSON file)
  bot/bot.go             ← Wiring semua komponen & lifecycle
pkg/logger/logger.go     ← Structured logger (slog)
```

## Cara Kerja

```
n8n ──(HTTP POST)──▶ WebhookHandler ─┐
                                     ├──▶ Queue ──▶ Workers ──▶ Notifier ──▶ Discord
n8n ──(JSON File)──▶ FileWatcher  ───┘
```

1. **Dua sumber input**: HTTP webhook (real-time) dan file watcher (polling setiap 5 detik).
2. **Queue** menjaga urutan dan memastikan tidak ada issue yang hilang saat burst traffic.
3. **Notifier** me-resolve channel berdasarkan label, me-mention assignee, dan mengirim Discord embed.

## Setup

### 1. Buat Bot Discord

1. Buka [Discord Developer Portal](https://discord.com/developers/applications)
2. Buat aplikasi baru → tambahkan Bot
3. Salin token → isi `DISCORD_TOKEN`
4. Di OAuth2 → URL Generator: centang `bot`, lalu centang permission `Send Messages` dan `Embed Links`
5. Invite bot ke server menggunakan URL yang dihasilkan

### 2. Konfigurasi Environment

```bash
cp .env.example .env
# Edit .env sesuai kebutuhan
```

| Variable | Wajib | Keterangan |
|---|---|---|
| `DISCORD_TOKEN` | ✅ | Token bot Discord |
| `DISCORD_DEFAULT_CHANNEL` | ✅ | Channel fallback (ID) |
| `SERVER_PORT` | - | Port HTTP server (default: `8080`) |
| `WEBHOOK_SECRET` | - | HMAC secret untuk verifikasi request n8n |
| `WATCHER_DIR` | - | Direktori file JSON (default: `./watch`) |
| `GITHUB_DISCORD_USER_MAP` | - | `githubLogin=discordID,...` |
| `GITHUB_LABEL_CHANNEL_MAP` | - | `labelName=channelID,...` |
| `APP_ENV` | - | `production` untuk JSON log |

### 3. Jalankan

```bash
# Development
make run

# Production (build binary dulu)
make build
./bin/bot
```

## Format Payload dari n8n

Bot menerima **tiga format** payload secara otomatis:

**Format 1 — Wrapped (direkomendasikan untuk n8n):**
```json
{
  "issues": [
    {
      "number": 42,
      "title": "Fix login bug",
      "html_url": "https://github.com/org/repo/issues/42",
      "state": "open",
      "labels": [{"name": "bug"}],
      "assignees": [{"login": "alice"}],
      "repository": {"full_name": "org/repo"},
      "created_at": "2026-08-16T00:00:00Z"
    }
  ]
}
```

**Format 2 — Array:**
```json
[{ "number": 42, ... }]
```

**Format 3 — Single issue:**
```json
{ "number": 42, ... }
```

## HTTP Endpoints

| Method | Path | Keterangan |
|---|---|---|
| `POST` | `/webhook` | Terima payload issue dari n8n |
| `GET` | `/health` | Health check (untuk monitoring) |

## File Watcher

Drop file `.json` ke dalam folder `WATCHER_DIR` (default `./watch/`).
- File berhasil diproses → dipindah ke `watch/processed/`
- File gagal diparse → dipindah ke `watch/failed/`

## Konfigurasi n8n

### Webhook Mode
- Node: **HTTP Request**
- Method: `POST`
- URL: `http://your-bot-host:8080/webhook`
- Body: JSON sesuai format di atas
- Header (opsional): `X-Hub-Signature-256: sha256=<hmac>` jika `WEBHOOK_SECRET` diset

### File Mode
- Node: **Write Binary File**
- Path: sesuai `WATCHER_DIR` di bot
