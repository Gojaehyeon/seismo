package main

import (
	"math"
	"math/rand"
	"time"

	"github.com/Gojaehyeon/seismo/internal/shm"
)

// runMockSensor generates synthetic seismic data at 100 Hz — microseismic
// background noise plus a damped-oscillation "earthquake" every ~15 s so the
// STA/LTA detector and envelope view have something to show without hardware.
func runMockSensor(ring *shm.RingBuffer) {
	const fs = 100
	ticker := time.NewTicker(time.Second / fs)
	defer ticker.Stop()

	tick := 0
	for range ticker.C {
		tick++
		t := float64(tick) / float64(fs)

		// Gravity on Z + tiny sensor noise
		nx := (rand.Float64() - 0.5) * 0.003
		ny := (rand.Float64() - 0.5) * 0.003
		nz := 1.0 + (rand.Float64()-0.5)*0.003

		// Microseismic background (~0.2 Hz ocean-swell-like oscillation)
		nx += 0.0015 * math.Sin(2*math.Pi*0.2*t)
		ny += 0.0012 * math.Cos(2*math.Pi*0.25*t)

		// Synthetic "earthquake" every 15 s — 1.5 s damped oscillation.
		phase := math.Mod(t, 15.0)
		if phase < 1.5 {
			decay := math.Exp(-phase * 3)
			freq := 4.0 + rand.Float64()*2
			amp := 0.09 * decay
			nx += amp * math.Sin(2*math.Pi*freq*phase)
			ny += amp * 0.7 * math.Cos(2*math.Pi*freq*phase)
			nz += amp * 0.5 * math.Sin(2*math.Pi*freq*phase+0.5)
		}

		ring.WriteSample(
			int32(nx*shm.AccelScale),
			int32(ny*shm.AccelScale),
			int32(nz*shm.AccelScale),
		)
	}
}
