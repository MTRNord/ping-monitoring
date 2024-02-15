package main

type Ping struct {
	Diffs  map[string]int `json:"diffs"`
	Mean   float64        `json:"mean"`
	Median float64        `json:"median"`
	GMean  float64        `json:"gmean"`
}

type Pings map[string]Ping

type Pong struct {
	Pings  []string `json:"pings"`
	Mean   float64  `json:"mean"`
	Median float64  `json:"median"`
	GMean  float64  `json:"gmean"`
}

type PongServers []string

type Data struct {
	Disclaimer  string      `json:"disclaimer"`
	Pings       Pings       `json:"pings"`
	Mean        float64     `json:"mean"`
	PongServers PongServers `json:"pongservers"`
}
