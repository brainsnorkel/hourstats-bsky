# Agent Instructions

This project uses **bd** (beads) for issue tracking. Run `bd onboard` to get started.

## Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --status in_progress  # Claim work
bd close <id>         # Complete work
bd sync               # Sync with git
```

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds

## Architecture diagrams 

Use **mermaid-ascii** ([github.com/AlexanderGrooff/mermaid-ascii](https://github.com/AlexanderGrooff/mermaid-ascii)) to render architecture diagrams as ASCII art in design docs, proposals, and code comments. This keeps diagrams version-controlled, diffable, and readable without a renderer.

```bash
# Install
pip install mermaid-ascii

# Render from a .mmd file
mermaid-ascii < diagram.mmd

# Render inline
echo 'graph LR; A-->B;' | mermaid-ascii
```

**When to use:**
- Proposals and design docs (`openspec/`) — include rendered ASCII alongside or instead of Mermaid source
- Code comments where a visual helps (data flow, state machines)
- PR descriptions explaining architectural changes

**Convention:** Keep the Mermaid source in a fenced `mermaid` block, followed by the rendered ASCII in a fenced `text` block. This gives both the editable source and the readable output.
