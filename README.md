# seismo

> 만드는 재미를 잃지 않기 위해 만든 프로젝트.
>
> A project I made so I wouldn't lose the joy of making things.

`seismo`는 특정 Apple Silicon MacBook 안에 숨어 있는 가속도계(IMU)를
꺼내서, 로컬에서 돌아가는 실시간 지진계/진동계로 바꿔주는 프로젝트입니다.
문서화되지 않은 `AppleSPU` 센서 경로를 읽고, 신호를 필터링하고, 이벤트를
감지해서, 브라우저 대시보드와 메뉴바 앱으로 보여줍니다.

과학 장비를 만들려는 프로젝트라기보다는,  
**“맥북 안에 이런 센서가 왜 있지?”라는 호기심과 만드는 재미를 다시 살리고
싶어서 만든 하드웨어 해킹 프로젝트**에 가깝습니다.

---

## 한국어

### 왜 만들었나

재밌는 걸 만드는 감각을 잃고 싶지 않았습니다.

요즘 컴퓨터는 엄청난 하드웨어를 품고 있으면서도, 정작 사용자는 그걸 거의
만져볼 수 없습니다. `seismo`는 그중에서도 맥북 안에 숨어 있는 모션 센서를
꺼내서 “책상 위 세계가 데이터로 보이는 경험”을 만드는 프로젝트입니다.

타이핑, 발걸음, 책상 충격, 문 닫힘 같은 흔들림이 바로 그래프로 나타나고,
생각보다 훨씬 민감하게 반응합니다.  
쓸모없어 보이는 내부 센서를 눈앞의 살아 있는 계측기로 바꾸는 재미가
이 프로젝트의 핵심입니다.

### 스크린샷

#### 전체 대시보드

![Seismo dashboard](assets/images/dashboard.png)

#### HELICORDER / 흐름 파형

![Seismo helicorder](assets/images/helicorder.png)

#### PEAK GROUND ACCEL meter

![Seismo PGA meter](assets/images/pga.png)

> 위 이미지는 실제 `http://127.0.0.1:8766/` 대시보드를 Puppeteer로 캡처한 것입니다.

### 간단한 원리

동작 흐름은 대략 이렇습니다.

1. 맥북 내부 가속도계의 X/Y/Z 데이터를 읽습니다.
2. 노이즈와 중력 성분 영향을 줄이기 위해 필터링합니다.
3. 실시간으로 진폭, RMS, peak ground acceleration, STA/LTA 비율을 계산합니다.
4. 로컬 서버가 브라우저 대시보드에 이 값을 뿌립니다.
5. Canvas 기반 UI가 파형, 피크, 이벤트를 실시간으로 그립니다.

즉, 보이지 않던 미세한 흔들림을  
**“실시간으로 볼 수 있는 신호”**로 바꾸는 프로젝트입니다.

### 기술 스택

- **Go**
  - 센서 읽기
  - 필터링 / 집계 / 이벤트 계산
  - 로컬 HTTP 서버
- **IOKit / Apple SPU HID**
  - 맥북 내부 IMU 데이터 직접 접근
- **POSIX shared memory**
  - 센서 루프와 소비 루프 사이의 경량 데이터 전달
- **HTML + JavaScript + Canvas**
  - 실시간 대시보드 렌더링
- **Swift + `SMAppService`**
  - 메뉴바 앱과 helper 등록

### 어떤 걸 감지할 수 있나

조용한 책상 위에 올려두면 이런 것들이 보입니다.

- 타이핑 진동
- 책상 두드림
- 발걸음
- 문 닫는 충격
- 주변의 큰 진동
- 가까운 실제 흔들림 이벤트

이건 정밀한 연구용 지진계는 아닙니다.  
대신, **소비자용 센서를 이용해 진동을 시각적으로 체감하게 만드는 아주 재밌는 장난감**입니다.

### 지원 하드웨어

현재 프로젝트 기준:

- 지원: **M2 이상 MacBook**, **M1 Pro MacBook SKU**
- 미지원: **Intel Mac**, **일반 M1**, **Mac Studio**

센서가 없는 기기에서는 동작하지 않습니다.

### 빠르게 실행하기

#### 실제 센서 사용

```bash
go build -o seismo ./cmd/seismo
sudo ./seismo
```

브라우저에서:

```text
http://127.0.0.1:8766
```

#### mock 모드

하드웨어 없이 UI를 보고 싶다면:

```bash
./seismo --mock
```

이 모드는 synthetic noise + impulse 이벤트로 대시보드를 구동합니다.

### 주요 플래그

```text
-addr     HTTP bind address               (default 127.0.0.1:8766)
-window   waveform window in seconds      (default 600)
-sta      STA window in seconds           (default 0.5)
-lta      LTA window in seconds           (default 10.0)
-trigger  STA/LTA ratio to flag an event  (default 4.0)
-record   CSV file to append samples to   (optional)
-mock     synthetic sensor demo mode      (default false)
```

### 메뉴바 앱

메뉴바 래퍼 앱도 포함되어 있습니다.

```bash
./app/build.sh
open app/Seismo.app
```

