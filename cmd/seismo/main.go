// seismo — live seismograph for Apple Silicon MacBooks.
//
// Reads the undocumented AppleSPU MEMS IMU (Bosch BMI286) at ~100 Hz via
// IOKit HID and serves a 3-axis seismograph UI on http://127.0.0.1:8766.
//
//   sudo seismo
//
// Root is required for IOKit HID access.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Gojaehyeon/seismo/internal/sensor"
	"github.com/Gojaehyeon/seismo/internal/shm"
)

//go:embed index.html
var indexHTML []byte

var (
	fAddr    = flag.String("addr", "127.0.0.1:8766", "HTTP bind address")
	fWindow  = flag.Int("window", 60, "waveform window in seconds")
	fRecord  = flag.String("record", "", "CSV file to append samples to (optional)")
	fSTAWin  = flag.Float64("sta", 0.5, "STA (short-term average) window in seconds")
	fLTAWin  = flag.Float64("lta", 10.0, "LTA (long-term average) window in seconds")
	fTrigger = flag.Float64("trigger", 4.0, "STA/LTA ratio threshold to flag an event")
)

// sampleRate is the effective rate after IMU decimation (8x from ~800Hz).
const sampleRate = 100

// Reading is one high-pass-filtered sample exposed to the web UI.
type Reading struct {
	T  float64 `json:"t"`  // seconds since start
	HX float64 `json:"hx"` // g
	HY float64 `json:"hy"`
	HZ float64 `json:"hz"`
	M  float64 `json:"m"` // magnitude
}

// Quake is a detected event via STA/LTA.
type Quake struct {
	T     float64 `json:"t"`
	Ratio float64 `json:"ratio"`
	Peak  float64 `json:"peak"`
}

// Bus holds the rolling waveform window and runtime stats.
type Bus struct {
	mu        sync.Mutex
	start     time.Time
	window    []Reading
	windowCap int
	head      int
	filled    int

	// running stats over the rolling window
	pga     float64 // peak ground acceleration (g)
	rms     float64 // rolling RMS
	staLTA  float64 // latest STA/LTA ratio
	sta     float64
	lta     float64
	triggerThreshold float64
	lastTriggered    time.Time

	quakes     []Quake
	quakesCap  int
	quakesHead int
	quakesLen  int

	// HP filter state (1-pole high-pass, alpha=0.97 ≈ 0.5Hz cutoff at 100Hz)
	hpAlpha   float64
	hpPrevRaw [3]float64
	hpPrevOut [3]float64
	hpReady   bool

	csvFile *os.File
}

func newBus(windowSec int, triggerThreshold float64, csvPath string) *Bus {
	cap := windowSec * sampleRate
	b := &Bus{
		start:            time.Now(),
		window:           make([]Reading, cap),
		windowCap:        cap,
		triggerThreshold: triggerThreshold,
		hpAlpha:          0.97,
		quakesCap:        50,
		quakes:           make([]Quake, 50),
	}
	if csvPath != "" {
		f, err := os.OpenFile(csvPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			log.Printf("record: %v", err)
		} else {
			b.csvFile = f
			if stat, _ := f.Stat(); stat.Size() == 0 {
				fmt.Fprintln(f, "t,x,y,z,hx,hy,hz,mag")
			}
		}
	}
	return b
}

func (b *Bus) push(s shm.Sample) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.hpReady {
		b.hpPrevRaw = [3]float64{s.X, s.Y, s.Z}
		b.hpReady = true
		return
	}
	a := b.hpAlpha
	hx := a * (b.hpPrevOut[0] + s.X - b.hpPrevRaw[0])
	hy := a * (b.hpPrevOut[1] + s.Y - b.hpPrevRaw[1])
	hz := a * (b.hpPrevOut[2] + s.Z - b.hpPrevRaw[2])
	b.hpPrevRaw = [3]float64{s.X, s.Y, s.Z}
	b.hpPrevOut = [3]float64{hx, hy, hz}
	mag := math.Sqrt(hx*hx + hy*hy + hz*hz)

	t := time.Since(b.start).Seconds()
	b.window[b.head] = Reading{T: t, HX: hx, HY: hy, HZ: hz, M: mag}
	b.head = (b.head + 1) % b.windowCap
	if b.filled < b.windowCap {
		b.filled++
	}

	// PGA — running max
	if mag > b.pga {
		b.pga = mag
	}

	// STA/LTA: exponential moving averages of mag^2
	staN := *fSTAWin * float64(sampleRate)
	ltaN := *fLTAWin * float64(sampleRate)
	e := mag * mag
	b.sta += (e - b.sta) / staN
	if b.lta == 0 {
		b.lta = e
	}
	b.lta += (e - b.lta) / ltaN
	if b.lta > 0 {
		b.staLTA = b.sta / b.lta
	}

	// Trigger detection
	if b.staLTA > b.triggerThreshold && time.Since(b.lastTriggered) > 2*time.Second {
		b.lastTriggered = time.Now()
		b.quakes[b.quakesHead] = Quake{T: t, Ratio: b.staLTA, Peak: mag}
		b.quakesHead = (b.quakesHead + 1) % b.quakesCap
		if b.quakesLen < b.quakesCap {
			b.quakesLen++
		}
	}

	if b.csvFile != nil {
		fmt.Fprintf(b.csvFile, "%.6f,%.6f,%.6f,%.6f,%.6f,%.6f,%.6f,%.6f\n",
			t, s.X, s.Y, s.Z, hx, hy, hz, mag)
	}
}

