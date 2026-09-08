// Command params shows the three ways a route carries data: path parameters,
// a catch-all, and the query string.
//
//	go run ./examples/params
//	curl localhost:8080/users/42/posts/7
//	curl localhost:8080/files/css/app.css
//	curl "localhost:8080/search?q=go&tag=web&tag=api"
package main

import (
	"log"
	"net/http"

	"github.com/gozarp/zarp"
)

func main() {
	r := zarp.New()

	// ":name" matches one segment.
	r.GET("/users/:id/posts/:postID", func(c *zarp.Context) {
		c.JSON(http.StatusOK, map[string]string{
			"user": c.Param("id"),
			"post": c.Param("postID"),
			// FullPath is the pattern, not the URL: log this, not the id.
			"route": c.FullPath(),
		})
	})

	// "*name" matches the rest of the path, leading slash included, and must
	// be the last segment of the route.
	r.GET("/files/*filepath", func(c *zarp.Context) {
		c.JSON(http.StatusOK, map[string]string{"path": c.Param("filepath")})
	})

	r.GET("/search", func(c *zarp.Context) {
		c.JSON(http.StatusOK, map[string]any{
			"q":     c.Query("q"),
			"page":  c.DefaultQuery("page", "1"),
			"tags":  c.QueryArray("tag"),
			"agent": c.GetHeader("User-Agent"),
		})
	})

	log.Println("listening on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
