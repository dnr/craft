# craft — code review tool

- goal: use github code review without using the awful github ui
- idea is to make it editor-agnostic, code review state is represented explicitly in file(s)
- code under review is locally navigable, buildable, testable
- github code review comments appear as special comments in the code
- there can be editor integration to easily create new comments, reply to
  existing ones, approve, etc.
- this means the local files won't match the branch exactly, and the line number will be off
  - Q: is that going to cause any problems?
- general flow
  - `craft get [<pr#>]`
    - assume we're already in a git repo with github as a remote
    - pulls head of PR branch into a local branch named for pr number (if already on a branch, can use just `get` to pull latest changes)
    - switches to that branch (abort if local changes)
    - gets inline PR comments, integrates them into comments in the code
    - gets general PR comments, puts in PR-COMMENTS.txt in repo root (along with sufficient metadata to send the review)
  - user: opens files in vim, use fugitive difftool/diffsplit, add new comments
  - `craft send`
    - loads PR-COMMENTS.txt, and loads all review comments from source files
    - figures out what api calls to make to send everything
    - prints out what will be sent
    - if given `--go`, sends it
    - saves new meta to PR-COMMENTS.txt if necessary
- decisions made
  - **API**: GitHub REST API (more stable/documented than GraphQL)
  - **Language**: Go (easier static binaries, good GitHub API support)
  - **Comment format**: Use `❯` as delimiter, e.g. `// ❯ content goes here`
    - Still valid language comments, but visually distinctive
    - Content after `❯` can use prefixes to distinguish types:
      - Comment headers, bodies, new comments to submit, approve/reject directives
    - Avoids conflicts with existing code using fancy unicode
  - **Architecture**: Use intermediate representation for file+comments
    - Parse files to extract existing embedded comments and clean source
    - Sync with GitHub API comments (add new, update existing)
    - Serialize back to disk with embedded comments
  - **Authentication**: Read from `~/.config/gh/hosts.yml` if available, fallback to `GITHUB_TOKEN`
  - **Configuration**: Use git config `craft.remoteName` to specify remote (defaults to "origin")
- references
  - https://github.com/shurcooL/githubv4 - graphql client for go

- **Comment Header Format**: Extended structured format with key-value fields
  - Format: `5+ RuleChars ─ field1 ─ field2 ─ ... ─ 5+ RuleChars`
  - Fields: `by author`, `date YYYY-MM-DD HH:MM`, `id 12345`, `parent 67890`, `range -N`, `file`, `new`
  - Boolean fields (`file`, `new`) can omit value (assumed true)
  - Examples:
    - Line comment: `───── by alice ─ date 2025-01-01 12:34 ─ id 543216 ─────`
    - File-level: `───── by bob ─ date 2025-01-01 12:34 ─ file ─ id 543216 ─────`
    - Range comment: `───── by carol ─ date 2025-01-01 12:34 ─ range -12 ─ id 543216 ─────`
    - Reply: `───── by dave ─ date 2025-01-01 12:34 ─ id 543216 ─ parent 284834 ─────`
    - New comment: `───── new ─────`
  - Enables proper sync, editing, and replies while keeping everything in-file

TODO:

- make `craft get` a sync operation (preserve existing new comments) using IDs
- use the `reviews` endpoint: `POST /repos/{owner}/{repo}/pulls/{pull_number}/reviews` to submit multiple comments at once
- make `craft send` able to approve/request-changes
- support editing existing comments (mark with `new` + original `id`)
- support reply comments (use `parent` field)
- support the `since` parameter to quickly fetch only new/updated comments on a PR
  (use the latest timestamp of any comment in any file as the since)

