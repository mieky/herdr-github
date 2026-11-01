package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The demo: Halcyon, a made-up notes-app company, and its pull requests.
// Everything works on it (drafts toggle, PRs merge, worktrees "open") and
// nothing is real: it never contacts GitHub, never runs git, never opens a
// browser. It's also where the README's screenshots come from.

// demoDoneMsg is a demo action's result: what the real plugin would have done.
type demoDoneMsg struct {
	note string
	key  string // the PR whose worktree now "exists"
}

type demoSource struct {
	mu      sync.Mutex
	home    repoRef // the repo the demo pretends it was opened in
	me      string
	prs     []*prDetail
	trees   map[string]bool
	methods map[string]string
}

func newDemoSource() *demoSource {
	now := nowFn()
	h := func(hours float64) time.Time { return now.Add(-time.Duration(hours * float64(time.Hour))) }
	d := &demoSource{
		home: repoRef{Host: "github.com", Owner: "halcyon", Name: "notes-app"},
		me:   "sam", trees: map[string]bool{}, methods: map[string]string{},
	}
	settings := repoSettings{SquashMergeAllowed: true, MergeCommitAllowed: true, AutoMergeAllowed: true, ViewerDefaultMergeMethod: "SQUASH", ViewerPermission: "WRITE"}
	pass := func(names ...string) []check {
		var cs []check
		for _, n := range names {
			cs = append(cs, check{Typename: "CheckRun", Name: n, Status: "COMPLETED", Conclusion: "SUCCESS", IsRequired: n == "build" || n == "test"})
		}
		return cs
	}
	add := func(p *prDetail) {
		p.Host = "github.com"
		p.ID = fmt.Sprintf("PR_demo_%d", p.Number)
		p.URL = fmt.Sprintf("https://github.com/%s/pull/%d", p.Repository.NameWithOwner, p.Number)
		p.State = "OPEN"
		p.RepoSettings = settings
		p.ViewerCanMerge = true
		p.HeadRefOid = "0000000"
		p.HeadRef = &struct {
			ID string `json:"id"`
		}{ID: "REF_" + p.HeadRefName}
		if p.BaseRefName == "" {
			p.BaseRefName = "main"
		}
		d.prs = append(d.prs, p)
	}
	pr := func(repo string, n int, title, author, branch string, updated float64) *prDetail {
		p := &prDetail{}
		p.Number, p.Title, p.Author, p.HeadRefName, p.UpdatedAt = n, title, &actor{author}, branch, h(updated)
		p.Repository.NameWithOwner = repo
		p.Mergeable = "MERGEABLE"
		return p
	}

	// Sam's own.
	p := pr("halcyon/notes-app", 482, "Offline edits survive a sync conflict", "sam", "sam/offline-conflicts", 0.5)
	p.Additions, p.Deletions, p.ChangedFiles = 318, 74, 9
	p.ReviewDecision, p.MergeStateStatus = "APPROVED", "CLEAN"
	p.Checks = pass("build", "test", "lint", "e2e (macOS)")
	p.Labels.Nodes = []label{{"sync", "0e8a16"}, {"bug", "d73a4a"}}
	p.Reviewers = []reviewer{{"priya", "APPROVED"}, {"jonas", "COMMENTED"}}
	p.Body = "When two devices edit the same note offline, the second sync used to drop the first device's edits.\n\n" +
		"This keeps both: the server now returns a **merge proposal** and the client applies it as a three-way merge.\n\n" +
		"### Changes\n\n- `SyncEngine` asks for a proposal instead of overwriting\n- New `ThreeWay.merge(base:ours:theirs:)`\n- Conflict markers only when a line changed on both sides\n\n" +
		"### Testing\n\n```swift\nlet merged = ThreeWay.merge(base: b, ours: o, theirs: t)\nXCTAssertEqual(merged.conflicts, 0)\n```\n\nFixes HAL-212."
	p.Reviews = []comment{
		{Author: &actor{"jonas"}, State: "COMMENTED", Body: "Nice. One question on the proposal endpoint's timeout, inline.", CreatedAt: h(20)},
		{Author: &actor{"priya"}, State: "APPROVED", Body: "Tested with two simulators in airplane mode: both edits land. Ship it.", CreatedAt: h(2)},
	}
	p.Comments = []comment{{Author: &actor{"sam"}, Body: "Added the e2e case for three devices too.", CreatedAt: h(3)}}
	p.Threads = []thread{{Path: "Sources/Sync/SyncEngine.swift", Line: 142, Comments: struct {
		Nodes []comment `json:"nodes"`
	}{[]comment{
		{Author: &actor{"jonas"}, Body: "Should this timeout be shorter than the request's own? Otherwise we never see it fire.", CreatedAt: h(20)},
		{Author: &actor{"sam"}, Body: "Good catch: it's 20s vs 30s now.", CreatedAt: h(4)},
	}}}}
	add(p)
	d.trees[p.key()] = true

	p = pr("halcyon/notes-app", 479, "Search index drops accents", "sam", "sam/search-accents", 26)
	p.Additions, p.Deletions, p.ChangedFiles = 41, 12, 3
	p.ReviewDecision, p.MergeStateStatus = "REVIEW_REQUIRED", "BLOCKED"
	p.Checks = append(pass("lint"), check{Typename: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "FAILURE", IsRequired: true},
		check{Typename: "CheckRun", Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS", IsRequired: true})
	p.Reviewers = []reviewer{{"priya", "PENDING"}}
	p.Body = "Folds diacritics before indexing, so `café` finds *cafe* and the other way round."
	add(p)

	p = pr("halcyon/notes-app", 476, "Spike: CRDT-backed notes", "sam", "sam/crdt-spike", 70)
	p.IsDraft, p.MergeStateStatus = true, "DRAFT"
	p.Additions, p.Deletions, p.ChangedFiles = 1204, 88, 31
	p.Checks = []check{{Typename: "CheckRun", Name: "build", Status: "IN_PROGRESS", IsRequired: true}}
	p.Body = "Not for merging yet: measuring what a CRDT costs us in memory on a 10k-note library."
	add(p)
	d.trees[p.key()] = true

	p = pr("halcyon/sync-server", 118, "Return merge proposals for concurrent edits", "sam", "sam/merge-proposals", 5)
	p.Additions, p.Deletions, p.ChangedFiles = 202, 19, 6
	p.ReviewDecision, p.MergeStateStatus = "CHANGES_REQUESTED", "BLOCKED"
	p.Checks = pass("build", "test")
	p.Reviewers = []reviewer{{"jonas", "CHANGES_REQUESTED"}}
	p.Body = "The server side of halcyon/notes-app#482."
	p.Reviews = []comment{{Author: &actor{"jonas"}, State: "CHANGES_REQUESTED", Body: "The proposal should carry the base revision, or the client can't tell a stale one.", CreatedAt: h(6)}}
	p.IsMergeQueueEnabled = true
	add(p)

	// sync-server's main merges through a merge queue: this one's ready to join it.
	p = pr("halcyon/sync-server", 121, "Back off on 429s from the push service", "sam", "sam/push-backoff", 3)
	p.Additions, p.Deletions, p.ChangedFiles = 64, 9, 2
	p.ReviewDecision, p.MergeStateStatus = "APPROVED", "CLEAN"
	p.Checks = pass("build", "test")
	p.Reviewers = []reviewer{{"jonas", "APPROVED"}}
	p.Body = "Retries a rate-limited push with exponential backoff and jitter, up to five tries, instead of dropping it."
	p.IsMergeQueueEnabled = true
	add(p)

	// Waiting on Sam's review.
	p = pr("halcyon/notes-app", 481, "Tag suggestions as you type", "priya", "priya/tag-suggest", 3)
	p.Additions, p.Deletions, p.ChangedFiles = 156, 20, 5
	p.ReviewDecision, p.MergeStateStatus = "REVIEW_REQUIRED", "BLOCKED"
	p.Checks = append(pass("build", "lint"), check{Typename: "CheckRun", Name: "test", Status: "IN_PROGRESS", IsRequired: true})
	p.Reviewers = []reviewer{{"sam", "PENDING"}}
	p.Labels.Nodes = []label{{"feature", "a2eeef"}}
	p.Body = "Suggests existing tags after `#`, ranked by how often you use them."
	add(p)

	p = pr("halcyon/notes-app", 480, "Fix typo in onboarding", "maria-k", "fix-typo", 9)
	p.IsCrossRepository = true
	p.HeadRepositoryOwner = &actor{"maria-k"}
	p.HeadRepository = &struct {
		NameWithOwner string `json:"nameWithOwner"`
	}{"maria-k/notes-app"}
	p.Additions, p.Deletions, p.ChangedFiles = 1, 1, 1
	p.ReviewDecision, p.MergeStateStatus = "REVIEW_REQUIRED", "BLOCKED"
	p.Checks = pass("build", "test", "lint")
	p.Reviewers = []reviewer{{"sam", "PENDING"}}
	p.Body = "\"Recieve\" → \"Receive\" on the second onboarding screen. First contribution!"
	add(p)

	p = pr("halcyon/design-system", 64, "Dark mode tokens for code blocks", "lee", "lee/code-tokens", 30)
	p.Additions, p.Deletions, p.ChangedFiles = 88, 40, 4
	p.Mergeable, p.MergeStateStatus = "CONFLICTING", "DIRTY"
	p.Checks = pass("build")
	p.Reviewers = []reviewer{{"sam", "PENDING"}}
	p.Body = "Adds `code.bg`, `code.fg` and `code.comment` for both modes."
	add(p)

	// Others' in notes-app.
	p = pr("halcyon/notes-app", 477, "Faster cold start: lazy-load the sidebar", "jonas", "jonas/lazy-sidebar", 12)
	p.Additions, p.Deletions, p.ChangedFiles = 97, 60, 7
	p.ReviewDecision, p.MergeStateStatus = "APPROVED", "CLEAN"
	p.Checks = pass("build", "test", "lint")
	p.AutoMergeRequest = &struct {
		EnabledAt   string `json:"enabledAt"`
		MergeMethod string `json:"mergeMethod"`
	}{"now", "SQUASH"}
	p.Body = "Cold start 1.9s → 1.2s on an iPhone 12."
	add(p)

	p = pr("halcyon/notes-app", 474, "Export to Markdown with attachments", "lee", "lee/export-md", 50)
	p.IsDraft, p.MergeStateStatus = true, "DRAFT"
	p.Additions, p.Deletions, p.ChangedFiles = 430, 12, 11
	p.Body = "Work in progress."
	add(p)
	return d
}

func (d *demoSource) list(_ context.Context, t tab, _ string) (page, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []pullRequest
	for _, p := range d.prs {
		if p.State != "OPEN" {
			continue
		}
		pr := p.pullRequest
		pr.Commits = rollupOf(p.Checks)
		mine := pr.author() == d.me
		requested := false
		for _, r := range p.Reviewers {
			requested = requested || (r.Name == d.me && r.State == "PENDING")
		}
		switch {
		case t == tabMine && mine, t == tabReview && requested, t == tabRepo && pr.repo().same(d.home):
			out = append(out, pr)
		}
	}
	sortPRs(out, t)
	return page{prs: out}, nil
}

// rollupOf is GitHub's combined state for a set of checks.
func rollupOf(cs []check) rollup {
	state := ""
	for _, c := range cs {
		switch c.outcome() {
		case "fail":
			state = "FAILURE"
		case "pending":
			if state != "FAILURE" {
				state = "PENDING"
			}
		case "pass":
			if state == "" {
				state = "SUCCESS"
			}
		}
	}
	var r rollup
	if state != "" {
		r.Nodes = make([]struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		}, 1)
		r.Nodes[0].Commit.StatusCheckRollup = &struct {
			State string `json:"state"`
		}{state}
	}
	return r
}

func (d *demoSource) homeRepo() repoRef {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.home
}

func (d *demoSource) setHome(r repoRef) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.home = r
}

