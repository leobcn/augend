package main // import "github.com/thraxil/augend"

import (
	_ "expvar"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/braintree/manners"
	"github.com/gorilla/sessions"
	"github.com/peterbourgon/g2s"
	config "github.com/stvp/go-toml-config"
)

var template_dir = "templates"
var statsd g2s.Statter

func makeHandler(f func(http.ResponseWriter, *http.Request, *site), s *site) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		f(w, r, s)
		t1 := time.Now()
		statsd.Counter(1.0, "augend.response.200", 1)
		statsd.Timing(1.0, "augend.view.GET", t1.Sub(t0))
	}
}

func LoggingHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		format := "%s - - [%s] \"%s %s %s\" %s\n"
		fmt.Printf(format, r.RemoteAddr, time.Now().Format(time.RFC1123),
			r.Method, r.URL.Path, r.Proto, r.UserAgent())
		h.ServeHTTP(w, r)
	})
}

func main() {
	log.Println("starting augend...")
	var store sessions.Store
	var configFile string
	var importjson string
	//	var dumpjson string
	flag.StringVar(&configFile, "config", "./dev.conf", "TOML config file")
	flag.StringVar(&importjson, "importjson", "", "json file to import")
	//	flag.StringVar(&dumpjson, "dumpjson", "", "dump data as json")
	flag.Parse()
	var (
		port       = config.String("port", "9999")
		media_dir  = config.String("media_dir", "media")
		secret_key = config.String("secret_key", "change-me")
		t_dir      = config.String("template_dir", "templates")
	)
	config.Parse(configFile)
	template_dir = *t_dir

	store = sessions.NewCookieStore([]byte(*secret_key))

	var DB_URL string
	if os.Getenv("AUGEND_DB_URL") != "" {
		DB_URL = os.Getenv("AUGEND_DB_URL")
	}
	log.Println(DB_URL)

	p := newPersistence(DB_URL)
	defer p.Close()

	log.Println("connected to db")

	s := newSite(p, store)

	if importjson != "" {
		fmt.Println("importing JSON file")
		importJsonFile(importjson, s)
		return
	}
	//	if dumpjson != "" {
	//		fmt.Println("dumping database as json")
	//		dumpJSON(dumpjson)
	//		return
	//	}
	statsd, _ = g2s.Dial("udp", "127.0.0.1:8125")

	address := ":" + *port
	log.Println("listening on", address)
	mux := http.NewServeMux()
	mux.HandleFunc("/favicon.ico", faviconHandler)
	mux.HandleFunc("/", makeHandler(indexHandler, s))
	mux.HandleFunc("/fact/", makeHandler(factHandler, s))
	mux.HandleFunc("/tag/", makeHandler(tagHandler, s))
	mux.HandleFunc("/add/", makeHandler(addHandler, s))
	mux.HandleFunc("/register/", makeHandler(registerHandler, s))
	mux.HandleFunc("/login/", makeHandler(loginHandler, s))
	mux.HandleFunc("/logout/", makeHandler(logoutHandler, s))
	mux.HandleFunc("/smoketest/", makeHandler(smoketestHandler, s))
	mux.Handle("/media/", http.StripPrefix("/media/",
		http.FileServer(http.Dir(*media_dir))))
	httpServer := manners.NewServer()
	httpServer.Addr = address
	httpServer.Handler = LoggingHandler(mux)

	errChan := make(chan error, 10)

	go func() {
		errChan <- httpServer.ListenAndServe()
	}()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case err := <-errChan:
			if err != nil {
				log.Fatal(err)
			}
		case s := <-signalChan:
			log.Println(fmt.Sprintf("Captured %v. Exiting...", s))
			httpServer.BlockingClose()
			os.Exit(0)
		}
	}
}
