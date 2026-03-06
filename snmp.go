package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidSysUpTime = "1.3.6.1.2.1.1.3.0"
	oidSysDescr  = "1.3.6.1.2.1.1.1.0"
	oidSysName   = "1.3.6.1.2.1.1.5.0"

	oidIfDescr       = "1.3.6.1.2.1.2.2.1.2"
	oidIfAlias       = "1.3.6.1.2.1.31.1.1.1.18"
	oidIfType        = "1.3.6.1.2.1.2.2.1.3"
	oidIfMTU         = "1.3.6.1.2.1.2.2.1.4"
	oidIfSpeed       = "1.3.6.1.2.1.2.2.1.5"
	oidIfAdminStatus = "1.3.6.1.2.1.2.2.1.7"
	oidIfOperStatus  = "1.3.6.1.2.1.2.2.1.8"

	oidIfInErrors     = "1.3.6.1.2.1.2.2.1.14"
	oidIfOutErrors    = "1.3.6.1.2.1.2.2.1.20"
	oidIfInDiscards   = "1.3.6.1.2.1.2.2.1.13"
	oidIfOutDiscards  = "1.3.6.1.2.1.2.2.1.19"
	oidIfInUcastPkts  = "1.3.6.1.2.1.2.2.1.11"
	oidIfOutUcastPkts = "1.3.6.1.2.1.2.2.1.17"

	oidIfHCInOctets  = "1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOctets = "1.3.6.1.2.1.31.1.1.1.10"

	oidCpu5min     = "1.3.6.1.4.1.9.9.109.1.1.1.1.5.2"
	oidMemoryUsed  = "1.3.6.1.4.1.9.9.48.1.1.1.5.1"
	oidMemoryFree  = "1.3.6.1.4.1.9.9.48.1.1.1.6.1"
	oidChassisTemp = "1.3.6.1.4.1.9.9.13.1.3.1.3.1"
)

type FullData struct {
	System       SystemInfo
	Optional     OptionalMetrics
	Availability Availability
	Interfaces   []Iface
}

func newSNMP(cfg Config) *gosnmp.GoSNMP {
	v := gosnmp.Version2c
	switch strings.ToLower(cfg.Version) {
	case "1", "v1":
		v = gosnmp.Version1
	case "2c", "v2c":
		v = gosnmp.Version2c
	}
	return &gosnmp.GoSNMP{
		Target:         cfg.Target,
		Port:           cfg.Port,
		Community:      cfg.Community,
		Version:        v,
		Timeout:        cfg.Timeout,
		Retries:        cfg.Retries,
		MaxOids:        60,
		MaxRepetitions: 25,
	}
}

