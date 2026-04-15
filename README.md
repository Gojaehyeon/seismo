# seismo

Live seismograph for Apple Silicon MacBooks. Reads the undocumented
`AppleSPU` MEMS IMU (Bosch BMI286) at ~100 Hz and serves a 3-axis trace, a
peak-magnitude envelope, and an STA/LTA event detector in your browser.

> Requires an Apple Silicon MacBook (M2+, or the M1 Pro SKU). Intel Macs,
> vanilla M1, and Mac Studio do not have the SPU MEMS IMU.

## What it actually detects

The Bosch BMI286 is a consumer-grade MEMS accelerometer with a noise floor
around 100 μg/√Hz. In a quiet room on a still table it picks up:

- Typing force transmitted through the chassis
- Footsteps on nearby flooring
- Slamming doors
- Heavy trucks driving past outside
- Real earthquakes (at least nearby ones)
- Your own heartbeat if you rest your wrist on the trackpad (BCG)

It is **not** a research-grade seismometer. It is a surprisingly capable toy.

## Build

```bash
go build -o seismo ./cmd/seismo
```

## Run

```bash
sudo ./seismo
# → http://127.0.0.1:8766/
```

Open the URL in your browser. You get:

- **3-axis trace** (X/Y/Z over the last 60 seconds, high-pass filtered)
- **Magnitude envelope** with red vertical bars at detected events
- **PGA** (peak ground acceleration, since start)
- **RMS** over the window
- **STA/LTA ratio** — classic seismology trigger:
  short-term average over long-term average
- **Event log** — timestamps of triggered events

### Flags

```
-addr     HTTP bind address               (default 127.0.0.1:8766)
-window   waveform window in seconds      (default 60)
-sta      STA window in seconds           (default 0.5)
-lta      LTA window in seconds           (default 10.0)
-trigger  STA/LTA ratio to flag an event  (default 4.0)
-record   CSV file to append samples to   (optional)
```

### Record raw data

```bash
sudo ./seismo -record ~/seismo.csv
```

Columns: `t,x,y,z,hx,hy,hz,mag` (raw g, high-pass g, magnitude g).

## Why sudo

`AppleSPUHIDDevice` is gated behind IOKit and requires root to open. There
is no user-space API for this sensor. Apple does not expose it through
Core Motion on macOS.

## Credits

- Sensor path originally ported from
  [olvvier/apple-silicon-accelerometer](https://github.com/olvvier/apple-silicon-accelerometer)
  via [taigrr/apple-silicon-accelerometer](https://github.com/taigrr/apple-silicon-accelerometer)
  (MIT).
- STA/LTA detector is the classic Trnkoczy 2002 earthquake trigger.

## License

MIT.
