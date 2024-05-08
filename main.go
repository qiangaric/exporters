package main

import (
	"exporters/collector"
	"flag"
	"log"
	"net/http"

	// "github.com/w0nwig/health-check-exporter/collector"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// 命令行参数
	listenAddr  = flag.String("web.listen-port", "8089", "An port to listen on for web interface and telemetry.")
	metricsPath = flag.String("web.telemetry-path", "/metrics", "A path under which to expose metrics.")
)

func main() {
	flag.Parse()
	// collector.NewMetrics().Collect()
	metrics := collector.NewMetrics()
	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics)

	http.Handle(*metricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
            <head><title>A Prometheus Exporter</title></head>
            <body>
            <h1>A Prometheus Exporter</h1>
            <p><a href='/metrics'>Metrics</a></p>
            </body>
            </html>`))
	})

	log.Printf("Starting Server at http://localhost:%s%s", *listenAddr, *metricsPath)
	log.Fatal(http.ListenAndServe(":"+*listenAddr, nil))
}