// spaceRepos are the demo org's repos with PRs, as if each had a herdr space.
func (d *demoSource) spaceRepos() []repoRef {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []repoRef
	for _, p := range d.prs {
		out = append(out, p.repo())
	}
	return out
}

// The demo's GitHub: its own repos, plus a few you'd reach through other
// orgs and your own account.
func (d *demoSource) repos(context.Context, string) ([]repoInfo, string, error) {
	now := nowFn()
	var out []repoInfo
	for i, r := range []string{
		"halcyon/notes-app", "halcyon/sync-server", "halcyon/design-system", "halcyon/website", "halcyon/infra",
		"sam/dotfiles", "sam/notes-cli", "oss-typesetting/mdtype", "oss-typesetting/fonts",
	} {
		owner, name, _ := strings.Cut(r, "/")
		out = append(out, repoInfo{repoRef{"github.com", owner, name}, now.Add(-time.Duration(i) * 6 * time.Hour)})
	}
	return out, "", nil
}

func (d *demoSource) find(key string) *prDetail {
	for _, p := range d.prs {
		if p.key() == key {
			return p
		}
	}
	return nil
}

func (d *demoSource) detail(_ context.Context, pr pullRequest) (*prDetail, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.find(pr.key())
	if p == nil {
		return nil, fmt.Errorf("#%d isn't there any more", pr.Number)
	}
	c := *p
	c.Commits = rollupOf(p.Checks)
	c.Checks = append([]check(nil), p.Checks...)
	sortChecks(c.Checks)
	return &c, nil
}

