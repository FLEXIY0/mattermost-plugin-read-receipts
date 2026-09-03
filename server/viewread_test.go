package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
)

// newTestPlugin wires a Plugin to a mocked API. Logging is always allowed so a
// test only has to describe the calls it cares about.
func newTestPlugin(t *testing.T) (*Plugin, *plugintest.API) {
	t.Helper()

	api := &plugintest.API{}
	api.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	p := &Plugin{}
	p.SetAPI(api)
	cfg := configuration{TickSize: defaultTickSize}.normalized()
	p.config.Store(&cfg)

	t.Cleanup(func() { api.AssertExpectations(t) })

	return p, api
}

func directChannel(id, authorID, peerID string) *model.Channel {
	return &model.Channel{Id: id, Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(authorID, peerID)}
}

func members(viewed map[string]int64) model.ChannelMembers {
	out := model.ChannelMembers{}
	for userID, at := range viewed {
		out = append(out, model.ChannelMember{UserId: userID, LastViewedAt: at})
	}
	return out
}

func TestViewLookupDirectMessage(t *testing.T) {
	postCreatedAt := int64(1_000)

	cases := []struct {
		name         string
		lastViewedAt int64
		createdAt    int64
		want         bool
	}{
		{"viewed after the post", postCreatedAt + 1, postCreatedAt, true},
		{"viewed exactly at the post", postCreatedAt, postCreatedAt, true},
		{"viewed before the post", postCreatedAt - 1, postCreatedAt, false},
		{"never viewed", 0, postCreatedAt, false},
		// Records written before CreatedAt existed must not be guessed at.
		{"legacy record without CreatedAt", postCreatedAt + 1, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authorID, peerID, channelID := model.NewId(), model.NewId(), model.NewId()
			p, api := newTestPlugin(t)

			if tc.createdAt > 0 {
				api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, peerID), nil)
				api.On("GetChannelMembers", channelID, 0, maxViewSyncMembers).
					Return(members(map[string]int64{peerID: tc.lastViewedAt}), nil)
			}

			status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: tc.createdAt, Delivered: true}
			readers := newViewLookup(p).readersOf(status)

			if got := len(readers) == 1; got != tc.want {
				t.Fatalf("readers = %v, want seen=%v", readers, tc.want)
			}
			if tc.want && readers[0] != peerID {
				t.Errorf("reader = %q, want %q", readers[0], peerID)
			}
		})
	}
}

// A DM needs no member-count check, so GetChannelStats must not be called.
func TestViewLookupSelfDMHasNoReader(t *testing.T) {
	authorID, channelID := model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, authorID), nil)
	api.On("GetChannelMembers", channelID, 0, maxViewSyncMembers).
		Return(members(map[string]int64{authorID: 9_000}), nil)

	status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true}
	if readers := newViewLookup(p).readersOf(status); len(readers) != 0 {
		t.Errorf("readers = %v, want none: the author is the only member", readers)
	}
}

func TestViewLookupChannelReturnsEveryoneWhoLooked(t *testing.T) {
	authorID, channelID := model.NewId(), model.NewId()
	seen, alsoSeen, notSeen := model.NewId(), model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)
	api.On("GetChannelStats", channelID).Return(&model.ChannelStats{MemberCount: 4}, nil)
	api.On("GetChannelMembers", channelID, 0, maxViewSyncMembers).Return(members(map[string]int64{
		authorID: 9_000, seen: 1_500, alsoSeen: 1_000, notSeen: 999,
	}), nil)

	status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true}
	readers := newViewLookup(p).readersOf(status)

	got := map[string]bool{}
	for _, r := range readers {
		got[r] = true
	}

	if len(got) != 2 || !got[seen] || !got[alsoSeen] {
		t.Errorf("readers = %v, want exactly %s and %s", readers, seen, alsoSeen)
	}
	if got[authorID] {
		t.Error("the author must never be recorded as a reader of their own post")
	}
}

// Loading a thousand memberships on every status request is not worth a tick.
func TestViewLookupSkipsLargeChannels(t *testing.T) {
	authorID, channelID := model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)
	api.On("GetChannelStats", channelID).Return(&model.ChannelStats{MemberCount: maxViewSyncMembers + 1}, nil)

	// GetChannelMembers is deliberately not mocked: reaching it fails the test.
	status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true}
	if readers := newViewLookup(p).readersOf(status); len(readers) != 0 {
		t.Errorf("readers = %v, want none above the member cap", readers)
	}
}

func TestViewLookupRespectsTheChannelSetting(t *testing.T) {
	authorID, channelID := model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	disabled := false
	cfg := configuration{TickSize: defaultTickSize, ChannelReadSync: &disabled}.normalized()
	p.config.Store(&cfg)

	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)

	// Neither the stats nor the memberships should be fetched when it is off.
	status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true}
	if readers := newViewLookup(p).readersOf(status); len(readers) != 0 {
		t.Errorf("readers = %v, want none when channel sync is off", readers)
	}
}

