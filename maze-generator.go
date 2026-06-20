package main

import (
	"encoding/json"
	"math/rand"
)

// Wall bits stored in lower 4 bits of each cell.
// A cell value is the OR of whichever walls are present.
// Example: cell with up and left walls = WallUp | WallLeft = 1 | 8 = 9
const (
	WallUp    = 1  // 0001
	WallDown  = 2  // 0010
	WallRight = 4  // 0100
	WallLeft  = 8  // 1000
	BitFlag   = 16 // 0001 0000 — flag is at this cell
)

type Maze struct {
	Grid     [][]int // Rooms x Rooms grid, each cell = wall bitmask (0-15)
	Rooms    int
	Start    Pos
	Flag     Pos
	FogCache [][]json.RawMessage // [r][c] = precomputed {"type":"state","fog":[...],"pos":{...}}
}

type MazeConfig struct {
	Rooms     int     // grid dimension (e.g. 10 = 10x10 rooms)
	ExtraPass float64 // fraction of remaining walls to remove (0.0=perfect maze, 0.1=10% extra passages)
}

// dirs encodes the 4 movement directions.
// dr/dc: step to reach neighbor room.
// wall: bit to clear on the current cell (the wall facing that direction).
// opposite: bit to clear on the neighbor cell (the wall facing back).
// Both must be cleared so each cell knows which of its sides are open.
var dirs = []struct {
	dr, dc   int
	wall     int
	opposite int
}{
	{-1, 0, WallUp, WallDown},    // UP:    step up one row
	{1, 0, WallDown, WallUp},     // DOWN:  step down one row
	{0, 1, WallRight, WallLeft},  // RIGHT: step right one col
	{0, -1, WallLeft, WallRight}, // LEFT:  step left one col
}

// dirMap maps move commands to their dirs index.
var dirMap = map[string]int{
	"UP":    0,
	"DOWN":  1,
	"RIGHT": 2,
	"LEFT":  3,
}

func GenerateMaze(cfg MazeConfig, rng *rand.Rand) *Maze {
	rooms := cfg.Rooms

	// initialise every cell with all 4 walls present
	grid := make([][]int, rooms)
	for i := range grid {
		grid[i] = make([]int, rooms)
		for j := range grid[i] {
			grid[i][j] = WallUp | WallDown | WallRight | WallLeft
		}
	}

	visited := make([][]bool, rooms)
	for i := range visited {
		visited[i] = make([]bool, rooms)
	}

	// DFS from a random room to carve a perfect maze (exactly one path between any two rooms)
	startRow := rng.Intn(rooms)
	startCol := rng.Intn(rooms)
	dfs(grid, visited, startRow, startCol, rooms, rng)

	// punch extra holes to create alternate paths (multiple solutions)
	if cfg.ExtraPass > 0 {
		carveExtra(grid, cfg, rooms, rng)
	}

	// pick two distinct random rooms for start and flag
	start := Pos{R: rng.Intn(rooms), C: rng.Intn(rooms)}
	flag := start
	for flag == start {
		flag = Pos{R: rng.Intn(rooms), C: rng.Intn(rooms)}
	}

	grid[flag.R][flag.C] |= BitFlag

	m := &Maze{
		Grid:  grid,
		Rooms: rooms,
		Start: start,
		Flag:  flag,
	}
	m.FogCache = buildFogCache(m)
	return m
}

func buildFogCache(m *Maze) [][]json.RawMessage {
	cache := make([][]json.RawMessage, m.Rooms)
	for r := range cache {
		cache[r] = make([]json.RawMessage, m.Rooms)
		for c := range cache[r] {
			pos := Pos{R: r, C: c}
			cache[r][c], _ = json.Marshal(fogState{Type: "state", Fog: m.fog(pos), Pos: pos})
		}
	}
	return cache
}

// fog computes the raw 25-element fog slice (internal use during cache build).
func (m *Maze) fog(p Pos) []int {
	fog := make([]int, 25)
	for i := range fog {
		fog[i] = WallUp | WallDown | WallRight | WallLeft
	}
	for dr := -2; dr <= 2; dr++ {
		for dc := -2; dc <= 2; dc++ {
			r, c := p.R+dr, p.C+dc
			fi := (dr+2)*5 + (dc + 2)
			if r >= 0 && r < m.Rooms && c >= 0 && c < m.Rooms {
				fog[fi] = m.Grid[r][c]
			}
		}
	}
	return fog
}

// dfs recursively visits unvisited neighbors in random order,
// clearing wall bits between each pair to open a passage.
func dfs(grid [][]int, visited [][]bool, row, col, rooms int, rng *rand.Rand) {
	visited[row][col] = true

	// visit neighbors in random order so the maze looks different each time
	for _, i := range rng.Perm(4) {
		d := dirs[i]
		nr, nc := row+d.dr, col+d.dc

		if nr < 0 || nr >= rooms || nc < 0 || nc >= rooms {
			continue
		}
		if visited[nr][nc] {
			continue
		}

		// carve: remove the shared wall from both sides
		grid[row][col] &^= d.wall     // clear wall on current cell
		grid[nr][nc] &^= d.opposite   // clear the matching wall on neighbor
		dfs(grid, visited, nr, nc, rooms, rng)
	}
}

// carveExtra removes a random fraction of remaining interior walls
// to introduce loops and alternate routes through the maze.
func carveExtra(grid [][]int, cfg MazeConfig, rooms int, rng *rand.Rand) {
	type wall struct{ r, c, dir int }
	var candidates []wall

	// collect every wall that still separates two rooms
	for r := 0; r < rooms; r++ {
		for c := 0; c < rooms; c++ {
			if c+1 < rooms && grid[r][c]&WallRight != 0 {
				candidates = append(candidates, wall{r, c, WallRight})
			}
			if r+1 < rooms && grid[r][c]&WallDown != 0 {
				candidates = append(candidates, wall{r, c, WallDown})
			}
		}
	}

	rng.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	n := int(float64(len(candidates)) * cfg.ExtraPass)
	for i := 0; i < n; i++ {
		w := candidates[i]
		switch w.dir {
		case WallRight:
			grid[w.r][w.c] &^= WallRight
			grid[w.r][w.c+1] &^= WallLeft
		case WallDown:
			grid[w.r][w.c] &^= WallDown
			grid[w.r+1][w.c] &^= WallUp
		}
	}
}

