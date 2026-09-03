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
3. **Notifier** membuat post di forum Discord untuk repo yang sudah disinkronkan. Repo yang belum disinkronkan tetap dikirim ke channel default/channel tambahan sebagai fallback.

## Setup

### 1. Buat Bot Discord

1. Buka [Discord Developer Portal](https://discord.com/developers/applications)
2. Buat aplikasi baru → tambahkan Bot
3. Salin token → isi `DISCORD_TOKEN`
4. Di OAuth2 → URL Generator: centang `bot` dan `applications.commands`, lalu centang permission `View Channels`, `Send Messages`, `Embed Links`, `Create Public Threads`, `Send Messages in Threads`, dan `Manage Threads`
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
| `DISCORD_GUILD_ID` | - | Server ID untuk register slash command instan. Bisa lebih dari satu, pisah koma. Kosong = global command |
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

Fallback notifikasi issue GitHub dikirim ke channel default. Channel tambahan bisa dikelola lewat slash command dan disimpan ke sqlite pada `DB_PATH`.

- `/notify-channel add channel:#channel` — tambahkan channel penerima notifikasi.
- `/notify-channel remove channel:#channel` — hapus channel tambahan.
- `/notify-channel list` — lihat channel utama dan channel tambahan.

Command ini memakai izin yang sama dengan `/ping` dan `/invite`: jika `DISCORD_ALLOWED_USERS` diisi, hanya user dalam daftar itu yang bisa mengelola channel.

### `/forum-sync` command

Gunakan command ini untuk menentukan repo GitHub mana yang masuk ke channel Discord bertipe forum. Saat webhook issue dari repo itu masuk, bot akan membuat post baru di forum dengan title dari issue GitHub dan isi embed dari body/metadata issue. Setiap issue GitHub hanya punya **satu** post forum: event pertama (`opened`) membuat post barunya, dan event berikutnya untuk issue yang sama (`edited`, `closed`, `reopened`) meng-edit post yang sama itu (judul, tag, isi embed) — bukan membuat post baru — mapping issue ↔ thread disimpan di sqlite (`DB_PATH`).

- `/forum-sync set repo_url:https://github.com/org/repo forum:#forum-channel` — sinkronkan repo ke forum.
- `/forum-sync remove repo_url:https://github.com/org/repo` — hapus sinkronisasi repo.
- `/forum-sync list` — lihat daftar repo yang sudah disinkronkan.

Tag forum akan dipasang otomatis jika label GitHub cocok dengan tag yang sudah tersedia di forum channel (tag yang belum ada di forum-nya otomatis dilewati, jadi aman walau forum belum punya semua tag di bawah). Tag yang dipakai saat ini: `Bug`, `Duplicate`, `Documentation`, `First Issue` (juga cocok dengan label default GitHub `good first issue`), `Enhancement`, `Help Wanted`, `Invalid`, `Question`, `Wontfix`, `Security`, dan `Dependencies`.

Selain itu bot juga otomatis mencoba memasang tag `Open`/`Closed` sesuai status issue (jika forum sudah punya tag dengan nama tersebut). Judul post forum diberi ikon 🟢 (open) / 🔴 (closed) — begitu juga warna & field status pada embed-nya — dan saat issue closed, thread-nya otomatis di-lock & di-archive (dibuka lagi otomatis kalau issue-nya reopened), supaya issue yang masih open dan yang sudah closed gampang dibedakan langsung dari daftar thread forum.

Bot perlu akses ke forum tersebut dan permission `Create Public Threads`, `Send Messages`, `Send Messages in Threads`, `Embed Links`, dan `Manage Threads` (dipakai untuk edit judul/tag serta lock/archive post saat issue closed).

### `/github-user` command

Gunakan command ini untuk menyambungkan username GitHub ke user Discord. Saat issue punya assignee yang sudah disinkronkan, bot akan mention user Discord tersebut di post/message notifikasi.

- `/github-user set github_username:octocat user:@discord_user` — sinkronkan username GitHub ke user Discord.
- `/github-user remove github_username:octocat` — hapus sinkronisasi user.
- `/github-user list` — lihat daftar user yang sudah disinkronkan.

### 3. Jalankan

```bash
# Development
make run

# Production (build binary dulu)
make build
./bin/bot
```

## Deploy ke Dokploy (build lokal, jalan terus di server)

Bot di-deploy sebagai Docker image yang **di-build di lokal**, di-push ke GitHub Container Registry (GHCR), lalu Dokploy tinggal menarik (pull) image itu — server tidak perlu compile apa-apa. `Dockerfile` yang sudah ada di repo ini multi-stage: hasil akhirnya cuma binary + Alpine minimal (compiler Go dibuang), dan tidak butuh cgo karena driver sqlite yang dipakai (`modernc.org/sqlite`) murni Go.

### Sekali saja: login ke GHCR

Butuh GitHub Personal Access Token dengan scope `write:packages` (dan `read:packages`).

```bash
echo <GITHUB_PAT> | docker login ghcr.io -u <github-username> --password-stdin
```

### Setiap kali mau rilis versi baru

```bash
# Tag otomatis: <git-short-sha> + latest
make release

# Atau tambah tag versi manual, mis. buat rilis "resmi":
VERSION=v1.2.0 make release
```

Ini menjalankan [scripts/release.sh](scripts/release.sh): build image dari `Dockerfile`, tag, lalu push ke `ghcr.io/mik-sea/bot_discord_go`. Karena setiap rilis dapat tag unik (`git-sha`, dan opsional `VERSION`), kamu bisa deploy tag tertentu di Dokploy dan tahu persis versi apa yang lagi jalan — bukan cuma `latest` yang menimpa diam-diam.

### Setup Dokploy (sekali saja)

1. Buat aplikasi baru di Dokploy dengan tipe **Docker Image** (bukan "build from Git"), isi image: `ghcr.io/mik-sea/bot_discord_go` dan tag yang mau dipakai (mis. `latest` atau tag versi tertentu dari `make release`).
2. Kalau package GHCR-nya private, tambahkan registry credentials di Dokploy (Settings → Registry) pakai GitHub username + PAT yang sama.
3. Set semua environment variable dari tabel di atas lewat tab **Environment** Dokploy (jangan commit `.env` ke git — sudah ada di `.gitignore`). `docker-compose.yml` di repo ini sudah memetakan semua variable itu.
4. Tambahkan **volume** ke `/app/data` supaya database sqlite (invite, channel settings, forum sync, dst.) tidak hilang tiap redeploy — sudah didefinisikan sebagai named volume `bot_data` di `docker-compose.yml`.
5. Set **Restart Policy** ke `unless-stopped` (sudah default di `docker-compose.yml`) supaya bot otomatis nyala lagi kalau container crash atau server reboot — ini yang bikin bot "aktif terus".
6. Atur domain publik supaya `/webhook` (dan `/health`) bisa diakses dari luar — lihat bagian berikutnya.

### Expose ke domain publik

`docker-compose.yml` sengaja **tidak** publish port ke host (`ports:`) — akses dari luar diatur lewat reverse proxy Traefik bawaan Dokploy, yang juga otomatis mengurus HTTPS (Let's Encrypt). Container tetap dengar di port `SERVER_PORT` (default `6000` sesuai `.env` kamu), tapi itu cuma port di dalam jaringan Docker; Traefik yang meneruskan trafik publik ke sana.

Di Dokploy, buka aplikasi bot → tab **Domains** → Add Domain:

- **Domain**: domain/subdomain yang sudah kamu siapkan (mis. `bot.kancadigital.com`), pastikan DNS-nya (A/CNAME) sudah mengarah ke IP server Dokploy.
- **Path**: `/` (default, biar semua path termasuk `/webhook` dan `/health` ke-cover).
- **Container Port**: isi sama persis dengan `SERVER_PORT` yang kamu set di Environment (`6000`) — bukan port 80/443, dan bukan `8080` bawaan `Dockerfile` (itu cuma default kalau `SERVER_PORT` kosong).
- **HTTPS**: aktifkan, biarkan Dokploy generate sertifikat Let's Encrypt otomatis.

Setelah domain aktif, endpoint-nya:
- Webhook n8n: `https://bot.kancadigital.com/webhook`
- Health check: `https://bot.kancadigital.com/health`

Kalau tetap mau publish port langsung ke host (tanpa lewat Traefik/HTTPS Dokploy) — misal untuk debug cepat — tinggal tambahkan lagi `ports: ["6000:6000"]` di `docker-compose.yml`, tapi untuk pemakaian normal dengan domain, cara lewat tab Domains di atas yang direkomendasikan.

### Redeploy otomatis setelah push tag baru

Dokploy tidak polling registry sendiri, jadi setelah `make release` kamu perlu memicu redeploy. Cara paling gampang: aktifkan **Deploy Webhook** di pengaturan aplikasi Dokploy (dia kasih URL unik), lalu set:

```bash
export DOKPLOY_WEBHOOK_URL="https://dokploy.example.com/api/deploy/webhook/xxxxxxxx"
make release
```

`scripts/release.sh` otomatis memanggil webhook itu setelah push berhasil, jadi satu perintah `make release` = build lokal → push ke GHCR → Dokploy redeploy.

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
