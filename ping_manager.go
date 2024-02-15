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
	"maunium.net/go/mautrix/id"
)

type PingCollector struct {
	Config        *Config
	Mean          *prometheus.GaugeVec
	Median        *prometheus.GaugeVec
	GMean         *prometheus.GaugeVec
	Failures      *prometheus.CounterVec
	LastCollected map[string]time.Time
}

func (c *PingCollector) Describe(ch chan<- *prometheus.Desc) {
	c.Mean.Describe(ch)
	c.Median.Describe(ch)
	c.GMean.Describe(ch)
	c.Failures.Describe(ch)
	log.Infof("Registered metrics")
}

func (c *PingCollector) Collect(ch chan<- prometheus.Metric) {
	c.Mean.Collect(ch)
	c.Median.Collect(ch)
	c.GMean.Collect(ch)
	c.Failures.Collect(ch)
}

// For collection of matrix metrics we first need to ping.
// To do that we do 2 things:
// 1. Write `!ping` in the ping room as a message from our own homeserver
// 2. Send `!ping` from all remote homeservers to our ping room
// We then track our event_ids and make sure that at least one event is reaching our own homeserver
// And at least one event id (can be a different one) reaches the other homeservers.
// We also do react on our own `!ping` message and send a `!pong` back to the ping room based on the maubot echobot logic.
func (c *PingCollector) UpdateData() {
	log.Infoln("Starting Collecting metrics")
	var wg sync.WaitGroup
	wg.Add(1)

	// Send ping from our own homeserver
	go c.SendPing(context.Background(), c.Config.OwnHomeserver.Client, &wg)

	// Send ping from all remote homeservers
	for _, homeserver := range c.Config.RemoteHomeservers {
		wg.Add(1)
		go c.SendPing(context.Background(), homeserver.Client, &wg)
	}

	wg.Wait()
}

// This sends a ping into the ping room
// It takes a homeserver config as an argument to know where to send the ping
func (c *PingCollector) SendPing(ctx context.Context, client *mautrix.Client, wg *sync.WaitGroup) {
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
	eventID := resp.EventID

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
	direction := "outgoing"
	if client.HomeserverURL.Host != u.Host {
		direction = "incoming"
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
			hs_parsed := id.UserID(c.Config.OwnHomeserver.Username).Homeserver()
			if _, ok := ping.Pongs[hs_parsed].Diffs[eventID.String()]; ok {
				log.Infof("Got pong for ping as %s", client.UserID)
				gotKnownPong = true
				break outer
			}
			// Check if its from a known remote homeserver in ping.Pongs
			for _, homeserver := range c.Config.RemoteHomeservers {
				hs_parsed := id.UserID(homeserver.Username).Homeserver()
				if _, ok := ping.Pongs[hs_parsed].Diffs[eventID.String()]; ok {
					log.Infof("Got pong for ping as %s", client.UserID)
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
		c.Failures.WithLabelValues(client.UserID.Homeserver(), direction).Inc()
	}

	// Wait 5s before we collect the final data
	time.Sleep(5 * time.Second)
	pingresp, err := http.Get(c.Config.PingJsonURL)
	if err != nil {
		log.Errorf("Failed to get ping json: %s", err)
	}

	// Parse the json
	// If we have a pong for this ping, we can break the loop
	// Otherwise we sleep for 1 second and try again
	defer pingresp.Body.Close()
	json.NewDecoder(pingresp.Body).Decode(&currentData)

	// Update mean, median and gmean metrics
	// All of these are per homeserver we received a pong from
	// They are all of type Gauge (Should they be a historgram?)
	for homeserver, ping := range currentData.Pings[client.UserID.Homeserver()].Pongs {
		direction := "outgoing"
		if client.HomeserverURL.Host != u.Host {
			direction = "incoming"
		}
		c.Mean.WithLabelValues(homeserver, client.UserID.Homeserver(), direction).Set(ping.Mean)
		c.Median.WithLabelValues(homeserver, client.UserID.Homeserver(), direction).Set(ping.Median)
		c.GMean.WithLabelValues(homeserver, client.UserID.Homeserver(), direction).Set(ping.GMean)
	}
}
