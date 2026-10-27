package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/statefile"
)

// Linear issues for the app's Issues screen. The person pastes a personal
// API key in Settings; it is kept in linear.json in the agent's folder,
// readable only by them, and never leaves this computer except to Linear.
// The app's webview may only talk to this agent (its content security
// policy), so every call to Linear is made here: a fixed set of reads and
// one write, a comment. The key is never sent back to the app, never logged
// and never given to a box.

// linearEndpoint is Linear's GraphQL API; tests point it at their own.
var linearEndpoint = "https://api.linear.app/graphql"

const linearFile = "linear.json"

type linearUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type linearWorkspace struct {
	Name   string `json:"name"`
	URLKey string `json:"url_key"`
}

// linearState is linear.json.
type linearState struct {
	APIKey    string           `json:"api_key"`
	Viewer    *linearUser      `json:"viewer,omitempty"`
	Workspace *linearWorkspace `json:"workspace,omitempty"`
}

// LinearStatus is what the app is told: who the key belongs to, not the key.
type LinearStatus struct {
	Connected bool             `json:"connected"`
	Viewer    *linearUser      `json:"viewer,omitempty"`
	Workspace *linearWorkspace `json:"workspace,omitempty"`
	// File is where the key is, or would be, kept on this computer.
	File string `json:"file"`
}

type linearTeam struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type linearLabel struct {
	Name string `json:"name"`
	// Color is hex with its #, as Linear gives it.
	Color string `json:"color"`
}

type linearWorkflowState struct {
	Name string `json:"name"`
	// Type is Linear's own: triage, backlog, unstarted, started, completed
	// or canceled.
	Type  string `json:"type"`
	Color string `json:"color"`
}

// LinearIssue is one row of the list.
type LinearIssue struct {
	ID            string              `json:"id"`
	Identifier    string              `json:"identifier"`
	Title         string              `json:"title"`
	URL           string              `json:"url"`
	Priority      int                 `json:"priority"`
	PriorityLabel string              `json:"priority_label,omitempty"`
	State         linearWorkflowState `json:"state"`
	Team          linearTeam          `json:"team"`
	Assignee      *linearUser         `json:"assignee,omitempty"`
	Creator       string              `json:"creator,omitempty"`
	Labels        []linearLabel       `json:"labels"`
	// Branch is the branch name Linear suggests; a pull request from it is
	// linked to the issue by Linear's own GitHub integration.
	Branch    string `json:"branch,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type linearComment struct {
	Author    string `json:"author,omitempty"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	URL       string `json:"url,omitempty"`
}

type linearLink struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	Source string `json:"source,omitempty"`
}

// LinearIssueDetail is an issue with its description and thread.
type LinearIssueDetail struct {
	LinearIssue
	Description string          `json:"description"`
	Comments    []linearComment `json:"comments"`
	Links       []linearLink    `json:"links"`
}

// errLinearOff: no key has been saved. errLinearAuth: Linear refused it.
var (
	errLinearOff  = errors.New("Linear isn't connected. Add an API key in Settings.")
	errLinearAuth = errors.New("Linear didn't accept the API key. Make a new one in Linear and paste it in Settings.")
)

func (a *Agent) linearPath() string { return filepath.Join(a.cfg.Dir, linearFile) }

func (a *Agent) linearLoad() (linearState, error) {
	var st linearState
	b, err := os.ReadFile(a.linearPath())
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	// A file nobody can read as JSON is the same as no key: the person
	// connects again and it is written over.
	_ = json.Unmarshal(b, &st)
	return st, nil
}

func (a *Agent) linearStatus(st linearState) LinearStatus {
	if st.APIKey == "" {
		return LinearStatus{File: a.linearPath()}
	}
	return LinearStatus{Connected: true, Viewer: st.Viewer, Workspace: st.Workspace, File: a.linearPath()}
}

// linearConnect checks the key with Linear and, only if it is accepted,
// keeps it.
func (a *Agent) linearConnect(ctx context.Context, key string) (LinearStatus, error) {
	var out struct {
		Viewer struct {
			ID, Name, DisplayName, Email string
		}
		Organization struct {
			Name, URLKey string
		}
	}
	if err := linearQuery(ctx, key, `query{viewer{id name displayName email} organization{name urlKey}}`, nil, &out); err != nil {
		return LinearStatus{}, err
	}
	st := linearState{
		APIKey:    key,
		Viewer:    &linearUser{ID: out.Viewer.ID, Name: firstNonEmpty(out.Viewer.Name, out.Viewer.DisplayName), Email: out.Viewer.Email},
		Workspace: &linearWorkspace{Name: out.Organization.Name, URLKey: out.Organization.URLKey},
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return LinearStatus{}, err
	}
	if err := statefile.Write(a.linearPath(), append(b, '\n')); err != nil {
		return LinearStatus{}, err
	}
	return a.linearStatus(st), nil
}

