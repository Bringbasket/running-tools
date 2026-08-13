package module

import (
	"net/http"

	"github.com/Bringbasket/running-tools/internal/platform/httpx"
)

type Backend interface {
	ID() string
	RegisterRoutes(mux *http.ServeMux, auth httpx.Middleware)
	Start()
	Stop()
}
