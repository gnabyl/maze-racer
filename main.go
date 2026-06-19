package main

import (
	"crypto/subtle"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed static
var staticFiles embed.FS

// basicAuth gates a handler behind HTTP Basic Auth. The browser sends the
// stored credentials on both the page load and the WebSocket upgrade, so one
// gate protects the admin UI and the /spectate socket together.
func basicAuth(pass string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, got, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="maze-admin"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	rooms := flag.Int("rooms", 15, "maze size (number of rooms per side)")
	extraPass := flag.Float64("extra", 0.1, "fraction of extra passages (0.0-1.0)")
	tickMs := flag.Int("tick", 300, "tick rate in milliseconds")
	addr := flag.String("addr", "", "listen address (overrides PORT env; default :8080)")
	flag.Parse()

	rng := rand.New(rand.NewSource(42))
	hub := NewHub(MazeConfig{Rooms: *rooms, ExtraPass: *extraPass}, rng, time.Duration(*tickMs)*time.Millisecond)
	log.Printf("maze: %dx%d rooms, extra=%.2f, tick=%dms", *rooms, *rooms, *extraPass, *tickMs)
	go hub.Run()

	// admin password gates the UI and /spectate. Since contestants share the
	// same listener (and could reach it via SSM port forwarding regardless of
	// bind address), the password — not the network — is the boundary.
	adminPass := os.Getenv("MAZE_ADMIN_PASS")
	if adminPass == "" {
		log.Println("WARNING: MAZE_ADMIN_PASS unset — admin endpoints are UNAUTHENTICATED")
	}
	gate := func(h http.Handler) http.Handler {
		if adminPass == "" {
			return h
		}
		return basicAuth(adminPass, h)
	}

	mux := http.NewServeMux()

	// /join/{id} — open to contestants
	mux.HandleFunc("/join/{id}", func(w http.ResponseWriter, r *http.Request) {
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

	// /spectate — admin control, gated
	mux.Handle("/spectate", gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})))

	// / — spectator UI, gated
	uiFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", gate(http.FileServer(http.FS(uiFS))))

	listenAddr := resolveAddr(*addr)
	log.Printf("server listening on %s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

// resolveAddr picks the listen address: -addr flag, then PORT env, then :8080.
func resolveAddr(flagAddr string) string {
	if flagAddr != "" {
		return flagAddr
	}
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}
