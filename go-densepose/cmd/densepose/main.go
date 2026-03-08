package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ruvnet/go-densepose/pkg/csi"
	"github.com/ruvnet/go-densepose/pkg/hardware"
	"github.com/ruvnet/go-densepose/pkg/server"
	"github.com/ruvnet/go-densepose/pkg/wifi"
)

var version = "0.1.0"

func main() {
	source := flag.String("source", "auto", "Data source: auto, esp32, wifi, simulated")
	httpPort := flag.Int("http-port", 3000, "HTTP server port")
	udpPort := flag.Int("udp-port", 5005, "ESP32 UDP port")
	tickMs := flag.Int("tick-ms", 100, "Sensing tick interval (ms)")
	uiPath := flag.String("ui-path", "", "Path to static UI files")
	showVersion := flag.Bool("version", false, "Show version and exit")

	flag.Parse()

	if *showVersion {
		fmt.Printf("go-densepose v%s\n", version)
		os.Exit(0)
	}

	log.Printf("go-densepose v%s", version)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	frameCh := make(chan *csi.CsiFrame, 256)
	sourceName := startSource(*source, *udpPort, frameCh)

	cfg := server.Config{
		HTTPPort: *httpPort,
		TickMs:   *tickMs,
		UIPath:   *uiPath,
		Source:   sourceName,
	}

	srv := server.New(cfg, frameCh, sourceName)
	if err := srv.Run(ctx); err != nil {
		log.Printf("Server exited: %v", err)
	}
}

func startSource(source string, udpPort int, out chan<- *csi.CsiFrame) string {
	switch source {
	case "esp32":
		return startESP32(udpPort, out)
	case "wifi":
		return startWiFi(out)
	case "simulated":
		return startSimulated(out)
	default:
		// Auto: try WiFi, fall back to simulated
		log.Println("[auto] trying simulated source (use --source wifi for real WiFi)")
		return startSimulated(out)
	}
}

func startESP32(port int, out chan<- *csi.CsiFrame) string {
	agg := hardware.NewAggregator("0.0.0.0", port, 256)
	if err := agg.Start(); err != nil {
		log.Fatalf("Failed to start ESP32 aggregator: %v", err)
	}
	go func() {
		for frame := range agg.Frames() {
			out <- frame
		}
	}()
	return "esp32"
}

func startWiFi(out chan<- *csi.CsiFrame) string {
	sc := wifi.NewScanner(10.0, 256)
	if err := sc.Start(); err != nil {
		log.Printf("WiFi scanner failed: %v, falling back to simulated", err)
		return startSimulated(out)
	}
	go func() {
		for frame := range sc.Frames() {
			out <- frame
		}
	}()
	return sc.Source()
}

func startSimulated(out chan<- *csi.CsiFrame) string {
	sim := wifi.NewSimulatedSource(10.0, 256)
	if err := sim.Start(); err != nil {
		log.Fatalf("Failed to start simulated source: %v", err)
	}
	go func() {
		for frame := range sim.Frames() {
			out <- frame
		}
	}()
	return "simulated"
}
