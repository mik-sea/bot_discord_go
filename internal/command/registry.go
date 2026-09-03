package command

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/mailer"
	"github.com/miksea/bot_discord_go/internal/mailer"
	"github.com/miksea/bot_discord_go/internal/planapi"
	"github.com/miksea/bot_discord_go/internal/store"
)

// registeredCommand tracks one command registered in one guild (or globally,
// when guildID is empty), so Deregister can delete it from the right scope.
type registeredCommand struct {
	guildID string
	id      string
}

// Registry manages slash command registration and routing.
// It keeps track of registered command IDs so they can be cleanly deleted on shutdown.
type Registry struct {
	session    *discordgo.Session
	cfg        *config.Config
	logger     *slog.Logger
	dataStore  *store.Store
	planClient *planapi.Client
	mailClient *mailer.Mailer
	registered []registeredCommand
}

// NewRegistry creates a new Registry.
func NewRegistry(session *discordgo.Session, cfg *config.Config, logger *slog.Logger, dataStore *store.Store, planClient *planapi.Client, mailClient *mailer.Mailer) *Registry {
	return &Registry{
		session:    session,
		cfg:        cfg,
		logger:     logger,
		dataStore:  dataStore,
		planClient: planClient,
		mailClient: mailClient,
	}
}

// Register registers all slash commands with Discord and attaches their handlers.
// It must be called after the Discord session is opened.
//
// guildIDs scopes registration to specific servers for near-instant
// propagation; an empty slice registers commands globally instead (~1h
// propagation). When multiple guild IDs are given, commands are registered
// in every one of them.
func (r *Registry) Register(guildIDs []string) error {
	ping := NewPingHandler(r.cfg, r.logger)
	invite := NewInviteHandler(r.cfg, r.logger, r.dataStore, r.planClient, r.mailClient)
	setChannel := NewSetChannelHandler(r.cfg, r.logger, r.dataStore)
	notifyChannel := NewNotifyChannelHandler(r.cfg, r.logger, r.dataStore)
	forumSync := NewForumSyncHandler(r.cfg, r.logger, r.dataStore)
	githubUser := NewGitHubUserHandler(r.cfg, r.logger, r.dataStore)

	commands := []struct {
		def     *discordgo.ApplicationCommand
		handler func(*discordgo.Session, *discordgo.InteractionCreate)
	}{
		{Definition(), ping.Handle},
		{InviteDefinition(), invite.Handle},
		{SetChannelDefinition(), setChannel.Handle},
		{NotifyChannelDefinition(), notifyChannel.Handle},
		{ForumSyncDefinition(), forumSync.Handle},
		{GitHubUserDefinition(), githubUser.Handle},
	}

	// Slice kosong berarti registrasi global — direpresentasikan sebagai satu
	// "guild" dengan ID kosong, sesuai konvensi discordgo.
	targets := guildIDs
	if len(targets) == 0 {
		targets = []string{""}
	}

	// Kegagalan di satu guild (mis. bot belum di-invite ke sana, "Missing
	// Access") tidak boleh menggagalkan seluruh startup — cukup di-skip
	// dan lanjut ke guild lain, supaya guild yang valid tetap jalan.
	for _, guildID := range targets {
		for _, cmd := range commands {
			registered, err := r.session.ApplicationCommandCreate(r.session.State.User.ID, guildID, cmd.def)
			if err != nil {
				r.logger.Error("failed to register command, skipping this guild",
					"name", cmd.def.Name,
					"guild", guildID,
					"error", err,
				)
				break
			}
			r.registered = append(r.registered, registeredCommand{guildID: guildID, id: registered.ID})
			r.logger.Info("slash command registered",
				"name", cmd.def.Name,
				"id", registered.ID,
				"guild", guildID,
			)
		}
	}

	// A single session-level handler routes all interactions to the correct command.
	r.session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type != discordgo.InteractionApplicationCommand {
			return
		}
		data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
		if !ok {
			return
		}
		for _, cmd := range commands {
			if cmd.def.Name == data.Name {
				cmd.handler(s, i)
				return
			}
		}
	})

	return nil
}

// Deregister deletes all registered slash commands from Discord.
// Should be called during graceful shutdown so stale commands don't accumulate.
func (r *Registry) Deregister() {
	appID := r.session.State.User.ID
	for _, cmd := range r.registered {
		if err := r.session.ApplicationCommandDelete(appID, cmd.guildID, cmd.id); err != nil {
			r.logger.Warn("failed to delete slash command", "id", cmd.id, "guild", cmd.guildID, "error", err)
		} else {
			r.logger.Info("slash command deregistered", "id", cmd.id, "guild", cmd.guildID)
		}
	}
}
