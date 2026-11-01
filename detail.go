package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	screenList screen = iota
	screenPR
)

type detailMsg struct {
	key    string
	detail *prDetail
	err    error
	gen    int
}

// prChangedMsg ends a change made from the PR screen: draft, merge, clean-up.
type prChangedMsg struct {
	key      string
	flash    string
	err      error
	after    *menu // what to offer next (after a merge: clean up)
	treeGone bool  // its worktree was removed
}

// ── opening ───────────────────────────────────────────────────────────────────

func (m model) openPR(pr pullRequest) (tea.Model, tea.Cmd) {
	m.screen, m.cur, m.curDetail, m.err, m.flash, m.scroll = screenPR, &pr, nil, "", "", 0
	return m, m.loadDetail()
}

func (m model) loadDetail() tea.Cmd {
	pr, client, ctx, gen := *m.cur, m.client, m.ctx, m.gen
	return func() tea.Msg {
		d, err := client.detail(ctx, pr)
		return detailMsg{pr.key(), d, err, gen}
	}
}

// ── messages ──────────────────────────────────────────────────────────────────

func (m model) updateDetail(msg tea.Msg) (model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case detailMsg:
		if msg.gen != m.gen || m.cur == nil || m.cur.key() != msg.key {
			return m, nil, true
		}
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.curDetail = msg.detail
		// The detail is newer than the row it was opened from.
		pr := msg.detail.pullRequest
		pr.Host = m.cur.Host
		*m.cur = pr
		m.replaceRow(pr)
		return m, nil, true

	case prChangedMsg:
		m.mode = modeList
		if msg.treeGone {
			m.worktrees[msg.key] = false
		}
		if msg.err != nil {
			m.err = msg.err.Error()
			if m.cur != nil && m.cur.key() == msg.key {
				return m, m.loadDetail(), true
			}
			return m, nil, true
		}
		m.err, m.flash = "", msg.flash
		if m.cur != nil && m.cur.key() == msg.key {
			m.curDetail = nil // what's shown is out of date until the reload lands
		}
		spawnTick()
		// The PR changed on GitHub: reload it, and the lists it's in.
		m, reload := m.reloadLists()
		cmds := []tea.Cmd{reload}
		if m.cur != nil && m.cur.key() == msg.key {
			cmds = append(cmds, m.loadDetail())
		}
		if msg.after != nil {
			m = m.openMenu(msg.after)
		}
		return m, tea.Batch(cmds...), true
	}
	return m, nil, false
}

// replaceRow puts a fresher copy of a PR into every list it's in.
func (m *model) replaceRow(pr pullRequest) {
	for t, list := range m.prs {
		for i := range list {
			if list[i].key() == pr.key() {
				m.prs[t][i] = pr
			}
		}
		sortPRs(m.prs[t], t) // it may be in another group now
	}
}

// ── keys ──────────────────────────────────────────────────────────────────────

func (m model) handleDetailKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	pr := *m.cur
	switch k.String() {
	case "up", "k", "ctrl+p":
		m.scrollBody(-1)
	case "down", "j", "ctrl+n":
		m.scrollBody(1)
	case "pgup":
		m.scrollBody(-max(1, m.bodyRoom()-2))
	case "pgdown", " ":
		m.scrollBody(max(1, m.bodyRoom()-2))
	case "esc", "q", "left", "h":
		if m.direct && (k.String() == "esc" || k.String() == "q") {
			return m.leave() // opened on this PR: there's no list to go back to
		}
		m.screen, m.err, m.flash, m.curDetail, m.direct = screenList, "", "", nil, false
		m.chose = true
		m.reselect(pr.key()) // wherever it's listed now
	case "o":
		m.openURL(pr.URL)
	case "y":
		if term, err := copyText(pr.URL); err != nil {
			m.err = err.Error()
		} else {
			m.flash = copied(pr.URL, term)
		}
	case "b":
		if term, err := copyText(localBranch(pr)); err != nil {
			m.err = err.Error()
		} else {
			m.flash = copied(localBranch(pr), term)
		}
	case "w", "enter":
		return m.runWorktree(pr, false)
	case "s":
		return m.runWorktree(pr, true)
	case "a":
		return m.goToAgent(pr)
	case "d":
		return m.toggleDraft()
	case "m":
		return m.mergeMenu()
	case "ctrl+r":
		m.err, m.flash = "", ""
		return m, m.loadDetail()
	}
	return m, nil
}

