package main

import (
	"github.com/mattermost/mattermost/server/public/model"
)

// Read receipts normally depend on the webapp calling POST /api/v1/read when a
// post scrolls into view. The mobile apps never load webapp plugins, so a
// recipient reading on their phone would leave the author on one tick forever.
//
// Every client does update ChannelMember.LastViewedAt though — it is what
// drives the unread badges — so for a direct message we can infer the read from
// that instead. There is no hook for it, so this runs when the author asks for
// statuses; the author sees the second tick on the next hydration (focus,
// reconnect or channel switch) rather than instantly.
//
// This is deliberately limited to DMs. In a channel it would mean loading every
// member on every status request, and "opened the channel" is a much weaker
// claim about a specific message than it is in a one-to-one conversation.

// dmViewLookup answers "is this a DM, and when did the other person last look
// at it" for the posts in one request, caching per channel so a screenful of
// posts in one conversation costs a single pair of API calls.
type dmViewLookup struct {
	plugin *Plugin
	peers  map[string]string
	views  map[string]int64
}

func newDMViewLookup(p *Plugin) *dmViewLookup {
	return &dmViewLookup{
		plugin: p,
		peers:  map[string]string{},
		views:  map[string]int64{},
	}
}

// peer returns the other participant of a direct channel, or "" when the
// channel is not a DM, is unreadable, or is the user's own self-DM.
func (l *dmViewLookup) peer(channelID, userID string) string {
	if cached, ok := l.peers[channelID]; ok {
		return cached
	}

	peerID := ""
	channel, appErr := l.plugin.API.GetChannel(channelID)
	switch {
	case appErr != nil:
		l.plugin.API.LogDebug("Skipping direct-message read sync", "channel_id", channelID, "error", appErr.Error())
	case channel.Type == model.ChannelTypeDirect:
		// A self-DM names the same user on both sides; nobody else can read it.
		if other := channel.GetOtherUserIdForDM(userID); other != userID {
			peerID = other
		}
	}

	l.peers[channelID] = peerID
	return peerID
}

// lastViewedAt reports when peerID last looked at the channel, or 0 if unknown.
func (l *dmViewLookup) lastViewedAt(channelID, peerID string) int64 {
	if cached, ok := l.views[channelID]; ok {
		return cached
	}

	viewedAt := int64(0)
	member, appErr := l.plugin.API.GetChannelMember(channelID, peerID)
	if appErr != nil {
		l.plugin.API.LogDebug("Skipping direct-message read sync", "channel_id", channelID, "error", appErr.Error())
	} else {
		viewedAt = member.LastViewedAt
	}

	l.views[channelID] = viewedAt
	return viewedAt
}

// hasSeen reports whether the other side of a DM has viewed the conversation at
// or after the moment this post was created.
func (l *dmViewLookup) hasSeen(status *PostStatus) (string, bool) {
	// A record written before this feature existed has no CreatedAt, so there is
	// nothing to compare against; it stays on whatever the webapp reported.
	if status == nil || status.CreatedAt <= 0 {
		return "", false
	}

	peerID := l.peer(status.ChannelID, status.AuthorID)
	if peerID == "" {
		return "", false
	}

	return peerID, l.lastViewedAt(status.ChannelID, peerID) >= status.CreatedAt
}

// syncDirectMessageRead promotes a delivered DM post to read when the recipient
// has viewed the conversation past it, and persists that so the reader list and
// the websocket update stay consistent with the webapp-driven path. It returns
// the status to report, never nil for a non-nil input.
func (p *Plugin) syncDirectMessageRead(status *PostStatus, lookup *dmViewLookup) *PostStatus {
	if status == nil || status.DerivedStatus() == "read" {
		return status
	}

	peerID, seen := lookup.hasSeen(status)
	if !seen {
		return status
	}

	// seed is nil: if the entry expired between the read above and now, there is
	// nothing to resurrect.
	updated, appErr := p.appendReader(status.PostID, peerID, nil)
	if appErr != nil {
		p.API.LogWarn("Failed to record direct-message read", "post_id", status.PostID, "error", appErr.Error())
		return status
	}

	if updated == nil {
		return status
	}

	return updated
}
