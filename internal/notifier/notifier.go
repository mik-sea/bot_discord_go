package notifier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/model"
	"github.com/miksea/bot_discord_go/internal/store"
)

// allowedForumTagNames adalah label GitHub yang boleh diterjemahkan menjadi
// tag forum Discord. Daftar ini sesuai dengan label default GitHub supaya
// cocok dengan kebanyakan repo tanpa konfigurasi tambahan; tag yang belum ada
// di forum-nya otomatis dilewati (lihat resolveForumTags), jadi menambah
// nama di sini aman meski forum belum punya tag tersebut.
var allowedForumTagNames = map[string]struct{}{
	"bug":           {},
	"duplicate":     {},
	"documentation": {},
	"first issue":   {},
	"enhancement":   {},
	"help wanted":   {},
	"invalid":       {},
	"question":      {},
	"wontfix":       {},
	"security":      {},
	"dependencies":  {},
}

// forumTagAliases memetakan variasi nama label GitHub ke nama tag forum yang
// dipakai bot (kunci di allowedForumTagNames), supaya mis. label default
// GitHub "good first issue" tetap cocok dengan tag forum "First Issue".
var forumTagAliases = map[string]string{
	"good first issue": "first issue",
}

// statusForumTagNames memetakan status issue ke nama tag forum yang dicoba
// dipasang otomatis (jika forum sudah punya tag dengan nama tersebut), agar
// post yang closed dan yang masih open bisa dibedakan langsung dari daftar
// thread forum, tanpa perlu membuka postnya.
var statusForumTagNames = map[string]string{
	"open":   "open",
	"closed": "closed",
}

// Notifier sends Discord notifications for GitHub issues.
type Notifier struct {
	session *discordgo.Session
	cfg     *config.Config
	store   *store.Store
	logger  *slog.Logger
}

// New creates a new Notifier.
func New(session *discordgo.Session, cfg *config.Config, dataStore *store.Store, logger *slog.Logger) *Notifier {
	return &Notifier{
		session: session,
		cfg:     cfg,
		store:   dataStore,
		logger:  logger,
	}
}

// Notify builds and sends a Discord notification for the given issue. Repos
// mapped to a forum channel create a forum post; unmapped repos fall back to
// the regular channel notification flow.
// It implements the queue.Processor signature.
func (n *Notifier) Notify(ctx context.Context, issue model.Issue) error {
	if forumID, ok := n.resolveForum(ctx, issue); ok {
		return n.notifyForum(ctx, issue, forumID)
	}

	channelIDs := n.resolveChannels(ctx, issue)
	mentions := n.buildMentions(ctx, issue.Assignees)
	embed := n.buildEmbed(issue)

	content := ""
	if mentions != "" {
		content = mentions
	}

	var sendErrs []error
	for _, channelID := range channelIDs {
		_, err := n.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Content: content,
			Embeds:  []*discordgo.MessageEmbed{embed},
		})
		if err != nil {
			sendErrs = append(sendErrs, fmt.Errorf("send discord message to %s: %w", channelID, err))
			continue
		}
		n.logger.Info("notification sent",
			"issue_number", issue.Number,
			"channel", channelID,
			"assignees", len(issue.Assignees),
		)
	}
	if len(sendErrs) > 0 {
		return errors.Join(sendErrs...)
	}
	return nil
}

// notifyForum sends the notification for one issue to its forum channel.
// The same GitHub issue always reuses the same forum thread — created once
// on the first event and edited in place on every later event (title/tags,
// embed, archived/locked state) — instead of a new post per event, so the
// forum's thread list only ever has one entry per issue.
func (n *Notifier) notifyForum(ctx context.Context, issue model.Issue, forumID string) error {
	repo := strings.ToLower(strings.TrimSpace(issue.Repository.FullName))
	mentions := n.buildMentions(ctx, issue.Assignees)
	embed := n.buildEmbed(issue)
	appliedTags := n.resolveForumTags(ctx, forumID, issue.Labels, issue.State)

	existing, hasExisting, err := n.store.GetForumPost(ctx, repo, issue.Number, forumID)
	if err != nil {
		n.logger.Warn("failed to read forum post mapping", "repo", repo, "issue_number", issue.Number, "error", err)
	}
	if hasExisting {
		if err := n.updateForumPost(ctx, existing, issue, mentions, embed, appliedTags); err != nil {
			n.logger.Warn("failed to update existing forum post, creating a new one instead",
				"thread", existing.ThreadID, "issue_number", issue.Number, "error", err)
		} else {
			return nil
		}
	}

	return n.createForumPost(ctx, repo, issue, forumID, mentions, embed, appliedTags)
}

