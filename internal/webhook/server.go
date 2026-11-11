// Package webhook receives incident notifications over HTTP and sends the
// incidents to a queue; and sends report notifications over HTTP.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/spoolersh/pagedog"
)

// ErrForbidden is the error an [IncidentSource] returns for a request that
// fails its authentication; the server answers it with 403.
var ErrForbidden = errors.New("forbidden")

// Queue is where the incidents go.
type Queue interface {
	Send(context.Context, pagedog.Incident) error
}

// IncidentSource reads the incidents one system's notifications carry.
type IncidentSource interface {
	// Handle reads the incidents a request carries. May return
	// ErrForbidden; any other error is a bad request.
	Handle(*http.Request) ([]pagedog.Incident, error)
}

// Server serves one route per registered [IncidentSource] and sends every
// incident a source reads to the [Queue].
type Server struct {
	Trace ServerTrace
	Queue Queue

	mu     sync.RWMutex
	source map[string]IncidentSource
}

// RegisterSource mounts s at name.
func (w *Server) RegisterSource(name string, s IncidentSource) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.source == nil {
		w.source = make(map[string]IncidentSource)
	}
	if _, has := w.source[name]; has {
		return fmt.Errorf(
			"incident source already registered: %q",
			name,
		)
	}
	w.source[name] = s
	return nil
}

// Handler routes a POST to /{source} to the source registered under that
// name.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /{source}", http.HandlerFunc(s.handle))
	return mux
}

func (s *Server) handle(res http.ResponseWriter, req *http.Request) {
	name := req.PathValue("source")
	s.mu.RLock()
	src, has := s.source[name]
	s.mu.RUnlock()
	if !has {
		s.Trace.onUnknownSource(name)
		http.Error(res, "unexpected source", http.StatusNotFound)
		return
	}
	defer req.Body.Close()

	incs, err := src.Handle(req)
	if errors.Is(err, ErrForbidden) {
		http.Error(res, "", http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(res, "failed parsing body", http.StatusBadRequest)
		return
	}
	for _, inc := range incs {
		if err := s.send(req.Context(), inc); err != nil {
			// The source may retry the whole notification: the queue
			// deduplicates what was sent already.
			http.Error(res, "failed sending job", http.StatusBadGateway)
			return
		}
	}
	// 200 OK.
}

func (s *Server) send(ctx context.Context, inc pagedog.Incident) (err error) {
	done := s.Trace.onSend(inc)
	defer func() { done(err) }()
	return s.Queue.Send(ctx, inc)
}
