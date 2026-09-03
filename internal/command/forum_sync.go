package command

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/store"
)

const (
	forumSyncCommand = "forum-sync"

	forumSyncSubcommandSet    = "set"
	forumSyncSubcommandRemove = "remove"
	forumSyncSubcommandList   = "list"

	forumSyncOptionRepo  = "repo_url"
	forumSyncOptionForum = "forum"

	forumSyncRequestTimeout = 5 * time.Second
)

// ForumSyncHandler manages mappings from GitHub repositories to Discord forum
// channels.
type ForumSyncHandler struct {
	cfg    *config.Config
	logger *slog.Logger
	store  *store.Store
}

// NewForumSyncHandler creates a new ForumSyncHandler.
func NewForumSyncHandler(cfg *config.Config, logger *slog.Logger, store *store.Store) *ForumSyncHandler {
	return &ForumSyncHandler{cfg: cfg, logger: logger, store: store}
}

// ForumSyncDefinition returns the ApplicationCommand definition for /forum-sync.
func ForumSyncDefinition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        forumSyncCommand,
		Description: "Sinkronkan repo GitHub ke forum Discord",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        forumSyncSubcommandSet,
				Description: "Hubungkan repo GitHub ke channel forum",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        forumSyncOptionRepo,
						Description: "URL repo GitHub, contoh: https://github.com/org/repo",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        forumSyncOptionForum,
						Description: "Channel forum tujuan",
						Required:    true,
						ChannelTypes: []discordgo.ChannelType{
							discordgo.ChannelTypeGuildForum,
						},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        forumSyncSubcommandRemove,
				Description: "Hapus sinkronisasi repo GitHub",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        forumSyncOptionRepo,
						Description: "URL repo GitHub atau owner/repo",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        forumSyncSubcommandList,
				Description: "Lihat repo yang sudah disinkronkan",
			},
		},
	}
}

