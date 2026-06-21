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
	bcast          chan func()
}

func NewHub(cfg MazeConfig, rng *rand.Rand, tickRate time.Duration) *Hub {
	h := &Hub{
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
		bcast:          make(chan func(), 16),
	}
	go h.runBroadcaster()
	return h
}

func (h *Hub) runBroadcaster() {
	for fn := range h.bcast {
		fn()
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
			for _, p := range h.players {
				p.won = false
				p.moves = 0
				p.pos = h.maze.Start
				trySend(p.send,h.playerState(p))
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
				trySend(p.send,[]byte(`{"type":"waiting"}`))
			}
			h.broadcastFull()
			log.Printf("game restarted: rooms=%d tick=%s", h.cfg.Rooms, h.tickRate)

		case s := <-h.joinSpectator:
			h.spectators[s] = true
			log.Printf("spectator joined (total: %d)", len(h.spectators))
			msg := h.spectatorFull()
			select {
			case h.bcast <- func() { s.send <- msg }:
			default:
				s.send <- msg // broadcaster full; send directly as fallback
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
			trySend(p.send,errMsg("unknown direction: " + dir))
			continue
		}
		d := dirs[di]
		cell := h.maze.Grid[p.pos.R][p.pos.C]
		if cell&d.wall != 0 {
			trySend(p.send,errMsg("wall"))
			continue
		}
		p.pos.R += d.dr
		p.pos.C += d.dc
		p.moves++
		trySend(p.send,h.playerState(p))

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
	if len(h.spectators) == 0 {
		return
	}
	targets := h.spectatorSnapshot()
	rankings := append([]rankEntry(nil), h.rankings...)
	select {
	case h.bcast <- func() {
		msg, _ := json.Marshal(msgMoved{Type: "moved", Players: movers, Rankings: rankings})
		for _, s := range targets {
			trySend(s.send, msg)
		}
	}:
	default: // broadcaster behind; drop this tick's spectator update
	}
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

type msgMoved struct {
	Type     string      `json:"type"`
	Players  []playerInfo `json:"players"`
	Rankings []rankEntry  `json:"rankings"`
}

type msgError struct {
	Type string `json:"type"`
	Msg  string `json:"msg"`
}

type msgSpectatorFull struct {
	Type     string       `json:"type"`
	Started  bool         `json:"started"`
	Rooms    int          `json:"rooms"`
	TickMs   int64        `json:"tick_ms"`
	Grid     []int        `json:"grid"`
	Players  []playerInfo `json:"players"`
	Rankings []rankEntry  `json:"rankings"`
}

type msgPositions struct {
	Type     string       `json:"type"`
	Started  bool         `json:"started"`
	Players  []playerInfo `json:"players"`
	Rankings []rankEntry  `json:"rankings"`
}

type msgWin struct {
	Type     string      `json:"type"`
	Player   string      `json:"player"`
	Moves    int         `json:"moves"`
	Rankings []rankEntry `json:"rankings"`
}

func (h *Hub) playerInfos() []playerInfo {
	out := make([]playerInfo, 0, len(h.players))
	for _, p := range h.players {
		out = append(out, playerInfo{ID: p.id, Pos: p.pos, Won: p.won, Moves: p.moves})
	}
	return out
}

func (h *Hub) playerState(p *Player) []byte {
	return h.maze.FogCache[p.pos.R][p.pos.C]
}

func errMsg(reason string) []byte {
	msg, _ := json.Marshal(msgError{Type: "error", Msg: reason})
	return msg
}

// spectatorFull includes the static maze grid. Sent once per spectator (on
// connect) and on restart (new maze) — NOT every tick.
func (h *Hub) spectatorFull() []byte {
	flat := make([]int, 0, h.maze.Rooms*h.maze.Rooms)
	for _, row := range h.maze.Grid {
		flat = append(flat, row...)
	}
	msg, _ := json.Marshal(msgSpectatorFull{
		Type:     "state",
		Started:  h.started,
		Rooms:    h.maze.Rooms,
		TickMs:   h.tickRate.Milliseconds(),
		Grid:     flat,
		Players:  h.playerInfos(),
		Rankings: h.rankings,
	})
	return msg
}

// spectatorPositions is the lightweight per-tick update: positions + rankings,
// no grid. Spectators cache the grid from the last full message.
func (h *Hub) spectatorPositions() []byte {
	msg, _ := json.Marshal(msgPositions{
		Type:     "positions",
		Started:  h.started,
		Players:  h.playerInfos(),
		Rankings: h.rankings,
	})
	return msg
}

func (h *Hub) broadcastWin(playerID string, moves int) {
	msg, _ := json.Marshal(msgWin{Type: "win", Player: playerID, Moves: moves, Rankings: h.rankings})
	if p, ok := h.players[playerID]; ok {
		trySend(p.send,msg)
	}
	h.sendSpectators(msg)
	log.Printf("player %s won in %d moves", playerID, moves)
}

func (h *Hub) spectatorSnapshot() []*Spectator {
	out := make([]*Spectator, 0, len(h.spectators))
	for s := range h.spectators {
		out = append(out, s)
	}
	return out
}

func (h *Hub) sendSpectators(msg []byte) {
	targets := h.spectatorSnapshot()
	select {
	case h.bcast <- func() {
		for _, s := range targets {
			trySend(s.send, msg)
		}
	}:
	default:
	}
}

// broadcastFull resends the grid (use on restart / new maze).
func (h *Hub) broadcastFull() { h.sendSpectators(h.spectatorFull()) }

// broadcastPositions sends the light positions-only update (per tick, join/leave).
func (h *Hub) broadcastPositions() {
	if len(h.spectators) == 0 {
		return
	}
	h.sendSpectators(h.spectatorPositions())
}
