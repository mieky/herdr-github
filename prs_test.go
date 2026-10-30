package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBranchOfQuery(t *testing.T) {
	cfg := withDefaults(config{})
	cases := []struct {
		name string
		q    prQuery
		want spaceBranch
		err  string // part of the error; "" for none
	}{
		{"https remote", prQuery{Remote: "https://github.com/o/r.git", Branch: "feat"},
			spaceBranch{repo: repoRef{"github.com", "o", "r"}, head: branchHead{"feat", "o"}}, ""},
		{"ssh remote", prQuery{Remote: "git@github.com:o/r.git", Branch: "feat"},
			spaceBranch{repo: repoRef{"github.com", "o", "r"}, head: branchHead{"feat", "o"}}, ""},
		{"pushed under another name", prQuery{Remote: "https://github.com/o/r", Branch: "feat", Head: "refs/heads/feature-x"},
			spaceBranch{repo: repoRef{"github.com", "o", "r"}, head: branchHead{"feature-x", "o"}}, ""},
		{"a fork's branch", prQuery{Remote: "https://github.com/o/r", Branch: "patch-1", Head: "patch-1", Owner: "someone"},
			spaceBranch{repo: repoRef{"github.com", "o", "r"}, head: branchHead{"patch-1", "someone"}}, ""},
		{"still tracking main", prQuery{Remote: "https://github.com/o/r", Branch: "feat", Head: "main"},
			spaceBranch{repo: repoRef{"github.com", "o", "r"}, head: branchHead{"feat", "o"}}, ""},
		{"the default branch", prQuery{Remote: "https://github.com/o/r", Branch: "main"}, spaceBranch{}, "default branch"},
		{"no branch", prQuery{Remote: "https://github.com/o/r"}, spaceBranch{}, "no branch"},
		{"not a remote", prQuery{Remote: "/srv/repo.git", Branch: "feat"}, spaceBranch{}, "isn't a GitHub remote"},
		{"another host", prQuery{Remote: "https://ghe.example.com/o/r", Branch: "feat"}, spaceBranch{}, "isn't one of the GitHub hosts"},
	}
	for _, c := range cases {
		got, err := branchOfQuery(cfg, c.q)
		switch {
		case c.err == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: err = %v, want %q", c.name, err, c.err)
		case c.err == "" && got != c.want:
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestSummaryOf(t *testing.T) {
	auto := &struct {
		EnabledAt string `json:"enabledAt"`
	}{"x"}
	cases := []struct {
		s    *prStatus
		want *prSummary
	}{
		{nil, nil},
		{&prStatus{Number: 482, URL: "u", State: "OPEN", ReviewDecision: "APPROVED", Commits: rollupOf([]check{{Typename: "StatusContext", State: "SUCCESS"}})},
			&prSummary{Number: 482, URL: "u", State: "open", Checks: "✓", Review: "approved", Badge: "#482 ✓ approved"}},
		{&prStatus{Number: 3, State: "OPEN", IsDraft: true, ReviewDecision: "REVIEW_REQUIRED"},
			&prSummary{Number: 3, State: "draft", Review: "needs review", Badge: "#3 draft"}},
		{&prStatus{Number: 4, State: "OPEN", Mergeable: "CONFLICTING", AutoMergeRequest: auto},
			&prSummary{Number: 4, State: "open", Conflicts: true, Auto: true, Badge: "#4 conflicts auto"}},
		{&prStatus{Number: 5, State: "MERGED", Mergeable: "CONFLICTING", ReviewDecision: "APPROVED", AutoMergeRequest: auto},
			&prSummary{Number: 5, State: "merged", Badge: "#5 merged"}},
		{&prStatus{Number: 6, State: "CLOSED", Commits: rollupOf([]check{{Typename: "StatusContext", State: "FAILURE"}})},
			&prSummary{Number: 6, State: "closed", Badge: "#6 closed"}},
	}
	for _, c := range cases {
		got := summaryOf(c.s)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("summaryOf(%+v) = %+v, want %+v", c.s, got, c.want)
		}
	}
}

// stubBranchLookup answers branch lookups from prs, by branch name, and
// counts the calls; fail makes them fail.
func stubBranchLookup(t *testing.T, prs map[string]prStatus, fail *bool) *int {
	t.Helper()
	calls := 0
	old := branchLookupFn
	branchLookupFn = func(_ context.Context, _ repoRef, heads []branchHead) (map[branchHead]prStatus, error) {
		calls++
		if fail != nil && *fail {
			return nil, errors.New("offline")
		}
		out := map[branchHead]prStatus{}
		for _, h := range heads {
			if s, ok := prs[h.Branch]; ok {
				out[h] = s
			}
		}
		return out, nil
	}
	t.Cleanup(func() { branchLookupFn = old })
	return &calls
}

// setClock makes nowFn return *at, for a test that moves time on.
func setClock(t *testing.T, at *time.Time) {
	t.Helper()
	old := nowFn
	nowFn = func() time.Time { return *at }
	t.Cleanup(func() { nowFn = old })
}

func TestLookupPRs(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	cfg := withDefaults(config{})
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	setClock(t, &now)
	fail := false
	calls := stubBranchLookup(t, map[string]prStatus{
		"feat": {Number: 7, State: "OPEN", URL: "https://github.com/o/r/pull/7"},
		"old":  {Number: 2, State: "MERGED"},
	}, &fail)
	qs := []prQuery{
		{Remote: "https://github.com/o/r.git", Branch: "feat"},
		{Remote: "nonsense", Branch: "feat"},
		{Remote: "git@github.com:o/r.git", Branch: "old"},
		{Remote: "https://github.com/o/r", Branch: "none"},
		{Remote: "https://github.com/o/r", Branch: "main"},
	}
	check := func(step string, as []prAnswer, wantErr string) {
		t.Helper()
		if len(as) != len(qs) {
			t.Fatalf("%s: %d answers", step, len(as))
		}
		for i, a := range as {
			if a.Remote != qs[i].Remote || a.Branch != qs[i].Branch {
				t.Errorf("%s: answer %d is for %s %s", step, i, a.Remote, a.Branch)
			}
		}
		if as[0].PR == nil || as[0].PR.Badge != "#7" || as[0].Error != wantErr {
			t.Errorf("%s: feat = %+v %q", step, as[0].PR, as[0].Error)
		}
		if as[1].PR != nil || !strings.Contains(as[1].Error, "isn't a GitHub remote") {
			t.Errorf("%s: a bad entry = %+v", step, as[1])
		}
		if as[2].PR == nil || as[2].PR.State != "merged" {
			t.Errorf("%s: a merged PR is still the branch's: %+v", step, as[2].PR)
		}
		if as[3].PR != nil || as[3].Error != wantErr || as[4].PR != nil || as[4].Error != "" {
			t.Errorf("%s: no PR = %+v, %+v", step, as[3], as[4])
		}
	}

	check("first", lookupPRs(context.Background(), cfg, qs, false), "")
	if *calls != 1 {
		t.Fatalf("one repo, one query: %d", *calls)
	}
	now = now.Add(30 * time.Second)
	check("cached", lookupPRs(context.Background(), cfg, qs, false), "")
	if *calls != 1 {
		t.Fatalf("a fresh answer is reused: %d calls", *calls)
	}
	lookupPRs(context.Background(), cfg, qs, true)
	if *calls != 2 {
		t.Fatalf("--force asks again: %d calls", *calls)
	}

	// Offline: what was known is kept, with why it's not newer, and GitHub
	// gets a rest before being asked again.
	fail = true
	now = now.Add(2 * time.Minute)
	as := lookupPRs(context.Background(), cfg, qs, false)
	check("offline", as, "offline")
	now = now.Add(10 * time.Second)
	as = lookupPRs(context.Background(), cfg, qs, false)
	if *calls != 3 || !strings.Contains(as[0].Error, "didn't answer") {
		t.Fatalf("after a failure, a rest: %d calls, %q", *calls, as[0].Error)
	}
}

func TestLookupPRsUsesTicksAnswers(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	calls := stubBranchLookup(t, nil, nil)
	sb, err := branchOfQuery(withDefaults(config{}), prQuery{Remote: "https://github.com/o/r", Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	st := readLabelState()
	st.Branches[sb.cacheKey()] = branchEntry{Fetched: nowFn(), PR: &prStatus{Number: 9, State: "OPEN"}}
	if err := writeLabelState(st); err != nil {
		t.Fatal(err)
	}
	as := lookupPRs(context.Background(), withDefaults(config{}), []prQuery{{Remote: "https://github.com/o/r", Branch: "feat"}}, false)
	if *calls != 0 || as[0].PR == nil || as[0].PR.Number != 9 {
		t.Fatalf("%d calls, %+v", *calls, as[0])
	}
}

func TestRunPRs(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	stubBranchLookup(t, map[string]prStatus{"feat": {Number: 1, State: "OPEN"}}, nil)
	cases := []struct {
		args []string
		want string // the output, or part of the error
		err  bool
	}{
		{[]string{`[{"remote":"https://github.com/o/r","branch":"feat"}]`},
			`[{"remote":"https://github.com/o/r","branch":"feat","pr":{"number":1,"url":"","state":"open","checks":"","review":"","conflicts":false,"auto":false,"badge":"#1"}}]` + "\n", false},
		{[]string{"--force", `[{"remote":"x","branch":"feat"}]`},
			`[{"remote":"x","branch":"feat","pr":null,"error":"\"x\" isn't a GitHub remote URL"}]` + "\n", false},
		{[]string{`[]`}, "[]\n", false},
		{nil, "needs a JSON array", true},
		{[]string{`{"remote":"x"}`}, "prs:", true},
		{[]string{`[]`, `[]`}, "one JSON array", true},
	}
	for _, c := range cases {
		var out bytes.Buffer
		err := runPRs(context.Background(), withDefaults(config{}), c.args, &out)
		switch {
		case c.err && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%q: err = %v, want %q", c.args, err, c.want)
		case !c.err && err != nil:
			t.Errorf("%q: %v", c.args, err)
		case !c.err && out.String() != c.want:
			t.Errorf("%q:\n got %s\nwant %s", c.args, out.String(), c.want)
		}
	}
	// The answer is JSON a program can read back.
	var out bytes.Buffer
	_ = runPRs(context.Background(), withDefaults(config{}), []string{`[{"remote":"https://github.com/o/r","branch":"feat"}]`}, &out)
	var back []prAnswer
	if err := json.Unmarshal(out.Bytes(), &back); err != nil || len(back) != 1 || back[0].PR.Number != 1 {
		t.Fatalf("%v %+v", err, back)
	}
}
