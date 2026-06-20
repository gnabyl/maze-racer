package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

type Spectator struct {
	conn *websocket.Conn
	send chan []byte
}

type restartRequest struct {
	Rooms int `json:"rooms"`
	Tick  int `json:"tick"` // ms
}

func (s *Spectator) WritePump() {
	defer s.conn.Close()

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-s.send:
			s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if !ok {
				s.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := s.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("spectator write error: %v", err)
				return
			}

		case <-ticker.C:
			s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Spectator) ReadPump(hub *Hub) {
	defer func() {
		hub.leaveSpectator <- s
		s.conn.Close()
	}()

	s.conn.SetReadDeadline(time.Now().Add(pongTimeout))
	s.conn.SetPongHandler(func(string) error {
		s.conn.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

	for {
		_, raw, err := s.conn.ReadMessage()
		if err != nil {
			break
		}
		var msg struct {
			Cmd   string `json:"cmd"`
			Rooms int    `json:"rooms"`
			Tick  int    `json:"tick"`
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
			req := restartRequest{Rooms: msg.Rooms, Tick: msg.Tick}
			select {
			case hub.restartGame <- req:
			default:
			}
		}
	}
}
