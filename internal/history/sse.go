package history

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/gin-gonic/gin"
)

type sseMessage struct {
	Type      string `json:"type"`
	Kind      string `json:"kind,omitempty"`
	PluginID  string `json:"plugin_id,omitempty"`
	DeviceID  string `json:"device_id,omitempty"`
	EntityID  string `json:"entity_id,omitempty"`
	Name      string `json:"name,omitempty"`
	State     string `json:"state,omitempty"`
	EventID   string `json:"event_id,omitempty"`
	CommandID string `json:"command_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Seq       uint64 `json:"seq,omitempty"`
}

type sseBroker struct {
	mu      sync.RWMutex
	clients map[chan string]struct{}
}

func newSSEBroker() *sseBroker {
	return &sseBroker{clients: make(map[chan string]struct{})}
}

func (b *sseBroker) addClient(ch chan string) {
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
}

func (b *sseBroker) removeClient(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *sseBroker) broadcast(msg sseMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	line := "data: " + string(data) + "\n\n"
	b.mu.RLock()
	for ch := range b.clients {
		select {
		case ch <- line:
		default:
			// slow client, skip
		}
	}
	b.mu.RUnlock()
}

// SSEHandler returns a Gin handler for the Server-Sent Events stream.
func (h *History) SSEHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		ch := make(chan string, 16)
		h.broker.addClient(ch)
		defer h.broker.removeClient(ch)

		log.Printf("SSE: client connected (%s)", c.Request.RemoteAddr)

		c.Stream(func(w io.Writer) bool {
			select {
			case msg := <-ch:
				fmt.Fprint(w, msg)
				return true
			case <-c.Request.Context().Done():
				log.Printf("SSE: client disconnected (%s)", c.Request.RemoteAddr)
				return false
			}
		})
	}
}

// BroadcastDevice pushes a device-change notification to all SSE subscribers.
func (h *History) BroadcastDevice(pluginID, deviceID string) {
	h.broker.broadcast(sseMessage{Type: "device", PluginID: pluginID, DeviceID: deviceID})
}

// BroadcastEntity pushes an entity-change notification to all SSE subscribers.
func (h *History) BroadcastEntity(pluginID, deviceID, entityID string) {
	h.broker.broadcast(sseMessage{Type: "entity", PluginID: pluginID, DeviceID: deviceID, EntityID: entityID})
}
