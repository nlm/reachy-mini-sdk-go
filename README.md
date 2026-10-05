# reachy-mini-sdk-go

A Go SDK for piloting a [Reachy Mini](https://github.com/pollen-robotics/reachy_mini) robot without writing any Python.

Pollen Robotics' official SDK is Python-only, but the robot runs a background **daemon** (a FastAPI service, pre-installed and auto-started) that exposes the entire control surface over plain HTTP REST + WebSocket JSON, plus a separate embedded WebRTC signalling server for the camera feed. This package is a from-scratch Go client for that daemon API, reverse-engineered from the daemon's live OpenAPI schema and, for the camera, the upstream GStreamer `webrtcsink` signalling protocol.

## Install

```
go get github.com/nlm/reachy-mini-sdk-go
```

## Requirements

- Go 1.25+
- `ffmpeg` on `PATH`, built with libopus (only needed for the WebRTC media features — `StreamCameraFrames`, `StreamMicrophoneAudio`, `OpenAudioSession` — where it decodes the camera's H.264/VP8 video and encodes/decodes Opus audio, since there are no production-quality pure-Go codecs for these)
- Network access to the robot's daemon: port `8000` for the REST/WebSocket API, and `8443` for the WebRTC signalling server used for the camera, microphone and speaker streams.

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
| `state.go` | Robot state: full state (with `FullStateOptions` to opt into head/passive joints, DoA, IMU, matrix poses), present head pose / body yaw / antenna positions, direction-of-arrival, IMU, plus `StreamFullState` for the live WebSocket feed |
| `move.go` | Movement: `Goto` (interpolated), `SetTarget`/`StreamSetTarget` (direct, low-latency), `WakeUp`/`GotoSleep` (canned animations), `Stop`, recorded move datasets |
| `motors.go` | Motor control mode (`enabled` / `disabled` / `gravity_compensation`), plus `EnsureMotorMode` |
| `media.go` | Sound playback: play/stop/list/upload/delete, clear incoming audio |
| `tracking.go` | Daemon-side face tracking: enable (with blend weight) / disable, latest tracked face |
| `wobbling.go` | Daemon-side audio-reactive head wobbling (the head moves while the robot speaks): enable / disable |
| `audio.go` | Speaker/mic volume, low-level audio mixer parameters, test sound |
| `daemon.go` | Daemon status, start/stop/restart, robot name, hardware ID, robot app-lock status |
| `apps.go` | The Hugging Face Spaces app store: list/install/remove/update/start/stop apps, check for updates, the auto-start app |
| `camera.go` | Camera specs (resolutions, intrinsics/distortion) |
| `camera_stream.go` + `webrtc_signalling.go` | `StreamCameraFrames` — live decoded video frames over WebRTC |
| `audio_stream.go` | `StreamMicrophoneAudio` — live decoded PCM audio from the robot's microphone, over the same WebRTC feed |
| `audio_session.go` + `speaker_audio.go` + `ogg.go` | `OpenAudioSession` — stream PCM to the robot's speaker (and optionally receive its microphone) over one WebRTC session, with `Clear`/`Buffered`/`Drain` for barge-in and end-of-utterance |
| `kinematics.go` | Kinematics info, URDF, STL mesh downloads |
| `ws.go` | Shared WebSocket helper for the streaming state/target endpoints |

All request/response types were derived from the daemon's live `/openapi.json` and cross-checked against a real robot and Pollen's own desktop simulator — see "Verified vs. inferred" below for the exceptions.

## Known issues / caveats

- **Some calls need daemon 1.11 or newer**: head tracking (`tracking.go`), `GetIMU` / `FullStateOptions.IMU`, `GetRobotName` / `SetRobotName`, `GetStartupApp` / `SetStartupApp` and `StartAppNoEvict`. Older daemons (e.g. the 1.8.x desktop app) answer 404, or simply leave the IMU and `DaemonStatus.FaceTarget` empty.

- **`gravity_compensation` motor mode returns HTTP 500** on daemon v1.8.1 (confirmed against real hardware). Prefer `disabled`, which works reliably.
- **Motor control mode does not survive a daemon restart.** On real hardware it always came back `disabled`; the desktop simulator has come back either way. Nothing enforces this — any program that moves the robot should call `EnsureMotorMode(ctx, MotorModeEnabled)` first rather than assuming it's already enabled. It's also been observed to reset to `disabled` on its own between sessions, with no restart involved, so "check before you move" is the safe default, not just "check after a restart."
- **Movement commands don't validate hardware state.** `Goto`, `SetTarget`, `WakeUp`, etc. all return `200 OK` and a UUID even when motor control is `disabled` — they're silently no-ops. There's no error to catch; the only sign is that nothing physically moves.
- **A daemon restart puts motors in compliant/free-spinning mode for its duration.** Anything resting on the antennas/head (or gravity) can leave them far from where they started. Re-enabling control doesn't re-home position.
- **`Pose`'s `AnyPose` union** (`XYZRPYPose` vs `Matrix4x4Pose`) is distinguished in `UnmarshalJSON` by the presence of an `"m"` field, since the daemon's Pydantic model doesn't use an explicit discriminator tag. Verified against live data using `XYZRPYPose`; `Matrix4x4Pose` round-tripping hasn't been separately exercised.

### Camera / WebRTC

- **Every WebRTC stream is its own consumer session**, and the daemon runs a separate camera encoder for each one, even for audio-only use. On the robot's Raspberry Pi that's real load: when you need both microphone and speaker, use one `OpenAudioSession` with `Microphone: true` rather than a separate `StreamMicrophoneAudio`.

- Verified live end-to-end against Pollen's desktop simulator: signalling handshake, WebRTC/ICE negotiation, VP8 depacketization, IVF muxing, and `ffmpeg` decode all confirmed producing real, correctly-decoded frames.
- The simulator negotiates **VP8**; real hardware (Reachy Mini Wireless) uses **hardware H.264**. Both codecs are handled in `camera_stream.go` (decoder choice is deferred until the negotiated codec is known), and both have been verified live: VP8 against the simulator, H.264 against a Reachy Mini Wireless on daemon 1.11.0 (1280x720 frames, first one about 2 s after connecting).
- Rarely (twice in about 60 back-to-back captures, on both the simulator and the robot), a camera stream opened right after the previous one closed delivered no frame within 15 s and reported no error. Give single-frame captures a timeout and retry.
- `TestFFmpegH264DecodePipeline` and `TestFFmpegVP8DecodePipeline` (`camera_stream_test.go`) test the ffmpeg decode pipeline in isolation with synthetic streams, independent of the robot — run with `go test ./...`.
- The same WebRTC feed also carries an **Opus audio track** from the robot's microphone, alongside the video track (confirmed by cross-referencing the official Python SDK's `ReachyMini(media_backend="webrtc")` + `mini.media.start_recording()` path, which relies on the same daemon capability). `StreamMicrophoneAudio` (`audio_stream.go`) consumes it, muxing RTP packets into an Ogg/Opus container (`pion/webrtc`'s `oggwriter`, no manual depacketization needed since one RTP packet is one Opus frame) and decoding via `ffmpeg` to raw PCM, the same subprocess pattern as the video path. It is tested end to end against an in-process fake producer and verified live against both Pollen's desktop simulator and a Reachy Mini Wireless.
- **Speaker audio** (`OpenAudioSession`) goes the other way: the daemon offers every consumer a send-and-receive audio transceiver and plays what it receives through its speaker, feeding the head wobbler (`EnableWobbling`) and the barge-in flush (`ClearIncomingAudio`). The session encodes PCM to Opus with `ffmpeg` and paces it in real time, sending silence when idle. The daemon assumes a single client sends audio: with several, `ClearIncomingAudio` and its echo canceller follow only the most recently connected one. Tested end to end against an in-process fake producer (`webrtc_fake_producer_test.go`, real pion WebRTC over loopback) and **verified live against Pollen's desktop simulator** (daemons 1.8.4 and 1.11.0) **and a Reachy Mini Wireless** (daemon 1.11.0): audio plays, drives the wobbler, and stops on `Clear` + `ClearIncomingAudio`. `go test -tags live -run Live` (see `live_test.go`) repeats those checks against any daemon.

## Verified vs. inferred

Most of the SDK has been exercised against either real Reachy Mini hardware or Pollen's desktop simulator (state reads, movement, sounds, motors, daemon restart, volume, apps listing, kinematics, camera). On a Reachy Mini Wireless with daemon 1.11.0 specifically: H.264 camera frames, `OpenAudioSession` (speaker, microphone, wobbling, barge-in), `GetIMU`, head tracking with a face in view, hardware ID, robot name and app-lock status. A few corners haven't been:

- `InstallApp` / `RemoveApp` / `UpdateApp` / `StartApp` / `StopCurrentApp` / `ResetAppsCache` — not run live, since they mutate persistent app installation state
- `GetKinematicsSTL` — needs a real filename from `GetURDF`'s content, not just guessed
- `ReadAudioParameter` / `ApplyAudioConfig` — parameter names are undocumented and backend-specific
- `StartAppNoEvict` — not run live, since it starts an installed app
- `StartDaemon` / `StopDaemon` — not run live, since they take the robot backend down

These are implemented against the daemon's documented schema and follow the same patterns as the verified parts of the SDK, but treat them as a starting point to debug against real traffic rather than a guaranteed-working path.
