package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// Handler processes a decoded request and returns a result or error.
type Handler func(ctx context.Context, s *Session, req *RequestMsg) (result interface{}, err *HandlerError)

type HandlerError struct {
	Code    ErrorCode
	Message string
}

func (e *HandlerError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func paramErr(format string, args ...interface{}) *HandlerError {
	return &HandlerError{Code: ErrInvalidParams, Message: fmt.Sprintf(format, args...)}
}

func notFound(msg string) *HandlerError { return &HandlerError{Code: ErrNotFound, Message: msg} }

func internalErr(err error) *HandlerError {
	return &HandlerError{Code: ErrInternal, Message: err.Error()}
}

// Router maps method names to handlers.
type Router struct {
	handlers map[string]Handler
}

func NewRouter() *Router { return &Router{handlers: make(map[string]Handler)} }

func (r *Router) Register(method string, h Handler) { r.handlers[method] = h }

func (r *Router) Dispatch(ctx context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	if !s.isAuthed() && req.Method != "auth.login" {
		return nil, &HandlerError{Code: ErrForbidden, Message: "not authenticated"}
	}
	h, ok := r.handlers[req.Method]
	if !ok {
		return nil, &HandlerError{Code: ErrUnknownMethod, Message: "unknown method: " + req.Method}
	}
	return h(ctx, s, req)
}

// decodeParams unmarshals req.Params into dst. Returns paramErr on failure.
func decodeParams(req *RequestMsg, dst interface{}) *HandlerError {
	if len(req.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(req.Params, dst); err != nil {
		return paramErr("invalid params: %v", err)
	}
	return nil
}
