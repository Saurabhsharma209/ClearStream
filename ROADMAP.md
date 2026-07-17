# ClearStream + LangStream — Product Roadmap

> Last updated: 2026-07-08  
> Owner: Saurabh Sharma (saurabh.sharma@exotel.com)  
> Repos: [ClearStream](https://github.com/Saurabhsharma209/ClearStream) · [LangStream](https://github.com/Saurabhsharma209/LangStream)

---

## Competitive Context — Why This Roadmap

A benchmarking pass against the leading commercial pre-ASR SDKs (July 2026) surfaces three capabilities ClearStream needs to build and two where it already leads the market:

### Feature Gaps to Close

| Gap | Market Standard | ClearStream Today | Priority |
|---|---|---|---|
| Turn-taking detection | Dedicated AI model — knows when a speaker has finished; reduces Voice AI agent latency by eliminating silence-pad heuristics | Not built | **P1 for LangStream** |
| Multi-speaker isolation | Blind source separation — separates overlapping speakers in noisy environments | Single-stream only | P2 |
| Production proof | 50K+ agents, 1B+ min/month, SOC 2 Type II | New — unvalidated at scale | Ongoing |
| WER benchmark | 20–90% relative WER reduction (published) | No published benchmark | **P1 — needed for POC sign-off** |

### Where ClearStream Already Leads

| Strength | ClearStream | Commercial Pre-ASR SDKs |
|---|---|---|
| Telephony-native | RTP, G.711 PCMU/PCMA, SIP, Kamailio, Asterisk, FreeSWITCH — native | Generic audio only; no telephony integration |
| Carrier-layer integration | Sits in RTPEngine media path, Asterisk EAGI, FreeSWITCH mod_audio_stream | All telephony plumbing must be built around them |
| Per-call config | SDK → session → per-call override chain | Not offered |
| Open source / in-stack | Go, in Exotel's own repo, no vendor lock-in | Closed commercial, request-access |
| AGC | Full AGC with soft limiter, ASRConfig preset (-18 dBFS), ClipCount QA | Not offered |

**Strategic read:** Commercial pre-ASR SDKs target the generic "Voice AI builder" (startups on Deepgram/Whisper). ClearStream owns the telecom operator layer. The gap to close is the AI feature layer — specifically VAD-quality turn detection and a measurable WER benchmark — before LangStream's ASR stage can be justified as a product.

---

## ClearStream Roadmap

### Phase 1 — POC Sign-Off (target: 2026-07-31)

**Goal:** Demo to Exotel leadership with a measured WER number, not just "it sounds better."

| Item | What | Why |
|---|---|---|
| **WER Benchmark** | Run ClearStream → Deepgram on real noisy call samples; measure delta WER vs passthrough baseline | The market benchmark is 20–90% relative WER reduction; we need our own number before any internal or external claim |
| **VAD — energy-based turn marker** | Lightweight end-of-utterance detector (energy drop + 200ms silence) surfaced as `Pipeline.TurnEnd()` event | Closes the biggest feature gap vs market; directly feeds LangStream's ASR segmentation |
| **AGC — ASRConfig preset in all demos** | Ensure every POC demo uses `ASRConfig()` not `DefaultAGCConfig()` | ASRConfig targets -18 dBFS — ASR sweet spot; default config was causing clipping on live calls |
| **Prometheus dashboard** | Grafana board wired to `/metrics/prometheus` — ClipCount, SuppressRatio, LatencyEMA per session | Shows "quality improving in real time" during demo |

### Phase 2 — Production Hardening (target: 2026-Q3)

| Item | What |
|---|---|
| **Multi-speaker awareness** | Integrate `EnergyDiarizer` (already in `pkg/audio/diarize.go`) into the RTP session path; surface `SpeakerLabel` per frame in `QualityReport` |
| **Scale validation** | 1K concurrent RTP sessions on a single VM (TieredNR + QuickVAD + ForwardOnly already proven at 1.6M frames/sec passthrough) |
| **SNR regression gate in CI** | Auto-fail CI if RNNoise path produces SNR < 12 dB on `testdata/sample_noisy.wav` |
| **SOC 2 prep** | On-device processing path (no audio leaves server); audit trail via WAL (pkg/billing already has WAL) |

### Phase 3 — Full Feature Parity (target: 2026-Q4)

| Item | What | Notes |
|---|---|---|
| **Turn-taking model** | ML-based end-of-turn (silence + energy decay + pitch fall); replaces energy-only heuristic | Can train on Exotel's own call corpus — that's the moat |
| **Multi-speaker isolation** | x-vector speaker embeddings + NLMS beamforming | Start with 2-speaker (agent + caller); 3+ speaker for conference |
| **SDK for non-Go stacks** | C shared library (`clearstream.h`) wrapping the Go binary via cgo export | Enables Python/Node/C++ integrations |

---

## LangStream Roadmap

> Branch: `langstream` off ClearStream v0.1.0  
> Repo: github.com/Saurabhsharma209/LangStream  
> Relationship: ClearStream denoises → LangStream translates. Better input audio = measurably better ASR.

### Architecture

```
Caller RTP (G.711)
    │
    ▼
ClearStream (noise suppress + AGC + turn detection)
    │
    ▼ PCM 16kHz, clean, utterance-segmented
    │
    ▼
ASR (Sarvam AI for Indic / Deepgram for English)  ← ~200ms
    │  text
    ▼
MT (GPT-4o for quality / NLLB-200 for cost)       ← ~150ms
    │  translated text
    ▼
TTS (Cartesia / ElevenLabs)                        ← ~200ms
    │
    ▼
Agent RTP (translated voice, same G.711 format)

Target end-to-end latency: < 800ms (first word)
```

### Week-by-Week Build Plan

#### Week 1 — ASR Integration
- Wire ClearStream's `Pipeline.TurnEnd()` event → trigger ASR call (no more silence padding)
- Implement Sarvam AI streaming ASR adapter (`pkg/asr/sarvam.go`)
- Implement Deepgram streaming ASR adapter (`pkg/asr/deepgram.go`)
- Config: `ASRProvider`, `ASRLanguage`, `ASRFallback`

#### Week 2 — Duplex RTP (extends ClearStream pkg/rtp.Session)
- Two-leg RTP: caller-leg + agent-leg, both through the pipeline simultaneously
- Forward leg: caller audio → ClearStream → ASR → MT → TTS → agent
- Return leg: agent audio → ClearStream → ASR → MT → TTS → caller
- Note: any changes needed in ClearStream's `pkg/rtp` will arrive as a separate PR against this repo

#### Week 3 — MT + TTS
- MT adapter: GPT-4o (quality path) + NLLB-200 self-hosted (cost path)
- Language pair config: `SourceLang`, `TargetLang`, `MTProvider`
- TTS adapter: Cartesia (low latency, ~80ms) + ElevenLabs (voice cloning)
- Streaming TTS: start playing first TTS chunk before full translation is complete

#### Week 4 — Latency Optimization + Demo
- Speculative translation: start MT before ASR is fully complete (partial hypothesis)
- TTS pre-warming: keep TTS session alive across turns
- Latency budget measurement: ASR + MT + TTS breakdown logged per call
- Demo: live Hindi → English call translation on Exotel's own infrastructure

### Language Priority (based on Exotel's existing market)

| Phase | Languages | ASR Provider | MT | TTS |
|---|---|---|---|---|
| Pilot | Hindi ↔ English | Sarvam AI | GPT-4o | Cartesia |
| Phase 2 | + Tamil, Telugu, Kannada, Marathi | Sarvam AI | GPT-4o + NLLB-200 | Cartesia |
| Phase 3 | + Bengali, Gujarati, Punjabi, Odia | Sarvam AI | NLLB-200 (cost-optimized) | ElevenLabs (voice match) |
| International | Indonesian, Arabic, UAE/KSA | Deepgram + local | GPT-4o | ElevenLabs |

### Market Sizing (for internal business case)

- Speech-to-speech translation market: **$690M–$762M (2025–26)**, growing
- Contact center software overall: **$72B → $184B by 2031**
- Exotel's addressable wedge: carrier-layer translation (no Teams/Google moat here — they don't own the RTP path)
- Key moat: Exotel owns the SIP trunk + RTPEngine path; LangStream runs in that media plane. Google Translate and DeepL require app-layer integration. Exotel can offer this transparent to both caller and agent.

