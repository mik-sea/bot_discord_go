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

// WebhookHandler handles HTTP POST requests from n8n containing GitHub issue data.
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

	if h.webhookSecret != "" {
		if err := h.verifySignature(r.Header.Get("X-Hub-Signature-256"), body); err != nil {
			h.logger.Warn("invalid webhook signature", "error", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	issues, err := parseBody(body)
	if err != nil {
		h.logger.Error("failed to parse payload", "error", err)
		http.Error(w, "unprocessable entity", http.StatusUnprocessableEntity)
		return
	}

	queued := 0
	for _, issue := range issues {
		if h.queue.Push(issue) {
			queued++
		}
	}

	h.logger.Info("webhook received", "total", len(issues), "queued", queued)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"queued":%d}`, queued)
}

// verifySignature checks the HMAC-SHA256 signature of the payload.
func (h *WebhookHandler) verifySignature(header string, body []byte) error {
	const prefix = "sha256="
	if len(header) < len(prefix) {
		return fmt.Errorf("missing signature header")
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

// parseBody attempts to decode the body as either N8NPayload or a single Issue.
func parseBody(body []byte) ([]model.Issue, error) {
	// Try wrapped payload first.
	var payload model.N8NPayload
	if err := json.Unmarshal(body, &payload); err == nil && len(payload.Issues) > 0 {
		return payload.Issues, nil
	}

	// Try array of issues.
	var issues []model.Issue
	if err := json.Unmarshal(body, &issues); err == nil && len(issues) > 0 {
		return issues, nil
	}

	// Try single issue.
	var single model.Issue
	if err := json.Unmarshal(body, &single); err == nil && single.Number != 0 {
		return []model.Issue{single}, nil
	}

	return nil, fmt.Errorf("unrecognized payload format")
}
