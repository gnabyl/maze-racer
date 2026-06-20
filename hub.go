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
			if _, ok := h.players[req.id]; ok {
				req.result <- joinResult{err: fmt.Errorf("player id %q already connected", req.id)}
				continue
			}
			p := &Player{
				id:   req.id,
				conn: req.conn,
				hub:  h,
				send: make(chan []byte, 16),
				pos:  h.maze.Start,
			}
			h.players[req.id] = p
			req.result <- joinResult{player: p}
			log.Printf("player joined: %s (total: %d)", req.id, len(h.players))
			h.broadcastPositions()

		case p := <-h.leave:
			// drop the player entirely on disconnect (no reconnect/resume)
			if current, ok := h.players[p.id]; ok && current == p {
				delete(h.players, p.id)
				close(p.send)
				log.Printf("player left: %s (total: %d)", p.id, len(h.players))
				h.broadcastPositions()
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
				p.trySend(h.playerState(p))
			}
			h.broadcastFull()
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
				p.trySend([]byte(`{"type":"waiting"}`))
			}
			h.broadcastFull()
			log.Printf("game restarted: rooms=%d tick=%s", h.cfg.Rooms, h.tickRate)

		case s := <-h.joinSpectator:
			h.spectators[s] = true
			log.Printf("spectator joined (total: %d)", len(h.spectators))
			s.send <- h.spectatorFull()

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
	var movers []playerInfo // only players that moved this tick (spectator delta)
	for _, p := range h.players {
		if p.won {
			continue
		}
		dir := p.popMove()
		if dir == "" {
			continue
		}
		di, ok := dirMap[dir]
		if !ok {
			p.trySend(errMsg("unknown direction: " + dir))
			continue
		}
		d := dirs[di]
		cell := h.maze.Grid[p.pos.R][p.pos.C]
		if cell&d.wall != 0 {
			p.trySend(errMsg("wall"))
			continue
		}
		p.pos.R += d.dr
		p.pos.C += d.dc
		p.moves++
		p.trySend(h.playerState(p))

		if p.pos == h.maze.Flag {
			p.won = true
			h.rankings = append(h.rankings, rankEntry{
				Rank:  len(h.rankings) + 1,
				ID:    p.id,
				Moves: p.moves,
			})
			h.broadcastWin(p.id, p.moves)
		}
		movers = append(movers, playerInfo{ID: p.id, Pos: p.pos, Won: p.won, Moves: p.moves})
	}
	if len(movers) > 0 {
		h.broadcastMoved(movers)
	}
}

// broadcastMoved sends only the players that moved this tick (delta). Spectators
// merge these into their cached set — far smaller than the full roster.
func (h *Hub) broadcastMoved(movers []playerInfo) {
	msg, _ := json.Marshal(map[string]any{
		"type":     "moved",
		"players":  movers,
		"rankings": h.rankings,
	})
	h.sendSpectators(msg)
}

func (h *Hub) Join(id string, conn *websocket.Conn) (*Player, error) {
	result := make(chan joinResult, 1)
	h.join <- joinRequest{id: id, conn: conn, result: result}
	res := <-result
	return res.player, res.err
}

type fogState struct {
	Type string `json:"type"`
	Fog  []int  `json:"fog"`
	Pos  Pos    `json:"pos"`
}

type playerInfo struct {
	ID    string `json:"id"`
	Pos   Pos    `json:"pos"`
	Won   bool   `json:"won"`
	Moves int    `json:"moves"`
}

func (h *Hub) playerInfos() []playerInfo {
	out := make([]playerInfo, 0, len(h.players))
	for _, p := range h.players {
		out = append(out, playerInfo{ID: p.id, Pos: p.pos, Won: p.won, Moves: p.moves})
	}
	return out
}

func (h *Hub) playerState(p *Player) []byte {
	msg, _ := json.Marshal(fogState{Type: "state", Fog: h.maze.Fog(p.pos), Pos: p.pos})
	return msg
}

func errMsg(reason string) []byte {
	msg, _ := json.Marshal(map[string]any{"type": "error", "msg": reason})
	return msg
}

// spectatorFull includes the static maze grid. Sent once per spectator (on
// connect) and on restart (new maze) — NOT every tick.
func (h *Hub) spectatorFull() []byte {
	flat := make([]int, 0, h.maze.Rooms*h.maze.Rooms)
	for _, row := range h.maze.Grid {
		flat = append(flat, row...)
	}
	msg, _ := json.Marshal(map[string]any{
		"type":     "state",
		"started":  h.started,
		"rooms":    h.maze.Rooms,
		"tick_ms":  h.tickRate.Milliseconds(),
		"grid":     flat,
		"players":  h.playerInfos(),
		"rankings": h.rankings,
	})
	return msg
}

// spectatorPositions is the lightweight per-tick update: positions + rankings,
// no grid. Spectators cache the grid from the last full message.
func (h *Hub) spectatorPositions() []byte {
	msg, _ := json.Marshal(map[string]any{
		"type":     "positions",
		"started":  h.started,
		"players":  h.playerInfos(),
		"rankings": h.rankings,
	})
	return msg
}

func (h *Hub) broadcastWin(playerID string, moves int) {
	msg, _ := json.Marshal(map[string]any{
		"type":     "win",
		"player":   playerID,
		"moves":    moves,
		"rankings": h.rankings,
	})
	if p, ok := h.players[playerID]; ok {
		p.trySend(msg)
	}
	h.sendSpectators(msg)
	log.Printf("player %s won in %d moves", playerID, moves)
}

func (h *Hub) sendSpectators(msg []byte) {
	for s := range h.spectators {
		select {
		case s.send <- msg:
		default:
		}
	}
}

// broadcastFull resends the grid (use on restart / new maze).
func (h *Hub) broadcastFull() { h.sendSpectators(h.spectatorFull()) }

// broadcastPositions sends the light positions-only update (per tick, join/leave).
func (h *Hub) broadcastPositions() { h.sendSpectators(h.spectatorPositions()) }
