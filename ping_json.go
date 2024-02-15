package main

type Ping struct {
	Pongs  map[string]Pong `json:"pongs"`
	Pings  []string        `json:"pings"`
	Mean   float64         `json:"mean"`
	Median float64         `json:"median"`
	GMean  float64         `json:"gmean"`
}

// A map of homeserver names to ping data
type Pings map[string]Ping

type Pong struct {
	Diffs  map[string]string `json:"diffs"`
	Mean   float64           `json:"mean"`
	Median float64           `json:"median"`
	GMean  float64           `json:"gmean"`
}

type Data struct {
	Disclaimer  string   `json:"disclaimer"`
	Pings       Pings    `json:"pings"`
	Mean        float64  `json:"mean"`
	PongServers []string `json:"pongservers"`
}