func (d *demoSource) byNumber(_ context.Context, r repoRef, number int) (pullRequest, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.prs {
		if strings.ToLower(p.key()) == openedKey(r, number) {
			pr := p.pullRequest
			pr.Commits = rollupOf(p.Checks)
			return pr, nil
		}
	}
	return pullRequest{}, fmt.Errorf("%s#%d isn't there", r, number)
}

func (d *demoSource) setDraft(_ context.Context, pr pullRequest, draft bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.find(pr.key())
	p.IsDraft = draft
	p.MergeStateStatus = "DRAFT"
	if !draft {
		p.MergeStateStatus = "BLOCKED"
		if p.ReviewDecision == "APPROVED" {
			p.MergeStateStatus = "CLEAN"
		}
	}
	return nil
}

func (d *demoSource) merge(_ context.Context, pd *prDetail, method string, auto bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.find(pd.key())
	if auto {
		p.AutoMergeRequest = &struct {
			EnabledAt   string `json:"enabledAt"`
			MergeMethod string `json:"mergeMethod"`
		}{"now", method}
		return nil
	}
	p.State = "MERGED"
	return nil
}

func (d *demoSource) disableAutoMerge(_ context.Context, pd *prDetail) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.find(pd.key()).AutoMergeRequest = nil
	return nil
}

