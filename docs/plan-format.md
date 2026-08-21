# The plan format

`mindskein priorities` reads one markdown file and prints the priorities in it.
The file is yours: any note, any editor, any folder scheme. Point at it in
`~/.mindskein/config.toml`:

```toml
[vault]
path = "~/notes"                 # optional base; ~ expands
plan = "plan.md"                 # relative to path, or absolute
```

Neither key has a default. A guessed layout is a guess about your filesystem,
and a wrong guess prints an empty list, which looks exactly like a quiet day.

## What counts as a priority

A checkbox **and** a level token:

```markdown
- [ ] !1 Ship the installer - traffic lands Wednesday
- [ ] !2 Rewrite the onboarding email
- [x] !1 Fix the login bug          ← ticked, so it stops showing
```

`!1` is what you are working on now, `!2` is next, `!3` is the backlog.
`priorities` shows `!1` and `!2`; `--all` adds `!3`.

Both halves are required because a plan is prose. The same tokens turn up in a
table explaining the convention, or in a fenced example of it — so fenced code
blocks are skipped entirely, and a token without a checkbox is not work.

## The label and the note

Everything before the first separator is the label; the rest is the note,
truncated to fit a column.

| You write | Label | Note |
| --- | --- | --- |
| `- [ ] !1 Ship it — why it matters` | Ship it | why it matters |
| `- [ ] !1 Ship it - why it matters` | Ship it | why it matters |
| `- [ ] !1 Login bug: session drops` | Login bug | session drops |
| `- [ ] !1 Ship the installer` | Ship the installer | — |

An em dash wins wherever it sits on the line. The others need spaces around
them (`: ` needs only the one after), so `3-email bridge` and `2026-08-12` stay
whole.

Bullets may be `-`, `*` or `+`, lines may be indented, and `**bold**` and
`` `code` `` markers are stripped from what is printed.

## Obsidian wikilinks

Optional. If a line names a note, the label comes from the link:

| You write | Label |
| --- | --- |
| `[[Ship & Sell Kit]]` | Ship & Sell Kit |
| `[[projects/kit/_index]]` | kit |
| `[[projects/kit/_index\|The Kit]]` | The Kit |
| `[[Roadmap#v0.2]]` | Roadmap |

`_index` takes its name from its folder, because in a vault where every project
note is called `_index` the filename identifies nothing. A separator inside a
link is part of the title, not a split point.

## When nothing prints

Each empty result says which kind of empty it is, because they want different
reactions:

```
no checkboxes in plan.md — is that the right file?
read 3 checkboxes in plan.md, none tagged !1/!2/!3 — a priority looks like:  - [ ] !1 Ship the thing - why it matters
nothing at !1 or !2 — 2 items further down (--all)
nothing open at !1 or !2 — 2 items done
```

If you keep priorities in another notation (`p1`, `#now`, `(A)`), nothing here
will see them yet. Open an issue saying which one you use.