func (n *Notifier) createForumPost(ctx context.Context, repo string, issue model.Issue, forumID, mentions string, embed *discordgo.MessageEmbed, appliedTags []string) error {
	post, err := n.session.ForumThreadStartComplex(forumID, &discordgo.ThreadStart{
		Name:                forumPostTitle(issue),
		AutoArchiveDuration: 1440,
		AppliedTags:         appliedTags,
	}, &discordgo.MessageSend{
		Content: mentions,
		Embeds:  []*discordgo.MessageEmbed{embed},
	})
	if err != nil {
		return fmt.Errorf("create forum post in %s: %w", forumID, err)
	}

	messageID := post.LastMessageID
	if messageID == "" {
		// Beberapa respons create-thread belum mengisi last_message_id;
		// ambil ulang channel-nya supaya starter message tetap bisa diedit
		// nanti alih-alih diam-diam kehilangan ID pesannya.
		if refreshed, err := n.session.Channel(post.ID, discordgo.WithContext(ctx)); err != nil {
			n.logger.Warn("failed to refresh forum post to resolve starter message id", "thread", post.ID, "error", err)
		} else {
			messageID = refreshed.LastMessageID
		}
	}

	if err := n.store.SetForumPost(ctx, repo, issue.Number, forumID, post.ID, messageID); err != nil {
		n.logger.Warn("failed to save forum post mapping", "repo", repo, "issue_number", issue.Number, "error", err)
	}

	n.logger.Info("forum notification post created",
		"issue_number", issue.Number,
		"repo", issue.Repository.FullName,
		"forum", forumID,
		"post", post.ID,
		"assignees", len(issue.Assignees),
		"tags", len(appliedTags),
	)
	return nil
}

// updateForumPost edits the thread already tracked for this issue in place:
// renames it (status icon + title), refreshes its tags, edits the starter
// message's embed, and archives/locks it once the issue is closed
// (unarchiving/unlocking it again if the issue gets reopened).
//
// Discord rejects message edits inside an archived thread ("Thread is
// archived", code 50083), so a previously-closed post has to be unarchived
// first before its name/tags/embed can be touched, then re-archived at the
// end if the issue is still closed.
func (n *Notifier) updateForumPost(ctx context.Context, existing store.ForumPost, issue model.Issue, mentions string, embed *discordgo.MessageEmbed, appliedTags []string) error {
	threadID := existing.ThreadID
	closed := strings.EqualFold(strings.TrimSpace(issue.State), "closed")
	open := false

	if _, err := n.session.ChannelEdit(threadID, &discordgo.ChannelEdit{
		Name:        forumPostTitle(issue),
		AppliedTags: &appliedTags,
		Archived:    &open,
		Locked:      &open,
	}, discordgo.WithContext(ctx)); err != nil {
		return fmt.Errorf("update forum thread %s: %w", threadID, err)
	}

	// message_id kosong berarti post lama dibuat sebelum kolom ini ada (lihat
	// migrasi di store.go) — nama/tag/status-nya tetap diperbarui di atas,
	// tapi embed pesan lawasnya dibiarkan apa adanya karena ID pesannya tidak
	// diketahui.
	if existing.MessageID != "" {
		embeds := []*discordgo.MessageEmbed{embed}
		if _, err := n.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
			Channel: threadID,
			ID:      existing.MessageID,
			Content: &mentions,
			Embeds:  &embeds,
		}, discordgo.WithContext(ctx)); err != nil {
			return fmt.Errorf("update forum post message %s: %w", existing.MessageID, err)
		}
	}

	if closed {
		if _, err := n.session.ChannelEdit(threadID, &discordgo.ChannelEdit{
			Archived: &closed,
			Locked:   &closed,
		}, discordgo.WithContext(ctx)); err != nil {
			return fmt.Errorf("archive forum thread %s: %w", threadID, err)
		}
	}

	n.logger.Info("forum notification post updated",
		"issue_number", issue.Number,
		"repo", issue.Repository.FullName,
		"thread", threadID,
		"closed", closed,
		"tags", len(appliedTags),
	)
	return nil
}

func (n *Notifier) resolveForumTags(ctx context.Context, forumID string, issueLabels []model.Label, issueState string) []string {
	wanted := allowedIssueForumTagNames(issueLabels)
	if statusTag, ok := statusForumTagNames[strings.ToLower(strings.TrimSpace(issueState))]; ok {
		wanted = append(wanted, statusTag)
	}
	if len(wanted) == 0 {
		return nil
	}

	forum, err := n.session.Channel(forumID, discordgo.WithContext(ctx))
	if err != nil {
		n.logger.Warn("failed to read forum tags", "forum", forumID, "error", err)
		return nil
	}

	tagIDsByName := make(map[string]string, len(forum.AvailableTags))
	for _, tag := range forum.AvailableTags {
		tagIDsByName[normalizeForumTagName(tag.Name)] = tag.ID
	}

	applied := make([]string, 0, len(wanted))
	for _, tagName := range wanted {
		tagID, ok := tagIDsByName[tagName]
		if !ok {
			n.logger.Warn("forum tag not found; skipping tag",
				"forum", forumID,
				"tag", tagName,
			)
			continue
		}
		applied = append(applied, tagID)
	}
	return applied
}

func (n *Notifier) resolveForum(ctx context.Context, issue model.Issue) (string, bool) {
	repo := strings.ToLower(strings.TrimSpace(issue.Repository.FullName))
	if repo == "" {
		return "", false
	}
	forumID, ok, err := n.store.GetForumChannelByRepo(ctx, repo)
	if err != nil {
		n.logger.Warn("failed to read forum mapping", "repo", repo, "error", err)
		return "", false
	}
	return forumID, ok
}

