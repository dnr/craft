# craft — code review tool

- Goal: use github code review without using the awful github ui
- UX
  - idea is to make it editor-agnostic, code review state is represented explicitly in files
  - code under review is locally navigable, buildable, testable
  - github code review comments appear as special comments in the code
  - there can be editor integration to easily create new comments, reply to
    existing ones, approve, etc.
  - this means the local files won't match the branch exactly, and the line numbers will be off
  - general flow
    - `craft get [<pr#>]`
      - assume we're already in a git repo with github as a remote
      - pulls head of PR branch into a local branch named for pr number (if already on a branch, can use just `get` to pull latest changes)
      - switches to that branch (abort if local changes)
      - gets inline PR comments, integrates them into comments in the code
      - gets general PR comments, puts in PR-STATE.txt in repo root (along with sufficient metadata to send the review)
    - user: opens files in vim, use fugitive difftool/diffsplit, add new comments
    - `craft send`
      - loads all review comments from source files, plus PR-STATE.txt
      - figures out what api calls to make to send everything
      - prints out what will be sent
      - if given `--go`, sends it
      - saves new metadata to PR-STATE.txt if necessary
- Decision record
  - **API**: GitHub GraphQL API (more features than REST)
  - **Language**: Go (easier static binaries, good GitHub API support)
  - **Version control**:
    - Assume git for most operations, but consider that we want good support for jj later
  - **Design**:
    - Based on **bidirectional sync**!
    - The data embedded in files should be a _complete_ representation of the PR
      state, including an in-progress review if it exists
    - e.g. comments that are edited by another user should be fetched and
      updated in the code
    - (depending on how the GraphQL api works, there _may_ need to be a special
      flag for "full sync", since we want the regular sync to be fast, i.e. use
      "get new data since last fetch)
    - Adding review comments is done by the user creating a new comment and then
      running the sync to push
    - e.g. the user should be able to edit existing comments and push those with
      the sync also
    - (depending on how the api works, a user editing existing comments _may_ be
      required to mark them as edited so the sync knows to compare+update them)
  - **Data model**:
    - For testability, we need to build this in two halves:
      - Top half: exactly **sync** PR state and all reviews into a local model
        (Go structs), and sync the local model to a GitHub PR using GraphQL
        mutations
      - Bottom half: completely **serialize** the local model into comments in
        source code, and **deserialize** our special comments into the local
        model
      - Additionally, the local model can be serialized as JSON for debugging and testing
    - Local model details (see `model.go`):
      - `PullRequest` - top level, contains metadata + all review data
      - `ReviewThread` - a thread anchored to a file/line, contains comments
      - `ReviewComment` - a single comment within a thread
      - `IssueComment` - general PR comment (not attached to code)
      - `Review` - a formal review submission (PENDING, COMMENTED, APPROVED, CHANGES_REQUESTED)
      - Comments have `IsNew` and `IsModified` flags for tracking local changes
  - **Comment format**:
    - All craft data is embedded in code comments
    - Use only line comments to keep things simple for now
    - All craft data has an additional prefix after the line comment marker
    - Use `❯` as a prefix, e.g. `// ❯ content goes here` for Go code
      - Still valid language comments, but easy to distinguish and avoids misinterpretation
    - Content is organized as a series of records
    - Each record starts with a header, and ends at the next header or first line that isn't craft data
    - As in the GitHub UI, review comments appear right _below_ the line they apply to
    - See "Comment Header Format" below for the header format
  - **Authentication**:
    - First check `GITHUB_TOKEN` env var
    - Otherwise read `~/.config/gh/hosts.yml` to get username, then use
      `github.com/zalando/go-keyring` to read token from system keyring
      (service=`gh:github.com`, user=username from hosts.yml)
    - Older gh versions stored `oauth_token` directly in hosts.yml (still supported)
  - **Configuration**:
    - Use git config `craft.remoteName` to specify remote (defaults to "origin")
    - Get the GH repo from the git remote config
    - Get the PR number from the branch name (pr-123), or store in PR-STATE.txt
- References
  - https://github.com/shurcooL/githubv4 - graphql client for go
  - the vscode extension uses graphql to do the same thing:
    - cloned locally in `references/vscode-pull-request-github`
    - specifically see:
      - src/view/reviewCommentController.ts around line 722
      - src/github/pullRequestModel.ts around lines 689, 722, 785
      - src/github/queriesShared.gql - all the GraphQL queries and mutations
    - use this in addition to the graphql api reference to see how actual
      working code uses it

- **GraphQL API notes** (see `github.go`, `debugsend.go`):
  - **Fetching PR data**:
    - Single query gets PR metadata + first page of reviewThreads, comments, reviews
    - All connections use cursor-based pagination (`first: 100, after: $cursor`)
    - Must paginate: reviewThreads, issueComments, reviews, and comments within each thread
    - For nested pagination (comments in thread), use `node(id: $threadId)` query
    - No `since` filter on reviewThreads/comments - must fetch all and diff locally
    - `DatabaseID` fields can exceed int32, use `int64` in Go
  - **Creating comments** (mutations):
    - All comments must be part of a review (pending or submitted)
    - Flow: `addPullRequestReview` → add comments → `submitPullRequestReview`
    - `addPullRequestReviewThread`: creates new thread, takes `pullRequestReviewId`
    - `addPullRequestReviewComment`: replies to existing comment, requires `pullRequestReviewId` and `inReplyTo` (node ID, not database ID)
    - `submitPullRequestReview`: takes `event` = COMMENT, APPROVE, or REQUEST_CHANGES
    - Check for existing pending review before creating new one (user can only have one)
  - **Gotchas**:
    - Review comments must be within 3 lines of the diff (GitHub restriction)
    - `githubv4` input structs use pointers for optional fields
    - `githubv4.ID` is an interface, assign with `githubv4.ID(stringValue)`

- **Comment Header Format**:
  - Light structured format with key/value fields
  - The following describes text after stripping the code comment character and `❯` prefix
  - Format: `───── field1 ─ field2 ─ ... ─────`
  - Field format: `key [value]`
  - Fields: `by author`, `at YYYY-MM-DD HH:MM`, `id 12345`, `parent 67890`, `range -N`, `file`, `new`
  - Boolean fields (`file`, `new`) can omit value (assume `true`)
  - Examples:
    - Line comment header: `───── by alice ─ at 2025-01-01 12:34 ─ id 543216 ─────`
    - File-level: `───── by bob ─ at 2025-01-01 12:34 ─ file ─ id 543216 ─────`
    - Range comment: `───── by carol ─ at 2025-01-01 12:34 ─ range -12 ─ id 543216 ─────`
    - Reply: `───── by dave ─ at 2025-01-01 12:34 ─ id 543216 ─ parent 284834 ─────`
    - New comment: `───── new ─────`

