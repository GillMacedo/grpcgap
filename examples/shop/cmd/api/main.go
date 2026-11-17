// Command api serves the shop's gRPC API.
package main

import (
	"flag"
	"log"
	"net"

	"example.com/shop/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:50051", "listen address")
	flag.Parse()
	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(server.New().Serve(lis))
}
