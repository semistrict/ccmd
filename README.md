# ccmd

A CLI tool for browsing, reading, and managing Claude Code session transcripts.

Parses the JSONL session files from `~/.claude/projects/` and renders them as formatted Markdown. Includes an interactive TUI browser and a smart context-compaction wrapper for Claude Code.

## Install

```
go install github.com/semistrict/ccmd@latest
```

Or try it without installing:

```
go run github.com/semistrict/ccmd@latest
```

## Usage

### Interactive session browser

```
ccmd
```

Opens a TUI with all sessions sorted by recency. Keyboard shortcuts:

| Key | Action |
|-----|--------|
| `↑↓` / `jk` | Navigate sessions |
| `enter` | Read session (rendered Markdown) |
| `c` | Continue session (resumes in Claude Code) |
| `f` | Fork session (resumes as a new branch) |
| `s` | Summary mode (one line per turn) |
| `/` | Filter by text (matches preview, project, UUID, path) |
| `p` | Toggle current project / all projects |
| `y` | Copy session UUID to clipboard |
| `q` | Quit |

The bottom pane shows the last few messages of the selected session. Token usage is displayed in the footer after loading.

When piped, falls back to a plain-text table:

```
ccmd | cat
```

### Render a session

```
ccmd <number>           # by position in the list
ccmd <uuid>             # by session UUID
ccmd <path>             # by file path
ccmd -s <uuid>          # summary mode
ccmd -from 5 -to 10 1   # turns 5-10 of the most recent session
ccmd -o out.md 1        # write to file
ccmd -no-thinking 1     # hide thinking blocks
```

If [glow](https://github.com/charmbracelet/glow) is installed, output is automatically piped through it with a pager.

### Claude Code wrapper

```
ccmd claude             # starts claude with --dangerously-skip-permissions
ccmd claude -c          # continue most recent session
ccmd claude <any args>  # all args passed through to claude
```

Runs Claude Code as a child process with fastcompact hooks automatically injected via `--settings`. No manual hook installation needed -- when a hook fires, it signals the parent `ccmd` process which handles the restart cleanly (no terminal corruption).

### Fastcompact

Renders a full session transcript and starts a new Claude Code instance with it as context, replacing the built-in compaction with a lossless alternative.

```
ccmd fastcompact              # most recent session in current project
ccmd fastcompact <uuid>       # specific session
```

Can also be triggered interactively:
- Type `fastcompact` as a prompt in Claude Code (when running under `ccmd claude`)
- When Claude Code hits its context limit, a confirmation dialog appears

## How fastcompact works

1. The `PreCompact` hook fires when Claude Code is about to compact context
2. The hook signals the parent `ccmd claude` process via SIGUSR1
3. `ccmd` kills the Claude Code child process and shows a confirmation dialog
4. If confirmed, it renders the full session transcript as Markdown (truncated to last 200KB if needed)
5. A new Claude Code instance starts with the rendered transcript as its initial prompt

The `UserPromptSubmit` hook intercepts the word `fastcompact` typed as a prompt, blocks it, and triggers the same flow without confirmation.

## Output format

Sessions are rendered as Markdown with:
- Session metadata header (date, project, branch, version)
- Numbered turns (`## [1] User`, `## [2] Claude`)
- Tool calls with compact summaries (file paths shortened, inputs abbreviated)
- Edit diffs in fenced code blocks
- Subagent conversations nested with blockquote prefixes
- Thinking blocks (optional, hidden by default)
