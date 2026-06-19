package main

import (
	"fmt"
	"log"
	"os"

	"github.com/gorilla/websocket"
)

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

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Fatal("read error:", err)
		}
		fmt.Printf("server: %s\n", msg)
	}
}
