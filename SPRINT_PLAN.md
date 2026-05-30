# ClearStream Agent Sprint Plan

**Project:** ClearStream Audio Enhancement SDK
**Repo:** https://github.com/Saurabhsharma209/ClearStream
**Team:** 6 AI agents, rotating daily | **Cadence:** Daily 9am auto-run
**Goal:** Production-ready SDK in 8 weeks

---

## Sprint 1 Ñ Foundation (Days 1Ð5)
_Goal: SDK compiles cleanly, core pipeline is correct, tests exist_

### ? Day 1 (2026-05-30) Ñ DONE
| Agent | Task | Status |
|---|---|---|
| API Layer | Fix CLI compile error, Config.Validate(), Version const, doc comments | ? |
| AI Model | CGo build tags, linear resampling in upsample/downsample, passthrough tests | ? |
| QA/Testing | Makefile, pipeline tests, jitter buffer tests, CI workflow | ? |

### ? Day 2 (2026-05-30) Ñ DONE
| Agent | Task | Status |
|---|---|---|
| Audio Pipeline | VAD with energy threshold + hangover, integrate into pipeline | ? |
| RTP/SIP | G.711 µ-law/A-law correctness fix, SSRC change detection, roundtrip tests | ? |
| Post-processing | ProcessDir batch, OnProgress callback, typed errors, processor tests | ? |

### Day 3 Ñ Quality & HTTP API
| Agent | Task | Priority |
|---|---|---|
| API Layer | Add `pkg/http/handler.go` Ñ POST /enhance HTTP endpoint with multipart upload | HIGH |
| Audio Pipeline | Improve ffprobe JSON parsing (use encoding/json), add codec_test.go | HIGH |
| QA/Testing | Add codec_test.go (normalizeCodec, payloadTypeToCodec, inferOutputCodec tests) | HIGH |

**Day 3 agent prompts focus:**
- HTTP handler: `http.Handler`, POST /enhance, multipart upload, 50MB limit, content-type validation, return cleaned file
- Codec parsing: replace manual string search with `encoding/json`, add fixtures
- Test coverage: reach 60%+ on pkg/audio and pkg/rtp

### Day 4 Ñ Model Quality + PLC Improvement
| Agent | Task | Priority |
|---|---|---|
| AI Model | Wire DeepFilterNet ONNX behind `//go:build onnx`, add BenchmarkRNNoise | HIGH |
| RTP/SIP | Fade-to-silence PLC (0.9x decay per lost frame instead of repeat), RTCP parsing | MEDIUM |
| Audio Pipeline | Pipeline Stats() method Ñ frames processed, suppressed, avg latency | MEDIUM |

**Day 4 agent prompts focus:**
- DeepFilterNet: real ONNX session open/run behind build tag, model loading from path
- PLC: apply exponential decay on consecutive lost frames, add test for decay behavior
- Stats: atomic counters, Stats struct, expose via Pipeline.Stats() method

### Day 5 Ñ Integration + go.mod
| Agent | Task | Priority |
|---|---|---|
| QA/Testing | `go mod tidy`, fix go.sum, verify `go test ./...` passes fully | CRITICAL |
| API Layer | Add `example_test.go` with Go doc examples for ProcessFile and NewRTPSession | MEDIUM |
| Post-processing | Add StreamProcess (io.Reader ? io.Writer) for HTTP handler integration | MEDIUM |

**Day 5 agent prompts focus:**
- go mod tidy is CRITICAL Ñ go.sum is incomplete, blocking CI
- Examples: runnable Go doc examples that appear in `go doc`
- Stream: remove temp file dependency for HTTP use case

---

## Sprint 2 Ñ Production Hardening (Days 6Ð10)
_Goal: Battle-tested, benchmarked, ready for ECC integration_

### Day 6 Ñ Fine-tuning + Benchmarks
| Agent | Task | Priority |
|---|---|---|
| AI Model | Model benchmark harness Ñ measure SNR improvement on synthetic noisy audio | HIGH |
| Audio Pipeline | Replace linear resampler with Kaiser-windowed FIR (better quality for 8kHz?16kHz) | HIGH |
| QA/Testing | Integration test: generate noisy WAV ? process ? verify output is quieter | HIGH |

**Why:** Before ECC integration, we need to prove the model actually reduces noise measurably. SNR test gives us a number to put in the business case.

### Day 7 Ñ Scale + Concurrency
| Agent | Task | Priority |
|---|---|---|
| RTP/SIP | Load test harness Ñ simulate 100 concurrent RTP sessions, measure CPU/memory | HIGH |
| Post-processing | ProcessDir concurrency test Ñ verify no race conditions under `-race` | HIGH |
| API Layer | HTTP handler: add request rate limiting, graceful shutdown, health endpoint | MEDIUM |

**Why:** Contact center use case requires hundreds of concurrent sessions. Day 7 validates we can handle it.

