package model

// ── Internal domain model ─────────────────────────────────────────────────────

// Issue is the canonical internal representation of a GitHub issue used
// throughout the application (queue, notifier, etc.).
type Issue struct {
	ID        int     `json:"id"`
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	URL       string  `json:"url"`
	HTMLURL   string  `json:"html_url"`
	State     string  `json:"state"`
	Labels    []Label `json:"labels"`
	Assignees []User  `json:"assignees"`

	// Repository is populated from the top-level "repository" field in
	// GitHub webhook payloads (not nested inside the issue object).
	Repository Repo   `json:"repository"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Label represents a GitHub issue label.
type Label struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// User represents a GitHub user (assignee, sender, owner, etc.).
type User struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

// Repo represents a GitHub repository.
type Repo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

// ── GitHub Webhook event payloads ─────────────────────────────────────────────
// These structs map directly to the raw JSON GitHub sends.
// After parsing, they are converted to the internal Issue model.

// issueActionsToNotify adalah daftar action yang memicu notifikasi Discord.
// Action lain (labeled, unlabeled, assigned, milestoned, dll.) diabaikan
// untuk menghindari spam notifikasi dari 1 issue yang sama.
var issueActionsToNotify = map[string]struct{}{
	"opened":   {}, // issue baru dibuat
	"closed":   {}, // issue ditutup
	"reopened": {}, // issue dibuka kembali
	"edited":   {}, // issue diedit
}

// GitHubIssueEvent is the payload for the "issues" webhook event.
// GitHub wraps the issue object inside an "issue" field together with an "action".
//
// Docs: https://docs.github.com/en/webhooks/webhook-events-and-payloads#issues
type GitHubIssueEvent struct {
	// Action is what happened: "opened", "edited", "closed", "assigned", etc.
	Action     string     `json:"action"`
	Issue      GitHubIssue `json:"issue"`
	Repository GitHubRepo  `json:"repository"`
	Sender     User        `json:"sender"`
}

// ShouldNotify melaporkan apakah event ini perlu dikirim ke Discord.
// Hanya action dalam issueActionsToNotify yang diproses.
func (e GitHubIssueEvent) ShouldNotify() bool {
	_, ok := issueActionsToNotify[e.Action]
	return ok
}

// GitHubIssue is the issue object inside GitHubIssueEvent.
type GitHubIssue struct {
	ID        int     `json:"id"`
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	HTMLURL   string  `json:"html_url"`
	State     string  `json:"state"`
	Labels    []Label `json:"labels"`
	Assignees []User  `json:"assignees"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// GitHubRepo is the repository object inside webhook payloads.
type GitHubRepo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

// ToIssue converts a GitHubIssueEvent to the internal Issue model.
// The repository info comes from the top-level event, not from inside the issue.
func (e GitHubIssueEvent) ToIssue() Issue {
	return Issue{
		ID:      e.Issue.ID,
		Number:  e.Issue.Number,
		Title:   e.Issue.Title,
		Body:    e.Issue.Body,
		HTMLURL: e.Issue.HTMLURL,
		State:   e.Issue.State,
		Labels:  e.Issue.Labels,
		Assignees: e.Issue.Assignees,
		Repository: Repo{
			FullName: e.Repository.FullName,
			HTMLURL:  e.Repository.HTMLURL,
		},
		CreatedAt: e.Issue.CreatedAt,
		UpdatedAt: e.Issue.UpdatedAt,
	}
}

// ── Legacy n8n payload ────────────────────────────────────────────────────────

// N8NPayload is the wrapper payload from n8n containing one or more issues.
// Kept for backward compatibility when using n8n as a middleware.
type N8NPayload struct {
	Issues []Issue `json:"issues"`
}
