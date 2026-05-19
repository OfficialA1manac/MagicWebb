package api

import (
	"fmt"
	"net/http"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
)

// topicChannels maps friendly topic names to Redis pub/sub channel names.
var topicChannels = map[string]string{
	"listings": "mktplace:events",
	"auctions": "auction:events",
	"offers":   "offers:events",
}

var defaultChannels = []string{"mktplace:events", "auction:events", "offers:events"}

// handleEvents streams Redis pub/sub messages to the client via SSE.
// GET /events?topic=listings&topic=auctions
func handleEvents(rdb *cache.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		// Resolve requested topics → Redis channels
		topics := r.URL.Query()["topic"]
		channels := make([]string, 0, len(topics))
		if len(topics) == 0 {
			channels = defaultChannels
		} else {
			for _, t := range topics {
				if ch, ok := topicChannels[t]; ok {
					channels = append(channels, ch)
				}
			}
			if len(channels) == 0 {
				channels = defaultChannels
			}
		}

		sub := rdb.Subscribe(r.Context(), channels...)
		defer sub.Close()

		ch := sub.Channel()
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", msg.Payload)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}
