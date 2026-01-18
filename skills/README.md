# Shelley Skills

This directory contains example skills that can be used with the Shelley coordinator.

## Installation

Copy skills to your local skills directory:

```bash
mkdir -p ~/.config/shelley/skills
cp -r skills/* ~/.config/shelley/skills/
```

## Available Skills

### security-review-audit

Full codebase security audit with OWASP Top 10 guidance, language-specific patterns, checklists, and fix examples. Best for comprehensive audits split by module/area.

**Parallel-friendly:** Yes

**Reference docs:**
- `owasp_top_10.md` - OWASP Top 10 detailed reference
- `language_patterns.md` - Python, JS, Go, Java, Ruby vulnerability patterns
- `checklist.md` - Security review checklist
- `common_fixes.md` - Code examples for common fixes

### security-review-pr

PR/branch security review from [Anthropic's claude-code-security-review](https://github.com/anthropics/claude-code-security-review). Focuses on HIGH-CONFIDENCE vulnerabilities with minimal false positives.

**Parallel-friendly:** No (uses git diff, self-parallelizes internally)

**Best for:** CI/CD integration, single PR reviews

## Creating Custom Skills

1. Create a directory under `~/.config/shelley/skills/`
2. Add a `SKILL.md` file with YAML frontmatter:

```yaml
---
name: my-skill
description: What this skill does
parallel_friendly: true
license: MIT
---

# Your skill content here
```

3. Optionally add a `reference/` directory for additional docs

Skills appear in the coordinator dashboard's skill dropdown.
