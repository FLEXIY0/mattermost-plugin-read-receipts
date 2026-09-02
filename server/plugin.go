package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const (
	statusEventName = "status_updated"
	kvPrefix        = "status_"

	// headerUserID is set by Mattermost for authenticated plugin requests and
	// stripped from anything a client sends.
	headerUserID = "Mattermost-User-ID"

	// statusTTLSeconds bounds KV growth. Receipts only matter while a post is
	// recent, and the webapp falls back to "delivered" once an entry expires.
	statusTTLSeconds = 60 * 60 * 24 * 30

	// casAttempts bounds the compare-and-set retry loop so a hot post cannot
	// spin a request indefinitely.
	casAttempts = 5
)

// Plugin implements the Mattermost plugin interface.
type Plugin struct {
	plugin.MattermostPlugin

	// router is published atomically: OnActivate runs concurrently with the
	// first requests the server may route to us.
	router atomic.Pointer[mux.Router]
}

func (p *Plugin) OnActivate() error {
	p.router.Store(p.initRouter())
	return nil
}

func isUserContentPostType(postType string) bool {
	if postType == "" || postType == model.PostTypeDefault {
		return true
	}

	if strings.HasPrefix(postType, "system_") {
		return false
	}

	// Plugins such as Channel Reply publish user-authored content as custom_*.
	if strings.HasPrefix(postType, "custom_") {
		return true
	}

	return false
}

// isTrackablePost decides whether a post should carry receipts at all. Bot and
// webhook posts are excluded so integrations do not generate KV churn.
func isTrackablePost(post *model.Post) bool {
	if post == nil || post.DeleteAt > 0 || !model.IsValidId(post.Id) {
		return false
	}

	if !isUserContentPostType(post.Type) {
		return false
	}

	if post.Props != nil {
		if fromWebhook, ok := post.Props["from_webhook"].(bool); ok && fromWebhook {
			return false
		}
		if fromWebhook, ok := post.Props["from_webhook"].(string); ok && fromWebhook == "true" {
			return false
		}
		if fromBot, ok := post.Props["from_bot"].(bool); ok && fromBot {
			return false
		}
		if fromBot, ok := post.Props["from_bot"].(string); ok && fromBot == "true" {
			return false
		}
	}

	return true
}

// MessageHasBeenPosted runs for every post on the server, so it stays cheap and
// never panics: a failure here must not affect message delivery.
func (p *Plugin) MessageHasBeenPosted(_ *plugin.Context, post *model.Post) {
	defer p.recoverPanic("MessageHasBeenPosted")

	if !isTrackablePost(post) {
		return
	}

	p.markDelivered(post)
}

// MessageHasBeenDeleted drops the receipt for a deleted post so reader lists do
// not outlive the message they describe.
func (p *Plugin) MessageHasBeenDeleted(_ *plugin.Context, post *model.Post) {
	defer p.recoverPanic("MessageHasBeenDeleted")

	if post == nil || !model.IsValidId(post.Id) {
		return
	}

	if appErr := p.API.KVDelete(p.kvKey(post.Id)); appErr != nil {
		p.API.LogWarn("Failed to delete post status", "post_id", post.Id, "error", appErr.Error())
	}
}

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	defer p.recoverPanic("ServeHTTP")

	router := p.router.Load()
	if router == nil {
		http.Error(w, "Plugin is not ready", http.StatusServiceUnavailable)
		return
	}

	router.ServeHTTP(w, r)
}

// recoverPanic keeps a bug in this plugin from taking down the plugin process
// (and, with it, the hooks Mattermost calls on the posting path).
func (p *Plugin) recoverPanic(where string) {
	if r := recover(); r != nil {
		p.API.LogError("Recovered from panic", "where", where, "panic", r)
	}
}

func (p *Plugin) kvKey(postID string) string {
	return kvPrefix + postID
}

// getStatus returns the raw stored bytes alongside the decoded status. The raw
// value is the compare-and-set token for the next write.
func (p *Plugin) getStatus(postID string) ([]byte, *PostStatus, *model.AppError) {
	data, appErr := p.API.KVGet(p.kvKey(postID))
	if appErr != nil {
		return nil, nil, appErr
	}
	if len(data) == 0 {
		return nil, nil, nil
	}

	var status PostStatus
	if err := json.Unmarshal(data, &status); err != nil {
		// A corrupt entry should not wedge the post forever: report it as
		// missing so the next write replaces it.
		p.API.LogWarn("Discarding unreadable post status", "post_id", postID, "error", err.Error())
		return nil, nil, nil
	}

	return data, &status, nil
}

