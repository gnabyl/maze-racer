package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
)

type joinRequest struct {
	player *Player
	result chan error
}

type Hub struct {
	maze       *Maze
	players    map[string]*Player
	spectators map[*Spectator]bool

	join           chan joinRequest
	leave          chan *Player
	joinSpectator  chan *Spectator
	leaveSpectator chan *Spectator
}

func NewHub(cfg MazeConfig, rng *rand.Rand) *Hub {
	return &Hub{
		maze:           GenerateMaze(cfg, rng),
		players:        make(map[string]*Player),
		spectators:     make(map[*Spectator]bool),
		join:           make(chan joinRequest),
		leave:          make(chan *Player),
		joinSpectator:  make(chan *Spectator),
		leaveSpectator: make(chan *Spectator),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case req := <-h.join:
			p := req.player
			if _, exists := h.players[p.id]; exists {
				req.result <- fmt.Errorf("player id %q already connected", p.id)
				continue
			}
			h.players[p.id] = p
			req.result <- nil
			log.Printf("player joined: %s (total: %d)", p.id, len(h.players))

		case p := <-h.leave:
			if current, ok := h.players[p.id]; ok && current == p {
				delete(h.players, p.id)
				close(p.send)
				log.Printf("player left: %s (total: %d)", p.id, len(h.players))
				h.broadcastSpectatorState()
			}

		case s := <-h.joinSpectator:
			h.spectators[s] = true
			log.Printf("spectator joined (total: %d)", len(h.spectators))
			// send current state immediately on connect
			if msg, err := h.spectatorState(); err == nil {
				s.send <- msg
			}

		case s := <-h.leaveSpectator:
			if h.spectators[s] {
				delete(h.spectators, s)
				close(s.send)
				log.Printf("spectator left (total: %d)", len(h.spectators))
			}
		}
	}
}

func (h *Hub) Join(p *Player) error {
	result := make(chan error, 1)
	h.join <- joinRequest{player: p, result: result}
	return <-result
}

// spectatorState builds the full maze state message for spectators.
func (h *Hub) spectatorState() ([]byte, error) {
	// flatten grid to 1D array
	flat := make([]int, 0, h.maze.Rooms*h.maze.Rooms)
	for _, row := range h.maze.Grid {
		flat = append(flat, row...)
	}

	type playerInfo struct {
		ID  string `json:"id"`
		Pos Pos    `json:"pos"`
	}
	players := make([]playerInfo, 0, len(h.players))
	for _, p := range h.players {
		players = append(players, playerInfo{ID: p.id, Pos: p.pos})
	}

	return json.Marshal(map[string]any{
		"type":    "state",
		"rooms":   h.maze.Rooms,
		"grid":    flat,
		"players": players,
	})
}

func (h *Hub) broadcastSpectatorState() {
	msg, err := h.spectatorState()
	if err != nil {
		return
	}
	for s := range h.spectators {
		select {
		case s.send <- msg:
		default:
			// slow spectator: drop the frame
		}
	}
}
