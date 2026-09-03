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
	githubUserCommand = "github-user"

	githubUserSubcommandSet    = "set"
	githubUserSubcommandRemove = "remove"
	githubUserSubcommandList   = "list"

	githubUserOptionLogin = "github_username"
	githubUserOptionUser  = "user"

	githubUserRequestTimeout = 5 * time.Second
)

// GitHubUserHandler manages mappings from GitHub usernames to Discord users.
type GitHubUserHandler struct {
	cfg    *config.Config
	logger *slog.Logger
	store  *store.Store
}

// NewGitHubUserHandler creates a new GitHubUserHandler.
func NewGitHubUserHandler(cfg *config.Config, logger *slog.Logger, store *store.Store) *GitHubUserHandler {
	return &GitHubUserHandler{cfg: cfg, logger: logger, store: store}
}

// GitHubUserDefinition returns the ApplicationCommand definition for /github-user.
func GitHubUserDefinition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        githubUserCommand,
		Description: "Sinkronkan username GitHub dengan akun Discord",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        githubUserSubcommandSet,
				Description: "Hubungkan username GitHub ke user Discord",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        githubUserOptionLogin,
						Description: "Username GitHub, contoh: octocat",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionUser,
						Name:        githubUserOptionUser,
						Description: "Akun Discord yang akan di-mention",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        githubUserSubcommandRemove,
				Description: "Hapus sinkronisasi username GitHub",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        githubUserOptionLogin,
						Description: "Username GitHub",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        githubUserSubcommandList,
				Description: "Lihat username GitHub yang sudah disinkronkan",
			},
		},
	}
}

// Handle is the InteractionCreate callback for /github-user.
func (h *GitHubUserHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
	if !ok || data.Name != githubUserCommand {
		return
	}

	callerID := interactionUserID(i)
	h.logger.Info("github-user command received", "caller_id", callerID)

	if !h.cfg.IsUserAllowed(callerID) {
		h.logger.Warn("unauthorized github-user attempt", "caller_id", callerID)
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
		h.logger.Error("github-user: expected exactly one subcommand", "count", len(data.Options))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), githubUserRequestTimeout)
	defer cancel()

	subcommand := data.Options[0]
	var embed *discordgo.MessageEmbed
	switch subcommand.Name {
	case githubUserSubcommandSet:
		login, user := parseGitHubUserSetOptions(subcommand.Options, s)
		embed = h.setLink(ctx, login, user, callerID)
	case githubUserSubcommandRemove:
		login := normalizeGitHubLogin(parseStringOption(subcommand.Options, githubUserOptionLogin))
		embed = h.removeLink(ctx, login)
	case githubUserSubcommandList:
		embed = h.listLinks(ctx)
	default:
		h.logger.Error("github-user: unknown subcommand", "name", subcommand.Name)
		embed = simpleFailureEmbed("Gagal Sinkronisasi User", "Subcommand tidak dikenal.")
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func parseGitHubUserSetOptions(opts []*discordgo.ApplicationCommandInteractionDataOption, s *discordgo.Session) (login string, user *discordgo.User) {
	for _, opt := range opts {
		switch opt.Name {
		case githubUserOptionLogin:
			login = normalizeGitHubLogin(opt.StringValue())
		case githubUserOptionUser:
			user = opt.UserValue(s)
		}
	}
	return
}

func (h *GitHubUserHandler) setLink(ctx context.Context, login string, user *discordgo.User, callerID string) *discordgo.MessageEmbed {
	if login == "" {
		return simpleFailureEmbed("Gagal Sinkronisasi User", "Username GitHub tidak valid.")
	}
	if user == nil {
		return simpleFailureEmbed("Gagal Sinkronisasi User", "User Discord tidak valid.")
	}
	if err := h.store.SetGitHubUserLink(ctx, login, user.ID, callerID); err != nil {
		h.logger.Error("github-user: failed to save link", "github_username", login, "discord_user_id", user.ID, "error", err)
		return simpleFailureEmbed("Gagal Sinkronisasi User", "Gagal menyimpan mapping user ke database.")
	}
	return simpleSuccessEmbed("User Disinkronkan", fmt.Sprintf("Assignee GitHub `%s` akan mention <@%s>.", login, user.ID))
}

func (h *GitHubUserHandler) removeLink(ctx context.Context, login string) *discordgo.MessageEmbed {
	if login == "" {
		return simpleFailureEmbed("Gagal Menghapus Sinkronisasi", "Username GitHub tidak valid.")
	}
	removed, err := h.store.RemoveGitHubUserLink(ctx, login)
	if err != nil {
		h.logger.Error("github-user: failed to remove link", "github_username", login, "error", err)
		return simpleFailureEmbed("Gagal Menghapus Sinkronisasi", "Gagal menghapus mapping user dari database.")
	}
	if !removed {
		return simpleFailureEmbed("Gagal Menghapus Sinkronisasi", fmt.Sprintf("Username GitHub `%s` belum disinkronkan.", login))
	}
	return simpleSuccessEmbed("Sinkronisasi User Dihapus", fmt.Sprintf("Username GitHub `%s` tidak lagi mention user Discord otomatis.", login))
}

func (h *GitHubUserHandler) listLinks(ctx context.Context) *discordgo.MessageEmbed {
	links, err := h.store.ListGitHubUserLinks(ctx)
	if err != nil {
		h.logger.Error("github-user: failed to list links", "error", err)
		return simpleFailureEmbed("Gagal Membaca Sinkronisasi", "Gagal membaca mapping user dari database.")
	}
	if len(links) == 0 {
		return simpleSuccessEmbed("GitHub User Sync", "Belum ada user GitHub yang disinkronkan.")
	}
	lines := make([]string, 0, len(links))
	for _, link := range links {
		lines = append(lines, fmt.Sprintf("`%s` -> <@%s>", link.GitHubLogin, link.DiscordUserID))
	}
	return simpleSuccessEmbed("GitHub User Sync", strings.Join(lines, "\n"))
}

func normalizeGitHubLogin(login string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(login, "@")))
}
