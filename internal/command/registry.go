package command

import (
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/planapi"
	"github.com/miksea/bot_discord_go/internal/store"
)

// Registry manages slash command registration and routing.
// It keeps track of registered command IDs so they can be cleanly deleted on shutdown.
type Registry struct {
	session       *discordgo.Session
	cfg           *config.Config
	logger        *slog.Logger
	inviteStore   *store.InviteStore
	planClient    *planapi.Client
	registeredIDs []string // Discord-assigned command IDs (for cleanup)
}

// NewRegistry creates a new Registry.
func NewRegistry(session *discordgo.Session, cfg *config.Config, logger *slog.Logger, inviteStore *store.InviteStore, planClient *planapi.Client) *Registry {
	return &Registry{
		session:     session,
		cfg:         cfg,
		logger:      logger,
		inviteStore: inviteStore,
		planClient:  planClient,
	}
}

// Register registers all slash commands with Discord and attaches their handlers.
// It must be called after the Discord session is opened.
func (r *Registry) Register(guildID string) error {
	ping := NewPingHandler(r.cfg, r.logger)
	invite := NewInviteHandler(r.cfg, r.logger, r.inviteStore, r.planClient)

	commands := []struct {
		def     *discordgo.ApplicationCommand
		handler func(*discordgo.Session, *discordgo.InteractionCreate)
	}{
		{Definition(), ping.Handle},
		{InviteDefinition(), invite.Handle},
	}

	for _, cmd := range commands {
		registered, err := r.session.ApplicationCommandCreate(r.session.State.User.ID, guildID, cmd.def)
		if err != nil {
			return fmt.Errorf("register command %q: %w", cmd.def.Name, err)
		}
		r.registeredIDs = append(r.registeredIDs, registered.ID)
		r.logger.Info("slash command registered",
			"name", cmd.def.Name,
			"id", registered.ID,
			"guild", guildID,
		)
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
func (r *Registry) Deregister(guildID string) {
	appID := r.session.State.User.ID
	for _, id := range r.registeredIDs {
		if err := r.session.ApplicationCommandDelete(appID, guildID, id); err != nil {
			r.logger.Warn("failed to delete slash command", "id", id, "error", err)
		} else {
			r.logger.Info("slash command deregistered", "id", id)
		}
	}
}
