# Refactoring guidelines

This document covers expectations for refactoring work. For the general workflow see
[CONTRIBUTING.md](../CONTRIBUTING.md).

## Background

Forge inherited a large, long-lived codebase. Over time that codebase accumulated
outdated mechanisms, mixed frameworks, and legacy code that can cause bugs or slow
down new features. Refactoring keeps the codebase maintainable, but it needs to be
done carefully so it improves things without introducing regressions.

## Writing a refactoring PR

- Be forward-looking: address the root cause, not just the immediate symptom.
- Aim to reduce ambiguity and conflicts and to improve maintainability.
- Explain the rationale in the PR description: why the refactor is necessary, how it
  resolves the legacy problem, and its advantages and disadvantages.
- Keep the scope tight: preserve existing behavior where feasible and avoid bundling
  unrelated changes.
- Break large refactors into intermediate steps across multiple PRs so each one is
  easy to review.
- Include tests that verify the behavior stays correct.
- Prefer scheduling non-bugfix refactoring early in a milestone, so any issues
  surface well before a release.
- If there is disagreement about a refactor, ask the Forge maintainer for a
  decision.

## Reviewing and merging

- Keep refactoring PRs short-lived (typically no more than 7 days) with quick review
  cycles, and merge them promptly so they do not block on unrelated work.
- The Forge maintainer decides when a refactoring change is ready to merge.
- Accept imperfect intermediate implementations as long as the final result improves
  the codebase.
- A temporary regression caused by a necessary refactor is acceptable if it is fixed
  promptly afterwards.
