package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type tab int

const (
	tabMine tab = iota
	tabReview
	tabRepo
)

// page is one page of a tab's list. next continues it; "" means that was all.
type page struct {
	prs  []pullRequest
	next string
}

// source is where the picker's pull requests come from: GitHub, or the demo.
type source interface {
	// list loads a page of a tab: the first with cursor "", then with each
	// page's next until it's "".
	list(ctx context.Context, t tab, cursor string) (page, error)
	detail(ctx context.Context, pr pullRequest) (*prDetail, error)
	// byNumber is a PR that isn't in a list: enough of it to open its screen.
	byNumber(ctx context.Context, r repoRef, number int) (pullRequest, error)
	setDraft(ctx context.Context, pr pullRequest, draft bool) error
	merge(ctx context.Context, d *prDetail, method string, auto bool) error
	disableAutoMerge(ctx context.Context, d *prDetail) error
	// enqueue and dequeue: in and out of the base branch's merge queue.
	enqueue(ctx context.Context, d *prDetail) error
	dequeue(ctx context.Context, d *prDetail) error
	// updateBranch merges the base into a PR's branch that's behind it.
	updateBranch(ctx context.Context, d *prDetail) error
	deleteBranch(ctx context.Context, d *prDetail) error
	// repos pages through the repos you can reach, for the repo list:
	// the first page with cursor "", then each page's next until it's "".
	repos(ctx context.Context, cursor string) ([]repoInfo, string, error)
}

// githubSource talks to every configured host, one client per host.
type githubSource struct {
	cfg     config
	repo    *repoRef // the repo the picker was opened in, if it's a GitHub one
	mu      sync.Mutex
	clients map[string]*ghClient
}

func newGitHubSource(cfg config, repo *repoRef) *githubSource {
	return &githubSource{cfg: cfg, repo: repo, clients: map[string]*ghClient{}}
}

func (s *githubSource) client(host string) *ghClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clients[host]
	if !ok {
		c = newClient(host)
		s.clients[host] = c
	}
	return c
}

var tabQueries = map[tab]string{
	tabMine:   "is:pr is:open archived:false author:@me sort:updated-desc",
	tabReview: "is:pr is:open archived:false review-requested:@me sort:updated-desc",
}

// list runs the tab's search. The repo tab pages through one search; Mine
// and Review search every host at once, and their cursor holds each host's
// place (JSON: host → cursor). A host you're signed out of is skipped while
// another answers; with none answering, the error stands.
func (s *githubSource) list(ctx context.Context, t tab, cursor string) (page, error) {
	size := firstPage
	if cursor != "" {
		size = morePage
	}
	if t == tabRepo {
		if s.repo == nil {
			return page{}, nil
		}
		prs, next, err := s.client(s.repo.Host).searchPage(ctx, "is:pr is:open repo:"+s.repo.String()+" sort:updated-desc", size, cursor)
		return page{prs, next}, err
	}

	hosts := map[string]string{} // host → where to continue
	if cursor == "" {
		for _, h := range s.cfg.Hosts {
			hosts[h] = ""
		}
	} else if err := json.Unmarshal([]byte(cursor), &hosts); err != nil {
		return page{}, err
	}
	type result struct {
		host string
		prs  []pullRequest
		next string
		err  error
	}
	results := make(chan result, len(hosts))
	for h, after := range hosts {
		go func() {
			prs, next, err := s.client(h).searchPage(ctx, tabQueries[t], size, after)
			results <- result{h, prs, next, err}
		}()
	}
	var out page
	var errs []error
	nexts := map[string]string{}
	answered := false
	for range hosts {
		r := <-results
		out.prs = append(out.prs, r.prs...)
		switch {
		case r.err == nil:
			answered = true
			if r.next != "" {
				nexts[r.host] = r.next
			}
		case errors.Is(r.err, errSignedOut) && len(hosts) > 1:
			errs = append(errs, r.err) // other hosts may still answer
		case isPartial(r.err):
			answered = true // what came back shows, with the error
			errs = append(errs, r.err)
			if r.next != "" {
				nexts[r.host] = r.next
			}
		default:
			return out, r.err
		}
	}
	if len(nexts) > 0 {
		data, _ := json.Marshal(nexts)
		out.next = string(data)
	}
	sortPRs(out.prs, t)
	if !answered || len(errs) > 0 {
		return out, errors.Join(errs...)
	}
	return out, nil
}

// sortPRs orders a tab: by group (groupOf: whose move it is, yours first),
// and within one, newest first.
func sortPRs(prs []pullRequest, t tab) {
	sort.SliceStable(prs, func(i, j int) bool {
		a, b := prs[i], prs[j]
		if ga, gb := groupOf(a, t), groupOf(b, t); ga != gb {
			return ga < gb
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
}

func (s *githubSource) detail(ctx context.Context, pr pullRequest) (*prDetail, error) {
	return s.client(pr.Host).detail(ctx, pr)
}

func (s *githubSource) byNumber(ctx context.Context, r repoRef, number int) (pullRequest, error) {
	return s.client(r.Host).prByNumber(ctx, r, number)
}

func (s *githubSource) setDraft(ctx context.Context, pr pullRequest, draft bool) error {
	return s.client(pr.Host).setDraft(ctx, pr.ID, draft)
}

func (s *githubSource) merge(ctx context.Context, d *prDetail, method string, auto bool) error {
	return s.client(d.Host).merge(ctx, d, method, auto)
}

func (s *githubSource) disableAutoMerge(ctx context.Context, d *prDetail) error {
	return s.client(d.Host).disableAutoMerge(ctx, d.ID)
}

func (s *githubSource) enqueue(ctx context.Context, d *prDetail) error {
	return s.client(d.Host).enqueue(ctx, d)
}

func (s *githubSource) dequeue(ctx context.Context, d *prDetail) error {
	return s.client(d.Host).dequeue(ctx, d.ID)
}

func (s *githubSource) updateBranch(ctx context.Context, d *prDetail) error {
	return s.client(d.Host).updateBranch(ctx, d)
}

func (s *githubSource) deleteBranch(ctx context.Context, d *prDetail) error {
	if d.HeadRef == nil {
		return errors.New("the branch is already gone")
	}
	return s.client(d.Host).deleteRef(ctx, d.HeadRef.ID)
}

func isPartial(err error) bool {
	var p *partialError
	return errors.As(err, &p)
}

// repos pages through each host in turn. The cursor is "<host index>:<GitHub's
// cursor>". A host that fails is skipped, unless it's the only one.
func (s *githubSource) repos(ctx context.Context, cursor string) ([]repoInfo, string, error) {
	i, after := 0, ""
	if cursor != "" {
		n, rest, _ := strings.Cut(cursor, ":")
		i, _ = strconv.Atoi(n)
		after = rest
	}
	if i >= len(s.cfg.Hosts) {
		return nil, "", nil
	}
	rs, next, err := s.client(s.cfg.Hosts[i]).viewerReposPage(ctx, after)
	switch {
	case next != "":
		next = strconv.Itoa(i) + ":" + next
	case i+1 < len(s.cfg.Hosts):
		next = strconv.Itoa(i+1) + ":"
	}
	if err != nil && len(s.cfg.Hosts) > 1 {
		return rs, next, nil
	}
	return rs, next, err
}