### Why Real-Time Translation is a Distinct Product

Existing Voice AI SDKs position themselves as pre-ASR cleanup layers for AI agents — not as real-time translation infrastructure. LangStream is a **carrier-layer translation product**: it sits in the RTP media path, operates transparently on both legs of a call, and requires no changes to the caller's app or the agent's system. The closest alternatives are:

- **Google CCAI Translation** — cloud-only, not carrier-native, ~1.2s latency
- **DeepL Voice** — EU-focused, no Indic languages, not RTP-integrated
- **Microsoft Azure Speech Translation** — strong on enterprise UC (Teams), weak on PSTN/SIP

None of them have Exotel's distribution (carrier + contact center + voicebot in one stack).

---

## Dependencies Between Repos

```
ClearStream v0.1.0 (tagged)
    └── pkg/rtp.Session  ← LangStream Week 2 extends this for duplex
    └── pkg/rtp.Session.CleanAudio() ← LangStream Week 2/3 ASR feed (added 2026-07-12, see Resolved Decisions below)
    └── pkg/audio.Pipeline → TurnEnd() event ← LangStream Week 1 consumes this (shipped 2026-07-13, see Resolved Decisions)
    └── pkg/audio.AGCConfig → ASRConfig() ← LangStream uses this preset
```

**Rule:** LangStream never silently modifies ClearStream. Any `pkg/rtp` or `pkg/audio` changes needed for LangStream arrive as a separate, reviewed PR against ClearStream with its own commit message.

