package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var (
	flagBind = flag.String("bind", ":8000", "TCP host:port to listen on")
	flagDB   = flag.String("db", "mysql", "database driver")
	flagDSN  = flag.String("dsn", "guildroster@/guildroster", "DSN for database driver")
)

var (
	gContext context.Context
	gCancel  context.CancelFunc

	gHardContext context.Context
	gHardCancel  context.CancelFunc

	gExiting  chan struct{}
	gWaitDone chan struct{}
	gWait     sync.WaitGroup

	gDatabase *sql.DB
	gServer   *http.Server

	gHealthyDatabase uintptr = 0
)

func init() {
	gHardContext, gHardCancel = context.WithCancel(context.Background())
	gContext, gCancel = context.WithCancel(gHardContext)
	gExiting = make(chan struct{})
	gWaitDone = make(chan struct{})
	go func() {
		gWait.Wait()
		close(gWaitDone)
	}()
}

func main() {
	flag.Parse()

	gWait.Add(1)
	go signalThread()

	var err error
	gDatabase, err = sql.Open(*flagDB, *flagDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer gDatabase.Close()

	err = gDatabase.PingContext(gHardContext)
	if err != nil {
		log.Fatal(err)
	}
	atomic.StoreUintptr(&gHealthyDatabase, 1)
	gWait.Add(1)
	go pingThread()

	gServer = &http.Server{
		Addr:           *flagBind,
		Handler:        http.HandlerFunc(mux),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	err = gServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}

	gCancel()
	close(gExiting)
	log.Print("exit")

	t := time.NewTimer(6 * time.Second)
	select {
	case <-gWaitDone:
		return

	case <-t.C:
		log.Print("force exit")
		gHardCancel()
		t.Reset(3 * time.Second)
		select {
		case <-gWaitDone:
			return

		case <-t.C:
			return
		}
	}
}

func shutdown() {
	gCancel()
	gServer.Shutdown(gHardContext)
}

type request struct {
	APIVersion uint
	GuildID string
	PlayerID string
	ToonID string
	Endpoint string
}

func mux(w http.ResponseWriter, r *http.Request) {
	requestPath := path.Clean(r.URL.Path)
	switch {
	case strings.HasPrefix(requestPath, "/v1/"):
		muxApi(w, r, 1, requestPath[3:])
	default:
		http.NotFound(w, r)
	}
}

func muxApi(w http.ResponseWriter, r *http.Request, apiVersion uint, requestPath string) {
	switch {
	case requestPath == "/guilds":
		handleGuilds(w, r, apiVersion)
	case requestPath == "/players":
		handleApiPlayers(w, r, apiVersion)
	case requestPath == "/toons":
		handleApiToons(w, r, apiVersion)
	case strings.HasPrefix(requestPath, "/guilds/"):
		guildID := requestPath[8:]
		index := strings.IndexByte(guildID, '/')
		if index < 0 {
			handleGuild(w, r, apiVersion, guildID)
		} else {
			rest := guildID[index:]
			guildID = guildID[:index]
			muxApiGuild(w, r, apiVersion, guildID, rest)
		}
	default:
		http.NotFound(w, r)
	}
}

func muxApiGuild(w http.ResponseWriter, r *http.Request, apiVersion uint, guildID string, requestPath string) {
	http.NotFound(w, r)
}

func handleGuild(w http.ResponseWriter, r *http.Request, apiVersion uint, guildID string) {
	http.NotFound(w, r)
}

func handleGuilds(w http.ResponseWriter, r *http.Request, apiVersion uint) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func handleApiPlayers(w http.ResponseWriter, r *http.Request, apiVersion uint) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func handleApiToons(w http.ResponseWriter, r *http.Request, apiVersion uint) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func signalThread() {
	defer gWait.Done()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer func() {
		signal.Stop(ch)
		close(ch)
	}()

Loop:
	for {
		select {
		case <-gExiting:
			return

		case <-gContext.Done():
			break Loop

		case sig := <-ch:
			log.Printf("received signal: %v", sig)
			shutdown()
			break Loop
		}
	}

	t := time.NewTimer(5 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-gExiting:
			return

		case <-gHardContext.Done():
			return

		case sig := <-ch:
			log.Printf("received signal: %v", sig)
			gHardCancel()
		case <-t.C:
			log.Printf("timed out")
			gHardCancel()
		}
	}
}

func pingThread() {
	defer gWait.Done()

	t := time.NewTicker(15 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-gExiting:
			return

		case <-gHardContext.Done():
			return

		case <-t.C:
			checkDatabase()
		}
	}
}

func checkDatabase() {
	log.Println("ping database")
	var value uintptr = 1
	if err := gDatabase.PingContext(gHardContext); err != nil {
		value = 0
	}
	atomic.StoreUintptr(&gHealthyDatabase, value)
}
