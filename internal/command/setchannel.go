package command

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/store"
)

const optionChannel = "channel"

// SetChannelHandler handles the /set-channel slash command: it changes the
// default Discord channel that GitHub issue notifications fall back to,
// without needing to edit DISCORD_DEFAULT_CHANNEL and restart the bot.
type SetChannelHandler struct {
	cfg    *config.Config
	logger *slog.Logger
	store  *store.Store
}

// NewSetChannelHandler creates a new SetChannelHandler.
func NewSetChannelHandler(cfg *config.Config, logger *slog.Logger, store *store.Store) *SetChannelHandler {
	return &SetChannelHandler{cfg: cfg, logger: logger, store: store}
}

// SetChannelDefinition returns the ApplicationCommand definition for /set-channel.
func SetChannelDefinition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "set-channel",
		Description: "🔀 Ubah channel default untuk notifikasi issue GitHub (hanya untuk user tertentu)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionChannel,
				Name:         optionChannel,
				Description:  "Channel tujuan notifikasi issue GitHub",
				ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
				Required:     true,
			},
		},
	}
}

// Handle is the InteractionCreate callback for the /set-channel command.
func (h *SetChannelHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
	if !ok || data.Name != "set-channel" {
		return
	}

	callerID := interactionUserID(i)
	h.logger.Info("set-channel command received", "caller_id", callerID)

	if !h.cfg.IsUserAllowed(callerID) {
		h.logger.Warn("unauthorized set-channel attempt", "caller_id", callerID)
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{forbiddenEmbed(callerID)},
				Flags:  discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	var channel *discordgo.Channel
	for _, opt := range data.Options {
		if opt.Name == optionChannel {
			channel = opt.ChannelValue(s)
		}
	}
	if channel == nil {
		h.logger.Error("set-channel: missing channel option")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	embed := h.setDefaultChannel(ctx, channel.ID)

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *SetChannelHandler) setDefaultChannel(ctx context.Context, channelID string) *discordgo.MessageEmbed {
	if err := h.store.SetSetting(ctx, store.SettingKeyDefaultChannel, channelID); err != nil {
		h.logger.Error("set-channel: failed to save setting", "error", err)
		return failureEmbed("Gagal menyimpan channel default ke database.")
	}

	h.logger.Info("default channel updated", "channel_id", channelID)
	return &discordgo.MessageEmbed{
		Title:       "✅ Channel Default Diubah",
		Description: fmt.Sprintf("Notifikasi issue GitHub (yang tidak cocok mapping repo/label) sekarang dikirim ke <#%s>.", channelID),
		Color:       colorGreen,
	}
}
