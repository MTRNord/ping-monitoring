package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	collector = "matrix_ping_exporter"
)

func main() {
	var config = ReadConfig()
	var address = config.Address
	flag.StringVar(&address, "address", "0.0.0.0:8080", "Address to listen on")
	flag.Parse()

	// Create a matrix client for each homeserver
	config.OwnHomeserver.Client = createMatrixClient(&config, &config.OwnHomeserver)
	for i := range config.RemoteHomeservers {
		config.RemoteHomeservers[i].Client = createMatrixClient(&config, &config.RemoteHomeservers[i])
	}

	pingCollector := &PingCollector{
		Config: &config,
		Mean: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: collector,
			Name:      "mean",
			Help:      "Mean ping time",
		}, []string{"homeserver", "origin", "direction"}),
		Median: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: collector,
			Name:      "median",
			Help:      "Median ping time",
		}, []string{"homeserver", "origin", "direction"}),
		GMean: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: collector,
			Name:      "gmean",
			Help:      "GMean ping time",
		}, []string{"homeserver", "origin", "direction"}),
		Failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: collector,
			Name:      "failures",
			Help:      "Ping failures",
		}, []string{"origin", "direction"}),
	}

	go func() {
		pingCollector.UpdateData()
		time.Sleep(time.Duration(config.PingRateSeconds+1) * time.Second)
		for {
			pingCollector.UpdateData()
			time.Sleep(time.Duration(config.PingRateSeconds+1) * time.Second)
		}
	}()

	reg := prometheus.NewRegistry()
	reg.MustRegister(version.NewCollector(collector))
	reg.MustRegister(pingCollector)

	http.Handle("/metrics", promhttp.HandlerFor(
		reg,
		promhttp.HandlerOpts{},
	))

	log.Infof("Starting http server - %s", address)
	if err := http.ListenAndServe(address, nil); err != nil {
		log.Errorf("Failed to start http server: %s", err)
	}
}

type PongEventRelation struct {
	RelType string `json:"rel_type"`
	EventID string `json:"event_id"`
}

type PongEventContent struct {
	MsgType   string            `json:"msgtype"`
	Body      string            `json:"body"`
	RelatesTo PongEventRelation `json:"m.relates_to"`
}

func createMatrixClient(config *Config, homeserver *MatrixConfig) *mautrix.Client {
	var startTime = time.Now()

	client, err := mautrix.NewClient(homeserver.Homeserver, id.UserID(homeserver.Username), "")
	if err != nil {
		log.Errorf("Failed to create client for %s: %s", homeserver.Homeserver, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if homeserver.AccessToken == "" {
		loginresp, err := client.Login(ctx, &mautrix.ReqLogin{
			Type:               mautrix.AuthTypePassword,
			Identifier:         mautrix.UserIdentifier{Type: "m.id.user", User: homeserver.Username},
			Password:           homeserver.Password,
			StoreCredentials:   true,
			StoreHomeserverURL: true,
		})
		if err != nil {
			log.Errorf("Failed to login to %s: %s", homeserver.Homeserver, err)
			os.Exit(1)
		}
		log.Infof("Logged in as %s", loginresp.UserID)

		// Tell user to store the access token and device id and remove the password from the config
		log.Warnf("Please store the access token and device id for %s and remove the password from the config", homeserver.Homeserver)
		log.Warnf("Access token: %s", loginresp.AccessToken)
		log.Warnf("Device ID: %s", loginresp.DeviceID)
	} else {
		client.AccessToken = homeserver.AccessToken
		client.DeviceID = id.DeviceID(homeserver.DeviceID)
	}

	// join the ping room
	resp, err := client.JoinRoom(ctx, config.PingRoom, "", nil)
	if err != nil {
		log.Errorf("Failed to join ping room: %s", err)
		os.Exit(1)
	}
	log.Infof("Joined ping room %s", resp.RoomID)
	config.PingRoomID = resp.RoomID

	syncer := client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		if evt.RoomID != config.PingRoomID {
			return
		}
		if evt.Sender == client.UserID {
			return
		}
		// Make sure the message is not older than startTime
		if evt.Timestamp < startTime.Unix()*1000 {
			return
		}

		if evt.Content.AsMessage().Body == "!ping" {
			log.Infoln("Received ping message in pingroom by", evt.Sender)
			// Respond to the ping
			pong_event_content := PongEventContent{
				MsgType: "m.notice",
				Body:    "Pong!",
				RelatesTo: PongEventRelation{
					RelType: "xyz.maubot.pong",
					EventID: evt.ID.String(),
				},
			}
			_, err := client.SendMessageEvent(ctx, evt.RoomID, event.EventMessage, pong_event_content)
			if err != nil {
				log.Errorf("Failed to send pong: %s", err)
			}
		}
	})

	go func() {
		err = client.Sync()
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Errorf("Failed to sync: %s", err)
			os.Exit(1)
		}
	}()

	return client
}