// busy starts a change on GitHub, with its spinner line.
func (m model) busy(status string, run func() prChangedMsg) (tea.Model, tea.Cmd) {
	m.mode, m.err, m.flash, m.status = modeBusy, "", "", status
	return m, tea.Batch(m.spin.Tick, func() tea.Msg { return run() })
}

func (m model) toggleDraft() (tea.Model, tea.Cmd) {
	// Go by the PR as just loaded, not the list's copy: after a change, or
	// from last time's list, that can be out of date.
	if m.curDetail == nil {
		m.flash = "Still loading…"
		return m, nil
	}
	pr := m.curDetail.pullRequest
	if pr.State != "OPEN" {
		m.err = fmt.Sprintf("#%d is %s.", pr.Number, strings.ToLower(pr.State))
		return m, nil
	}
	client, ctx := m.client, m.ctx
	if pr.IsDraft {
		return m.busy(fmt.Sprintf("Marking #%d ready for review…", pr.Number), func() prChangedMsg {
			return prChangedMsg{key: pr.key(), flash: fmt.Sprintf("#%d is ready for review", pr.Number), err: client.setDraft(ctx, pr, false)}
		})
	}
	return m.busy(fmt.Sprintf("Converting #%d to a draft…", pr.Number), func() prChangedMsg {
		return prChangedMsg{key: pr.key(), flash: fmt.Sprintf("#%d is a draft again", pr.Number), err: client.setDraft(ctx, pr, true)}
	})
}

// ── merging ───────────────────────────────────────────────────────────────────

var methodNames = map[string]string{"MERGE": "Create a merge commit", "SQUASH": "Squash and merge", "REBASE": "Rebase and merge"}
var methodVerbs = map[string]string{"MERGE": "Merge", "SQUASH": "Squash-merge", "REBASE": "Rebase-merge"}

// mergeableNow says whether GitHub would merge it right now.
func mergeableNow(d *prDetail) bool {
	switch d.MergeStateStatus {
	case "CLEAN", "UNSTABLE", "HAS_HOOKS":
		return d.Mergeable != "CONFLICTING"
	}
	return false
}

// readiness is one line on whether the PR can merge, and if not, why not.
func readiness(d *prDetail) (text string, style lipgloss.Style) {
	switch {
	case d.State == "MERGED":
		return "Merged", styleMerged
	case d.State == "CLOSED":
		return "Closed without merging", styleErr
	case d.IsDraft:
		return "Draft: mark it ready for review (d) before it can merge", styleDim
	case d.Mergeable == "CONFLICTING" || d.MergeStateStatus == "DIRTY":
		return "Conflicts with " + d.BaseRefName + ": resolve them before merging", styleErr
	case d.MergeQueueEntry != nil:
		return queued(d.MergeQueueEntry)
	}
	failing, pending := requiredCounts(d.Checks)
	var s string
	switch d.MergeStateStatus {
	case "CLEAN":
		s, style = "Ready to merge", styleOK
	case "HAS_HOOKS":
		s, style = "Ready to merge (the server runs pre-receive hooks)", styleOK
	case "UNSTABLE":
		s, style = "Can merge, but checks that aren't required are failing", styleWarn
	case "BEHIND":
		s, style = "Behind "+d.BaseRefName+": it must be brought up to date first", styleWarn
	case "BLOCKED":
		style = styleErr
		switch {
		case failing > 0:
			s = fmt.Sprintf("Blocked: %d required %s failing", failing, plural(failing, "check", "checks"))
		case d.ReviewDecision == "CHANGES_REQUESTED":
			s = "Blocked: changes requested"
		case d.ReviewDecision == "REVIEW_REQUIRED":
			s = "Blocked: needs an approving review"
		case pending > 0:
			s, style = fmt.Sprintf("Waiting on %d required %s", pending, plural(pending, "check", "checks")), styleWarn
		default:
			s = "Blocked by the branch's rules"
		}
	default:
		s, style = "GitHub is still working out whether it can merge (ctrl+r)", styleDim
	}
	if d.IsMergeQueueEnabled && (d.MergeStateStatus == "CLEAN" || d.MergeStateStatus == "HAS_HOOKS") {
		s += ": through the merge queue"
	}
	if !d.ViewerCanMerge {
		s += " · you can't merge in this repo"
	}
	return s, style
}

