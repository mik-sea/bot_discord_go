package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/model"
	"github.com/miksea/bot_discord_go/internal/store"
)

// Notifier sends Discord notifications for GitHub issues.
type Notifier struct {
	session *discordgo.Session
	cfg     *config.Config
	store   *store.InviteStore
	logger  *slog.Logger
}

// New creates a new Notifier.
func New(session *discordgo.Session, cfg *config.Config, store *store.InviteStore, logger *slog.Logger) *Notifier {
	return &Notifier{
		session: session,
		cfg:     cfg,
		store:   store,
		logger:  logger,
	}
}

// Notify builds and sends a Discord embed message for the given issue.
// It implements the queue.Processor signature.
func (n *Notifier) Notify(ctx context.Context, issue model.Issue) error {
	channelIDs := n.resolveChannels(ctx)
	mentions := n.buildMentions(issue.Assignees)
	embed := n.buildEmbed(issue)

	content := ""
	if mentions != "" {
		content = mentions
	}

	var failed []string
	for _, channelID := range channelIDs {
		if _, err := n.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Content: content,
			Embeds:  []*discordgo.MessageEmbed{embed},
		}); err != nil {
			n.logger.Error("failed to send discord notification",
				"issue_number", issue.Number,
				"channel", channelID,
				"error", err,
			)
			failed = append(failed, channelID)
			continue
		}

		n.logger.Info("notification sent",
			"issue_number", issue.Number,
			"channel", channelID,
			"assignees", len(issue.Assignees),
		)
	}

	if len(failed) > 0 {
		return fmt.Errorf("send discord message failed for channel(s): %s", strings.Join(failed, ", "))
	}
	return nil
}

// resolveChannels returns the default notification channel plus any extra
// global channels configured through slash commands.
func (n *Notifier) resolveChannels(ctx context.Context) []string {
	channels := []string{n.cfg.Discord.DefaultChannel}
	extras, err := n.store.ListNotificationChannels(ctx)
	if err != nil {
		n.logger.Error("failed to load notification channels; using default only", "error", err)
		return channels
	}

	seen := map[string]struct{}{n.cfg.Discord.DefaultChannel: {}}
	for _, channelID := range extras {
		if channelID == "" {
			continue
		}
		if _, ok := seen[channelID]; ok {
			continue
		}
		channels = append(channels, channelID)
		seen[channelID] = struct{}{}
	}
	return channels
}

// buildMentions builds a string of Discord user mentions from assignees.
func (n *Notifier) buildMentions(assignees []model.User) string {
	var parts []string
	for _, a := range assignees {
		if discordID := n.cfg.ResolveDiscordUser(a.Login); discordID != "" {
			parts = append(parts, fmt.Sprintf("<@%s>", discordID))
		}
	}
	return strings.Join(parts, " ")
}

// buildEmbed constructs the Discord embed for an issue.
func (n *Notifier) buildEmbed(issue model.Issue) *discordgo.MessageEmbed {
	labelStr := buildLabelString(issue.Labels)
	assigneeStr := buildAssigneeString(issue.Assignees)

	color := 0xE74C3C // red — open issue
	if issue.State == "closed" {
		color = 0x2ECC71 // green — closed
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("[%s] #%d %s", issue.Repository.FullName, issue.Number, issue.Title),
		URL:   issue.HTMLURL,
		Color: color,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🔗 Link Issue",
				Value:  fmt.Sprintf("[Buka di GitHub](%s)", issue.HTMLURL),
				Inline: false,
			},
			{
				Name:   "👥 Assignee",
				Value:  orDefault(assigneeStr, "_Tidak ada_"),
				Inline: true,
			},
			{
				Name:   "🏷️ Labels",
				Value:  orDefault(labelStr, "_Tidak ada_"),
				Inline: true,
			},
			{
				Name:   "📌 Status",
				Value:  issue.State,
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Dibuat: %s", issue.CreatedAt),
		},
	}

	if issue.Body != "" {
		body := issue.Body
		if len(body) > 300 {
			body = body[:300] + "..."
		}
		embed.Description = body
	}

	return embed
}

func buildLabelString(labels []model.Label) string {
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = "`" + l.Name + "`"
	}
	return strings.Join(names, " ")
}

func buildAssigneeString(assignees []model.User) string {
	names := make([]string, len(assignees))
	for i, a := range assignees {
		names[i] = "@" + a.Login
	}
	return strings.Join(names, ", ")
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
