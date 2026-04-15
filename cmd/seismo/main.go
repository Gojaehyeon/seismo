// seismo — live seismograph for Apple Silicon MacBooks.
//
// Reads the undocumented AppleSPU MEMS IMU (Bosch BMI286) at ~100 Hz via
// IOKit HID and serves a 3-axis seismograph UI on http://127.0.0.1:8766.
//
//	sudo seismo
//
// Root is required for IOKit HID access.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
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
	fWindow  = flag.Int("window", 600, "waveform window in seconds (default: 10 minutes)")
	fRecord  = flag.String("record", "", "CSV file to append samples to (optional)")
	fSTAWin  = flag.Float64("sta", 0.5, "STA (short-term average) window in seconds")
	fLTAWin  = flag.Float64("lta", 10.0, "LTA (long-term average) window in seconds")
	fTrigger = flag.Float64("trigger", 4.0, "STA/LTA ratio threshold to flag an event")
	fMock    = flag.Bool("mock", false, "use synthetic sensor (no IOKit, no sudo) — for the UI demo")
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
	pga              float64 // peak ground acceleration (g)
	rms              float64 // rolling RMS
	staLTA           float64 // latest STA/LTA ratio
	sta              float64
	lta              float64
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
	// Peak-preserving decimation: each output point keeps the xyz of the
	// bucket's first sample but the max magnitude seen across the whole
	// bucket so brief events (taps) are never averaged out of existence.
	out := make([]Reading, 0, b.filled/decim+1)
	var rmsSum float64
	var bucketPeak float64
	var bucketStart Reading
	var bucketActive bool
	for i := range b.filled {
		idx := (b.head - b.filled + i + b.windowCap) % b.windowCap
		r := b.window[idx]
		rmsSum += r.M * r.M
		if i%decim == 0 {
			if bucketActive {
				bucketStart.M = bucketPeak
				out = append(out, bucketStart)
			}
			bucketStart = r
			bucketPeak = r.M
			bucketActive = true
		} else if r.M > bucketPeak {
			bucketPeak = r.M
		}
	}
	if bucketActive {
		bucketStart.M = bucketPeak
		out = append(out, bucketStart)
	}
	if b.filled > 0 {
		b.rms = math.Sqrt(rmsSum / float64(b.filled))
	}

	// Full-rate buffer of the last ~2.56s (256 samples at 100Hz) for the
	// frontend FFT spectrogram and particle-motion plot.
	recentN := 256
	if b.filled < recentN {
		recentN = b.filled
	}
	recent := make([]Reading, recentN)
	for i := 0; i < recentN; i++ {
		idx := (b.head - recentN + i + b.windowCap) % b.windowCap
		recent[i] = b.window[idx]
	}

	quakes := make([]Quake, 0, b.quakesLen)
	for i := range b.quakesLen {
		idx := (b.quakesHead - b.quakesLen + i + b.quakesCap) % b.quakesCap
		quakes = append(quakes, b.quakes[idx])
	}

	host, _ := os.Hostname()
	uptime := time.Since(b.start).Seconds()

	// Derive a few conveniences for the UI so it doesn't have to reinvent them.
	// 1 g ≈ 980.665 gal, 1 g ≈ 9.80665 m/s².
	const g2gal = 980.665
	const g2ms2 = 9.80665

	return map[string]any{
		"samples": out,
		"recent":  recent,
		"quakes":  quakes,
		"stats": map[string]any{
			"pga":        b.pga,
			"pga_gal":    b.pga * g2gal,
			"pga_ms2":    b.pga * g2ms2,
			"rms":        b.rms,
			"sta_lta":    b.staLTA,
			"window_s":   *fWindow,
			"rate_hz":    sampleRate,
			"trigger":    b.triggerThreshold,
			"filled":     b.filled,
			"host":       host,
			"uptime_s":   uptime,
			"start_unix": b.start.Unix(),
			"go":         runtime.Version(),
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

	if !*fMock && os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "seismo: requires root for IOKit HID access → sudo seismo")
		fmt.Fprintln(os.Stderr, "        (or pass --mock for the synthetic demo)")
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
	if *fMock {
		fmt.Println("seismo: running with synthetic sensor (--mock)")
		go runMockSensor(accelRing)
	} else {
		go func() {
			if err := sensor.Run(sensor.Config{
				AccelRing: accelRing,
				GyroRing:  gyroRing,
			}); err != nil {
				sensorErrCh <- err
			}
		}()
	}
	time.Sleep(250 * time.Millisecond)

	bus := newBus(*fWindow, *fTrigger, *fRecord)
	defer func() {
		if bus.csvFile != nil {
			bus.csvFile.Close()
		}
	}()

	httpErrCh, err := startHTTPServer(ctx, *fAddr, bus)
	if err != nil {
		log.Fatalf("http listen %s: %v", *fAddr, err)
	}
	fmt.Printf("seismo: listening on http://%s/  (window=%ds, trigger STA/LTA=%.1f)\n",
		*fAddr, *fWindow, *fTrigger)
	if *fRecord != "" {
		fmt.Printf("  recording to %s\n", *fRecord)
	}

	runSensorLoop(ctx, accelRing, bus, sensorErrCh, httpErrCh)
}

func runSensorLoop(ctx context.Context, ring *shm.RingBuffer, bus *Bus, sensorErrCh, httpErrCh <-chan error) {
	ticker := time.NewTicker(8 * time.Millisecond)
	defer ticker.Stop()

	var lastTotal uint64
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nbye!")
			return
		case err := <-sensorErrCh:
			log.Fatalf("sensor worker: %v", err)
		case err := <-httpErrCh:
			log.Fatalf("http server: %v", err)
		case <-ticker.C:
		}

		samples, newTotal := ring.ReadNew(lastTotal, shm.AccelScale)
		lastTotal = newTotal
		for _, s := range samples {
			bus.push(s)
		}
	}
}

func startHTTPServer(ctx context.Context, addr string, bus *Bus) (<-chan error, error) {
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

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	srv := &http.Server{Handler: mux}
	errCh := make(chan error, 1)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	return errCh, nil
}