// queued is where a PR is in its merge queue, in words: its place, what's
// happening to it, and how long GitHub expects.
func queued(q *queueEntry) (string, lipgloss.Style) {
	what := map[string]string{
		"QUEUED":          "waiting its turn",
		"AWAITING_CHECKS": "its checks are running",
		"MERGEABLE":       "about to merge",
		"UNMERGEABLE":     "it can't merge, and will be taken out",
		"LOCKED":          "merging now",
	}[q.State]
	if what == "" {
		what = strings.ToLower(q.State)
	}
	s := fmt.Sprintf("In the merge queue, %s in line: %s", ordinal(q.Position), what)
	if q.EstimatedTimeToMerge != nil && *q.EstimatedTimeToMerge > 0 {
		s += fmt.Sprintf(" · about %d min", max(1, (*q.EstimatedTimeToMerge+30)/60))
	}
	if q.State == "UNMERGEABLE" {
		return s, styleErr
	}
	return s, styleTree
}

// ordinal is 1st, 2nd, 3rd, 4th, … 11th, 12th, 13th, … 21st.
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

func requiredCounts(cs []check) (failing, pending int) {
	for _, c := range cs {
		if !c.IsRequired {
			continue
		}
		switch c.outcome() {
		case "fail":
			failing++
		case "pending":
			pending++
		}
	}
	return
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// mergeMenu offers what can be done now: merge with any method the repo
// allows (your last choice for this repo first), or, while it's not ready,
// auto-merge; or turning auto-merge off.
func (m model) mergeMenu() (tea.Model, tea.Cmd) {
	d := m.curDetail
	if d == nil {
		m.flash = "Still loading…"
		return m, nil
	}
	if d.State != "OPEN" {
		m.err = fmt.Sprintf("#%d is already %s.", d.Number, strings.ToLower(d.State))
		return m, nil
	}
	if d.IsDraft {
		m.err = "It's a draft: press d to mark it ready for review first."
		return m, nil
	}
	if !d.ViewerCanMerge {
		m.err = "You don't have permission to merge in " + d.Repository.NameWithOwner + "."
		return m, nil
	}
	if d.IsMergeQueueEnabled {
		return m.queueMenu()
	}
	methods := orderMethods(d.RepoSettings.methods(), preferredMethod(d.repo(), d.RepoSettings.ViewerDefaultMergeMethod))
	mn := &menu{title: fmt.Sprintf("Merge #%d into %s", d.Number, d.BaseRefName)}
	if d.AutoMergeRequest != nil {
		mn.items = append(mn.items, menuItem{label: "Turn off auto-merge", run: func(m model) (tea.Model, tea.Cmd) {
			client, ctx, key := m.client, m.ctx, d.key()
			return m.busy("Turning off auto-merge…", func() prChangedMsg {
				return prChangedMsg{key: key, flash: "Auto-merge is off", err: client.disableAutoMerge(ctx, d)}
			})
		}})
	}
	now := mergeableNow(d)
	for _, meth := range methods {
		meth := meth
		if now {
			mn.items = append(mn.items, menuItem{label: methodNames[meth], run: func(m model) (tea.Model, tea.Cmd) {
				return m.openMenu(confirmMerge(d, meth, false)), nil
			}})
		} else if d.RepoSettings.AutoMergeAllowed && d.AutoMergeRequest == nil && d.Mergeable != "CONFLICTING" {
			mn.items = append(mn.items, menuItem{label: "Auto-merge when ready: " + strings.ToLower(methodNames[meth]), run: func(m model) (tea.Model, tea.Cmd) {
				return m.openMenu(confirmMerge(d, meth, true)), nil
			}})
		}
	}
	if len(mn.items) == 0 {
		reason, _ := readiness(d)
		if !d.RepoSettings.AutoMergeAllowed && !now {
			reason += " (and auto-merge is off for this repo)"
		}
		m.err = reason
		return m, nil
	}
	mn.items = append(mn.items, menuItem{label: "Cancel", run: func(m model) (tea.Model, tea.Cmd) { return m, nil }})
	return m.openMenu(mn), nil
}

// queueMenu is the merge menu where the base branch has a merge queue: the
// queue merges it, by its own method, once it's through. In the queue, it
// can be taken out; ready (or only waiting on checks the queue will run),
// put in; otherwise why not.
func (m model) queueMenu() (tea.Model, tea.Cmd) {
	d := m.curDetail
	mn := &menu{title: fmt.Sprintf("Merge #%d into %s, through its merge queue", d.Number, d.BaseRefName)}
	if d.MergeQueueEntry != nil {
		mn.items = append(mn.items, menuItem{label: "Take it out of the merge queue", run: func(m model) (tea.Model, tea.Cmd) {
			client, ctx, key := m.client, m.ctx, d.key()
			return m.busy("Taking it out of the merge queue…", func() prChangedMsg {
				return prChangedMsg{key: key, flash: fmt.Sprintf("#%d is out of the merge queue", d.Number), err: client.dequeue(ctx, d)}
			})
		}})
	} else {
		failing, _ := requiredCounts(d.Checks)
		onlyWaiting := d.MergeStateStatus == "BLOCKED" && failing == 0 &&
			d.ReviewDecision != "CHANGES_REQUESTED" && d.ReviewDecision != "REVIEW_REQUIRED"
		if !mergeableNow(d) && !onlyWaiting {
			reason, _ := readiness(d)
			m.err = "It can't join the merge queue yet: " + reason
			return m, nil
		}
		mn.items = append(mn.items, menuItem{label: "Add it to the merge queue", run: func(m model) (tea.Model, tea.Cmd) {
			return m.openMenu(confirmQueue(d)), nil
		}})
	}
	mn.items = append(mn.items, menuItem{label: "Cancel", run: func(m model) (tea.Model, tea.Cmd) { return m, nil }})
	return m.openMenu(mn), nil
}

// confirmQueue asks once more before a PR joins the merge queue.
func confirmQueue(d *prDetail) *menu {
	return &menu{
		title:  fmt.Sprintf("Add #%d to the merge queue for %s? It merges when its turn comes.", d.Number, d.BaseRefName),
		cursor: 1, noDigits: true,
		items: []menuItem{
			{label: "Add it to the queue", run: func(m model) (tea.Model, tea.Cmd) {
				client, ctx, key := m.client, m.ctx, d.key()
				return m.busy(fmt.Sprintf("Adding #%d to the merge queue…", d.Number), func() prChangedMsg {
					if err := client.enqueue(ctx, d); err != nil {
						return prChangedMsg{key: key, err: err}
					}
					return prChangedMsg{key: key, flash: fmt.Sprintf("#%d is in the merge queue", d.Number)}
				})
			}},
			{label: "Cancel", run: func(m model) (tea.Model, tea.Cmd) { return m, nil }},
		},
	}
}

// orderMethods puts the preferred method first, keeping GitHub's order otherwise.
func orderMethods(methods []string, preferred string) []string {
	out := []string{}
	for _, x := range methods {
		if x == preferred {
			out = append(out, x)
		}
	}
	for _, x := range methods {
		if x != preferred {
			out = append(out, x)
		}
	}
	return out
}

// confirmMerge asks once more, naming exactly what's about to happen.
func confirmMerge(d *prDetail, method string, auto bool) *menu {
	title := fmt.Sprintf("%s #%d into %s now?", methodVerbs[method], d.Number, d.BaseRefName)
	yes := methodVerbs[method] + " it"
	if auto {
		title = fmt.Sprintf("Turn on auto-merge (%s) for #%d? It merges once checks and reviews allow.", strings.ToLower(methodNames[method]), d.Number)
		yes = "Turn on auto-merge"
	}
	return &menu{title: title, cursor: 1, noDigits: true, items: []menuItem{
		{label: yes, run: func(m model) (tea.Model, tea.Cmd) {
			client, ctx, key, repo := m.client, m.ctx, d.key(), d.repo()
			status := fmt.Sprintf("Merging #%d…", d.Number)
			if auto {
				status = "Turning on auto-merge…"
			}
			after := m.cleanupMenu(d)
			return m.busy(status, func() prChangedMsg {
				if err := client.merge(ctx, d, method, auto); err != nil {
					return prChangedMsg{key: key, err: err}
				}
				rememberMethod(repo, method)
				if auto {
					return prChangedMsg{key: key, flash: fmt.Sprintf("Auto-merge is on: #%d merges when it's ready", d.Number)}
				}
				return prChangedMsg{key: key, flash: fmt.Sprintf("Merged #%d", d.Number), after: after}
			})
		}},
		{label: "Cancel", run: func(m model) (tea.Model, tea.Cmd) { return m, nil }},
	}}
}

// cleanupMenu is offered after a merge: delete the branch on GitHub (unless
// the repo does that itself, or it's someone's fork) and remove the worktree.
func (m model) cleanupMenu(d *prDetail) *menu {
	pr := d.pullRequest
	canDelete := !d.RepoSettings.DeleteBranchOnMerge && !d.IsCrossRepository && d.HeadRef != nil
	hasTree := m.worktrees[pr.key()]
	if !canDelete && !hasTree {
		return nil
	}
	deleteBranch := func(m model) error { return m.client.deleteBranch(m.ctx, d) }
	known := m.checkouts[pr.repo().key()] // read here: the removal runs off the UI's goroutine
	removeTree := func(m model) error {
		if d, ok := m.client.(*demoSource); ok {
			d.removeTree(pr.key())
			return nil
		}
		dir := known
		if dir == "" {
			var err error
			if dir, err = findCheckout(m.ctx, m.cfg, pr.repo(), m.invoked); err != nil {
				return err
			}
		}
		return removeWorktree(m.ctx, pr, dir)
	}
	run := func(label string, branch, tree bool) menuItem {
		return menuItem{label: label, run: func(m model) (tea.Model, tea.Cmd) {
			return m.busy("Cleaning up…", func() prChangedMsg {
				var done, failed []string
				if branch {
					if err := deleteBranch(m); err != nil {
						failed = append(failed, "branch not deleted: "+err.Error())
					} else {
						done = append(done, "branch deleted")
					}
				}
				if tree {
					if err := removeTree(m); err != nil {
						failed = append(failed, "worktree kept: "+err.Error())
					} else {
						done = append(done, "worktree removed")
					}
				}
				msg := prChangedMsg{key: pr.key(), flash: strings.Join(done, ", "), treeGone: tree && len(failed) == 0}
				if len(failed) > 0 {
					msg.err = fmt.Errorf("%s", strings.Join(append(done, failed...), "; "))
				}
				return msg
			})
		}}
	}
	mn := &menu{title: fmt.Sprintf("Merged #%d. Clean up?", d.Number), noDigits: true}
	if canDelete && hasTree {
		mn.items = append(mn.items, run("Delete the branch and remove the worktree", true, true))
	}
	if canDelete {
		mn.items = append(mn.items, run("Delete the branch "+d.HeadRefName+" on GitHub", true, false))
	}
	if hasTree {
		mn.items = append(mn.items, run("Remove the worktree (and close its space)", false, true))
	}
	mn.items = append(mn.items, menuItem{label: "Leave everything", run: func(m model) (tea.Model, tea.Cmd) { return m, nil }})
	return mn
}

// ── helpers ───────────────────────────────────────────────────────────────────

// openURL opens a PR in the browser. The demo's repos don't exist, so it only
// says where it would have gone.
func (m *model) openURL(u string) {
	if m.demo {
		m.flash = "Demo: would open " + u
		return
	}
	if !m.isGitHubURL(u) {
		m.err = "Not opening " + u + ": not a link to your GitHub"
		return
	}
	// A browser here wouldn't be in front of you (over SSH), or there's none
	// to open (no display): the link goes on your clipboard instead.
	if noBrowser() {
		if term, err := copyText(u); err != nil {
			m.err = "Couldn't copy the link: " + err.Error()
		} else {
			m.flash = copied("the link", term) + ": open it in your browser"
		}
		return
	}
	if err := browse(u); err != nil {
		m.err = "Couldn't open the browser: " + err.Error()
	}
}

// isGitHubURL admits https links on a configured GitHub host only. The URL
// comes from the API and goes to `open` or `xdg-open`, which would as happily launch
// a file: path or another app's URL scheme.
func (m model) isGitHubURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.User != nil {
		return false
	}
	return m.cfg.knownHost(p.Hostname())
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := nowFn().Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2 Jan 2006")
}

