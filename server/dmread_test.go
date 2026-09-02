package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
)

// newTestPlugin wires a Plugin to a mocked API. LogDebug/LogWarn are always
// allowed so a test only has to describe the calls it cares about.
func newTestPlugin(t *testing.T) (*Plugin, *plugintest.API) {
	t.Helper()

	api := &plugintest.API{}
	api.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	p := &Plugin{}
	p.SetAPI(api)
	t.Cleanup(func() { api.AssertExpectations(t) })

	return p, api
}

func directChannel(id, authorID, peerID string) *model.Channel {
	return &model.Channel{
		Id:   id,
		Type: model.ChannelTypeDirect,
		Name: model.GetDMNameFromIds(authorID, peerID),
	}
}

func TestDMViewLookupHasSeen(t *testing.T) {
	authorID, peerID, channelID := model.NewId(), model.NewId(), model.NewId()
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
			p, api := newTestPlugin(t)
			status := &PostStatus{
				PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID,
				CreatedAt: tc.createdAt, Delivered: true,
			}

			if tc.createdAt > 0 {
				api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, peerID), nil)
				api.On("GetChannelMember", channelID, peerID).
					Return(&model.ChannelMember{LastViewedAt: tc.lastViewedAt}, nil)
			}

			gotPeer, gotSeen := newDMViewLookup(p).hasSeen(status)
			if gotSeen != tc.want {
				t.Errorf("hasSeen = %v, want %v", gotSeen, tc.want)
			}
			if gotSeen && gotPeer != peerID {
				t.Errorf("peer = %q, want %q", gotPeer, peerID)
			}
		})
	}
}

func TestDMViewLookupIgnoresNonDirectChannels(t *testing.T) {
	authorID, channelID := model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)

	status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1, Delivered: true}
	if _, seen := newDMViewLookup(p).hasSeen(status); seen {
		t.Error("an open channel must not be resolved through the DM path")
	}
}

func TestDMViewLookupIgnoresSelfDM(t *testing.T) {
	authorID, channelID := model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, authorID), nil)

	status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1, Delivered: true}
	if _, seen := newDMViewLookup(p).hasSeen(status); seen {
		t.Error("a self-DM has no other reader and must not produce a receipt")
	}
}

// A screenful of posts in one conversation must not cost one pair of API calls
// per post.
func TestDMViewLookupCachesPerChannel(t *testing.T) {
	authorID, peerID, channelID := model.NewId(), model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, peerID), nil).Once()
	api.On("GetChannelMember", channelID, peerID).
		Return(&model.ChannelMember{LastViewedAt: 5_000}, nil).Once()

	lookup := newDMViewLookup(p)
	for i := 0; i < 25; i++ {
		status := &PostStatus{PostID: model.NewId(), ChannelID: channelID, AuthorID: authorID, CreatedAt: 1_000, Delivered: true}
		if _, seen := lookup.hasSeen(status); !seen {
			t.Fatalf("post %d should have been seen", i)
		}
	}
}

func TestSyncDirectMessageReadLeavesReadPostsAlone(t *testing.T) {
	p, _ := newTestPlugin(t)

	// Already read: no channel lookup should happen at all, which the mock's
	// AssertExpectations enforces by failing on any unexpected call.
	status := &PostStatus{
		PostID: model.NewId(), ChannelID: model.NewId(), AuthorID: model.NewId(),
		CreatedAt: 1, Delivered: true, ReadBy: []string{model.NewId()},
	}

	if got := p.syncDirectMessageRead(status, newDMViewLookup(p)); got != status {
		t.Error("an already-read status must be returned untouched")
	}
}

func TestSyncDirectMessageReadRecordsThePeer(t *testing.T) {
	authorID, peerID, channelID, postID := model.NewId(), model.NewId(), model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, peerID), nil)
	api.On("GetChannelMember", channelID, peerID).Return(&model.ChannelMember{LastViewedAt: 2_000}, nil)

	stored := &PostStatus{
		PostID: postID, ChannelID: channelID, AuthorID: authorID,
		CreatedAt: 1_000, Delivered: true, ReadBy: []string{},
	}
	api.On("KVGet", kvPrefix+postID).Return(mustMarshal(t, stored), nil)
	api.On("KVSetWithOptions", kvPrefix+postID, mock.Anything, mock.Anything).Return(true, nil)
	api.On("PublishWebSocketEvent", statusEventName, mock.Anything, mock.Anything).Return()

	got := p.syncDirectMessageRead(stored, newDMViewLookup(p))
	if got.DerivedStatus() != "read" {
		t.Fatalf("status = %q, want %q", got.DerivedStatus(), "read")
	}
	if len(got.ReadBy) != 1 || got.ReadBy[0] != peerID {
		t.Errorf("ReadBy = %v, want [%s]", got.ReadBy, peerID)
	}
}

// The entry can expire between the caller's read and the write-back.
func TestSyncDirectMessageReadHandlesAVanishedEntry(t *testing.T) {
	authorID, peerID, channelID, postID := model.NewId(), model.NewId(), model.NewId(), model.NewId()

	p, api := newTestPlugin(t)
	api.On("GetChannel", channelID).Return(directChannel(channelID, authorID, peerID), nil)
	api.On("GetChannelMember", channelID, peerID).Return(&model.ChannelMember{LastViewedAt: 2_000}, nil)
	api.On("KVGet", kvPrefix+postID).Return(nil, nil)

	stored := &PostStatus{
		PostID: postID, ChannelID: channelID, AuthorID: authorID,
		CreatedAt: 1_000, Delivered: true, ReadBy: []string{},
	}

	if got := p.syncDirectMessageRead(stored, newDMViewLookup(p)); got != stored {
		t.Error("a vanished entry must leave the reported status unchanged")
	}
}
