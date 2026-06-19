package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"

	"github.com/gorilla/websocket"
)

const (
	wallUp    = 1
	wallDown  = 2
	wallRight = 4
	wallLeft  = 8
)

var dirWall = map[string]int{
	"UP":    wallUp,
	"DOWN":  wallDown,
	"RIGHT": wallRight,
	"LEFT":  wallLeft,
}

func validMoves(fog []int) []string {
	cell := fog[12] // player always at center
	var valid []string
	for dir, wall := range dirWall {
		if cell&wall == 0 {
			valid = append(valid, dir)
		}
	}
	return valid
}

func main() {
	playerID := "player1"
	if len(os.Args) > 1 {
		playerID = os.Args[1]
	}

	url := fmt.Sprintf("ws://localhost:8080/join/%s", playerID)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		log.Fatal("connect error:", err)
	}
	defer conn.Close()

	log.Printf("connected as %s", playerID)

	rng := rand.New(rand.NewSource(int64(os.Getpid())))

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Fatal("read error:", err)
		}

		var resp struct {
			Type string `json:"type"`
			Fog  []int  `json:"fog"`
		}
		if err := json.Unmarshal(msg, &resp); err != nil || resp.Type != "state" {
			continue
		}

		moves := validMoves(resp.Fog)
		if len(moves) == 0 {
			continue
		}

		dir := moves[rng.Intn(len(moves))]
		payload, _ := json.Marshal(map[string]string{"move": dir})
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			log.Fatal("write error:", err)
		}
	}
}
