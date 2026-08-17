package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/model"
)

// Notifier sends Discord notifications for GitHub issues.
type Notifier struct {
	session *discordgo.Session
	cfg     *config.Config
	logger  *slog.Logger
}

// New creates a new Notifier.
func New(session *discordgo.Session, cfg *config.Config, logger *slog.Logger) *Notifier {
	return &Notifier{
		session: session,
		cfg:     cfg,
		logger:  logger,
	}
}

// Notify builds and sends a Discord embed message for the given issue.
// It implements the queue.Processor signature.
func (n *Notifier) Notify(ctx context.Context, issue model.Issue) error {
	channelID := n.resolveChannel(issue)
	mentions := n.buildMentions(issue.Assignees)
	embed := n.buildEmbed(issue)

	content := ""
	if mentions != "" {
		content = mentions
	}

	_, err := n.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: content,
		Embeds:  []*discordgo.MessageEmbed{embed},
	})
	if err != nil {
		return fmt.Errorf("send discord message: %w", err)
	}

	n.logger.Info("notification sent",
		"issue_number", issue.Number,
		"channel", channelID,
		"assignees", len(issue.Assignees),
	)
	return nil
}

// resolveChannel determines the target channel based on issue labels.
func (n *Notifier) resolveChannel(issue model.Issue) string {
	labels := make([]string, len(issue.Labels))
	for i, lbl := range issue.Labels {
		labels[i] = lbl.Name
	}
	return n.cfg.ResolveChannel(labels)
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
