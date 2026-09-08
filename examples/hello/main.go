// Command hello is the smallest zarp server there is.
//
//	go run ./examples/hello
//	curl localhost:8080/ping
package main

import (
	"log"
	"net/http"

	"github.com/gozarp/zarp"
)

func main() {
	r := zarp.New()

	r.GET("/ping", func(c *zarp.Context) {
		c.Text(http.StatusOK, "pong")
	})

	r.GET("/json", func(c *zarp.Context) {
		c.JSON(http.StatusOK, map[string]string{"message": "pong"})
	})

	log.Println("listening on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
