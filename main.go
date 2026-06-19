package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed static
var staticFiles embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	rooms := flag.Int("rooms", 15, "maze size (number of rooms per side)")
	extraPass := flag.Float64("extra", 0.1, "fraction of extra passages (0.0-1.0)")
	tickMs := flag.Int("tick", 300, "tick rate in milliseconds")
	gameAddr := flag.String("game-addr", ":8080", "game listener (contestants /join) — bind to 0.0.0.0 so it's reachable via SSM tunnel")
	adminAddr := flag.String("admin-addr", "127.0.0.1:9090", "admin listener (UI + /spectate) — bind loopback so only a local tunnel reaches it")
	flag.Parse()

	rng := rand.New(rand.NewSource(42))
	hub := NewHub(MazeConfig{Rooms: *rooms, ExtraPass: *extraPass}, rng, time.Duration(*tickMs)*time.Millisecond)
	log.Printf("maze: %dx%d rooms, extra=%.2f, tick=%dms", *rooms, *rooms, *extraPass, *tickMs)
	go hub.Run()

	// game mux: only /join/{id} — exposed to contestants
	gameMux := http.NewServeMux()
	gameMux.HandleFunc("/join/{id}", func(w http.ResponseWriter, r *http.Request) {
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

		p, err := hub.Join(playerID, conn)
		if err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"error","msg":%q}`, err.Error())))
			conn.Close()
			return
		}

		p.hub = hub
		p.send <- []byte(fmt.Sprintf(`{"type":"welcome","player_id":%q}`, playerID))
		if hub.started {
			p.send <- hub.playerState(p)
		} else {
			p.send <- []byte(`{"type":"waiting"}`)
		}

		go p.WritePump()
		p.ReadPump()
	})

	// admin mux: spectator UI + /spectate (start/restart control) — loopback only
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("/spectate", func(w http.ResponseWriter, r *http.Request) {
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

	uiFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}
	adminMux.Handle("/", http.FileServer(http.FS(uiFS)))

	// run admin listener in background, game listener in foreground
	go func() {
		log.Printf("admin  listening on %s (UI + /spectate)", *adminAddr)
		log.Fatal(http.ListenAndServe(*adminAddr, adminMux))
	}()

	log.Printf("game   listening on %s (/join)", *gameAddr)
	log.Fatal(http.ListenAndServe(*gameAddr, gameMux))
}
