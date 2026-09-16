package sdk

import (
	"strings"
	"sync"
)

// Outbound attachment intent for worker-produced file sends (REQ-026).
//
// A worker names a file it wants uploaded by calling the send_attachment
// tool. The tool validates the opaque reference against the session's
// attachment store and records the intent here, keyed by the session ID
// carried on the tool context. HarnessLoop.Entry drains the tracker for the
// turn's session and stamps Output.Metadata[OutAttachmentIDsKey], which the
// Discord transport already consumes via its SendFiles path. File bytes never
// travel through the canonical text path; only comma-joined opaque IDs do.
//
// The tracker is process-local, bounded, and per-turn: Entry clears the entry
// it drains so a retry of the same turn cannot duplicate an upload.
const (
	OutAttachmentIDsKey      = "out_attachment_ids"
	MaxOutboundAttachmentIDs = 10
)

var (
	outboundMu      sync.Mutex
	outboundPending = make(map[string][]string)
)

// RecordOutboundAttachment queues one opaque reference for the session.
// Duplicates are ignored and the list is capped at MaxOutboundAttachmentIDs;
// overflow beyond the cap is dropped, which is the safe direction.
func RecordOutboundAttachment(sessionID, refID string) {
	sessionID = strings.TrimSpace(sessionID)
	refID = strings.TrimSpace(refID)
	if sessionID == "" || refID == "" {
		return
	}
	outboundMu.Lock()
	defer outboundMu.Unlock()
	ids := outboundPending[sessionID]
	for _, existing := range ids {
		if existing == refID {
			return
		}
	}
	if len(ids) >= MaxOutboundAttachmentIDs {
		return
	}
	outboundPending[sessionID] = append(ids, refID)
}

// TakeOutboundAttachmentIDs drains the queued references for the session and
// returns them as a bounded comma-joined list. An empty string means nothing
// was queued, so the caller must leave the plain text path untouched. The
// entry is always cleared, preserving per-turn dedup.
func TakeOutboundAttachmentIDs(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	outboundMu.Lock()
	defer outboundMu.Unlock()
	ids := outboundPending[sessionID]
	delete(outboundPending, sessionID)
	if len(ids) == 0 {
		return ""
	}
	if len(ids) > MaxOutboundAttachmentIDs {
		ids = ids[:MaxOutboundAttachmentIDs]
	}
	return strings.Join(ids, ",")
}

// MergeOutboundAttachmentIDs combines an existing metadata list with newly
// drained IDs, deduplicating and capping at MaxOutboundAttachmentIDs.
func MergeOutboundAttachmentIDs(existing, added string) string {
	seen := make(map[string]bool)
	var out []string
	for _, list := range []string{existing, added} {
		for _, part := range strings.Split(list, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
			if len(out) >= MaxOutboundAttachmentIDs {
				return strings.Join(out, ",")
			}
		}
	}
	return strings.Join(out, ",")
}