func (d *demoSource) enqueue(_ context.Context, pd *prDetail) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	eta := 540
	d.find(pd.key()).MergeQueueEntry = &queueEntry{Position: 1, State: "AWAITING_CHECKS", EstimatedTimeToMerge: &eta}
	return nil
}

func (d *demoSource) dequeue(_ context.Context, pd *prDetail) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.find(pd.key()).MergeQueueEntry = nil
	return nil
}

func (d *demoSource) deleteBranch(_ context.Context, pd *prDetail) error { return nil }

func (d *demoSource) removeTree(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.trees, key)
}

// agents: one PR's agent is in its space with another reviewing beside it,
// and another PR's has gone.
func (d *demoSource) agents() map[string][]prAgent {
	return map[string][]prAgent{
		"github.com/halcyon/notes-app#482": {
			{Pane: "w1:p1", Name: "Offline sync conflicts", Status: "idle", Session: "demo-session-482", Part: partOpened},
			{Pane: "w1:p2", Name: "Review the three-way merge", Status: "working", Session: "demo-session-483", Part: partBranch},
		},
		"github.com/halcyon/notes-app#476": {{Name: "CRDT spike", Session: "demo-session-476", Cwd: "~/code/halcyon/notes-app", Part: partOpened}},
	}
}

func (d *demoSource) worktreeMarks() map[string]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]bool{}
	for k, v := range d.trees {
		out[k] = v
	}
	return out
}

// worktree says what the real plugin would have done, and marks the PR as
// having a worktree now.
func (d *demoSource) worktree(pr pullRequest, start bool) demoDoneMsg {
	d.mu.Lock()
	defer d.mu.Unlock()
	branch := localBranch(pr)
	verb := "create a worktree on " + branch
	if d.trees[pr.key()] {
		verb = "open the worktree on " + branch
	}
	if pr.IsCrossRepository {
		verb += fmt.Sprintf(" (fetched from refs/pull/%d/head)", pr.Number)
	}
	note := "Demo: would " + verb
	if start && !d.trees[pr.key()] {
		note += ", then prompt its agent"
	}
	d.trees[pr.key()] = true
	return demoDoneMsg{note: strings.TrimSpace(note), key: pr.key()}
}
