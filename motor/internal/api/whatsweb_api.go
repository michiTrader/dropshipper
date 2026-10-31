package api

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Routes of the WhatsApp Web channel (phone linked with a QR code).
func (s *Server) whatsWebRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/whatsapp/web/estado", s.waWebStatus)
	api.HandleFunc("POST /api/whatsapp/web/vincular", s.waWebLink)
	api.HandleFunc("POST /api/whatsapp/web/codigo", s.waWebCode)
	api.HandleFunc("POST /api/whatsapp/web/desvincular", s.waWebUnlink)
}

// waWebStatus returns the connection state and, while linking, the QR code.
func (s *Server) waWebStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Hub.Web.Info())
}

// waWebLink starts linking: the QR code then shows up in the status.
func (s *Server) waWebLink(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.Hub.Web.StartPairing(ctx); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, s.Hub.Web.Info())
}

// waWebCode links with an 8-character code instead of a QR (phone number needed).
func (s *Server) waWebCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone string `json:"telefono"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if in.Phone == "" {
		writeErr(w, errors.New("escribe el número de WhatsApp que vas a vincular, con el código del país (ej.: 573001234567)"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	code, err := s.Hub.Web.PairCode(ctx, in.Phone)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "codigo": code})
}

// waWebUnlink removes the linked device and forgets the session.
func (s *Server) waWebUnlink(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.Hub.Web.Unlink(ctx); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