// Turning channel sync off must not disable it for direct messages.
func TestViewLookupChannelSettingLeavesDMsAlone(t *testing.T) {
	authorID, peerID, channelID := model.NewId(), model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	disabled := false
	cfg := configuration{TickSize: defaultTickSize, ChannelReadSync: &disabled}.normalized()
	p.config.Store(&cfg)

	api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, peerID), nil)
	api.On("GetChannelMembers", channelID, 0, maxViewSyncMembers).
		Return(members(map[string]int64{peerID: 2_000}), nil)

	status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true}
	if readers := newViewLookup(p).readersOf(status); len(readers) != 1 {
		t.Errorf("readers = %v, want the DM peer", readers)
	}
}

// A screenful of one conversation must not cost a lookup per post.
func TestViewLookupCachesPerChannel(t *testing.T) {
	authorID, peerID, channelID := model.NewId(), model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, peerID), nil).Once()
	api.On("GetChannelMembers", channelID, 0, maxViewSyncMembers).
		Return(members(map[string]int64{peerID: 5_000}), nil).Once()

	lookup := newViewLookup(p)
	for i := 0; i < 25; i++ {
		status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true}
		if readers := lookup.readersOf(status); len(readers) != 1 {
			t.Fatalf("post %d: readers = %v, want the peer", i, readers)
		}
	}
}

// An ineligible channel must be decided once, not re-checked per post.
func TestViewLookupCachesIneligibleChannels(t *testing.T) {
	authorID, channelID := model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil).Once()
	api.On("GetChannelStats", channelID).Return(&model.ChannelStats{MemberCount: 5_000}, nil).Once()

	lookup := newViewLookup(p)
	for i := 0; i < 25; i++ {
		status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true}
		if readers := lookup.readersOf(status); len(readers) != 0 {
			t.Fatalf("post %d: readers = %v, want none", i, readers)
		}
	}
}

func TestSyncViewedReadsLeavesReadPostsAlone(t *testing.T) {
	p, _ := newTestPlugin(t)

	// Already read: no channel lookup should happen at all, which the mock's
	// AssertExpectations enforces by failing on any unexpected call.
	status := &PostStatus{
		PostID: model.NewId(), ChannelID: model.NewId(), AuthorID: model.NewId(),
		CreatedAt: 1, Delivered: true, ReadBy: []string{model.NewId()},
	}

	if got := p.syncViewedReads(status, newViewLookup(p)); got != status {
		t.Error("an already-read status must be returned untouched")
	}
}

func TestSyncViewedReadsRecordsEveryReaderInOneWrite(t *testing.T) {
	authorID, channelID, postID := model.NewId(), model.NewId(), model.NewId()
	first, second := model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypePrivate}, nil)
	api.On("GetChannelStats", channelID).Return(&model.ChannelStats{MemberCount: 3}, nil)
	api.On("GetChannelMembers", channelID, 0, maxViewSyncMembers).
		Return(members(map[string]int64{first: 2_000, second: 3_000}), nil)

	stored := &PostStatus{PostID: postID, ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true, ReadBy: []string{}}
	api.On("KVGet", kvPrefix+postID).Return(mustMarshal(t, stored), nil)
	// Once: both readers must land in a single compare-and-set.
	api.On("KVSetWithOptions", kvPrefix+postID, mock.Anything, mock.Anything).Return(true, nil).Once()
	api.On("PublishWebSocketEvent", statusEventName, mock.Anything, mock.Anything).Return()

	got := p.syncViewedReads(stored, newViewLookup(p))
	if got.DerivedStatus() != "read" {
		t.Fatalf("status = %q, want %q", got.DerivedStatus(), "read")
	}
	if len(got.ReadBy) != 2 {
		t.Errorf("ReadBy = %v, want both readers", got.ReadBy)
	}
}

// The entry can expire between the caller's read and the write-back.
func TestSyncViewedReadsHandlesAVanishedEntry(t *testing.T) {
	authorID, peerID, channelID, postID := model.NewId(), model.NewId(), model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, peerID), nil)
	api.On("GetChannelMembers", channelID, 0, maxViewSyncMembers).
		Return(members(map[string]int64{peerID: 2_000}), nil)
	api.On("KVGet", kvPrefix+postID).Return(nil, nil)

	stored := &PostStatus{PostID: postID, ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true, ReadBy: []string{}}
	if got := p.syncViewedReads(stored, newViewLookup(p)); got != stored {
		t.Error("a vanished entry must leave the reported status unchanged")
	}
}
