package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	rooms    := flag.Int("rooms", 15, "maze size (number of rooms per side)")
	extraPass := flag.Float64("extra", 0.1, "fraction of extra passages (0.0-1.0)")
	tickMs   := flag.Int("tick", 300, "tick rate in milliseconds")
	flag.Parse()

	rng := rand.New(rand.NewSource(42))
	hub := NewHub(MazeConfig{Rooms: *rooms, ExtraPass: *extraPass}, rng, time.Duration(*tickMs)*time.Millisecond)
	log.Printf("maze: %dx%d rooms, extra=%.2f, tick=%dms", *rooms, *rooms, *extraPass, *tickMs)
	go hub.Run()

	// player connection: /join/{id}
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
			id:       playerID,
			conn:     conn,
			hub:      hub,
			send:     make(chan []byte, 16),
			pos:      hub.maze.Start,
			joinedAt: time.Now(),
		}

		if err := hub.Join(p); err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"error","msg":%q}`, err.Error())))
			conn.Close()
			return
		}

		p.send <- []byte(fmt.Sprintf(`{"type":"welcome","player_id":%q}`, playerID))
		p.send <- hub.playerState(p)
		hub.broadcastSpectatorState()

		go p.WritePump()
		p.ReadPump()
	})

	// spectator connection: /spectate
	http.HandleFunc("/spectate", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("spectator upgrade error: %v", err)
			return
		}

		s := &Spectator{
			conn: conn,
			send: make(chan []byte, 64),
		}

		hub.joinSpectator <- s
		go s.WritePump()
		s.ReadPump(hub)
	})

	// serve spectator UI
	http.Handle("/", http.FileServer(http.Dir("static")))

	log.Println("server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