// linearQuery runs one GraphQL document as the key's owner and decodes its
// data into out. A personal API key goes in Authorization as it is, with no
// "Bearer".
func linearQuery(ctx context.Context, key, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, linearEndpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", key)
	req.Header.Set("User-Agent", "shipyard")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		// The error names the URL, never the key; say it in the app's words.
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("Linear didn't answer in 20s")
		}
		return errors.New("couldn't reach Linear: check this computer's connection")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return errors.New("Linear's answer was cut short")
	}
	var reply struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &reply)
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return errLinearAuth
	}
	for _, e := range reply.Errors {
		if strings.Contains(strings.ToLower(e.Extensions.Code+" "+e.Extensions.Type), "authentication") {
			return errLinearAuth
		}
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return errors.New("Linear is rate limiting this key; try again in a minute")
	}
	if len(reply.Errors) > 0 {
		return fmt.Errorf("Linear: %s", reply.Errors[0].Message)
	}
	if res.StatusCode != http.StatusOK || len(reply.Data) == 0 {
		return fmt.Errorf("Linear answered %s", res.Status)
	}
	return json.Unmarshal(reply.Data, out)
}

// The GraphQL shapes, and how they become what the app is told.

const linearIssueFields = `id identifier title url priority priorityLabel createdAt updatedAt branchName state{name type color} team{id key name} assignee{id name displayName} creator{name displayName} labels(first:12){nodes{name color}}`

type gqlLinearUser struct {
	ID, Name, DisplayName string
}

func (u *gqlLinearUser) name() string {
	if u == nil {
		return ""
	}
	return firstNonEmpty(u.Name, u.DisplayName)
}

type gqlLinearIssue struct {
	ID, Identifier, Title, URL string
	Priority                   float64
	PriorityLabel              string
	CreatedAt, UpdatedAt       string
	BranchName                 string
	State                      linearWorkflowState
	Team                       linearTeam
	Assignee                   *gqlLinearUser
	Creator                    *gqlLinearUser
	Labels                     struct{ Nodes []linearLabel }
}

