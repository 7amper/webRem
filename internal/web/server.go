package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"rockpi-scan/internal/config"
	"rockpi-scan/internal/control"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	cfg  *config.Manager
	ctrl *control.Controller
	mux  *http.ServeMux
}

func New(cfg *config.Manager, ctrl *control.Controller) *Server {
	s := &Server{cfg: cfg, ctrl: ctrl, mux: http.NewServeMux()}

	sub, err := fs.Sub(staticFS, "static")
	if err == nil {
		s.mux.Handle("/", http.FileServer(http.FS(sub)))
	}

	s.mux.HandleFunc("/api/config", s.handleConfig)
	s.mux.HandleFunc("/api/state", s.handleState)
	s.mux.HandleFunc("/api/events", s.handleEvents)
	s.mux.HandleFunc("/api/connect", s.handleConnect)
	s.mux.HandleFunc("/api/manual", s.handleManual)
	s.mux.HandleFunc("/api/scan", s.handleScan)
	s.mux.HandleFunc("/api/route", s.handleRoute)
	return s
}

func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// --- конфигурация -------------------------------------------------------------

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, s.cfg.Get())
	case http.MethodPost, http.MethodPut:
		var c config.Config
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			writeErr(w, 400, "неверный JSON: "+err.Error())
			return
		}
		if err := s.cfg.Set(c); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, s.cfg.Get())
	default:
		writeErr(w, 405, "метод не поддерживается")
	}
}

// --- состояние ------------------------------------------------------------------

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.ctrl.State())
}

// SSE-поток состояния (10 Гц)
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "поток не поддерживается")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	enc := json.NewEncoder(w)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// начальное состояние
	_, _ = w.Write([]byte(": connected\n\n"))
	fl.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = w.Write([]byte("data: "))
			_ = enc.Encode(s.ctrl.State())
			_, _ = w.Write([]byte("\n"))
			fl.Flush()
		}
	}
}

// --- подключение ----------------------------------------------------------------

type connectReq struct {
	On bool `json:"on"`
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "метод не поддерживается")
		return
	}
	var req connectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.ctrl.SetConnected(req.On); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"connected": s.ctrl.Connected()})
}

// --- ручное управление ----------------------------------------------------------

type manualReq struct {
	Axis    string  `json:"axis"`
	Action  string  `json:"action"`
	Value   float64 `json:"value"`
	Outputs uint16  `json:"outputs"`
	On      bool    `json:"on"`
}

func (s *Server) handleManual(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "метод не поддерживается")
		return
	}
	var req manualReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	var err error
	switch req.Action {
	case "enable":
		err = s.ctrl.Enable(req.Axis, true)
	case "disable":
		err = s.ctrl.Enable(req.Axis, false)
	case "speed":
		err = s.ctrl.Speed(req.Axis, req.Value)
	case "move-to":
		err = s.ctrl.MoveTo(req.Axis, req.Value)
	case "stop":
		err = s.ctrl.Stop(req.Axis)
	case "home":
		err = s.ctrl.Home(req.Axis, req.Value)
	case "outputs":
		err = s.ctrl.SetOutputs(req.Axis, req.Outputs)
	default:
		writeErr(w, 400, "неизвестная команда: "+req.Action)
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// --- сканирование ---------------------------------------------------------------

type scanReq struct {
	Action string `json:"action"` // start | stop | pause | resume | reset
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "метод не поддерживается")
		return
	}
	var req scanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	switch req.Action {
	case "start":
		if err := s.ctrl.StartScan(); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	case "stop":
		s.ctrl.StopScan()
		s.ctrl.SetUDP(false)
	case "pause":
		s.ctrl.PauseScan(true)
	case "resume":
		s.ctrl.PauseScan(false)
	case "reset":
		s.ctrl.ResetScan()
		s.ctrl.SetUDP(false)
	default:
		writeErr(w, 400, "неизвестное действие: "+req.Action)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.ctrl.Route())
}