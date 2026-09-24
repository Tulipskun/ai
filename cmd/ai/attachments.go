package main

import (
	"context"
	"log"
	"time"

	"github.com/Tulipskun/ai/runtime/filestore"
	"github.com/Tulipskun/ai/tools"
)

// This file owns the runtime wiring of the attachment file store: one store
// opened from config/attachment.json and handed to the worker registry for
// reads, plus the TTL sweep that keeps the store bounded (REQ-026, CON-011).
// The Discord intake/upload side of the boundary was removed with the transport
// (CHANGE-059); the mobile gateway stays text-only.
//
// Nothing here moves file content. Every consumer receives either the store
// itself or an opaque reference, so the canonical text path stays byte-free
// (CON-004).

// attachmentCleanupInterval is how often expired attachments are swept. It is a
// runtime maintenance cadence, not a user-facing limit, so it stays a named
// constant rather than a config key: the budget it enforces (ttl) is configured,
// the polling rate is not (CON-001).
const attachmentCleanupInterval = time.Hour

// attachmentCleaner is the slice of the store the sweep needs, kept narrow so
// the loop is testable without a filesystem.
type attachmentCleaner interface {
	Cleanup(ctx context.Context, now time.Time) (int, error)
}

// attachmentToolStore adapts the store for the worker registry. A nil store
// returns a nil interface, which is what makes the attachment tools report
// "attachment store is not configured" instead of panicking when
// config/attachment.json is disabled.
//
// The explicit nil check matters: a typed-nil *filestore.Store inside a
// non-nil interface would defeat every consumer's nil test.
func attachmentToolStore(store *filestore.Store) tools.AttachmentStore {
	if store == nil {
		return nil
	}
	return store
}

// startAttachmentCleanup runs the TTL sweep in the background for the life of
// the runtime and returns immediately. The goroutine stops when ctx is
// cancelled, so shutdown never has to join it, and one sweep at startup reclaims
// what a previous run left behind.
func startAttachmentCleanup(ctx context.Context, store *filestore.Store, interval time.Duration) {
	if store == nil {
		return
	}
	if interval <= 0 {
		interval = attachmentCleanupInterval
	}
	go runAttachmentCleanup(ctx, store, interval)
}

// runAttachmentCleanup sweeps once immediately, then on every tick. A failed
// sweep is logged and retried on the next tick rather than ending the loop,
// because attachments are bounded by the store's own size limits as well, so a
// transient error must not turn the store into an unbounded one silently.
func runAttachmentCleanup(ctx context.Context, store attachmentCleaner, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sweepAttachments(ctx, store)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepAttachments(ctx, store)
		}
	}
}

func sweepAttachments(ctx context.Context, store attachmentCleaner) {
	removed, err := store.Cleanup(ctx, time.Now().UTC())
	if err != nil {
		log.Printf("attachment cleanup failed: %v", err)
		return
	}
	if removed > 0 {
		log.Printf("attachment cleanup removed %d expired file(s)", removed)
	}
}
