package main

// The Herdr app's GitHub mod runs this binary as `herdr-github app JSON`:
// one request in, one JSON answer out, on the machine where gh is signed in.
// It's the popup's own code underneath (the same lists, groups, readiness,
// merge menu and worktrees), without the popup. A request that can outlast
// the app's 30 seconds (a worktree's fetch, a clone) runs as a job in the
// background, and the mod asks after it.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// appVersion is this build's version, as herdr-plugin.toml has it (a test
// holds them together): the mod checks it's the one it pinned.
const appVersion = "0.10.0"

// appRequest is what the mod asks.
type appRequest struct {
	Op     string `json:"op"`
	Demo   bool   `json:"demo,omitempty"`
	Tab    string `json:"tab,omitempty"`    // list: mine, review, repo
	Repo   string `json:"repo,omitempty"`   // list's repo tab: host/owner/name
	Cursor string `json:"cursor,omitempty"` // list, repos: where to go on from
	PR     *prID  `json:"pr,omitempty"`
	Do     string `json:"do,omitempty"`     // act: draft, ready, merge, auto, auto-off, enqueue, dequeue, delete-branch
	Method string `json:"method,omitempty"` // act merge, auto: MERGE, SQUASH, REBASE
	// worktree: folders to look in for a checkout of the PR's repo (the
	// machine's spaces), and whether to clone it when there's none.
	Dirs  []string `json:"dirs,omitempty"`
	Clone bool     `json:"clone,omitempty"`
	Path  string   `json:"path,omitempty"` // unworktree: the worktree to remove
	BG    bool     `json:"bg,omitempty"`   // run it as a job
	Job   string   `json:"job,omitempty"`  // job: which; on a job's own run, where its answer goes
}