---

## Resolved Decisions

| Date | Decision | Why |
|---|---|---|
| 2026-07-12 | **Clean-audio hand-off to LangStream:** added `Session.CleanAudio() <-chan CleanAudioFrame`, opt-in via `Config.CleanAudioBufferSize` (0 = disabled, default, zero cost). Each frame is an owned PCM copy; delivery is non-blocking with drop-oldest-on-full. Rejected: (a) a synchronous `OnCleanAudio` callback mirroring `OnDTMF` -- wrong fit, since the source PCM is a pooled buffer reused every 10ms and ASR consumers are inherently async; (b) LangStream forking its own RTP receive loop -- would duplicate the hardened jitter/PLC/SSRC logic in `pkg/rtp.Session` and contradicts the Week 2 plan of extending it. This was the single blocker on Week 3s MT adapter, language-pair config threading, and TTS adapter -- all three need a defined, stable audio-in contract before they can be built/tested against. | Unblocks LangStream Week 2 (duplex RTP) and Week 3 (MT + TTS); channel-of-owned-copies avoids the pooled-buffer race a synchronous callback would risk under async ASR calls. |

**Resolved:** `Pipeline.TurnEnd()` (referenced above) shipped 2026-07-13 in `pkg/audio/turnend.go` -- an energy-based end-of-utterance detector (speech frame followed by ~200ms sustained silence) surfaced as a `<-chan TurnEndEvent`, opt-in via `PipelineConfig.TurnEndBufferSize` (0 = disabled, zero cost). LangStream Week 1's ASR trigger can now be wired against it; no longer a blocker for Week 1/2 integration testing.

---

## Open Questions / Decisions Needed

| Question | Options | Owner |
|---|---|---|
| Turn-taking: energy heuristic vs ML model for Phase 1? | Energy (2 weeks) vs ML (6 weeks, needs training data) | Saurabh |
| WER benchmark: which call sample set? | Exotel's own recorded calls (best) vs synthetic test set (available now) | Saurabh |
| LangStream: build in-house ASR or use Sarvam API? | API first (faster), in-house later (cost at scale) | Saurabh |
| MT: GPT-4o only, or NLLB-200 from day 1? | GPT-4o for pilot (quality), NLLB-200 when volume justifies | Saurabh |
| Multi-speaker isolation: roadmap or defer? | Defer to Phase 3 — single-stream covers 95% of call center use cases | Agreed |
