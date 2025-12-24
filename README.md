
# craft – Code Review As File Tool

## what is this?

It's a thing I made to let me do code reviews in my editor (Vim), instead of the
awful GitHub review UI. There are various solutions for this, but none of them
worked the way I wanted.

## what's the big idea?

The idea is to accurately represent the full state of the code review in special
comments in the code itself, bidirectionally. So you can just browse and
navigate the new code as you would normally, see existing comments, add new
comments, and then sync that to the PR as a review pass.

The special comments are marked with special characters (unicode box-drawing
characters) to differentiate from normal comments. This implies you need a
little bit of editor integration to use it effectively. The integration is
currently implemented for Vim.

Even "outdated" and "resolved" comments are reflected in the code somewhere.
This is sometimes slightly annoying on long reviews, but it means you can still
reply to them.

## how do I install it?

```
go install github.com/dnr/craft@main
```

Then install the Vim integration with your favorite Vim plugin manager.
It's in the [`./vim`](./tree/vim) directory.

The Vim integration depends on
[vim-fugitive](https://github.com/tpope/vim-fugitive),
so make sure that's installed too.

It optionally integrates with
[vim-gitgutter](https://github.com/airblade/vim-gitgutter).

## how do I use it?

The basic flow goes like this:

In your repo, run `craft get 8765`.
This gets the code and PR description and comments so far.

Then run `vim`. In Vim, start by running `:Ctool`. This figures out the base
commit, sets up some stuff, and runs fugitive's `:G difftool`. You can now
navigate among the PR diffs and existing comments. If you want to switch the
base commit, use `:Cbase <commit>`.

When you're looking at a diff, you can type `<Leader>D` to open a diffsplit with
the base.

To add a comment, type `<Leader>C` and start typing. Format options should be
set to keep the special formatting looking nice. You can use full markdown in
your comments. Lines are re-wrapped so won't worry about wrapping, it should
work intuitively.

(Note that due to GitHub limitations, all new code comments must be within a few
lines of code changes, you can't just add them anywhere.)

You can also add review-level comments in the `PR-STATE.txt` file. (The editor
integration is broken for this right now, will fix soon.)

When you've added all your comments, run `craft send`. `send` accepts flags:

- `--dry-run`: just print, don't send
- `--approve`: mark as approved
- `--request-changes`: mark as changes requested
- `--pending`: create comments and leave review in pending state

That's pretty much it.

To get the next round, just run `craft get` again.

## who wrote it?

Well, 98% was written by Claude. I did direct it very specifically and review it
pretty carefully. It's an experiment with heavy AI coding, and it worked well
for a tool like this, though I'm still hesitatant using it for larger systems.
The organization of the code has suffered a little through evolution, I'll clean
it up at some point.

The README is 100% human-written.

## other questions

**Is it specific to GitHub?**
Yes, although it's not that much of a stretch to imagine adapting it to other
review systems.

**Is it specific to Vim?**
I've only written integrations for Vim, but it should be easy to adapt them for
any editor. Because of the special characters used, you do need a little editor
integration.

**How does it interact with my VCS?**
Git and Jujutsu are supported. I've mainly been using Jujutsu. In Git, it
creates/resets a branch for each PR and does everything there. In Jujutsu, it
creates changes on top of the PR head and tries to clean up old ones.

## future work and ideas

- Be able to mark threads as resolved.
- Be able to add emoji reactions.
- Make it easy to handle inter-diffs: diffs of one revision of a PR to another,
  that ignore irrelevant merges and even rebases.
- Automatically handle rebasing comments on top of changed code, if the PR
  changes while you're reviewing.
- Allow small edits to the code and automatically turn changes into "suggestion"
  comments.

