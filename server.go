package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed web
var webFS embed.FS

// tixTimeout bounds one tix call; a read of 750 tasks takes tens of milliseconds.
const tixTimeout = 10 * time.Second

var (
	taskID = regexp.MustCompile(`^T-[0-9]+$`)
	// statusName keeps a status from being read as a flag; tix checks the rest.
	statusName = regexp.MustCompile(`^[^-\s]\S*$`)
)

// maxBody bounds a write request; the largest is a task body.
const maxBody = 1 << 20

// md renders task text written by agents. Without html.WithUnsafe goldmark drops raw HTML and
// dangerous link schemes, so a <script> in a task never reaches the page.
var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

type server struct {
	tix   tixCLI
	hosts map[string]bool
}

func newServer(tix tixCLI, port string) http.Handler {
	s := &server{tix: tix, hosts: map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true}}
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err) // the embedded tree is fixed at build time
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("GET /api/board", s.board)
	mux.HandleFunc("GET /api/tasks/{id}", s.task)
	mux.HandleFunc("POST /api/tasks/{id}/move", s.move)
	// Writes from another origin are refused by Sec-Fetch-Site or Origin against Host.
	return s.checkHost(http.NewCrossOriginProtection().Handler(mux))
}

// checkHost refuses a Host other than the bound loopback address: a DNS rebinding page
// would reach the server under its own name.
func (s *server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) board(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), tixTimeout)
	defer cancel()
	out, err := s.tix.run(ctx, "status")
	if err != nil {
		writeErr(w, err)
		return
	}
	statuses, err := parseStatuses(out)
	if err != nil {
		writeErr(w, err)
		return
	}
	if out, err = s.tix.run(ctx, "projects"); err != nil {
		writeErr(w, err)
		return
	}
	projects, err := parseProjects(out)
	if err != nil {
		writeErr(w, err)
		return
	}
	if out, err = s.tix.run(ctx, "list", "--all", "--json", "--project", ""); err != nil {
		writeErr(w, err)
		return
	}
	var tasks []map[string]json.RawMessage
	if err := json.Unmarshal(out, &tasks); err != nil {
		writeErr(w, fmt.Errorf("tix list: %w", err))
		return
	}
	for _, t := range tasks {
		delete(t, "path") // the file path stays on the server
	}
	writeJSON(w, http.StatusOK, map[string]any{"statuses": statuses, "projects": projects, "tasks": tasks})
}

// task passes `tix show -v --json` through and adds HTML of the body, notes and comments.
func (s *server) task(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !taskID.MatchString(id) {
		writeErr(w, badRequest("err "+id+": not a task id, want T-N"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), tixTimeout)
	defer cancel()
	out, err := s.tix.run(ctx, "show", id, "-v", "--json")
	if err != nil {
		writeErr(w, err)
		return
	}
	var t map[string]any
	if err := json.Unmarshal(out, &t); err != nil {
		writeErr(w, fmt.Errorf("tix show %s: %w", id, err))
		return
	}
	delete(t, "path")
	if t["body_html"], err = render(t["body"]); err != nil {
		writeErr(w, err)
		return
	}
	for _, key := range []string{"notes", "comments"} {
		list, _ := t[key].([]any)
		for _, item := range list {
			e, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if e["html"], err = render(e["text"]); err != nil {
				writeErr(w, err)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, t)
}

type moveReq struct {
	Status  string `json:"status"`
	After   string `json:"after"`
	First   bool   `json:"first"`
	Message string `json:"message"`
}

// move runs `tix move`: after a card, first in the column, or at its end when neither is set.
func (s *server) move(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req moveReq
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, badRequest("err: request body: "+err.Error()))
		return
	}
	switch {
	case !taskID.MatchString(id):
		writeErr(w, badRequest("err "+id+": not a task id, want T-N"))
		return
	case !statusName.MatchString(req.Status):
		writeErr(w, badRequest(fmt.Sprintf("err %s: bad status %q", id, req.Status)))
		return
	case req.After != "" && (!taskID.MatchString(req.After) || req.First):
		writeErr(w, badRequest(fmt.Sprintf("err %s: after %q: want T-N and no first", id, req.After)))
		return
	}
	args := []string{"move", id, req.Status, "--as", "human"}
	switch {
	case req.After != "":
		args = append(args, "--after", req.After)
	case req.First:
		args = append(args, "--first")
	}
	if req.Message != "" {
		args = append(args, "-m", req.Message)
	}
	ctx, cancel := context.WithTimeout(r.Context(), tixTimeout)
	defer cancel()
	if _, err := s.tix.run(ctx, args...); err != nil {
		writeErr(w, err)
		return
	}
	slog.Info("task moved", "id", id, "status", req.Status)
	w.WriteHeader(http.StatusNoContent)
}

// badRequest is a refusal of the server itself, shaped like a tix usage error (exit 6, HTTP 400).
func badRequest(msg string) *tixError {
	return &tixError{Code: 6, Msg: msg}
}

func render(v any) (string, error) {
	text, _ := v.(string)
	var b bytes.Buffer
	if err := md.Convert([]byte(text), &b); err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}
	return b.String(), nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", "err", err)
	}
}

// writeErr sends a tix refusal with its exit code and text unchanged; anything else is a 500.
func writeErr(w http.ResponseWriter, err error) {
	te, ok := errors.AsType[*tixError](err)
	if !ok {
		te = &tixError{Code: -1, Msg: err.Error()}
	}
	slog.Warn("request failed", "code", te.Code, "err", te.Msg)
	writeJSON(w, te.httpStatus(), map[string]any{"error": te.Msg, "code": te.Code})
}
