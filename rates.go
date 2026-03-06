package main

// deriveRates считает скорости по интерфейсам на основе разности счётчиков между двумя full-poll.
func deriveRates(cur *[]Iface, prev []Iface, dt float64, avail Availability) {
	if dt <= 0 {
		return
	}

	prevByIdx := make(map[int]Iface, len(prev))
	for _, p := range prev {
		prevByIdx[p.IfIndex] = p
	}

	for i := range *cur {
		c := &(*cur)[i]
		p, ok := prevByIdx[c.IfIndex]
		if !ok {
			continue
		}
		if avail.IfHCOctets {
			dIn := diffCounter64(c.InOctets, p.InOctets)
			dOut := diffCounter64(c.OutOctets, p.OutOctets)
			c.InBps = float64(dIn*8) / dt
			c.OutBps = float64(dOut*8) / dt
		}
		if avail.IfErrors {
			errDelta := diffCounter32(c.InErrors, p.InErrors) + diffCounter32(c.OutErrors, p.OutErrors)
			c.ErrRatePps = float64(errDelta) / dt
		}
		if avail.IfDiscards {
			discDelta := diffCounter32(c.InDiscards, p.InDiscards) + diffCounter32(c.OutDiscards, p.OutDiscards)
			c.DiscRatePps = float64(discDelta) / dt
		}
		if avail.IfUcastPkts {
			ucDelta := diffCounter32(c.InUcastPkts, p.InUcastPkts) + diffCounter32(c.OutUcastPkts, p.OutUcastPkts)
			c.UcastRatePps = float64(ucDelta) / dt
		}
	}
}

// cloneIfaces делает неглубокую копию слайса интерфейсов.
func cloneIfaces(in []Iface) []Iface {
	out := make([]Iface, len(in))
	copy(out, in)
	return out
}
