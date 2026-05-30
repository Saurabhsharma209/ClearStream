# ClearStream Ñ Business Case

**Version:** 1.0 | **Date:** 2026-05-30 | **Author:** Saurabh Sharma, Exotel

---

## Executive Summary

ClearStream is an AI-powered, codec-agnostic audio enhancement SDK that removes
background noise, echo, and artifacts from live telephony streams and recorded
audio/video files in real time. Built natively for Exotel's infrastructure, it
addresses a critical gap: Voice AI and contact center products are only as good
as the audio they receive Ñ and telephony audio today is hostile to AI.

ClearStream turns this liability into a product advantage. By cleaning audio
before it reaches STT, NLU, and voice bots, Exotel can deliver measurably
better AI accuracy without changing the AI models Ñ just the input quality.

**Ask:** 2 engineers * 4 months (~?40L all-in) to reach a shippable v1.0.
**Return:** Conservative ?8Ð12 Cr ARR within 18 months, with a long-term moat
as the only Indian contact center platform with native real-time audio enhancement.

---

## Problem

### Telephony Audio Is Uniquely Hostile to AI

| Issue | Impact on Voice AI |
|---|---|
| G.711 codec compresses to 8kHz | STT models trained on 16kHz+ audio perform poorly |
| Background noise (floors, WFH, street) | 20Ð40% higher word error rate |
| Echo from speakerphones | False barge-in triggers, missed commands |
| Network jitter and packet loss | Mid-sentence artifacts break NLU context |
| Multiple codec hops (carrier ? platform) | Cumulative quality degradation |

STT models like Whisper and Deepgram are trained on clean studio audio. When
fed noisy 8kHz telephony, word error rates jump 20Ð40%. Every downstream AI
system Ñ intent detection, sentiment analysis, bot response Ñ inherits these
errors and compounds them.

### The Cost Is Measurable

A contact center handling 100,000 calls/day with an average handle time of
4 minutes. If 15% of calls have a 30-second repeat/clarification loop caused
by AI mishearing:

- **15,000 calls * 30 seconds = 125 agent-hours wasted per day**
- At ?300/hour agent cost: **?37,500/day = ?1.3 Cr/year** per large customer
- Multiply across Exotel's enterprise customer base: the total waste is enormous

This is before counting customer frustration, CSAT impact, and escalation costs.

---

## Solution: ClearStream

ClearStream sits transparently in the audio path Ñ between the carrier RTP
stream and Exotel's AI stack Ñ and cleans audio in real time with <20ms latency.

```
Caller (noisy)
    ? G.711/Opus RTP
Exotel Media Server
    ?
[ClearStream Ñ intercepts RTP]
  ¥ Decode G.711 ? 16kHz PCM
  ¥ AI noise suppression (RNNoise / DeepFilterNet)
  ¥ Re-encode ? forward clean stream
    ?
STT ? NLU ? Voice Bot / Agent Desktop
    ?
Clean, accurate AI response
```

For recorded calls, ClearStream post-processes audio files before feeding them
to transcription, analytics, or compliance review pipelines.

### Key Technical Properties

- **Codec-agnostic:** G.711 (µ-law/A-law), G.722, G.729, Opus, AAC, MP3, FLAC
- **Dual mode:** Real-time RTP interception + post-processing of audio/video files
- **Sub-20ms latency:** Suitable for live call paths (target: <15ms)
- **Transparent:** No SIP re-INVITE required; intercepts at RTP level
- **Scalable:** Stateless per-session design; horizontal scaling with existing infra
- **Open-source AI core:** Built on RNNoise (Xiph/Mozilla) and DeepFilterNet
  (Microsoft) Ñ production-proven, no licensing cost
- **Language:** Go Ñ consistent with Exotel's existing media stack

---

## Market Opportunity

### Krisp's Proof of Market

Krisp (the closest comparable product) raised $9M Series A in 2020 and is
valued at ~$150M today. They charge $8Ð14/user/month to end users Ñ on top
of existing communication tools. Their entire value proposition is a software
layer that cleans audio. ClearStream replicates this capability natively,
eliminating the need for Exotel's customers to pay a third party.

### Exotel's Addressable Market

| Segment | Opportunity |
|---|---|
| ECC (Contact Center) customers | Bundle ClearStream as a premium add-on: ?200Ð500/agent/month |
| Platform API customers | Charge per-minute for enhanced audio: ?0.02Ð0.05/min premium |
| Voice AI / bot customers | Required for reliable bot performance; included in AI tier |
| Third-party SaaS (SDK licensing) | License ClearStream SDK to other Indian voice platforms |

**Conservative ARR estimate (18 months post-launch):**
- 500 ECC agents on enhanced plan * ?300/month = ?18L/month
- 10M enhanced minutes/month * ?0.03/min = ?30L/month
- 2 SDK licensing deals = ?50L/year
- **Total: ~?8Ð10 Cr ARR by month 18**

This is deliberately conservative Ñ it assumes no new customer wins, only
upsell to existing base.

---

## Use Cases

