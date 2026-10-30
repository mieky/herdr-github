package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// `prs` answers, as JSON, which pull request each of a list of branches has,
// for a program that can't use the sidebar labels: the Herdr app, say, which
// shows agents from several machines and doesn't run plugin hooks. A branch
// is named by its repo's remote URL and its name, not by a checkout, so the
// one machine where gh is signed in can answer for agents on any of them.
//
// It picks the PR tick would (the same query, the same choice among a
// branch's PRs), and keeps answers in prs.json beside tick's labels.json: a
// caller that asks every few seconds reaches GitHub once a minute at most.

// prQuery is one branch to look up.
type prQuery struct {
	Remote string `json:"remote"` // the repo's git remote URL, https or ssh
	Branch string `json:"branch"` // the local branch
	Head   string `json:"head"`   // what it pushes to, when it has an upstream
	Owner  string `json:"owner"`  // whose fork it pushes to, when known
}

// prAnswer is what's said about one prQuery, in the same order.
type prAnswer struct {
	Remote string     `json:"remote"`
	Branch string     `json:"branch"`
	PR     *prSummary `json:"pr"`
	Error  string     `json:"error,omitempty"`
}

// prSummary is a branch's PR, in the words of the sidebar tokens.
type prSummary struct {
	Number    int    `json:"number"`
	URL       string `json:"url"`
	State     string `json:"state"`  // open, draft, merged or closed
	Checks    string `json:"checks"` // ✓, ✗, ● or empty
	Review    string `json:"review"` // approved, changes requested, needs review or empty
	Conflicts bool   `json:"conflicts"`
	Auto      bool   `json:"auto"`
	Badge     string `json:"badge"`
}

// summaryOf turns a PR's status into its summary, from the same tokens the
// sidebar shows; nil for no PR.
func summaryOf(s *prStatus) *prSummary {
	t := badgeTokens(s)
	if t == nil {
		return nil
	}
	open := s.State == "OPEN"
	return &prSummary{
		Number:    s.Number,
		URL:       s.URL,
		State:     t["pr_state"],
		Checks:    t["pr_checks"],
		Review:    t["pr_review"],
		Conflicts: open && s.Mergeable == "CONFLICTING",
		Auto:      open && s.AutoMergeRequest != nil,
		Badge:     t["pr_badge"],
	}
}

// errNoPRBranch marks a branch that can't have a PR of its own: a default
// branch, which is where PRs go, not where they come from.
var errNoPRBranch = errors.New("a default branch")

// branchOfQuery works out the repo and head branch to ask GitHub about, as
// spaceBranchOf does for a checkout. With no checkout to ask, main and master
// are taken for the default branch.
func branchOfQuery(cfg config, q prQuery) (spaceBranch, error) {
	repo, ok := parseRemote(q.Remote)
	if !ok {
		return spaceBranch{}, fmt.Errorf("%q isn't a GitHub remote URL", q.Remote)
	}
	if !cfg.knownHost(repo.Host) {
		return spaceBranch{}, fmt.Errorf("%s isn't one of the GitHub hosts in the config (%s)", repo.Host, strings.Join(cfg.Hosts, ", "))
	}
	branch := strings.TrimSpace(q.Branch)
	if branch == "" {
		return spaceBranch{}, errors.New("no branch")
	}
	isDefault := func(b string) bool { return b == "main" || b == "master" }
	if isDefault(branch) {
		return spaceBranch{}, errNoPRBranch
	}
	head := branchHead{Branch: branch, Owner: strings.TrimSpace(q.Owner)}
	if h := strings.TrimPrefix(strings.TrimSpace(q.Head), "refs/heads/"); h != "" && !isDefault(h) {
		// A branch made from origin/main tracks main until it's pushed: its
		// PR (if any) is still under its own name, not main's.
		head.Branch = h
	}
	if head.Owner == "" {
		head.Owner = repo.Owner
	}
	return spaceBranch{repo: repo, head: head}, nil
}

// lookupState is prs.json: what GitHub last said per branch, keyed as tick's
// labels.json keys it, and when a repo last failed to answer.
type lookupState struct {
	Branches map[string]branchEntry `json:"branches"`
	Failed   map[string]time.Time   `json:"failed"`
}

// lookupKeep is how long an answer nobody has asked for again is kept.
const lookupKeep = 24 * time.Hour

func lookupStatePath() string { return filepath.Join(stateDir(), "prs.json") }

