package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type PersistedState struct {
	ObsStart   time.Time `json:"obsStart"`
	State      string    `json:"state"`
	LastChange time.Time `json:"lastChange"`

	Failures   int     `json:"failures"`
	Repairs    int     `json:"repairs"`
	UpSumSec   float64 `json:"upSumSec"`
	DownSumSec float64 `json:"downSumSec"`

	Events []Event `json:"events"`
}

type Collector struct {
	cfg Config
	mu  sync.RWMutex

	obsStart   time.Time
	state      string
	lastChange time.Time
	failures   int
	repairs    int
	upSumSec   float64
	downSumSec float64
	events     []Event

	health Health

	lastHealthTS time.Time
	lastFullTS   time.Time

	system       SystemInfo
	optional     OptionalMetrics
	availability Availability
	ifaces       []Iface

	prevIfaces        []Iface
	prevFullUptimeSec uint64
	prevFullTS        time.Time

	models  *ModelBank
	anomaly Anomaly
	signals Signals

	lastSnapshot *Snapshot

	downCandSince time.Time
	upCandSince   time.Time

	healthRunning atomic.Bool
	fullRunning   atomic.Bool

	lastGoodForecast Forecast
	hasGoodForecast  bool
}

func NewCollector(cfg Config) *Collector {
	now := time.Now()
	c := &Collector{
		cfg:        cfg,
		obsStart:   now,
		state:      "UNKNOWN",
		lastChange: now,
		models:     NewModelBank(cfg),
		anomaly:    Anomaly{ByKey: map[string]AnomKey{}},
	}
	_ = c.loadSnapshotFile()
	_ = c.loadStateFile()
	c.mu.Lock()
	if c.lastSnapshot == nil {
		c.recomputeSnapshotLocked(time.Now())
	}
	c.mu.Unlock()
	return c
}

func (c *Collector) Run(ctxDone <-chan struct{}) {
	healthTicker := time.NewTicker(c.cfg.HealthInterval)
	fullTicker := time.NewTicker(c.cfg.FullInterval)
	saveTicker := time.NewTicker(c.cfg.SaveEvery)
	defer healthTicker.Stop()
	defer fullTicker.Stop()
	defer saveTicker.Stop()

	go c.healthPoll()
	go c.fullPoll()

	for {
		select {
		case <-ctxDone:
			return
		case <-healthTicker.C:
			go c.healthPoll()
		case <-fullTicker.C:
			go c.fullPoll()
		case <-saveTicker.C:
			c.mu.Lock()
			_ = c.saveStateLocked()
			_ = c.saveSnapshotLocked()
			c.mu.Unlock()
		}
	}
}

