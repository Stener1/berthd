// Command fakehelper is a plugin's helper as berthd's tests need one: it
// listens on a loopback port, says so in a ready file, echoes requests,
// answers an attach, and misbehaves when a file in its data directory asks
// it to.
//
//	fakehelper serve --dir DIR
//
// Files in DIR that change what it does:
//
//	fail            exit with status 3 before listening
//	ignore-term     do not stop on SIGTERM
//	attach-answer   the body to answer POST /api/attach with (default {"env":{}})
//	attach-status   the status to answer it with (default 200)
//	attach-delay    how long to wait before answering it, as a Go duration
//	detach-status   the status to answer POST /api/detach with (default 200)
//	detach-delay    how long to wait before answering it, as a Go duration
//	token           a token to put in helper.json, as a helper that asks for one does
//
// It writes helper.json ({"api": "http://127.0.0.1:PORT"}) once it listens,
// adds a line to starts each time it starts, keeps the last attach request
// as attach-request and the Authorization it came with as
// attach-authorization, and adds each detach request it answered as a line
// of detach-requests.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 4 || os.Args[1] != "serve" || os.Args[2] != "--dir" {
		fmt.Fprintf(os.Stderr, "fakehelper: unexpected arguments %q\n", os.Args[1:])
		os.Exit(2)
	}
	dir := os.Args[3]
	in := func(name string) string { return filepath.Join(dir, name) }
	read := func(name string) string {
		b, _ := os.ReadFile(in(name))
		return strings.TrimSpace(string(b))
	}
	has := func(name string) bool {
		_, err := os.Stat(in(name))
		return err == nil
	}
	// What it was started with, for the tests to read in its log.
	wd, _ := os.Getwd()
	fmt.Printf("fakehelper args=%q cwd=%s\n", os.Args[1:], wd)
	env := os.Environ()
	sort.Strings(env)
	for _, kv := range env {
		fmt.Println("env " + kv)
	}
	if f, err := os.OpenFile(in("starts"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		fmt.Fprintln(f, os.Getpid())
		f.Close()
	}
	if has("fail") {
		fmt.Fprintln(os.Stderr, "fakehelper: asked to fail")
		os.Exit(3)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakehelper:", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/echo/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"method": r.Method, "path": r.URL.EscapedPath(), "query": r.URL.RawQuery,
			"host": r.Host, "origin": r.Header.Get("Origin"), "berth_origin": r.Header.Get("X-Berth-Origin"),
			"forwarded_for": r.Header.Get("X-Forwarded-For"), "content_type": r.Header.Get("Content-Type"),
			"authorization": strings.Join(r.Header.Values("Authorization"), " | "),
			"body":          string(body),
		})
	})
	mux.HandleFunc("GET /api/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: hello\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /api/attach", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		os.WriteFile(in("attach-request"), body, 0o600)
		os.WriteFile(in("attach-authorization"), []byte(strings.Join(r.Header.Values("Authorization"), " | ")), 0o600)
		if d, err := time.ParseDuration(read("attach-delay")); err == nil {
			time.Sleep(d)
		}
		w.Header().Set("Content-Type", "application/json")
		if status, err := strconv.Atoi(read("attach-status")); err == nil {
			w.WriteHeader(status)
		}
		answer := read("attach-answer")
		if answer == "" {
			answer = `{"env":{}}`
		}
		io.WriteString(w, answer)
	})

	mux.HandleFunc("POST /api/detach", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if d, err := time.ParseDuration(read("detach-delay")); err == nil {
			time.Sleep(d)
		}
		if f, err := os.OpenFile(in("detach-requests"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			fmt.Fprintln(f, strings.TrimSpace(string(body)))
			f.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		if status, err := strconv.Atoi(read("detach-status")); err == nil {
			w.WriteHeader(status)
		}
		io.WriteString(w, `{"found":true}`)
	})

	ready := map[string]any{"api": "http://" + ln.Addr().String(), "pid": os.Getpid()}
	if token, err := os.ReadFile(in("token")); err == nil {
		ready["token"] = string(token)
	}
	state, _ := json.Marshal(ready)
	if err := os.WriteFile(in("helper.json.tmp"), state, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "fakehelper:", err)
		os.Exit(1)
	}
	os.Rename(in("helper.json.tmp"), in("helper.json"))
	fmt.Println("fakehelper listening")

	signals := make(chan os.Signal, 1)
	if has("ignore-term") {
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	}
	go func() {
		<-signals
		os.Remove(in("helper.json"))
		fmt.Println("fakehelper stopping")
		os.Exit(0)
	}()
	http.Serve(ln, mux)
}
