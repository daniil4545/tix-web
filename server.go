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
	"strconv"
	"strings"
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
	revHash    = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
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
	mux.HandleFunc("PATCH /api/tasks/{id}", s.set)
	mux.HandleFunc("PUT /api/tasks/{id}", s.edit)
	mux.HandleFunc("POST /api/tasks/{id}/check", s.check("check"))
	mux.HandleFunc("POST /api/tasks/{id}/uncheck", s.check("uncheck"))
	mux.HandleFunc("POST /api/tasks/{id}/comment", s.comment)
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
	var req moveReq
	id, ok := decode(w, r, &req)
	if !ok {
		return
	}
	switch {
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
	s.write(w, r, "", args...)
}

type setReq struct {
	Title *string `json:"title"`
	Tags  *string `json:"tags"`
}

// set runs `tix set` for the fields present; tags are one comma-separated string, as tix takes them.
func (s *server) set(w http.ResponseWriter, r *http.Request) {
	var req setReq
	id, ok := decode(w, r, &req)
	if !ok {
		return
	}
	args := []string{"set", id}
	if req.Title != nil {
		args = append(args, "title="+*req.Title)
	}
	if req.Tags != nil {
		args = append(args, "tags="+*req.Tags)
	}
	if len(args) == 2 {
		writeErr(w, badRequest("err "+id+": nothing to set, want title or tags"))
		return
	}
	s.write(w, r, "", append(args, "--as", "human")...)
}

type editReq struct {
	Rev  string `json:"rev"`
	Body string `json:"body"`
}

// edit replaces the body with `tix edit --base rev`; a body changed since rev is a tix conflict (409).
func (s *server) edit(w http.ResponseWriter, r *http.Request) {
	var req editReq
	id, ok := decode(w, r, &req)
	if !ok {
		return
	}
	if !revHash.MatchString(req.Rev) {
		writeErr(w, badRequest(fmt.Sprintf("err %s: bad rev %q", id, req.Rev)))
		return
	}
	if req.Body == "" {
		writeErr(w, badRequest("err "+id+": empty body"))
		return
	}
	s.write(w, r, req.Body, "edit", id, "--base", req.Rev, "--as", "human")
}

type checkReq struct {
	N int `json:"n"`
}

// check returns the handler of `tix check` or `tix uncheck` for one criterion number.
func (s *server) check(cmd string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req checkReq
		id, ok := decode(w, r, &req)
		if !ok {
			return
		}
		if req.N < 1 {
			writeErr(w, badRequest(fmt.Sprintf("err %s: criterion %d, want 1 or more", id, req.N)))
			return
		}
		s.write(w, r, "", cmd, id, strconv.Itoa(req.N), "--as", "human")
	}
}

type commentReq struct {
	Text string `json:"text"`
}

// comment adds a human comment, the instruction an agent reads; "--" keeps a text with a dash a text.
func (s *server) comment(w http.ResponseWriter, r *http.Request) {
	var req commentReq
	id, ok := decode(w, r, &req)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeErr(w, badRequest("err "+id+": empty comment"))
		return
	}
	s.write(w, r, "", "comment", id, "--as", "human", "--", req.Text)
}

// decode reads the JSON body into v and returns the task id of the path; a bad request is answered
// with 400 and false.
func decode(w http.ResponseWriter, r *http.Request, v any) (string, bool) {
	id := r.PathValue("id")
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, badRequest("err: request body: "+err.Error()))
		return "", false
	}
	if !taskID.MatchString(id) {
		writeErr(w, badRequest("err "+id+": not a task id, want T-N"))
		return "", false
	}
	return id, true
}

// write runs one tix change and answers 204, or passes the tix refusal on.
func (s *server) write(w http.ResponseWriter, r *http.Request, stdin string, args ...string) {
	ctx, cancel := context.WithTimeout(r.Context(), tixTimeout)
	defer cancel()
	if _, err := s.tix.runInput(ctx, stdin, args...); err != nil {
		writeErr(w, err)
		return
	}
	slog.Info("task changed", "cmd", args[0], "id", args[1])
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
