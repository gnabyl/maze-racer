package main

import (
	"encoding/json"
	"log"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeTimeout  = 10 * time.Second
	pongTimeout   = 60 * time.Second
	pingInterval  = 54 * time.Second
)

type Player struct {
	id   string
	conn *websocket.Conn
	hub  *Hub
	send chan []byte
	pos  Pos
	won  bool
	moves int

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

	p.conn.SetReadDeadline(time.Now().Add(pongTimeout))
	p.conn.SetPongHandler(func(string) error {
		p.conn.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

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

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-p.send:
			p.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if !ok {
				p.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := p.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("player %s write error: %v", p.id, err)
				return
			}

		case <-ticker.C:
			p.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := p.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