// ── views ─────────────────────────────────────────────────────────────────────

// field is one "Name  value" line of the PR header, cut to the popup's width.
func (m model) field(name, value string) string {
	if value == "" {
		return ""
	}
	return " " + styleDim.Render(fmt.Sprintf("%-9s", name)) + " " + shorten(value, max(10, m.width-12)) + "\n"
}

func (m model) viewPR() string {
	header := m.prHeader()
	var b strings.Builder
	b.WriteString(header)
	room := m.bodyRoomFor(header)
	switch {
	case m.menu != nil:
		b.WriteString(m.viewMenu(room))
	case m.curDetail == nil && m.err == "":
		b.WriteString(" " + m.spin.View() + styleDim.Render(" loading…") + "\n")
	default:
		b.WriteString(window(m.bodyLines(), m.scroll, room))
	}
	return b.String()
}

// bodyRoomFor is how many lines are left under the header.
func (m model) bodyRoomFor(header string) int {
	used := strings.Count(header, "\n") + 2 // + tabs line + blank line
	return m.height - used - 2              // status line + footer
}

func (m model) bodyRoom() int { return m.bodyRoomFor(m.prHeader()) }

func (m *model) scrollBody(delta int) {
	m.scroll = min(max(0, m.scroll+delta), maxScroll(m.bodyLines(), m.bodyRoom()))
}

