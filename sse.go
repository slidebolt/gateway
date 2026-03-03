package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/gin-gonic/gin"
)

type sseMessage struct {
	Type     string `json:"type"`
	PluginID string `json:"plugin_id,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	EntityID string `json:"entity_id,omitempty"`
}

type sseBroker struct {
	mu      sync.RWMutex
	clients map[chan string]struct{}
}

var broker = &sseBroker{
	clients: make(map[chan string]struct{}),
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

func sseHandler(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	ch := make(chan string, 16)
	broker.addClient(ch)
	defer broker.removeClient(ch)

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
