// Command restapi is a CRUD service over an in-memory store, using binding for
// requests and render for responses.
//
//	go run ./examples/restapi
//	curl localhost:8080/api/v1/users
//	curl -X POST localhost:8080/api/v1/users \
//	     -H 'Content-Type: application/json' \
//	     -d '{"name":"octocat","email":"octo@example.com","role":"admin"}'
//	curl localhost:8080/api/v1/users/1
//	curl -X DELETE localhost:8080/api/v1/users/1
package main

import (
	"errors"
	"log"
	"net/http"
	"sync"

	"github.com/gozarp/zarp"
	"github.com/gozarp/zarp/binding"
	"github.com/gozarp/zarp/middleware"
)

// User is what the store holds and what the API returns.
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// createUser is the request body. The binding tags are the validation rules,
// checked by binding.Validate after the body has been decoded.
type createUser struct {
	Name  string `json:"name"  binding:"required,min=2,max=64"`
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role"  binding:"required,oneof=admin member"`
}

// listQuery is bound from the query string, using `form` tags.
type listQuery struct {
	Role  string `form:"role"  binding:"oneof=admin member"`
	Limit int    `form:"limit" binding:"min=1,max=100"`
}

func main() {
	store := newStore()

	r := zarp.New()
	r.Use(middleware.Default()...)

	api := r.Group("/api/v1")
	api.GET("/users", store.list)
	api.POST("/users", store.create)
	api.GET("/users/:id", store.get)
	api.DELETE("/users/:id", store.remove)

	log.Println("listening on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}

// ---------------------------------------------------------------- handlers

func (s *store) list(c *zarp.Context) {
	var q listQuery
	if err := binding.Query(c, &q); err != nil {
		bindingFailed(c, err)
		return
	}

	users := s.all()
	if q.Role != "" {
		filtered := users[:0]
		for _, u := range users {
			if u.Role == q.Role {
				filtered = append(filtered, u)
			}
		}
		users = filtered
	}
	if q.Limit > 0 && len(users) > q.Limit {
		users = users[:q.Limit]
	}

	c.JSON(http.StatusOK, users)
}

func (s *store) create(c *zarp.Context) {
	var in createUser
	// Decoding and validating are separate steps, so the response can say which
	// one failed: a body that is not JSON is not the same problem as a body that
	// is JSON but names an unknown role.
	if err := binding.JSON(c, &in); err != nil {
		bindingFailed(c, err)
		return
	}
	if err := binding.Validate(in); err != nil {
		bindingFailed(c, err)
		return
	}

	user := s.add(User{Name: in.Name, Email: in.Email, Role: in.Role})
	c.Header("Location", "/api/v1/users/"+itoa(user.ID))
	c.JSON(http.StatusCreated, user)
}

func (s *store) get(c *zarp.Context) {
	// URI binding turns route parameters into a struct, with the same rules.
	var params struct {
		ID int `uri:"id" binding:"required,min=1"`
	}
	if err := binding.URI(c, &params); err != nil {
		bindingFailed(c, err)
		return
	}

	user, ok := s.find(params.ID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no such user"})
		return
	}
	c.JSON(http.StatusOK, user)
}

func (s *store) remove(c *zarp.Context) {
	var params struct {
		ID int `uri:"id" binding:"required,min=1"`
	}
	if err := binding.URI(c, &params); err != nil {
		bindingFailed(c, err)
		return
	}

	if !s.delete(params.ID) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no such user"})
		return
	}
	c.Status(http.StatusNoContent)
}

// bindingFailed maps the three ways a request body can be wrong onto the three
// statuses that mean them: 415 for a media type we do not read, 422 for a
// well-formed body whose values are unacceptable, and 400 for everything else.
func bindingFailed(c *zarp.Context, err error) {
	var invalid binding.ValidationErrors
	switch {
	case errors.Is(err, binding.ErrUnsupportedMediaType):
		c.JSON(http.StatusUnsupportedMediaType, map[string]string{"error": err.Error()})

	case errors.As(err, &invalid):
		fields := make([]map[string]string, len(invalid))
		for i, fe := range invalid {
			fields[i] = map[string]string{"field": fe.Field, "rule": fe.Rule, "message": fe.Msg}
		}
		c.JSON(http.StatusUnprocessableEntity, map[string]any{"errors": fields})

	default:
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

// ---------------------------------------------------------------- store

type store struct {
	mu     sync.RWMutex
	users  map[int]User
	nextID int
}

func newStore() *store {
	s := &store{users: make(map[int]User), nextID: 1}
	s.add(User{Name: "octocat", Email: "octo@example.com", Role: "admin"})
	s.add(User{Name: "hubot", Email: "hubot@example.com", Role: "member"})
	return s
}

func (s *store) add(u User) User {
	s.mu.Lock()
	defer s.mu.Unlock()
	u.ID = s.nextID
	s.nextID++
	s.users[u.ID] = u
	return u
}

func (s *store) all() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.users))
	for id := 1; id < s.nextID; id++ {
		if u, ok := s.users[id]; ok {
			out = append(out, u)
		}
	}
	return out
}

func (s *store) find(id int) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	return u, ok
}

func (s *store) delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return false
	}
	delete(s.users, id)
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
