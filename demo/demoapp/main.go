// Demoapp is the service pagedog watches over in the demo: a page with two
// buttons, one answered with 200 and one with 500. It counts its requests
// for Prometheus, logs them for Loki, and shows the alerts Alertmanager and
// the reports pagedog post to it.
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	//go:embed index.html.tmpl
	indexHTML string

	page = template.Must(template.New("index").Parse(indexHTML))
)

func main() {
	addr := flag.String("listen", "127.0.0.1:8081", "address to listen on")
	flag.Parse()

	http.HandleFunc("GET /{$}", index)
	http.HandleFunc("POST /ok", ok)
	http.HandleFunc("POST /fail", fail)
	http.HandleFunc("POST /alert", receiveAlert)
	http.HandleFunc("POST /report", receiveReport)
	http.HandleFunc("GET /metrics", metrics)

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	slog.Info("listening", "addr", *addr)
	err := http.ListenAndServe(*addr, middleware(http.DefaultServeMux))
	slog.Error("stopped", "error", err)
	os.Exit(1)
}

var (
	mu sync.Mutex

	// requests counts by status code. The two the buttons answer are there
	// from the start: a counter that first appears at 1 has no rate.
	requests = map[int]int{
		http.StatusOK:                  0,
		http.StatusInternalServerError: 0,
	}
	alerts  []alert  // newest first, one per alert
	reports []report // newest first, one per incident
)

// middleware counts and logs every request by its status.
func middleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		h.ServeHTTP(rec, r)
		mu.Lock()
		requests[rec.status]++
		mu.Unlock()
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
		)
	})
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func index(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	data := struct {
		Metrics string
		Alerts  []alert
		Reports []report
	}{
		Metrics: exposition(),
		Alerts:  slices.Clone(alerts),
		Reports: slices.Clone(reports),
	}
	mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.Execute(w, data)
}

func ok(w http.ResponseWriter, r *http.Request) {
	slog.Info("received text", "text", r.FormValue("text"))
	fmt.Fprint(w, "ok")
}

func fail(w http.ResponseWriter, r *http.Request) {
	slog.Info("received text", "text", r.FormValue("text"))
	http.Error(w, "fail", http.StatusInternalServerError)
}

// receiveAlert takes a notification as Alertmanager's webhook receiver
// posts it: the latest state of each alert in it is kept.
func receiveAlert(w http.ResponseWriter, r *http.Request) {
	var n struct {
		Alerts []alert `json:"alerts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		http.Error(w, "bad notification", http.StatusBadRequest)
		return
	}
	mu.Lock()
	for _, a := range n.Alerts {
		a.ReceivedAt = time.Now()
		alerts = slices.DeleteFunc(alerts, func(old alert) bool {
			return old.Fingerprint == a.Fingerprint
		})
		alerts = append([]alert{a}, alerts...)
	}
	mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func receiveReport(w http.ResponseWriter, r *http.Request) {
	var rep report
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil || rep.Incident.Key == "" {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	rep.ReceivedAt = time.Now()
	mu.Lock()
	reports = slices.DeleteFunc(reports, func(old report) bool {
		return old.Incident.Key == rep.Incident.Key
	})
	reports = append([]report{rep}, reports...)
	mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func metrics(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprint(w, exposition())
}

// exposition is the metrics as Prometheus scrapes them; mu must be held.
func exposition() string {
	var sb strings.Builder
	fmt.Fprintln(&sb, "# HELP http_requests_total Requests served, by status code.")
	fmt.Fprintln(&sb, "# TYPE http_requests_total counter")
	for _, code := range slices.Sorted(maps.Keys(requests)) {
		fmt.Fprintf(&sb, "http_requests_total{code=%q} %d\n", fmt.Sprint(code), requests[code])
	}
	return sb.String()
}

// alert is one alert as Alertmanager's webhook receiver posts it.
type alert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	Fingerprint string            `json:"fingerprint"`
	ReceivedAt  time.Time         `json:"-"`
}

// report is what pagedog's webhook notifier posts.
type report struct {
	Incident struct {
		Key      string `json:"key"`
		Severity string `json:"severity"`
		Title    string `json:"title"`
	} `json:"incident"`
	Report struct {
		URL  string `json:"url"`
		Text string `json:"text"`
	} `json:"report"`
	ReceivedAt time.Time `json:"-"`
}