func collectFull(cfg Config) (FullData, error) {
	var out FullData

	snmp := newSNMP(cfg)
	if err := snmp.Connect(); err != nil {
		return out, fmt.Errorf("snmp connect: %w", err)
	}
	defer func() { _ = snmp.Conn.Close() }()

	{
		res, err := snmp.Get([]string{oidSysUpTime, oidSysName, oidSysDescr})
		if err != nil {
			return out, fmt.Errorf("snmp get system: %w", err)
		}
		for _, v := range res.Variables {
			if isNoSuchType(v.Type) {
				continue
			}
			switch trimDot(v.Name) {
			case oidSysUpTime:
				out.System.UptimeSec = toUint64(v.Value) / 100
				out.Availability.SysUpTime = true
			case oidSysName:
				out.System.SysName = toString(v.Value)
				out.Availability.SysName = true
			case oidSysDescr:
				out.System.SysDescr = toString(v.Value)
				out.Availability.SysDescr = true
			}
		}
	}

	if v, ok := tryGetInt64(snmp, oidCpu5min); ok {
		out.Optional.CPU5minPct = &v
		out.Availability.CPU5min = true
	}
	memUsed, okUsed := tryGetInt64(snmp, oidMemoryUsed)
	memFree, okFree := tryGetInt64(snmp, oidMemoryFree)
	if okUsed && okFree {
		out.Optional.MemUsedKB = &memUsed
		out.Optional.MemFreeKB = &memFree
		out.Availability.MemoryPool = true
	}
	if t, ok := tryGetInt64(snmp, oidChassisTemp); ok {
		if t > 200 {
			t = t / 10
		}
		out.Optional.TempC = &t
		out.Availability.Temperature = true
	}

	ifNames, ok := walkStringTable(snmp, oidIfDescr)
	out.Availability.IfDescr = ok
	if !ok || len(ifNames) == 0 {
		return out, fmt.Errorf("ifDescr walk failed or empty")
	}

	ifAlias, aliasOk := walkStringTable(snmp, oidIfAlias)
	out.Availability.IfAlias = aliasOk

	ifType, ifTypeOk := walkIntTable(snmp, oidIfType)
	out.Availability.IfType = ifTypeOk
	ifMtu, ifMtuOk := walkIntTable(snmp, oidIfMTU)
	out.Availability.IfMTU = ifMtuOk
	ifSpeed, ifSpeedOk := walkIntTable(snmp, oidIfSpeed)
	out.Availability.IfSpeed = ifSpeedOk
	ifAdmin, ifAdminOk := walkIntTable(snmp, oidIfAdminStatus)
	out.Availability.IfAdminStatus = ifAdminOk
	ifOper, ifOperOk := walkIntTable(snmp, oidIfOperStatus)
	out.Availability.IfOperStatus = ifOperOk

	inOct, inOctOk := walkCounter64Table(snmp, oidIfHCInOctets)
	outOct, outOctOk := walkCounter64Table(snmp, oidIfHCOutOctets)
	out.Availability.IfHCOctets = inOctOk && outOctOk

	inErr, inErrOk := walkCounter32Table(snmp, oidIfInErrors)
	outErr, outErrOk := walkCounter32Table(snmp, oidIfOutErrors)
	out.Availability.IfErrors = inErrOk && outErrOk

	inDis, inDisOk := walkCounter32Table(snmp, oidIfInDiscards)
	outDis, outDisOk := walkCounter32Table(snmp, oidIfOutDiscards)
	out.Availability.IfDiscards = inDisOk && outDisOk

	inU, inUOk := walkCounter32Table(snmp, oidIfInUcastPkts)
	outU, outUOk := walkCounter32Table(snmp, oidIfOutUcastPkts)
	out.Availability.IfUcastPkts = inUOk && outUOk

	indices := make([]int, 0, len(ifNames))
	for idx := range ifNames {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	ifaces := make([]Iface, 0, len(indices))
	for _, idx := range indices {
		it := Iface{IfIndex: idx, IfName: ifNames[idx]}
		if v, ok := ifAlias[idx]; ok {
			it.IfAlias = v
		}
		it.IfType = ifType[idx]
		it.IfMTU = ifMtu[idx]
		it.IfSpeedBps = ifSpeed[idx]
		it.IfAdminStatus = ifAdmin[idx]
		it.IfOperStatus = ifOper[idx]

		if out.Availability.IfHCOctets {
			it.InOctets = inOct[idx]
			it.OutOctets = outOct[idx]
		}
		if out.Availability.IfErrors {
			it.InErrors = inErr[idx]
			it.OutErrors = outErr[idx]
		}
		if out.Availability.IfDiscards {
			it.InDiscards = inDis[idx]
			it.OutDiscards = outDis[idx]
		}
		if out.Availability.IfUcastPkts {
			it.InUcastPkts = inU[idx]
			it.OutUcastPkts = outU[idx]
		}
		ifaces = append(ifaces, it)
	}
	out.Interfaces = ifaces
	return out, nil
}

func isNoSuchType(t gosnmp.Asn1BER) bool {
	return t == gosnmp.NoSuchObject || t == gosnmp.NoSuchInstance || t == gosnmp.EndOfMibView
}

func tryGetInt64(snmp *gosnmp.GoSNMP, oid string) (int64, bool) {
	res, err := snmp.Get([]string{oid})
	if err != nil || res == nil || len(res.Variables) == 0 {
		return 0, false
	}
	v := res.Variables[0]
	if isNoSuchType(v.Type) {
		return 0, false
	}
	return int64(toUint64(v.Value)), true
}

func walkStringTable(snmp *gosnmp.GoSNMP, baseOid string) (map[int]string, bool) {
	out := map[int]string{}
	var saw bool
	err := snmp.BulkWalk(baseOid, func(pdu gosnmp.SnmpPDU) error {
		if isNoSuchType(pdu.Type) {
			return nil
		}
		name := trimDot(pdu.Name)
		if !strings.HasPrefix(name, baseOid+".") {
			return nil
		}
		idx, ok := lastIndex(name)
		if ok {
			out[idx] = toString(pdu.Value)
			saw = true
		}
		return nil
	})
	return out, err == nil && saw
}

func walkIntTable(snmp *gosnmp.GoSNMP, baseOid string) (map[int]int64, bool) {
	out := map[int]int64{}
	var saw bool
	err := snmp.BulkWalk(baseOid, func(pdu gosnmp.SnmpPDU) error {
		if isNoSuchType(pdu.Type) {
			return nil
		}
		name := trimDot(pdu.Name)
		if !strings.HasPrefix(name, baseOid+".") {
			return nil
		}
		idx, ok := lastIndex(name)
		if ok {
			out[idx] = int64(toUint64(pdu.Value))
			saw = true
		}
		return nil
	})
	return out, err == nil && saw
}

func walkCounter32Table(snmp *gosnmp.GoSNMP, baseOid string) (map[int]uint64, bool) {
	out := map[int]uint64{}
	var saw bool
	err := snmp.BulkWalk(baseOid, func(pdu gosnmp.SnmpPDU) error {
		if isNoSuchType(pdu.Type) {
			return nil
		}
		name := trimDot(pdu.Name)
		if !strings.HasPrefix(name, baseOid+".") {
			return nil
		}
		idx, ok := lastIndex(name)
		if ok {
			out[idx] = toUint64(pdu.Value) & 0xFFFFFFFF
			saw = true
		}
		return nil
	})
	return out, err == nil && saw
}

func walkCounter64Table(snmp *gosnmp.GoSNMP, baseOid string) (map[int]uint64, bool) {
	out := map[int]uint64{}
	var saw bool
	err := snmp.BulkWalk(baseOid, func(pdu gosnmp.SnmpPDU) error {
		if isNoSuchType(pdu.Type) {
			return nil
		}
		name := trimDot(pdu.Name)
		if !strings.HasPrefix(name, baseOid+".") {
			return nil
		}
		idx, ok := lastIndex(name)
		if ok {
			out[idx] = toUint64(pdu.Value)
			saw = true
		}
		return nil
	})
	return out, err == nil && saw
}

func lastIndex(oid string) (int, bool) {
	parts := strings.Split(oid, ".")
	if len(parts) == 0 {
		return 0, false
	}
	i, err := strconv.Atoi(parts[len(parts)-1])
	return i, err == nil
}

func trimDot(s string) string {
	if strings.HasPrefix(s, ".") {
		return s[1:]
	}
	return s
}

func toString(v any) string {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func toUint64(v any) uint64 { return gosnmp.ToBigInt(v).Uint64() }

func diffCounter64(cur, prev uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	return (math.MaxUint64 - prev) + cur + 1
}

func diffCounter32(cur, prev uint64) uint64 {
	cur &= 0xFFFFFFFF
	prev &= 0xFFFFFFFF
	if cur >= prev {
		return cur - prev
	}
	return (uint64(math.MaxUint32) - prev) + cur + 1
}
