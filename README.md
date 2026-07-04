# reachy-mini-sdk-go

A Go SDK for piloting a [Reachy Mini](https://github.com/pollen-robotics/reachy_mini) robot without writing any Python.

Pollen Robotics' official SDK is Python-only, but the robot runs a background **daemon** (a FastAPI service, pre-installed and auto-started) that exposes the entire control surface over plain HTTP REST + WebSocket JSON, plus a separate embedded WebRTC signalling server for the camera feed. This package is a from-scratch Go client for that daemon API, reverse-engineered from the daemon's live OpenAPI schema and, for the camera, the upstream GStreamer `webrtcsink` signalling protocol.

## Install

```
go get github.com/nlm/reachy-mini-sdk-go
```

## Requirements

- Go 1.25+
- `ffmpeg` on `PATH` (only needed for `StreamCameraFrames` — it's used to decode the camera's H.264/VP8 video, since there's no production-quality pure-Go decoder for either)
- Network access to the robot's daemon: port `8000` for the REST/WebSocket API, and `8443` for the WebRTC signalling server used only for the camera feed.

## Usage

```go
import reachymini "github.com/nlm/reachy-mini-sdk-go"

client := reachymini.New("http://localhost:8000")

ctx := context.Background()
if err := client.EnsureMotorMode(ctx, reachymini.MotorModeEnabled); err != nil {
	log.Fatal(err)
}
if err := client.WakeUp(ctx); err != nil {
	log.Fatal(err)
}
```

See [github.com/nlm/reachy-mini-sdk-go on pkg.go.dev](https://pkg.go.dev/github.com/nlm/reachy-mini-sdk-go) for the full API, and [github.com/nlm/reachy-mini-demos](https://github.com/nlm/reachy-mini-demos) for runnable example programs built on this SDK.

## SDK coverage

| File | Covers |
|---|---|
| `state.go` | Robot state: full state, present head pose / body yaw / antenna positions, direction-of-arrival, plus `StreamFullState` for the live WebSocket feed |
| `move.go` | Movement: `Goto` (interpolated), `SetTarget`/`StreamSetTarget` (direct, low-latency), `WakeUp`/`GotoSleep` (canned animations), `Stop`, recorded move datasets |
| `motors.go` | Motor control mode (`enabled` / `disabled` / `gravity_compensation`), plus `EnsureMotorMode` |
| `media.go` | Sound playback: play/stop/list/upload/delete, clear incoming audio |
| `audio.go` | Speaker/mic volume, low-level audio mixer parameters, test sound |
| `daemon.go` | Daemon status and restart |
| `apps.go` | The Hugging Face Spaces app store: list/install/remove/update/start/stop apps, check for updates |
| `camera.go` | Camera specs (resolutions, intrinsics/distortion) |
| `camera_stream.go` + `webrtc_signalling.go` | `StreamCameraFrames` — live decoded video frames over WebRTC |
| `audio_stream.go` | `StreamMicrophoneAudio` — live decoded PCM audio from the robot's microphone, over the same WebRTC feed |
| `kinematics.go` | Kinematics info, URDF, STL mesh downloads |
| `ws.go` | Shared WebSocket helper for the streaming state/target endpoints |

All request/response types were derived from the daemon's live `/openapi.json` and cross-checked against a real robot and Pollen's own desktop simulator — see "Verified vs. inferred" below for the exceptions.

## Known issues / caveats

- **`gravity_compensation` motor mode returns HTTP 500** on daemon v1.8.1 (confirmed against real hardware). Prefer `disabled`, which works reliably.
- **Motor control mode does not survive a daemon restart.** On real hardware it always came back `disabled`; the desktop simulator has come back either way. Nothing enforces this — any program that moves the robot should call `EnsureMotorMode(ctx, MotorModeEnabled)` first rather than assuming it's already enabled. It's also been observed to reset to `disabled` on its own between sessions, with no restart involved, so "check before you move" is the safe default, not just "check after a restart."
- **Movement commands don't validate hardware state.** `Goto`, `SetTarget`, `WakeUp`, etc. all return `200 OK` and a UUID even when motor control is `disabled` — they're silently no-ops. There's no error to catch; the only sign is that nothing physically moves.
- **A daemon restart puts motors in compliant/free-spinning mode for its duration.** Anything resting on the antennas/head (or gravity) can leave them far from where they started. Re-enabling control doesn't re-home position.
- **`Pose`'s `AnyPose` union** (`XYZRPYPose` vs `Matrix4x4Pose`) is distinguished in `UnmarshalJSON` by the presence of an `"m"` field, since the daemon's Pydantic model doesn't use an explicit discriminator tag. Verified against live data using `XYZRPYPose`; `Matrix4x4Pose` round-tripping hasn't been separately exercised.

### Camera / WebRTC

- Verified live end-to-end against Pollen's desktop simulator: signalling handshake, WebRTC/ICE negotiation, VP8 depacketization, IVF muxing, and `ffmpeg` decode all confirmed producing real, correctly-decoded frames.
- The simulator negotiates **VP8**. Real hardware has been observed in the daemon's own source to use **hardware H.264** on Raspberry Pi instead. Both codecs are handled in `camera_stream.go` (decoder choice is deferred until the negotiated codec is known), but only the VP8 path has actually been exercised against a live producer — H.264 is implemented from the same protocol understanding but unverified against real hardware.
- `TestFFmpegH264DecodePipeline` and `TestFFmpegVP8DecodePipeline` (`camera_stream_test.go`) test the ffmpeg decode pipeline in isolation with synthetic streams, independent of the robot — run with `go test ./...`.
- The same WebRTC feed also carries an **Opus audio track** from the robot's microphone, alongside the video track (confirmed by cross-referencing the official Python SDK's `ReachyMini(media_backend="webrtc")` + `mini.media.start_recording()` path, which relies on the same daemon capability). `StreamMicrophoneAudio` (`audio_stream.go`) consumes it, muxing RTP packets into an Ogg/Opus container (`pion/webrtc`'s `oggwriter`, no manual depacketization needed since one RTP packet is one Opus frame) and decoding via `ffmpeg` to raw PCM, the same subprocess pattern as the video path. This is implemented from the same protocol understanding as the rest of this file but has **not yet been exercised against a live robot or simulator** — treat it as unverified until tested against real hardware.

## Verified vs. inferred

Most of the SDK has been exercised against either real Reachy Mini hardware or Pollen's desktop simulator (state reads, movement, sounds, motors, daemon restart, volume, apps listing, kinematics, camera). A few corners haven't been:

- `InstallApp` / `RemoveApp` / `UpdateApp` / `StartApp` / `StopCurrentApp` / `ResetAppsCache` — not run live, since they mutate persistent app installation state
- `GetKinematicsSTL` — needs a real filename from `GetURDF`'s content, not just guessed
- `ReadAudioParameter` / `ApplyAudioConfig` — parameter names are undocumented and backend-specific
- H.264 camera decoding (see above)
- `StreamMicrophoneAudio` (see "Camera / WebRTC" above)

These are implemented against the daemon's documented schema and follow the same patterns as the verified parts of the SDK, but treat them as a starting point to debug against real traffic rather than a guaranteed-working path.