// prHeaderTop is the PR screen down to its branch: what's above its agents.
func (m model) prHeaderTop() string {
	pr := m.cur
	w := max(20, m.width-2)
	var b strings.Builder
	ps := prStyle(pr.State, pr.IsDraft)
	b.WriteString(" " + statePill(ps, prStateWord(*pr)) + "  " + prNumber(pr.Number, pr.State, pr.IsDraft) + styleDim.Render("  ·  "+pr.Repository.NameWithOwner) + "\n\n")
	b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(1).Bold(true).Render(pr.Title) + "\n\n")

	author := pr.author()
	if !pr.UpdatedAt.IsZero() {
		author += styleDim.Render("  ·  updated " + ago(pr.UpdatedAt))
	}
	b.WriteString(m.field("Author", author))
	head := pr.HeadRefName
	if pr.IsCrossRepository {
		head = pr.headOwner() + ":" + head
	}
	tree := styleDim.Render("no worktree yet")
	if m.worktrees[pr.key()] {
		tree = styleTree.Render("⌥ worktree")
	}
	b.WriteString(m.field("Branch", styleBranch.Render(head)+styleDim.Render(" → ")+styleBranch.Render(pr.BaseRefName)+"  "+tree))
	return b.String()
}

// prHeader is everything on the PR screen above the scrolling part.
func (m model) prHeader() string {
	pr := m.cur
	var b strings.Builder
	top := m.prHeaderTop()
	b.WriteString(top)
	b.WriteString(m.agentLines(*pr, 2+strings.Count(top, "\n"))) // under the tabs and a blank line
	d := m.curDetail
	if d != nil {
		b.WriteString(m.field("Changes", styleOK.Render(fmt.Sprintf("+%d", d.Additions))+" "+styleErr.Render(fmt.Sprintf("−%d", d.Deletions))+
			styleDim.Render(fmt.Sprintf("  ·  %d %s", d.ChangedFiles, plural(d.ChangedFiles, "file", "files")))))
	}
	var labels []string
	for _, l := range pr.Labels.Nodes {
		labels = append(labels, colored("●", "#"+l.Color)+" "+l.Name)
	}
	b.WriteString(m.field("Labels", strings.Join(labels, "  ")))
	if d != nil {
		b.WriteString(m.field("Reviews", reviewersLine(d.Reviewers)))
		b.WriteString(m.field("Checks", checksLine(d.Checks)))
		text, style := readiness(d)
		merge := style.Render(text)
		if d.AutoMergeRequest != nil && d.State == "OPEN" {
			merge += styleTree.Render("  ·  auto-merge on (" + strings.ToLower(d.AutoMergeRequest.MergeMethod) + ")")
		}
		b.WriteString(m.field("Merge", merge))
	}
	b.WriteString("\n")
	return b.String()
}