// Handle is the InteractionCreate callback for /forum-sync.
func (h *ForumSyncHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
	if !ok || data.Name != forumSyncCommand {
		return
	}

	callerID := interactionUserID(i)
	h.logger.Info("forum-sync command received", "caller_id", callerID)

	if !h.cfg.IsUserAllowed(callerID) {
		h.logger.Warn("unauthorized forum-sync attempt", "caller_id", callerID)
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
		h.logger.Error("forum-sync: expected exactly one subcommand", "count", len(data.Options))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), forumSyncRequestTimeout)
	defer cancel()

	subcommand := data.Options[0]
	var embed *discordgo.MessageEmbed
	switch subcommand.Name {
	case forumSyncSubcommandSet:
		repoURL, forumID := parseForumSyncSetOptions(subcommand.Options, s)
		embed = h.setMapping(ctx, repoURL, forumID, callerID)
	case forumSyncSubcommandRemove:
		repoURL := parseStringOption(subcommand.Options, forumSyncOptionRepo)
		embed = h.removeMapping(ctx, repoURL)
	case forumSyncSubcommandList:
		embed = h.listMappings(ctx)
	default:
		h.logger.Error("forum-sync: unknown subcommand", "name", subcommand.Name)
		embed = simpleFailureEmbed("Gagal Sinkronisasi Forum", "Subcommand tidak dikenal.")
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func parseForumSyncSetOptions(opts []*discordgo.ApplicationCommandInteractionDataOption, s *discordgo.Session) (repoURL, forumID string) {
	for _, opt := range opts {
		switch opt.Name {
		case forumSyncOptionRepo:
			repoURL = opt.StringValue()
		case forumSyncOptionForum:
			if ch := opt.ChannelValue(s); ch != nil {
				forumID = ch.ID
			}
		}
	}
	return
}

func (h *ForumSyncHandler) setMapping(ctx context.Context, rawRepoURL, forumID, callerID string) *discordgo.MessageEmbed {
	repoFullName, repoURL, err := normalizeGitHubRepo(rawRepoURL)
	if err != nil {
		return simpleFailureEmbed("Gagal Sinkronisasi Forum", err.Error())
	}
	if forumID == "" {
		return simpleFailureEmbed("Gagal Sinkronisasi Forum", "Channel forum tidak valid.")
	}
	if err := h.store.SetForumRepoChannel(ctx, repoFullName, repoURL, forumID, callerID); err != nil {
		h.logger.Error("forum-sync: failed to save mapping", "repo", repoFullName, "forum", forumID, "error", err)
		return simpleFailureEmbed("Gagal Sinkronisasi Forum", "Gagal menyimpan mapping repo ke database.")
	}
	return simpleSuccessEmbed("Forum Disinkronkan", fmt.Sprintf("Issue dari `%s` akan dibuat sebagai post di <#%s>.", repoFullName, forumID))
}

func (h *ForumSyncHandler) removeMapping(ctx context.Context, rawRepoURL string) *discordgo.MessageEmbed {
	repoFullName, _, err := normalizeGitHubRepo(rawRepoURL)
	if err != nil {
		return simpleFailureEmbed("Gagal Menghapus Sinkronisasi", err.Error())
	}
	removed, err := h.store.RemoveForumRepoChannel(ctx, repoFullName)
	if err != nil {
		h.logger.Error("forum-sync: failed to remove mapping", "repo", repoFullName, "error", err)
		return simpleFailureEmbed("Gagal Menghapus Sinkronisasi", "Gagal menghapus mapping dari database.")
	}
	if !removed {
		return simpleFailureEmbed("Gagal Menghapus Sinkronisasi", fmt.Sprintf("Repo `%s` belum disinkronkan ke forum.", repoFullName))
	}
	return simpleSuccessEmbed("Sinkronisasi Dihapus", fmt.Sprintf("Repo `%s` tidak lagi membuat post forum otomatis.", repoFullName))
}

func (h *ForumSyncHandler) listMappings(ctx context.Context) *discordgo.MessageEmbed {
	mappings, err := h.store.ListForumRepoChannels(ctx)
	if err != nil {
		h.logger.Error("forum-sync: failed to list mappings", "error", err)
		return simpleFailureEmbed("Gagal Membaca Sinkronisasi", "Gagal membaca mapping repo dari database.")
	}
	if len(mappings) == 0 {
		return simpleSuccessEmbed("Forum Sync", "Belum ada repo yang disinkronkan.")
	}
	lines := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		lines = append(lines, fmt.Sprintf("`%s` -> <#%s>", mapping.RepoFullName, mapping.ForumChannelID))
	}
	return simpleSuccessEmbed("Forum Sync", strings.Join(lines, "\n"))
}

func normalizeGitHubRepo(raw string) (fullName, normalizedURL string, err error) {
	value := strings.TrimSpace(raw)
	value = strings.TrimSuffix(value, ".git")
	if value == "" {
		return "", "", fmt.Errorf("Repo GitHub tidak boleh kosong.")
	}

	if !strings.Contains(value, "://") {
		parts := strings.Split(strings.Trim(value, "/"), "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			fullName = strings.ToLower(parts[0] + "/" + parts[1])
			return fullName, "https://github.com/" + fullName, nil
		}
		value = "https://" + value
	}

	parsed, parseErr := url.Parse(value)
	if parseErr != nil || parsed.Host == "" {
		return "", "", fmt.Errorf("Format repo GitHub tidak valid.")
	}
	if !strings.EqualFold(parsed.Host, "github.com") {
		return "", "", fmt.Errorf("Repo harus dari github.com.")
	}

	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("URL repo harus berbentuk `https://github.com/owner/repo`.")
	}
	fullName = strings.ToLower(parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"))
	return fullName, "https://github.com/" + fullName, nil
}

func parseStringOption(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range opts {
		if opt.Name == name {
			return opt.StringValue()
		}
	}
	return ""
}

func simpleSuccessEmbed(title, description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{Title: title, Description: description, Color: colorGreen}
}

func simpleFailureEmbed(title, description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{Title: title, Description: description, Color: colorRed}
}
