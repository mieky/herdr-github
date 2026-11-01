# GitHub

**Your pull requests, one key away from whatever you're working in.**

Agents open pull requests faster than anyone can keep track of them. This
popup (⌘G) keeps them in one place, for every repo, whichever machine
you're on:

- **Whose move it is.** Yours, the ones waiting on your review, and the
  shown space's repo's, grouped: to review, needs fixing, ready to merge,
  waiting, drafts. Each with its branch, checks, review, size, auto-merge
  or place in the merge queue, labels, worktree and agents.
- **Can it merge, and if not, why not,** in one line, with the failing
  checks by name, the reviewers, and the description and conversation.
- **Merge from here:** any method the repo allows, auto-merge, or its merge
  queue; then delete the branch and remove the worktree. Draft ↔ ready with
  a key.
- **A worktree with an agent on it, in one key:** made from the PR's head
  (forks included) on the machine with the checkout, opened as a space,
  with Claude Code asked to look at the PR.
- **Every PR knows its agents:** the one that opened it and any that worked
  on it since, a click away, or resumed if closed.
- **Nothing else to install** but gh, signed in somewhere.

## What it is

A popup mod (`mod.json`: an overlay popup at the top, 80% of the window),
opened with ⌘G. It's the Herdr app's side of
[herdr-github](https://github.com/mrolafsson/herdr-github), whose code does
the work.

## Installing it (for the agent adding it)

1. Copy `mods/github/` to `~/.config/herdr-app/mods/github/`.
2. Merge `keys.json` into the person's: add its sections and keys; ask
   before replacing a key they already bind to something else.
3. `herdr-app check ~/.config/herdr-app/mods/github`.

**Already installed? That's an update, not a clash.** If `mods/github/`
exists and is this mod (its `main.js` sets `GITHUB_VERSION` and fetches
herdr-github), replace its files with these, in place: don't install a
second copy under another name. Its keys are merged already; step 2 only
adds any new ones.

**A different mod named `github`?** Then rename this one, and everything
that names it follows: the folder; `ICONS` in `look.js`
(`mods/<name>/icons`); in `keys.json`, the section `mod:github` and its
action `github.escape`, and `mod.open:github`. Recap opens a pull request
in a mod named `github`: renamed, it opens them in the browser instead.

That's all. **Don't install herdr-github, or anything else, on any
machine:** the mod fetches the herdr-github release it needs onto each
machine it works on by itself, the first time. What it needs there:
[gh](https://cli.github.com), signed in (`gh auth login`) on at least one
machine (it finds which); git; and `curl`, `tar` and `sha256sum` or
`shasum`, which macOS and Linux have. If gh isn't signed in anywhere, tell
the person to run `gh auth login`; don't do it for them.

To see that it works: open it (⌘G). The first opening takes a few seconds
longer while herdr-github is fetched; afterwards it's in
`~/.local/share/herdr-github/app/<version>/` there, and the lists fill. A
machine that isn't signed in is skipped; with none signed in, the popup
says so.

## What it does, and asks

- Runs `herdr-github app` (one JSON request in, one answer out) through
  `exec(…, { on: machine })` on the machine where gh is signed in: the
  lists (GitHub's search, as the TUI plugin's), a PR's screen, and changes
  to it: draft, ready, merge, auto-merge, the merge queue, deleting its
  branch. It borrows gh's token; it keeps none of its own.
- **Runs programs and reaches the network:** the first time it asks a
  machine, it downloads the herdr-github release it names
  (`GITHUB_VERSION` in `main.js`) from GitHub for that machine, checks it
  against the release's `checksums.txt`, and keeps it in
  `~/.local/share/herdr-github/app/<version>/`, apart from any TUI install.
- Reads the shown space's git remotes, to know its repo.
- **Worktrees:** on the machine of the shown space when it has a checkout
  of the PR's repo (else another machine with one), it fetches the PR's
  head and adds a worktree in `~/.herdr/worktrees/<repo>/<branch>` (as the
  app's own worktree mod does), then opens it with `space.new`; for Start,
  with the first installed agent from `knownAgents` (Claude Code first) and
  a first message asking it to look at the PR. With no checkout anywhere,
  it offers to clone the repo with `gh repo clone`, only when asked. After a
  merge it offers to remove the worktree (`git worktree remove`, never
  forced) and close its space.
- **Its agents:** reads Claude Code's transcripts on each machine with
  agents for the PRs each session worked on (herdr-github's `sessions`),
  and matches them to the app's agents by folder; a click goes to one with
  `agent.focus`, or resumes a closed one (`claude --resume`) in a space.
- Opens links with the system's browser and copies them to the clipboard.
- Keeps, in its own `localStorage`, which machine answered, the tab it was
  on and the worktrees it made; and its last lists in `github-cache.json` in
  the app's home, to show them at once on opening while fresh ones load.

## Its look

Recap's: colour only on state, from the theme's own roles (the ANSI green
for ready, the working colour for waiting, red for broken, magenta for
merged, the accent for what's asked of you). Drawn icons in `icons/`, 16px
with a 1.6 stroke, tinted with the text colour. Cards with a raised surface,
a shadow and an edge in their group's colour when chosen; details are
chips; whether it can merge is a callout in its tone. JetBrains Mono for
branches, numbers and keys.

## Keys (`keys.json`)

`cmd-g` opens it; `escape` leaves a menu, then a PR's screen (back to the
list, or to Recap if Recap opened it), then the popup. In the list: type to
filter, ← → for the tabs, ↑ ↓ and Enter. On a PR: `w` worktree, `s` start an
agent, `m` merge, `d` draft ↔ ready, `a` agents, `o` open on GitHub, `y`
copy the link, `l` its Linear issue (in herdr-linear's Linear mod), `r`
reload. Every key is also a click along the bottom.
`github.demo` (with `herdr-app do`, while it's open) switches to a made-up
org for that opening.
