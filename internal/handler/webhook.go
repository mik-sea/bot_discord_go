package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/miksea/bot_discord_go/internal/model"
	"github.com/miksea/bot_discord_go/internal/queue"
)

// eventHeader is the GitHub header that identifies the event type.
const eventHeader = "X-GitHub-Event"

// WebhookHandler handles HTTP POST requests from GitHub or n8n.
type WebhookHandler struct {
	queue         *queue.Queue
	webhookSecret string
	logger        *slog.Logger
}

// NewWebhookHandler creates a new WebhookHandler.
func NewWebhookHandler(q *queue.Queue, secret string, logger *slog.Logger) *WebhookHandler {
	return &WebhookHandler{
		queue:         q,
		webhookSecret: secret,
		logger:        logger,
	}
}

// ServeHTTP implements http.Handler.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		h.logger.Error("failed to read body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Verifikasi signature HMAC jika secret dikonfigurasi.
	if h.webhookSecret != "" {
		if err := h.verifySignature(r.Header.Get("X-Hub-Signature-256"), body); err != nil {
			h.logger.Warn("invalid webhook signature", "error", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Route berdasarkan X-GitHub-Event header.
	// Kalau tidak ada header ini (payload dari n8n), fallback ke legacy parser.
	eventType := r.Header.Get(eventHeader)

	h.logger.Info("webhook received", "event", eventType)

	issues, skip, err := h.parseEvent(eventType, body)
	if err != nil {
		h.logger.Error("failed to parse payload", "event", eventType, "error", err)
		http.Error(w, "unprocessable entity", http.StatusUnprocessableEntity)
		return
	}

	// Event dikenali tapi sengaja di-skip (misalnya ping, push, dll).
	if skip {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ignored"}`)
		return
	}

	queued := 0
	for _, issue := range issues {
		if h.queue.Push(issue) {
			queued++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"queued":%d}`, queued)
}

// parseEvent memilih parser yang sesuai berdasarkan tipe event GitHub.
// Mengembalikan (issues, skip, error):
//   - issues: daftar issue yang akan diproses
//   - skip:   true jika event dikenali tapi tidak perlu diproses (ping, labeled, dll)
//   - error:  parsing gagal total
func (h *WebhookHandler) parseEvent(eventType string, body []byte) ([]model.Issue, bool, error) {
	switch eventType {

	case "ping":
		// GitHub kirim ping saat webhook pertama kali dibuat — normal, abaikan saja.
		h.logger.Info("github ping received — webhook aktif")
		return nil, true, nil

	case "issues":
		// Payload langsung dari GitHub dengan format GitHubIssueEvent.
		// parseGitHubIssueEvent sudah mengembalikan skip=true untuk action
		// yang tidak perlu dinotifikasikan (labeled, assigned, edited, dll).
		issues, skip, err := parseGitHubIssueEvent(body)
		if skip && err == nil {
			// Peek action dari body untuk logging yang informatif.
			var peek struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(body, &peek)
			h.logger.Info("issue action skipped", "action", peek.Action)
		}
		return issues, skip, err

	case "":
		// Tidak ada header X-GitHub-Event → kemungkinan payload dari n8n.
		issues, err := parseLegacyBody(body)
		return issues, false, err

	default:
		// Event lain (push, pull_request, star, dll) — abaikan dengan log.
		h.logger.Info("event type not handled, skipping", "event", eventType)
		return nil, true, nil
	}
}

// ── Parser per event type ─────────────────────────────────────────────────────

// parseGitHubIssueEvent mem-parse payload "issues" event dari GitHub.
// Format: {"action":"opened","issue":{...},"repository":{...}}
// Mengembalikan skip=true untuk action yang tidak perlu dinotifikasikan.
func parseGitHubIssueEvent(body []byte) ([]model.Issue, bool, error) {
	var event model.GitHubIssueEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, false, fmt.Errorf("parse github issue event: %w", err)
	}
	if event.Issue.Number == 0 {
		return nil, false, fmt.Errorf("github issue event: field 'issue.number' kosong")
	}

	// Hanya proses action tertentu — abaikan labeled, assigned, edited, dll.
	if !event.ShouldNotify() {
		return nil, true, nil
	}

	issue := event.ToIssue()
	return []model.Issue{issue}, false, nil
}

// parseLegacyBody adalah parser lama untuk payload dari n8n.
// Mendukung tiga format: N8NPayload wrapper, array issue, atau single issue.
func parseLegacyBody(body []byte) ([]model.Issue, error) {
	var payload model.N8NPayload
	if err := json.Unmarshal(body, &payload); err == nil && len(payload.Issues) > 0 {
		return payload.Issues, nil
	}

	var issues []model.Issue
	if err := json.Unmarshal(body, &issues); err == nil && len(issues) > 0 {
		return issues, nil
	}

	var single model.Issue
	if err := json.Unmarshal(body, &single); err == nil && single.Number != 0 {
		return []model.Issue{single}, nil
	}

	return nil, fmt.Errorf("unrecognized payload format")
}

// ── Signature verification ────────────────────────────────────────────────────

// verifySignature memverifikasi HMAC-SHA256 signature dari GitHub.
func (h *WebhookHandler) verifySignature(header string, body []byte) error {
	const prefix = "sha256="
	if len(header) < len(prefix) {
		return fmt.Errorf("missing X-Hub-Signature-256 header")
	}
	got := header[len(prefix):]

	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(got), []byte(want)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