func (c *Collector) healthPoll() {
	if !c.healthRunning.CompareAndSwap(false, true) {
		return
	}
	defer c.healthRunning.Store(false)

	now := time.Now()
	c.mu.Lock()
	c.health.LastAttempt = now
	c.mu.Unlock()

	snmp := newSNMP(c.cfg)
	if err := snmp.Connect(); err != nil {
		c.applyHealthFail(now, fmt.Errorf("snmp connect: %w", err))
		return
	}
	defer func() { _ = snmp.Conn.Close() }()

	res, err := snmp.Get([]string{oidSysUpTime, oidSysName})
	if err != nil || res == nil {
		c.applyHealthFail(now, fmt.Errorf("snmp get health: %w", err))
		return
	}

	var gotUptime bool
	var gotName bool
	var upSec uint64
	var sysName string
	for _, v := range res.Variables {
		if isNoSuchType(v.Type) {
			continue
		}
		switch trimDot(v.Name) {
		case oidSysUpTime:
			upSec = toUint64(v.Value) / 100
			gotUptime = true
		case oidSysName:
			sysName = toString(v.Value)
			gotName = true
		}
	}
	if !gotUptime {
		c.applyHealthFail(now, fmt.Errorf("health missing sysUpTime"))
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.health.SNMPOk = true
	c.health.LastSuccess = now
	c.lastHealthTS = now
	c.health.ConsecutiveOK++
	c.health.ConsecutiveFail = 0
	c.health.LastError = ""

	c.system.UptimeSec = upSec
	if gotName {
		c.system.SysName = sysName
	}

	if c.upCandSince.IsZero() {
		c.upCandSince = now
	}
	c.downCandSince = time.Time{}

	if c.state == "DOWN" {
		if c.health.ConsecutiveOK >= c.cfg.UpAfterOK && now.Sub(c.upCandSince) >= c.cfg.UpHold {
			c.transitionLocked(now, "UP", "snmp_ok", "")
		}
	} else if c.state == "UNKNOWN" {
		c.transitionLocked(now, "UP", "init_ok", "")
	}

	c.recomputeSnapshotLocked(now)
	_ = c.saveSnapshotLocked()
}

func (c *Collector) applyHealthFail(now time.Time, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.health.SNMPOk = false
	c.health.ConsecutiveFail++
	c.health.ConsecutiveOK = 0
	c.health.LastError = err.Error()

	if c.downCandSince.IsZero() {
		c.downCandSince = now
	}
	c.upCandSince = time.Time{}

	if c.health.ConsecutiveFail >= c.cfg.DownAfterFails && now.Sub(c.downCandSince) >= c.cfg.DownHold {
		c.transitionLocked(now, "DOWN", "snmp_fail", err.Error())
	}

	c.recomputeSnapshotLocked(now)
	_ = c.saveSnapshotLocked()
}

func (c *Collector) fullPoll() {
	if !c.fullRunning.CompareAndSwap(false, true) {
		return
	}
	defer c.fullRunning.Store(false)

	c.mu.RLock()
	st := c.state
	c.mu.RUnlock()
	if st != "UP" {
		return
	}

	fd, err := collectFull(c.cfg)
	if err != nil {
		return
	}
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastFullTS = now
	c.system = fd.System
	c.optional = fd.Optional
	c.availability = fd.Availability
	c.ifaces = fd.Interfaces

	if !c.prevFullTS.IsZero() && c.system.UptimeSec < c.prevFullUptimeSec {
		c.prevIfaces = nil
		c.prevFullTS = time.Time{}
		c.prevFullUptimeSec = 0
	}

	if len(c.prevIfaces) > 0 && !c.prevFullTS.IsZero() {
		dt := now.Sub(c.prevFullTS).Seconds()
		if dt > 0.1 && dt < 3600 {
			deriveRates(&c.ifaces, c.prevIfaces, dt, c.availability)
		}
	}

	c.prevIfaces = cloneIfaces(c.ifaces)
	c.prevFullTS = now
	c.prevFullUptimeSec = c.system.UptimeSec

	c.signals = aggregateSignals(c.ifaces, c.optional)
	c.anomaly = c.updateAnomalyLocked(c.signals)

	c.recomputeSnapshotLocked(now)
	_ = c.saveSnapshotLocked()
}

func (c *Collector) updateAnomalyLocked(sig Signals) Anomaly {
	out := Anomaly{ByKey: map[string]AnomKey{}}
	if c.state != "UP" {
		out.Score = 0
		out.ByKey["note"] = AnomKey{Available: false, Note: "no fresh data"}
		return out
	}

	type kv struct {
		key string
		val float64
		ok  bool
	}
	items := []kv{
		{"linkDownRatio", sig.LinkDownRatio, true},
		{"ifErrRatePps", sig.IfErrRatePps, true},
		{"ifDiscRatePps", sig.IfDiscRatePps, true},
		{"trafficInBps", sig.TrafficInBps, true},
		{"trafficOutBps", sig.TrafficOutBps, true},
	}
	if sig.CPU5minPct != nil {
		items = append(items, kv{"cpu5minPct", *sig.CPU5minPct, true})
	} else {
		out.ByKey["cpu5minPct"] = AnomKey{Available: false, Note: "not available"}
	}
	if sig.TempC != nil {
		items = append(items, kv{"tempC", *sig.TempC, true})
	} else {
		out.ByKey["tempC"] = AnomKey{Available: false, Note: "not available"}
	}

	sumW, sum := 0.0, 0.0
	for _, it := range items {
		exp, z, sc, modelName, n := c.models.Update(it.key, it.val)
		out.ByKey[it.key] = AnomKey{
			Available: it.ok,
			Value:     it.val,
			Expected:  exp,
			Z:         z,
			Score:     sc,
			Model:     modelName,
			Samples:   n,
		}
		w := c.models.weights[it.key]
		sumW += w
		sum += w * sc
	}
	if sumW > 0 {
		out.Score = clamp01(sum / sumW)
	}
	return out
}

func (c *Collector) transitionLocked(now time.Time, newState, reason, errStr string) {
	if c.state == newState {
		return
	}
	seg := now.Sub(c.lastChange).Seconds()
	if seg < 0 {
		seg = 0
	}
	switch c.state {
	case "UP":
		c.upSumSec += seg
	case "DOWN":
		c.downSumSec += seg
	}

	c.events = append(c.events, Event{
		At: now, From: c.state, To: newState, PrevDurSec: seg, Reason: reason, Error: errStr,
	})
	if c.cfg.MaxEventLog > 0 && len(c.events) > c.cfg.MaxEventLog {
		c.events = c.events[len(c.events)-c.cfg.MaxEventLog:]
	}

	if c.state != "UNKNOWN" && newState != "UNKNOWN" {
		if c.state == "UP" && newState == "DOWN" {
			c.failures++
		}
		if c.state == "DOWN" && newState == "UP" {
			c.repairs++
		}
	}

	c.state = newState
	c.lastChange = now
}

func (c *Collector) uiStateLocked(now time.Time) string {
	age := ageSec(now, c.health.LastSuccess)
	if c.state == "UP" && age > c.cfg.StaleAfter.Seconds() {
		return "STALE"
	}
	return c.state
}

func (c *Collector) statsLocked(now time.Time) Stats {
	u := c.upSumSec
	d := c.downSumSec
	seg := now.Sub(c.lastChange).Seconds()
	if seg < 0 {
		seg = 0
	}
	switch c.state {
	case "UP":
		u += seg
	case "DOWN":
		d += seg
	}

	mttf := 0.0
	if c.failures > 0 {
		mttf = u / float64(c.failures)
	}
	mttr := 0.0
	if c.repairs > 0 {
		mttr = d / float64(c.repairs)
	}
	availTime := 0.0
	if u+d > 0 {
		availTime = u / (u + d)
	}
	availFit := 0.0
	if mttf+mttr > 0 {
		availFit = mttf / (mttf + mttr)
	}

	return Stats{
		ObservationStart:   c.obsStart,
		Now:                now,
		State:              c.state,
		Failures:           c.failures,
		Repairs:            c.repairs,
		TotalUpSec:         u,
		TotalDownSec:       d,
		MTTFSec:            mttf,
		MTTRSec:            mttr,
		MTBFCycleSec:       mttf + mttr,
		AvailabilityByTime: clamp01(availTime),
		AvailabilityByFit:  clamp01(availFit),
	}
}

func (c *Collector) computeRiskLocked(now time.Time) RiskNow {
	ui := c.uiStateLocked(now)
	if c.cfg.ForceDownRisk && (ui == "DOWN" || ui == "STALE") {
		return RiskNow{Score: 1, ProbNow: 1, Reason: "telemetry lost"}
	}

	score := 0.0
	w := 0.0
	score += 1.8 * clamp01(c.anomaly.Score)
	w += 1.8

	thrHit := 0.0
	thrW := 0.0

	if c.signals.CPU5minPct != nil {
		if *c.signals.CPU5minPct >= c.cfg.ThreshCPUHighPct {
			thrHit += 1
		}
		thrW += 1
	}

	if c.signals.LinkDownRatio >= c.cfg.ThreshLinkDownRatio {
		thrHit += 1
	}
	thrW += 1

	if c.signals.IfErrRatePps >= c.cfg.ThreshErrRatePps {
		thrHit += 1
	}
	thrW += 1

	if c.signals.IfDiscRatePps >= c.cfg.ThreshDiscRatePps {
		thrHit += 1
	}
	thrW += 1

	thrScore := 0.0
	if thrW > 0 {
		thrScore = thrHit / thrW
	}
	score += 1.0 * clamp01(thrScore)
	w += 1.0

	u := float64(c.system.UptimeSec)
	reboot := 0.0
	if u < 300 {
		reboot = (300 - u) / 300
	}
	score += 0.6 * clamp01(reboot)
	w += 0.6

	if w > 0 {
		score /= w
	}
	prob := 1.0 / (1.0 + math.Exp(-6.0*(clamp01(score)-0.5)))
	return RiskNow{Score: clamp01(score), ProbNow: clamp01(prob)}
}

func (c *Collector) computeForecastLocked(now time.Time, risk RiskNow) Forecast {
	h := c.cfg.ForecastHorizon.Seconds()
	if h <= 0 {
		return Forecast{Available: false, Frozen: false, Note: "disabled"}
	}

	ui := c.uiStateLocked(now)
	if ui == "DOWN" || ui == "STALE" || !c.health.SNMPOk {
		if c.hasGoodForecast {
			f := c.lastGoodForecast
			f.Available = false
			f.Frozen = true
			f.Note = "telemetry lost; forecast frozen (not updated)"
			return f
		}
		return Forecast{
			Available:          false,
			Frozen:             false,
			Note:               "telemetry lost; no baseline forecast yet",
			HorizonSec:         h,
			ProbFailureHorizon: 0,
			LambdaPerSec:       0,
			LambdaEffPerSec:    0,
		}
	}

	stats := c.statsLocked(now)
	lambda := c.cfg.LambdaPrior
	comment := "prior"
	if stats.MTTFSec > 0 {
		lambda = 1.0 / stats.MTTFSec
		comment = "from MTTF"
	}
	if lambda < 0 || math.IsNaN(lambda) || math.IsInf(lambda, 0) {
		lambda = c.cfg.LambdaPrior
		comment = "fallback prior"
	}

	amp := 1.0 + c.cfg.ForecastAlpha*clamp01(c.anomaly.Score)
	amp *= 1.0 + 0.6*clamp01(risk.Score)
	lambdaEff := lambda * amp
	if math.IsNaN(lambdaEff) || math.IsInf(lambdaEff, 0) || lambdaEff < 0 {
		lambdaEff = lambda
	}

	p := 1.0 - math.Exp(-lambdaEff*h)

	out := Forecast{
		Available:          true,
		Frozen:             false,
		Note:               "",
		HorizonSec:         h,
		LambdaPerSec:       lambda,
		LambdaEffPerSec:    lambdaEff,
		ProbFailureHorizon: clamp01(p),
		Comment:            comment,
	}
	c.lastGoodForecast = out
	c.hasGoodForecast = true
	return out
}

func (c *Collector) recomputeSnapshotLocked(now time.Time) {
	ui := c.uiStateLocked(now)

	c.health.State = ui
	c.health.AgeSec = ageSec(now, c.health.LastSuccess)
	c.health.Stale = c.health.AgeSec > c.cfg.StaleAfter.Seconds()

	stats := c.statsLocked(now)
	risk := c.computeRiskLocked(now)
	fc := c.computeForecastLocked(now, risk)

	healthAge := ageSec(now, c.lastHealthTS)
	fullAge := ageSec(now, c.lastFullTS)
	healthFresh := c.lastHealthTS.After(time.Time{}) && healthAge <= c.cfg.StaleAfter.Seconds()
	fullFresh := c.lastFullTS.After(time.Time{}) && fullAge <= c.cfg.StaleAfter.Seconds()

	s := &Snapshot{
		Timestamp:    now,
		Device:       DeviceInfo{Target: c.cfg.Target, Port: c.cfg.Port},
		System:       c.system,
		Optional:     c.optional,
		Health:       c.health,
		Stats:        stats,
		Availability: c.availability,
		Fresh: Freshness{
			HealthFresh:  healthFresh,
			HealthAgeSec: healthAge,
			FullFresh:    fullFresh,
			FullAgeSec:   fullAge,
		},
		Signals:    c.signals,
		Anomaly:    c.anomaly,
		Risk:       risk,
		Forecast:   fc,
		Events:     append([]Event(nil), c.events...),
		Interfaces: append([]Iface(nil), c.ifaces...),
	}
	c.lastSnapshot = s
}

func (c *Collector) GetSnapshot() *Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.lastSnapshot == nil {
		return &Snapshot{
			Timestamp: time.Now(),
			Device:    DeviceInfo{Target: c.cfg.Target, Port: c.cfg.Port},
			Health:    Health{State: "UNKNOWN"},
			Anomaly:   Anomaly{ByKey: map[string]AnomKey{}},
			Forecast:  Forecast{Available: false, Frozen: false, Note: "no data yet"},
		}
	}
	cp := *c.lastSnapshot
	cp.Events = append([]Event(nil), c.lastSnapshot.Events...)
	cp.Interfaces = append([]Iface(nil), c.lastSnapshot.Interfaces...)
	return &cp
}

