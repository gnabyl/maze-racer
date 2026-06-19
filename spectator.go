package main

import (
	"log"

	"github.com/gorilla/websocket"
)

type Spectator struct {
	conn *websocket.Conn
	send chan []byte
}

func (s *Spectator) WritePump() {
	defer s.conn.Close()
	for msg := range s.send {
		if err := s.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Printf("spectator write error: %v", err)
			break
		}
	}
}

func (s *Spectator) ReadPump(hub *Hub) {
	defer func() {
		hub.leaveSpectator <- s
		s.conn.Close()
	}()
	for {
		// spectators are read-only; drain any incoming messages and discard
		if _, _, err := s.conn.ReadMessage(); err != nil {
			break
		}
	}
}
