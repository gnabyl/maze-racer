package main

import (
	"encoding/json"
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
		_, raw, err := s.conn.ReadMessage()
		if err != nil {
			break
		}
		var msg struct {
			Cmd string `json:"cmd"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		switch msg.Cmd {
		case "start":
			select {
			case hub.startGame <- struct{}{}:
			default:
			}
		case "restart":
			select {
			case hub.restartGame <- struct{}{}:
			default:
			}
		}
	}
}
