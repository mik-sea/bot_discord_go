package command

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/mailer"
	"github.com/miksea/bot_discord_go/internal/planapi"
	"github.com/miksea/bot_discord_go/internal/store"
)

const (
	subcommandUser  = "user"
	subcommandEmail = "email"

	optionProjectKey = "project_key"
	optionUser       = "user"
	optionEmail      = "email"

	inviteRequestTimeout = 15 * time.Second
)

var emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// InviteHandler handles the /invite slash command: it registers an invite
// with the plan API and, when the invitee is a Discord member, sends them
// the invite link by DM.
//
// Two variants are supported as subcommands:
//   - /invite user  — invitee identified by tagging a Discord user
//   - /invite email — invitee identified by email directly, for people who
//     are not on Discord at all
type InviteHandler struct {
	cfg    *config.Config
	logger *slog.Logger
	store  *store.InviteStore
	plan   *planapi.Client
	mail   *mailer.Mailer
}

// NewInviteHandler creates a new InviteHandler.
func NewInviteHandler(cfg *config.Config, logger *slog.Logger, store *store.InviteStore, plan *planapi.Client, mail *mailer.Mailer) *InviteHandler {
	return &InviteHandler{cfg: cfg, logger: logger, store: store, plan: plan, mail: mail}
}

// InviteDefinition returns the ApplicationCommand definition for /invite.
func InviteDefinition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "invite",
		Description: "📩 Undang ke project plan (hanya untuk user tertentu)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        subcommandUser,
				Description: "Undang lewat tag user Discord",
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
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        subcommandEmail,
				Description: "Undang lewat email langsung (untuk orang di luar Discord)",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        optionProjectKey,
						Description: "Key project plan tujuan",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        optionEmail,
						Description: "Alamat email yang akan diundang",
						Required:    true,
					},
				},
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

	if len(data.Options) != 1 {
		h.logger.Error("invite: expected exactly one subcommand", "count", len(data.Options))
		return
	}
	subcommand := data.Options[0]

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

	var embed *discordgo.MessageEmbed
	switch subcommand.Name {
	case subcommandUser:
		projectKey, targetUser := parseUserOptions(subcommand.Options, s)
		embed = h.createInviteForUser(ctx, s, projectKey, targetUser)
	case subcommandEmail:
		projectKey, email := parseEmailOptions(subcommand.Options)
		embed = h.createInviteForEmail(ctx, projectKey, email)
	default:
		h.logger.Error("invite: unknown subcommand", "name", subcommand.Name)
		embed = failureEmbed("Subcommand tidak dikenal.")
	}

	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	}); err != nil {
		h.logger.Error("invite: failed to edit response", "error", err)
	}
}

func parseUserOptions(opts []*discordgo.ApplicationCommandInteractionDataOption, s *discordgo.Session) (projectKey string, target *discordgo.User) {
	for _, opt := range opts {
		switch opt.Name {
		case optionProjectKey:
			projectKey = opt.StringValue()
		case optionUser:
			target = opt.UserValue(s)
		}
	}
	return
}

func parseEmailOptions(opts []*discordgo.ApplicationCommandInteractionDataOption) (projectKey, email string) {
	for _, opt := range opts {
		switch opt.Name {
		case optionProjectKey:
			projectKey = opt.StringValue()
		case optionEmail:
			email = opt.StringValue()
		}
	}
	return
}

// createInviteForUser performs the invite flow for a tagged Discord user and
// returns the embed to show the caller. Every failure path returns early
// with an explanatory embed instead of an error, since this result is
// displayed directly to the user.
func (h *InviteHandler) createInviteForUser(ctx context.Context, s *discordgo.Session, projectKey string, target *discordgo.User) *discordgo.MessageEmbed {
	// Email ini derivasi dari username Discord, bukan alamat aktif — hanya
	// dipakai untuk memenuhi syarat plan API dan disimpan ke db sebagai
	// catatan, tidak pernah benar-benar dikirimi email.
	email := strings.ToLower(target.Username) + h.cfg.PlanAPI.InviteEmailDomain

	planInvite, err := h.plan.CreateInvite(ctx, projectKey, email)
	if err != nil {
		h.logger.Error("invite: plan api call failed", "error", err, "project_key", projectKey)
		return failureEmbed(fmt.Sprintf("Gagal membuat undangan di plan API untuk project `%s`.", projectKey))
	}

	// Baru disimpan ke db setelah plan API konfirmasi sukses, supaya invite
	// yang gagal tidak ikut tercatat.
	inviteID, err := h.store.CreateForUser(ctx, target.ID, target.Username, email, projectKey, planInvite.Token)
	if err != nil {
		h.logger.Error("invite: failed to save invite record", "error", err)
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

// createInviteForEmail performs the invite flow for a plain email address —
// used for people who are not Discord members. The invite link is emailed
// directly to that address via SMTP; if sending fails (or SMTP isn't
// configured), the link is shown in the (ephemeral) reply instead so the
// caller can forward it themselves.
func (h *InviteHandler) createInviteForEmail(ctx context.Context, projectKey, email string) *discordgo.MessageEmbed {
	if !emailPattern.MatchString(email) {
		return failureEmbed(fmt.Sprintf("Format email `%s` tidak valid.", email))
	}

	planInvite, err := h.plan.CreateInvite(ctx, projectKey, email)
	if err != nil {
		h.logger.Error("invite: plan api call failed", "error", err, "project_key", projectKey)
		return failureEmbed(fmt.Sprintf("Gagal membuat undangan di plan API untuk project `%s`.", projectKey))
	}

	// Baru disimpan ke db setelah plan API konfirmasi sukses, supaya invite
	// yang gagal tidak ikut tercatat.
	inviteID, err := h.store.CreateForEmail(ctx, email, projectKey, planInvite.Token)
	if err != nil {
		h.logger.Error("invite: failed to save invite record", "error", err)
		return failureEmbed("Undangan berhasil dibuat di plan API, tapi gagal disimpan ke database.")
	}

	inviteLink := fmt.Sprintf("%s/%s", h.cfg.PlanAPI.InviteWebURL, planInvite.Token)

	if err := h.mail.Send(email, emailInviteSubject(projectKey), emailInviteBody(projectKey, inviteLink)); err != nil {
		h.logger.Warn("invite: failed to send invite email", "error", err, "email", email)
		return partialSuccessEmailEmbed(email, projectKey, inviteLink)
	}

	h.logger.Info("invite created", "email", email, "project_key", projectKey, "invite_id", inviteID)
	return emailSuccessEmbed(email, projectKey)
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

func emailInviteSubject(projectKey string) string {
	return fmt.Sprintf("Undangan Project Plan: %s", projectKey)
}

func emailInviteBody(projectKey, inviteLink string) string {
	return fmt.Sprintf(
		"Kamu diundang untuk bergabung ke project %s.\n\nKlik link berikut untuk menerima undangan:\n%s",
		projectKey, inviteLink,
	)
}

func emailSuccessEmbed(email, projectKey string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "✅ Undangan Terkirim",
		Description: fmt.Sprintf("Undangan untuk project **%s** berhasil dibuat dan sudah dikirim lewat email ke **%s**.", projectKey, email),
		Color:       colorGreen,
	}
}

func partialSuccessEmailEmbed(email, projectKey, inviteLink string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: "⚠️ Undangan Dibuat, Email Gagal Terkirim",
		Description: fmt.Sprintf(
			"Undangan untuk project **%s** berhasil dibuat, tapi bot gagal mengirim email ke **%s**.\nKirim link ini secara manual:\n%s",
			projectKey, email, inviteLink,
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
