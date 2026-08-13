---
name: document-decision
description: Use when documenting technical choices (architecture pattern, protocol choice, schema design) - provides structure for recording context, rationale, and alternatives considered in dated folders
---

# Document Architectural Decision

## Overview

Architectural decisions document **why** the system makes specific technical choices. Each decision lives in a dated folder with a lean main document linking to numbered sub-documents.

**High signal only.** Every file must be dense with relevant information. No fluff, no repeated context.

## File Structure

```text
memories/decisions/
└── YYYYMMDD_decision-name/
    ├── DECISION.md                  # Main decision (lean, links to numbered files)
    ├── 01_dimension-name.md         # Dimension: analysis/consideration
    ├── 02_dimension-name.md         # Dimension: another aspect
    ├── 03_dimension-name.md         # Dimension: yet another aspect
    ├── 04_research_topic-name.md    # Research: investigation findings
    └── 05_research_topic-name.md    # Research: additional investigation
```

**Numbered files are either:**
- **Dimensions** - Analysis, considerations, sub-decisions, trade-offs
- **Research** - Investigation notes, benchmarks, exploration findings (prefix with `research_`)

## DECISION.md Template

```markdown
# [Decision Title]

**Decision:** [One sentence: what we chose]

**Rationale:** [One sentence: why we chose it]

## Details

- [Dimension name](./01_dimension-name.md) - Brief
- [Another dimension](./02_dimension-name.md) - Brief
- [Research: Topic](./03_research_topic.md) - Brief
```

## Research File Template

```markdown
# Research: [Topic]

## Investigation

[What we explored, tests conducted, benchmarks run]

## Findings

[Key learnings, data points, measurements]

## Conclusion

[What this means for the decision]
```

## Common Dimension Patterns

While dimensions are flexible, some patterns recur:

**Alternatives comparison:**
```markdown
# [Dimension: Comparing Options]

## Option A (Chosen)
✅ Benefit 1
✅ Benefit 2

## Option B
❌ Rejected because...

## Option C
❌ Rejected because...
```

**Trade-offs analysis:**
```markdown
# [Dimension: Trade-offs]

## Benefits
✅ What we gain

## Costs
⚠️ What we give up

## Mitigations
How we address the costs
```

**Context/Requirements:**
```markdown
# [Dimension: Requirements]

**Problem:** [What we're solving]

**Constraints:**
- [Technical/business constraints]

**Non-negotiables:**
- [Must-haves]
```

## Examples

Different decisions have different aspects. Mix dimensions and research as needed:

**Framework selection:**
- `01_performance-requirements.md` (dimension) - Latency, throughput needs
- `02_ecosystem-comparison.md` (dimension) - Library availability, community
- `03_migration-cost.md` (dimension) - Effort to migrate
- `04_research_benchmark-results.md` (research) - Load testing 3 frameworks

**Database choice:**
- `01_data-model-fit.md` (dimension) - Relational vs document model
- `02_scale-requirements.md` (dimension) - Read/write patterns, volume
- `03_operational-overhead.md` (dimension) - Maintenance, monitoring
- `04_research_postgres-benchmarks.md` (research) - Performance testing results

**Architecture pattern:**
- `01_consistency-vs-availability.md` (dimension) - CAP theorem trade-offs
- `02_failure-modes.md` (dimension) - What can go wrong
- `03_evolution-path.md` (dimension) - How this enables future changes
- `04_research_chaos-testing.md` (research) - Fault injection findings

## Naming Convention

**Folder name:** `YYYYMMDD_kebab-case-decision-name/`

**Examples:**

- ✅ `20251210_dbos-durability/`
- ✅ `20251201_use-postgresql/`
- ✅ `20251115_grpc-vs-rest/`
- ❌ `dbos-durability/` (missing date)
- ❌ `2025-12-10-dbos/` (wrong date format, use YYYYMMDD)

**Why date prefix?**

- Chronological ordering
- Context of when decision was made
- Easy to find latest decisions

## What Goes Here

✅ **Technical choices:**

- "Why use DBOS for HITL durability"
- "Why PostgreSQL over MongoDB"
- "Why gRPC over REST for internal communication"

✅ **Architectural patterns:**

- "Why event sourcing for audit trail"
- "Why DAG-over-shared-store for check composition"

❌ **NOT for:**

- Feature requirements (belongs in features/)
- Implementation details (belongs in component repo)

## Writing Guidelines

**High signal only:**
- Every sentence must add new information
- No repeated context between files
- No filler phrases or preambles
- Dense, technical, specific

**File scope:**
- Each file covers one aspect completely
- Reference other files, don't duplicate
- If a file becomes too large, split into sub-aspects

## Common Mistakes

| Mistake | Fix |
|---------|-----|
| Low signal content | Cut fluff, keep only essential technical info |
| Repeating context across files | Reference other files instead |
| Missing date prefix | Use YYYYMMDD_ format |
| DECISION.md directly in decisions/ | Create YYYYMMDD_name/ folder first |
| Mixing implementation and decision | Decision = why, implementation = how (repo) |
| Too broad | Keep focused on one decision |
