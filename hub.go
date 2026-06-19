package main

import (
	"fmt"
	"log"
)

type joinRequest struct {
	player *Player
	result chan error
}

type Hub struct {
	players map[string]*Player
	join    chan joinRequest
	leave   chan *Player
}

func NewHub() *Hub {
	return &Hub{
		players: make(map[string]*Player),
		join:    make(chan joinRequest),
		leave:   make(chan *Player),
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
			}
		}
	}
}

func (h *Hub) Join(p *Player) error {
	result := make(chan error, 1)
	h.join <- joinRequest{player: p, result: result}
	return <-result
}
