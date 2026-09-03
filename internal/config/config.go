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
	Database DatabaseConfig
	PlanAPI  PlanAPIConfig
	SMTP     SMTPConfig
	UserMap  map[string]string // GitHub login -> Discord user ID
	LabelMap map[string]string // GitHub label name -> Discord channel ID
	RepoMap  map[string]string // GitHub repo "owner/repo" -> Discord channel ID

	// AllowedUsers is the set of Discord user IDs allowed to run privileged commands.
	// When empty, ALL users are allowed (open mode).
	AllowedUsers map[string]struct{}
}

// DiscordConfig contains Discord-specific settings.
type DiscordConfig struct {
	Token          string
	DefaultChannel string
	// GuildIDs scopes slash commands to specific servers (comma-separated in
	// DISCORD_GUILD_ID, e.g. "111,222"). Commands are registered in every
	// listed guild individually, with near-instant propagation.
	// Leave empty for global commands instead (all servers, ~1h propagation).
	GuildIDs []string
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

// DatabaseConfig contains local sqlite settings.
type DatabaseConfig struct {
	Path string
}

// PlanAPIConfig contains settings for calling the plan API (used by /invite).
type PlanAPIConfig struct {
	BaseURL      string
	APIKey       string
	InviteWebURL string // base URL used to build the invite link shown to users

	// InviteEmailDomain is appended to a Discord username to derive the email
	// address used for "/invite user" (the plan API only accepts emails).
	InviteEmailDomain string
}

// SMTPConfig contains credentials for sending invite emails via "/invite email".
// All values come from environment variables — never hardcode credentials in code.
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string

	// InviteEmailDomain is appended to a Discord username to derive the email
	// address used for "/invite user" (the plan API only accepts emails).
	InviteEmailDomain string
}


// Load reads all configuration from environment variables.
// It returns an error if any required variable is missing.
func Load() (*Config, error) {
	cfg := &Config{}

	cfg.Discord.Token = os.Getenv("DISCORD_TOKEN")
	cfg.Discord.DefaultChannel = os.Getenv("DISCORD_DEFAULT_CHANNEL")
	cfg.Discord.GuildIDs = parseIDList(os.Getenv("DISCORD_GUILD_ID"))

	cfg.Server.Port = getEnvOrDefault("SERVER_PORT", "8080")
	cfg.Server.WebhookSecret = os.Getenv("WEBHOOK_SECRET")

	cfg.Watcher.Dir = getEnvOrDefault("WATCHER_DIR", "./watch")

	cfg.Database.Path = getEnvOrDefault("DB_PATH", "./data/bot.db")

	cfg.PlanAPI.BaseURL = getEnvOrDefault("PLAN_API_BASE_URL", "https://api-plan.kancadigital.com")
	cfg.PlanAPI.APIKey = os.Getenv("PLAN_API_KEY")
	cfg.PlanAPI.InviteWebURL = getEnvOrDefault("PLAN_INVITE_WEB_URL", "https://plan.kancadigital.com/invite")
	cfg.PlanAPI.InviteEmailDomain = getEnvOrDefault("PLAN_INVITE_EMAIL_DOMAIN", "@kancadigital.com")

	cfg.SMTP.Host = os.Getenv("SMTP_HOST")
	cfg.SMTP.Port = getEnvOrDefault("SMTP_PORT", "587")
	cfg.SMTP.Username = os.Getenv("SMTP_USERNAME")
	cfg.SMTP.Password = os.Getenv("SMTP_PASSWORD")
	cfg.SMTP.From = getEnvOrDefault("SMTP_FROM", cfg.SMTP.Username)
	cfg.PlanAPI.InviteEmailDomain = getEnvOrDefault("PLAN_INVITE_EMAIL_DOMAIN", "@kancadigital.com")

	cfg.SMTP.Host = os.Getenv("SMTP_HOST")
	cfg.SMTP.Port = getEnvOrDefault("SMTP_PORT", "587")
	cfg.SMTP.Username = os.Getenv("SMTP_USERNAME")
	cfg.SMTP.Password = os.Getenv("SMTP_PASSWORD")
	cfg.SMTP.From = getEnvOrDefault("SMTP_FROM", cfg.SMTP.Username)

	cfg.UserMap = parseKVMap(os.Getenv("GITHUB_DISCORD_USER_MAP"))
	cfg.LabelMap = parseKVMap(os.Getenv("GITHUB_LABEL_CHANNEL_MAP"))
	cfg.RepoMap = parseKVMap(os.Getenv("GITHUB_REPO_CHANNEL_MAP"))
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

// ResolveChannel picks the Discord channel for an incoming GitHub issue.
// Priority: repo-specific mapping (GITHUB_REPO_CHANNEL_MAP) > label mapping
// (GITHUB_LABEL_CHANNEL_MAP) > defaultChannel, which callers pass in so a
// runtime override (e.g. via /set-channel) can take precedence over the
// static DISCORD_DEFAULT_CHANNEL env var.
func (c *Config) ResolveChannel(repoFullName string, labels []string, defaultChannel string) string {
	if ch, ok := c.RepoMap[repoFullName]; ok {
		return ch
	}
	for _, lbl := range labels {
		if ch, ok := c.LabelMap[lbl]; ok {
			return ch
		}
	}
	return defaultChannel
}

// ResolveDiscordUser returns the Discord user ID for a GitHub login.
// Returns empty string if not found.
func (c *Config) ResolveDiscordUser(githubLogin string) string {
	if discordID := c.UserMap[githubLogin]; discordID != "" {
		return discordID
	}
	return c.UserMap[strings.ToLower(strings.TrimSpace(githubLogin))]
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

// parseIDList parses a comma-separated list of IDs into a slice, trimming
// whitespace and dropping empty entries.
// Example: "123, 456,789" → []string{"123", "456", "789"}
func parseIDList(raw string) []string {
	if raw == "" {
		return nil
	}
	var ids []string
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
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
