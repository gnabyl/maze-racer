# Maze Runner — Contestant Guide

Write a bot (any language) that connects to the maze server, navigates a
fog-of-war maze, and reaches the flag in the fewest moves.

## Goal

All players spawn at the **same start cell**. Somewhere in the maze is a
**flag**. First to reach it wins; ranking is by **number of moves taken**
(fewest = best). You only ever see a 5×5 window around yourself — the rest is
fog.

## Connect

Open a WebSocket to the game server, where the last path segment is your chosen
player id (your name):

```
ws://<PUBLIC_IP>:8080/join/<your-name>
```

- Pick a unique id. If that id is already connected, the server rejects you with
  an error and closes the socket.
- If you disconnect and reconnect with the **same id**, your position and move
  count are restored (you resume where you were).

All messages in both directions are **JSON text frames**.

## Message flow

```
1. you connect
2. server → {"type":"welcome","player_id":"<you>"}
3. server → {"type":"waiting"}           (if the game hasn't started yet)
   ... when the admin starts the game ...
   server → {"type":"state", ...}        (your first view)
4. you   → {"move":"UP"}                 (one move)
5. server→ {"type":"state", ...}         (your new view, next tick)
   ... repeat 4–5 until you reach the flag ...
6. server → {"type":"win", ...}          (you reached the flag)
```

Send a move, get a new state back, send the next move. Simple request/response
loop, paced by the server tick.

## Server → client messages

| `type` | When | Fields |
|--------|------|--------|
| `welcome` | on connect | `player_id` |
| `waiting` | game not started yet | — |
| `state` | your current view | `fog` (25 ints), `pos` (`{r,c}`) |
| `error` | invalid move / bad id | `msg` |
| `win` | you reached the flag | `player`, `moves`, `rankings` |

Examples:
```json
{"type":"welcome","player_id":"alice"}
{"type":"waiting"}
{"type":"state","fog":[15,9,...],"pos":{"r":3,"c":7}}
{"type":"error","msg":"wall"}
{"type":"win","player":"alice","moves":42,
 "rankings":[{"rank":1,"id":"alice","moves":42}]}
```

Notes:
- `error` with `"msg":"wall"` means your last move hit a wall — you didn't move.
  Other errors: `"unknown direction: X"`, or a duplicate-id rejection at connect.
- `win.moves` is your final move count; `rankings` is the standings so far.
- Ignore message types you don't recognise (more may be added).

## Client → server messages

Exactly one shape:
```json
{"move":"UP"}
```
`move` is one of: **`UP`**, **`DOWN`**, **`LEFT`**, **`RIGHT`** (upper-case).

## The fog (your 5×5 view)

`fog` is a flat array of **25 integers**, row-major, representing the 5×5 block
of cells centred on you. **You are always at index 12** (the centre):

```
 0  1  2  3  4
 5  6  7  8  9
10 11 12 13 14     <- 12 is you
15 16 17 18 19
20 21 22 23 24
```

Neighbours of your cell:
- index `7` = one cell **UP**
- index `17` = one cell **DOWN**
- index `11` = one cell **LEFT**
- index `13` = one cell **RIGHT**

### Cell value = wall bitmask

Each integer encodes which sides of that cell have walls, as a bitmask:

| Bit | Value | Wall on side |
|-----|-------|--------------|
| 0 | `1` | UP |
| 1 | `2` | DOWN |
| 2 | `4` | RIGHT |
| 3 | `8` | LEFT |
| 4 | `16` | **flag is in this cell** |

So a cell value of `0` is fully open; `15` (`1+2+4+8`) is walled on all four
sides. `16` added on top means the flag is there.

**To decide if you can move**, check the walls of *your own* cell (`fog[12]`):
```
canGoUp    = (fog[12] & 1) == 0
canGoDown  = (fog[12] & 2) == 0
canGoRight = (fog[12] & 4) == 0
canGoLeft  = (fog[12] & 8) == 0
```
Moving into a wall is rejected (`{"type":"error","msg":"wall"}`) and wastes a
tick — check before moving.

**Spotting the flag:** any fog cell with bit `16` set holds the flag. e.g. if
`fog[13] & 16` is non-zero, the flag is one cell to your RIGHT — move `RIGHT`.

### Out of bounds

Cells outside the maze edge appear as `15` (all walls). Treat them as solid.

## Coordinates

`pos` is `{"r":row, "c":col}`, zero-based from the top-left.
- `UP` decreases `r`, `DOWN` increases `r`
- `LEFT` decreases `c`, `RIGHT` increases `c`

You don't need absolute coordinates to play (fog is relative), but `pos` lets
you map the maze as you explore.

## Tick mechanics — important

The server advances on a fixed **tick** (default 300 ms). Each tick it applies
**at most one move per player**:

- If you send several moves between ticks, only the **most recent** is used; the
  rest are discarded. Don't spam — send one move, wait for the next `state`.
- If you send nothing, you simply don't move that tick.
- The natural loop is: receive `state` → decide → send one `move` → repeat.

## Winning & ranking

- Reach the flag cell → you get a `win` message and stop (further moves ignored).
- Ranking is by **move count**, not wall-clock time — so a slow network doesn't
  hurt you. Fewer moves = higher rank.
- The maze can have multiple paths to the flag; the shortest route you can find
  wins you a better rank.

## Minimal bot (pseudocode)

```
connect ws://<ip>:8080/join/myname

on message m:
    if m.type == "state":
        fog = m.fog
        # flag visible? step toward it
        # else pick any open direction (avoid walls via fog[12])
        dirs = []
        if fog[12] & 1 == 0: dirs.add("UP")
        if fog[12] & 2 == 0: dirs.add("DOWN")
        if fog[12] & 4 == 0: dirs.add("RIGHT")
        if fog[12] & 8 == 0: dirs.add("LEFT")
        send {"move": choose(dirs)}
    elif m.type == "win":
        stop
    # ignore welcome / waiting / error (or log them)
```

A random open-direction walker works but ranks poorly. Track where you've been
(using `pos`), avoid backtracking, and head toward the flag bit when you see it.

## Develop against a local server

You don't need the contest server to build your bot — run one locally.

Requires Go. From the repo root:

```bash
# 1. start a server (no password needed locally; admin UI is open)
go run .                        # listens on :8080

# 2. open the spectator UI and click START GAME
#    → http://localhost:8080

# 3. point your bot at the local server
#    ws://localhost:8080/join/<your-name>
```

The game won't send `state` until you press **START** in the UI — until then
your bot gets `{"type":"waiting"}`. Use **RESTART** in the UI to generate a
fresh maze (and adjust rooms / tick rate).

Reference client (random walker) to sanity-check your setup:
```bash
go run ./client myname          # defaults to ws://localhost:8080
```

Tuning the local server:
```bash
go run . -rooms 10 -tick 150    # smaller maze, faster ticks for quick iteration
```
Flags: `-rooms` (size), `-tick` (ms per move), `-extra` (0–1, extra passages),
`-addr` (listen address).

Good luck.
