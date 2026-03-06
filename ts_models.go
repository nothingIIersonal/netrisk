package main

import "math"

type TSModel interface {
	Update(x float64) (expected float64, z float64, score float64)
	Samples() int
	Name() string
	Reset()
}

type EWMAZ struct {
	alpha, varAlpha, zScale float64
	init                    bool
	mean, varE              float64
	n                       int
}

func NewEWMAZ(alpha, varAlpha, zScale float64) *EWMAZ {
	return &EWMAZ{alpha: alpha, varAlpha: varAlpha, zScale: zScale}
}
func (m *EWMAZ) Name() string { return "EWMA-Z" }
func (m *EWMAZ) Samples() int { return m.n }
func (m *EWMAZ) Reset()       { *m = *NewEWMAZ(m.alpha, m.varAlpha, m.zScale) }
func (m *EWMAZ) Update(x float64) (expected, z, score float64) {
	if !m.init {
		m.init = true
		m.mean = x
		m.varE = 1e-6
		m.n = 1
		return x, 0, 0
	}
	expected = m.mean
	err := x - expected
	m.mean = m.alpha*x + (1-m.alpha)*m.mean
	m.varE = m.varAlpha*(err*err) + (1-m.varAlpha)*m.varE
	if m.varE < 1e-12 {
		m.varE = 1e-12
	}
	z = err / math.Sqrt(m.varE)
	m.n++
	score = 1 - math.Exp(-math.Abs(z)/m.zScale)
	return expected, z, clamp01(score)
}

type HoltWintersAdd struct {
	alpha, beta, gamma float64
	seasonLen          int
	varAlpha           float64
	zScale             float64

	init         bool
	n            int
	level, trend float64
	season       []float64
	pos          int
	varE         float64
}

func NewHoltWintersAdd(alpha, beta, gamma float64, seasonLen int, varAlpha, zScale float64) *HoltWintersAdd {
	if seasonLen < 2 {
		seasonLen = 2
	}
	return &HoltWintersAdd{
		alpha: alpha, beta: beta, gamma: gamma,
		seasonLen: seasonLen,
		varAlpha:  varAlpha,
		zScale:    zScale,
		season:    make([]float64, seasonLen),
	}
}
func (m *HoltWintersAdd) Name() string { return "Holt-Winters(add)" }
func (m *HoltWintersAdd) Samples() int { return m.n }
func (m *HoltWintersAdd) Reset() {
	*m = *NewHoltWintersAdd(m.alpha, m.beta, m.gamma, m.seasonLen, m.varAlpha, m.zScale)
}

func (m *HoltWintersAdd) Update(x float64) (expected, z, score float64) {
	if !m.init {
		m.init = true
		m.level = x
		m.trend = 0
		for i := range m.season {
			m.season[i] = 0
		}
		m.varE = 1e-6
		m.n = 1
		return x, 0, 0
	}
	si := m.season[m.pos]
	expected = m.level + m.trend + si
	err := x - expected

	prevLevel := m.level
	m.level = m.alpha*(x-si) + (1-m.alpha)*(m.level+m.trend)
	m.trend = m.beta*(m.level-prevLevel) + (1-m.beta)*m.trend
	m.season[m.pos] = m.gamma*(x-m.level) + (1-m.gamma)*si
	m.pos = (m.pos + 1) % m.seasonLen

	m.varE = m.varAlpha*(err*err) + (1-m.varAlpha)*m.varE
	if m.varE < 1e-12 {
		m.varE = 1e-12
	}
	z = err / math.Sqrt(m.varE)
	m.n++
	score = 1 - math.Exp(-math.Abs(z)/m.zScale)
	return expected, z, clamp01(score)
}

type ModelBank struct {
	minSamples int
	models     map[string]TSModel
	weights    map[string]float64
}

func NewModelBank(cfg Config) *ModelBank {
	mb := &ModelBank{
		minSamples: cfg.MinSamplesToScore,
		models:     map[string]TSModel{},
		weights:    map[string]float64{},
	}
	mb.models["linkDownRatio"] = NewHoltWintersAdd(cfg.HWAlpha, cfg.HWBeta, cfg.HWGamma, cfg.HWSeasonLen, cfg.EWMAVarAlpha, cfg.ZToScoreScale)
	mb.models["ifErrRatePps"] = NewHoltWintersAdd(cfg.HWAlpha, cfg.HWBeta, cfg.HWGamma, cfg.HWSeasonLen, cfg.EWMAVarAlpha, cfg.ZToScoreScale)
	mb.models["ifDiscRatePps"] = NewHoltWintersAdd(cfg.HWAlpha, cfg.HWBeta, cfg.HWGamma, cfg.HWSeasonLen, cfg.EWMAVarAlpha, cfg.ZToScoreScale)
	mb.models["trafficInBps"] = NewHoltWintersAdd(cfg.HWAlpha, cfg.HWBeta, cfg.HWGamma, cfg.HWSeasonLen, cfg.EWMAVarAlpha, cfg.ZToScoreScale)
	mb.models["trafficOutBps"] = NewHoltWintersAdd(cfg.HWAlpha, cfg.HWBeta, cfg.HWGamma, cfg.HWSeasonLen, cfg.EWMAVarAlpha, cfg.ZToScoreScale)
	mb.models["cpu5minPct"] = NewEWMAZ(cfg.EWMAAlpha, cfg.EWMAVarAlpha, cfg.ZToScoreScale)
	mb.models["tempC"] = NewEWMAZ(cfg.EWMAAlpha, cfg.EWMAVarAlpha, cfg.ZToScoreScale)

	mb.weights["linkDownRatio"] = 1.4
	mb.weights["ifErrRatePps"] = 1.2
	mb.weights["ifDiscRatePps"] = 0.9
	mb.weights["trafficInBps"] = 0.6
	mb.weights["trafficOutBps"] = 0.6
	mb.weights["cpu5minPct"] = 0.8
	mb.weights["tempC"] = 0.7
	return mb
}

func (mb *ModelBank) Reset() {
	for _, m := range mb.models {
		m.Reset()
	}
}

func (mb *ModelBank) Update(key string, x float64) (expected, z, score float64, modelName string, samples int) {
	m, ok := mb.models[key]
	if !ok {
		return 0, 0, 0, "none", 0
	}
	exp, zz, sc := m.Update(x)
	n := m.Samples()
	if n < mb.minSamples {
		return exp, zz, 0, m.Name(), n
	}
	return exp, zz, sc, m.Name(), n
}
