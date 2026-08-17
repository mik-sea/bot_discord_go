package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	Discord  DiscordConfig
	Server   ServerConfig
	Watcher  WatcherConfig
	UserMap  map[string]string // GitHub login -> Discord user ID
	LabelMap map[string]string // GitHub label name -> Discord channel ID

	// AllowedUsers is the set of Discord user IDs allowed to run privileged commands.
	// When empty, ALL users are allowed (open mode).
	AllowedUsers map[string]struct{}
}

// DiscordConfig contains Discord-specific settings.
type DiscordConfig struct {
	Token          string
	DefaultChannel string
	// GuildID scopes slash commands to a specific server.
	// Leave empty for global commands (all servers, ~1h propagation).
	// Set to your server ID during development for instant propagation.
	GuildID string
}

// ServerConfig contains HTTP server settings.
type ServerConfig struct {
	Port          string
	WebhookSecret string
}

// WatcherConfig contains file-watcher settings.
type WatcherConfig struct {
	Dir string
}

// Load reads all configuration from environment variables.
// It returns an error if any required variable is missing.
func Load() (*Config, error) {
	cfg := &Config{}

	cfg.Discord.Token = os.Getenv("DISCORD_TOKEN")
	cfg.Discord.DefaultChannel = os.Getenv("DISCORD_DEFAULT_CHANNEL")
	cfg.Discord.GuildID = os.Getenv("DISCORD_GUILD_ID")

	cfg.Server.Port = getEnvOrDefault("SERVER_PORT", "8080")
	cfg.Server.WebhookSecret = os.Getenv("WEBHOOK_SECRET")

	cfg.Watcher.Dir = getEnvOrDefault("WATCHER_DIR", "./watch")

	cfg.UserMap = parseKVMap(os.Getenv("GITHUB_DISCORD_USER_MAP"))
	cfg.LabelMap = parseKVMap(os.Getenv("GITHUB_LABEL_CHANNEL_MAP"))
	cfg.AllowedUsers = parseIDSet(os.Getenv("DISCORD_ALLOWED_USERS"))
	return cfg, cfg.validate()
}

func (c *Config) validate() error {
	if c.Discord.Token == "" {
		return fmt.Errorf("DISCORD_TOKEN is required")
	}
	if c.Discord.DefaultChannel == "" {
		return fmt.Errorf("DISCORD_DEFAULT_CHANNEL is required")
	}
	return nil
}

// ResolveChannel returns the Discord channel ID for a given set of label names.
// It uses the first label that has a mapping; falls back to the default channel.
func (c *Config) ResolveChannel(labels []string) string {
	for _, lbl := range labels {
		if ch, ok := c.LabelMap[lbl]; ok {
			return ch
		}
	}
	return c.Discord.DefaultChannel
}

// ResolveDiscordUser returns the Discord user ID for a GitHub login.
// Returns empty string if not found.
func (c *Config) ResolveDiscordUser(githubLogin string) string {
	return c.UserMap[githubLogin]
}

// parseKVMap parses a comma-separated key=value string into a map.
// Example: "alice=123,bob=456" → map[alice:123 bob:456]
func parseKVMap(raw string) map[string]string {
	result := make(map[string]string)
	if raw == "" {
		return result
	}
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(kv) == 2 {
			result[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return result
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// IsUserAllowed reports whether the given Discord user ID is permitted to run
// privileged commands. When AllowedUsers is empty, ALL users are allowed.
func (c *Config) IsUserAllowed(discordUserID string) bool {
	if len(c.AllowedUsers) == 0 {
		return true
	}
	_, ok := c.AllowedUsers[discordUserID]
	return ok
}

// parseIDSet parses a comma-separated list of IDs into a set (map[string]struct{}).
// Example: "123,456,789" → set{123, 456, 789}
func parseIDSet(raw string) map[string]struct{} {
	result := make(map[string]struct{})
	if raw == "" {
		return result
	}
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			result[id] = struct{}{}
		}
	}
	return result
}
