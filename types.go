package main

import "time"

type Snapshot struct {
	Timestamp time.Time `json:"timestamp"`

	Device DeviceInfo `json:"device"`
	System SystemInfo `json:"system"`

	Optional OptionalMetrics `json:"optional"`

	Health Health `json:"health"`
	Stats  Stats  `json:"stats"`

	Availability Availability `json:"availability"`
	Fresh        Freshness    `json:"fresh"`

	Signals  Signals  `json:"signals"`
	Anomaly  Anomaly  `json:"anomaly"`
	Risk     RiskNow  `json:"risk"`
	Forecast Forecast `json:"forecast"`

	Events     []Event `json:"events"`
	Interfaces []Iface `json:"interfaces"`
}

type DeviceInfo struct {
	Target string `json:"target"`
	Port   uint16 `json:"port"`
}

type SystemInfo struct {
	SysName   string `json:"sysName"`
	SysDescr  string `json:"sysDescr"`
	UptimeSec uint64 `json:"uptimeSec"`
}

type OptionalMetrics struct {
	CPU5minPct *int64 `json:"cpu5minPct,omitempty"`
	MemUsedKB  *int64 `json:"memUsedKB,omitempty"`
	MemFreeKB  *int64 `json:"memFreeKB,omitempty"`
	TempC      *int64 `json:"tempC,omitempty"`
}

type Availability struct {
	SysUpTime bool `json:"sysUpTime"`
	SysName   bool `json:"sysName"`
	SysDescr  bool `json:"sysDescr"`

	CPU5min     bool `json:"cpu5min"`
	MemoryPool  bool `json:"memoryPool"`
	Temperature bool `json:"temperature"`

	IfDescr       bool `json:"ifDescr"`
	IfAlias       bool `json:"ifAlias"`
	IfType        bool `json:"ifType"`
	IfMTU         bool `json:"ifMtu"`
	IfSpeed       bool `json:"ifSpeed"`
	IfAdminStatus bool `json:"ifAdminStatus"`
	IfOperStatus  bool `json:"ifOperStatus"`

	IfHCOctets  bool `json:"ifHCOctets"`
	IfErrors    bool `json:"ifErrors"`
	IfDiscards  bool `json:"ifDiscards"`
	IfUcastPkts bool `json:"ifUcastPkts"`
}

type Freshness struct {
	HealthFresh  bool    `json:"healthFresh"`
	HealthAgeSec float64 `json:"healthAgeSec"`

	FullFresh  bool    `json:"fullFresh"`
	FullAgeSec float64 `json:"fullAgeSec"`
}

type Health struct {
	SNMPOk          bool      `json:"snmpOk"`
	State           string    `json:"state"`
	LastAttempt     time.Time `json:"lastAttempt"`
	LastSuccess     time.Time `json:"lastSuccess"`
	ConsecutiveFail int       `json:"consecutiveFail"`
	ConsecutiveOK   int       `json:"consecutiveOk"`
	LastError       string    `json:"lastError,omitempty"`
	AgeSec          float64   `json:"ageSec"`
	Stale           bool      `json:"stale"`
}

type Event struct {
	At         time.Time `json:"at"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	PrevDurSec float64   `json:"prevDurSec"`
	Reason     string    `json:"reason,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type Stats struct {
	ObservationStart time.Time `json:"observationStart"`
	Now              time.Time `json:"now"`
	State            string    `json:"state"`

	Failures int `json:"failures"`
	Repairs  int `json:"repairs"`

	TotalUpSec   float64 `json:"totalUpSec"`
	TotalDownSec float64 `json:"totalDownSec"`

	MTTFSec      float64 `json:"mttfSec"`
	MTTRSec      float64 `json:"mttrSec"`
	MTBFCycleSec float64 `json:"mtbfCycleSec"`

	AvailabilityByTime float64 `json:"availabilityByTime"`
	AvailabilityByFit  float64 `json:"availabilityByFit"`
}

type Signals struct {
	LinkDownRatio float64  `json:"linkDownRatio"`
	IfErrRatePps  float64  `json:"ifErrRatePps"`
	IfDiscRatePps float64  `json:"ifDiscRatePps"`
	TrafficInBps  float64  `json:"trafficInBps"`
	TrafficOutBps float64  `json:"trafficOutBps"`
	CPU5minPct    *float64 `json:"cpu5minPct,omitempty"`
	TempC         *float64 `json:"tempC,omitempty"`
}

type Anomaly struct {
	Score float64            `json:"score"`
	ByKey map[string]AnomKey `json:"byKey"`
}

type AnomKey struct {
	Available bool    `json:"available"`
	Value     float64 `json:"value"`
	Expected  float64 `json:"expected"`
	Z         float64 `json:"z"`
	Score     float64 `json:"score"`
	Model     string  `json:"model"`
	Samples   int     `json:"samples"`
	Note      string  `json:"note,omitempty"`
}

type RiskNow struct {
	Score   float64 `json:"score"`
	ProbNow float64 `json:"probNow"`
	Reason  string  `json:"reason,omitempty"`
}

type Forecast struct {
	Available bool   `json:"available"`
	Frozen    bool   `json:"frozen"`
	Note      string `json:"note,omitempty"`

	HorizonSec         float64 `json:"horizonSec"`
	LambdaPerSec       float64 `json:"lambdaPerSec"`
	LambdaEffPerSec    float64 `json:"lambdaEffPerSec"`
	ProbFailureHorizon float64 `json:"probFailureHorizon"`
	Comment            string  `json:"comment,omitempty"`
}

type Iface struct {
	IfIndex int `json:"ifIndex"`

	IfName  string `json:"ifName,omitempty"`
	IfAlias string `json:"ifAlias,omitempty"`

	IfType        int64 `json:"ifType,omitempty"`
	IfMTU         int64 `json:"ifMtu,omitempty"`
	IfSpeedBps    int64 `json:"ifSpeedBps,omitempty"`
	IfAdminStatus int64 `json:"ifAdminStatus,omitempty"`
	IfOperStatus  int64 `json:"ifOperStatus,omitempty"`

	InOctets     uint64 `json:"inOctets,omitempty"`
	OutOctets    uint64 `json:"outOctets,omitempty"`
	InErrors     uint64 `json:"inErrors,omitempty"`
	OutErrors    uint64 `json:"outErrors,omitempty"`
	InDiscards   uint64 `json:"inDiscards,omitempty"`
	OutDiscards  uint64 `json:"outDiscards,omitempty"`
	InUcastPkts  uint64 `json:"inUcastPkts,omitempty"`
	OutUcastPkts uint64 `json:"outUcastPkts,omitempty"`

	InBps        float64 `json:"inBps,omitempty"`
	OutBps       float64 `json:"outBps,omitempty"`
	ErrRatePps   float64 `json:"errRatePps,omitempty"`
	DiscRatePps  float64 `json:"discRatePps,omitempty"`
	UcastRatePps float64 `json:"ucastRatePps,omitempty"`
}
