// Command uipreview serves the installer UI over HTTP with the real back end,
// so the HTML/JS can be exercised in any browser (and in automated tests) on a
// machine without WebView2. It is a development tool, not part of the installer.
//
//	go run ./cmd/uipreview -addr 127.0.0.1:8765 [-steam <steam root>] [-gog <folder with one sub-folder per GOG game>] [-dev]
//
// Native dialogs are replaced by a queue. With -dev, POST /dev/pick?path=...
// (header X-Dev-Token: <token printed at start>) answers the next Browse click;
// an empty path cancels it. Without -dev the endpoint does not exist, and the
// server only ever binds to a loopback address.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"fearinstaller/internal/app"
	"fearinstaller/internal/install"
	"fearinstaller/internal/pcgame"
	"fearinstaller/ui"
)

type queueDialogs struct {
	mu   sync.Mutex
	cond *sync.Cond
	q    []string
}

func newDialogs() *queueDialogs {
	d := &queueDialogs{}
	d.cond = sync.NewCond(&d.mu)
	return d
}

// next waits (up to 60 s) for an answer, like a real dialog waits for the user.
func (d *queueDialogs) next() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	deadline := time.Now().Add(60 * time.Second)
	timer := time.AfterFunc(60*time.Second, func() { d.mu.Lock(); d.cond.Broadcast(); d.mu.Unlock() })
	defer timer.Stop()
	for len(d.q) == 0 && time.Now().Before(deadline) {
		d.cond.Wait()
	}
	if len(d.q) == 0 {
		return ""
	}
	p := d.q[0]
	d.q = d.q[1:]
	return p
}
func (d *queueDialogs) push(p string) {
	d.mu.Lock()
	d.q = append(d.q, p)
	d.cond.Broadcast()
	d.mu.Unlock()
}
func (d *queueDialogs) PickFolder(_, _ string) string { return d.next() }

// shim defines the window.be_* functions the UI expects, forwarding to /rpc.
const shim = `<script>
(function(){
  async function rpc(name, args){
    const r = await fetch('/rpc/'+name, {method:'POST', body: JSON.stringify(args)});
    return r.json();
  }
  for (const n of ['init','setPC','recheck','browse','clear','plan','snapshot','install','cancel','uninstall','quit','echopage']) window['be_'+n] = (...a) => rpc(n, a);
  // The desktop host pushes state; here the page long-polls for it.
  (async function poll(seq){
    for(;;){
      try{ const r = await (await fetch('/state?seq='+seq)).json(); seq = r.seq; if(window.__fearState && r.state) window.__fearState(r.state); }
      catch(e){ await new Promise(r=>setTimeout(r,500)); }
    }
  })(-1);
})();
</script>`

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "listen address (loopback only)")
	steam := flag.String("steam", "", "Steam root folder (sets FEAR_STEAM_ROOT)")
	gog := flag.String("gog", "", "folder holding one sub-folder per GOG game (sets FEAR_GOG_ROOT)")
	dev := flag.Bool("dev", false, "enable /dev/pick (scripted file dialogs); prints a random token")
	slow := flag.Duration("slow-install", 0, "(testing) pause this long after every 1 MiB the install job reads, so a short job can be watched and cancelled")
	flag.Parse()
	if *steam != "" {
		os.Setenv("FEAR_STEAM_ROOT", *steam)
	}
	if *gog != "" {
		os.Setenv("FEAR_GOG_ROOT", *gog)
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatal(err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		log.Fatalf("refusing to listen on %q: only loopback addresses (127.0.0.1, ::1) are allowed", host)
	}
	token := randomToken()

	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	seq := 0
	var last app.State
	b := app.New(func(s app.State) { mu.Lock(); seq++; last = s; cond.Broadcast(); mu.Unlock() })
	// Only the folders given on the command line, never the registry: a preview run must not find (and read) the
	// real game installations of the machine it runs on.
	b.SetSources(func() pcgame.Sources {
		var src pcgame.Sources
		if *steam != "" {
			src.SteamRoots = []string{*steam}
		}
		if *gog != "" {
			src.GOGRoots = []string{filepath.Clean(*gog)}
		}
		return src
	})
	dlg := newDialogs()
	b.SetInstaller(&app.DryRunInstaller{ChunkDelay: *slow})
	// Like the install job, the preview's Uninstall changes nothing.
	b.SetUninstaller(func(string) (*install.UninstallResult, error) {
		return &install.UninstallResult{Notes: []string{"Preview: nothing was removed."}}, nil
	})
	br := &app.Bridge{B: b, D: dlg, Post: func(f func()) { go f() },
		OnQuit:  func() { log.Print("QUIT requested by the page") },
		OpenURL: func(url string) { log.Print("OPEN requested by the page (not opened in the preview): ", url) }}
	// Ctrl+C / kill: stop the job and the checks cleanly before exiting (same as closing the real window).
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; b.Shutdown(); os.Exit(0) }()

	// guard rejects requests whose Host or Origin is not this server (DNS
	// rebinding / cross-site requests from other pages in the same browser).
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Host != *addr {
				http.Error(w, "bad host", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != "http://"+*addr {
				http.Error(w, "bad origin", http.StatusForbidden)
				return
			}
			next(w, r)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		html := strings.Replace(string(ui.IndexHTML), "<script>\n\"use strict\";", shim+"\n<script>\n\"use strict\";", 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, html)
	}))
	mux.HandleFunc("/rpc/", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var a []json.RawMessage
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&a)
		str := func(i int) (s string) {
			if i < len(a) {
				json.Unmarshal(a[i], &s)
			}
			return
		}
		var out any
		switch strings.TrimPrefix(r.URL.Path, "/rpc/") {
		case "init":
			out = br.Init()
		case "setPC":
			out = br.SetPC(str(0))
		case "recheck":
			out = br.Recheck()
		case "browse":
			out = br.Browse(str(0), str(1))
		case "clear":
			out = br.Clear(str(0))
		case "snapshot":
			out = br.Snapshot()
		case "install":
			var o app.Options
			if len(a) > 0 {
				json.Unmarshal(a[0], &o)
			}
			out = br.Install(o)
		case "cancel":
			out = br.Cancel()
		case "uninstall":
			out = br.Uninstall()
		case "quit":
			br.Quit()
			out = nil
		case "echopage":
			br.OpenEchoPatchPage()
			out = nil
		case "plan":
			var o app.Options
			if len(a) > 0 {
				json.Unmarshal(a[0], &o)
			}
			out = br.Plan(o)
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	mux.HandleFunc("/state", guard(func(w http.ResponseWriter, r *http.Request) {
		var since int
		fmt.Sscan(r.URL.Query().Get("seq"), &since)
		mu.Lock()
		for seq == since || seq == 0 { // nothing is pushed before the first state
			cond.Wait()
		}
		resp := map[string]any{"seq": seq, "state": last}
		mu.Unlock()
		json.NewEncoder(w).Encode(resp)
	}))
	if *dev {
		mux.HandleFunc("/dev/pick", guard(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Dev-Token")), []byte(token)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			dlg.push(r.URL.Query().Get("path"))
		}))
		fmt.Printf("DEV_TOKEN=%s\n", token)
	}
	log.Printf("UI preview on http://%s/ (dev endpoints: %v)", *addr, *dev)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
