package binary

import "net/http"

// TntIoDoer is an interface that wraps the Do method.
type TntIoDoer interface {
	// Do sends an HTTP request and returns Body data from HTTP response.
	Do(req *http.Request) ([]byte, error)

	// Token provides the session token after the Do method is executed.
	Token() string
}
