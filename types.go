package main

type Pos struct {
	R int `json:"r"`
	C int `json:"c"`
}

func trySend(ch chan []byte, msg []byte) {
	select {
	case ch <- msg:
	default:
	}
}
