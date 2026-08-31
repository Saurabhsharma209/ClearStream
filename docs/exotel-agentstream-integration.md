# Exotel AgentStream Integration (branch: `feature/exotel-agentstream`)

Status: proof-of-concept, not merged to `main`. Implements a WebSocket server
(`pkg/agentstream.AgentStreamServer`) that speaks Exotel AgentStream's real
wire protocol and runs each call's audio through a per-call ClearStream
`Pipeline`, with all noise-suppression configuration driven by data in the
request -- nothing hardcoded per customer or call.

## Where this fits in an Exotel call

```
Exotel platform  --(start_stream / /calls/connect / VoiceBot Applet)-->  ClearStream AgentStreamServer  -->  your bot backend
                        JSON WSS protocol                                    (this repo)                       (your own leg,
                        connected/start/media/                                                                  any protocol
                        dtmf/stop/mark/clear                                                                    you choose)
```

Point the `url` / `StreamUrl` / VoiceBot Applet stream URL at
`AgentStreamServer`'s `/media` endpoint (see `examples/agentstream/main.go`
for a runnable server) instead of directly at your bot. ClearStream
terminates the Exotel-facing leg, cleans the audio, and re-originates its own
leg to your real bot.

## How a customer configures this per call -- no code changes, no fixed switch/case

Exotel already gives every AgentStream entry point a way to pass per-call
data into the stream URL:

- **Custom parameters** (VoiceBot Applet): up to 3 key/value pairs appended
  to the stream URL as a query string, e.g.
  `wss://clearstream.yourdomain.com/media?ns_model=deepfilter&ns_agc=true`.
  Exotel delivers these to the WSS endpoint in the `start` event's
  `custom_parameters` field.
- **Dynamic URL resolution** (VoiceBot Applet "dynamic method", or the
  `/calls/connect` Voice API's `StreamUrl`): point `url` at an HTTPS endpoint
  you control; Exotel calls it first and uses whatever `wss://` URL it
  returns, letting you embed as much config as you want in that response
  with no 3-param/256-character limit.

`AgentStreamServer.handleStart` reads these from `StartEvent.CustomParameters`
and builds that call's `model.SuppressorConfig` + `audio.PipelineConfig`
purely from key lookups with safe defaults -- see the recognised keys below.

### Recognised `CustomParameters` keys (all optional)

| Key | Values | Effect |
|---|---|---|
| `ns_model` | `rnnoise` \| `rnnoise-onnx` \| `deepfilter` \| `deepfilter-server` \| `passthrough` | Suppressor backend for this call. Defaults to the server's `DefaultBackend` (itself defaults to `passthrough`). |
| `ns_model_path` | path/URL | `ModelPath`, required for `deepfilter`/`rnnoise-onnx`. |
| `ns_aggressiveness` | `0`-`3` | Suppressor aggressiveness at construction time. |
| `ns_vad` | `static` \| `adaptive` \| `true` | Enables VAD (`true`/`static` = fixed threshold, `adaptive` = self-calibrating). |
| `ns_agc` | `true` | Enables AGC. |
| `ns_agc_target_rms` | float | Overrides AGC target RMS. |
| `ns_mode` | `adaptive` | Enables TieredNR (SNR-adaptive gate/RNNoise/DeepFilter tier selection). |
| `ns_high_snr_db` / `ns_low_snr_db` | float | Overrides TieredNR's tier boundaries (defaults: 25 / 10 dB). |

### Example: VoiceBot Applet custom parameters

```
wss://clearstream.yourdomain.com/media?ns_model=rnnoise&ns_agc=true&ns_mode=adaptive
```

### Example: `start_stream` Legs action pointing at ClearStream

```
POST /v2/accounts/{account_sid}/legs/{leg_sid}/actions/start_stream
{
  "direction": "bidirectional",
  "url": "wss://clearstream.yourdomain.com/media?ns_model=deepfilter&ns_agc=true",
  "content_type": "audio/x-mulaw;rate=8000"
}
```

## Changing behavior mid-call: the `reconfigure` event

`reconfigure` is a **ClearStream-defined** message -- it is not part of
Exotel's native protocol and only ever travels on the leg between
ClearStream and your bot backend, so it requires no Exotel platform changes.
Your bot sends it over the same open WebSocket at any point during the call:

```json
{"event": "reconfigure", "stream_sid": "<stream_sid>", "mode": "disabled"}
{"event": "reconfigure", "stream_sid": "<stream_sid>", "mode": "aggressive", "level": 3}
{"event": "reconfigure", "stream_sid": "<stream_sid>", "mode": "enabled"}
```

| Mode | Effect |
|---|---|
| `disabled` | `Pipeline.SetBypass(true)` -- raw passthrough, no AEC/NR/suppressor/AGC/limiter. |
| `enabled` | `Pipeline.SetBypass(false)` -- undoes `disabled`. |
| `adaptive` / `aggressive` / `mild` | Adjusts TieredNR's SNR thresholds via `Pipeline.Reconfigure`. **Only takes effect if the call started with `ns_mode=adaptive`** -- TieredNR cannot be enabled from nothing mid-call in this pass. |
| (any mode) with `level` > 0 | Also calls `Pipeline.SetAggressiveness(level)`. |

## Known limitations (intentional scope cuts for this sprint, not bugs)

- `reconfigure` can only retune an *already-configured* TieredNR; it cannot
  turn tiered reduction on mid-call if the start event didn't request it.
- `CleanMediaEvent.Enhancement` (`EnhancementInfo`) has no dedicated field
  for TieredNR or AEC -- TieredNR is folded into `NoiseSuppression`, and
  `VoiceIsolation`, `BackgroundVoiceCancellation`, and `EchoCancellation`
  always report `false` since none of them are implemented by this
  integration (AEC exists in `pkg/audio` but isn't wired into
  `AgentStreamServer` yet).
- DTMF, `mark`, and `clear` events are accepted for protocol completeness
  but not acted on.
- FSM validation (`pkg/agentstream.CanTransition`) is not strictly enforced
  on every transition in this pass -- the package's `StreamState` graph was
  modeled with a SIP/RTP prefix that a WSS-only integration point never
  actually sees, so `handleStart` jumps straight to `StateStreaming` rather
  than walking through states this component has no way to observe.

## Files

- `pkg/agentstream/events.go` -- wire protocol types (pre-existing, +`ConnectedEvent`/`ReconfigureEvent`)
- `pkg/agentstream/state.go` -- call lifecycle FSM (pre-existing, unchanged)
- `pkg/agentstream/server.go` -- the protocol adapter (new)
- `pkg/agentstream/server_test.go` -- end-to-end lifecycle test (new)
- `examples/agentstream/main.go` -- runnable standalone server (new)
- `pkg/audio/pipeline.go` -- `SetBypass`/`Bypassed` live toggle (new methods)
