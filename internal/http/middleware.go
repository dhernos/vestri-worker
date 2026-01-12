package http

import "net/http"

type Middleware func(http.Handler) http.Handler

func chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

func withMiddlewares(handler http.Handler) http.Handler {
	return chain(handler, tlsMiddleware(), authMiddleware(), rateLimitMiddleware())
}