// prID names a pull request: host, owner/name and number.
type prID struct {
	Host   string `json:"host"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

func (p prID) ref() (repoRef, error) {
	owner, name, ok := strings.Cut(p.Repo, "/")
	r := repoRef{Host: p.Host, Owner: owner, Name: name}
	if !ok || !r.valid() || p.Number <= 0 {
		return r, fmt.Errorf("not a pull request: %s#%d", p.Repo, p.Number)
	}
	return r, nil
}

// appRow is a PR as the mod lists it.
type appRow struct {
	Key       string      `json:"key"`
	Host      string      `json:"host"`
	Repo      string      `json:"repo"`
	Number    int         `json:"number"`
	Title     string      `json:"title"`
	URL       string      `json:"url"`
	Draft     bool        `json:"draft"`
	State     string      `json:"state"`
	Author    string      `json:"author"`
	Updated   int64       `json:"updated"` // ms since the epoch
	Head      string      `json:"head"`    // the PR's branch, owner: in front for a fork's
	Base      string      `json:"base"`
	Branch    string      `json:"branch"` // the branch its worktree is on
	Cross     bool        `json:"cross"`
	Review    string      `json:"review"`
	Conflicts bool        `json:"conflicts"`
	Additions int         `json:"additions"`
	Deletions int         `json:"deletions"`
	Checks    string      `json:"checks"` // pass, fail, pending, ""
	Labels    []label     `json:"labels"`
	Auto      string      `json:"auto,omitempty"` // auto-merge's method, while it's on
	Queue     *queueEntry `json:"queue,omitempty"`
	Group     string      `json:"group"`
	GroupName string      `json:"groupName"`
}

var groupKeys = map[group]string{groupReview: "review", groupFixing: "fixing", groupReady: "ready", groupWaiting: "waiting", groupDrafts: "drafts", groupDone: "done"}

func rowOf(pr pullRequest, t tab) appRow {
	head := pr.HeadRefName
	if pr.IsCrossRepository {
		head = pr.headOwner() + ":" + head
	}
	g := groupOf(pr, t)
	r := appRow{
		Key: strings.ToLower(pr.key()), Host: pr.Host, Repo: pr.Repository.NameWithOwner, Number: pr.Number,
		Title: clean(pr.Title, false), URL: pr.URL, Draft: pr.IsDraft, State: pr.State, Author: pr.author(),
		Updated: pr.UpdatedAt.UnixMilli(), Head: head, Base: pr.BaseRefName, Branch: localBranch(pr),
		Cross: pr.IsCrossRepository, Review: pr.ReviewDecision, Conflicts: pr.Mergeable == "CONFLICTING",
		Additions: pr.Additions, Deletions: pr.Deletions, Checks: rollupWord(pr.checks()),
		Labels: pr.Labels.Nodes, Queue: pr.MergeQueueEntry, Group: groupKeys[g], GroupName: g.label(),
	}
	if r.Labels == nil {
		r.Labels = []label{}
	}
	for i := range r.Labels {
		r.Labels[i].Name = clean(r.Labels[i].Name, false)
	}
	if pr.AutoMergeRequest != nil {
		r.Auto = pr.AutoMergeRequest.MergeMethod
	}
	return r
}

func rollupWord(state string) string {
	switch state {
	case "SUCCESS":
		return "pass"
	case "FAILURE", "ERROR":
		return "fail"
	case "PENDING", "EXPECTED":
		return "pending"
	}
	return ""
}

// appDetail is the PR screen.
type appDetail struct {
	appRow
	Files     int           `json:"files"`
	Ready     string        `json:"ready"`
	Tone      string        `json:"tone"` // ok, warn, err, dim, merged, queue
	Checks    []appCheck    `json:"checkList"`
	Reviewers []appReviewer `json:"reviewers"`
	Threads   int           `json:"threads"` // unresolved
	Body      string        `json:"body"`    // the description, threads and conversation, as Markdown
	CanMerge  bool          `json:"canMerge"`
	QueueOn   bool          `json:"queueOn"`
	// What the merge menu offers, under its title, or why there's nothing.
	MergeTitle string      `json:"mergeTitle"`
	Merge      []appChoice `json:"merge"`
	MergeWhy   string      `json:"mergeWhy,omitempty"`
	Cleanup    *appCleanup `json:"cleanup,omitempty"`
	Prompt     string      `json:"prompt"` // Start's first message to a new worktree's agent
}

type appCheck struct {
	Name     string `json:"name"`
	Outcome  string `json:"outcome"` // pass, fail, pending, skip
	Required bool   `json:"required"`
	URL      string `json:"url,omitempty"`
}

type appReviewer struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// appChoice is a merge menu choice, with what to ask before it runs.
type appChoice struct {
	mergeChoice
	Confirm string `json:"confirm,omitempty"`
	Yes     string `json:"yes,omitempty"`
}

// appCleanup is what can be tidied once it's merged: its branch on GitHub
// (unless the repo does that itself, or it's someone's fork).
type appCleanup struct {
	DeleteBranch bool `json:"deleteBranch"`
}

func detailOf(d *prDetail, cfg config) appDetail {
	out := appDetail{appRow: rowOf(d.pullRequest, tabMine), Files: d.ChangedFiles, CanMerge: d.ViewerCanMerge, QueueOn: d.IsMergeQueueEnabled}
	out.Ready, out.Tone = readinessTone(d)
	for _, c := range d.Checks {
		u := c.DetailsURL
		if u == "" {
			u = c.TargetURL
		}
		out.Checks = append(out.Checks, appCheck{Name: clean(c.name(), false), Outcome: c.outcome(), Required: c.IsRequired, URL: u})
	}
	for _, r := range d.Reviewers {
		out.Reviewers = append(out.Reviewers, appReviewer{Name: r.Name, State: r.State})
	}
	for _, t := range d.Threads {
		if !t.IsResolved {
			out.Threads++
		}
	}
	out.Body = clean(prBody(d), true)
	title, choices, why := mergeChoices(d)
	out.MergeTitle, out.MergeWhy = title, why
	for _, c := range choices {
		q, yes := confirmText(d, c)
		out.Merge = append(out.Merge, appChoice{mergeChoice: c, Confirm: q, Yes: yes})
	}
	if out.Checks == nil {
		out.Checks = []appCheck{}
	}
	if out.Reviewers == nil {
		out.Reviewers = []appReviewer{}
	}
	if out.Merge == nil {
		out.Merge = []appChoice{}
	}
	if !d.RepoSettings.DeleteBranchOnMerge && !d.IsCrossRepository && d.HeadRef != nil {
		out.Cleanup = &appCleanup{DeleteBranch: true}
	}
	out.Prompt = expandPrompt(cfg.StartPrompt, d.pullRequest)
	return out
}

// runApp answers one request on stdout. An error is an answer too, as
// {"error": …}, so the mod has one thing to read.
func runApp(ctx context.Context, cfg config, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("app needs one JSON request")
	}
	var req appRequest
	if err := json.Unmarshal([]byte(args[0]), &req); err != nil {
		return fmt.Errorf("app: %w", err)
	}
	labelStateFile = "app-sessions.json"
	var out any
	var err error
	switch {
	case req.BG && req.Job == "":
		out, err = startJob(req)
	case req.Op == "job":
		out, err = jobAnswer(req.Job)
	default:
		out, err = appAnswer(ctx, cfg, req)
	}
	if err != nil {
		out = appError(err)
	}
	if req.BG && req.Job != "" { // this is the job: its answer goes to its file
		return finishJob(req.Job, out)
	}
	return json.NewEncoder(stdout).Encode(out)
}

func appError(err error) map[string]any {
	e := map[string]any{"error": err.Error()}
	var so *signedOutError
	var nc *noCheckoutError
	switch {
	case errors.As(err, &so):
		e["signedOut"] = so.Host
	case errors.Is(err, errSignedOut):
		e["signedOut"] = true
	case errors.As(err, &nc):
		e["noCheckout"] = map[string]string{"repo": nc.Repo.String(), "dest": nc.Dest}
	}
	return e
}

func appAnswer(ctx context.Context, cfg config, req appRequest) (any, error) {
	var demo *appDemo
	var src source
	if req.Demo {
		demo = loadAppDemo()
		src = demo.src
	} else {
		var repo *repoRef
		if req.Repo != "" {
			parts := strings.SplitN(req.Repo, "/", 3)
			if len(parts) == 3 {
				repo = &repoRef{Host: parts[0], Owner: parts[1], Name: parts[2]}
			}
		}
		src = newGitHubSource(cfg, repo)
	}
	switch req.Op {
	case "status":
		return appStatus(ctx, cfg, req.Demo), nil
	case "list":
		t, ok := map[string]tab{"mine": tabMine, "review": tabReview, "repo": tabRepo}[req.Tab]
		if !ok {
			return nil, fmt.Errorf("no tab %q", req.Tab)
		}
		if demo != nil && t == tabRepo && req.Repo != "" {
			parts := strings.SplitN(req.Repo, "/", 3)
			if len(parts) == 3 {
				demo.src.setHome(repoRef{Host: parts[0], Owner: parts[1], Name: parts[2]})
			}
		}
		p, err := src.list(ctx, t, req.Cursor)
		rows := []appRow{}
		for _, pr := range p.prs {
			rows = append(rows, rowOf(pr, t))
		}
		res := map[string]any{"prs": rows, "next": p.next}
		if err != nil {
			if len(rows) == 0 {
				return nil, err
			}
			res["warning"] = err.Error() // what came back shows, with what's missing
		}
		return res, nil
	case "repos":
		list, next, err := src.repos(ctx, req.Cursor)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, r := range list {
			out = append(out, map[string]any{"host": r.Repo.Host, "repo": r.Repo.String(), "pushed": r.Pushed.UnixMilli()})
		}
		return map[string]any{"repos": out, "next": next}, nil
	case "sessions":
		if req.Demo {
			return map[string]any{"prs": demoSessions()}, nil
		}
		return map[string]any{"prs": appSessions(cfg)}, nil
	case "unworktree":
		if req.Demo {
			return map[string]any{"ok": true, "note": "Demo: would remove " + req.Path}, nil
		}
		return map[string]any{"ok": true}, removeAppWorktree(ctx, req.Path)
	}

	if req.PR == nil {
		return nil, errors.New(req.Op + " needs a pull request")
	}
	r, err := req.PR.ref()
	if err != nil {
		return nil, err
	}
	pr, err := src.byNumber(ctx, r, req.PR.Number)
	if err != nil {
		return nil, err
	}
	pr.Host = r.Host
	d, err := src.detail(ctx, pr)
	if err != nil {
		return nil, err
	}
	d.Host = r.Host
	switch req.Op {
	case "detail":
		return detailOf(d, cfg), nil
	case "act":
		note, err := appAct(ctx, src, d, req)
		if err != nil {
			return nil, err
		}
		if demo != nil {
			if err := demo.save(); err != nil {
				return nil, err
			}
		}
		return map[string]any{"ok": true, "note": note}, nil
	case "worktree":
		if demo != nil {
			note := demo.src.worktree(pr, false).note
			_ = demo.save()
			return map[string]any{"path": "~/.herdr/worktrees/" + r.Name + "/" + slug(localBranch(pr)), "created": true, "demo": true, "note": note, "prompt": expandPrompt(cfg.StartPrompt, pr)}, nil
		}
		return appWorktree(ctx, cfg, pr, req)
	}
	return nil, fmt.Errorf("no op %q", req.Op)
}

// appAct changes the PR on GitHub, and says what it did.
func appAct(ctx context.Context, src source, d *prDetail, req appRequest) (string, error) {
	n := d.Number
	switch req.Do {
	case "draft", "ready":
		if err := src.setDraft(ctx, d.pullRequest, req.Do == "draft"); err != nil {
			return "", err
		}
		if req.Do == "draft" {
			return fmt.Sprintf("#%d is a draft again", n), nil
		}
		return fmt.Sprintf("#%d is ready for review", n), nil
	case "merge", "auto":
		if _, ok := methodNames[req.Method]; !ok {
			return "", fmt.Errorf("no merge method %q", req.Method)
		}
		if err := src.merge(ctx, d, req.Method, req.Do == "auto"); err != nil {
			return "", err
		}
		rememberMethod(d.repo(), req.Method)
		if req.Do == "auto" {
			return fmt.Sprintf("Auto-merge is on: #%d merges when it's ready", n), nil
		}
		return fmt.Sprintf("Merged #%d", n), nil
	case "auto-off":
		return "Auto-merge is off", src.disableAutoMerge(ctx, d)
	case "enqueue":
		return fmt.Sprintf("#%d is in the merge queue", n), src.enqueue(ctx, d)
	case "dequeue":
		return fmt.Sprintf("#%d is out of the merge queue", n), src.dequeue(ctx, d)
	case "delete-branch":
		return "Deleted " + d.HeadRefName + " on GitHub", src.deleteBranch(ctx, d)
	}
	return "", fmt.Errorf("can't %q a pull request", req.Do)
}

// appStatus is which hosts gh is signed in to, and as whom.
func appStatus(ctx context.Context, cfg config, demo bool) map[string]any {
	hosts := []map[string]string{}
	if demo {
		hosts = append(hosts, map[string]string{"host": "github.com", "login": "sam"})
		return map[string]any{"hosts": hosts, "version": appVersion}
	}
	for _, h := range cfg.Hosts {
		e := map[string]string{"host": h}
		if _, err := tokenFn(ctx, h); err != nil {
			e["error"] = err.Error()
		} else {
			var res struct {
				Viewer struct {
					Login string `json:"login"`
				} `json:"viewer"`
			}
			if err := newClient(h).graphql(ctx, `query { viewer { login } }`, nil, &res); err != nil {
				e["error"] = err.Error()
			} else {
				e["login"] = res.Viewer.Login
			}
		}
		hosts = append(hosts, e)
	}
	return map[string]any{"hosts": hosts, "version": appVersion}
}

// ── sessions ──────────────────────────────────────────────────────────────────

// appSession is a Claude session that worked on a PR: the mod finds its
// agent among the app's by where it runs, or offers to resume it.
type appSession struct {
	Session string `json:"session"`
	Title   string `json:"title"`
	Cwd     string `json:"cwd"`
	Part    string `json:"part"` // opened it, worked on it
	Last    int64  `json:"last"` // ms since the epoch
}

// appSessions reads what Claude's transcripts say about PRs since the app
// last asked (its own record, apart from the labels'), by openedKey.
func appSessions(cfg config) map[string][]appSession {
	out := map[string][]appSession{}
	if !cfg.agentPRsOn() {
		return out
	}
	st := readLabelState()
	st.trackSessions(cfg, nil, nowFn())
	_ = writeLabelState(st)
	for key, pr := range st.PRs {
		opener := pr.opener()
		for s, part := range pr.By {
			if !sessionID.MatchString(s) {
				continue
			}
			ss := st.Sessions[s]
			a := appSession{Session: s, Title: clean(ss.Title, false), Cwd: clean(ss.Cwd, false), Part: partWorked, Last: part.Last.UnixMilli()}
			if s == opener {
				a.Part = partOpened
			}
			out[key] = append(out[key], a)
		}
	}
	for key := range out {
		sort.SliceStable(out[key], func(i, j int) bool {
			a, b := out[key][i], out[key][j]
			if (a.Part == partOpened) != (b.Part == partOpened) {
				return a.Part == partOpened
			}
			return a.Last > b.Last
		})
	}
	return out
}

func demoSessions() map[string][]appSession {
	now := nowFn()
	return map[string][]appSession{
		"github.com/halcyon/notes-app#482": {{Session: "demo-session-482", Title: "Offline sync conflicts", Cwd: "~/.herdr/worktrees/notes-app/sam-offline-conflicts", Part: partOpened, Last: now.Add(-20 * time.Minute).UnixMilli()}},
		"github.com/halcyon/notes-app#476": {{Session: "demo-session-476", Title: "CRDT spike", Cwd: "~/code/halcyon/notes-app", Part: partOpened, Last: now.Add(-70 * time.Hour).UnixMilli()}},
	}
}

// ── worktrees ─────────────────────────────────────────────────────────────────

// worktreeRoot is where the app keeps worktrees, as its own worktree mod
// does: <root>/<repo>/<branch, slashes as dashes>.
func worktreeRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".herdr", "worktrees")
}

var slugJunk = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func slug(branch string) string { return strings.Trim(slugJunk.ReplaceAllString(branch, "-"), "-") }

// appWorktree is openWorktree for the app: the PR's worktree in a checkout
// of its repo, made if there's none, from its head as GitHub has it now.
// The mod opens it in a space.
func appWorktree(ctx context.Context, cfg config, pr pullRequest, req appRequest) (any, error) {
	dir, err := findCheckout(ctx, cfg, pr.repo(), req.Dirs...)
	var nc *noCheckoutError
	if errors.As(err, &nc) && req.Clone {
		dir, err = clone(ctx, cfg, pr.repo())
	}
	if err != nil {
		return nil, err
	}
	prompt := expandPrompt(cfg.StartPrompt, pr)
	branch := localBranch(pr)
	if path := gitWorktreeOn(ctx, dir, branch); path != "" {
		if err := branchConflict(ctx, dir, pr, branch); err != nil {
			return nil, err
		}
		return map[string]any{"path": path, "created": false, "prompt": prompt}, nil
	}
	rem, ok := remoteForCheckout(ctx, cfg, dir, pr.repo())
	if !ok {
		return nil, fmt.Errorf("%s has no remote for %s", dir, pr.repo())
	}
	src, dst := "refs/heads/"+pr.HeadRefName, "refs/remotes/"+rem.Name+"/"+pr.HeadRefName
	if pr.IsCrossRepository {
		src, dst = fmt.Sprintf("refs/pull/%d/head", pr.Number), fmt.Sprintf("refs/remotes/%s/pr/%d", rem.Name, pr.Number)
	}
	fctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	_, fetchErr := git(fctx, dir, "fetch", "--quiet", "--no-tags", rem.Name, "+"+src+":"+dst)
	cancel()

	main := dir
	if common, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		main = filepath.Dir(strings.TrimSpace(common))
	}
	path := filepath.Join(worktreeRoot(), filepath.Base(main), slug(branch))
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s is already there, and isn't %s's worktree", path, branch)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	_, haveLocal := git(ctx, dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	switch {
	case haveLocal == nil:
		if err := branchConflict(ctx, dir, pr, branch); err != nil {
			return nil, err
		}
		if _, err := git(ctx, dir, "worktree", "add", path, branch); err != nil {
			return nil, err
		}
	case fetchErr != nil:
		return nil, fmt.Errorf("couldn't fetch #%d from %s: %v", pr.Number, rem.Name, fetchErr)
	default:
		if _, err := git(ctx, dir, "worktree", "add", "--no-track", "-b", branch, path, strings.TrimPrefix(dst, "refs/")); err != nil {
			return nil, err
		}
		_, _ = git(ctx, path, "config", "branch."+branch+"."+prMarker, prMark(pr))
		trackHead(ctx, path, pr, rem, branch)
	}
	return map[string]any{"path": path, "created": true, "prompt": prompt}, nil
}

// gitWorktreeOn is the linked worktree of dir's repo on branch, or "".
func gitWorktreeOn(ctx context.Context, dir, branch string) string {
	out, err := git(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	for i, block := range strings.Split(out, "\n\n") {
		if i == 0 {
			continue // the main checkout is the repo itself, whatever it's on
		}
		var path, b string
		for _, l := range strings.Split(block, "\n") {
			if p, ok := strings.CutPrefix(l, "worktree "); ok {
				path = p
			}
			if r, ok := strings.CutPrefix(l, "branch "); ok {
				b = strings.TrimPrefix(r, "refs/heads/")
			}
		}
		if path != "" && b == branch {
			return path
		}
	}
	return ""
}

// removeAppWorktree removes a worktree after its PR merged, never forcing:
// uncommitted changes keep it where it is.
func removeAppWorktree(ctx context.Context, path string) error {
	if path == "" {
		return errors.New("which worktree?")
	}
	common, err := git(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	main := filepath.Dir(strings.TrimSpace(common))
	if filepath.Clean(main) == filepath.Clean(path) {
		return errors.New(path + " is the repo's own checkout, not a worktree")
	}
	_, err = git(ctx, main, "worktree", "remove", path)
	return err
}

// ── the demo ──────────────────────────────────────────────────────────────────

// appDemo is the demo with what was done to it kept between requests, each
// of which is its own run: a draft marked ready stays ready.
type appDemo struct{ src *demoSource }

type demoChange struct {
	Draft bool        `json:"draft"`
	State string      `json:"state"`
	Merge string      `json:"merge"`
	Auto  string      `json:"auto,omitempty"`
	Queue *queueEntry `json:"queue,omitempty"`
	Tree  bool        `json:"tree"`
}

func appDemoPath() string { return filepath.Join(stateDir(), "app-demo.json") }

func loadAppDemo() *appDemo {
	d := &appDemo{src: newDemoSource()}
	var saved map[string]demoChange
	if data, err := os.ReadFile(appDemoPath()); err == nil {
		_ = json.Unmarshal(data, &saved)
	}
	for _, p := range d.src.prs {
		c, ok := saved[p.key()]
		if !ok {
			continue
		}
		p.IsDraft, p.State, p.MergeStateStatus, p.MergeQueueEntry = c.Draft, c.State, c.Merge, c.Queue
		p.AutoMergeRequest = nil
		if c.Auto != "" {
			p.AutoMergeRequest = &struct {
				EnabledAt   string `json:"enabledAt"`
				MergeMethod string `json:"mergeMethod"`
			}{"now", c.Auto}
		}
		d.src.trees[p.key()] = c.Tree
	}
	return d
}

func (d *appDemo) save() error {
	saved := map[string]demoChange{}
	for _, p := range d.src.prs {
		c := demoChange{Draft: p.IsDraft, State: p.State, Merge: p.MergeStateStatus, Queue: p.MergeQueueEntry, Tree: d.src.trees[p.key()]}
		if p.AutoMergeRequest != nil {
			c.Auto = p.AutoMergeRequest.MergeMethod
		}
		saved[p.key()] = c
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(appDemoPath(), data, 0o600)
}

// ── jobs ──────────────────────────────────────────────────────────────────────

func jobPath(id string) string { return filepath.Join(stateDir(), "app-jobs", id+".json") }

var jobID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// startJob runs the request again in the background, as a job of its own,
// and says which: the mod asks after it with {"op": "job"}.
func startJob(req appRequest) (any, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	req.Job = hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(jobPath(req.Job)), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(jobPath(req.Job), []byte(`{"done":false}`), 0o600); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(req)
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{"app", string(data)}
	cmd := jobCmd(self, args)
	if cmd == nil {
		cmd = exec.Command(self, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if cmd.Path != self {
		// systemd-run: it returns once the job's unit is up.
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("couldn't start the job: %v %s", err, strings.TrimSpace(string(out)))
		}
	} else {
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		_ = cmd.Process.Release()
	}
	return map[string]any{"job": req.Job}, nil
}

func finishJob(id string, out any) error {
	if !jobID.MatchString(id) {
		return errors.New("not a job")
	}
	data, err := json.Marshal(map[string]any{"done": true, "result": out})
	if err != nil {
		return err
	}
	tmp := jobPath(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, jobPath(id))
}

// jobAnswer is how a job is getting on; once it's done, its answer, which
// is then forgotten.
func jobAnswer(id string) (any, error) {
	if !jobID.MatchString(id) {
		return nil, errors.New("not a job")
	}
	data, err := os.ReadFile(jobPath(id))
	if err != nil {
		return nil, errors.New("that job is gone")
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out["done"] == true {
		_ = os.Remove(jobPath(id))
	}
	return out, nil
}
