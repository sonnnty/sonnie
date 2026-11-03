package readaloud

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"
)

const (
	// bulkChunk is Discord's limit of messages per bulk-delete request.
	bulkChunk = 100
	// bulkMaxAge is how old a message may be for bulk deletion. Discord's
	// limit is 14 days; the hour of margin keeps a message from crossing it
	// between selection and the request.
	bulkMaxAge = 14*24*time.Hour - time.Hour
)

// CleanupResult says what deleting a session's messages achieved.
type CleanupResult struct {
	// Deleted counts messages removed; Failed those that could not be.
	Deleted, Failed int
	// NoPermission is set when deletion was skipped because the bot lacks
	// Manage Messages.
	NoPermission bool
}

// cleanupJob describes which messages a finished session leaves behind.
type cleanupJob struct {
	ChannelID        string
	SessionMessageID string
	// Started is when the session message was created, Ended when the session
	// ended. Only messages in between are deleted.
	Started, Ended time.Time
	// Participants is everyone who was in the session at any time.
	Participants map[string]bool
	// Tracked are the participant messages seen live (ID to creation time).
	// They are the fallback when the history scan fails.
	Tracked map[string]time.Time
}

type candidate struct {
	id   string
	time time.Time
}

// cleanupMessages deletes the messages participants posted in the session's
// text channel between the session message and the end of the session. The
// history scan is authoritative: it sees messages the bot missed (a restart,
// a dropped event) and leaves out ones people already deleted themselves. If
// the scan fails or is incomplete, the live-tracked IDs are added so nothing
// known is left behind.
//
// Messages younger than 14 days go through bulk delete in chunks of at most
// 100; older ones, and single leftovers, one at a time. Nothing from other
// authors, other channels, before the session message, after its end, or the
// bot's own session message is ever selected.
func cleanupMessages(ctx context.Context, msgr Messenger, job cleanupJob, now time.Time) CleanupResult {
	cands := make(map[string]candidate)
	add := func(id string, t time.Time) {
		if id == job.SessionMessageID || t.Before(job.Started) || t.After(job.Ended) {
			return
		}
		cands[id] = candidate{id: id, time: t}
	}

	scanned, err := msgr.MessagesAfter(ctx, job.ChannelID, job.SessionMessageID)
	for _, m := range scanned {
		if job.Participants[m.AuthorID] {
			add(m.ID, m.Time)
		}
	}
	if err != nil {
		log.Printf("tts: clean-up in channel %s: history scan failed, using the %d messages seen live: %v",
			job.ChannelID, len(job.Tracked), err)
		for id, t := range job.Tracked {
			add(id, t)
		}
	}

	var bulk, single []candidate
	cutoff := now.Add(-bulkMaxAge)
	for _, c := range cands {
		if c.time.After(cutoff) {
			bulk = append(bulk, c)
		} else {
			single = append(single, c)
		}
	}
	sortCandidates(bulk)
	sortCandidates(single)

	var res CleanupResult
	for len(bulk) > 0 {
		n := min(len(bulk), bulkChunk)
		chunk := bulk[:n]
		bulk = bulk[n:]
		if n == 1 {
			single = append(single, chunk...)
			continue
		}
		ids := make([]string, n)
		for i, c := range chunk {
			ids[i] = c.id
		}
		if err := msgr.BulkDelete(ctx, job.ChannelID, ids); err != nil {
			log.Printf("tts: clean-up in channel %s: bulk delete of %d messages failed, deleting one by one: %v",
				job.ChannelID, n, err)
			single = append(single, chunk...)
			continue
		}
		res.Deleted += n
	}

	for i, c := range single {
		if ctx.Err() != nil {
			res.Failed += len(single) - i
			break
		}
		switch err := msgr.DeleteMessage(ctx, job.ChannelID, c.id); {
		case err == nil:
			res.Deleted++
		case errors.Is(err, ErrMessageGone):
			// Someone else got there first.
		default:
			log.Printf("tts: clean-up in channel %s: delete message %s: %v", job.ChannelID, c.id, err)
			res.Failed++
		}
	}
	return res
}

func sortCandidates(cs []candidate) {
	sort.Slice(cs, func(i, j int) bool {
		if !cs[i].time.Equal(cs[j].time) {
			return cs[i].time.Before(cs[j].time)
		}
		return cs[i].id < cs[j].id
	})
}
