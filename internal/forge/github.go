package forge

import "encoding/json"

// GitHub is reached through the GitHub App, whose webhook handler and
// installation lifecycle live in ghapp and httpapi; here it only names its
// payloads' subjects.
var GitHub Provider = github{}

type github struct{}

func (github) ID() string         { return "github" }
func (github) Name() string       { return "GitHub" }
func (github) KindPrefix() string { return "" }
func (github) Auth() AuthMode     { return AuthApp }

func (github) Subjects(_ string, raw json.RawMessage) Subjects {
	return githubSubjects(raw)
}

// githubSubjects reads the GitHub payload shape, which Gitea and its forks
// copy closely enough to share it.
func githubSubjects(raw json.RawMessage) Subjects {
	var p struct {
		Ref    string `json:"ref"`
		Action string `json:"action"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
		Pusher struct {
			Name  string `json:"name"`
			Login string `json:"login"`
		} `json:"pusher"`
		Label struct {
			Name string `json:"name"`
		} `json:"label"`
		PullRequest struct {
			Head struct {
				Ref string `json:"ref"`
			} `json:"head"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"pull_request"`
		Issue struct {
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"issue"`
	}
	_ = json.Unmarshal(raw, &p)

	s := Subjects{Author: p.Sender.Login, Action: p.Action}
	if s.Author == "" {
		s.Author = p.Pusher.Name
	}
	if s.Author == "" {
		s.Author = p.Pusher.Login
	}

	switch {
	case p.Ref != "":
		s.Branch = trimRef(p.Ref)
	case p.PullRequest.Head.Ref != "":
		s.Branch = p.PullRequest.Head.Ref
	}

	// The top-level label field belongs to the "labeled"/"unlabeled" events;
	// issues and pull requests carry their labels in a list.
	if p.Label.Name != "" {
		s.Labels = append(s.Labels, p.Label.Name)
	}
	for _, l := range p.PullRequest.Labels {
		s.Labels = append(s.Labels, l.Name)
	}
	for _, l := range p.Issue.Labels {
		s.Labels = append(s.Labels, l.Name)
	}
	return s
}
