package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/gorilla/websocket"
)

type joinResult struct {
	player *Player
	err    error
}

type joinRequest struct {
	id   string
	conn *websocket.Conn
	result chan joinResult
}

type rankEntry struct {
	Rank  int    `json:"rank"`
	ID    string `json:"id"`
	Moves int    `json:"moves"`
}

type Hub struct {
	cfg        MazeConfig
	rng        *rand.Rand
	maze       *Maze
	players    map[string]*Player
	spectators map[*Spectator]bool
	tickRate   time.Duration
	started    bool
	rankings   []rankEntry

	join           chan joinRequest
	leave          chan *Player
	startGame      chan struct{}
	restartGame    chan restartRequest
	joinSpectator  chan *Spectator
	leaveSpectator chan *Spectator
}

func NewHub(cfg MazeConfig, rng *rand.Rand, tickRate time.Duration) *Hub {
	return &Hub{
		cfg:            cfg,
		rng:            rng,
		maze:           GenerateMaze(cfg, rng),
		players:        make(map[string]*Player),
		spectators:     make(map[*Spectator]bool),
		tickRate:       tickRate,
		join:           make(chan joinRequest),
		leave:          make(chan *Player),
		startGame:      make(chan struct{}, 1),
		restartGame:    make(chan restartRequest, 1),
		joinSpectator:  make(chan *Spectator),
		leaveSpectator: make(chan *Spectator),
	}
}

func (h *Hub) Run() {
	ticker := time.NewTicker(h.tickRate)
	defer ticker.Stop()

	for {
		select {
		case req := <-h.join:
			if existing, ok := h.players[req.id]; ok {
				if existing.connected {
					req.result <- joinResult{err: fmt.Errorf("player id %q already connected", req.id)}
					continue
				}
				// reconnect: restore state with new connection
				existing.conn = req.conn
				existing.send = make(chan []byte, 16)
				existing.connected = true
				existing.pendingMove.Store(nil)
				req.result <- joinResult{player: existing}
				log.Printf("player reconnected: %s", req.id)
			} else {
				p := &Player{
					id:        req.id,
					conn:      req.conn,
					hub:       h,
					send:      make(chan []byte, 16),
					pos:       h.maze.Start,
					connected: true,
				}
				h.players[req.id] = p
				req.result <- joinResult{player: p}
				log.Printf("player joined: %s (total: %d)", req.id, len(h.players))
			}
			h.broadcastSpectatorState()

		case p := <-h.leave:
			if current, ok := h.players[p.id]; ok && current == p {
				p.connected = false
				close(p.send)
				log.Printf("player disconnected: %s", p.id)
				h.broadcastSpectatorState()
			}

		case <-h.startGame:
			if h.started {
				continue
			}
			h.started = true
			h.rankings = nil
			now := time.Now()
			for _, p := range h.players {
				p.joinedAt = now
				p.won = false
				p.moves = 0
				p.pos = h.maze.Start
				p.send <- h.playerState(p)
			}
			h.broadcastSpectatorState()
			log.Printf("game started with %d players", len(h.players))

		case req := <-h.restartGame:
			if req.Rooms > 0 {
				h.cfg.Rooms = req.Rooms
			}
			if req.Tick > 0 {
				h.tickRate = time.Duration(req.Tick) * time.Millisecond
				ticker.Reset(h.tickRate)
			}
			h.maze = GenerateMaze(h.cfg, h.rng)
			h.started = false
			h.rankings = nil
			for _, p := range h.players {
				p.won = false
				p.moves = 0
				p.pos = h.maze.Start
				p.pendingMove.Store(nil)
				p.send <- []byte(`{"type":"waiting"}`)
			}
			h.broadcastSpectatorState()
			log.Printf("game restarted: rooms=%d tick=%s", h.cfg.Rooms, h.tickRate)

		case s := <-h.joinSpectator:
			h.spectators[s] = true
			log.Printf("spectator joined (total: %d)", len(h.spectators))
			if msg, err := h.spectatorState(); err == nil {
				s.send <- msg
			}

		case s := <-h.leaveSpectator:
			if h.spectators[s] {
				delete(h.spectators, s)
				close(s.send)
				log.Printf("spectator left (total: %d)", len(h.spectators))
			}

		case <-ticker.C:
			if h.started {
				h.tick()
			}
		}
	}
}

func (h *Hub) tick() {
	moved := false
	for _, p := range h.players {
		if p.won || !p.connected {
			continue
		}
		dir := p.popMove()
		if dir == "" {
			continue
		}
		di, ok := dirMap[dir]
		if !ok {
			p.send <- errMsg("unknown direction: " + dir)
			continue
		}
		d := dirs[di]
		cell := h.maze.Grid[p.pos.R][p.pos.C]
		if cell&d.wall != 0 {
			p.send <- errMsg("wall")
			continue
		}
		p.pos.R += d.dr
		p.pos.C += d.dc
		p.moves++
		p.send <- h.playerState(p)
		moved = true

		if p.pos == h.maze.Flag {
			p.won = true
			h.rankings = append(h.rankings, rankEntry{
				Rank:  len(h.rankings) + 1,
				ID:    p.id,
				Moves: p.moves,
			})
			h.broadcastWin(p.id, p.moves)
		}
	}
	if moved {
		h.broadcastSpectatorState()
	}
}

func (h *Hub) Join(id string, conn *websocket.Conn) (*Player, error) {
	result := make(chan joinResult, 1)
	h.join <- joinRequest{id: id, conn: conn, result: result}
	res := <-result
	return res.player, res.err
}

func (h *Hub) playerState(p *Player) []byte {
	msg, _ := json.Marshal(map[string]any{
		"type": "state",
		"fog":  h.maze.Fog(p.pos),
		"pos":  p.pos,
	})
	return msg
}

func errMsg(reason string) []byte {
	msg, _ := json.Marshal(map[string]any{
		"type": "error",
		"msg":  reason,
	})
	return msg
}

func (h *Hub) spectatorState() ([]byte, error) {
	flat := make([]int, 0, h.maze.Rooms*h.maze.Rooms)
	for _, row := range h.maze.Grid {
		flat = append(flat, row...)
	}

	type playerInfo struct {
		ID        string `json:"id"`
		Pos       Pos    `json:"pos"`
		Won       bool   `json:"won"`
		Connected bool   `json:"connected"`
		Moves     int    `json:"moves"`
	}
	players := make([]playerInfo, 0, len(h.players))
	for _, p := range h.players {
		players = append(players, playerInfo{
			ID: p.id, Pos: p.pos, Won: p.won,
			Connected: p.connected, Moves: p.moves,
		})
	}

	return json.Marshal(map[string]any{
		"type":     "state",
		"started":  h.started,
		"rooms":    h.maze.Rooms,
		"tick_ms":  h.tickRate.Milliseconds(),
		"grid":     flat,
		"players":  players,
		"rankings": h.rankings,
	})
}

func (h *Hub) broadcastWin(playerID string, moves int) {
	msg, _ := json.Marshal(map[string]any{
		"type":     "win",
		"player":   playerID,
		"moves":    moves,
		"rankings": h.rankings,
	})
	if p, ok := h.players[playerID]; ok {
		p.send <- msg
	}
	for s := range h.spectators {
		select {
		case s.send <- msg:
		default:
		}
	}
	log.Printf("player %s won in %d moves", playerID, moves)
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
		}
	}
}