func reviewersLine(rs []reviewer) string {
	if len(rs) == 0 {
		return styleDim.Render("none yet")
	}
	var parts []string
	for _, r := range rs {
		switch r.State {
		case "APPROVED":
			parts = append(parts, styleOK.Render("✓")+" "+r.Name)
		case "CHANGES_REQUESTED":
			parts = append(parts, styleErr.Render("✗")+" "+r.Name)
		case "PENDING":
			parts = append(parts, styleWarn.Render("○")+" "+r.Name+styleDim.Render(" (requested)"))
		default:
			parts = append(parts, styleDim.Render("·")+" "+r.Name)
		}
	}
	return strings.Join(parts, "  ")
}

func checksLine(cs []check) string {
	if len(cs) == 0 {
		return styleDim.Render("none")
	}
	n := map[string]int{}
	for _, c := range cs {
		n[c.outcome()]++
	}
	var parts []string
	if n["fail"] > 0 {
		parts = append(parts, styleErr.Render(fmt.Sprintf("✗ %d failing", n["fail"])))
	}
	if n["pending"] > 0 {
		parts = append(parts, styleWarn.Render(fmt.Sprintf("● %d running", n["pending"])))
	}
	if n["pass"] > 0 {
		parts = append(parts, styleOK.Render(fmt.Sprintf("✓ %d passed", n["pass"])))
	}
	if n["skip"] > 0 {
		parts = append(parts, styleDim.Render(fmt.Sprintf("%d skipped", n["skip"])))
	}
	return strings.Join(parts, styleDim.Render("  ·  "))
}