### 1. Voice AI & Bots (Highest ROI)
Clean audio fed to STT/NLU improves accuracy 20Ð40% on noisy telephony.
Fewer misheard words ? fewer repeat loops ? shorter calls ? lower cost.
Reduces false barge-ins. Enables reliable real-time sentiment detection.

### 2. Contact Center Agent Calls
Agents in noisy WFH or floor environments sound professional to customers.
Customers in noisy environments (street, car) are better understood by agents
and IVR systems. Reduces average handle time and repeat calls.

### 3. Post-Call Recording & Analytics
Call recordings cleaned before transcription ? higher STT accuracy ? better
compliance review, QA scoring, and coaching insights. AI analytics that
previously had 70% accuracy on raw recordings can reach 90%+ on clean audio.

### 4. IVR / DTMF / Speech Recognition
"Press 1 or say Yes" Ñ background noise causes false positives and missed
detections. Clean audio dramatically reduces IVR failure rates, a top customer
complaint.

### 5. Outbound Campaign Calls
Pre-recorded messages and TTS responses sound cleaner. Customer responses are
better understood. Conversion rates on voice campaigns improve.

### 6. Video Call Recording Cleanup
Meeting recordings, video interviews, training sessions Ñ ClearStream strips
background noise from any video file while leaving the video track untouched.

### 7. Compliance & Legal
Court-admissible recordings, regulatory compliance calls Ñ clean audio is
required. Currently many companies pay third-party transcription services to
clean audio manually. ClearStream automates this.

---

## Competitive Landscape

| Product | Approach | Limitation vs ClearStream |
|---|---|---|
| Krisp | Desktop app / SDK | Per-user SaaS pricing, not carrier-grade, no RTP interception |
| NVIDIA RTX Voice | GPU-only | Requires NVIDIA GPU, not server-deployable at scale |
| Adobe Podcast Enhance | Post-processing only | Cloud API, not real-time, data leaves customer premises |
| AWS Transcribe noise suppression | STT-only, cloud | Locked to AWS STT, not a standalone audio layer |
| Zoom / Teams built-in | App-specific | Only works within their platform |

**ClearStream's differentiation:**
- Only solution purpose-built for carrier-grade RTP interception
- Works at the infrastructure level Ñ no client-side software required
- On-premises deployable Ñ data never leaves customer environment (critical for BFSI, healthcare)
- Codec-agnostic Ñ handles the full range of telephony codecs
- Part of Exotel's platform Ñ one vendor, one contract, one support relationship

---

## Investment Required

### Phase 1: MVP (Months 1Ð4) Ñ ?40L

| Resource | Cost |
|---|---|
| 1 Senior Go engineer (media/networking) | ?20L |
| 1 ML engineer (model fine-tuning) | ?15L |
| Infrastructure (GPU for training, test environment) | ?5L |

**Deliverables:**
- Post-call recording cleanup (production-ready)
- Live RTP noise suppression (beta)
- Integration with ECC agent desktop
- Fine-tuned model on Indian English + call center noise profiles

### Phase 2: Scale (Months 5Ð8) Ñ ?25L

| Resource | Cost |
|---|---|
| 1 additional engineer (infra/scale) | ?15L |
| GPU inference optimization | ?5L |
| Sales & marketing support | ?5L |

**Deliverables:**
- HTTP API for third-party SDK licensing
- GPU inference path (10x capacity improvement)
- ECC product integration complete
- First external SDK customer

### Total 8-Month Investment: ?65L
### Break-even: Month 14 at conservative revenue projections

---

## ROI Summary

| Metric | Value |
|---|---|
| Total investment (8 months) | ?65L |
| ARR at Month 18 | ?8Ð10 Cr |
| Payback period | ~14 months |
| 3-year cumulative revenue | ?30Ð40 Cr |
| Strategic value | Defensible moat; no Indian competitor has this |

---

## Risks & Mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| Latency too high for live calls | Medium | Start with post-processing (zero latency pressure); optimize live path iteratively |
| Model quality insufficient for Indian accents | Medium | Fine-tune on Exotel's own call recordings (we have the data) |
| Customer resistance to audio processing | Low | On-premises deployment; no data leaves their environment |
| Large cloud providers add this natively | Low | They will eventually; our advantage is integration depth with telephony stack |
| Engineering takes longer than estimated | Medium | SDK foundation already built; core risk is model fine-tuning timeline |

---

## Recommendation

**Proceed with Phase 1 immediately.**

The technical foundation (ClearStream SDK) is already built and on GitHub.
The market is proven (Krisp's success). The problem is acute for Exotel's
customers and getting worse as voice AI adoption grows Ñ every new bot
deployment makes audio quality a more critical bottleneck.

The question is not whether to build this, but whether Exotel builds it
first or a competitor does.

ClearStream should ship as a beta feature of ECC within 4 months and be
generally available within 8 months.

---

## Appendix: Technical Architecture

See: https://github.com/Saurabhsharma209/ClearStream

The SDK is written in Go, uses FFmpeg for codec handling, RNNoise/DeepFilterNet
for AI suppression, and integrates at the RTP layer for live calls. No changes
to existing SIP signaling are required. The suppression engine runs as a
transparent media proxy between Exotel's media server and the AI pipeline.
