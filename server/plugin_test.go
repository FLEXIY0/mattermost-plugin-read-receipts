package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestIsUserContentPostType(t *testing.T) {
	cases := []struct {
		postType string
		want     bool
	}{
		{"", true},
		{model.PostTypeDefault, true},
		{"custom_channel_reply", true},
		{"system_join_channel", false},
		{"system_add_to_channel", false},
		{"slack_attachment", false},
	}

	for _, tc := range cases {
		if got := isUserContentPostType(tc.postType); got != tc.want {
			t.Errorf("isUserContentPostType(%q) = %v, want %v", tc.postType, got, tc.want)
		}
	}
}

func TestIsTrackablePost(t *testing.T) {
	valid := func() *model.Post {
		return &model.Post{Id: model.NewId(), ChannelId: model.NewId(), UserId: model.NewId()}
	}

	if !isTrackablePost(valid()) {
		t.Error("a plain user post should be trackable")
	}

	if isTrackablePost(nil) {
		t.Error("nil post must not be trackable")
	}

	deleted := valid()
	deleted.DeleteAt = model.GetMillis()
	if isTrackablePost(deleted) {
		t.Error("deleted post must not be trackable")
	}

	badID := valid()
	badID.Id = "not-an-id"
	if isTrackablePost(badID) {
		t.Error("post with an invalid id must not be trackable")
	}

	system := valid()
	system.Type = "system_join_channel"
	if isTrackablePost(system) {
		t.Error("system post must not be trackable")
	}

	// Integrations set these props with either a bool or a string, depending on
	// the code path that created the post.
	for _, props := range []model.StringInterface{
		{"from_webhook": true},
		{"from_webhook": "true"},
		{"from_bot": true},
		{"from_bot": "true"},
	} {
		post := valid()
		post.Props = props
		if isTrackablePost(post) {
			t.Errorf("post with props %v must not be trackable", props)
		}
	}
}

func TestPostStatusDerivedStatus(t *testing.T) {
	empty := &PostStatus{}
	if got := empty.DerivedStatus(); got != "" {
		t.Errorf("empty status = %q, want %q", got, "")
	}

	delivered := &PostStatus{Delivered: true}
	if got := delivered.DerivedStatus(); got != "delivered" {
		t.Errorf("delivered status = %q, want %q", got, "delivered")
	}

	read := &PostStatus{Delivered: true, ReadBy: []string{model.NewId()}}
	if got := read.DerivedStatus(); got != "read" {
		t.Errorf("read status = %q, want %q", got, "read")
	}
}

func TestPostStatusAddReader(t *testing.T) {
	reader := model.NewId()
	status := &PostStatus{Delivered: true}

	if !status.addReader(reader) {
		t.Fatal("first addReader should report a change")
	}

	if status.addReader(reader) {
		t.Fatal("addReader must be idempotent for the same reader")
	}

	if len(status.ReadBy) != 1 {
		t.Fatalf("ReadBy = %d entries, want 1", len(status.ReadBy))
	}
}

func TestPostStatusAddReaderIsCapped(t *testing.T) {
	status := &PostStatus{Delivered: true}

	for i := 0; i < maxReadBy; i++ {
		if !status.addReader(model.NewId()) {
			t.Fatalf("reader %d should have been recorded", i)
		}
	}

	if status.addReader(model.NewId()) {
		t.Error("addReader must refuse to grow past maxReadBy")
	}

	if len(status.ReadBy) != maxReadBy {
		t.Errorf("ReadBy = %d entries, want %d", len(status.ReadBy), maxReadBy)
	}
}
