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
3. **Notifier** mengirim ke channel default dan channel tambahan yang disimpan di database, me-mention assignee, dan mengirim Discord embed.

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
| `APP_ENV` | - | `production` untuk JSON log |
| `DISCORD_ALLOWED_USERS` | - | ID Discord yang boleh pakai command privileged (`/ping`, `/invite`), pisah koma. Kosong = semua boleh |
| `DB_PATH` | - | Path file sqlite untuk data invite (default: `./data/bot.db`) |
| `PLAN_API_BASE_URL` | - | Base URL API plan (default: `https://api-plan.kancadigital.com`) |
| `PLAN_API_KEY` | ✅ (untuk `/invite`) | API key plan, dikirim sebagai header `x-api-key` |
| `PLAN_INVITE_WEB_URL` | - | Base URL link undangan yang dikirim ke user (default: `https://plan.kancadigital.com/invite`) |
| `PLAN_INVITE_EMAIL_DOMAIN` | - | Domain email untuk `/invite user` (default: `@kancadigital.com`) |
| `SMTP_HOST` | ✅ (untuk `/invite email`) | Host SMTP untuk mengirim email undangan |
| `SMTP_PORT` | - | Port SMTP (default: `587`) |
| `SMTP_USERNAME` | ✅ (untuk `/invite email`) | Username/login SMTP |
| `SMTP_PASSWORD` | ✅ (untuk `/invite email`) | Password/app-password SMTP |
| `SMTP_FROM` | - | Alamat pengirim (default: sama dengan `SMTP_USERNAME`) |

### `/invite` command

Punya dua subcommand tergantung apakah orang yang diundang ada di Discord atau tidak. Keduanya tunduk pada allowlist `DISCORD_ALLOWED_USERS` yang sama dengan `/ping`.

**`/invite user project_key:<key> user:<@discord_user>`** — untuk member Discord yang bisa di-tag:

1. Data invite (Discord user ID + username, project key, waktu) disimpan ke sqlite.
2. Bot memanggil `POST {PLAN_API_BASE_URL}/projects/{project_key}/invites` dengan header `x-api-key` dan body `{"email": "<username>@kancadigital.com", "role": "member"}`.
3. Token undangan dari response API disimpan kembali ke sqlite (kolom `token_id`).
4. Bot mengirim DM ke user yang diundang berisi link `{PLAN_INVITE_WEB_URL}/{token}`.

**`/invite email project_key:<key> email:<alamat@email.com>`** — untuk orang di luar Discord, langsung pakai email:

1. Data invite (email, project key, waktu) disimpan ke sqlite — tanpa data Discord karena tidak ada akun Discord yang terlibat.
2. Bot memanggil endpoint plan API yang sama, tapi dengan email yang diberikan langsung (bukan hasil turunan dari username).
3. Token undangan disimpan ke sqlite seperti biasa.
4. Karena tidak ada user Discord untuk di-DM, bot mengirim email berisi link undangan langsung ke alamat tersebut lewat SMTP (`SMTP_HOST`/`SMTP_USERNAME`/`SMTP_PASSWORD`). Jika SMTP belum dikonfigurasi atau pengiriman gagal, link ditampilkan di balasan (ephemeral) sebagai fallback supaya tetap bisa diteruskan manual.

### `/notify-channel` command

Notifikasi issue GitHub selalu dikirim ke `DISCORD_DEFAULT_CHANNEL`. Channel tambahan bisa dikelola lewat slash command dan disimpan ke sqlite pada `DB_PATH`.

- `/notify-channel add channel:#channel` — tambahkan channel penerima notifikasi.
- `/notify-channel remove channel:#channel` — hapus channel tambahan.
- `/notify-channel list` — lihat channel utama dan channel tambahan.

Command ini memakai izin yang sama dengan `/ping` dan `/invite`: jika `DISCORD_ALLOWED_USERS` diisi, hanya user dalam daftar itu yang bisa mengelola channel.

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
