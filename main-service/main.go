package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"math"
	mrand "math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type FeatureDef struct {
	Name   string   `json:"name"`
	Weight float64  `json:"weight"`
	Models []string `json:"models"`
}

type FeatureConfig struct {
	Features []FeatureDef `json:"features"`
}

func loadFeatureConfig(path string) FeatureConfig {
	var cfg FeatureConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(b, &cfg)
	return cfg
}

type State struct {
	sync.RWMutex
	Timestamp string                        `json:"timestamp"`
	Features  map[string]float64            `json:"features"`
	Methods   map[string]map[string]float64 `json:"methods"`
	Anomaly   map[string]float64            `json:"anomaly"`
	DegIndex  float64                       `json:"degradation_index"`
	FailureP  float64                       `json:"failure_probability_1h"`
	Beta      float64                       `json:"beta"`
	Eta       float64                       `json:"eta"`
	MTBF      float64                       `json:"mtbf_hours"`
	MTTR      float64                       `json:"mttr_hours"`
	Hazard    float64                       `json:"hazard_1h"`
	Avail     float64                       `json:"availability"`
	Up        float64                       `json:"up"`
	Failures  []float64                     `json:"failure_intervals_hours"`
	Weights   map[string]float64            `json:"weights"`
}

var state = &State{Features: map[string]float64{}, Methods: map[string]map[string]float64{}, Anomaly: map[string]float64{}, Failures: []float64{}}
var promURL = getenv("PROMETHEUS_URL", "http://localhost:9090")
var mlURL = getenv("ML_SERVICE_URL", "http://localhost:8000")
var horizonHours = atof(getenv("HORIZON_HOURS", "1"))
var pollSeconds = atoi(getenv("POLL_INTERVAL_SECONDS", "30"))
var lastDown *time.Time
var lastFailureMark *time.Time
var startTime = time.Now()