func (g gqlLinearIssue) issue() LinearIssue {
	i := LinearIssue{
		ID: g.ID, Identifier: g.Identifier, Title: g.Title, URL: g.URL,
		Priority: int(g.Priority), PriorityLabel: g.PriorityLabel,
		State: g.State, Team: g.Team, Creator: g.Creator.name(),
		Labels: g.Labels.Nodes, Branch: g.BranchName,
		CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
	if g.Assignee != nil {
		i.Assignee = &linearUser{ID: g.Assignee.ID, Name: g.Assignee.name()}
	}
	if i.Labels == nil {
		i.Labels = []linearLabel{}
	}
	return i
}

// linearIssues lists up to 100 issues that are not done or cancelled, the
// most recently updated first: a team's, the key owner's own, or both.
func linearIssues(ctx context.Context, key, team string, mine bool) ([]LinearIssue, error) {
	filter := map[string]any{"state": map[string]any{"type": map[string]any{"nin": []string{"completed", "canceled"}}}}
	if team != "" {
		filter["team"] = map[string]any{"key": map[string]any{"eq": team}}
	}
	if mine {
		filter["assignee"] = map[string]any{"isMe": map[string]any{"eq": true}}
	}
	var out struct {
		Issues struct{ Nodes []gqlLinearIssue }
	}
	q := `query($filter:IssueFilter){issues(first:100,filter:$filter,orderBy:updatedAt){nodes{` + linearIssueFields + `}}}`
	if err := linearQuery(ctx, key, q, map[string]any{"filter": filter}, &out); err != nil {
		return nil, err
	}
	issues := make([]LinearIssue, 0, len(out.Issues.Nodes))
	for _, n := range out.Issues.Nodes {
		issues = append(issues, n.issue())
	}
	return issues, nil
}

func linearTeams(ctx context.Context, key string) ([]linearTeam, error) {
	var out struct {
		Teams struct{ Nodes []linearTeam }
	}
	if err := linearQuery(ctx, key, `query{teams(first:100){nodes{id key name}}}`, nil, &out); err != nil {
		return nil, err
	}
	teams := out.Teams.Nodes
	if teams == nil {
		teams = []linearTeam{}
	}
	sort.Slice(teams, func(i, j int) bool { return strings.ToLower(teams[i].Name) < strings.ToLower(teams[j].Name) })
	return teams, nil
}

// linearIssue reads one issue by its id or its identifier ("ENG-123"), with
// its description, its last 50 comments oldest first, and what is attached
// to it (the pull requests Linear linked, among others).
func linearIssue(ctx context.Context, key, id string) (LinearIssueDetail, error) {
	var out struct {
		Issue *struct {
			gqlLinearIssue
			Description string
			Comments    struct {
				Nodes []struct {
					Body, CreatedAt, URL string
					User                 *gqlLinearUser
				}
			}
			Attachments struct {
				Nodes []struct{ Title, URL, SourceType string }
			}
		}
	}
	q := `query($id:String!){issue(id:$id){` + linearIssueFields + ` description comments(last:50){nodes{body createdAt url user{name displayName}}} attachments(first:10){nodes{title url sourceType}}}}`
	if err := linearQuery(ctx, key, q, map[string]any{"id": id}, &out); err != nil {
		return LinearIssueDetail{}, err
	}
	if out.Issue == nil {
		return LinearIssueDetail{}, fmt.Errorf("Linear has no issue %s", id)
	}
	d := LinearIssueDetail{LinearIssue: out.Issue.issue(), Description: out.Issue.Description, Comments: []linearComment{}, Links: []linearLink{}}
	for _, c := range out.Issue.Comments.Nodes {
		d.Comments = append(d.Comments, linearComment{Author: c.User.name(), Body: c.Body, CreatedAt: c.CreatedAt, URL: c.URL})
	}
	sort.SliceStable(d.Comments, func(i, j int) bool { return d.Comments[i].CreatedAt < d.Comments[j].CreatedAt })
	for _, at := range out.Issue.Attachments.Nodes {
		d.Links = append(d.Links, linearLink{Title: at.Title, URL: at.URL, Source: at.SourceType})
	}
	return d, nil
}

// linearCommentCreate posts body on an issue as the key's owner. id is the
// issue's id, not its identifier.
func linearCommentCreate(ctx context.Context, key, id, body string) error {
	var out struct {
		CommentCreate struct{ Success bool }
	}
	q := `mutation($id:String!,$body:String!){commentCreate(input:{issueId:$id,body:$body}){success}}`
	if err := linearQuery(ctx, key, q, map[string]any{"id": id, "body": body}, &out); err != nil {
		return err
	}
	if !out.CommentCreate.Success {
		return errors.New("Linear didn't save the comment")
	}
	return nil
}

// linearRoutes serves Settings' Linear row and the Issues screen's Linear
// side.
func (a *Agent) linearRoutes(mux *http.ServeMux) {
	fail := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, errLinearOff):
			writeCoded(w, http.StatusConflict, err.Error(), "linear_off")
		case errors.Is(err, errLinearAuth):
			writeCoded(w, http.StatusUnauthorized, err.Error(), "linear_auth")
		default:
			writeError(w, http.StatusBadGateway, err.Error())
		}
	}
	// key hands a route the saved key, or answers that there is none.
	key := func(w http.ResponseWriter) (string, bool) {
		st, err := a.linearLoad()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return "", false
		}
		if st.APIKey == "" {
			fail(w, errLinearOff)
			return "", false
		}
		return st.APIKey, true
	}

	mux.HandleFunc("GET /v1/linear", func(w http.ResponseWriter, r *http.Request) {
		st, err := a.linearLoad()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a.linearStatus(st))
	})
	mux.HandleFunc("PUT /v1/linear", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			APIKey string `json:"api_key"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		k := strings.TrimSpace(req.APIKey)
		if k == "" || len(k) > 256 || strings.ContainsAny(k, " \t\r\n") {
			writeError(w, http.StatusBadRequest, "paste a Linear API key: it starts with lin_api_")
			return
		}
		st, err := a.linearConnect(r.Context(), k)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("DELETE /v1/linear", func(w http.ResponseWriter, r *http.Request) {
		if err := os.Remove(a.linearPath()); err != nil && !os.IsNotExist(err) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a.linearStatus(linearState{}))
	})
	mux.HandleFunc("GET /v1/linear/teams", func(w http.ResponseWriter, r *http.Request) {
		k, ok := key(w)
		if !ok {
			return
		}
		teams, err := linearTeams(r.Context(), k)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"teams": teams})
	})
	mux.HandleFunc("GET /v1/linear/issues", func(w http.ResponseWriter, r *http.Request) {
		k, ok := key(w)
		if !ok {
			return
		}
		q := r.URL.Query()
		issues, err := linearIssues(r.Context(), k, strings.TrimSpace(q.Get("team")), q.Get("mine") == "1")
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"issues": issues})
	})
	mux.HandleFunc("GET /v1/linear/issues/{id}", func(w http.ResponseWriter, r *http.Request) {
		k, ok := key(w)
		if !ok {
			return
		}
		d, err := linearIssue(r.Context(), k, r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	})
	mux.HandleFunc("POST /v1/linear/issues/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		k, ok := key(w)
		if !ok {
			return
		}
		var req struct {
			Body string `json:"body"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Body) == "" {
			writeError(w, http.StatusBadRequest, "write a comment first")
			return
		}
		if err := linearCommentCreate(r.Context(), k, r.PathValue("id"), req.Body); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}