// resolveChannels returns the deduplicated set of channels that should
// receive this issue's notification: the repo/label/default resolved
// channel plus every extra channel registered via /notify-channel.
func (n *Notifier) resolveChannels(ctx context.Context, issue model.Issue) []string {
	primary := n.resolveChannel(ctx, issue)
	extras, err := n.store.ListNotificationChannels(ctx)
	if err != nil {
		n.logger.Warn("failed to read extra notification channels", "error", err)
	}

	seen := map[string]struct{}{primary: {}}
	channels := []string{primary}
	for _, channelID := range extras {
		if channelID == "" {
			continue
		}
		if _, ok := seen[channelID]; ok {
			continue
		}
		seen[channelID] = struct{}{}
		channels = append(channels, channelID)
	}
	return channels
}

// resolveChannel determines the target channel based on the issue's repo and
// labels, falling back to the default channel — which may have been changed
// at runtime via /set-channel (stored in sqlite) instead of the static
// DISCORD_DEFAULT_CHANNEL env var.
func (n *Notifier) resolveChannel(ctx context.Context, issue model.Issue) string {
	labels := make([]string, len(issue.Labels))
	for i, lbl := range issue.Labels {
		labels[i] = lbl.Name
	}
	return n.cfg.ResolveChannel(issue.Repository.FullName, labels, n.defaultChannel(ctx))
}

// defaultChannel returns the /set-channel override if one has been set,
// otherwise the static DISCORD_DEFAULT_CHANNEL from config.
func (n *Notifier) defaultChannel(ctx context.Context) string {
	if override, ok, err := n.store.GetSetting(ctx, store.SettingKeyDefaultChannel); err != nil {
		n.logger.Warn("failed to read default channel override", "error", err)
	} else if ok {
		return override
	}
	return n.cfg.Discord.DefaultChannel
}

// buildMentions builds a string of Discord user mentions from assignees.
func (n *Notifier) buildMentions(ctx context.Context, assignees []model.User) string {
	var parts []string
	for _, a := range assignees {
		if discordID := n.resolveDiscordUser(ctx, a.Login); discordID != "" {
			parts = append(parts, fmt.Sprintf("<@%s>", discordID))
		}
	}
	return strings.Join(parts, " ")
}

func (n *Notifier) resolveDiscordUser(ctx context.Context, githubLogin string) string {
	login := strings.ToLower(strings.TrimSpace(githubLogin))
	if login == "" {
		return ""
	}
	if discordID, ok, err := n.store.GetDiscordUserByGitHubLogin(ctx, login); err != nil {
		n.logger.Warn("failed to read github user mapping", "github_login", login, "error", err)
	} else if ok {
		return discordID
	}
	return n.cfg.ResolveDiscordUser(githubLogin)
}

// buildEmbed constructs the Discord embed for an issue.
func (n *Notifier) buildEmbed(issue model.Issue) *discordgo.MessageEmbed {
	labelStr := buildLabelString(issue.Labels)
	assigneeStr := buildAssigneeString(issue.Assignees)
	status := issueStatus(issue.State)

	embed := &discordgo.MessageEmbed{
		Title: truncateDiscordText(fmt.Sprintf("%s [%s] #%d %s", status.Icon, issue.Repository.FullName, issue.Number, issue.Title), 256),
		URL:   issue.HTMLURL,
		Color: status.Color,
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
				Value:  status.Text,
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Dibuat: %s", issue.CreatedAt),
		},
	}

	if issue.Body != "" {
		embed.Description = truncateDiscordText(issue.Body, 4096)
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

// forumPostTitle builds the forum thread title, prefixed with the issue's
// status icon so open vs. closed issues can be told apart straight from the
// forum's thread list without opening each post.
func forumPostTitle(issue model.Issue) string {
	title := strings.TrimSpace(issue.Title)
	if title == "" {
		title = fmt.Sprintf("Issue #%d", issue.Number)
	}
	status := issueStatus(issue.State)
	prefixed := fmt.Sprintf("%s #%d %s", status.Icon, issue.Number, title)
	return truncateDiscordText(prefixed, 100)
}

func truncateDiscordText(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return strings.TrimSpace(string(runes[:max-3])) + "..."
}

type statusView struct {
	Icon  string
	Text  string
	Color int
}

func issueStatus(state string) statusView {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "closed":
		return statusView{Icon: "🔴", Text: "Closed", Color: 0xE74C3C}
	default:
		return statusView{Icon: "🟢", Text: "Open", Color: 0x2ECC71}
	}
}

func allowedIssueForumTagNames(labels []model.Label) []string {
	seen := make(map[string]struct{})
	for _, label := range labels {
		name := normalizeForumTagName(label.Name)
		if alias, ok := forumTagAliases[name]; ok {
			name = alias
		}
		if _, ok := allowedForumTagNames[name]; !ok {
			continue
		}
		seen[name] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func normalizeForumTagName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
}
