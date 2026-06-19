package main

import (
	"encoding/json"
	"log"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type Player struct {
	id        string
	conn      *websocket.Conn
	hub       *Hub
	send      chan []byte
	pos       Pos
	won       bool
	connected bool
	moves     int
	joinedAt  time.Time

	pendingMove atomic.Pointer[string] // latest move, nil if none queued
}

func (p *Player) queueMove(dir string) {
	p.pendingMove.Store(&dir)
}

func (p *Player) popMove() string {
	ptr := p.pendingMove.Swap(nil)
	if ptr == nil {
		return ""
	}
	return *ptr
}

func (p *Player) ReadPump() {
	defer func() {
		p.hub.leave <- p
		p.conn.Close()
	}()

	for {
		_, raw, err := p.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("player %s read error: %v", p.id, err)
			}
			break
		}

		var msg struct {
			Move string `json:"move"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Move == "" {
			log.Printf("player %s bad message: %s", p.id, raw)
			continue
		}

		p.queueMove(msg.Move)
	}
}

func (p *Player) WritePump() {
	defer p.conn.Close()

	for msg := range p.send {
		if err := p.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Printf("player %s write error: %v", p.id, err)
			break
		}
	}
}
