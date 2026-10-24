// Command devserver serves the frontend in a normal browser with the Go API
// exposed over a localhost-only JSON endpoint. It is for UI development and
// automated screenshots; the shipped app uses Wails bindings instead.
//
//	go run ./cmd/devserver -open "samples/Team crest.dst"
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/app"
)

type headless struct{}

func (headless) OpenFiles(string, bool, bool, string) ([]string, error) {
	return nil, errors.New("file dialogs are not available in the dev server; drop a path instead")
}
func (headless) SaveFile(string, string, string, string) (string, error) {
	return "", errors.New("save dialogs are not available in the dev server")
}
func (headless) Confirm(string, string) bool               { return true }
func (headless) Emit(string, any)                          {}
func (headless) SetTitle(string)                           {}
func (headless) OpenURL(u string)                          { log.Println("open url:", u) }
func (headless) Quit()                                     {}
func (headless) PickFolder(string, string) (string, error) { return "", nil }
func (headless) Focus()                                    {}

func main() {
	addr := flag.String("addr", "127.0.0.1:34115", "listen address (keep it on localhost)")
	dir := flag.String("dir", "frontend/dist", "frontend directory")
	var opens multi
	flag.Var(&opens, "open", "file to open/merge at startup (repeatable)")
	flag.Parse()
	if !strings.HasPrefix(*addr, "127.0.0.1:") && !strings.HasPrefix(*addr, "localhost:") {
		log.Fatal("dev server must listen on localhost")
	}

	a := app.New(headless{})
	for i, p := range opens {
		if _, err := a.OpenPath(p, i == 0); err != nil {
			log.Fatalf("open %s: %v", p, err)
		}
	}

	av := reflect.ValueOf(a)
	http.Handle("/", http.FileServer(http.Dir(*dir)))
	http.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/")
		m := av.MethodByName(name)
		if !m.IsValid() {
			http.Error(w, "unknown method", http.StatusNotFound)
			return
		}
		var raw []json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&raw); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mt := m.Type()
		if len(raw) != mt.NumIn() {
			http.Error(w, fmt.Sprintf("%s takes %d args", name, mt.NumIn()), http.StatusBadRequest)
			return
		}
		args := make([]reflect.Value, mt.NumIn())
		for i := range args {
			p := reflect.New(mt.In(i))
			if err := json.Unmarshal(raw[i], p.Interface()); err != nil {
				http.Error(w, fmt.Sprintf("arg %d: %v", i, err), http.StatusBadRequest)
				return
			}
			args[i] = p.Elem()
		}
		out := m.Call(args)
		var result any
		for _, v := range out {
			if v.Type() == reflect.TypeOf((*error)(nil)).Elem() {
				if !v.IsNil() {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnprocessableEntity)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": v.Interface().(error).Error()})
					return
				}
				continue
			}
			result = v.Interface()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	})
	log.Printf("dev server on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }
