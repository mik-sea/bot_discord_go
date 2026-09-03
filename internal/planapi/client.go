// Package planapi calls the kancadigital plan API to create project invites.
package planapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	requestTimeout = 10 * time.Second
	// memberRole is the only access level the bot grants via /invite.
	memberRole = "member"
)

// Client calls the plan API's invite endpoint.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a plan API client. baseURL may include or omit a trailing slash.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: requestTimeout},
	}
}

// Invite is the plan API's response after creating a project invite.
type Invite struct {
	ID     int    `json:"id"`
	Token  string `json:"token"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

type createInviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// CreateInvite calls POST /projects/{projectKey}/invites with the given email
// and the fixed "member" role, returning the created invite.
func (c *Client) CreateInvite(ctx context.Context, projectKey, email string) (*Invite, error) {
	body, err := json.Marshal(createInviteRequest{Email: email, Role: memberRole})
	if err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}

	url := fmt.Sprintf("%s/projects/%s/invites", c.baseURL, projectKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call plan api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plan api returned status %d for project %q", resp.StatusCode, projectKey)
	}

	var invite Invite
	if err := json.NewDecoder(resp.Body).Decode(&invite); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &invite, nil
}
