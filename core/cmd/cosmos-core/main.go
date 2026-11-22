// cosmos-core is the companion's mind as a single local process. The Electron app
// starts it and talks to it over HTTP; it also runs on its own.
package main

import (
	"context"
	"cosmos/core/internal/mcp"
	"cosmos/core/internal/voice"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"cosmos/core/internal/api"
	"cosmos/core/internal/brain"
	"cosmos/core/internal/config"
	"cosmos/core/internal/importer"
	"cosmos/core/internal/launcher"
	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

func defaultDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "cosmos-data"
	}
	return filepath.Join(dir, "Cosmos")
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7717", "address to listen on")
	data := flag.String("data", defaultDataDir(), "folder for the database and config")
	importNova := flag.String("import-nova", "", "import memories, journal and people from this Nova data folder, then exit")
	flag.Parse()
	logger := log.New(os.Stderr, "", log.LstdFlags)

	if err := os.MkdirAll(*data, 0o700); err != nil {
		logger.Fatalf("data folder: %v", err)
	}
	// The log is kept next to her data, so what went wrong yesterday can
	// still be read today. It holds what the models were asked to remember.
	if f, err := os.OpenFile(filepath.Join(*data, "cosmos.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		defer f.Close()
		logger = log.New(io.MultiWriter(os.Stderr, f), "", log.LstdFlags)
	}
	s, err := store.Open(filepath.Join(*data, "cosmos.db"))
	if err != nil {
		logger.Fatalf("database: %v", err)
	}
	defer s.Close()

	if *importNova != "" {
		c, err := importer.Nova(s, *importNova)
		if err != nil {
			logger.Fatalf("import: %v", err)
		}
		fmt.Printf("imported %d memories, %d journal entries, %d people (%d already present or empty)\n", c.Memories, c.Journal, c.People, c.Skipped)
		return
	}

	cfg, err := config.Load(filepath.Join(*data, "config.json"))
	if err != nil {
		logger.Fatalf("config: %v", err)
	}
	launch := launcher.New(cfg, logger)
	defer launch.StopAll()
	client := llm.NewHTTP(cfg)
	client.Prepare = launch.Acquire

	b := brain.New(s, client, cfg)
	b.Log = logger
	b.Rest = launch.Rest
	b.E = client
	b.PodDir = filepath.Join(*data, "podcasts")
	b.BookDir = filepath.Join(*data, "books")
	b.MCP = mcp.NewManager()
	// Her connections start beside everything else: a slow or broken
	// server must not hold up waking.
	go b.MCP.Sync(cfg.Get().MCPServers)

	// The token is kept in the data folder, readable only by this user.
	// The app reads it from there, and so can any local tool that should
	// be able to talk to her. It survives restarts, so a window that was
	// open before the core restarted carries on working.
	token := os.Getenv("COSMOS_TOKEN")
	if token == "" {
		tokenFile := filepath.Join(*data, "token")
		if raw, err := os.ReadFile(tokenFile); err == nil && len(strings.TrimSpace(string(raw))) >= 16 {
			token = strings.TrimSpace(string(raw))
		} else {
			raw := make([]byte, 24)
			rand.Read(raw)
			token = hex.EncodeToString(raw)
			if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
				logger.Fatalf("token file: %v", err)
			}
		}
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Fatalf("listen: %v", err)
	}
	// Where the Models screen looks for .gguf files.
	modelDirs := []string{filepath.Join(*data, "models"), "~/models"}
	if extra := os.Getenv("COSMOS_MODELS"); extra != "" {
		modelDirs = append(modelDirs, filepath.SplitList(extra)...)
	}
	os.MkdirAll(modelDirs[0], 0o700)
	home, _ := os.UserHomeDir()
	speaker := &voice.Manager{Dir: filepath.Join(*data, "voice"), ModelDir: filepath.Join(home, "models", "kokoro")}
	defer speaker.Stop()
	hearing := &voice.Hearing{}
	defer hearing.Stop()
	// Faces and voices are printed by a helper in her voice's Python
	// environment; the pictures she keeps go in the data folder.
	senses := voice.DefaultSenses(speaker.Dir)
	defer senses.Stop()
	b.Senses, b.SightDir = senses, filepath.Join(*data, "sight")
	srv := &http.Server{Handler: (&api.Server{B: b, Token: token, Launch: launch, ModelDirs: modelDirs, Voice: speaker, Hearing: hearing, Senses: senses}).Handler()}
	fmt.Printf("listening on http://%s\n", ln.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The heartbeat: reminders, the morning note, the weather watch,
	// follow-ups and sleep all hang off this one ticker.
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				b.Tick(ctx)
			}
		}
	}()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("serve: %v", err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
}
