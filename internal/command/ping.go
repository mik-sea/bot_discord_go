package command

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/clock"
	"github.com/miksea/bot_discord_go/internal/config"
)

const (
	colorGreen  = 0x2ECC71
	colorYellow = 0xF39C12
	colorRed    = 0xE74C3C

	// latency thresholds for colour-coding the embed.
	thresholdGood = 150 * time.Millisecond
	thresholdWarn = 400 * time.Millisecond
)

// PingHandler handles the /ping slash command.
type PingHandler struct {
	cfg    *config.Config
	logger *slog.Logger
}

// NewPingHandler creates a new PingHandler.
func NewPingHandler(cfg *config.Config, logger *slog.Logger) *PingHandler {
	return &PingHandler{cfg: cfg, logger: logger}
}

// Definition returns the ApplicationCommand definition that must be registered with Discord.
func Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "ping",
		Description: "🏓 Cek status bot dan ukur latency Discord (hanya untuk user tertentu)",
	}
}

// Handle is the InteractionCreate callback for the /ping command.
func (h *PingHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// ── Guard: only handle /ping ──────────────────────────────────────────────
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
	if !ok || data.Name != "ping" {
		return
	}

	userID := interactionUserID(i)

	h.logger.Info("ping command received", "user_id", userID)

	// ── Allowlist check ───────────────────────────────────────────────────────
	if !h.cfg.IsUserAllowed(userID) {
		h.logger.Warn("unauthorized ping attempt", "user_id", userID)
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{
					forbiddenEmbed(userID),
				},
				Flags: discordgo.MessageFlagsEphemeral, // only visible to the caller
			},
		})
		return
	}

	// ── Step 1: ACK immediately (records t0 on Discord side) ─────────────────
	t0 := time.Now()
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "🏓 Mengukur latency...",
		},
	}); err != nil {
		h.logger.Error("ping: failed to ack interaction", "error", err)
		return
	}

	// ── Step 2: Edit the response — delta = true round-trip time ─────────────
	roundTrip := time.Since(t0)
	heartbeat := s.HeartbeatLatency()

	embed := buildPingEmbed(roundTrip, heartbeat, userID)

	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: strPtr(""),
		Embeds:  &[]*discordgo.MessageEmbed{embed},
	}); err != nil {
		h.logger.Error("ping: failed to edit response", "error", err)
		return
	}

	h.logger.Info("ping responded",
		"user_id", userID,
		"round_trip_ms", roundTrip.Milliseconds(),
		"heartbeat_ms", heartbeat.Milliseconds(),
	)
}

// ─── Embed builders ───────────────────────────────────────────────────────────

func buildPingEmbed(roundTrip, heartbeat time.Duration, userID string) *discordgo.MessageEmbed {
	rtMs := roundTrip.Milliseconds()
	hbMs := heartbeat.Milliseconds()
	color := latencyColor(roundTrip)

	statusText := qualityLabel(roundTrip)

	return &discordgo.MessageEmbed{
		Title: "🏓 Pong!",
		// Description mendukung mention — <@id> akan render jadi tag biru yang bisa diklik.
		Description: fmt.Sprintf("Diminta oleh <@%s>", userID),
		Color:       color,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "⚡ Round-trip (API edit)",
				Value:  fmt.Sprintf("`%d ms`", rtMs),
				Inline: true,
			},
			{
				Name:   "💓 WebSocket Heartbeat",
				Value:  fmt.Sprintf("`%d ms`", hbMs),
				Inline: true,
			},
			{
				Name:   "📶 Kualitas Koneksi",
				Value:  statusText,
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			// Footer hanya plain text — jangan taruh mention di sini.
			Text: clock.Now().Format("02 Jan 2006 • 15:04:05 MST"),
		},
	}
}

func forbiddenEmbed(userID string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🚫 Akses Ditolak",
		Description: fmt.Sprintf("User <@%s> tidak memiliki izin untuk menggunakan command ini.", userID),
		Color:       colorRed,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Hubungi admin untuk mendapatkan akses.",
		},
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// interactionUserID safely extracts the user ID whether the interaction
// came from a guild (Member) or a DM (User).
func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func latencyColor(d time.Duration) int {
	switch {
	case d <= thresholdGood:
		return colorGreen
	case d <= thresholdWarn:
		return colorYellow
	default:
		return colorRed
	}
}

func qualityLabel(d time.Duration) string {
	switch {
	case d <= thresholdGood:
		return "🟢 Sangat Baik"
	case d <= thresholdWarn:
		return "🟡 Cukup"
	default:
		return "🔴 Lambat"
	}
}

func strPtr(s string) *string { return &s }