일반적인 사용 흐름:

1. `Seismo.app` 빌드
2. `/Applications`로 복사
3. 앱 실행
4. **enable helper…** 또는 **repair helper registration…**
5. 필요하면 **System Settings → General → Login Items & Extensions** 에서 승인

### 왜 sudo가 필요한가

이 프로젝트는 macOS의 저수준 센서 경로를 직접 여는 방식이라서,  
일반 사용자 권한으로는 접근할 수 없습니다.

즉:
- macOS 공개 API로는 안 되고
- undocumented sensor path를 열어야 해서
- root 권한이 필요합니다

### 주의사항

- Apple의 undocumented hardware path에 의존합니다
- 모델별 하드웨어 지원 여부가 다릅니다
- 시각화는 “정확한 과학 측정”보다 “즉각적인 체감”에 맞춰 튜닝되어 있습니다
- helper가 오래 떠 있으면 최신 대시보드가 반영되지 않을 수 있어 재시작이 필요할 수 있습니다

### 크레딧

- Sensor path reference:
  [olvvier/apple-silicon-accelerometer](https://github.com/olvvier/apple-silicon-accelerometer)
  →
  [taigrr/apple-silicon-accelerometer](https://github.com/taigrr/apple-silicon-accelerometer)
- STA/LTA trigger 아이디어는 고전적인 지진 이벤트 감지 방식에서 가져왔습니다

---

## English

### Why this exists

I built `seismo` because I did not want to lose the joy of making things.

Certain Apple Silicon MacBooks contain a motion sensor path that most people
never see. This project pulls that hidden hardware into the open and turns it
into a live desktop seismograph: part hardware hack, part signal-processing
toy, part excuse to make something delightfully unnecessary.

### Screenshots

#### Full dashboard

![Seismo dashboard](assets/images/dashboard.png)

#### Helicorder / flow view

![Seismo helicorder](assets/images/helicorder.png)

#### Peak meter

![Seismo PGA meter](assets/images/pga.png)

> These screenshots were captured from the live local dashboard with Puppeteer.

### How it works

At a high level:

1. Read X/Y/Z acceleration from the internal MacBook IMU
2. Filter the signal to remove slow drift and emphasize motion
3. Compute live metrics such as magnitude, RMS, PGA, and STA/LTA triggers
4. Serve the dashboard locally at `http://127.0.0.1:8766`
5. Render everything in real time in the browser

The result is less “scientific earthquake instrument” and more
“watch your laptop become a vibration instrument.”

### Tech stack

- **Go**
  - sensor access loop
  - filtering and aggregation
  - local HTTP server
- **IOKit / Apple SPU HID**
  - low-level IMU access on macOS
- **POSIX shared memory**
  - lightweight local transport
- **HTML + JavaScript + Canvas**
  - real-time dashboard rendering
- **Swift + `SMAppService`**
  - menu bar wrapper app and helper registration

### What it can detect

On a desk in a quiet room, it can pick up:

- typing vibration
- taps on the desk
- footsteps
- doors closing
- strong nearby vibration
- real local shaking events

This is **not** a research-grade seismometer.  
It is a consumer IMU used in an intentionally fun and inappropriate way.

### Supported hardware

From the current project notes:

- supported: **M2+ MacBooks**, and the **M1 Pro MacBook SKU**
- not supported: **Intel Macs**, **plain M1**, **Mac Studio**

### Quick start

#### Real hardware

```bash
go build -o seismo ./cmd/seismo
sudo ./seismo
```

Open:

```text
http://127.0.0.1:8766
```

#### Mock mode

```bash
./seismo --mock
```

Mock mode drives the UI with synthetic background motion and impulse-like events.

### Flags

```text
-addr     HTTP bind address               (default 127.0.0.1:8766)
-window   waveform window in seconds      (default 600)
-sta      STA window in seconds           (default 0.5)
-lta      LTA window in seconds           (default 10.0)
-trigger  STA/LTA ratio to flag an event  (default 4.0)
-record   CSV file to append samples to   (optional)
-mock     synthetic sensor demo mode      (default false)
```

### Menu bar app

```bash
./app/build.sh
open app/Seismo.app
```

Typical flow:

1. Build `Seismo.app`
2. Copy it into `/Applications`
3. Launch it
4. Use **enable helper…** or **repair helper registration…**
5. Approve it in **System Settings → General → Login Items & Extensions** if needed

### Why `sudo` is needed

This project opens a low-level undocumented sensor path on macOS rather than a
public motion framework API, so root access is required.

### Caveats

- depends on undocumented Apple hardware paths
- hardware support varies by model
- visualization is tuned for feel and responsiveness, not scientific calibration
- if the helper is stale, restart or re-register it so localhost serves the latest embedded dashboard

### Credits

- Sensor path originally ported from
  [olvvier/apple-silicon-accelerometer](https://github.com/olvvier/apple-silicon-accelerometer)
  via
  [taigrr/apple-silicon-accelerometer](https://github.com/taigrr/apple-silicon-accelerometer)
- STA/LTA trigger inspiration comes from classic earthquake event detection workflows

## License

MIT