func readLookupState() lookupState {
	var s lookupState
	if data, err := os.ReadFile(lookupStatePath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.Branches == nil {
		s.Branches = map[string]branchEntry{}
	}
	if s.Failed == nil {
		s.Failed = map[string]time.Time{}
	}
	return s
}

// writeLookupState saves s, without what's too old to be of use. Two callers
// at once can lose each other's answers, but never leave a torn file.
func writeLookupState(s lookupState, now time.Time) error {
	for k, e := range s.Branches {
		if now.Sub(e.Fetched) > lookupKeep {
			delete(s.Branches, k)
		}
	}
	for k, t := range s.Failed {
		if now.Sub(t) > lookupKeep {
			delete(s.Failed, k)
		}
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir(), "prs-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), lookupStatePath())
}

// lookupPRs answers each query, in order. An answer younger than
// label_refresh_seconds (tick's, or one of ours) is used as it is, unless
// force; the rest are asked of GitHub, a repo at a time. One query that
// can't be answered says so in its own answer and spoils none of the others.
func lookupPRs(ctx context.Context, cfg config, qs []prQuery, force bool) []prAnswer {
	now := nowFn()
	fresh := time.Duration(cfg.LabelRefreshSeconds) * time.Second
	st := readLookupState()
	ticked := readLabelState().Branches

	// The newest answer we have for a branch, from either cache.
	known := func(key string) (branchEntry, bool) {
		e, ok := st.Branches[key]
		if t, tok := ticked[key]; tok && (!ok || t.Fetched.After(e.Fetched)) {
			return t, true
		}
		return e, ok
	}

	out := make([]prAnswer, len(qs))
	sbs := make([]spaceBranch, len(qs))
	errs := make([]error, len(qs))
	due := map[string][]spaceBranch{} // repo key → branches to ask about
	asked := map[string]bool{}        // branch key → it was due
	for i, q := range qs {
		out[i] = prAnswer{Remote: q.Remote, Branch: q.Branch}
		sbs[i], errs[i] = branchOfQuery(cfg, q)
		if errs[i] != nil {
			continue
		}
		e, ok := known(sbs[i].cacheKey())
		if !force && ok && now.Sub(e.Fetched) < fresh {
			continue
		}
		asked[sbs[i].cacheKey()] = true
		due[sbs[i].repo.key()] = append(due[sbs[i].repo.key()], sbs[i])
	}

	failed := map[string]error{} // repo key → why GitHub didn't answer
	for key, list := range due {
		// After a failure (offline, rate limited), give GitHub the same rest
		// as a fresh answer gets.
		if t, ok := st.Failed[key]; ok && !force && now.Sub(t) < fresh {
			failed[key] = fmt.Errorf("GitHub didn't answer for %s at %s; asking again after %s", list[0].repo, t.Format("15:04:05"), t.Add(fresh).Format("15:04:05"))
			continue
		}
		repo := list[0].repo
		seen := map[branchHead]bool{}
		var heads []branchHead
		for _, sb := range list {
			if !seen[sb.head] {
				seen[sb.head] = true
				heads = append(heads, sb.head)
			}
		}
		for i := 0; i < len(heads); i += branchesPerQ {
			batch := heads[i:min(len(heads), i+branchesPerQ)]
			found, err := branchLookupFn(ctx, repo, batch)
			if err != nil {
				failed[key] = err
				st.Failed[key] = now
				continue
			}
			for _, h := range batch {
				e := branchEntry{Fetched: now}
				if s, ok := found[h]; ok {
					s := s
					e.PR = &s
				}
				st.Branches[spaceBranch{repo: repo, head: h}.cacheKey()] = e
			}
		}
		if failed[key] == nil {
			delete(st.Failed, key)
		}
	}
	if len(due) > 0 {
		if err := writeLookupState(st, now); err != nil {
			fmt.Fprintln(os.Stderr, "herdr-github: prs:", err)
		}
	}

	for i := range qs {
		switch {
		case errors.Is(errs[i], errNoPRBranch):
			continue // no PR, and nothing wrong
		case errs[i] != nil:
			out[i].Error = errs[i].Error()
			continue
		}
		e, ok := known(sbs[i].cacheKey())
		if ok {
			out[i].PR = summaryOf(e.PR)
		}
		// It was due, and GitHub didn't answer: say so, along with what was
		// known before, if anything.
		if err := failed[sbs[i].repo.key()]; err != nil && asked[sbs[i].cacheKey()] && (!ok || e.Fetched.Before(now)) {
			out[i].Error = err.Error()
		}
	}
	return out
}

// runPRs is `prs [--force] JSON`: the queries as a JSON array, the answers
// as one on stdout.
func runPRs(ctx context.Context, cfg config, args []string, stdout io.Writer) error {
	force, raw := false, ""
	for _, a := range args {
		switch {
		case a == "--force":
			force = true
		case raw == "":
			raw = a
		default:
			return errors.New("prs takes one JSON array (and --force)")
		}
	}
	if strings.TrimSpace(raw) == "" {
		return errors.New(`prs needs a JSON array: [{"remote": …, "branch": …, "head": …, "owner": …}]`)
	}
	var qs []prQuery
	if err := json.Unmarshal([]byte(raw), &qs); err != nil {
		return fmt.Errorf("prs: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, tickTimeLimit)
	defer cancel()
	data, err := json.Marshal(lookupPRs(ctx, cfg, qs, force))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(data))
	return err
}