### Day 8 Ñ Indian Accent + Noise Profile
| Agent | Task | Priority |
|---|---|---|
| AI Model | Fine-tuning data pipeline Ñ script to mix clean speech with Indian call center noise | HIGH |
| Audio Pipeline | Adaptive VAD threshold Ñ auto-calibrate based on first 500ms of audio | MEDIUM |
| QA/Testing | Benchmark suite comparing RNNoise vs passthrough on real call samples | HIGH |

**Why:** Indian accents + call center noise (AC, open floor, WFH) are the primary use case. Generic models underperform here.

### Day 9 Ñ ECC Integration
| Agent | Task | Priority |
|---|---|---|
| API Layer | Add gRPC service definition (proto + generated Go) for ECC sidecar deployment | HIGH |
| RTP/SIP | SIP-aware mode: parse SDP to auto-detect codec from offer/answer | MEDIUM |
| Post-processing | Add webhook callback on completion (for async batch processing) | MEDIUM |

**Why:** ECC integration requires gRPC (matches Exotel's internal service mesh). SDP parsing removes manual codec config.

### Day 10 Ñ Polish + v0.1.0 Release
| Agent | Task | Priority |
|---|---|---|
| QA/Testing | Full test suite pass, coverage report, race detector clean | CRITICAL |
| API Layer | CHANGELOG.md, version bump to v0.1.0, GitHub release tag | HIGH |
| All agents | Code review pass Ñ remove TODOs, add missing doc comments, go vet clean | HIGH |

**Why:** Day 10 is the v0.1.0 milestone. Everything must compile, test, and be documented.

---

## Sprint 3 Ñ ECC Productization (Days 11Ð20)
_Goal: Integrated into ECC, dogfooded on real calls, beta customer ready_

### Week 3 Focus Areas
- **Days 11Ð13:** ECC agent desktop integration (SDK embedded in softphone layer)
- **Days 14Ð16:** Real call testing Ñ process actual Exotel call recordings, measure WER improvement
- **Days 17Ð18:** Performance optimization Ñ GPU inference path, CPU profiling
- **Days 19Ð20:** Beta documentation, API reference, integration guide for ECC team

### Week 4 Focus Areas
- **Days 21Ð23:** DeepFilterNet fine-tuned on Exotel call data (if data available)
- **Days 24Ð26:** Dashboard Ñ real-time audio quality metrics per call
- **Days 27Ð28:** Load testing at production scale (10K concurrent sessions)

---

## Agent Rotation Schedule

To ensure all workstreams get attention, agents rotate weekly:

| Week | Heavy Focus | Light Touch |
|---|---|---|
| Week 1 | Audio Pipeline, AI Model, QA | RTP, File, API |
| Week 2 | RTP/SIP, Post-processing, API | Audio, Model, QA |
| Week 3 | AI Model (fine-tuning), QA | Audio, RTP, File |
| Week 4 | All agents Ñ integration & polish | Ñ |

---

## Definition of Done (v0.1.0)

- [ ] `go build ./...` passes with and without CGo
- [ ] `go test -race ./...` passes with 0 failures
- [ ] Test coverage ³ 60% across all packages
- [ ] CLI (`clearstream file`, `clearstream rtp`, `clearstream probe`) works end-to-end
- [ ] HTTP `/enhance` endpoint works with curl
- [ ] Live RTP session handles 10 concurrent streams without memory leak
- [ ] G.711, Opus, G.722 codecs verified round-trip correct
- [ ] VAD reduces CPU usage ³ 25% on silent audio
- [ ] DEVLOG up to date
- [ ] README accurate and complete
- [ ] All exported symbols have Go doc comments

---

## Blocked Items (needs human input)

| Item | Why blocked | What Saurabh needs to do |
|---|---|---|
| DeepFilterNet model | Needs ONNX export | `pip install deepfilternet && python -c "from df.enhance import init_df; m,_,_=init_df(); m.export_onnx('deepfilter.onnx')"` |
| go.sum | Incomplete | Run `go mod tidy` in ~/ClearStream |
| Fine-tuning data | Needs real call recordings | Provide 10Ð20 hours of labeled Exotel call audio |
| ECC integration | Needs ECC codebase access | Share ECC repo access with agent team |

---

## Progress Tracker

| Workstream | Day 1 | Day 2 | Day 3 | Day 4 | Day 5 | v0.1.0 |
|---|---|---|---|---|---|---|
| Audio Pipeline | 30% | 45% | 55% | 60% | 65% | 80% |
| AI Model | 35% | 35% | 40% | 55% | 55% | 70% |
| RTP/SIP | 25% | 40% | 40% | 55% | 55% | 80% |
| Post-processing | 20% | 40% | 40% | 45% | 60% | 75% |
| API Layer | 50% | 50% | 70% | 70% | 80% | 90% |
| QA/Testing | 0% | 40% | 55% | 60% | 75% | 90% |
| **Overall** | **27%** | **42%** | Ñ | Ñ | Ñ | **81%** |
