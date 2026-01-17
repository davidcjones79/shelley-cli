package coordinator

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// WSEvent represents a real-time event sent to clients
type WSEvent struct {
	Type string      `json:"type"` // tasks, workers, stats, task_update, worker_update
	Data interface{} `json:"data"`
}

// HandleWebSocket handles WebSocket connections for real-time updates
func (c *Coordinator) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade to WebSocket
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Create channel for this client
	msgChan := make(chan []byte, 100)

	// Register client
	c.wsClientsMu.Lock()
	c.wsClients[msgChan] = struct{}{}
	c.wsClientsMu.Unlock()

	// Unregister on disconnect
	defer func() {
		c.wsClientsMu.Lock()
		delete(c.wsClients, msgChan)
		c.wsClientsMu.Unlock()
		close(msgChan)
	}()

	log.Printf("WebSocket client connected (total: %d)", len(c.wsClients))

	// Send initial state
	c.sendFullState(msgChan)

	// Read goroutine (to detect disconnects and handle pings)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, _, err := wsutil.ReadClientData(conn)
			if err != nil {
				return
			}
		}
	}()

	// Write loop
	for {
		select {
		case msg, ok := <-msgChan:
			if !ok {
				return
			}
			if err := wsutil.WriteServerMessage(conn, ws.OpText, msg); err != nil {
				log.Printf("WebSocket write error: %v", err)
				return
			}
		case <-done:
			log.Printf("WebSocket client disconnected")
			return
		case <-c.shutdown:
			return
		}
	}
}

// sendFullState sends the complete current state to a client
func (c *Coordinator) sendFullState(ch chan []byte) {
	// Send stats
	stats := c.GetStats()
	c.sendEvent(ch, "stats", stats)

	// Send tasks
	tasks, _ := c.ListTasks("", 100)
	c.sendEvent(ch, "tasks", tasks)

	// Send workers
	workers, _ := c.ListWorkers(true)
	c.sendEvent(ch, "workers", workers)

	// Send groups
	groups, _ := c.ListGroups("", 50)
	c.sendEvent(ch, "groups", groups)
}

// sendEvent sends an event to a single client channel
func (c *Coordinator) sendEvent(ch chan []byte, eventType string, data interface{}) {
	event := WSEvent{Type: eventType, Data: data}
	msg, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to marshal WS event: %v", err)
		return
	}
	select {
	case ch <- msg:
	default:
		// Channel full, skip
	}
}

// BroadcastUpdate sends an update to all connected WebSocket clients
func (c *Coordinator) BroadcastUpdate(eventType string, data interface{}) {
	event := WSEvent{Type: eventType, Data: data}
	msg, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to marshal WS event: %v", err)
		return
	}

	c.wsClientsMu.RLock()
	defer c.wsClientsMu.RUnlock()

	for ch := range c.wsClients {
		select {
		case ch <- msg:
		default:
			// Channel full, skip this client
		}
	}
}

// BroadcastFullRefresh sends complete state to all clients
func (c *Coordinator) BroadcastFullRefresh() {
	c.wsClientsMu.RLock()
	defer c.wsClientsMu.RUnlock()

	for ch := range c.wsClients {
		c.sendFullState(ch)
	}
}

// StartPeriodicBroadcast starts a goroutine that broadcasts stats periodically
func (c *Coordinator) StartPeriodicBroadcast() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.wsClientsMu.RLock()
				clientCount := len(c.wsClients)
				c.wsClientsMu.RUnlock()
				if clientCount > 0 {
					stats := c.GetStats()
					c.BroadcastUpdate("stats", stats)
				}
			case <-c.shutdown:
				return
			}
		}
	}()
}
