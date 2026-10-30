package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-fluxer/gofluxer"
)

func main() {
	log.SetFlags(log.LstdFlags)

	loadDotEnv(".env")
	cfg := loadConfig()
	if cfg.token == "" {
		log.Fatal("FLUXER_TOKEN is required")
	}

	// gofluxer sends through http.DefaultClient, so give it a timeout.
	http.DefaultClient = &http.Client{Timeout: 30 * time.Second}

	bot := gofluxer.NewBot(cfg.token, cfg.prefix)

	apiBase := strings.TrimSpace(os.Getenv("FLUXER_API"))
	gateway := strings.TrimSpace(os.Getenv("FLUXER_GATEWAY"))
	if apiBase != "" || gateway != "" {
		if apiBase == "" {
			apiBase = gofluxer.DefaultBaseURL
		}
		if gateway == "" {
			gateway = gofluxer.DefaultGatewayURL
		}
		bot.NewBotInstance(apiBase, gateway)
	}

	state := newAppState()
	app := &botApp{
		bot:   bot,
		cfg:   cfg,
		api:   newAPIClient(cfg, state),
		state: state,
	}

	var startOnce sync.Once
	bot.OnReady(func() {
		if err := bot.SetPresence("online"); err != nil {
			log.Printf("presence: %v", err)
		}
		startOnce.Do(func() {
			log.Printf("connected to Fluxer; serving prefix %q", cfg.prefix)
			go app.refreshStatsLoop()
			if cfg.instancePingEvery > 0 {
				go app.instanceSweepLoop()
			}
		})
	})
	bot.OnMessage(func(m *gofluxer.Message) {
		go app.handleMessage(m)
	})

	log.Printf("starting waifu Fluxer bot")
	if err := bot.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *botApp) refreshStatsLoop() {
	a.refreshStats()

	ticker := time.NewTicker(statsRefreshEvery)
	defer ticker.Stop()
	for range ticker.C {
		a.refreshStats()
	}
}

func (a *botApp) refreshStats() {
	body, err := a.api.getJSON(a.cfg.apiBase, "stats/public", nil, longReadTimeout)
	if err != nil {
		log.Printf("stats refresh failed: %s", a.cfg.redact(err.Error()))
		return
	}
	a.state.setCatalogue(
		getInt(body, "totalImages"),
		getInt(body, "totalTags"),
		getInt(body, "totalArtists"),
	)
}

func (a *botApp) instanceSweepLoop() {
	a.refreshInstancePings()

	ticker := time.NewTicker(a.cfg.instancePingEvery)
	defer ticker.Stop()
	for range ticker.C {
		a.refreshInstancePings()
	}
}
