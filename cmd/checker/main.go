// Command checker is the paily-check service entry point.
// It loads a YAML config, schedules a recurring check via cron, and on each
// tick fetches nodes from paily-core, runs the full pipeline, and posts
// results back.
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/openpaily/paily-check/internal/checker"
	"github.com/openpaily/paily-check/internal/client"
	"github.com/openpaily/paily-check/internal/config"
	geoMacro "github.com/openpaily/paily-check/internal/macros/geo"
	"github.com/openpaily/paily-check/internal/vendor"
	"github.com/sirupsen/logrus"
)

const (
	getNodesRetryCount    = 10
	getNodesRetryDelay    = time.Second
	postResultsRetryCount = 10
	postResultsRetryDelay = time.Second
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("checker: load config: %v", err)
	}

	logrus.SetOutput(io.Discard)

	cl, err := client.New(cfg.Core.URL, cfg.Core.Secret)
	if err != nil {
		log.Fatalf("checker: create client: %v", err)
	}
	v := vendor.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var background sync.WaitGroup
	if strings.EqualFold(cfg.Deep.Geo.Mode, "mmdb") && cfg.Deep.Geo.MmdbPath != "" {
		updater := geoMacro.NewMMDBUpdater(cfg.Deep.Geo.MmdbPath, geoMacro.DefaultMMDBUpdateURL, geoMacro.DefaultMMDBUpdateInterval)
		background.Add(1)
		go func() {
			defer background.Done()
			updater.Start(ctx)
		}()
		log.Printf("checker: geo mmdb updater enabled for %s", cfg.Deep.Geo.MmdbPath)
	}

	// running is 1 while a cycle is in progress; CAS ensures at most one
	// cycle runs at a time — concurrent cron triggers are skipped.
	var running atomic.Int32

	// runOnce encapsulates a single check cycle.
	// Returns false (skipped) when another cycle is already running.
	runOnce := func(urgent bool) bool {
		if !running.CompareAndSwap(0, 1) {
			log.Printf("checker: cycle already running, skipping trigger")
			return false
		}
		defer running.Store(0)

		// Sentinel: skip the whole cycle when local network is down,
		// so a router outage cannot produce bogus timeouts that kill alive nodes.
		checker.ResetSentinel()
		if err := checker.CheckSentinel(); err != nil {
			log.Printf("checker: sentinel says local network down, skipping cycle: %v", err)
			return true
		}

		var nodes []client.Node
		var err error
		for attempt := 0; attempt <= getNodesRetryCount; attempt++ {
			if urgent {
				// A queued round must never fall back to the rotation: an empty queue has
				// to cost nothing, or every poll would launch a full sweep.
				nodes, err = cl.GetUrgentNodes()
			} else {
				nodes, err = cl.GetNodes()
			}
			if err == nil {
				break
			}
			if attempt == getNodesRetryCount {
				log.Printf("checker: GetNodes failed after %d retries: %v", getNodesRetryCount, err)
				return true
			}
			log.Printf("checker: GetNodes failed (attempt %d/%d): %v", attempt+1, getNodesRetryCount+1, err)
			time.Sleep(getNodesRetryDelay)
		}
		if len(nodes) == 0 {
			log.Printf("checker: no nodes returned, skipping cycle")
			return true
		}
		log.Printf("checker: starting pipeline for %d nodes", len(nodes))

		results := checker.Run(context.Background(), nodes, v, cfg)

		// Sentinel re-check before posting: an outage that started mid-cycle
		// poisons every result collected after it - discard instead of posting.
		if err := checker.DiscardSentinel(); err != nil {
			log.Printf("checker: sentinel failed after pipeline, discarding %d results: %v", len(results), err)
			return true
		}

		for attempt := 0; attempt <= postResultsRetryCount; attempt++ {
			err = cl.PostResults(results)
			if err == nil {
				break
			}
			if attempt == postResultsRetryCount {
				log.Printf("checker: PostResults failed after %d retries: %v", postResultsRetryCount, err)
				return true
			}
			log.Printf("checker: PostResults failed (attempt %d/%d): %v", attempt+1, postResultsRetryCount+1, err)
			time.Sleep(postResultsRetryDelay)
		}
		log.Printf("checker: cycle complete, posted %d results", len(results))
		return true
	}

	// Poll paily-core for an operator-queued recheck.
	//
	// The cron below is the heartbeat; this is the interrupt. Adding a node or a
	// source used to mean waiting up to two hours for the next tick, or restarting
	// this process, which re-probes the whole rotation and throws the round away.
	// A short poll makes an enqueue effectively immediate at one small request per
	// interval.
	//
	// runOnce() guards itself with a CAS, so a poll that fires mid-cycle is skipped
	// rather than stacked.
	if cfg.Schedule.UrgentPollSeconds > 0 {
		interval := time.Duration(cfg.Schedule.UrgentPollSeconds) * time.Second
		log.Printf("checker: polling for urgent rechecks every %s", interval)
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					pending, err := cl.UrgentPending()
					if err != nil {
						// A failed poll must not end the loop: the cron still runs and
						// the next tick retries.
						log.Printf("checker: urgent poll failed: %v", err)
						continue
					}
					if pending == 0 {
						continue
					}
					log.Printf("checker: %d node(s) queued for an urgent recheck", pending)
					// Queued work runs as its own small round, so it must not carry the
					// rotation with it.
					runOnce(true)
				}
			}
		}()
	}
	// Validate the cron expression before starting anything.
	c := cron.New(cron.WithSeconds())
	_, err = c.AddFunc(cfg.Schedule.Cron, func() { runOnce(false) })
	if err != nil {
		log.Fatalf("checker: invalid cron expression %q: %v", cfg.Schedule.Cron, err)
	}
	log.Printf("checker: scheduled with cron %q", cfg.Schedule.Cron)

	// Run immediately on startup, then hand off to the cron scheduler.
	log.Printf("checker: running initial cycle")
	runOnce(false)

	c.Start()
	defer c.Stop()

	// Block until SIGINT / SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Printf("checker: shutting down")
	cancel()
	background.Wait()
}
