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
	config.OwnHomeserver.Client = createMatrixClient(config, config.OwnHomeserver)
	for i := range config.RemoteHomeservers {
		config.RemoteHomeservers[i].Client = createMatrixClient(config, config.RemoteHomeservers[i])
	}

	prometheus.Register(version.NewCollector(collector))
	prometheus.Register(&PingCollector{
		Config: config,
	})

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		h := promhttp.HandlerFor(prometheus.Gatherers{
			prometheus.DefaultGatherer,
		}, promhttp.HandlerOpts{})
		h.ServeHTTP(w, r)
	})

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

func createMatrixClient(Config Config, Homeserver MatrixConfig) *mautrix.Client {
	var startTime = time.Now()

	client, err := mautrix.NewClient(Homeserver.Homeserver, id.UserID(Homeserver.Username), "")
	if err != nil {
		log.Errorf("Failed to create client for %s: %s", Homeserver.Homeserver, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = client.Login(ctx, &mautrix.ReqLogin{
		Type:       mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{Type: "m.id.user", User: Homeserver.Username},
		Password:   Homeserver.Password,
	})
	if err != nil {
		log.Errorf("Failed to login to %s: %s", Homeserver.Homeserver, err)
		os.Exit(1)
	}

	// join the ping room
	resp, err := client.JoinRoom(ctx, Config.PingRoom, "", nil)
	if err != nil {
		log.Errorf("Failed to join ping room: %s", err)
		os.Exit(1)
	}
	Config.PingRoomID = resp.RoomID

	syncer := client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		if evt.RoomID != Config.PingRoomID {
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

	syncCtx, cancelSync := context.WithCancel(context.Background())
	defer cancelSync()

	go func() {
		err = client.SyncWithContext(syncCtx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Errorf("Failed to sync: %s", err)
			os.Exit(1)
		}
	}()

	return client
}
