package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// tixCLI runs the tix binary. It never builds a shell line: arguments go to exec as an array.
type tixCLI struct {
	bin string
}

// tixError is a non-zero exit of tix with its stderr text, passed to the browser as is.
type tixError struct {
	Code int
	Msg  string
}

func (e *tixError) Error() string {
	return fmt.Sprintf("tix exit %d: %s", e.Code, e.Msg)
}

// httpStatus maps tix exit codes to HTTP: 5 (rule) and 3 (conflict) are 409, 4 is 404, 6 is 400.
func (e *tixError) httpStatus() int {
	switch e.Code {
	case 3, 5:
		return http.StatusConflict
	case 4:
		return http.StatusNotFound
	case 6:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func (t tixCLI) run(ctx context.Context, args ...string) ([]byte, error) {
	return t.runInput(ctx, "", args...)
}

// tixTimeout bounds one tix call; a read of 750 tasks takes tens of milliseconds.
const tixTimeout = 10 * time.Second

// runInput runs tix with stdin; an empty stdin is none.
func (t tixCLI) runInput(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, tixTimeout)
	defer cancel()
	//nolint:gosec // bin is the operator's -tix flag; args go to exec as an array, no shell
	cmd := exec.CommandContext(ctx, t.bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// A child of tix (git) may hold the pipes after tix is killed on timeout.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" { // killed on timeout or a closed request: tix said nothing
			msg = fmt.Sprintf("tix %s: %v", strings.Join(args, " "), errors.Join(err, ctx.Err()))
		}
		return nil, &tixError{Code: exit.ExitCode(), Msg: msg}
	}
	return nil, fmt.Errorf("run %s: %w", t.bin, err)
}

type statusRow struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type projectRow struct {
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Catalog bool   `json:"catalog"` // false: only tasks mention it, tix new refuses it
}

// parseStatuses reads `tix status`: name, kind and count per line. tix 0.9.1 has no --json there.
func parseStatuses(out []byte) ([]statusRow, error) {
	rows := []statusRow{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 3 {
			return nil, fmt.Errorf("tix status: unexpected line %q", sc.Text())
		}
		n, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("tix status: count in %q: %w", sc.Text(), err)
		}
		rows = append(rows, statusRow{Name: f[0], Kind: f[1], Count: n})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("tix status: %w", err)
	}
	return rows, nil
}

// parseProjects reads `tix projects`: name, path or "-", count, an optional note.
// The path stays on the server; the count is the last number on the line.
func parseProjects(out []byte) ([]projectRow, error) {
	rows := []projectRow{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		row := projectRow{Name: f[0], Count: -1, Catalog: !strings.HasSuffix(strings.TrimSpace(sc.Text()), "not in catalog")}
		for _, s := range f[1:] {
			if n, err := strconv.Atoi(s); err == nil {
				row.Count = n
			}
		}
		if row.Count < 0 {
			return nil, fmt.Errorf("tix projects: no count in %q", sc.Text())
		}
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("tix projects: %w", err)
	}
	return rows, nil
}
