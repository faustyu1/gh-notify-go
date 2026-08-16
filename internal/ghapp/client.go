package ghapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

type Repository struct {
	GitHubID    int64
	FullName    string
	Private     bool
	Description string
}

// Account is the GitHub user or organization an installation belongs to.
type Account struct {
	Login string
	Type  string
}

type DiffStat struct {
	Additions    int
	Deletions    int
	ChangedFiles int
}

type Client struct {
	BaseURL string

	tokens *TokenSource
	http   *http.Client
}

func NewClient(tokens *TokenSource, httpClient *http.Client) *Client {
	return &Client{BaseURL: "https://api.github.com", tokens: tokens, http: httpClient}
}

var nextLinkRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// StatusError carries a non-200 response code so callers can branch on it
// without string matching.
type StatusError struct {
	Code int
}

func (e StatusError) Error() string { return fmt.Sprintf("github status %d", e.Code) }

// ListRepositories returns every repository the installation can see,
// following Link-header pagination.
func (c *Client) ListRepositories(ctx context.Context, installationID int64) ([]Repository, error) {
	url := c.BaseURL + "/installation/repositories?per_page=100"
	var out []Repository

	for url != "" {
		var page struct {
			Repositories []struct {
				ID          int64  `json:"id"`
				FullName    string `json:"full_name"`
				Private     bool   `json:"private"`
				Description string `json:"description"`
			} `json:"repositories"`
		}

		next, err := c.get(ctx, installationID, url, &page)
		if err != nil {
			return nil, err
		}
		for _, r := range page.Repositories {
			out = append(out, Repository{
				GitHubID:    r.ID,
				FullName:    r.FullName,
				Private:     r.Private,
				Description: r.Description,
			})
		}
		url = next
	}
	return out, nil
}

// InstallationInfo fetches the account behind an installation. Authenticated
// with the App JWT, because at claim time no installation token can be minted
// for a row that does not exist yet.
func (c *Client) InstallationInfo(
	ctx context.Context, installationID int64,
) (Account, error) {
	url := fmt.Sprintf("%s/app/installations/%d", c.BaseURL, installationID)

	appJWT, err := c.tokens.AppJWT()
	if err != nil {
		return Account{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Account{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Account{}, fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return Account{}, StatusError{Code: resp.StatusCode}
	}

	var payload struct {
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Account{}, fmt.Errorf("decode github response: %w", err)
	}
	return Account{Login: payload.Account.Login, Type: payload.Account.Type}, nil
}

// CompareStats sums the per-file counts of a commit range. A missing range
// (force push, deleted commit) yields a zero DiffStat rather than an error,
// because the notification is still worth sending without the numbers.
func (c *Client) CompareStats(
	ctx context.Context, installationID int64, repoFullName, base, head string,
) (DiffStat, error) {
	url := fmt.Sprintf("%s/repos/%s/compare/%s...%s", c.BaseURL, repoFullName, base, head)

	var payload struct {
		Files []struct {
			Additions int `json:"additions"`
			Deletions int `json:"deletions"`
		} `json:"files"`
	}
	if _, err := c.get(ctx, installationID, url, &payload); err != nil {
		var se StatusError
		if errors.As(err, &se) && se.Code == http.StatusNotFound {
			return DiffStat{}, nil
		}
		return DiffStat{}, err
	}

	stat := DiffStat{ChangedFiles: len(payload.Files)}
	for _, f := range payload.Files {
		stat.Additions += f.Additions
		stat.Deletions += f.Deletions
	}
	return stat, nil
}

// get performs one authenticated request and returns the "next" page URL if
// the response carries one.
func (c *Client) get(ctx context.Context, installationID int64, url string, out any) (string, error) {
	token, err := c.tokens.InstallationToken(ctx, installationID)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return "", StatusError{Code: resp.StatusCode}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return "", fmt.Errorf("decode github response: %w", err)
	}

	if m := nextLinkRe.FindStringSubmatch(resp.Header.Get("Link")); len(m) == 2 {
		return m[1], nil
	}
	return "", nil
}
