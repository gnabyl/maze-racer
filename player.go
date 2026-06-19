package main

import (
	"log"

	"github.com/gorilla/websocket"
)

type Player struct {
	id   string
	conn *websocket.Conn
	hub  *Hub
	send chan []byte
}

func (p *Player) ReadPump() {
	defer func() {
		p.hub.leave <- p
		p.conn.Close()
	}()

	for {
		_, msg, err := p.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("player %s read error: %v", p.id, err)
			}
			break
		}
		log.Printf("player %s sent: %s", p.id, msg)
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
