package main

import (
	"flag"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	log "github.com/sirupsen/logrus"
)

func main() {
	var config = ReadConfig()
	var address = config.Address
	flag.StringVar(&address, "address", "0.0.0.0:8080", "Address to listen on")
	flag.Parse()

	prometheus.Register(version.NewCollector("matrix_ping_exporter"))

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
