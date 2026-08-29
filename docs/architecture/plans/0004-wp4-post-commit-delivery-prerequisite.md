# ADR 0004 WP4 post-commit delivery prerequisite

WP4 separates authoritative database changes from existing notification,
webhook, and indexing fanout. A newly applied mutation returns inert effects
from the receipt transaction and dispatches them only after that transaction
commits. Unchanged, rejected, rolled-back, cancelled, and replayed operations
dispatch nothing.

The current notifier interface is synchronous, process-local, and returns no
delivery acknowledgement. A process failure after the database commit and
before or during notifier fanout therefore has no durable fact from which to
recover delivery. WP4 can prove post-commit ordering and replay suppression,
but it cannot prove the ADR's at-least-once delivery property across that crash
window without a durable commit-to-delivery bridge.

That bridge is a separate prerequisite for enabling MCP Work mutation in a
configuration that requires crash-safe at-least-once effects. It must retain
stable repository, Project, Issue, event, and revision identifiers; dispatch
only after the authoritative commit; let consumers re-read current state; and
record delivery progress durably enough for retry. It must not store copied
Work projections, choose an executor, or turn notification failure into a
domain rollback. WP4 deliberately does not introduce a general outbox or claim
that the existing notifier satisfies this prerequisite.
