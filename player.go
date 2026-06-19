package main

import (
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
)

type Player struct {
	id   string
	conn *websocket.Conn
	hub  *Hub
	send chan []byte
	pos  Pos
}

type MoveRequest struct {
	player *Player
	dir    string // "UP", "DOWN", "LEFT", "RIGHT"
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

		p.hub.move <- MoveRequest{player: p, dir: msg.Move}
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
