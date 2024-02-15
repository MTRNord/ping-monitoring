package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
)

type PingCollector struct {
	Config        *Config
	Metrics       map[string]*prometheus.Desc
	LastCollected map[string]time.Time
	Failures      map[string]int
}

func (c *PingCollector) Describe(ch chan<- *prometheus.Desc) {
	mean := prometheus.NewDesc(prometheus.BuildFQName(collector, "", "mean"), "Mean ping time", []string{"homeserver", "origin", "direction"}, nil)
	median := prometheus.NewDesc(prometheus.BuildFQName(collector, "", "median"), "Median ping time", []string{"homeserver", "origin", "direction"}, nil)
	gmean := prometheus.NewDesc(prometheus.BuildFQName(collector, "", "gmean"), "GMean ping time", []string{"homeserver", "origin", "direction"}, nil)
	failures := prometheus.NewDesc(prometheus.BuildFQName(collector, "", "failures"), "Ping failures", []string{"origin", "direction"}, nil)
	c.Metrics = make(map[string]*prometheus.Desc)
	c.Metrics["mean"] = mean
	c.Metrics["median"] = median
	c.Metrics["gmean"] = gmean
	c.Metrics["failures"] = failures
	log.Infof("Registered metrics")
}

// For collection of matrix metrics we first need to ping.
// To do that we do 2 things:
// 1. Write `!ping` in the ping room as a message from our own homeserver
// 2. Send `!ping` from all remote homeservers to our ping room
// We then track our event_ids and make sure that at least one event is reaching our own homeserver
// And at least one event id (can be a different one) reaches the other homeservers.
// We also do react on our own `!ping` message and send a `!pong` back to the ping room based on the maubot echobot logic.
func (c *PingCollector) Collect(ch chan<- prometheus.Metric) {
	log.Infoln("Starting Collecting metrics")
	var wg sync.WaitGroup
	wg.Add(1)

	// Send ping from our own homeserver
	go c.SendPing(context.Background(), c.Config.OwnHomeserver.Client, ch, &wg)

	// Send ping from all remote homeservers
	for _, homeserver := range c.Config.RemoteHomeservers {
		wg.Add(1)
		go c.SendPing(context.Background(), homeserver.Client, ch, &wg)
	}

	wg.Wait()
}

// This sends a ping into the ping room
// It takes a homeserver config as an argument to know where to send the ping
func (c *PingCollector) SendPing(ctx context.Context, client *mautrix.Client, ch chan<- prometheus.Metric, wg *sync.WaitGroup) {
	defer wg.Done()
	if c.LastCollected[client.UserID.Homeserver()].Add(time.Duration(c.Config.PingRateSeconds) * time.Second).After(time.Now()) {
		log.Infof("Not sending ping as we sent one less than %d seconds ago", c.Config.PingRateSeconds)
		return
	}
	if c.LastCollected == nil {
		c.LastCollected = make(map[string]time.Time)
	}
	c.LastCollected[client.UserID.Homeserver()] = time.Now()
	log.Infof("Sending ping as %s", client.UserID)
	if c.Config.PingRoomID == "" {
		log.Errorf("No ping room ID found")
		return
	}

	// Send the ping
	resp, err := client.SendText(ctx, c.Config.PingRoomID, "!ping")
	if err != nil {
		log.Errorf("Failed to send ping: %s", err)
	}
	log.Infof("Sent ping as %s with event_id %s", client.UserID, resp.EventID)

	// Poll the json to check if any pongs have been received for this ping
	// Otherwise timeout after PingThresholdSeconds
	var currentData Data
	var gotKnownPong bool = false
	var pingTime time.Time = time.Now()
	u, err := url.Parse(c.Config.OwnHomeserver.Homeserver)
	if err != nil {
		log.Errorf("Failed to parse homeserver url: %s", err)
		return
	}

outer:
	for time.Now().Before(pingTime.Add(time.Duration(c.Config.PingThresholdSeconds) * time.Second)) {
		resp, err := http.Get(c.Config.PingJsonURL)
		if err != nil {
			log.Errorf("Failed to get ping json: %s", err)
		}

		// Parse the json
		// If we have a pong for this ping, we can break the loop
		// Otherwise we sleep for 1 second and try again
		defer resp.Body.Close()
		json.NewDecoder(resp.Body).Decode(&currentData)

		// Check if we have a pong for this ping
		log.Infof("Checking for pong for ping as %s", client.UserID.Homeserver())
		if ping, ok := currentData.Pings[client.UserID.Homeserver()]; ok {
			log.Infof("Got pong for ping as %s", client.UserID)

			// Check if its from a known remote homeserver in ping.Pongs
			for homeserver := range ping.Pongs {
				if homeserver == client.UserID.Homeserver() {
					continue
				}
				if _, ok := currentData.Pings[homeserver]; ok {
					log.Infof("Got pong for ping as %s from %s", client.UserID, homeserver)
					// break out of outer loop
					gotKnownPong = true
					break outer
				}
			}
		} else {
			log.Warnf("No pong for ping as %s", client.UserID)
		}

		// Sleep for 1 second
		time.Sleep(1 * time.Second)
	}

	// If we have not received a pong for this ping, we should log it
	if !gotKnownPong {
		log.Errorf("Failed to get pong for ping as %s", client.UserID)
		if c.Failures == nil {
			c.Failures = make(map[string]int)
		}

		if client.HomeserverURL.Host == u.Host {
			c.Failures["outgoing"]++
			ch <- prometheus.MustNewConstMetric(c.Metrics["failures"], prometheus.CounterValue, float64(c.Failures["outgoing"]), client.UserID.Homeserver(), "outgoing")
		} else {
			c.Failures["incoming"]++
			ch <- prometheus.MustNewConstMetric(c.Metrics["failures"], prometheus.CounterValue, float64(c.Failures["incoming"]), client.UserID.Homeserver(), "incoming")
		}
	}

	// Update mean, median and gmean metrics
	// All of these are per homeserver we received a pong from
	// They are all of type Gauge (Should they be a historgram?)
	for homeserver, ping := range currentData.Pings[client.UserID.Homeserver()].Pongs {
		if client.HomeserverURL.Host == u.Host {
			ch <- prometheus.MustNewConstMetric(c.Metrics["mean"], prometheus.GaugeValue, ping.Mean, homeserver, client.UserID.Homeserver(), "outgoing")
			ch <- prometheus.MustNewConstMetric(c.Metrics["median"], prometheus.GaugeValue, ping.Median, homeserver, client.UserID.Homeserver(), "outgoing")
			ch <- prometheus.MustNewConstMetric(c.Metrics["gmean"], prometheus.GaugeValue, ping.GMean, homeserver, client.UserID.Homeserver(), "outgoing")
		} else {
			ch <- prometheus.MustNewConstMetric(c.Metrics["mean"], prometheus.GaugeValue, ping.Mean, homeserver, client.UserID.Homeserver(), "incoming")
			ch <- prometheus.MustNewConstMetric(c.Metrics["median"], prometheus.GaugeValue, ping.Median, homeserver, client.UserID.Homeserver(), "incoming")
			ch <- prometheus.MustNewConstMetric(c.Metrics["gmean"], prometheus.GaugeValue, ping.GMean, homeserver, client.UserID.Homeserver(), "incoming")
		}
	}
}