// writeStatus stores the record only if the stored value still matches
// oldValue, which makes concurrent updates safe across cluster nodes. A nil
// oldValue means "insert only if absent".
func (p *Plugin) writeStatus(oldValue []byte, status *PostStatus) (bool, *model.AppError) {
	status.UpdatedAt = model.GetMillis()

	data, err := json.Marshal(status)
	if err != nil {
		return false, model.NewAppError("writeStatus", "plugin.message_status.marshal.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	if len(oldValue) == 0 {
		oldValue = nil
	}

	return p.API.KVSetWithOptions(p.kvKey(status.PostID), data, model.PluginKVSetOptions{
		Atomic:          true,
		OldValue:        oldValue,
		ExpireInSeconds: statusTTLSeconds,
	})
}

// statusForPost builds the record a post starts life with. CreatedAt is kept
// so the direct-message sync can compare a post against a channel view time
// without loading the post again.
func statusForPost(post *model.Post) *PostStatus {
	return &PostStatus{
		PostID:    post.Id,
		ChannelID: post.ChannelId,
		AuthorID:  post.UserId,
		CreatedAt: post.CreateAt,
		Delivered: true,
		ReadBy:    []string{},
	}
}

func (p *Plugin) markDelivered(post *model.Post) {
	status := statusForPost(post)

	// A brand new post almost never has a stored status, so try the insert-only
	// write first and pay for a read only when it loses the race.
	inserted, appErr := p.writeStatus(nil, status)
	if appErr != nil {
		p.API.LogWarn("Failed to save delivered status", "post_id", post.Id, "error", appErr.Error())
		return
	}

	if inserted {
		p.publishStatusUpdate(status)
		return
	}

	_, existing, appErr := p.getStatus(post.Id)
	if appErr != nil {
		p.API.LogWarn("Failed to load post status", "post_id", post.Id, "error", appErr.Error())
		return
	}

	if existing != nil {
		p.publishStatusUpdate(existing)
	}
}

// markRead records readerID against a post. It returns (nil, nil) whenever the
// request should be silently ignored, so callers cannot tell an unknown post
// apart from one they are not allowed to see.
func (p *Plugin) markRead(postID, readerID string) (*PostStatus, *model.AppError) {
	post, appErr := p.API.GetPost(postID)
	if appErr != nil {
		if appErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, appErr
	}

	if !isTrackablePost(post) || post.UserId == readerID {
		return nil, nil
	}

	// Without this check any account could forge receipts for posts in channels
	// it never joined, and probe which post IDs exist.
	if !p.API.HasPermissionToChannel(readerID, post.ChannelId, model.PermissionReadChannel) {
		return nil, nil
	}

	return p.appendReader(postID, readerID, func() *PostStatus {
		return statusForPost(post)
	})
}

// appendReader runs the read/modify/write loop that records one reader against
// a post. seed supplies a record when nothing is stored yet; if it returns nil
// the post is left alone. Callers are responsible for authorizing the reader.
func (p *Plugin) appendReader(postID, readerID string, seed func() *PostStatus) (*PostStatus, *model.AppError) {
	for attempt := 0; attempt < casAttempts; attempt++ {
		raw, status, appErr := p.getStatus(postID)
		if appErr != nil {
			return nil, appErr
		}

		if status == nil {
			if seed == nil {
				return nil, nil
			}
			if status = seed(); status == nil {
				return nil, nil
			}
		}

		if !status.addReader(readerID) {
			// Already recorded, or the reader cap is full: nothing to write.
			return status, nil
		}

		written, appErr := p.writeStatus(raw, status)
		if appErr != nil {
			return nil, appErr
		}

		if written {
			p.publishStatusUpdate(status)
			return status, nil
		}
	}

	// Lost every race. The concurrent writers already moved the post to "read",
	// so report the current state instead of failing the request.
	_, status, appErr := p.getStatus(postID)
	if appErr != nil {
		return nil, appErr
	}

	return status, nil
}

// publishStatusUpdate notifies the author only. Reader identities must never be
// broadcast to a channel.
func (p *Plugin) publishStatusUpdate(status *PostStatus) {
	if status == nil || !model.IsValidId(status.AuthorID) {
		return
	}

	derived := status.DerivedStatus()
	if derived == "" {
		return
	}

	// Mattermost websocket payloads must use primitive values only.
	readBy := strings.Join(status.ReadBy, ",")

	payload := map[string]any{
		"post_id":    status.PostID,
		"channel_id": status.ChannelID,
		"author_id":  status.AuthorID,
		"status":     derived,
		"read_by":    readBy,
	}

	p.API.PublishWebSocketEvent(statusEventName, payload, &model.WebsocketBroadcast{
		UserId: status.AuthorID,
	})
}
