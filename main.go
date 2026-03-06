package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	var cfg Config
	var port uint

	flag.StringVar(&cfg.Target, "target", "192.168.2.104", "SNMP agent IP/host")
	flag.UintVar(&port, "port", 161, "SNMP port")
	flag.StringVar(&cfg.Community, "community", "public", "SNMP community")
	flag.StringVar(&cfg.Version, "v", "2c", "SNMP version: 1, 2c")
	flag.StringVar(&cfg.Listen, "listen", "127.0.0.1:8080", "HTTP listen address")

	flag.DurationVar(&cfg.HealthInterval, "health-interval", 250*time.Millisecond, "Light health poll interval")
	flag.DurationVar(&cfg.FullInterval, "full-interval", 5*time.Second, "Full poll interval")
	flag.DurationVar(&cfg.Timeout, "timeout", 800*time.Millisecond, "SNMP timeout")
	flag.IntVar(&cfg.Retries, "retries", 0, "SNMP retries")

	flag.IntVar(&cfg.DownAfterFails, "down-after", 2, "Enter DOWN after N consecutive failures")
	flag.DurationVar(&cfg.DownHold, "down-hold", 2200*time.Millisecond, "Fail must persist >2s to enter DOWN")
	flag.IntVar(&cfg.UpAfterOK, "up-after", 3, "Enter UP after N consecutive OK")
	flag.DurationVar(&cfg.UpHold, "up-hold", 2200*time.Millisecond, "OK must persist >2s to enter UP")
	flag.DurationVar(&cfg.StaleAfter, "stale-after", 12*time.Second, "STALE threshold")

	flag.StringVar(&cfg.StateFile, "state-file", "state.json", "Reliability persistence file")
	flag.StringVar(&cfg.SnapshotFile, "snapshot-file", "last_snapshot.json", "Last snapshot persistence file")
	flag.IntVar(&cfg.MaxEventLog, "events", 200, "Max events")
	flag.DurationVar(&cfg.SaveEvery, "save-every", 10*time.Second, "Periodic save")

	flag.BoolVar(&cfg.ForceDownRisk, "force-down-risk", true, "DOWN/STALE => probNow=100%")

	flag.DurationVar(&cfg.ForecastHorizon, "forecast-horizon", 1*time.Hour, "Forecast horizon")
	flag.Float64Var(&cfg.ForecastAlpha, "forecast-alpha", 3.0, "Hazard amplification by anomaly")
	flag.Float64Var(&cfg.LambdaPrior, "lambda-prior", 1.0/(24.0*3600.0), "Prior hazard (1/sec)")

	flag.IntVar(&cfg.MinSamplesToScore, "min-samples", 30, "Min FULL samples to score anomaly")
	flag.Float64Var(&cfg.EWMAAlpha, "ewma-alpha", 0.15, "EWMA alpha")
	flag.Float64Var(&cfg.EWMAVarAlpha, "ewma-var-alpha", 0.10, "EWMA residual variance alpha")
	flag.Float64Var(&cfg.ZToScoreScale, "zscale", 3.0, "Z scale")
	flag.Float64Var(&cfg.HWAlpha, "hw-alpha", 0.20, "Holt-Winters alpha")
	flag.Float64Var(&cfg.HWBeta, "hw-beta", 0.05, "Holt-Winters beta")
	flag.Float64Var(&cfg.HWGamma, "hw-gamma", 0.10, "Holt-Winters gamma")
	flag.IntVar(&cfg.HWSeasonLen, "hw-season", 12, "Season length (in FULL samples)")

	flag.Float64Var(&cfg.ThreshCPUHighPct, "thr-cpu", 90, "Threshold: CPU% high")
	flag.Float64Var(&cfg.ThreshLinkDownRatio, "thr-linkdown", 0.30, "Threshold: link down ratio high")
	flag.Float64Var(&cfg.ThreshErrRatePps, "thr-err", 5.0, "Threshold: avg errors/s high")
	flag.Float64Var(&cfg.ThreshDiscRatePps, "thr-disc", 5.0, "Threshold: avg discards/s high")

	flag.Parse()

	if cfg.Target == "" {
		log.Fatal("empty -target")
	}
	if port > 65535 {
		log.Fatalf("invalid -port %d", port)
	}
	cfg.Port = uint16(port)
	if cfg.DownHold < 2*time.Second {
		log.Fatal("down-hold must be >= 2s")
	}
	if net.ParseIP(cfg.Target) == nil {
		// allow DNS
	}

	coll := NewCollector(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coll.Run(ctx.Done())

	htmlBytes, err := os.ReadFile("ui/ui.html")
	if err != nil {
		log.Printf("warning: cannot read ui.html: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if len(htmlBytes) == 0 {
			_, _ = w.Write([]byte("SNMP Risk Monitor UI file missing"))
			return
		}
		_, _ = w.Write(htmlBytes)
	})

	mux.HandleFunc("/api/v1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		s := coll.GetSnapshot()

		b, err := json.MarshalIndent(s, "", "  ")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			msg := `{"error":"json encode failed","detail":` + strconv.Quote(err.Error()) + `}`
			_, _ = w.Write([]byte(msg))
			return
		}
		_, _ = w.Write(append(b, '\n'))
	})

	mux.HandleFunc("/api/v1/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		coll.ResetAll()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	log.Printf("listening on http://%s (SNMP %s:%d v%s)", cfg.Listen, cfg.Target, cfg.Port, cfg.Version)
	log.Fatal(srv.ListenAndServe())
}
