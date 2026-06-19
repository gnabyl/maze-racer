package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	hub := NewHub()
	go hub.Run()

	http.HandleFunc("/join/{id}", func(w http.ResponseWriter, r *http.Request) {
		playerID := r.PathValue("id")
		if playerID == "" {
			http.Error(w, "missing player id", http.StatusBadRequest)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("upgrade error: %v", err)
			return
		}

		p := &Player{
			id:   playerID,
			conn: conn,
			hub:  hub,
			send: make(chan []byte, 16),
		}

		if err := hub.Join(p); err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"error","msg":%q}`, err.Error())))
			conn.Close()
			return
		}

		p.send <- []byte(fmt.Sprintf(`{"type":"welcome","player_id":%q}`, playerID))

		go p.WritePump()
		p.ReadPump()
	})

	log.Println("server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
