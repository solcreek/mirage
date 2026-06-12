package main

import (
	"net"
	"sort"
)

func parseHardwareAddr(s string) (net.HardwareAddr, error) {
	return net.ParseMAC(s)
}

func minF(v []float64) float64 { s := sorted(v); return s[0] }
func maxF(v []float64) float64 { s := sorted(v); return s[len(s)-1] }

func medianF(v []float64) float64 {
	s := sorted(v)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func sorted(v []float64) []float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s
}
