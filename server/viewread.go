package main

import (
	"github.com/mattermost/mattermost/server/public/model"
)

// Read receipts normally depend on the webapp calling POST /api/v1/read when a
// post scrolls into view. The mobile apps never load webapp plugins, so a
// recipient reading on their phone would leave the author on one tick forever.
//
// Every client does update ChannelMember.LastViewedAt though — it is what
// drives the unread badges — so a read can be inferred from that instead. There
// is no hook for it, so this runs when the author asks for statuses; the author
// sees the second tick on their next hydration (focus, reconnect or channel
// switch) rather than instantly.
//
// The claim it makes is "viewed the conversation after this message arrived",
// which is not quite "read this message". In a direct message that gap is
// small, so it is always on. In a channel it is wider, so it is an admin
// setting, and it is skipped entirely for channels above maxViewSyncMembers:
// loading a thousand memberships on every status request would cost far more
// than the receipt is worth.

// maxViewSyncMembers bounds the memberships one channel lookup may load. It
// matches maxReadBy, since readers past that cap are dropped anyway.
const maxViewSyncMembers = maxReadBy

// viewLookup answers "who has looked at this conversation since this post" for
// the posts in one request, caching per channel so a screenful of one
// conversation costs a single set of API calls.
type viewLookup struct {
	plugin *Plugin

	// channelID -> reader user ID -> LastViewedAt. A nil map marks a channel
	// this sync does not apply to, so it is only decided once.
	views map[string]map[string]int64
}

func newViewLookup(p *Plugin) *viewLookup {
	return &viewLookup{
		plugin: p,
		views:  map[string]map[string]int64{},
	}
}

// eligible reports whether view times may stand in for read receipts here, and
// is the only place the DM/channel policy difference lives.
func (l *viewLookup) eligible(channel *model.Channel) bool {
	if channel.Type == model.ChannelTypeDirect {
		return true
	}

	if !l.plugin.getConfiguration().channelReadSyncEnabled() {
		return false
	}

	if channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate {
		// Group messages carry no member cap of their own; treat them like
		// channels and let the member count below decide.
		if channel.Type != model.ChannelTypeGroup {
			return false
		}
	}

	// Ask for the size before pulling the memberships themselves.
	stats, appErr := l.plugin.API.GetChannelStats(channel.Id)
	if appErr != nil {
		l.plugin.API.LogDebug("Skipping view-based read sync", "channel_id", channel.Id, "error", appErr.Error())
		return false
	}

	return stats.MemberCount <= maxViewSyncMembers
}

// viewTimes returns each potential reader's last view time for a channel,
// excluding the author, or nil when the sync does not apply.
func (l *viewLookup) viewTimes(channelID, authorID string) map[string]int64 {
	if cached, ok := l.views[channelID]; ok {
		return cached
	}

	l.views[channelID] = nil

	channel, appErr := l.plugin.API.GetChannel(channelID)
	if appErr != nil {
		l.plugin.API.LogDebug("Skipping view-based read sync", "channel_id", channelID, "error", appErr.Error())
		return nil
	}

	if !l.eligible(channel) {
		return nil
	}

	members, appErr := l.plugin.API.GetChannelMembers(channelID, 0, maxViewSyncMembers)
	if appErr != nil {
		l.plugin.API.LogDebug("Skipping view-based read sync", "channel_id", channelID, "error", appErr.Error())
		return nil
	}

	viewed := make(map[string]int64, len(members))
	for _, member := range members {
		// The author reading their own post is not a receipt. This also covers
		// a self-DM, where the author is the only member.
		if member.UserId == authorID || !model.IsValidId(member.UserId) {
			continue
		}
		viewed[member.UserId] = member.LastViewedAt
	}

	if len(viewed) == 0 {
		return nil
	}

	l.views[channelID] = viewed
	return viewed
}

// readersOf returns everyone whose channel view has moved past this post.
func (l *viewLookup) readersOf(status *PostStatus) []string {
	// A record written before CreatedAt existed has nothing to compare against,
	// so it stays on whatever the webapp reported.
	if status == nil || status.CreatedAt <= 0 {
		return nil
	}

	var readers []string
	for userID, viewedAt := range l.viewTimes(status.ChannelID, status.AuthorID) {
		if viewedAt >= status.CreatedAt {
			readers = append(readers, userID)
		}
	}

	return readers
}

// syncViewedReads promotes a delivered post to read for everyone who has viewed
// the conversation since it arrived, and persists that so the reader list and
// the websocket update stay consistent with the webapp-driven path. It returns
// the status to report, never nil for a non-nil input.
func (p *Plugin) syncViewedReads(status *PostStatus, lookup *viewLookup) *PostStatus {
	if status == nil || status.DerivedStatus() == "read" {
		return status
	}

	readers := lookup.readersOf(status)
	if len(readers) == 0 {
		return status
	}

	// seed is nil: if the entry expired between the read above and now, there is
	// nothing to resurrect.
	updated, appErr := p.appendReaders(status.PostID, readers, nil)
	if appErr != nil {
		p.API.LogWarn("Failed to record view-based read", "post_id", status.PostID, "error", appErr.Error())
		return status
	}

	if updated == nil {
		return status
	}

	return updated
}