func (c *Collector) ResetAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.obsStart = now
	c.state = "UNKNOWN"
	c.lastChange = now
	c.failures = 0
	c.repairs = 0
	c.upSumSec = 0
	c.downSumSec = 0
	c.events = nil

	c.health = Health{State: "UNKNOWN"}
	c.downCandSince = time.Time{}
	c.upCandSince = time.Time{}

	c.system = SystemInfo{}
	c.optional = OptionalMetrics{}
	c.availability = Availability{}
	c.ifaces = nil

	c.prevIfaces = nil
	c.prevFullTS = time.Time{}
	c.prevFullUptimeSec = 0

	c.lastHealthTS = time.Time{}
	c.lastFullTS = time.Time{}

	c.models.Reset()
	c.anomaly = Anomaly{ByKey: map[string]AnomKey{}}
	c.signals = Signals{}

	c.hasGoodForecast = false
	c.lastGoodForecast = Forecast{}

	c.recomputeSnapshotLocked(now)
	_ = c.saveStateLocked()
	_ = c.saveSnapshotLocked()
}

func (c *Collector) loadStateFile() error {
	if c.cfg.StateFile == "" {
		return nil
	}
	b, err := os.ReadFile(c.cfg.StateFile)
	if err != nil {
		return nil
	}
	var ps PersistedState
	if err := json.Unmarshal(b, &ps); err != nil {
		return err
	}
	if ps.ObsStart.IsZero() {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.obsStart = ps.ObsStart
	c.state = ps.State
	c.lastChange = ps.LastChange
	c.failures = ps.Failures
	c.repairs = ps.Repairs
	c.upSumSec = ps.UpSumSec
	c.downSumSec = ps.DownSumSec
	c.events = append([]Event(nil), ps.Events...)
	c.recomputeSnapshotLocked(time.Now())
	return nil
}

func (c *Collector) saveStateLocked() error {
	if c.cfg.StateFile == "" {
		return nil
	}
	ps := PersistedState{
		ObsStart:   c.obsStart,
		State:      c.state,
		LastChange: c.lastChange,
		Failures:   c.failures,
		Repairs:    c.repairs,
		UpSumSec:   c.upSumSec,
		DownSumSec: c.downSumSec,
		Events:     append([]Event(nil), c.events...),
	}
	b, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(c.cfg.StateFile, b, 0o644)
}

func (c *Collector) loadSnapshotFile() error {
	if c.cfg.SnapshotFile == "" {
		return nil
	}
	b, err := os.ReadFile(c.cfg.SnapshotFile)
	if err != nil {
		return nil
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSnapshot = &s
	return nil
}

func (c *Collector) saveSnapshotLocked() error {
	if c.cfg.SnapshotFile == "" || c.lastSnapshot == nil {
		return nil
	}
	b, err := json.MarshalIndent(c.lastSnapshot, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(c.cfg.SnapshotFile, b, 0o644)
}