// snapshot returns a decimated copy of the window for the frontend (every Nth
// sample so the JSON stays small). Also computes rolling RMS.
func (b *Bus) snapshot(decim int) map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()

	if decim < 1 {
		decim = 1
	}
	out := make([]Reading, 0, b.filled/decim+1)
	var rmsSum float64
	for i := range b.filled {
		idx := (b.head - b.filled + i + b.windowCap) % b.windowCap
		r := b.window[idx]
		rmsSum += r.M * r.M
		if i%decim == 0 {
			out = append(out, r)
		}
	}
	if b.filled > 0 {
		b.rms = math.Sqrt(rmsSum / float64(b.filled))
	}

	quakes := make([]Quake, 0, b.quakesLen)
	for i := range b.quakesLen {
		idx := (b.quakesHead - b.quakesLen + i + b.quakesCap) % b.quakesCap
		quakes = append(quakes, b.quakes[idx])
	}

	return map[string]any{
		"samples": out,
		"quakes":  quakes,
		"stats": map[string]any{
			"pga":       b.pga,
			"rms":       b.rms,
			"sta_lta":   b.staLTA,
			"window_s":  *fWindow,
			"rate_hz":   sampleRate,
			"trigger":   b.triggerThreshold,
			"filled":    b.filled,
		},
	}
}

func (b *Bus) resetPGA() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pga = 0
}

func main() {
	flag.Parse()

	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "seismo: requires root for IOKit HID access → sudo seismo")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	accelRing, err := shm.CreateRing(shm.NameAccel)
	if err != nil {
		log.Fatalf("accel shm: %v", err)
	}
	defer accelRing.Close()
	defer accelRing.Unlink()

	gyroRing, err := shm.CreateRing(shm.NameGyro)
	if err != nil {
		log.Fatalf("gyro shm: %v", err)
	}
	defer gyroRing.Close()
	defer gyroRing.Unlink()

	sensorErrCh := make(chan error, 1)
	go func() {
		if err := sensor.Run(sensor.Config{
			AccelRing: accelRing,
			GyroRing:  gyroRing,
		}); err != nil {
			sensorErrCh <- err
		}
	}()
	time.Sleep(250 * time.Millisecond)

	bus := newBus(*fWindow, *fTrigger, *fRecord)
	defer func() {
		if bus.csvFile != nil {
			bus.csvFile.Close()
		}
	}()

	startHTTPServer(*fAddr, bus)
	fmt.Printf("seismo: listening on http://%s/  (window=%ds, trigger STA/LTA=%.1f)\n",
		*fAddr, *fWindow, *fTrigger)
	if *fRecord != "" {
		fmt.Printf("  recording to %s\n", *fRecord)
	}

	runSensorLoop(ctx, accelRing, bus, sensorErrCh)
}

func runSensorLoop(ctx context.Context, ring *shm.RingBuffer, bus *Bus, errCh <-chan error) {
	ticker := time.NewTicker(8 * time.Millisecond)
	defer ticker.Stop()

	var lastTotal uint64
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nbye!")
			return
		case err := <-errCh:
			log.Fatalf("sensor worker: %v", err)
		case <-ticker.C:
		}

		samples, newTotal := ring.ReadNew(lastTotal, shm.AccelScale)
		lastTotal = newTotal
		for _, s := range samples {
			bus.push(s)
		}
	}
}

func startHTTPServer(addr string, bus *Bus) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		decim := 1
		if s := r.URL.Query().Get("decim"); s != "" {
			fmt.Sscanf(s, "%d", &decim)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bus.snapshot(decim))
	})

	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		ticker := time.NewTicker(100 * time.Millisecond) // 10 Hz
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
			}
			snap := bus.snapshot(4) // 25 Hz effective via decimation
			data, err := json.Marshal(snap)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	})

	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		bus.resetPGA()
		w.WriteHeader(http.StatusNoContent)
	})

	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("http: %v", err)
		}
	}()
}
