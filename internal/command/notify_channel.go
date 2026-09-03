package command

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/store"
)

const (
	notifyChannelCommand = "notify-channel"

	subcommandAdd    = "add"
	subcommandRemove = "remove"
	subcommandList   = "list"

	notifyChannelRequestTimeout = 5 * time.Second
)

// NotifyChannelHandler manages the global Discord channels that receive issue
// notifications in addition to DISCORD_DEFAULT_CHANNEL.
type NotifyChannelHandler struct {
	cfg    *config.Config
	logger *slog.Logger
	store  *store.Store
}

// NewNotifyChannelHandler creates a new NotifyChannelHandler.
func NewNotifyChannelHandler(cfg *config.Config, logger *slog.Logger, store *store.Store) *NotifyChannelHandler {
	return &NotifyChannelHandler{cfg: cfg, logger: logger, store: store}
}

// NotifyChannelDefinition returns the ApplicationCommand definition for
// /notify-channel.
func NotifyChannelDefinition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        notifyChannelCommand,
		Description: "Atur channel global untuk notifikasi issue GitHub",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        subcommandAdd,
				Description: "Tambahkan channel penerima notifikasi",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        optionChannel,
						Description: "Channel yang akan ditambahkan",
						Required:    true,
						ChannelTypes: []discordgo.ChannelType{
							discordgo.ChannelTypeGuildText,
							discordgo.ChannelTypeGuildNews,
						},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        subcommandRemove,
				Description: "Hapus channel tambahan penerima notifikasi",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        optionChannel,
						Description: "Channel yang akan dihapus",
						Required:    true,
						ChannelTypes: []discordgo.ChannelType{
							discordgo.ChannelTypeGuildText,
							discordgo.ChannelTypeGuildNews,
						},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        subcommandList,
				Description: "Lihat channel penerima notifikasi",
			},
		},
	}
}

// Handle is the InteractionCreate callback for /notify-channel.
func (h *NotifyChannelHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
	if !ok || data.Name != notifyChannelCommand {
		return
	}

	callerID := interactionUserID(i)
	h.logger.Info("notify-channel command received", "caller_id", callerID)

	if !h.cfg.IsUserAllowed(callerID) {
		h.logger.Warn("unauthorized notify-channel attempt", "caller_id", callerID)
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
		h.logger.Error("notify-channel: expected exactly one subcommand", "count", len(data.Options))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), notifyChannelRequestTimeout)
	defer cancel()

	subcommand := data.Options[0]
	var embed *discordgo.MessageEmbed

	switch subcommand.Name {
	case subcommandAdd:
		channelID := parseChannelID(subcommand.Options, s)
		embed = h.addChannel(ctx, channelID, callerID)
	case subcommandRemove:
		channelID := parseChannelID(subcommand.Options, s)
		embed = h.removeChannel(ctx, channelID)
	case subcommandList:
		embed = h.listChannels(ctx)
	default:
		h.logger.Error("notify-channel: unknown subcommand", "name", subcommand.Name)
		embed = notifyChannelFailureEmbed("Subcommand tidak dikenal.")
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func parseChannelID(opts []*discordgo.ApplicationCommandInteractionDataOption, s *discordgo.Session) string {
	for _, opt := range opts {
		if opt.Name != optionChannel {
			continue
		}
		if ch := opt.ChannelValue(s); ch != nil {
			return ch.ID
		}
		return opt.StringValue()
	}
	return ""
}

func (h *NotifyChannelHandler) addChannel(ctx context.Context, channelID, callerID string) *discordgo.MessageEmbed {
	if channelID == "" {
		return notifyChannelFailureEmbed("Channel tidak valid.")
	}
	if channelID == h.defaultChannel(ctx) {
		return notifyChannelSuccessEmbed("Channel Utama Sudah Aktif", fmt.Sprintf("<#%s> sudah menjadi channel utama.", channelID))
	}
	if err := h.store.AddNotificationChannel(ctx, channelID, callerID); err != nil {
		h.logger.Error("notify-channel: failed to add channel", "channel_id", channelID, "error", err)
		return notifyChannelFailureEmbed("Gagal menyimpan channel ke database.")
	}
	return notifyChannelSuccessEmbed("Channel Ditambahkan", fmt.Sprintf("<#%s> sekarang ikut menerima notifikasi issue.", channelID))
}

func (h *NotifyChannelHandler) removeChannel(ctx context.Context, channelID string) *discordgo.MessageEmbed {
	if channelID == "" {
		return notifyChannelFailureEmbed("Channel tidak valid.")
	}
	if channelID == h.defaultChannel(ctx) {
		return notifyChannelFailureEmbed("Channel utama tidak bisa dihapus lewat command ini — gunakan `/set-channel` untuk menggantinya.")
	}
	removed, err := h.store.RemoveNotificationChannel(ctx, channelID)
	if err != nil {
		h.logger.Error("notify-channel: failed to remove channel", "channel_id", channelID, "error", err)
		return notifyChannelFailureEmbed("Gagal menghapus channel dari database.")
	}
	if !removed {
		return notifyChannelFailureEmbed(fmt.Sprintf("<#%s> belum ada di daftar channel tambahan.", channelID))
	}
	return notifyChannelSuccessEmbed("Channel Dihapus", fmt.Sprintf("<#%s> tidak lagi menerima notifikasi issue.", channelID))
}

func (h *NotifyChannelHandler) listChannels(ctx context.Context) *discordgo.MessageEmbed {
	defaultChannel := h.defaultChannel(ctx)

	extras, err := h.store.ListNotificationChannels(ctx)
	if err != nil {
		h.logger.Error("notify-channel: failed to list channels", "error", err)
		return notifyChannelFailureEmbed("Gagal membaca daftar channel dari database.")
	}

	lines := []string{fmt.Sprintf("Utama: <#%s>", defaultChannel)}
	for _, channelID := range extras {
		if channelID != "" && channelID != defaultChannel {
			lines = append(lines, fmt.Sprintf("Tambahan: <#%s>", channelID))
		}
	}
	return notifyChannelSuccessEmbed("Channel Notifikasi", strings.Join(lines, "\n"))
}

// defaultChannel returns the /set-channel override if one has been set,
// otherwise the static DISCORD_DEFAULT_CHANNEL from config.
func (h *NotifyChannelHandler) defaultChannel(ctx context.Context) string {
	if override, ok, err := h.store.GetSetting(ctx, store.SettingKeyDefaultChannel); err != nil {
		h.logger.Warn("notify-channel: failed to read default channel override", "error", err)
	} else if ok {
		return override
	}
	return h.cfg.Discord.DefaultChannel
}

func notifyChannelSuccessEmbed(title, description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       colorGreen,
	}
}

func notifyChannelFailureEmbed(reason string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "Gagal Mengatur Channel",
		Description: reason,
		Color:       colorRed,
	}
}
