package main

import (
	"context"
	"log"
	"tcp-client/iface"
)

func main() {
	ctx := context.Background()
	i, err := iface.New(ctx, "tun0", "192.168.10.1/24")
	if err != nil {
		log.Fatalf("Failed to set up TUN interface: %v", err)
	}

	listener, _ := i.Bind(9000)
	stream, err := listener.Accept()
	if err != nil {
		log.Printf("Failed to accept connection on port 9000")
		return
	}
	log.Printf("Accepted connection on port 9000")

	buffer := make([]byte, 1024)
	for {
		n, err := stream.Read(buffer)
		if err != nil {
			log.Printf("Failed to read from stream: %v", err)
			return
		}
		log.Printf("Received data: %s", string(buffer[:n]))
	}
}
