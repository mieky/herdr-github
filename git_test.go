package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseRemote(t *testing.T) {
	cases := map[string]string{
		"https://github.com/o/r.git":            "github.com/o/r",
		"https://github.com/o/r":                "github.com/o/r",
		"https://token@github.com/o/r.git/":     "github.com/o/r",
		"git@github.com:o/r.git":                "github.com/o/r",
		"ssh://git@github.com/o/r.git":          "github.com/o/r",
		"ssh://git@GHE.example.com:2222/O/R":    "ghe.example.com/O/R",
		"git://github.com/o/r.git":              "github.com/o/r",
		"org-123@ssh.github.example.com:o/r":    "ssh.github.example.com/o/r",
		"/local/path/repo.git":                  "",
		"https://github.com/o":                  "",
		"https://gitlab.com/group/sub/repo.git": "",
	}
	for in, want := range cases {
		r, ok := parseRemote(in)
		got := ""
		if ok {
			got = r.Host + "/" + r.Owner + "/" + r.Name
		}
		if got != want {
			t.Errorf("parseRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── a real repo, a real remote ────────────────────────────────────────────────

type fixture struct {
	root, origin, clone string
	featSHA, forkSHA    string
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitFixture makes a bare "GitHub" repo o/r with main, a branch feat, and a
// fork's PR #5 at refs/pull/5/head; and a clone of it whose origin is spelled
// https://github.com/o/r.git (rewritten to the bare repo by insteadOf).
func gitFixture(t *testing.T) fixture {
	t.Helper()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.com")
	}
	remoteCache = sync.Map{}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := fixture{root: root, origin: filepath.Join(root, "origin.git"), clone: filepath.Join(root, "clone")}
	work := filepath.Join(root, "work")
	sh(t, root, "git", "init", "-q", "--bare", "-b", "main", f.origin)
	sh(t, root, "git", "init", "-q", "-b", "main", work)
	sh(t, work, "git", "commit", "-q", "--allow-empty", "-m", "init")
	sh(t, work, "git", "remote", "add", "origin", f.origin)
	sh(t, work, "git", "push", "-q", "origin", "main")
	sh(t, work, "git", "switch", "-q", "-c", "feat")
	sh(t, work, "git", "commit", "-q", "--allow-empty", "-m", "feat")
	f.featSHA = sh(t, work, "git", "rev-parse", "HEAD")
	sh(t, work, "git", "push", "-q", "origin", "feat")
	sh(t, work, "git", "switch", "-q", "-c", "forkwork", "main")
	sh(t, work, "git", "commit", "-q", "--allow-empty", "-m", "from a fork")
	f.forkSHA = sh(t, work, "git", "rev-parse", "HEAD")
	sh(t, work, "git", "push", "-q", "origin", "forkwork:refs/pull/5/head")

	sh(t, root, "git", "clone", "-q", f.origin, f.clone)
	sh(t, f.clone, "git", "config", "url."+f.origin+".insteadOf", "https://github.com/o/r.git")
	sh(t, f.clone, "git", "remote", "set-url", "origin", "https://github.com/o/r.git")
	return f
}

// fakeHerdr stands in for herdr's socket: worktrees through git, spaces from
// the list given, and every metadata report recorded.
type fakeHerdr struct {
	mu         sync.Mutex
	workspaces []workspaceInfo
	panes      []paneInfo
	reports    []map[string]any
	created    int
}

func (h *fakeHerdr) install(t *testing.T) {
	old := herdrCallFn
	t.Cleanup(func() { herdrCallFn = old })
	herdrCallFn = h.call
}

func (h *fakeHerdr) call(method string, params any, out any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, _ := params.(map[string]any)
	var res any
	switch method {
	case "worktree.list":
		out, err := exec.Command("git", "-C", p["cwd"].(string), "worktree", "list", "--porcelain").Output()
		if err != nil {
			return err
		}
		var wts []map[string]any
		for i, block := range strings.Split(strings.TrimSpace(string(out)), "\n\n") {
			w := map[string]any{"is_linked_worktree": i > 0} // git lists the main checkout first
			for _, line := range strings.Split(block, "\n") {
				k, v, _ := strings.Cut(line, " ")
				switch k {
				case "worktree":
					w["path"] = v
				case "branch":
					w["branch"] = v
				}
			}
			wts = append(wts, w)
		}
		res = map[string]any{"worktrees": wts}
	case "worktree.create":
		cwd, branch := p["cwd"].(string), p["branch"].(string)
		path := filepath.Join(filepath.Dir(cwd), "wt-"+strings.ReplaceAll(branch, "/", "-"))
		args := []string{"-C", cwd, "worktree", "add", "-q"}
		if base, ok := p["base"].(string); ok {
			args = append(args, "-b", branch, path, base)
		} else {
			args = append(args, path, branch)
		}
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		h.created++
		res = map[string]any{"workspace": map[string]any{"workspace_id": "w9"}, "worktree": map[string]any{"path": path, "branch": branch}, "root_pane": map[string]any{"pane_id": "p9"}}
	case "worktree.open":
		res = map[string]any{"workspace": map[string]any{"workspace_id": "w8"}}
	case "workspace.list":
		res = map[string]any{"workspaces": h.workspaces}
	case "pane.list":
		res = map[string]any{"panes": h.panes}
	case "workspace.report_metadata", "pane.report_metadata":
		c := map[string]any{"method": method}
		for k, v := range p {
			c[k] = v
		}
		h.reports = append(h.reports, c)
	default:
		return errors.New("fake herdr: " + method)
	}
	if out != nil {
		data, _ := json.Marshal(res)
		return json.Unmarshal(data, out)
	}
	return nil
}

func (h *fakeHerdr) takeReports() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.reports
	h.reports = nil
	return r
}

func samePR(host string) pullRequest {
	pr := pullRequest{Host: host, Number: 12, Title: "Feat", HeadRefName: "feat", BaseRefName: "main"}
	pr.Repository.NameWithOwner = "o/r"
	return pr
}

func forkPR() pullRequest {
	pr := pullRequest{Host: "github.com", Number: 5, Title: "Fix from a fork", HeadRefName: "fix", IsCrossRepository: true,
		HeadRepositoryOwner: &actor{"alice"}}
	pr.Repository.NameWithOwner = "o/r"
	pr.HeadRepository = &struct {
		NameWithOwner string `json:"nameWithOwner"`
	}{"alice/r"}
	return pr
}

func TestFindCheckout(t *testing.T) {
	f := gitFixture(t)
	h := &fakeHerdr{}
	h.install(t)
	old := listWorkspacesFn
	t.Cleanup(func() { listWorkspacesFn = old })
	listWorkspacesFn = listWorkspaces
	ctx := context.Background()
	r := repoRef{"github.com", "o", "r"}
	cfg := withDefaults(config{CloneRoot: filepath.Join(f.root, "code")})

	// Opened from inside the clone (a subdirectory, even).
	sub := filepath.Join(f.clone, "sub")
	_ = os.MkdirAll(sub, 0o755)
	if dir, err := findCheckout(ctx, cfg, r, sub); err != nil || dir != f.clone {
		t.Fatalf("from inside: %q %v", dir, err)
	}
	// Opened from elsewhere, but a herdr space has it.
	h.workspaces = []workspaceInfo{{WorkspaceID: "w1", Worktree: &struct {
		CheckoutPath     string `json:"checkout_path"`
		RepoRoot         string `json:"repo_root"`
		IsLinkedWorktree bool   `json:"is_linked_worktree"`
	}{CheckoutPath: f.clone, RepoRoot: f.clone}}}
	if dir, err := findCheckout(ctx, cfg, r, ""); err != nil || dir != f.clone {
		t.Fatalf("from herdr's spaces: %q %v", dir, err)
	}
	// Case doesn't matter in a GitHub name.
	if dir, _ := findCheckout(ctx, cfg, repoRef{"github.com", "O", "R"}, ""); dir != f.clone {
		t.Fatal("owner/repo should match case-insensitively")
	}
	// The config map wins.
	cfg.Repos = map[string]string{"o/r": "/mapped"}
	if dir, _ := findCheckout(ctx, cfg, r, f.clone); dir != "/mapped" {
		t.Fatalf("config map: %q", dir)
	}
	// Nowhere: says where it would clone.
	h.workspaces = nil
	cfg.Repos = nil
	_, err := findCheckout(ctx, cfg, repoRef{"github.com", "o", "other"}, "")
	var nc *noCheckoutError
	if !errors.As(err, &nc) || nc.Dest != filepath.Join(f.root, "code", "o", "other") {
		t.Fatalf("no checkout: %v", err)
	}
}

func TestOpenWorktreeForSameRepoPR(t *testing.T) {
	f := gitFixture(t)
	h := &fakeHerdr{}
	h.install(t)
	ctx := context.Background()
	pr := samePR("github.com")

	res, created, err := openWorktree(ctx, withDefaults(config{}), pr, f.clone)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	path := res.Worktree.Path
	if got := sh(t, path, "git", "rev-parse", "HEAD"); got != f.featSHA {
		t.Fatal("worktree isn't at the PR's head")
	}
	if got := sh(t, path, "git", "rev-parse", "--abbrev-ref", "feat@{upstream}"); got != "origin/feat" {
		t.Fatalf("upstream = %q", got)
	}
	// Again: it's opened, not made twice.
	if _, created, err := openWorktree(ctx, withDefaults(config{}), pr, f.clone); err != nil || created || h.created != 1 {
		t.Fatalf("second open: created=%v err=%v", created, err)
	}
}

func TestOpenWorktreeForForkPR(t *testing.T) {
	f := gitFixture(t)
	h := &fakeHerdr{}
	h.install(t)
	res, created, err := openWorktree(context.Background(), withDefaults(config{}), forkPR(), f.clone)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	path := res.Worktree.Path
	if got := sh(t, path, "git", "rev-parse", "HEAD"); got != f.forkSHA {
		t.Fatal("worktree isn't at refs/pull/5/head")
	}
	if got := sh(t, path, "git", "branch", "--show-current"); got != "alice/fix" {
		t.Fatalf("branch = %q", got)
	}
	if got := sh(t, path, "git", "config", "branch.alice/fix.remote"); got != "https://github.com/alice/r.git" {
		t.Fatalf("pushes to %q", got)
	}
	if got := sh(t, path, "git", "config", "branch.alice/fix.merge"); got != "refs/heads/fix" {
		t.Fatalf("merge = %q", got)
	}
}

// A local branch of the PR's name may hold your commits: it's checked out
// as it is, never reset to the remote.
func TestOpenWorktreeKeepsAnExistingBranch(t *testing.T) {
	f := gitFixture(t)
	h := &fakeHerdr{}
	h.install(t)
	sh(t, f.clone, "git", "branch", "feat", "main")
	mainSHA := sh(t, f.clone, "git", "rev-parse", "main")
	res, _, err := openWorktree(context.Background(), withDefaults(config{}), samePR("github.com"), f.clone)
	if err != nil {
		t.Fatal(err)
	}
	if got := sh(t, res.Worktree.Path, "git", "rev-parse", "HEAD"); got != mainSHA {
		t.Fatal("an existing local branch was moved")
	}
}

func TestForkRemoteURLFollowsTheBaseRemote(t *testing.T) {
	for base, want := range map[string]string{
		"https://github.com/o/r.git":   "https://github.com/alice/r.git",
		"git@github.com:o/r.git":       "git@github.com:alice/r.git",
		"ssh://git@github.com/o/r.git": "ssh://git@github.com/alice/r.git",
	} {
		if got := forkRemoteURL(base, "github.com", "alice/r"); got != want {
			t.Errorf("%s → %s, want %s", base, got, want)
		}
	}
}

func TestExpandPrompt(t *testing.T) {
	pr := samePR("github.com")
	pr.URL = "https://github.com/o/r/pull/12"
	got := expandPrompt("{repo}#{number} {url} on {branch}: {title}", pr)
	if got != "o/r#12 https://github.com/o/r/pull/12 on feat: Feat" {
		t.Fatal(got)
	}
}

func TestRemoveWorktreeWithoutASpaceUsesGit(t *testing.T) {
	f := gitFixture(t)
	h := &fakeHerdr{}
	h.install(t)
	res, _, err := openWorktree(context.Background(), withDefaults(config{}), samePR("github.com"), f.clone)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeWorktree(context.Background(), samePR("github.com"), f.clone); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(res.Worktree.Path); !os.IsNotExist(err) {
		t.Fatal("worktree still there")
	}
	if err := removeWorktree(context.Background(), samePR("github.com"), f.clone); err == nil {
		t.Fatal("removing it twice should say it's gone")
	}
}

// ── labels ────────────────────────────────────────────────────────────────────

func wsAt(id, checkout, root string, tokens map[string]string) workspaceInfo {
	w := workspaceInfo{WorkspaceID: id, Tokens: tokens}
	w.Worktree = &struct {
		CheckoutPath     string `json:"checkout_path"`
		RepoRoot         string `json:"repo_root"`
		IsLinkedWorktree bool   `json:"is_linked_worktree"`
	}{CheckoutPath: checkout, RepoRoot: root}
	return w
}

func reportFor(reports []map[string]any, key, id string) map[string]any {
	for _, r := range reports {
		if r[key] == id {
			return r
		}
	}
	return nil
}

func TestTickLabelsSpacesAndAgents(t *testing.T) {
	f := gitFixture(t)
	h := &fakeHerdr{}
	h.install(t)
	ctx := context.Background()
	feat, _, err := openWorktree(ctx, withDefaults(config{}), samePR("github.com"), f.clone)
	if err != nil {
		t.Fatal(err)
	}
	fork, _, err := openWorktree(ctx, withDefaults(config{}), forkPR(), f.clone)
	if err != nil {
		t.Fatal(err)
	}
	old := listWorkspacesFn
	listWorkspacesFn = listWorkspaces
	t.Cleanup(func() { listWorkspacesFn = old })
	h.workspaces = []workspaceInfo{
		wsAt("w1", feat.Worktree.Path, f.clone, nil),
		wsAt("w2", f.clone, f.clone, nil), // on main: no PR to look for
		wsAt("w3", fork.Worktree.Path, f.clone, nil),
		{WorkspaceID: "w4"}, // not a checkout
	}
	h.panes = []paneInfo{{PaneID: "p1", WorkspaceID: "w1", Agent: "claude"}, {PaneID: "p2", WorkspaceID: "w1"}}

	var asked [][]branchHead
	statuses := map[branchHead]prStatus{
		{"feat", "o"}:    {Number: 12, State: "OPEN", ReviewDecision: "APPROVED", Commits: rollupOf([]check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"}})},
		{"fix", "alice"}: {Number: 5, State: "OPEN", IsDraft: true},
	}
	oldLookup := branchLookupFn
	t.Cleanup(func() { branchLookupFn = oldLookup })
	branchLookupFn = func(_ context.Context, r repoRef, heads []branchHead) (map[branchHead]prStatus, error) {
		if r.key() != "github.com/o/r" {
			t.Errorf("asked about %v", r)
		}
		asked = append(asked, heads)
		out := map[branchHead]prStatus{}
		for _, hd := range heads {
			if s, ok := statuses[hd]; ok {
				out[hd] = s
			}
		}
		return out, nil
	}
	start := nowFn()
	oldNow := nowFn
	t.Cleanup(func() { nowFn = oldNow })
	at := func(d time.Duration) { nowFn = func() time.Time { return start.Add(d) } }
	cfg := withDefaults(config{})

	if err := tick(ctx, cfg, false); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || len(asked[0]) != 2 {
		t.Fatalf("one query for both branches, got %v", asked)
	}
	reports := h.takeReports()
	w1 := reportFor(reports, "workspace_id", "w1")
	if w1 == nil || w1["tokens"].(map[string]any)["pr_badge"] != "#12 ✓ approved" || w1["tokens"].(map[string]any)["pr_state"] != "open" {
		t.Fatalf("w1: %v", w1)
	}
	if w3 := reportFor(reports, "workspace_id", "w3"); w3 == nil || w3["tokens"].(map[string]any)["pr_badge"] != "#5 draft" {
		t.Fatalf("w3 (fork): %v", w3)
	}
	if reportFor(reports, "workspace_id", "w2") != nil || reportFor(reports, "workspace_id", "w4") != nil {
		t.Fatal("spaces with no PR and no tokens should be left alone")
	}
	if p1 := reportFor(reports, "pane_id", "p1"); p1 == nil || p1["tokens"].(map[string]any)["pr"] != "#12" || p1["tokens"].(map[string]any)["prs"] != "#12 ✓ approved" {
		t.Fatalf("agent pane: %v", p1)
	}
	if reportFor(reports, "pane_id", "p2") != nil {
		t.Fatal("a shell pane got a label")
	}

	// herdr now shows the tokens; within the cooldown nothing happens at all.
	h.workspaces[0].Tokens = map[string]string{"pr": "#12", "pr_badge": "#12 ✓ approved", "pr_state": "open", "pr_checks": "✓", "pr_review": "approved", "prs": "#12 ✓ approved"}
	at(time.Second)
	_ = tick(ctx, cfg, false)
	if len(asked) != 1 || len(h.takeReports()) != 0 {
		t.Fatal("a tick in the cooldown should do nothing")
	}
	// Later, but still fresh: no GitHub, and no re-report of the same tokens.
	at(10 * time.Second)
	_ = tick(ctx, cfg, false)
	if len(asked) != 1 {
		t.Fatal("a fresh answer was asked for again")
	}
	if r := reportFor(h.takeReports(), "workspace_id", "w1"); r != nil {
		t.Fatalf("unchanged tokens re-sent: %v", r)
	}
	// Stale: asked again. The PR is gone now, so w1's tokens are cleared.
	delete(statuses, branchHead{"feat", "o"})
	at(2 * time.Minute)
	_ = tick(ctx, cfg, false)
	if len(asked) != 2 {
		t.Fatal("a stale answer wasn't refreshed")
	}
	w1 = reportFor(h.takeReports(), "workspace_id", "w1")
	if w1 == nil {
		t.Fatal("w1's tokens weren't cleared")
	}
	for _, n := range tokenNames {
		if v, ok := w1["tokens"].(map[string]any)[n]; !ok || v != nil {
			t.Fatalf("clearing should null every token, %s = %v", n, v)
		}
	}
}

func TestTickKeepsLabelsWhenGitHubFails(t *testing.T) {
	f := gitFixture(t)
	h := &fakeHerdr{}
	h.install(t)
	feat, _, _ := openWorktree(context.Background(), withDefaults(config{}), samePR("github.com"), f.clone)
	old := listWorkspacesFn
	listWorkspacesFn = listWorkspaces
	t.Cleanup(func() { listWorkspacesFn = old })
	h.workspaces = []workspaceInfo{wsAt("w1", feat.Worktree.Path, f.clone, map[string]string{"pr": "#12"})}
	oldLookup := branchLookupFn
	t.Cleanup(func() { branchLookupFn = oldLookup })
	branchLookupFn = func(context.Context, repoRef, []branchHead) (map[branchHead]prStatus, error) {
		return nil, &signedOutError{"github.com"}
	}
	err := tick(context.Background(), withDefaults(config{}), true)
	if !errors.Is(err, errSignedOut) || !isQuietTickErr(err) {
		t.Fatalf("err = %v", err)
	}
	for _, r := range h.takeReports() {
		t.Fatalf("labels were touched while GitHub was unreachable: %v", r)
	}
}

func TestTickClearsEverythingWhenLabelsAreOff(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	h := &fakeHerdr{workspaces: []workspaceInfo{{WorkspaceID: "w1", Tokens: map[string]string{"pr": "#1"}}, {WorkspaceID: "w2"}}}
	h.install(t)
	old := listWorkspacesFn
	listWorkspacesFn = listWorkspaces
	t.Cleanup(func() { listWorkspacesFn = old })
	off := false
	if err := tick(context.Background(), withDefaults(config{Labels: &off}), true); err != nil {
		t.Fatal(err)
	}
	reports := h.takeReports()
	if len(reports) != 1 || reports[0]["workspace_id"] != "w1" {
		t.Fatalf("reports: %v", reports)
	}
}

func TestBadgeTokens(t *testing.T) {
	cases := []struct {
		s    prStatus
		want string
	}{
		{prStatus{Number: 1, State: "MERGED"}, "#1 merged"},
		{prStatus{Number: 2, State: "CLOSED"}, "#2 closed"},
		{prStatus{Number: 3, State: "OPEN", IsDraft: true}, "#3 draft"},
		{prStatus{Number: 4, State: "OPEN", Mergeable: "CONFLICTING", ReviewDecision: "APPROVED"}, "#4 conflicts"},
		{prStatus{Number: 5, State: "OPEN", ReviewDecision: "CHANGES_REQUESTED"}, "#5 changes"},
		{prStatus{Number: 6, State: "OPEN", Commits: rollupOf([]check{{Typename: "StatusContext", State: "PENDING"}}),
			AutoMergeRequest: &struct {
				EnabledAt string `json:"enabledAt"`
			}{"x"}}, "#6 ● auto"},
		{prStatus{Number: 7, State: "OPEN"}, "#7"},
		{prStatus{Number: 8, State: "OPEN", ReviewDecision: "APPROVED", Commits: rollupOf([]check{{Typename: "StatusContext", State: "SUCCESS"}}),
			MergeQueueEntry: &struct {
				Position int `json:"position"`
			}{2}}, "#8 ✓ approved queued"},
	}
	for _, c := range cases {
		if got := badgeTokens(&c.s)["pr_badge"]; got != c.want {
			t.Errorf("badge = %q, want %q", got, c.want)
		}
	}
	if badgeTokens(nil) != nil {
		t.Fatal("no PR: no tokens")
	}
	merged := badgeTokens(&prStatus{Number: 1, State: "MERGED", ReviewDecision: "APPROVED"})
	if _, ok := merged["pr_review"]; ok {
		t.Fatal("a merged PR's review state is noise")
	}
}