var metricDeg = prometheus.NewGauge(prometheus.GaugeOpts{Name: "main_degradation_index", Help: "Integrated degradation index"})
var metricFailure = prometheus.NewGauge(prometheus.GaugeOpts{Name: "main_failure_probability_1h", Help: "Failure probability in 1 hour"})
var metricMTBF = prometheus.NewGauge(prometheus.GaugeOpts{Name: "main_mtbf_hours", Help: "MTBF hours"})
var metricMTTR = prometheus.NewGauge(prometheus.GaugeOpts{Name: "main_mttr_hours", Help: "MTTR hours"})
var metricHazard = prometheus.NewGauge(prometheus.GaugeOpts{Name: "main_hazard_1h", Help: "Hazard function at 1 hour"})
var metricAvail = prometheus.NewGauge(prometheus.GaugeOpts{Name: "main_availability", Help: "Availability"})

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func atof(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
func atoi(s string) int     { v, _ := strconv.Atoi(s); return v }
func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func promQuery(expr string) float64 {
	q := strings.NewReplacer(" ", "%20", "+", "%2B", "\"", "%22", "[", "%5B", "]", "%5D", "{", "%7B", "}", "%7D", "=", "%3D", ",", "%2C", "(", "%28", ")", "%29").Replace(expr)
	url := promURL + "/api/v1/query?query=" + q
	resp, err := http.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		Data struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &data) != nil || len(data.Data.Result) == 0 || len(data.Data.Result[0].Value) < 2 {
		return 0
	}
	s, ok := data.Data.Result[0].Value[1].(string)
	if !ok {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func collectFeatures() map[string]float64 {
	memUsed := promQuery(`avg(hrStorageUsed{hrStorageDescr="memory"})`)
	memSize := promQuery(`avg(hrStorageSize{hrStorageDescr="memory"})`)
	f := map[string]float64{
		"cpu_load":         promQuery(`avg(hrProcessorLoad)`),
		"mem_util":         safeDiv(memUsed, memSize),
		"temp_celsius":     promQuery(`avg(entPhySensorValue{sensor="chassis_temp"})`),
		"traffic_in_rate":  promQuery(`sum(rate(ifHCInOctets[1m]))`),
		"traffic_out_rate": promQuery(`sum(rate(ifHCOutOctets[1m]))`),
		"if_error_rate":    promQuery(`sum(rate(ifInErrors[1m]) + rate(ifOutErrors[1m]))`),
		"if_discard_rate":  promQuery(`sum(rate(ifInDiscards[1m]))`),
		"if_down_ratio":    promQuery(`avg(ifOperStatus != 1)`),
		"reboot_indicator": promQuery(`changes(sysUpTime[10m]) > 0`),
	}
	state.Up = promQuery(`avg(up{job="snmp_switch_sim"})`)
	return f
}

func trainModel() {
	features := []string{"cpu_load", "mem_util", "temp_celsius", "traffic_in_rate", "traffic_out_rate", "if_error_rate", "if_discard_rate", "if_down_ratio", "reboot_indicator"}
	samples := make([]map[string]float64, 0, 6000)
	for i := 0; i < 6000; i++ {
		cyc1 := math.Sin(2 * math.Pi * float64(i%288) / 288.0)
		cyc2 := math.Sin(2 * math.Pi * float64(i%96) / 96.0)
		samples = append(samples, map[string]float64{
			"cpu_load":         24 + 12*cyc1 + 3*cyc2 + mrand.NormFloat64()*2,
			"mem_util":         0.45 + 0.10*(1+cyc1)/2 + mrand.NormFloat64()*0.015,
			"temp_celsius":     40 + 6*(1+cyc1)/2 + mrand.NormFloat64()*0.9,
			"traffic_in_rate":  5e6 + 1.5e6*(1+cyc1)/2 + mrand.NormFloat64()*2e5,
			"traffic_out_rate": 4.7e6 + 1.4e6*(1+cyc1)/2 + mrand.NormFloat64()*2e5,
			"if_error_rate":    math.Abs(mrand.NormFloat64() * 0.02),
			"if_discard_rate":  math.Abs(mrand.NormFloat64() * 0.01),
			"if_down_ratio":    0,
			"reboot_indicator": 0,
		})
	}
	body, _ := json.Marshal(map[string]any{"feature_order": features, "samples": samples})
	_, _ = http.Post(mlURL+"/train", "application/json", bytes.NewReader(body))
	state.Weights = map[string]float64{
		"if_error_rate":    0.22,
		"if_discard_rate":  0.16,
		"if_down_ratio":    0.20,
		"temp_celsius":     0.12,
		"cpu_load":         0.10,
		"mem_util":         0.08,
		"traffic_in_rate":  0.05,
		"traffic_out_rate": 0.05,
		"reboot_indicator": 0.02,
	}
}

func predict(features map[string]float64) (map[string]map[string]float64, map[string]float64) {
	body, _ := json.Marshal(map[string]any{"metrics": features})
	resp, err := http.Post(mlURL+"/predict", "application/json", bytes.NewReader(body))
	if err != nil {
		return map[string]map[string]float64{}, map[string]float64{}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Methods   map[string]any     `json:"methods"`
		PerMetric map[string]float64 `json:"per_metric"`
	}
	_ = json.Unmarshal(raw, &out)
	methods := map[string]map[string]float64{}
	for k, v := range out.Methods {
		if mm, ok := v.(map[string]any); ok {
			methods[k] = map[string]float64{}
			for kk, vv := range mm {
				if f, ok := vv.(float64); ok {
					methods[k][kk] = f
				}
			}
		}
	}
	return methods, out.PerMetric
}

func computeDegIndex(perMetric map[string]float64) float64 {
	h := 0.0
	for k, v := range perMetric {
		h += state.Weights[k] * v
	}
	if h < 0 {
		h = 0
	}
	if h > 1 {
		h = 1
	}
	return h
}

func updateFailures() {
	now := time.Now()
	up := state.Up >= 0.5
	if !up {
		if lastDown == nil {
			t := now
			lastDown = &t
			if lastFailureMark != nil {
				delta := t.Sub(*lastFailureMark).Hours()
				if delta > 0 {
					state.Failures = append(state.Failures, delta)
				}
			} else {
				delta := t.Sub(startTime).Hours()
				if delta > 0 {
					state.Failures = append(state.Failures, delta)
				}
			}
			lastFailureMark = &t
		}
	} else if lastDown != nil {
		dur := now.Sub(*lastDown).Hours()
		if state.MTTR == 0 {
			state.MTTR = dur
		} else {
			state.MTTR = 0.7*state.MTTR + 0.3*dur
		}
		lastDown = nil
	}
}

func weibullEstimate(samples []float64) (float64, float64) {
	if len(samples) < 2 {
		return 2.0, 100.0
	}
	xs := make([]float64, 0, len(samples))
	for _, x := range samples {
		if x > 0 {
			xs = append(xs, x)
		}
	}
	if len(xs) < 2 {
		return 2.0, 100.0
	}
	sort.Float64s(xs)
	beta := 1.5
	for i := 0; i < 50; i++ {
		s1, s2, s3, s4 := 0.0, 0.0, 0.0, 0.0
		n := float64(len(xs))
		for _, x := range xs {
			lx := math.Log(x)
			xb := math.Pow(x, beta)
			s1 += xb * lx
			s2 += xb
			s3 += lx
			s4 += xb * lx * lx
		}
		g := s1/s2 - s3/n - 1.0/beta
		gp := s4/s2 - math.Pow(s1/s2, 2) + 1.0/(beta*beta)
		next := beta - g/gp
		if math.IsNaN(next) || math.IsInf(next, 0) || next <= 0 {
			break
		}
		if math.Abs(next-beta) < 1e-7 {
			beta = next
			break
		}
		beta = next
	}
	sum := 0.0
	for _, x := range xs {
		sum += math.Pow(x, beta)
	}
	eta := math.Pow(sum/float64(len(xs)), 1.0/beta)
	return beta, eta
}

func recomputeReliability() {
	beta, eta := weibullEstimate(state.Failures)
	state.Beta, state.Eta = beta, eta
	t := horizonHours
	effectiveEta := state.Eta * (1.0 - 0.6*state.DegIndex)
	if effectiveEta < 1.0 {
		effectiveEta = 1.0
	}
	state.FailureP = 1 - math.Exp(-math.Pow(horizonHours/effectiveEta, state.Beta))
	state.MTBF = eta * math.Gamma(1+1/beta)
	if state.MTTR == 0 {
		state.MTTR = 0.05
	}
	state.Hazard = (beta / eta) * math.Pow(t/eta, beta-1)
	state.Avail = state.MTBF / (state.MTBF + state.MTTR)
}

func pollLoop() {
	time.Sleep(20 * time.Second)
	trainModel()
	for {
		state.Lock()
		state.Timestamp = time.Now().Format(time.RFC3339)
		state.Features = collectFeatures()
		state.Methods, state.Anomaly = predict(state.Features)
		state.DegIndex = computeDegIndex(state.Anomaly)
		updateFailures()
		recomputeReliability()
		metricDeg.Set(state.DegIndex)
		metricFailure.Set(state.FailureP)
		metricMTBF.Set(state.MTBF)
		metricMTTR.Set(state.MTTR)
		metricHazard.Set(state.Hazard)
		metricAvail.Set(state.Avail)
		state.Unlock()
		time.Sleep(time.Duration(pollSeconds) * time.Second)
	}
}

const uiHTML = `<!doctype html>
<html lang="ru">

<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Reliability</title>
  <style>
    :root {
      --bg: #0b1020;
      --panel: #121b31;
      --line: #26344f;
      --text: #edf2f7;
      --muted: #9fb0d1;
      --accent: #7dd3fc;
      --ok: #0f766e;
    }

    * {
      box-sizing: border-box
    }

    body {
      margin: 0;
      font-family: Inter, Arial, sans-serif;
      background: var(--bg);
      color: var(--text);
    }

    header {
      padding: 24px 28px;
      border-bottom: 1px solid #23304f;
      background: #10172a;
      position: sticky;
      top: 0;
    }

    h1 {
      margin: 0;
      font-size: 28px
    }

    .sub {
      color: var(--muted);
      margin-top: 8px
    }

    .tag {
      display: inline-block;
      padding: 6px 10px;
      border-radius: 999px;
      background: var(--ok);
      font-size: 13px;
    }

    .wrap {
      padding: 24px;
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(420px, 1fr));
      gap: 20px;
    }

    .card {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 18px;
      padding: 20px;
      box-shadow: 0 8px 28px rgba(0, 0, 0, .25);
    }

    h2 {
      margin: 0 0 12px;
      font-size: 20px
    }

    .kpi {
      font-size: 44px;
      font-weight: 800;
      color: var(--accent);
      line-height: 1;
    }

    .sub2 {
      color: var(--muted);
      margin: 10px 0 16px;
    }

    table {
      width: 100%;
      border-collapse: collapse;
    }

    th,
    td {
      padding: 10px 0;
      border-bottom: 1px solid #22314d;
      font-size: 14px;
      text-align: left;
    }

    th:last-child,
    td:last-child {
      text-align: right;
    }

    @media (max-width:700px) {
      .wrap {
        grid-template-columns: 1fr
      }

      .kpi {
        font-size: 36px
      }
    }
  </style>
</head>

<body>
  <header>
    <h1>Система оценки надежности сетевого оборудования</h1>
    <div class="sub">
      SNMP решение
      <span class="tag">Standard MIB</span>
    </div>
  </header>

  <div class="wrap">
    <section class="card">
      <h2>Блок деградации</h2>
      <div class="kpi" id="deg">-</div>
      <div class="sub2">Интегральный индекс деградации</div>
      <table>
        <thead>
          <tr>
            <th>Метрика</th>
            <th>Аномалия</th>
            <th>Вес</th>
            <th>Вклад</th>
          </tr>
        </thead>
        <tbody id="degTable"></tbody>
      </table>
    </section>

    <section class="card">
      <h2>Блок надежности</h2>
      <div class="kpi" id="fp">-</div>
      <div class="sub2">Вероятность отказа на горизонте 1 час</div>
      <table>
        <tbody id="relTable"></tbody>
      </table>
    </section>
  </div>

  <script>
    function fmt(v, n) {
      var x = Number(v);
      if (Number.isFinite(x)) return x.toFixed(n);
      return '-';
    }

    async function load() {
      var resp = await fetch('/api/state');
      var s = await resp.json();

      document.getElementById('deg').textContent =
        fmt(s.degradation_index || 0, 4);

      document.getElementById('fp').textContent =
        fmt((s.failure_probability_1h || 0) * 100, 4) + ' %';

      var anomaly = s.anomaly || {};
      var weights = s.weights || {};

      var rows = Object.keys(anomaly).map(function (k) {
        var a = Number(anomaly[k] || 0);
        var w = Number(weights[k] || 0);
        return {
          metric: k,
          anomaly: a,
          weight: w,
          contribution: a * w
        };
      });

      rows.sort(function (a, b) {
        return b.contribution - a.contribution;
      });

      var degTable = document.getElementById('degTable');
      degTable.innerHTML = '';

      rows.forEach(function (r) {
        degTable.innerHTML +=
          '<tr>' +
          '<td>' + r.metric + '</td>' +
          '<td>' + fmt(r.anomaly, 5) + '</td>' +
          '<td>' + fmt(r.weight, 4) + '</td>' +
          '<td>' + fmt(r.contribution, 5) + '</td>' +
          '</tr>';
      });

      var relTable = document.getElementById('relTable');
      relTable.innerHTML = '';

      var relRows = [
        ['MTBF, ч', s.mtbf_hours],
        ['MTTR, ч', s.mttr_hours],
        ['Availability', s.availability],
      ];

      relRows.forEach(function (item) {
        relTable.innerHTML +=
          '<tr>' +
          '<td>' + item[0] + '</td>' +
          '<td>' + fmt(item[1], 6) + '</td>' +
          '</tr>';
      });
    }

    load();
    setInterval(load, 5000);
  </script>
</body>

</html>`

func main() {
	mrand.Seed(time.Now().UnixNano())

	cfg := loadFeatureConfig(getenv("FEATURE_CONFIG", "/app/config/features.json"))
	state.Weights = map[string]float64{}
	for _, f := range cfg.Features {
		state.Weights[f.Name] = f.Weight
	}

	prometheus.MustRegister(metricDeg, metricFailure, metricMTBF, metricMTTR, metricHazard, metricAvail)
	go pollLoop()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		state.RLock()
		defer state.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, uiHTML)
	})

	log.Println("main-service listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
