package main

import "time"

type Config struct {
	Target    string
	Port      uint16
	Community string
	Version   string
	Listen    string

	HealthInterval time.Duration
	FullInterval   time.Duration
	Timeout        time.Duration
	Retries        int

	DownAfterFails int
	DownHold       time.Duration
	UpAfterOK      int
	UpHold         time.Duration
	StaleAfter     time.Duration

	StateFile    string
	SnapshotFile string
	MaxEventLog  int
	SaveEvery    time.Duration

	ForceDownRisk bool

	ForecastHorizon time.Duration
	ForecastAlpha   float64
	LambdaPrior     float64

	MinSamplesToScore int
	EWMAAlpha         float64
	EWMAVarAlpha      float64
	ZToScoreScale     float64
	HWAlpha           float64
	HWBeta            float64
	HWGamma           float64
	HWSeasonLen       int

	ThreshCPUHighPct    float64
	ThreshLinkDownRatio float64
	ThreshErrRatePps    float64
	ThreshDiscRatePps   float64
}
