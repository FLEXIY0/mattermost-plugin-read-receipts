package main

const (
	// maxReadBy caps how many reader IDs are retained per post. Without a cap a
	// single post in a large channel would grow its KV entry (and every
	// websocket payload derived from it) without bound.
	maxReadBy = 50
)

// PostStatus is the per-post record persisted in the plugin KV store.
type PostStatus struct {
	PostID    string   `json:"post_id"`
	ChannelID string   `json:"channel_id"`
	AuthorID  string   `json:"author_id"`
	Delivered bool     `json:"delivered"`
	ReadBy    []string `json:"read_by"`
	UpdatedAt int64    `json:"updated_at"`
}

func (s *PostStatus) DerivedStatus() string {
	if len(s.ReadBy) > 0 {
		return "read"
	}
	if s.Delivered {
		return "delivered"
	}
	return ""
}

func (s *PostStatus) hasReader(userID string) bool {
	for _, id := range s.ReadBy {
		if id == userID {
			return true
		}
	}
	return false
}

// addReader records a reader and reports whether the record actually changed.
// Once maxReadBy readers are stored the post is already "read", so further
// readers are dropped rather than allowed to grow the entry.
func (s *PostStatus) addReader(userID string) bool {
	if s.hasReader(userID) || len(s.ReadBy) >= maxReadBy {
		return false
	}

	s.ReadBy = append(s.ReadBy, userID)
	return true
}

type readRequest struct {
	PostID string `json:"post_id"`
}

type statusResponse struct {
	PostID string   `json:"post_id"`
	Status string   `json:"status"`
	ReadBy []string `json:"read_by"`
}
