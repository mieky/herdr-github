package main

import (
	"strconv"

	"github.com/charmbracelet/lipgloss"
)

// group is where a pull request has got to, which every list is grouped by.
// In list order: whose move it is, yours first.
type group int

const (
	groupReview  group = iota // (Review requested) waiting on your review
	groupFixing               // failing, in conflict or sent back
	groupReady                // nothing in the way of merging it
	groupWaiting              // on its reviewers, or its checks
	groupDrafts               // not out for review yet
	groupDone                 // merged or closed
)

// groupOf derives a PR's group in tab t. A draft is a draft whatever its
// checks say: it isn't out yet. One you're asked to review is yours to
// review unless something's wrong with it, which is its author's to fix.
func groupOf(pr pullRequest, t tab) group {
	failing := checkGlyph(pr.checks()) == "✗"
	running := checkGlyph(pr.checks()) == "●"
	switch {
	case pr.State == "MERGED" || pr.State == "CLOSED":
		return groupDone
	case pr.IsDraft:
		return groupDrafts
	// In a merge queue: its turn is coming.
	case pr.MergeQueueEntry != nil:
		return groupWaiting
	case failing || pr.Mergeable == "CONFLICTING" || pr.ReviewDecision == "CHANGES_REQUESTED":
		return groupFixing
	case t == tabReview:
		return groupReview
	case running:
		return groupWaiting
	// Approved, or with no review asked of it and its checks passed.
	case pr.ReviewDecision == "APPROVED", pr.ReviewDecision == "" && pr.checks() == "SUCCESS":
		return groupReady
	}
	return groupWaiting
}

func (g group) label() string {
	switch g {
	case groupReview:
		return "To review"
	case groupFixing:
		return "Needs fixing"
	case groupReady:
		return "Ready to merge"
	case groupDrafts:
		return "Drafts"
	case groupDone:
		return "Done"
	}
	return "Waiting"
}

func (g group) style() lipgloss.Style {
	switch g {
	case groupReview:
		return styleTree
	case groupFixing:
		return styleErr
	case groupReady:
		return styleOK
	case groupDrafts:
		return styleWorking
	case groupDone:
		return styleMerged
	}
	return styleWarn
}

// headed is whether the i-th row has its group's heading above it: the
// first of each group, and whichever PR the list is scrolled to, so the top
// of the list always says what it's showing.
func (m model) headed(rows []row, i int) bool {
	if rows[i].pr == nil {
		return false
	}
	return i == 0 || i == m.offset || rows[i-1].pr == nil || groupOf(*rows[i].pr, m.tab) != groupOf(*rows[i-1].pr, m.tab)
}

// headingHeight is the lines the i-th row's heading takes: the heading and
// a blank under it, or none.
func (m model) headingHeight(rows []row, i int) int {
	if m.headed(rows, i) {
		return 2
	}
	return 0
}

// viewHeading is a group's heading: its name, and how many are listed in it.
func (m model) viewHeading(rows []row, g group) string {
	n := 0
	for _, r := range rows {
		if r.pr != nil && groupOf(*r.pr, m.tab) == g {
			n++
		}
	}
	return " " + g.style().Bold(true).Render(g.label()) + styleDim.Render(" "+strconv.Itoa(n))
}

// tabCell is one tab along the top, and the cells it's drawn in (x0 up to
// x1), for the mouse.
type tabCell struct {
	tab    tab
	x0, x1 int
}

// tabName is a tab's name; short, for a popup too narrow for it in full.
func (m model) tabName(t tab, short bool) string {
	switch {
	case t == tabMine:
		return "Mine"
	case t == tabReview && short:
		return "Review"
	case t == tabReview:
		return "Review requested"
	case m.repo == nil && short:
		return "Repo"
	case m.repo == nil:
		return "This repo"
	case short:
		return shorten(m.repo.Name, 12)
	}
	return m.repo.Name
}

// tabBar is the popup's first line: the tabs, each with how many pull
// requests it has (and a + while more are on their way), the one shown as a
// pill, in line with the keys' pills along the bottom. The popup's name is
// on its frame, which herdr draws. Too wide for the popup, the labels
// shorten. On a PR's screen no tab is the one shown: they're the way back.
func (m model) tabBar() (string, []tabCell) {
	cells := []tabCell{{tab: tabMine}, {tab: tabReview}, {tab: tabRepo}}
	var line string
	for _, short := range []bool{false, true} {
		line = " "
		x := lipgloss.Width(line)
		for i, c := range cells {
			label, n := m.tabName(c.tab, short), ""
			if m.loaded[c.tab] && (c.tab != tabRepo || m.repo != nil) {
				n = " " + strconv.Itoa(len(m.prs[c.tab]))
				if m.paging[c.tab] {
					n += "+"
				}
			}
			cell := " " + styleText.Render(label) + styleDim.Render(n) + " "
			if c.tab == m.tab && m.screen == screenList {
				cell = pill(look.Accent, label+n)
			}
			line += cell
			cells[i].x0, cells[i].x1 = x, x+lipgloss.Width(cell)
			x = cells[i].x1
		}
		if x <= m.width {
			break
		}
	}
	return line, cells
}
