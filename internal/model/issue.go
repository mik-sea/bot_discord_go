package model

// Issue represents a GitHub issue payload received from n8n.
type Issue struct {
	ID         int      `json:"id"`
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	URL        string   `json:"url"`
	HTMLURL    string   `json:"html_url"`
	State      string   `json:"state"`
	Labels     []Label  `json:"labels"`
	Assignees  []User   `json:"assignees"`
	Repository Repo     `json:"repository"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

// Label represents a GitHub issue label.
type Label struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// User represents a GitHub user.
type User struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

// Repo represents a GitHub repository.
type Repo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

// N8NPayload is the wrapper payload from n8n containing one or more issues.
type N8NPayload struct {
	Issues []Issue `json:"issues"`
}
