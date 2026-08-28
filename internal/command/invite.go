package command

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/planapi"
	"github.com/miksea/bot_discord_go/internal/store"
)

const (
	optionProjectKey = "project_key"
	optionUser       = "user"

	// emailDomain is appended to the invited Discord user's username to build
	// the email address the plan API expects.
	emailDomain = "@kancadigital.com"

	inviteRequestTimeout = 15 * time.Second
)

// InviteHandler handles the /invite slash command: it registers an invite
// with the plan API for a Discord user and sends them the invite link by DM.
type InviteHandler struct {
	cfg    *config.Config
	logger *slog.Logger
	store  *store.InviteStore
	plan   *planapi.Client
}

// NewInviteHandler creates a new InviteHandler.
func NewInviteHandler(cfg *config.Config, logger *slog.Logger, store *store.InviteStore, plan *planapi.Client) *InviteHandler {
	return &InviteHandler{cfg: cfg, logger: logger, store: store, plan: plan}
}

// InviteDefinition returns the ApplicationCommand definition for /invite.
func InviteDefinition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "invite",
		Description: "📩 Undang user Discord ke project plan (hanya untuk user tertentu)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        optionProjectKey,
				Description: "Key project plan tujuan",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        optionUser,
				Description: "User Discord yang akan diundang",
				Required:    true,
			},
		},
	}
}

// Handle is the InteractionCreate callback for the /invite command.
func (h *InviteHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
	if !ok || data.Name != "invite" {
		return
	}

	callerID := interactionUserID(i)
	h.logger.Info("invite command received", "caller_id", callerID)

	if !h.cfg.IsUserAllowed(callerID) {
		h.logger.Warn("unauthorized invite attempt", "caller_id", callerID)
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{forbiddenEmbed(callerID)},
				Flags:  discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	var projectKey string
	var targetUser *discordgo.User
	for _, opt := range data.Options {
		switch opt.Name {
		case optionProjectKey:
			projectKey = opt.StringValue()
		case optionUser:
			targetUser = opt.UserValue(s)
		}
	}

	// ACK dulu — memanggil plan API dan mengirim DM butuh waktu lebih dari
	// batas 3 detik yang diberikan Discord untuk respons awal.
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		h.logger.Error("invite: failed to ack interaction", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), inviteRequestTimeout)
	defer cancel()

	embed := h.createInvite(ctx, s, projectKey, targetUser)

	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	}); err != nil {
		h.logger.Error("invite: failed to edit response", "error", err)
	}
}

// createInvite performs the full invite flow and returns the embed to show
// the caller. Every failure path returns early with an explanatory embed
// instead of an error, since this result is displayed directly to the user.
func (h *InviteHandler) createInvite(ctx context.Context, s *discordgo.Session, projectKey string, target *discordgo.User) *discordgo.MessageEmbed {
	inviteID, err := h.store.Create(ctx, target.ID, target.Username, projectKey)
	if err != nil {
		h.logger.Error("invite: failed to save invite record", "error", err)
		return failureEmbed("Gagal menyimpan data undangan ke database.")
	}

	email := strings.ToLower(target.Username) + emailDomain
	planInvite, err := h.plan.CreateInvite(ctx, projectKey, email)
	if err != nil {
		h.logger.Error("invite: plan api call failed", "error", err, "project_key", projectKey)
		return failureEmbed(fmt.Sprintf("Gagal membuat undangan di plan API untuk project `%s`.", projectKey))
	}

	if err := h.store.SetTokenID(ctx, inviteID, planInvite.Token); err != nil {
		h.logger.Error("invite: failed to save token_id", "error", err)
		return failureEmbed("Undangan berhasil dibuat di plan API, tapi gagal disimpan ke database.")
	}

	inviteLink := fmt.Sprintf("%s/%s", h.cfg.PlanAPI.InviteWebURL, planInvite.Token)

	if err := sendInviteDM(s, target.ID, projectKey, inviteLink); err != nil {
		h.logger.Warn("invite: failed to DM invited user", "error", err, "target_id", target.ID)
		return partialSuccessEmbed(target.ID, projectKey, inviteLink)
	}

	h.logger.Info("invite created", "target_id", target.ID, "project_key", projectKey, "invite_id", inviteID)
	return successEmbed(target.ID, projectKey)
}

// sendInviteDM opens a DM channel with the target user and sends the invite link.
func sendInviteDM(s *discordgo.Session, targetID, projectKey, inviteLink string) error {
	channel, err := s.UserChannelCreate(targetID)
	if err != nil {
		return fmt.Errorf("open DM channel: %w", err)
	}

	_, err = s.ChannelMessageSendEmbed(channel.ID, dmEmbed(projectKey, inviteLink))
	if err != nil {
		return fmt.Errorf("send DM message: %w", err)
	}
	return nil
}

// ─── Embed builders ───────────────────────────────────────────────────────────

func dmEmbed(projectKey, inviteLink string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "📩 Undangan Project Plan",
		Description: fmt.Sprintf("Kamu diundang ke project **%s**. Klik link di bawah untuk bergabung:\n%s", projectKey, inviteLink),
		Color:       colorGreen,
	}
}

func successEmbed(targetID, projectKey string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "✅ Undangan Terkirim",
		Description: fmt.Sprintf("<@%s> berhasil diundang ke project **%s** dan link undangan sudah dikirim lewat DM.", targetID, projectKey),
		Color:       colorGreen,
	}
}

func partialSuccessEmbed(targetID, projectKey, inviteLink string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: "⚠️ Undangan Dibuat, DM Gagal Terkirim",
		Description: fmt.Sprintf(
			"<@%s> berhasil diundang ke project **%s**, tapi bot tidak bisa mengirim DM (kemungkinan DM ditutup).\nKirim link ini secara manual:\n%s",
			targetID, projectKey, inviteLink,
		),
		Color: colorYellow,
	}
}

func failureEmbed(reason string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🚫 Gagal Membuat Undangan",
		Description: reason,
		Color:       colorRed,
	}
}
