package main

func aggregateSignals(ifaces []Iface, opt OptionalMetrics) Signals {
	var adminUpEth, downEth int
	var nActive int
	var sumErr, sumDisc, sumIn, sumOut float64

	for _, it := range ifaces {
		if it.IfType != 6 {
			continue
		}
		if it.IfAdminStatus == 1 {
			adminUpEth++
			if it.IfOperStatus != 1 {
				downEth++
			}
		}
		if it.IfOperStatus == 1 {
			sumErr += it.ErrRatePps
			sumDisc += it.DiscRatePps
			sumIn += it.InBps
			sumOut += it.OutBps
			nActive++
		}
	}

	ratio := 0.0
	if adminUpEth > 0 {
		ratio = float64(downEth) / float64(adminUpEth)
	}
	avgErr := 0.0
	avgDisc := 0.0
	if nActive > 0 {
		avgErr = sumErr / float64(nActive)
		avgDisc = sumDisc / float64(nActive)
	}

	var cpuP *float64
	if opt.CPU5minPct != nil {
		v := float64(*opt.CPU5minPct)
		cpuP = &v
	}
	var tc *float64
	if opt.TempC != nil {
		v := float64(*opt.TempC)
		tc = &v
	}

	return Signals{
		LinkDownRatio: ratio,
		IfErrRatePps:  avgErr,
		IfDiscRatePps: avgDisc,
		TrafficInBps:  sumIn,
		TrafficOutBps: sumOut,
		CPU5minPct:    cpuP,
		TempC:         tc,
	}
}
