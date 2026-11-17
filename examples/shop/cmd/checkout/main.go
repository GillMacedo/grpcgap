// Command checkout reads an order from the shop's API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	shopv1 "example.com/shop/gen/shop/v1"
	"example.com/shop/internal/ordersclient"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:50051", "API address")
	flag.Parse()
	conn, err := grpc.NewClient(*addr, ordersclient.DialOptions()...)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx := metadata.AppendToOutgoingContext(context.Background(), "role", "user")
	ord, err := shopv1.NewOrdersClient(conn).GetOrder(ctx, &shopv1.GetOrderRequest{Id: "o-1"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("checkout:", ord.GetId(), ord.GetTotalCents())
}