// bodyLines is the scrolling part: the checks that need attention, the
// description, unresolved review threads, then the conversation.
func (m model) bodyLines() []string {
	if m.curDetail == nil {
		return nil
	}
	return m.md.lines(m.cur.key(), prBody(m.curDetail), max(20, m.width))
}

// prBody is the PR screen's scrolling part as one Markdown document.
func prBody(d *prDetail) string {
	var b strings.Builder

	var attention []check
	for _, c := range d.Checks {
		if o := c.outcome(); o == "fail" || o == "pending" {
			attention = append(attention, c)
		}
	}
	if len(attention) > 0 {
		b.WriteString("## Checks\n\n")
		for _, c := range attention {
			glyph := "✗"
			if c.outcome() == "pending" {
				glyph = "●"
			}
			req := ""
			if c.IsRequired {
				req = " *(required)*"
			}
			fmt.Fprintf(&b, "- %s %s%s\n", glyph, mdEscape(c.name()), req)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Description\n\n")
	if strings.TrimSpace(d.Body) == "" {
		b.WriteString("*No description.*\n\n")
	} else {
		b.WriteString(d.Body + "\n\n")
	}

	var open []thread
	for _, t := range d.Threads {
		if !t.IsResolved {
			open = append(open, t)
		}
	}
	if len(open) > 0 {
		fmt.Fprintf(&b, "## Unresolved threads (%d)\n\n", len(open))
		for _, t := range open {
			where := "`" + t.Path + "`"
			if t.Line > 0 {
				where = fmt.Sprintf("`%s:%d`", t.Path, t.Line)
			}
			if t.IsOutdated {
				where += " *(outdated)*"
			}
			b.WriteString("**" + where + "**\n\n")
			for _, c := range t.Comments.Nodes {
				fmt.Fprintf(&b, "> **%s** · %s\n>\n%s\n\n", mdEscape(commentAuthor(c)), ago(c.CreatedAt), quote(c.Body))
			}
		}
	}

	type entry struct {
		at   time.Time
		text string
	}
	var conv []entry
	for _, r := range d.Reviews {
		verb := map[string]string{"APPROVED": "approved", "CHANGES_REQUESTED": "requested changes", "COMMENTED": "reviewed", "DISMISSED": "review dismissed"}[r.State]
		if verb == "" || (r.State == "COMMENTED" && strings.TrimSpace(r.Body) == "") {
			continue // a review that's only inline comments is in the threads
		}
		text := fmt.Sprintf("**%s** %s · %s\n\n", mdEscape(commentAuthor(r)), verb, ago(r.CreatedAt))
		if strings.TrimSpace(r.Body) != "" {
			text += r.Body + "\n\n"
		}
		conv = append(conv, entry{r.CreatedAt, text})
	}
	for _, c := range d.Comments {
		conv = append(conv, entry{c.CreatedAt, fmt.Sprintf("**%s** · %s\n\n%s\n\n", mdEscape(commentAuthor(c)), ago(c.CreatedAt), c.Body)})
	}
	if len(conv) > 0 {
		// Oldest first, as on GitHub.
		for i := 1; i < len(conv); i++ {
			for j := i; j > 0 && conv[j].at.Before(conv[j-1].at); j-- {
				conv[j], conv[j-1] = conv[j-1], conv[j]
			}
		}
		b.WriteString("## Conversation\n\n")
		for i, e := range conv {
			if i > 0 {
				b.WriteString("---\n\n")
			}
			b.WriteString(e.text)
		}
	}
	return b.String()
}

func commentAuthor(c comment) string {
	if c.Author == nil {
		return "ghost"
	}
	return c.Author.Login
}

// quote puts a comment inside a Markdown block quote, line by line.
func quote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// mdEscape keeps a name (a check's, a login) from being read as Markdown.
func mdEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", `\<`, "#", `\#`).Replace(s)
}

func (m model) detailFooter() []hint {
	// By kind, so the colours sit together: go, act, view, leave.
	var hs []hint
	if m.cur != nil && len(m.agentsOf(*m.cur)) > 0 {
		hs = append(hs, hint{"a " + plural(len(m.agentsOf(*m.cur)), "agent", "agents"), "a", hintGo})
	}
	hs = append(hs, hint{"w worktree", "w", hintAct}, hint{"s start", "s", hintAct})
	if m.cur != nil && m.cur.State == "OPEN" {
		if m.cur.IsDraft {
			hs = append(hs, hint{"d ready", "d", hintAct})
		} else {
			hs = append(hs, hint{"d draft", "d", hintAct})
		}
		hs = append(hs, hint{"m merge", "m", hintAct})
	}
	hs = append(hs, hint{"o open", "o", hintView}, hint{"y copy url", "y", hintView})
	if m.direct {
		return append(hs, hint{"← lists", "left", hintView}, hint{m.escLabel(), "esc", hintQuiet})
	}
	return append(hs, hint{"esc back", "esc", hintQuiet})
}
