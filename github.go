package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── errors ────────────────────────────────────────────────────────────────────

// errSignedOut: gh has no working token for a host. signedOutError says which.
var errSignedOut = errors.New("not signed in to GitHub")

type signedOutError struct{ Host string }

func (e *signedOutError) Error() string {
	return "not signed in to " + e.Host + ": run `gh auth login --hostname " + e.Host + "`"
}
func (e *signedOutError) Unwrap() error { return errSignedOut }

// partialError: GitHub answered, but with errors for part of the answer (a
// repo in an org whose SAML sign-on the token lacks, say). What came back is
// used; the error says what's missing.
type partialError struct {
	msg string
	// notFound: all that's missing is things that aren't there (or that you
	// can't see), as opposed to GitHub not managing to answer.
	notFound bool
}

func (e *partialError) Error() string { return e.msg }

// errTruncated: a list stopped at the cap rather than page on without end.
var errTruncated = errors.New("showing the first 200: filter, or open GitHub for the rest")

// ── data ──────────────────────────────────────────────────────────────────────

type actor struct {
	Login string `json:"login"`
}

type label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type rollup struct {
	Nodes []struct {
		Commit struct {
			StatusCheckRollup *struct {
				State string `json:"state"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	} `json:"nodes"`
}

// pullRequest is a row in the lists: enough to draw it and to find its
// worktree. Host is filled in by the client, not the API.
type pullRequest struct {
	Host       string    `json:"host,omitempty"` // ours, not the API's: kept in the list cache
	ID         string    `json:"id"`
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	IsDraft    bool      `json:"isDraft"`
	State      string    `json:"state"` // OPEN, MERGED, CLOSED
	UpdatedAt  time.Time `json:"updatedAt"`
	Author     *actor    `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	HeadRefName         string `json:"headRefName"`
	BaseRefName         string `json:"baseRefName"`
	HeadRepositoryOwner *actor `json:"headRepositoryOwner"`
	HeadRepository      *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	ReviewDecision    string `json:"reviewDecision"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	Mergeable         string `json:"mergeable"`      // MERGEABLE, CONFLICTING, UNKNOWN
	Additions         int    `json:"additions"`
	Deletions         int    `json:"deletions"`
	Commits           rollup `json:"commits"`
	Labels            struct {
		Nodes []label `json:"nodes"`
	} `json:"labels"`
	AutoMergeRequest *struct {
		EnabledAt   string `json:"enabledAt"`
		MergeMethod string `json:"mergeMethod"`
	} `json:"autoMergeRequest"`
	// Its place in its base branch's merge queue, while it's in one.
	MergeQueueEntry *queueEntry `json:"mergeQueueEntry"`
}

// queueEntry is a PR's place in a merge queue.
type queueEntry struct {
	Position int    `json:"position"`
	State    string `json:"state"` // QUEUED, AWAITING_CHECKS, MERGEABLE, UNMERGEABLE, LOCKED
	// GitHub's guess at how long until it merges, in seconds; the PR screen's.
	EstimatedTimeToMerge *int `json:"estimatedTimeToMerge"`
}

// checks is the head commit's combined check state: SUCCESS, FAILURE, ERROR,
// PENDING, EXPECTED, or "" when the commit has no checks.
func (pr pullRequest) checks() string {
	if n := pr.Commits.Nodes; len(n) > 0 && n[0].Commit.StatusCheckRollup != nil {
		return n[0].Commit.StatusCheckRollup.State
	}
	return ""
}

func (pr pullRequest) repo() repoRef {
	owner, name, _ := strings.Cut(pr.Repository.NameWithOwner, "/")
	return repoRef{Host: pr.Host, Owner: owner, Name: name}
}

func (pr pullRequest) key() string {
	return fmt.Sprintf("%s/%s#%d", pr.Host, pr.Repository.NameWithOwner, pr.Number)
}

func (pr pullRequest) author() string {
	if pr.Author == nil {
		return "ghost"
	}
	return pr.Author.Login
}

// headOwner is the login of the repo the PR's branch lives in.
func (pr pullRequest) headOwner() string {
	if pr.HeadRepositoryOwner != nil {
		return pr.HeadRepositoryOwner.Login
	}
	return pr.repo().Owner
}

// matches says whether every word of q is in the PR's number, title, repo,
// author, branch or labels, or in extra (its agent's name).
func (pr pullRequest) matches(q string, extra ...string) bool {
	hay := strings.ToLower(fmt.Sprintf("#%d %s %s %s %s %s", pr.Number, pr.Title, pr.Repository.NameWithOwner, pr.author(), pr.HeadRefName, strings.Join(extra, " ")))
	for _, l := range pr.Labels.Nodes {
		hay += " " + strings.ToLower(l.Name)
	}
	for _, w := range strings.Fields(strings.ToLower(q)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

type check struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`       // CheckRun
	Status     string `json:"status"`     // CheckRun: QUEUED, IN_PROGRESS, COMPLETED, …
	Conclusion string `json:"conclusion"` // CheckRun: SUCCESS, FAILURE, NEUTRAL, SKIPPED, …
	Context    string `json:"context"`    // StatusContext
	State      string `json:"state"`      // StatusContext: SUCCESS, FAILURE, ERROR, PENDING, EXPECTED
	IsRequired bool   `json:"isRequired"`
	DetailsURL string `json:"detailsUrl"`
	TargetURL  string `json:"targetUrl"`
}

func (c check) name() string {
	if c.Typename == "StatusContext" {
		return c.Context
	}
	return c.Name
}

// outcome folds a check run or status into pass, fail, pending or skip.
func (c check) outcome() string {
	if c.Typename == "StatusContext" {
		switch c.State {
		case "SUCCESS":
			return "pass"
		case "FAILURE", "ERROR":
			return "fail"
		}
		return "pending"
	}
	if c.Status != "COMPLETED" {
		return "pending"
	}
	switch c.Conclusion {
	case "SUCCESS":
		return "pass"
	case "NEUTRAL", "SKIPPED", "STALE":
		return "skip"
	}
	return "fail"
}

type comment struct {
	Author    *actor    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	State     string    `json:"state"` // reviews only
}

type thread struct {
	IsResolved bool   `json:"isResolved"`
	IsOutdated bool   `json:"isOutdated"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Comments   struct {
		Nodes []comment `json:"nodes"`
	} `json:"comments"`
}

// prDetail is the PR screen: the row, plus what it takes to judge and merge.
type prDetail struct {
	pullRequest
	Body                string `json:"body"`
	MergeStateStatus    string `json:"mergeStateStatus"` // CLEAN, BLOCKED, BEHIND, DIRTY, UNSTABLE, HAS_HOOKS, DRAFT, UNKNOWN
	ChangedFiles        int    `json:"changedFiles"`
	HeadRefOid          string `json:"headRefOid"`
	MaintainerCanModify bool   `json:"maintainerCanModify"`
	// Whether its base branch merges through a merge queue.
	IsMergeQueueEnabled bool `json:"isMergeQueueEnabled"`
	HeadRef             *struct {
		ID string `json:"id"`
	} `json:"headRef"`
	Checks         []check      `json:"-"`
	Reviews        []comment    `json:"-"`
	Comments       []comment    `json:"-"`
	Threads        []thread     `json:"-"`
	Reviewers      []reviewer   `json:"-"`
	RepoSettings   repoSettings `json:"-"`
	ViewerCanMerge bool         `json:"-"`
}

type reviewer struct {
	Name  string
	State string // APPROVED, CHANGES_REQUESTED, COMMENTED, PENDING (requested)
}

type repoSettings struct {
	SquashMergeAllowed       bool   `json:"squashMergeAllowed"`
	MergeCommitAllowed       bool   `json:"mergeCommitAllowed"`
	RebaseMergeAllowed       bool   `json:"rebaseMergeAllowed"`
	AutoMergeAllowed         bool   `json:"autoMergeAllowed"`
	DeleteBranchOnMerge      bool   `json:"deleteBranchOnMerge"`
	ViewerDefaultMergeMethod string `json:"viewerDefaultMergeMethod"`
	ViewerPermission         string `json:"viewerPermission"`
}

// methods are the repo's allowed merge methods, in GitHub's order.
func (s repoSettings) methods() []string {
	var out []string
	if s.MergeCommitAllowed {
		out = append(out, "MERGE")
	}
	if s.SquashMergeAllowed {
		out = append(out, "SQUASH")
	}
	if s.RebaseMergeAllowed {
		out = append(out, "REBASE")
	}
	return out
}

// ── client ────────────────────────────────────────────────────────────────────

// tokenFn finds the token for a host; a seam for tests. gh's own environment
// variables win, as they do for gh.
var tokenFn = ghToken

func ghToken(ctx context.Context, host string) (string, error) {
	vars := []string{"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}
	if host == "github.com" {
		vars = []string{"GH_TOKEN", "GITHUB_TOKEN"}
	}
	for _, v := range vars {
		if t := strings.TrimSpace(os.Getenv(v)); t != "" {
			return t, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", host).Output()
	if err != nil {
		var ee *exec.Error
		if errors.As(err, &ee) {
			return "", errors.New("gh isn't installed: https://cli.github.com")
		}
		return "", &signedOutError{host}
	}
	t := strings.TrimSpace(string(out))
	if t == "" {
		return "", &signedOutError{host}
	}
	return t, nil
}

// endpointFn is the GraphQL URL for a host; a seam for tests.
var endpointFn = func(host string) string {
	if host == "github.com" {
		return "https://api.github.com/graphql"
	}
	return "https://" + host + "/api/graphql"
}

type ghClient struct {
	host  string
	mu    sync.Mutex
	token string
	http  *http.Client
}

func newClient(host string) *ghClient {
	return &ghClient{host: host, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *ghClient) auth(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return c.token, nil
	}
	t, err := tokenFn(ctx, c.host)
	if err != nil {
		return "", err
	}
	c.token = t
	return t, nil
}

type gqlError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// graphql runs one query. Every string in the answer was written by someone
// else, so it's sanitized before anything else sees it.
func (c *ghClient) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	token, err := c.auth(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpointFn(c.host), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "herdr-github")
	// mergeStateStatus is still behind a preview on some GitHub Enterprise versions.
	req.Header.Set("Accept", "application/vnd.github.merge-info-preview+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("GitHub: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		c.mu.Lock()
		c.token = ""
		c.mu.Unlock()
		return &signedOutError{c.host}
	}
	var reply struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GitHub: HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("GitHub: bad reply: %w", err)
	}
	if len(reply.Errors) > 0 {
		msgs := make([]string, 0, len(reply.Errors))
		notFound := true
		for _, e := range reply.Errors {
			msgs = append(msgs, clean(e.Message, false))
			notFound = notFound && e.Type == "NOT_FOUND"
		}
		msg := "GitHub: " + strings.Join(msgs, "; ")
		if out == nil || len(reply.Data) == 0 || string(reply.Data) == "null" {
			return errors.New(msg)
		}
		if err := json.Unmarshal(reply.Data, out); err != nil {
			return errors.New(msg)
		}
		sanitize(out)
		return &partialError{msg, notFound}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub: HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(reply.Data, out); err != nil {
			return fmt.Errorf("GitHub: %w", err)
		}
		sanitize(out)
	}
	return nil
}

// ── queries ───────────────────────────────────────────────────────────────────

const prFields = `
fragment PR on PullRequest {
  id number title url isDraft state updatedAt
  author { login }
  repository { nameWithOwner }
  headRefName baseRefName isCrossRepository
  headRepositoryOwner { login }
  headRepository { nameWithOwner }
  reviewDecision mergeable additions deletions
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  labels(first: 6) { nodes { name color } }
  autoMergeRequest { enabledAt mergeMethod }
  mergeQueueEntry { position state }
}`

const searchQuery = `query($q: String!, $first: Int!, $after: String) {
  search(query: $q, type: ISSUE, first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { ...PR }
  }
}` + prFields

// Page sizes: GitHub takes about a second per 10 PRs here (checks, reviews
// and mergeability are worked out per PR), so the first page is small to
// show something soon, and the rest follow in bigger pages.
const (
	firstPage = 20
	morePage  = 50
	listCap   = 200
)

// searchPage runs one page of a GitHub search. next is the cursor for the
// following page, "" at the end.
func (c *ghClient) searchPage(ctx context.Context, q string, first int, after string) (prs []pullRequest, next string, err error) {
	var res struct {
		Search struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []pullRequest `json:"nodes"`
		} `json:"search"`
	}
	vars := map[string]any{"q": q, "first": first, "after": nil}
	if after != "" {
		vars["after"] = after
	}
	err = c.graphql(ctx, searchQuery, vars, &res)
	var partial *partialError
	if err != nil && !errors.As(err, &partial) {
		return nil, "", err
	}
	for _, pr := range res.Search.Nodes {
		if pr.ID == "" { // not a PR (search can't be limited to PRs by type)
			continue
		}
		pr.Host = c.host
		prs = append(prs, pr)
	}
	if res.Search.PageInfo.HasNextPage {
		next = res.Search.PageInfo.EndCursor
	}
	return prs, next, err
}

// search lists every PR matching a GitHub search, up to listCap.
func (c *ghClient) search(ctx context.Context, q string) ([]pullRequest, error) {
	var all []pullRequest
	after, size := "", firstPage
	for {
		prs, next, err := c.searchPage(ctx, q, size, after)
		all = append(all, prs...)
		switch {
		case err != nil:
			return all, err
		case next == "":
			return all, nil
		case len(all) >= listCap:
			return all, errTruncated
		}
		after, size = next, morePage
	}
}

const detailQuery = `query($id: ID!, $n: Int!) {
  node(id: $id) {
    ... on PullRequest {
      ...PR
      body mergeStateStatus changedFiles headRefOid maintainerCanModify
      isMergeQueueEnabled
      mergeQueueEntry { estimatedTimeToMerge }
      headRef { id }
      viewerCanMergeAsAdmin
      repository {
        squashMergeAllowed mergeCommitAllowed rebaseMergeAllowed autoMergeAllowed
        deleteBranchOnMerge viewerDefaultMergeMethod viewerPermission
      }
      last: commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
        __typename
        ... on CheckRun { name status conclusion detailsUrl isRequired(pullRequestNumber: $n) }
        ... on StatusContext { context state targetUrl isRequired(pullRequestNumber: $n) }
      } } } } } }
      latestReviews(first: 30) { nodes { author { login } state } }
      reviewRequests(first: 30) { nodes { requestedReviewer {
        ... on User { login } ... on Team { name } ... on Mannequin { login }
      } } }
      reviews(last: 30) { nodes { author { login } body state createdAt: submittedAt } }
      comments(last: 50) { nodes { author { login } body createdAt } }
      reviewThreads(first: 60) { nodes {
        isResolved isOutdated path line
        comments(first: 4) { nodes { author { login } body createdAt } }
      } }
    }
  }
}` + prFields

func (c *ghClient) detail(ctx context.Context, pr pullRequest) (*prDetail, error) {
	var res struct {
		Node *struct {
			prDetail
			Repository struct {
				NameWithOwner string `json:"nameWithOwner"`
				repoSettings
			} `json:"repository"`
			ViewerCanMergeAsAdmin bool `json:"viewerCanMergeAsAdmin"`
			Last                  struct {
				Nodes []struct {
					Commit struct {
						StatusCheckRollup *struct {
							Contexts struct {
								Nodes []check `json:"nodes"`
							} `json:"contexts"`
						} `json:"statusCheckRollup"`
					} `json:"commit"`
				} `json:"nodes"`
			} `json:"last"`
			LatestReviews struct {
				Nodes []comment `json:"nodes"`
			} `json:"latestReviews"`
			ReviewRequests struct {
				Nodes []struct {
					RequestedReviewer *struct {
						Login string `json:"login"`
						Name  string `json:"name"`
					} `json:"requestedReviewer"`
				} `json:"nodes"`
			} `json:"reviewRequests"`
			Reviews struct {
				Nodes []comment `json:"nodes"`
			} `json:"reviews"`
			Comments struct {
				Nodes []comment `json:"nodes"`
			} `json:"comments"`
			ReviewThreads struct {
				Nodes []thread `json:"nodes"`
			} `json:"reviewThreads"`
		} `json:"node"`
	}
	err := c.graphql(ctx, detailQuery, map[string]any{"id": pr.ID, "n": pr.Number}, &res)
	var partial *partialError
	if err != nil && (!errors.As(err, &partial) || res.Node == nil) {
		return nil, err
	}
	// A partial answer (a reviewer team the token can't see) still shows.
	n := res.Node
	if n == nil {
		return nil, fmt.Errorf("#%d isn't there any more", pr.Number)
	}
	d := n.prDetail
	d.Host = c.host
	d.pullRequest.Repository.NameWithOwner = n.Repository.NameWithOwner
	d.RepoSettings = n.Repository.repoSettings
	d.ViewerCanMerge = n.ViewerCanMergeAsAdmin || map[string]bool{"ADMIN": true, "MAINTAIN": true, "WRITE": true}[d.RepoSettings.ViewerPermission]
	if l := n.Last.Nodes; len(l) > 0 && l[0].Commit.StatusCheckRollup != nil {
		d.Checks = l[0].Commit.StatusCheckRollup.Contexts.Nodes
	}
	sortChecks(d.Checks)
	for _, r := range n.LatestReviews.Nodes {
		if r.Author != nil {
			d.Reviewers = append(d.Reviewers, reviewer{r.Author.Login, r.State})
		}
	}
	for _, r := range n.ReviewRequests.Nodes {
		if rr := r.RequestedReviewer; rr != nil {
			name := rr.Login
			if name == "" {
				name = rr.Name
			}
			d.Reviewers = append(d.Reviewers, reviewer{name, "PENDING"})
		}
	}
	d.Reviews, d.Comments, d.Threads = n.Reviews.Nodes, n.Comments.Nodes, n.ReviewThreads.Nodes
	return &d, nil
}

// sortChecks puts failures first, then pending, then the rest, by name.
func sortChecks(cs []check) {
	rank := map[string]int{"fail": 0, "pending": 1, "pass": 2, "skip": 3}
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := rank[cs[i].outcome()], rank[cs[j].outcome()]
		if a != b {
			return a < b
		}
		return strings.ToLower(cs[i].name()) < strings.ToLower(cs[j].name())
	})
}

// ── mutations ─────────────────────────────────────────────────────────────────

func (c *ghClient) setDraft(ctx context.Context, prID string, draft bool) error {
	q := `mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }`
	if draft {
		q = `mutation($id: ID!) { convertPullRequestToDraft(input: {pullRequestId: $id}) { pullRequest { isDraft } } }`
	}
	return c.graphql(ctx, q, map[string]any{"id": prID}, nil)
}

// merge merges now, or with auto turns on auto-merge. The head commit is
// pinned: if someone pushed since the PR screen loaded, GitHub refuses
// rather than merge code you haven't seen.
func (c *ghClient) merge(ctx context.Context, d *prDetail, method string, auto bool) error {
	vars := map[string]any{"id": d.ID, "method": method, "oid": d.HeadRefOid}
	if auto {
		return c.graphql(ctx, `mutation($id: ID!, $method: PullRequestMergeMethod!, $oid: GitObjectID) {
  enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: $method, expectedHeadOid: $oid}) { clientMutationId }
}`, vars, nil)
	}
	return c.graphql(ctx, `mutation($id: ID!, $method: PullRequestMergeMethod!, $oid: GitObjectID) {
  mergePullRequest(input: {pullRequestId: $id, mergeMethod: $method, expectedHeadOid: $oid}) { clientMutationId }
}`, vars, nil)
}

// enqueue adds the PR to its base branch's merge queue, which merges it
// when its turn comes, by the queue's own method. Pinned to the head commit,
// as merge is.
func (c *ghClient) enqueue(ctx context.Context, d *prDetail) error {
	return c.graphql(ctx, `mutation($id: ID!, $oid: GitObjectID) {
  enqueuePullRequest(input: {pullRequestId: $id, expectedHeadOid: $oid}) { clientMutationId }
}`, map[string]any{"id": d.ID, "oid": d.HeadRefOid}, nil)
}

// updateBranch merges the base branch into the PR's, as GitHub's "Update
// branch" does (a merge, its default; updateMethod is left out, which older
// Enterprise servers don't know). Pinned to the head commit, as merge is.
func (c *ghClient) updateBranch(ctx context.Context, d *prDetail) error {
	return c.graphql(ctx, `mutation($id: ID!, $oid: GitObjectID) {
  updatePullRequestBranch(input: {pullRequestId: $id, expectedHeadOid: $oid}) { clientMutationId }
}`, map[string]any{"id": d.ID, "oid": d.HeadRefOid}, nil)
}

// dequeue takes the PR out of the merge queue.
func (c *ghClient) dequeue(ctx context.Context, prID string) error {
	return c.graphql(ctx, `mutation($id: ID!) { dequeuePullRequest(input: {id: $id}) { clientMutationId } }`,
		map[string]any{"id": prID}, nil)
}

func (c *ghClient) disableAutoMerge(ctx context.Context, prID string) error {
	return c.graphql(ctx, `mutation($id: ID!) { disablePullRequestAutoMerge(input: {pullRequestId: $id}) { clientMutationId } }`,
		map[string]any{"id": prID}, nil)
}

func (c *ghClient) deleteRef(ctx context.Context, refID string) error {
	return c.graphql(ctx, `mutation($id: ID!) { deleteRef(input: {refId: $id}) { clientMutationId } }`,
		map[string]any{"id": refID}, nil)
}

// ── branch → PR, for the sidebar labels ───────────────────────────────────────

// prStatus is what a sidebar label needs to know about a branch's PR.
type prStatus struct {
	Number              int    `json:"number"`
	State               string `json:"state"`
	IsDraft             bool   `json:"isDraft"`
	ReviewDecision      string `json:"reviewDecision"`
	Mergeable           string `json:"mergeable"`
	URL                 string `json:"url"`
	HeadRepositoryOwner *actor `json:"headRepositoryOwner"`
	Commits             rollup `json:"commits"`
	AutoMergeRequest    *struct {
		EnabledAt string `json:"enabledAt"`
	} `json:"autoMergeRequest"`
	MergeQueueEntry *struct {
		Position int `json:"position"`
	} `json:"mergeQueueEntry"`
	ClosedAt *time.Time `json:"closedAt,omitempty"` // when it was merged or closed, where that was asked
}

func (s prStatus) checks() string {
	return pullRequest{Commits: s.Commits}.checks()
}

// branchHead is a branch to look up: the head branch's name and, when known,
// the owner of the repo it's pushed to (a fork's owner, say).
type branchHead struct {
	Branch string
	Owner  string
}

// prsForBranches finds the PR for each head branch in one repo, in a single
// query. A branch can have had several PRs; an open one wins, else the most
// recently updated. Branches with no PR are left out.
func (c *ghClient) prsForBranches(ctx context.Context, r repoRef, heads []branchHead) (map[branchHead]prStatus, error) {
	if len(heads) == 0 {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("query($owner: String!, $name: String!")
	vars := map[string]any{"owner": r.Owner, "name": r.Name}
	for i, h := range heads {
		fmt.Fprintf(&b, ", $b%d: String!", i)
		vars[fmt.Sprintf("b%d", i)] = h.Branch
	}
	b.WriteString(") { repository(owner: $owner, name: $name) {")
	for i := range heads {
		fmt.Fprintf(&b, ` b%[1]d: pullRequests(headRefName: $b%[1]d, first: 5, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes {
  number state isDraft reviewDecision mergeable url headRepositoryOwner { login }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  autoMergeRequest { enabledAt }
  mergeQueueEntry { position }
} }`, i)
	}
	b.WriteString(" } }")
	var res struct {
		Repository map[string]struct {
			Nodes []prStatus `json:"nodes"`
		} `json:"repository"`
	}
	// A partial answer here is a failure: a branch GitHub didn't answer for
	// would read as "no PR" and clear its label.
	if err := c.graphql(ctx, b.String(), vars, &res); err != nil {
		return nil, err
	}
	out := map[branchHead]prStatus{}
	for i, h := range heads {
		if s, ok := pickPR(res.Repository[fmt.Sprintf("b%d", i)].Nodes, h.Owner); ok {
			out[h] = s
		}
	}
	return out, nil
}

// prByNumber is one pull request, as a list would have it.
func (c *ghClient) prByNumber(ctx context.Context, r repoRef, number int) (pullRequest, error) {
	var res struct {
		Repository *struct {
			PullRequest *pullRequest `json:"pullRequest"`
		} `json:"repository"`
	}
	err := c.graphql(ctx, `query($owner: String!, $name: String!, $n: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $n) { ...PR } }
}`+prFields, map[string]any{"owner": r.Owner, "name": r.Name, "n": number}, &res)
	if res.Repository == nil || res.Repository.PullRequest == nil {
		if err == nil || isPartial(err) {
			err = fmt.Errorf("%s#%d isn't there, or isn't yours to see", r, number)
		}
		return pullRequest{}, err
	}
	pr := *res.Repository.PullRequest
	pr.Host = c.host
	return pr, nil
}

// prsByNumber is the status of pull requests in one repo, in a single query.
// One that isn't there (or a repo that isn't) is left out.
func (c *ghClient) prsByNumber(ctx context.Context, r repoRef, numbers []int) (map[int]prStatus, error) {
	if len(numbers) == 0 {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("query($owner: String!, $name: String!")
	vars := map[string]any{"owner": r.Owner, "name": r.Name}
	for i, n := range numbers {
		fmt.Fprintf(&b, ", $n%d: Int!", i)
		vars[fmt.Sprintf("n%d", i)] = n
	}
	b.WriteString(") { repository(owner: $owner, name: $name) {")
	for i := range numbers {
		fmt.Fprintf(&b, ` n%[1]d: pullRequest(number: $n%[1]d) {
  number state isDraft reviewDecision mergeable url closedAt headRepositoryOwner { login }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  autoMergeRequest { enabledAt }
  mergeQueueEntry { position }
}`, i)
	}
	b.WriteString(" } }")
	var res struct {
		Repository map[string]*prStatus `json:"repository"`
	}
	// Any other partial answer is a failure: a PR GitHub didn't answer for
	// would read as gone.
	var partial *partialError
	if err := c.graphql(ctx, b.String(), vars, &res); err != nil && !(errors.As(err, &partial) && partial.notFound) {
		return nil, err
	}
	out := map[int]prStatus{}
	for i, n := range numbers {
		if s := res.Repository[fmt.Sprintf("n%d", i)]; s != nil {
			out[n] = *s
		}
	}
	return out, nil
}

// pickPR chooses among the PRs from branches of one name: those from the
// expected owner's repo (another fork can use the same branch name), an open
// one first, else the latest.
func pickPR(nodes []prStatus, owner string) (prStatus, bool) {
	var fit []prStatus
	for _, n := range nodes {
		if owner == "" || n.HeadRepositoryOwner == nil || strings.EqualFold(n.HeadRepositoryOwner.Login, owner) {
			fit = append(fit, n)
		}
	}
	for _, n := range fit {
		if n.State == "OPEN" {
			return n, true
		}
	}
	if len(fit) > 0 {
		return fit[0], true
	}
	return prStatus{}, false
}

// ── your repos, for the repo picker ───────────────────────────────────────────

// repoInfo is a repo you can reach, and when it was last pushed to.
type repoInfo struct {
	Repo   repoRef
	Pushed time.Time
}

const viewerReposQuery = `query($after: String) {
  viewer {
    repositories(first: 100, after: $after, isArchived: false,
      affiliations: [OWNER, ORGANIZATION_MEMBER, COLLABORATOR],
      ownerAffiliations: [OWNER, ORGANIZATION_MEMBER, COLLABORATOR],
      orderBy: {field: PUSHED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes { nameWithOwner pushedAt }
    }
  }
}`

// viewerReposPage is one page (100) of the repos you own or reach through
// an org or as a collaborator, most recently pushed first. next continues it.
func (c *ghClient) viewerReposPage(ctx context.Context, after string) (out []repoInfo, next string, err error) {
	var res struct {
		Viewer struct {
			Repositories struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					NameWithOwner string    `json:"nameWithOwner"`
					PushedAt      time.Time `json:"pushedAt"`
				} `json:"nodes"`
			} `json:"repositories"`
		} `json:"viewer"`
	}
	vars := map[string]any{"after": nil}
	if after != "" {
		vars["after"] = after
	}
	err = c.graphql(ctx, viewerReposQuery, vars, &res)
	if err != nil && !isPartial(err) {
		return nil, "", err
	}
	for _, n := range res.Viewer.Repositories.Nodes {
		if owner, name, ok := strings.Cut(n.NameWithOwner, "/"); ok {
			out = append(out, repoInfo{repoRef{Host: c.host, Owner: owner, Name: name}, n.PushedAt})
		}
	}
	if res.Viewer.Repositories.PageInfo.HasNextPage {
		next = res.Viewer.Repositories.PageInfo.EndCursor
	}
	return out, next, nil
}

// viewerRepos lists them all, up to 300.
func (c *ghClient) viewerRepos(ctx context.Context) ([]repoInfo, error) {
	var out []repoInfo
	after := ""
	for page := 0; page < 3; page++ {
		rs, next, err := c.viewerReposPage(ctx, after)
		out = append(out, rs...)
		if err != nil || next == "" {
			return out, err
		}
		after = next
	}
	return out, nil
}
